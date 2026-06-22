package planner

import (
	"errors"
	"sort"

	"github.com/dogring/bdg/engine/actions"
	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/gates"
	"github.com/dogring/bdg/engine/needs"
	"github.com/dogring/bdg/engine/rng"
	"github.com/dogring/bdg/engine/stats"
)

// ── Sentinel errors ─────────────────────────────────────────────────────────

// ErrBudgetExceeded is returned when a Budget cap (MaxDepth | MaxActions | MaxNodes)
// is reached. The why-trace can record the abort.
var ErrBudgetExceeded = errors.New("planner: budget exceeded")

// ErrNoGoal is returned when values is empty — nothing to pursue.
var ErrNoGoal = errors.New("planner: no goal dimensions provided")

// ErrUnreachable is returned when the goal has no visible producer reachable
// within budget.
var ErrUnreachable = errors.New("planner: goal unreachable within budget")

// ── Planner ─────────────────────────────────────────────────────────────────

// Planner is the opaque, immutable-after-New deliberation engine. It holds
// borrowed read-only references to the registries and a copy of the config.
// Safe to share across goroutines during the read/plan phase (each Plan call
// is a pure function of its arguments + the registries).
type Planner struct {
	actionsReg         *actions.Registry
	gatesReg           *gates.Registry
	needsReg           *needs.Registry
	statsReg           *stats.Registry
	cfg                PlannerConfig
	sortedTagCostKeys  []core.Tag  // precomputed sorted keys for D12 iteration
	intelligenceStat   core.StatID // resolved Intelligence StatID (D7)
	baseHorizonTicks   int         // from cfg
	lookaheadThreshold float64     // P5: below this, forward-sim is skipped entirely
}

// New constructs a Planner from the already-loaded registries and an injected
// config. The registries are borrowed read-only (never mutated). cfg is copied;
// TagCosts is snapshotted into a sorted-key form for deterministic iteration (D12).
//
// New resolves the Intelligence StatID from the stats registry (D7/D10: resolved
// once at construction, stored, and never repeated as a literal in planner logic).
func New(
	actReg *actions.Registry,
	gateReg *gates.Registry,
	needReg *needs.Registry,
	statReg *stats.Registry,
	cfg PlannerConfig,
) *Planner {
	intelStat := resolveIntelligenceStatID(statReg)
	sortedKeys := sortedTagCostKeys(cfg.TagCosts)

	baseHorizon := cfg.BaseHorizonTicks
	if baseHorizon < 1 {
		baseHorizon = 1
	}

	lookaheadThreshold := cfg.LookaheadThreshold
	if lookaheadThreshold < 0 {
		lookaheadThreshold = 0
	}
	if lookaheadThreshold > 1 {
		lookaheadThreshold = 1
	}

	return &Planner{
		actionsReg:         actReg,
		gatesReg:           gateReg,
		needsReg:           needReg,
		statsReg:           statReg,
		cfg:                cfg,
		sortedTagCostKeys:  sortedKeys,
		intelligenceStat:   intelStat,
		baseHorizonTicks:   baseHorizon,
		lookaheadThreshold: lookaheadThreshold,
	}
}

// resolveIntelligenceStatID resolves the glossary Intelligence capability StatID
// from the stats registry. This is the ONE place the Intelligence concept is named —
// it resolves it to a StatID so the rest of the planner uses only the resolved id (D7/D10).
// Failing fast (returning "") means horizon calc will use minimum viable foresight.
func resolveIntelligenceStatID(statReg *stats.Registry) core.StatID {
	const intelGlossaryID core.StatID = "Intelligence"
	if statReg.Has(intelGlossaryID) {
		return intelGlossaryID
	}
	return ""
}

// ── Public API ──────────────────────────────────────────────────────────────

// Plan assembles the action sequence pursuing the highest-Priority dimension in
// values. It is a PURE function of (agent, values, the borrowed registries, rng):
// identical inputs + identical rng seed/state -> identical Plan and Trace (D12).
// rng is accepted but not called for default tie-breaking (lexicographic ActionID
// is sufficient).
//
// Returns ErrBudgetExceeded if any Budget cap is reached; ErrNoGoal if values is
// empty; ErrUnreachable if the highest-Priority goal has no visible producer
// within budget.
func (p *Planner) Plan(
	agent AgentSnapshot,
	values []DimensionPriority,
	rng *rng.RNG,
) (Plan, Trace, error) {
	_ = rng // reserved for future configurable random tie-breaking

	// ── 1. Goal dimension ────────────────────────────────────────────────────
	// goalDim is read from values[0] (caller-supplied, post-mediateGoal order)
	// BEFORE any re-sort, so the agent's stickiness/deadband choice is honoured.
	if len(values) == 0 {
		return Plan{}, Trace{}, ErrNoGoal
	}

	goalDim := values[0].Dim

	// ── 2. Compute Intelligence-gated horizon (P5 lookahead threshold) ────────
	horizonTicks := computeHorizon(
		agent.SelfModel,
		p.intelligenceStat,
		p.statsReg,
		p.baseHorizonTicks,
		p.lookaheadThreshold,
	)

	// ── 3. Forward-sim provisioning (D9) — hard-skip at horizon=0 (P5) ─────────
	var provisionedDims []core.Dimension
	if horizonTicks > 0 {
		provisionedDims = forwardSimProvision(p.needsReg, agent.NeedIntensities, horizonTicks)
	} else {
		provisionedDims = nil
	}

	// ── 4. GOAP + HTN search for the goal ─────────────────────────────────────
	ss := &searchState{
		actionsReg:       p.actionsReg,
		gatesReg:         p.gatesReg,
		statsReg:         p.statsReg,
		agent:            agent,
		urgencyThreshold: p.cfg.UrgencyThreshold,
		sortedTagKeys:    p.sortedTagCostKeys,
		tagCosts:         p.cfg.TagCosts,
		nodesExpanded:    0,
		maxNodes:         p.cfg.Budget.MaxNodes,
		maxDepth:         p.cfg.Budget.MaxDepth,
		maxActions:       p.cfg.Budget.MaxActions,
	}

	goalPred := dimensionToProducerPredicate(goalDim)
	goalActions, goalCost, err := ss.findBestSequence(goalPred)
	if err != nil {
		return Plan{}, Trace{}, err
	}

	if len(goalActions) > ss.maxActions {
		return Plan{}, Trace{}, ErrBudgetExceeded
	}

	// ── 5. Insert provisioning subgoals BEFORE the goal action ────────────────
	var fullActions []actions.ActionID
	totalCost := 0.0

	for _, pd := range provisionedDims {
		provPred := dimensionToProducerPredicate(pd)
		provActions, provCost, err := ss.findBestSequence(provPred)
		if err != nil {
			if errors.Is(err, ErrBudgetExceeded) {
				return Plan{}, Trace{}, err
			}
			continue // unreachable -> skip provisioning for this dimension
		}
		fullActions = append(fullActions, provActions...)
		totalCost += provCost
	}

	fullActions = append(fullActions, goalActions...)
	totalCost += goalCost

	if len(fullActions) > ss.maxActions {
		return Plan{}, Trace{}, ErrBudgetExceeded
	}

	// ── 6. Sort candidates for deterministic trace (D12) ──────────────────────
	sort.Slice(ss.candidates, func(i, j int) bool {
		return string(ss.candidates[i].Action) < string(ss.candidates[j].Action)
	})

	trace := Trace{
		GoalDim:       goalDim,
		Candidates:    ss.candidates,
		Provisioned:   provisionedDims,
		Relaxed:       ss.relaxed,
		TotalCost:     totalCost,
		NodesExpanded: ss.nodesExpanded,
	}

	plan := Plan{
		Actions: fullActions,
		Horizon: horizonTicks,
	}

	return plan, trace, nil
}
