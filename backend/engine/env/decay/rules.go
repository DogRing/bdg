package decay

import (
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
)

// ── decayContext: expr.Context adapter ───────────────────────────────────────

// decayContext adapts EnvInput to the expr.Context interface for §6 accel evaluation (Dm3(a)).
//
// §6 operands exposed:
//   - Attr "temperature" → EnvInput.Temperature in °C
//   - Attr "moisture"    → EnvInput.Moisture ∈ [0,1]
//
// Decay accel formulas reference only env attributes (not stats or predicates).
// The zero-value decayContext is valid (all attrs resolve to 0 / ok=false).
type decayContext struct {
	in EnvInput
}

func (c decayContext) Stat(_ core.StatID) float64 {
	return 0 // decay accel formulas reference no stats
}

func (c decayContext) Attr(name core.Tag) (float64, bool) {
	switch name {
	case "temperature":
		return c.in.Temperature, true
	case "moisture":
		return c.in.Moisture, true
	}
	return 0, false
}

func (c decayContext) Pred(_ string, _ core.Tag) bool {
	return false // decay accel formulas use no predicates
}

// Compile-time check that decayContext satisfies expr.Context.
var _ expr.Context = decayContext{}

// ── KindRule / StateRule / TransformRule ─────────────────────────────────────

// KindRule is one item_kind's compiled decay spec (the NewRules input).
type KindRule struct {
	BaseRate float64       // base decay rate in effective-decay-time units per tick at neutral env+storage (Q2)
	Accel    *expr.Program // compiled §6 accel over {temperature °C, moisture} (Dm3(a)); nil ⇒ constant 1
	States   []StateRule   // ordered discrete states (ascending Threshold) → StateAt/SupplyAt
}

// StateRule is one ordered decay state: entry threshold + transform products + optional supply override.
type StateRule struct {
	Threshold float64                    // DecayAge entry threshold (strictly ascending; first state = 0)
	Supply    map[core.Dimension]float64 // per-state supply override (nil ⇒ item base supply) → SupplyAt
	Transform []TransformRule            // products on ENTERING this state (Q3); empty for terminal gone
}

// TransformRule is one per-source-item transform product (apply scales Qty by the lot's Qty, Dm5(a)).
type TransformRule struct {
	Item KindID
	Qty  int
}

// ── Rules ─────────────────────────────────────────────────────────────────────

// Rules is the compiled, immutable per-KindID decay table from content/objects.yaml decay: blocks.
// An empty byKind map (no kind entries) produces decay-off behavior.
// Opaque; constructed only via NewRules.
type Rules struct {
	byKind map[KindID]KindRule // keyed by kind; single key lookups only (D12)
}

// NewRules builds the immutable decay table from per-kind COMPILED inputs — the config-facing
// constructor (WI-P0). platform/config parses content/objects.yaml decay: blocks, compiles
// each accel via expr.Parse, validates kind/item ids + strictly-ascending thresholds, then
// calls this; decay never parses YAML (D10). States within a kind are ordered (ascending
// Threshold, first = 0). Pure. A nil or empty map produces decay-off Rules.
func NewRules(kinds map[KindID]KindRule) *Rules {
	m := make(map[KindID]KindRule, len(kinds))
	for k, v := range kinds {
		m[k] = v
	}
	return &Rules{byKind: m}
}

// ── Rules accessors ───────────────────────────────────────────────────────────

// StateAt maps a continuous DecayAge (effective-time units, Dm1(a)) to the kind's DERIVED
// discrete state index via the kind's ordered thresholds (mirrors flora.Stage over Length; D9
// — state is never stored). DecayAge below the first non-zero threshold ⇒ state 0; at/above
// the last threshold ⇒ the terminal state. Pure, no RNG.
//
// Returns 0 for an unknown kind (decay-off; no species record means no transitions).
func (r *Rules) StateAt(kind KindID, decayAge float64) int {
	if r == nil {
		return 0
	}
	kr, ok := r.byKind[kind]
	if !ok {
		return 0
	}
	return stateFromKind(kr, decayAge)
}

// SupplyAt returns the supply Effect a kind delivers in a given derived state — the per-state
// override if the state declares one, else nil (indicating item_kind base supply). Decayed food
// feeds less (D9 — still a supply Effect on the object, no future field). Read by world/values
// when an item from the lot is consumed. Pure, no RNG.
func (r *Rules) SupplyAt(kind KindID, state int) map[core.Dimension]float64 {
	if r == nil {
		return nil
	}
	kr, ok := r.byKind[kind]
	if !ok {
		return nil
	}
	if state < 0 || state >= len(kr.States) {
		return nil
	}
	return kr.States[state].Supply
}

// BaseRate returns the kind's data base decay rate (Q2), in effective-decay-time units per tick
// at neutral env+storage. It is the first multiplicative term of effectiveRate (Dm2(a)).
// Returns 0 for an unknown kind (decay-off; no advancement without a rule).
func (r *Rules) BaseRate(kind KindID) float64 {
	if r == nil {
		return 0
	}
	kr, ok := r.byKind[kind]
	if !ok {
		return 0
	}
	return kr.BaseRate
}

// Accel evaluates the kind's §6 acceleration multiplier over the env (Temperature/Moisture).
// Decay builds an expr.Context from EnvInput (operands "temperature", "moisture") and calls
// the compiled accel Program (Dm3(a) — decay-owned). It is the second multiplicative term of
// effectiveRate (Dm2(a)); cold+dry → < 1 (slows), warm+wet → > 1 (speeds). The result is
// clamped ≥ 0. Returns 1 for an unknown kind or nil Accel program (neutral multiplier).
func (r *Rules) Accel(kind KindID, in EnvInput) float64 {
	if r == nil {
		return 1
	}
	kr, ok := r.byKind[kind]
	if !ok {
		return 1
	}
	if kr.Accel == nil {
		return 1 // nil program ⇒ constant 1 (per KindRule doc)
	}
	ctx := decayContext{in: in}
	v := kr.Accel.EvalNumber(ctx)
	if v < 0 {
		v = 0 // domain clamp ≥ 0 (matches climate/flora clamp contract)
	}
	return v
}
