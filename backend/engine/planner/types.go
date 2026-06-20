// Package planner assembles the shortest viable ordered sequence of atomic actions
// that pursues the highest-Priority need/value Dimension, using three composed
// mechanisms — GOAP backward chaining, HTN forward decomposition, and forward-sim
// provisioning (D9) — bounded by a deliberation Budget, with action cost derived
// from Tags (D4) and dynamic gate-relaxation driven by Urgency.
//
// This is the how-to-get-it module (D5): it never decides WHAT is wanted (that is
// engine/values + engine/needs). Plans are emergent sequences only; no Method/Task/
// Subtask type exists here (D3).
package planner

import (
	"github.com/dogring/bdg/engine/actions"
	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/stats"
	"github.com/dogring/bdg/engine/tom"
	"github.com/dogring/bdg/engine/values"
)

// ── Search bounds ──────────────────────────────────────────────────────────────

// Budget caps deliberation cost so a search always terminates (no infinite chaining).
// All three caps are read-only after construction; injected from content/balance.yaml
// planner.budget.* (D10). A breach of any cap aborts the current Plan call with
// ErrBudgetExceeded — it is NOT silently truncated (so a why-trace can record the abort).
type Budget struct {
	MaxDepth   int // max HTN recursion depth (prevents infinite precondition loops)
	MaxActions int // max plan length (actions in the assembled sequence)
	MaxNodes   int // max GOAP nodes expanded per Plan call
}

// ── Configuration (injected from content/balance.yaml planner.*) ────────────────

// PlannerConfig bundles every tunable the planner reads. All fields are injected by
// the caller (read from content/balance.yaml's planner: block via platform/config);
// the planner hardcodes NO numeric constant (D10). Immutable for the planner's lifetime.
type PlannerConfig struct {
	Budget           Budget               // search caps (planner.budget.*)
	BaseHorizonTicks int                  // forward-sim base lookahead (planner.base_horizon_ticks)
	TagCosts         map[core.Tag]float64 // per-Tag cost weight (planner.tag_costs.<Tag>)
	UrgencyThreshold float64              // gate-relaxation trigger (planner.urgency_threshold)
}

// ── Inputs (read-only per Plan call) ───────────────────────────────────────────

// AgentSnapshot is the READ-ONLY view of the deliberating agent. The planner MUST
// NOT mutate it and MUST NOT retain a reference past the Plan call. Stat-reading
// decisions (gates, horizon) read SelfModel (ToM[self]) — never Real Stats (D8).
// Body scalars (NEW P3) are threaded to gates for stamina/apathy/conscience-relief/
// adrenaline-cost evaluation.
type AgentSnapshot struct {
	ID              core.ObjectID              // the agent's id (D12 apply-order)
	Pos             core.Vec2                  // current position (distance-aware cost / MoveTo)
	CurrentStats    stats.Stats                // stat vector (for reference; decisions use SelfModel, D8)
	SelfModel       tom.Belief                 // ToM[self]: self-belief for gate eval + horizon (D8). SPEC resolved: planner uses tom.Belief directly.
	NeedIntensities map[core.Dimension]float64 // current GROWN intensity per consumable dimension
	Known           map[core.ObjectID]struct{} // objects this agent knows of
	Urgency         float64                    // max Salience across all Dimensions (drives relaxation)
	SatisfiedFacts  []core.Pred               // predicates already true in the current world state (e.g. near_other)

	// Body scalars (NEW P3) — threaded to gates for body-scalar leaf evaluation.
	Stamina    float64 // [0, StaminaMax]
	Mood       float64 // signed
	Adrenaline float64 // [0, AdrMax]
}

// DimensionPriority is one row of the Priority-ordered goal list produced by
// engine/values. The caller passes these sorted by Priority DESCENDING; ties are
// broken by Dim lexicographically BEFORE the planner sees them (the planner
// re-sorts defensively to guarantee D12 order).
type DimensionPriority struct {
	Dim      core.Dimension
	Priority values.Priority
	Salience values.Salience
}

// ── Outputs ────────────────────────────────────────────────────────────────────

// Plan is the assembled ordered action sequence and its validity window.
type Plan struct {
	Actions []actions.ActionID // ordered: prerequisites first, goal action last (HTN order)
	Horizon int                // ticks this plan is forward-valid for (Intelligence-gated lookahead)
}

// Trace is the why-trace breakdown for one Plan call (data-contracts §4 PlanBuilt /
// GoalSelected). It records the selected goal dimension, the competing candidate
// actions with their cost, the gate verdicts consulted, whether relaxation was
// applied, and the provisioned dimensions.
type Trace struct {
	GoalDim       core.Dimension
	Candidates    []Candidate      // competing producers considered for the root goal, in ActionID order
	Provisioned   []core.Dimension // dimensions a provisioning subgoal was inserted for (D9)
	Relaxed       bool             // whether Urgency relaxation unlocked a gated action this call
	TotalCost     float64          // Σ tag-derived cost of the chosen Actions
	NodesExpanded int              // GOAP nodes expanded (≤ Budget.MaxNodes)
}

// Candidate is one considered producer action and its evaluation, for the why-trace.
type Candidate struct {
	Action  actions.ActionID
	Cost    float64 // tag-derived cost (Σ TagCosts over its tags)
	Visible bool    // gate verdict (after any relaxation)
	Chosen  bool    // selected into the plan
}
