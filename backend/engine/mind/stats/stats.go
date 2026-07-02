// Package stats owns the open stat vector (Stats = map[StatID]float64) and the
// immutable Registry of stat definitions built from content/stats.yaml (D10:
// stats are data, not code).
//
// The registry is the single authority on which StatIDs exist, their [min, max]
// range, default value, generation distribution, and capability/disposition kind.
// Every other engine module reads stat metadata through this registry — there are
// no hardcoded stat names anywhere in engine code (D7: competence is a composition
// of these base attributes, never a per-skill field).
package stats

import (
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"

	"github.com/dogring/bdg/engine/kernel/core"
	"gopkg.in/yaml.v3"
)

// ── The stat vector ──────────────────────────────────────────────────────────

// Stats is the open per-agent stat vector (glossary: "Open stat vector").
// It is NOT a fixed struct — adding a stat is a content edit, not a code edit (D7/D10).
// The same shape backs Real Stats and every ToM[X] belief (data-contracts §1 real_stats).
//
// Stats is a map for storage/lookup ONLY. Per D12, code MUST NOT drive logic by ranging
// over it; iterate via Registry.IDs() (canonical sorted order) instead.
type Stats map[core.StatID]float64

// Clone returns a deep copy (maps are reference types; the apply phase must not alias).
func (s Stats) Clone() Stats {
	if s == nil {
		return nil
	}
	c := make(Stats, len(s))
	for k, v := range s {
		c[k] = v
	}
	return c
}

// Get returns the value for id (0 if absent — callers should pre-fill via Registry.Defaults).
func (s Stats) Get(id core.StatID) float64 {
	return s[id]
}

// ── Stat definitions ──────────────────────────────────────────────────────────

// Kind classifies a stat (glossary §"Capability vs disposition").
type Kind uint8

const (
	Capability  Kind = iota // Strength, Agility, Intelligence — gates/outcomes/prediction read these
	Disposition             // value-weighting axes (Aggression, Honesty, Greed, …)
)

// kindNames maps YAML kind strings to Kind constants.
var kindNames = map[string]Kind{
	"capability":  Capability,
	"disposition": Disposition,
}

// Def is one immutable stat definition (mirrors a content/stats.yaml entry, after
// platform/config has validated that file against content/schema/stats.schema.json).
type Def struct {
	ID      core.StatID // canonical identifier (docs/glossary.md §StatID)
	Label   string      // human-readable name; UI-only, IGNORED by engine logic (see Notes).
	Kind    Kind        // capability | disposition
	Min     float64     // inclusive lower bound (range[0])
	Max     float64     // inclusive upper bound (range[1])
	Default float64     // fallback value when Gen is absent; Min ≤ Default ≤ Max (see Notes)
	Gen     GenSpec     // agent-generation distribution; takes precedence over Default (see Notes)
	Inherit float64     // parent-inheritance weight in [0,1]
}

// GenSpec is the generation distribution for a stat (content/stats.yaml gen.*).
type GenSpec struct {
	Dist string // "normal" | "uniform"
	Mean float64
	SD   float64 // ≥ 0
}

// Clamp returns v constrained to [Min, Max].
func (d Def) Clamp(v float64) float64 {
	if v < d.Min {
		return d.Min
	}
	if v > d.Max {
		return d.Max
	}
	return v
}

// ── YAML intermediate types ──────────────────────────────────────────────────

// rawStat is the on-disk shape for one stat entry (content/stats.yaml stats[*]).
type rawStat struct {
	ID      string    `yaml:"id"`
	Label   string    `yaml:"label,omitempty"`
	Kind    string    `yaml:"kind"`
	Range   []float64 `yaml:"range"`
	Default *float64  `yaml:"default,omitempty"`
	Gen     *rawGen   `yaml:"gen"`
	Inherit float64   `yaml:"inherit"`
}

type rawGen struct {
	Dist string  `yaml:"dist"`
	Mean float64 `yaml:"mean"`
	SD   float64 `yaml:"sd"`
}

type rawDocument struct {
	SchemaVersion int       `yaml:"schema_version"`
	Stats         []rawStat `yaml:"stats"`
}

// ── Validation ───────────────────────────────────────────────────────────────

// statIDPattern matches canonical StatIDs: start with a letter, then letters/digits/underscores.
var statIDPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// ── The registry ──────────────────────────────────────────────────────────────

// Registry is the immutable, read-only set of stat definitions. After Load it never
// changes (no setters, no exported mutable fields). Shared freely across goroutines
// in the read/plan phase.
type Registry struct {
	defs  map[core.StatID]Def
	ids   []core.StatID
	kinds map[Kind][]core.StatID
}

// Load parses the stats document from r (the bytes of content/stats.yaml — the path is
// injected by platform/config, NEVER a file path in engine/stats, keeping the engine
// IO-free) and builds an immutable Registry. It performs SEMANTIC validation (see
// Invariants) and returns an error describing the first violation. STRUCTURAL JSON-schema
// validation (content/schema/stats.schema.json) is NOT done here — it is platform/config's
// responsibility, run before this call (see that SPEC).
func Load(r io.Reader) (*Registry, error) {
	var doc rawDocument
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("stats.Load: yaml decode: %w", err)
	}

	if len(doc.Stats) == 0 {
		return nil, fmt.Errorf("stats.Load: stats list is empty; at least one stat required")
	}

	defs := make(map[core.StatID]Def, len(doc.Stats))
	ids := make([]core.StatID, 0, len(doc.Stats))

	for i, rs := range doc.Stats {
		// 1. Validate id pattern.
		if !statIDPattern.MatchString(rs.ID) {
			return nil, fmt.Errorf("stats.Load: entry %d: invalid stat id %q: must match %s", i, rs.ID, statIDPattern)
		}

		id := core.StatID(rs.ID)

		// 2. Check for duplicate id.
		if _, exists := defs[id]; exists {
			return nil, fmt.Errorf("stats.Load: duplicate stat id %q at entry %d", rs.ID, i)
		}

		// 3. Validate kind.
		kind, ok := kindNames[rs.Kind]
		if !ok {
			return nil, fmt.Errorf("stats.Load: entry %d (%q): invalid kind %q; must be %q or %q", i, rs.ID, rs.Kind, "capability", "disposition")
		}

		// 4. Validate range.
		if len(rs.Range) != 2 {
			return nil, fmt.Errorf("stats.Load: entry %d (%q): range must have exactly 2 elements, got %d", i, rs.ID, len(rs.Range))
		}
		min, max := rs.Range[0], rs.Range[1]
		if min > max {
			return nil, fmt.Errorf("stats.Load: entry %d (%q): min (%v) > max (%v)", i, rs.ID, min, max)
		}

		// 5. Determine default. If omitted, use range midpoint.
		defaultVal := (min + max) / 2.0
		if rs.Default != nil {
			defaultVal = *rs.Default
		}
		if defaultVal < min || defaultVal > max {
			return nil, fmt.Errorf("stats.Load: entry %d (%q): default %v outside [%v, %v]", i, rs.ID, defaultVal, min, max)
		}

		// 6. Validate inherit ∈ [0,1].
		if rs.Inherit < 0 || rs.Inherit > 1 {
			return nil, fmt.Errorf("stats.Load: entry %d (%q): inherit %v outside [0, 1]", i, rs.ID, rs.Inherit)
		}

		// 7. Validate gen.
		if rs.Gen == nil {
			return nil, fmt.Errorf("stats.Load: entry %d (%q): gen is required", i, rs.ID)
		}
		if rs.Gen.SD < 0 {
			return nil, fmt.Errorf("stats.Load: entry %d (%q): gen.sd %v is negative", i, rs.ID, rs.Gen.SD)
		}
		if rs.Gen.Dist != "normal" && rs.Gen.Dist != "uniform" {
			return nil, fmt.Errorf("stats.Load: entry %d (%q): gen.dist %q must be %q or %q", i, rs.ID, rs.Gen.Dist, "normal", "uniform")
		}

		// 8. Build label.
		label := rs.Label
		if label == "" {
			label = rs.ID
		}

		defs[id] = Def{
			ID:      id,
			Label:   label,
			Kind:    kind,
			Min:     min,
			Max:     max,
			Default: defaultVal,
			Gen: GenSpec{
				Dist: rs.Gen.Dist,
				Mean: rs.Gen.Mean,
				SD:   rs.Gen.SD,
			},
			Inherit: rs.Inherit,
		}
		ids = append(ids, id)
	}

	// Sort ids lexicographically for deterministic ordering (D12).
	sort.Slice(ids, func(i, j int) bool {
		return string(ids[i]) < string(ids[j])
	})

	// Build kind-index.
	kinds := map[Kind][]core.StatID{
		Capability:  {},
		Disposition: {},
	}
	for _, id := range ids {
		kind := defs[id].Kind
		kinds[kind] = append(kinds[kind], id)
	}
	// kind slices are already built from the sorted ids, so they are also sorted.

	return &Registry{
		defs:  defs,
		ids:   ids,
		kinds: kinds,
	}, nil
}

// IDs returns ALL stat ids in the canonical, fixed order: sorted lexicographically by
// StatID. This is the ONE ordering every consumer must use to iterate stats (D12).
// The returned slice is a copy; callers may retain it. Identical across calls and across
// processes for the same content/stats.yaml.
func (reg *Registry) IDs() []core.StatID {
	c := make([]core.StatID, len(reg.ids))
	copy(c, reg.ids)
	return c
}

// Def returns the definition for id and whether it exists.
func (reg *Registry) Def(id core.StatID) (Def, bool) {
	d, ok := reg.defs[id]
	return d, ok
}

// Has reports whether id is a known stat. Used to reject unknown StatIDs referenced
// elsewhere (the planner / gates / config referential-integrity check).
func (reg *Registry) Has(id core.StatID) bool {
	_, ok := reg.defs[id]
	return ok
}

// Len returns the number of defined stats.
func (reg *Registry) Len() int {
	return len(reg.ids)
}

// Kinds returns the lexicographically-sorted ids of all stats of the given Kind (e.g. the
// three Capability stats), so consumers compose competence without naming stats in code (D7).
func (reg *Registry) Kinds(k Kind) []core.StatID {
	sl, ok := reg.kinds[k]
	if !ok {
		return nil
	}
	c := make([]core.StatID, len(sl))
	copy(c, sl)
	return c
}

// Defaults returns a fresh Stats pre-filled with every stat's Default value.
func (reg *Registry) Defaults() Stats {
	s := make(Stats, len(reg.ids))
	for _, id := range reg.ids {
		s[id] = reg.defs[id].Default
	}
	return s
}

// Clamp returns s with every value constrained to its Def range; ids absent from the
// registry are dropped (rejects unknown stats sneaking into a vector). Iterates s via
// IDs() order, not map order (D12). Known stats not present in s are omitted from the output.
func (reg *Registry) Clamp(s Stats) Stats {
	out := make(Stats, len(s))
	for _, id := range reg.ids {
		if val, ok := s[id]; ok {
			d := reg.defs[id]
			out[id] = math.Round(d.Clamp(val)*1e12) / 1e12 // truncate float noise
		}
	}
	return out
}
