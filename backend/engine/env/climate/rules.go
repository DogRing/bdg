package climate

import (
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
)

// ── cellContext: expr.Context adapter for CellState ──────────────────────────

// cellContext adapts a CellState to the expr.Context interface for rule evaluation.
//
// The §6 operands exposed by climate cells are:
//   - "moisture"    float64 ∈ [0,1]  — SPEC §6 operand name
//   - "temperature" float64 °C (CA3) — SPEC §6 operand name
//
// Climate transition conditions use no stats and no predicates. Any Attr name other than
// the two above returns (0, false) per the expr deterministic-absence policy (→ 0).
type cellContext struct {
	state CellState
}

func (c cellContext) Stat(_ core.StatID) float64 {
	return 0 // climate conditions use no stats
}

func (c cellContext) Attr(name core.Tag) (float64, bool) {
	switch name {
	case "moisture":
		return c.state.Moisture, true
	case "temperature":
		return c.state.Temperature, true
	}
	return 0, false
}

func (c cellContext) Pred(_ string, _ core.Tag) bool {
	return false // climate conditions use no predicates
}

// Compile-time check that cellContext satisfies expr.Context.
var _ expr.Context = cellContext{}

// ── Rules ─────────────────────────────────────────────────────────────────────

// ruleEntry holds one compiled when→to pair for a given from-terrain.
type ruleEntry struct {
	when *expr.Program
	to   navTerrainID
}

// Rules is the compiled, immutable transition table (data-defined, D4/D10).
// An empty Rules means climate-off: no transitions, no panic (RESOLVED #10).
// Opaque; constructed only via NewRules.
type Rules struct {
	// byFrom maps a "from" terrain id to its ordered slice of ruleEntry.
	// Order within the slice is the file order preserved by NewRules (first-match wins, D12).
	// Map lookup by key is deterministic; we never iterate over map keys in Eval (D12).
	byFrom map[navTerrainID][]ruleEntry
}

// TransitionRule is one compiled from×when→to rule (the NewRules input, mirroring Eval).
// When is the compiled §6 boolean Program over CellState operands ("moisture" [0,1],
// "temperature" °C, CA3). For a given From, rules are evaluated in slice order; the FIRST
// whose When is true wins.
type TransitionRule struct {
	From navTerrainID
	When *expr.Program
	To   navTerrainID
}

// NewRules builds an immutable Rules from a compiled transition slice.
//
// platform/config parses content/climate.yaml `transitions`, compiles each `when` via
// expr.Parse, validates every from/to ⊆ content/terrain.yaml, preserves FILE ORDER,
// then calls this. climate never parses YAML (D10).
//
// An empty slice ⇒ climate-off (no transitions, outcome-neutral). Pure.
func NewRules(transitions []TransitionRule) *Rules {
	r := &Rules{
		byFrom: make(map[navTerrainID][]ruleEntry, len(transitions)),
	}
	for _, t := range transitions {
		r.byFrom[t.From] = append(r.byFrom[t.From], ruleEntry{when: t.When, to: t.To})
	}
	return r
}

// Eval returns the destination terrain for a cell given its state, or ("", false) if no rule
// fires (stays put). Rules are §6-DSL booleans over CellState operands evaluated by the shared
// expr evaluator. The FIRST matching rule for the from-type wins (ordered table → deterministic,
// D12). No RNG inside a condition.
//
// Nil receiver is handled gracefully: returns ("", false).
func (r *Rules) Eval(from navTerrainID, s CellState) (navTerrainID, bool) {
	if r == nil {
		return "", false
	}
	ctx := cellContext{state: s}
	for _, entry := range r.byFrom[from] {
		if entry.when.EvalBool(ctx) {
			return entry.to, true
		}
	}
	return "", false
}
