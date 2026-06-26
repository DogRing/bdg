// Package actions owns the immutable Registry of atomic ActionDefs loaded from
// content/actions.yaml (D10: actions are data, not code). Each action is atomic and
// durative (D3: no task trees, methods, or plans); it carries Tags (D4: no per-action
// gate or cost field), Duration, optional Effect/EffectPerMinute, Target kind, and the
// produces/requires predicates for GOAP backward-chaining. The Producers reverse index
// (Pred -> []ActionID) is built at Load and is the only relational structure.
//
// This module knows WHAT an action is, never WHEN to do it (D5).
package actions

import (
	"fmt"
	"io"
	"sort"

	"github.com/dogring/bdg/engine/kernel/core"
	"gopkg.in/yaml.v3"
)

// ActionID names an atomic action (canonical id from content/actions.yaml, e.g. "Forage").
type ActionID string

// TargetKind classifies what an action acts on. It is DERIVED at Load from the action's
// content shape (target_kind / the at_target|near_other predicates), so the YAML stays in
// its existing form -- no new schema field.
type TargetKind uint8

const (
	TargetNone     TargetKind = iota // no target (Rest, Sleep)
	TargetLocation                   // a Vec2 destination (MoveTo; binds via at_target)
	TargetObject                     // an objects.yaml object_kind (Forage->berry_bush, Craft->tool_bench, ...)
	TargetAgent                      // another agent (Signal, Attack; binds via near_other)
)

// Effect is a per-Dimension need delta (glossary: Effect = supply). Keys are need ids
// (core.Dimension); the value is the satisfaction delta applied to that need. Reuses the
// same shape as content/objects.yaml `supply` so a consumed item's supply and an action's
// direct effect are interchangeable to the planner (D9).
type Effect map[core.Dimension]float64

// ActionDef is one immutable atomic action (glossary: Action{Tags, Produces, Duration, Effect}).
// Mirrors a content/actions.yaml entry after platform/config has validated it against
// content/schema/actions.schema.json. All fields are read-only after Load.
type ActionDef struct {
	ID       ActionID       // canonical identifier
	Tags     []core.Tag     // drives gate visibility (engine/gates) + planner cost (D4)
	Duration core.GameMinutes // BASE durative length in game-minutes (>= 1)

	Target       TargetKind // none | location | object | agent (derived; see Notes)
	TargetKindID core.Tag   // objects.yaml object_kind id when Target == TargetObject; empty otherwise

	Requires    []core.Pred // preconditions that must hold to START (ALL must hold)
	RequiresAny []core.Pred // alt preconditions (ANY satisfies the start gate)
	Produces    []core.Pred // predicates this action makes true on completion (GOAP forward)

	ProducesItem core.Tag // item kind placed into inventory on completion (empty if none)
	ConsumesItem core.Tag // item kind removed; its supply becomes the Effect on completion (D9)

	Effect          Effect // direct need deltas applied ON COMPLETION (empty for consumption acts)
	EffectPerMinute Effect // need deltas accrued EACH game-minute while running (durative supply)

	Interruptible bool // durative actions are interruptible unless explicitly false
}

// Registry is the immutable, read-only set of action definitions plus the GOAP reverse index.
// After Load it never changes (no setters, no exported mutable fields). Safe to share across
// goroutines in the read/plan phase.
type Registry struct {
	defs      map[ActionID]ActionDef
	sortedIDs []ActionID
	producers map[core.Pred][]ActionID // Pred -> sorted []ActionID
	allTags   []core.Tag              // sorted union of all tags across all actions
}

// ── Internal YAML shape ────────────────────────────────────────────────────────

// rawAction mirrors the YAML structure for one action entry.
type rawAction struct {
	ID              string              `yaml:"id"`
	Tags            []string            `yaml:"tags"`
	Duration        int                 `yaml:"duration"`
	TargetKind      string              `yaml:"target_kind,omitempty"`
	Requires        []string            `yaml:"requires,omitempty"`
	RequiresAny     []string            `yaml:"requires_any,omitempty"`
	Produces        []string            `yaml:"produces,omitempty"`
	ProducesItem    string              `yaml:"produces_item,omitempty"`
	ConsumesItem    string              `yaml:"consumes_item,omitempty"`
	Effect          map[string]float64  `yaml:"effect,omitempty"`
	EffectPerMinute map[string]float64  `yaml:"effect_per_minute,omitempty"`
	Interruptible   *bool               `yaml:"interruptible,omitempty"`
}

// rawActionsDoc matches the top-level YAML document.
type rawActionsDoc struct {
	SchemaVersion int          `yaml:"schema_version"`
	Actions       []rawAction  `yaml:"actions"`
}

// ── Public API ─────────────────────────────────────────────────────────────────

// Load parses the actions document from r (the bytes of content/actions.yaml) and builds
// an immutable Registry plus its Producers reverse index. It performs SEMANTIC validation
// (see Invariants in SPEC.md) and returns an error describing the FIRST violation.
func Load(r io.Reader) (*Registry, error) {
	var doc rawActionsDoc
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("actions: yaml decode: %w", err)
	}

	if len(doc.Actions) == 0 {
		return nil, fmt.Errorf("actions: no actions defined")
	}

	reg := &Registry{
		defs:      make(map[ActionID]ActionDef, len(doc.Actions)),
		producers: make(map[core.Pred][]ActionID),
	}

	seen := make(map[ActionID]bool, len(doc.Actions))

	for _, ra := range doc.Actions {
		id := ActionID(ra.ID)

		// Duplicate check.
		if seen[id] {
			return nil, fmt.Errorf("actions: duplicate action id %q", id)
		}
		seen[id] = true

		// Semantic: empty tags.
		if len(ra.Tags) == 0 {
			return nil, fmt.Errorf("actions: action %q has empty tags", id)
		}

		// Semantic: duration < 1.
		if ra.Duration < 1 {
			return nil, fmt.Errorf("actions: action %q has duration %d < 1", id, ra.Duration)
		}

		// Interruptible defaults to true.
		interruptible := true
		if ra.Interruptible != nil {
			interruptible = *ra.Interruptible
		}

		// Build typed slices.
		tags := make([]core.Tag, len(ra.Tags))
		for i, t := range ra.Tags {
			tags[i] = core.Tag(t)
		}

		requires := make([]core.Pred, len(ra.Requires))
		for i, p := range ra.Requires {
			requires[i] = core.Pred(p)
		}

		requiresAny := make([]core.Pred, len(ra.RequiresAny))
		for i, p := range ra.RequiresAny {
			requiresAny[i] = core.Pred(p)
		}

		produces := make([]core.Pred, len(ra.Produces))
		for i, p := range ra.Produces {
			produces[i] = core.Pred(p)
		}

		// Semantic: consumption action must not have non-empty direct Effect (D9).
		if ra.ConsumesItem != "" && len(ra.Effect) > 0 {
			return nil, fmt.Errorf("actions: action %q has both consumes_item and non-empty effect (D9 conflict)", id)
		}

		// Target kind derivation (no new schema field).
		target, targetKindID := deriveTargetKind(ra.TargetKind, produces, requires)

		// Effect and EffectPerMinute.
		effect := make(Effect, len(ra.Effect))
		for k, v := range ra.Effect {
			effect[core.Dimension(k)] = v
		}
		effectPerMinute := make(Effect, len(ra.EffectPerMinute))
		for k, v := range ra.EffectPerMinute {
			effectPerMinute[core.Dimension(k)] = v
		}

		def := ActionDef{
			ID:              id,
			Tags:            tags,
			Duration:        core.GameMinutes(ra.Duration),
			Target:          target,
			TargetKindID:    targetKindID,
			Requires:        requires,
			RequiresAny:     requiresAny,
			Produces:        produces,
			ProducesItem:    core.Tag(ra.ProducesItem),
			ConsumesItem:    core.Tag(ra.ConsumesItem),
			Effect:          effect,
			EffectPerMinute: effectPerMinute,
			Interruptible:   interruptible,
		}
		reg.defs[id] = def
	}

	// Build sorted ID list.
	reg.sortedIDs = make([]ActionID, 0, len(reg.defs))
	for id := range reg.defs {
		reg.sortedIDs = append(reg.sortedIDs, id)
	}
	sort.Slice(reg.sortedIDs, func(i, j int) bool {
		return reg.sortedIDs[i] < reg.sortedIDs[j]
	})

	// Build Producers reverse index (Pred -> []ActionID, in IDs() order).
	for _, id := range reg.sortedIDs {
		def := reg.defs[id]
		for _, pred := range def.Produces {
			reg.producers[pred] = append(reg.producers[pred], id)
		}
	}

	// Build sorted union of all tags.
	tagSet := make(map[core.Tag]bool)
	for _, def := range reg.defs {
		for _, t := range def.Tags {
			tagSet[t] = true
		}
	}
	reg.allTags = make([]core.Tag, 0, len(tagSet))
	for t := range tagSet {
		reg.allTags = append(reg.allTags, t)
	}
	sort.Slice(reg.allTags, func(i, j int) bool {
		return reg.allTags[i] < reg.allTags[j]
	})

	return reg, nil
}

// Get returns a deep copy of the definition for id and whether it exists.
// The returned ActionDef owns its own slice/map memory; mutating it does not
// affect the registry.
func (reg *Registry) Get(id ActionID) (ActionDef, bool) {
	def, ok := reg.defs[id]
	if !ok {
		return def, false
	}
	// Deep-copy all slice and map fields so the caller cannot mutate registry state.
	def.Tags = copySlice(def.Tags)
	def.Requires = copySlice(def.Requires)
	def.RequiresAny = copySlice(def.RequiresAny)
	def.Produces = copySlice(def.Produces)
	def.Effect = copyMap(def.Effect)
	def.EffectPerMinute = copyMap(def.EffectPerMinute)
	return def, true
}

// IDs returns ALL action ids in canonical fixed order: sorted lexicographically by ActionID.
// The returned slice is a copy; identical across calls and across processes for the same content.
func (reg *Registry) IDs() []ActionID {
	out := make([]ActionID, len(reg.sortedIDs))
	copy(out, reg.sortedIDs)
	return out
}

// Has reports whether id is a known action.
func (reg *Registry) Has(id ActionID) bool {
	_, ok := reg.defs[id]
	return ok
}

// Len returns the number of defined actions.
func (reg *Registry) Len() int {
	return len(reg.defs)
}

// Producers returns the actions whose Produces includes pred, in IDs() order.
// Returns nil for an unproduced predicate. The slice is a copy.
func (reg *Registry) Producers(pred core.Pred) []ActionID {
	ids, ok := reg.producers[pred]
	if !ok {
		return nil
	}
	out := make([]ActionID, len(ids))
	copy(out, ids)
	return out
}

// Tags returns the union of all Tags across every action, in lexicographic order.
func (reg *Registry) Tags() []core.Tag {
	out := make([]core.Tag, len(reg.allTags))
	copy(out, reg.allTags)
	return out
}

// ── Internal helpers ───────────────────────────────────────────────────────────

// deriveTargetKind computes TargetKind and TargetKindID from the raw content fields
// following the SPEC's derivation rules (no new schema field):
//
//  1. target_kind present -> TargetObject (TargetKindID = that id)
//  2. "at_target" in produces -> TargetLocation (the action IS the move)
//  3. "near_other" in produces -> TargetAgent (the action IS the approach toward an agent)
//  4. "near_other" in requires -> TargetAgent
//  5. otherwise -> TargetNone
func deriveTargetKind(targetKind string, produces, requires []core.Pred) (TargetKind, core.Tag) {
	if targetKind != "" {
		return TargetObject, core.Tag(targetKind)
	}
	for _, p := range produces {
		if p == "at_target" {
			return TargetLocation, ""
		}
		if p == "near_other" {
			return TargetAgent, ""
		}
	}
	for _, r := range requires {
		if r == "near_other" {
			return TargetAgent, ""
		}
	}
	return TargetNone, ""
}

// copySlice returns a copy of a generic-type slice.
func copySlice[T any](s []T) []T {
	if s == nil {
		return nil
	}
	out := make([]T, len(s))
	copy(out, s)
	return out
}

// copyMap returns a copy of a map[core.Dimension]float64.
func copyMap(m Effect) Effect {
	if m == nil {
		return nil
	}
	out := make(Effect, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// D3/D4 struct-shape guards are in actions_test.go (reflection-based).
// The SPEC acceptance criteria "No gate/cost/plan/method/subtask field on ActionDef"
// is verified there so these checks remain with the test infrastructure,
// not mixed into production code.
