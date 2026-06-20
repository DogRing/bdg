// Package needs owns the immutable Registry of need/value Dimension definitions
// and the pure forward-roll helpers that project a consumable need's satisfaction
// level over time. The registry is assembled from TWO injected sources (D10):
// content/needs.yaml (catalog) and content/balance.yaml (per-need rate constants).
//
// This package imports no "os" or filesystem package — both inputs are injected
// io.Readers (architecture §1: engine is IO-free). Structural JSON-schema validation
// is NOT done here — that is platform/config's job.
package needs

import (
	"fmt"
	"io"
	"math"
	"sort"

	"github.com/dogring/bdg/engine/core"
	"gopkg.in/yaml.v3"
)

// ── NeedID alias ──────────────────────────────────────────────────────────────

// NeedID names a need / value Dimension (glossary §"Values & goals" Dimension:
// Satiety, Hydration, Rest, Safety, Standing, Openness, …). It is an ALIAS of the
// canonical core.Dimension type, so a NeedID and a values-module Dimension are the
// exact same type — no conversion, no naming drift.
type NeedID = core.Dimension

// ── Enums ─────────────────────────────────────────────────────────────────────

// Kind classifies how a need's level changes over time (content/needs.yaml kind).
type Kind uint8

const (
	Consumable  Kind = iota // level decays by time at Rate; planner forward-rolls + provisions.
	Conditional             // not time-driven; set by world/social state; Rate == 0.
)

// Posture is the goal posture for a Value on this dimension (glossary: Posture).
type Posture uint8

const (
	Maximize      Posture = iota // push as high as possible (Standing, Openness)
	MaintainAbove                // keep at/above setpoint (Satiety, Hydration, Rest)
	PreventBelow                 // act when threatened to fall below setpoint (Safety)
)

// SalienceCurve names how a need's gap maps to momentary salience.
type SalienceCurve uint8

const (
	Deficit  SalienceCurve = iota // salience ~ max(0, setpoint - level)
	GapToMax                      // salience ~ (1 - level)
)

// ── Def (one immutable need/value dimension definition) ───────────────────────

// Def is one immutable need/value Dimension definition. Catalog fields (Kind, Posture,
// Referent, Curve, Gain, Setpoint default) come from content/needs.yaml; rate fields
// (Rate, Threshold) come from content/balance.yaml's needs:<id> block for consumable needs.
type Def struct {
	ID        NeedID            // canonical Dimension id (docs/glossary.md)
	Kind      Kind              // consumable | conditional
	Rate      float64           // satisfaction lost per tick (= per game-minute), D9.
	Threshold float64           // satisfaction_threshold (setpoint) in [0,1].
	Posture   Posture           // default goal posture for a fresh Value on this dimension
	Setpoint  float64           // default setpoint in [0,1] (needs.yaml default.setpoint)
	Referent  core.ReferentKind // default referent kind (Self | Other | Place | Collective)
	Curve     SalienceCurve     // salience curve
	Gain      float64           // salience gain (>= 0)
}

// ── Registry (immutable, read-only set of dimension definitions) ──────────────

// Registry is the immutable, read-only set of Dimension definitions. After Load it
// never changes (no setters, no exported mutable fields). Safe to share across
// goroutines.
type Registry struct {
	defs  map[NeedID]Def
	ids   []NeedID
	kinds map[Kind][]NeedID
}

// Load parses the dimension catalog from needsDoc (the bytes of content/needs.yaml)
// and the per-need rate constants from balanceDoc (the bytes of content/balance.yaml,
// from which only the top-level `needs:` block is read). BOTH readers are injected by
// platform/config — the engine opens NO file and hardcodes NO path (D10).
//
// Load MERGES the two: a consumable dimension's Rate/Threshold are taken from
// balanceDoc's needs:<id> entry; its catalog metadata from needsDoc. It performs
// SEMANTIC validation and returns an error describing the FIRST violation.
func Load(needsDoc, balanceDoc io.Reader) (*Registry, error) {
	// 1. Parse the catalog.
	catalog, err := parseNeedsCatalog(needsDoc)
	if err != nil {
		return nil, err
	}

	// 2. Parse the balance needs block.
	balanceNeeds, err := parseBalanceNeeds(balanceDoc)
	if err != nil {
		return nil, err
	}

	// 3. Merge: build Defs from catalog, overlay rate/threshold from balance.
	defs := make(map[NeedID]Def, len(catalog))
	ids := make([]NeedID, 0, len(catalog))

	for _, cat := range catalog {
		id := NeedID(cat.ID)
		d := Def{
			ID:       id,
			Kind:     cat.Kind,
			Posture:  cat.Posture,
			Setpoint: cat.Setpoint,
			Referent: cat.Referent,
			Curve:    cat.Curve,
			Gain:     cat.Gain,
		}

		if cat.Kind == Consumable {
			rate, hasRate := balanceNeeds[string(core.Dimension(id))]
			if !hasRate {
				return nil, fmt.Errorf("needs.Load: consumable need %q has no matching entry in balance.yaml needs: block", id)
			}
			if rate.DecayPerTick <= 0 {
				return nil, fmt.Errorf("needs.Load: consumable need %q has decay_per_tick <= 0 (%v)", id, rate.DecayPerTick)
			}
			d.Rate = rate.DecayPerTick
			d.Threshold = rate.Threshold
			// For consumable needs, Setpoint equals Threshold.
			d.Setpoint = rate.Threshold
		} else {
			d.Rate = 0
			d.Threshold = cat.Setpoint
			// Ensure conditional needs are NOT in balance.
			if _, hasBalance := balanceNeeds[string(core.Dimension(id))]; hasBalance {
				return nil, fmt.Errorf("needs.Load: conditional need %q must NOT appear in balance.yaml needs: block", id)
			}
		}

		defs[id] = d
		ids = append(ids, id)
	}

	// 4. Check that every balance needs: key names a known consumable dimension
	// (would have been caught above as "no matching entry", but a balance key
	// referencing an entirely unknown dimension also needs an error).
	for balID := range balanceNeeds {
		if _, exists := defs[NeedID(balID)]; !exists {
			return nil, fmt.Errorf("needs.Load: balance.yaml needs: key %q does not name a dimension defined in needs.yaml", balID)
		}
	}

	// 5. Sort ids lexicographically for deterministic ordering (D12).
	sort.Slice(ids, func(i, j int) bool {
		return string(ids[i]) < string(ids[j])
	})

	// 6. Build kind-index.
	kinds := map[Kind][]NeedID{
		Consumable:  {},
		Conditional: {},
	}
	for _, id := range ids {
		kind := defs[id].Kind
		kinds[kind] = append(kinds[kind], id)
	}

	return &Registry{
		defs:  defs,
		ids:   ids,
		kinds: kinds,
	}, nil
}

// IDs returns ALL need ids in canonical fixed order: sorted lexicographically by
// NeedID. This is the ONE ordering every consumer uses to iterate needs (D12).
// The returned slice is a copy; identical across calls and across processes for
// the same content.
func (reg *Registry) IDs() []NeedID {
	c := make([]NeedID, len(reg.ids))
	copy(c, reg.ids)
	return c
}

// Def returns the definition for id and whether it exists.
func (reg *Registry) Def(id NeedID) (Def, bool) {
	d, ok := reg.defs[id]
	return d, ok
}

// Has reports whether id is a known need. Used to reject unknown NeedIDs referenced
// elsewhere.
func (reg *Registry) Has(id NeedID) bool {
	_, ok := reg.defs[id]
	return ok
}

// Len returns the number of defined needs.
func (reg *Registry) Len() int {
	return len(reg.ids)
}

// Kinds returns the lexicographically-sorted ids of all needs of the given Kind
// (e.g. the consumable needs the planner must forward-roll).
func (reg *Registry) Kinds(k Kind) []NeedID {
	sl, ok := reg.kinds[k]
	if !ok {
		return nil
	}
	c := make([]NeedID, len(sl))
	copy(c, sl)
	return c
}

// ── Pure forward-roll helpers (D9: demand is DERIVED, never authored) ──────────

// Level returns the satisfaction level after `minutes` of pure decay from `level0`,
// clamped to [0,1]: level0 - Rate*minutes for a Consumable; level0 unchanged for a
// Conditional (rate 0).
func (d Def) Level(level0 float64, minutes core.GameMinutes) float64 {
	if d.Kind == Conditional {
		return level0
	}
	decay := d.Rate * float64(minutes)
	v := level0 - decay
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// Demand returns the predicted shortfall over a horizon: rate * predicted-time
// (glossary: "Demand = need-rate * predicted-time"). This is the value the planner
// uses to size a provisioning subgoal; it is computed here, NEVER stored on an
// object (D9). For a Conditional need, Demand is always 0.
func (d Def) Demand(minutes core.GameMinutes) float64 {
	return d.Rate * float64(minutes)
}

// BreachAt returns the game-minute offset at which a Consumable's level would first
// cross below `setpoint` given `level0`, and ok=false if it never does within the
// horizon or for a Conditional need. The planner uses this to decide WHEN
// provisioning is needed (forward-sim); it does not insert the subgoal — that is
// the planner.
func (d Def) BreachAt(level0, setpoint float64, horizon core.GameMinutes) (at core.GameMinutes, ok bool) {
	if d.Kind == Conditional || d.Rate == 0 {
		return 0, false
	}
	if level0 <= setpoint {
		// Already below setpoint — breach at time 0.
		return 0, true
	}
	// level0 - Rate * t < setpoint  =>  t > (level0 - setpoint) / Rate
	breachMinutes := (level0 - setpoint) / d.Rate
	bm := int64(math.Ceil(breachMinutes))
	if bm < 0 {
		bm = 0
	}
	if core.GameMinutes(bm) > horizon {
		return 0, false
	}
	return core.GameMinutes(bm), true
}

// Salience returns the momentary salience of this dimension given the current
// level and setpoint, per the dimension's Curve and Gain. Bounded >= 0.
func (d Def) Salience(level, setpoint float64) float64 {
	var raw float64
	switch d.Curve {
	case Deficit:
		raw = setpoint - level
	case GapToMax:
		raw = 1 - level
	}
	s := raw * d.Gain
	if s < 0 {
		return 0
	}
	return s
}

// ── YAML intermediate types ──────────────────────────────────────────────────

// rawNeed is one entry in content/needs.yaml's needs array.
type rawNeed struct {
	ID      string        `yaml:"id"`
	Kind    string        `yaml:"kind"`
	Default rawDefault    `yaml:"default"`
	Salience rawSalience  `yaml:"salience"`
}

type rawDefault struct {
	Posture  string  `yaml:"posture"`
	Setpoint float64 `yaml:"setpoint"`
	Referent string  `yaml:"referent"`
}

type rawSalience struct {
	Curve string  `yaml:"curve"`
	Gain  float64 `yaml:"gain"`
}

type rawNeedsDocument struct {
	SchemaVersion int       `yaml:"schema_version"`
	Needs         []rawNeed `yaml:"needs"`
}

// rawBalanceNeeds maps the top-level needs: key in balance.yaml.
type rawBalanceDocument struct {
	Needs map[string]rawBalanceNeed `yaml:"needs"`
}

type rawBalanceNeed struct {
	DecayPerTick      float64 `yaml:"decay_per_tick"`
	SatisfactionThreshold float64 `yaml:"satisfaction_threshold"`
}

// parsed catalog entry (after kind/posture/curve/referent string -> enum conversion).
type catalogEntry struct {
	ID       string
	Kind     Kind
	Posture  Posture
	Setpoint float64
	Referent core.ReferentKind
	Curve    SalienceCurve
	Gain     float64
}

// balanceEntry for merging.
type balanceEntry struct {
	DecayPerTick float64
	Threshold    float64
}

// ── Parsers ───────────────────────────────────────────────────────────────────

// string→enum maps.
var kindNames = map[string]Kind{
	"consumable":  Consumable,
	"conditional": Conditional,
}

var postureNames = map[string]Posture{
	"Maximize":      Maximize,
	"MaintainAbove": MaintainAbove,
	"PreventBelow":  PreventBelow,
}

var referentNames = map[string]core.ReferentKind{
	"Self":        core.Self,
	"Other":       core.Other,
	"Place":       core.Place,
	"Collective":  core.Collective,
}

var curveNames = map[string]SalienceCurve{
	"deficit":    Deficit,
	"gap_to_max": GapToMax,
}

func parseNeedsCatalog(r io.Reader) ([]catalogEntry, error) {
	var doc rawNeedsDocument
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("needs.Load: needs.yaml decode: %w", err)
	}

	if len(doc.Needs) == 0 {
		return nil, fmt.Errorf("needs.Load: needs list is empty; at least one need required")
	}

	seen := make(map[string]bool)
	entries := make([]catalogEntry, 0, len(doc.Needs))

	for i, rn := range doc.Needs {
		// Validate id non-empty.
		if rn.ID == "" {
			return nil, fmt.Errorf("needs.Load: entry %d: empty id", i)
		}

		// Check duplicate.
		if seen[rn.ID] {
			return nil, fmt.Errorf("needs.Load: duplicate need id %q at entry %d", rn.ID, i)
		}
		seen[rn.ID] = true

		// Parse kind.
		kind, ok := kindNames[rn.Kind]
		if !ok {
			return nil, fmt.Errorf("needs.Load: entry %d (%q): invalid kind %q; must be %q or %q",
				i, rn.ID, rn.Kind, "consumable", "conditional")
		}

		// Parse posture.
		posture, ok := postureNames[rn.Default.Posture]
		if !ok {
			return nil, fmt.Errorf("needs.Load: entry %d (%q): invalid posture %q",
				i, rn.ID, rn.Default.Posture)
		}

		// Validate setpoint.
		if rn.Default.Setpoint < 0 || rn.Default.Setpoint > 1 {
			return nil, fmt.Errorf("needs.Load: entry %d (%q): setpoint %.4f outside [0,1]",
				i, rn.ID, rn.Default.Setpoint)
		}

		// Parse referent.
		referent, ok := referentNames[rn.Default.Referent]
		if !ok {
			return nil, fmt.Errorf("needs.Load: entry %d (%q): invalid referent %q",
				i, rn.ID, rn.Default.Referent)
		}

		// Parse curve.
		curve, ok := curveNames[rn.Salience.Curve]
		if !ok {
			return nil, fmt.Errorf("needs.Load: entry %d (%q): invalid salience curve %q",
				i, rn.ID, rn.Salience.Curve)
		}

		// Validate gain.
		if rn.Salience.Gain < 0 {
			return nil, fmt.Errorf("needs.Load: entry %d (%q): salience gain %.4f is negative",
				i, rn.ID, rn.Salience.Gain)
		}

		entries = append(entries, catalogEntry{
			ID:       rn.ID,
			Kind:     kind,
			Posture:  posture,
			Setpoint: rn.Default.Setpoint,
			Referent: referent,
			Curve:    curve,
			Gain:     rn.Salience.Gain,
		})
	}

	return entries, nil
}

func parseBalanceNeeds(r io.Reader) (map[string]balanceEntry, error) {
	var doc rawBalanceDocument
	dec := yaml.NewDecoder(r)
	// Do NOT use KnownFields here — the balance.yaml contains many top-level keys
	// (world, perception, generation, ...) that we don't parse; we only need the
	// `needs:` block. KnownFields would reject the extra keys.
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("needs.Load: balance.yaml decode: %w", err)
	}

	if len(doc.Needs) == 0 {
		return nil, fmt.Errorf("needs.Load: balance.yaml needs: block is empty or missing; at least one consumable need entry required")
	}

	result := make(map[string]balanceEntry, len(doc.Needs))
	for key, bn := range doc.Needs {
		if bn.DecayPerTick <= 0 {
			return nil, fmt.Errorf("needs.Load: balance.yaml needs:%s: decay_per_tick must be > 0, got %v", key, bn.DecayPerTick)
		}
		if bn.SatisfactionThreshold < 0 || bn.SatisfactionThreshold > 1 {
			return nil, fmt.Errorf("needs.Load: balance.yaml needs:%s: satisfaction_threshold %.4f outside [0,1]", key, bn.SatisfactionThreshold)
		}
		result[key] = balanceEntry{
			DecayPerTick: bn.DecayPerTick,
			Threshold:    bn.SatisfactionThreshold,
		}
	}

	return result, nil
}
