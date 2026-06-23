package agent

import (
	"math"
	"slices"

	"github.com/dogring/bdg/engine/actions"
	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/planner"
	"github.com/dogring/bdg/engine/rng"
)

const interactionRadius = 5.0 // world units; range for near_other predicate

// ── Phase 5: replan ────────────────────────────────────────────────────────────

// needsReplan reports whether the agent should call the planner this tick.
func (a *Agent) needsReplan(goalChanged bool) bool {
	if goalChanged {
		return true
	}
	// No current plan — need one.
	if len(a.Plan.Actions) == 0 {
		return true
	}
	// Currently mid-durative-action — don't re-plan.
	// (Re-plan happens on completion/interruption via ApplyOutcome.)
	return false
}

// replan builds an AgentSnapshot and delegates to the planner. Returns the plan
// and trace, or an error that triggers the coping cascade.
// P3: threads live body scalars (Stamina, Mood, Adrenaline, Urgency) into the
// planner snapshot so the P3 gates can read them.
// P5: combinedPriority is the max of self-priority and max Other-referent priority
// (computed by appraiseOthers). This urgency proxy drives the planner's conscience
// loosening: when caring for an Other in distress raises urgency above the threshold,
// transgressive actions become visible.
func (a *Agent) replan(world WorldView, now core.Tick, rng *rng.RNG, svc Services, priorities []planner.DimensionPriority, combinedPriority float64) (planner.Plan, planner.Trace, error) {
	// Build known object set from WorldView.
	knownObjs := world.KnownObjects(a.ID)
	knownSet := make(map[core.ObjectID]struct{}, len(knownObjs))
	for _, ko := range knownObjs {
		knownSet[ko.ID] = struct{}{}
	}

	// Get self-belief (ToM[self], D8).
	selfBelief, _ := a.ToM.Self(a.ToM.SelfID())

	// Compute Urgency from combinedPriority (self + Other-referent max).
	// Normalize by MaxPossiblePriority and clamp to [0,1].
	urgency := clamp01(combinedPriority / a.Cfg.MaxPossiblePriority)

	// Populate SatisfiedFacts from current world state.
	var satisfiedFacts []core.Pred
	entities := world.EntitiesInRadius(a.Pos, interactionRadius)
	nearOther := false
	ownedByOther := false
	for _, e := range entities {
		if e.ID == core.ObjectID(a.ID) {
			continue
		}
		isAgent := false
		hasItems := false
		for _, tag := range e.Tags {
			switch tag {
			case "agent":
				isAgent = true
			case "has_items":
				hasItems = true
			}
		}
		if isAgent {
			nearOther = true
			if hasItems {
				ownedByOther = true
			}
		}
	}
	if nearOther {
		satisfiedFacts = append(satisfiedFacts, "near_other")
	}
	if ownedByOther {
		// A nearby agent holds items the current agent could Take (B-3: owned_by_other
		// is a world-state fact, not an action producer — planner SPEC §World-state facts).
		satisfiedFacts = append(satisfiedFacts, "owned_by_other")
	}
	if world.HasPendingOffer(a.ID) {
		satisfiedFacts = append(satisfiedFacts, "tradeOffered")
	}

	// Compute per-agent budget from Intelligence and coping state (§A-budget).
	perceivedIntel := a.normalizedIntelligence(svc.Stats)
	effectiveNodes := a.Cfg.BudgetBase + int(perceivedIntel*float64(a.Cfg.BudgetPerIntelligence))

	// Build the planner's AgentSnapshot with live body scalars (P3).
	snapshot := planner.AgentSnapshot{
		ID:              core.ObjectID(a.ID),
		Pos:             a.Pos,
		CurrentStats:    a.RealStats,
		SelfModel:       selfBelief,
		NeedIntensities: a.NeedIntensities,
		Known:           knownSet,
		Urgency:         urgency,
		SatisfiedFacts:  satisfiedFacts,
		// P3: live body scalars for gate evaluation.
		Stamina:    a.Stamina,
		Mood:       a.Mood,
		Adrenaline: a.Adrenaline,
	}

	// Apathy-reduced budget (gap-closure §A-budget): only when the agent is apathetic.
	if a.Coping == Apathy && a.Cfg.ApathyBudgetPenalty > 0 {
		factor := 1.0 - a.Cfg.ApathyBudgetPenalty
		reducedNodes := effectiveNodes
		reducedNodes = max(int(float64(reducedNodes)*factor), 1)
		reducedActions := max(int(float64(a.Cfg.BudgetBase)*factor), 1)
		reducedDepth := max(int(float64(a.Cfg.BudgetBase/4)*factor), 1)
		budget := planner.Budget{
			MaxNodes:   reducedNodes,
			MaxActions: reducedActions,
			MaxDepth:   reducedDepth,
		}
		snapshot.ApathyBudget = &budget
	}
	// else: ApathyBudget remains nil → planner uses its configured PlannerConfig.Budget.

	// Promote a.Goal to index 0 so the planner targets the mediated goal.
	goalPriorities := promoteGoal(a.Goal, priorities)

	return svc.Planner.Plan(snapshot, goalPriorities, rng)
}

// ── Phase 6: execute ───────────────────────────────────────────────────────────

// execute emits the intent for the current step in the durative plan.
// It advances Elapsed and applies Stamina drain/regen but does NOT check
// completion — that's the world's job (reported via ApplyOutcome). The
// concrete object the action operates on is bound into Intent.Target here
// (the planner picks the ACTION; the agent binds the instance — D3 parametric
// targeting), so the world can resolve resource conflicts over it.
func (a *Agent) execute(now core.Tick, actReg *actions.Registry, world WorldView) Intent {
	// No plan or plan exhausted — nothing to execute.
	if len(a.Plan.Actions) == 0 || a.PlanIdx >= len(a.Plan.Actions) {
		return Intent{
			Kind:  IntentNone,
			Agent: a.ID,
			Tick:  now,
		}
	}

	actionID := a.Plan.Actions[a.PlanIdx]
	tickMinutes := core.GameMinutes(1)
	target := a.bindTarget(actionID, actReg, world)

	if a.Elapsed == 0 {
		// First tick of this action — start it.
		intent := Intent{
			Kind:   IntentStart,
			Agent:  a.ID,
			Action: actionID,
			Target: target,
			Tick:   now,
		}
		a.Elapsed += tickMinutes
		a.applyStaminaDelta(actionID, actReg, tickMinutes)
		return intent
	}

	// Continuing a durative action.
	intent := Intent{
		Kind:   IntentContinue,
		Agent:  a.ID,
		Action: actionID,
		Target: target,
		Tick:   now,
	}
	a.Elapsed += tickMinutes
	a.applyStaminaDelta(actionID, actReg, tickMinutes)
	return intent
}

// bindTarget resolves the concrete object instance an action operates on, for
// Intent.Target. Object-targeted actions (Forage→berry_bush, Hunt→prey,
// Craft→tool_bench, Drink→water_source, TakeShelter→shelter) bind the NEAREST
// known object of the action's target_kind; ties break by ObjectID, since
// KnownObjects is ObjectID-sorted (D12). Actions with no object target (Rest,
// MoveTo's location, social/agent actions) bind the empty ObjectID — the world
// treats an empty Target as "no resource", so they never contend.
func (a *Agent) bindTarget(actionID actions.ActionID, actReg *actions.Registry, world WorldView) core.ObjectID {
	if actReg == nil || world == nil {
		return ""
	}
	def, ok := actReg.Get(actionID)
	if !ok || def.Target != actions.TargetObject || def.TargetKindID == "" {
		return ""
	}

	var best core.ObjectID
	bestDist := math.MaxFloat64
	for _, ko := range world.KnownObjects(a.ID) {
		if ko.Kind != def.TargetKindID {
			continue
		}
		if d := a.Pos.Distance(ko.Pos); d < bestDist {
			bestDist = d
			best = ko.ID
		}
	}
	return best
}

// applyStaminaDelta applies Stamina drain (for effort) and regen (for Rest/Sleep)
// for one tick of executing actionID. P3: data-driven — effort level resolved
// from Config.EffortLevels; Rest/Sleep detected by action tags + effect.
func (a *Agent) applyStaminaDelta(actionID actions.ActionID, actReg *actions.Registry, tickMinutes core.GameMinutes) {
	if actReg == nil {
		return
	}
	def, ok := actReg.Get(actionID)
	if !ok {
		return
	}

	// ── Drain: effort level × DrainPerEffort ────────────────────────────────
	effortLevel := a.resolveEffortLevel(def.Tags)
	drain := a.Cfg.DrainPerEffort * effortLevel * float64(tickMinutes)

	// ── Regen: Rest/Sleep (detected by zero effort + effect_per_minute on Rest) ──
	regen := 0.0
	if effortLevel == 0 && a.hasRestEffectPerMinute(def) {
		regen = a.resolveRegenRate(def)
	}

	a.Stamina = clamp(a.Stamina-drain+regen, 0, a.Cfg.StaminaMax)
}

// resolveEffortLevel returns the effort level from the action's tags using the
// injected EffortLevels map (P3: data-driven, no hardcoded literal — D10).
func (a *Agent) resolveEffortLevel(tags []core.Tag) float64 {
	for _, t := range tags {
		if level, ok := a.Cfg.EffortLevels[t]; ok {
			return level
		}
	}
	return 0.0 // default: no effort tag → effort:none = 0
}

// hasRestEffectPerMinute returns true if the action has an effect_per_minute
// on the Rest dimension. P3: data-driven — no action-id literal (D7/D10).
// The caller must verify zero effort level separately via resolveEffortLevel.
func (a *Agent) hasRestEffectPerMinute(def actions.ActionDef) bool {
	// D10: Rest dimension resolved from agent config, injected by platform/config (no hardcoded literal).
	_, hasRest := def.EffectPerMinute[a.Cfg.RestDim]
	return hasRest
}

// resolveRegenRate returns the regen rate for a recovery action.
// Distinguishes Rest vs Sleep by the magnitude of Rest effect_per_minute
// (Sleep > Rest). P3: data-driven, no action-id literal.
func (a *Agent) resolveRegenRate(def actions.ActionDef) float64 {
	restRate, ok := def.EffectPerMinute[a.Cfg.RestDim]
	if !ok {
		return 0
	}
	// Sleep has a higher Rest effect_per_minute (0.0030) vs Rest (0.0010).
	// Use the magnitude to distinguish: >= 0.0030 → Sleep, otherwise Rest.
	if restRate >= 0.0030 {
		return a.Cfg.RegenSleep
	}
	return a.Cfg.RegenRest
}

// ── Phase 7: signal ────────────────────────────────────────────────────────────

// emitSignal produces an IntentSignal for trade actions (Offer/AcceptTrade/RejectTrade).
// Other actions return IntentNone.
func (a *Agent) emitSignal(now core.Tick, world WorldView) Intent {
	if len(a.Plan.Actions) == 0 || a.PlanIdx >= len(a.Plan.Actions) {
		return Intent{Kind: IntentNone, Agent: a.ID, Tick: now}
	}

	actionID := a.Plan.Actions[a.PlanIdx]
	switch actionID {
	case "Offer":
		target := a.nearestOtherAgentID(world)
		if target == "" {
			return Intent{Kind: IntentNone, Agent: a.ID, Tick: now}
		}
		// ClaimedValue is inflated inversely with Honesty (D8: reads ToM[self]).
		// It lerps from the honest floor to the dishonest ceiling as perceived Honesty
		// falls (D10: band from balance.yaml trade.claim_inflate_{min,max}; no literal).
		honesty := clamp01(a.perceivedStat("Honesty") / 100.0)
		claimedValue := a.Cfg.ClaimInflateMin + (1.0-honesty)*(a.Cfg.ClaimInflateMax-a.Cfg.ClaimInflateMin)
		truth := honesty * claimedValue // truth proportional to honesty
		return Intent{
			Kind:  IntentSignal,
			Agent: a.ID,
			Tick:  now,
			Signal: &Signal{
				Kind:         "Offer",
				Toward:       target,
				Valence:      0.5,
				ClaimedValue: claimedValue,
				Truth:        truth,
				Intensity:    0.7,
			},
		}

	case "AcceptTrade":
		target := a.nearestOtherAgentID(world)
		if target == "" {
			return Intent{Kind: IntentNone, Agent: a.ID, Tick: now}
		}
		return Intent{
			Kind:  IntentSignal,
			Agent: a.ID,
			Tick:  now,
			Signal: &Signal{
				Kind:      "Accept",
				Toward:    target,
				Valence:   0.8,
				Intensity: 0.6,
			},
		}

	case "RejectTrade":
		target := a.nearestOtherAgentID(world)
		if target == "" {
			return Intent{Kind: IntentNone, Agent: a.ID, Tick: now}
		}
		return Intent{
			Kind:  IntentSignal,
			Agent: a.ID,
			Tick:  now,
			Signal: &Signal{
				Kind:      "Reject",
				Toward:    target,
				Valence:   -0.3,
				Intensity: 0.5,
			},
		}
	}

	return Intent{Kind: IntentNone, Agent: a.ID, Tick: now}
}

// nearestOtherAgentID returns the nearest other agent within interactionRadius, or "".
func (a *Agent) nearestOtherAgentID(world WorldView) core.AgentID {
	entities := world.EntitiesInRadius(a.Pos, interactionRadius)
	for _, e := range entities {
		if e.ID == core.ObjectID(a.ID) {
			continue
		}
		if slices.Contains(e.Tags, core.Tag("agent")) {
			return core.AgentID(e.ID)
		}
	}
	return ""
}

// promoteGoal returns a copy of priorities with goalDim moved to index 0 so the
// planner targets the agent's mediated goal (which may differ from priorities[0]).
// If goalDim is not found, the original order is preserved (fallback to top priority).
func promoteGoal(goalDim core.Dimension, priorities []planner.DimensionPriority) []planner.DimensionPriority {
	if goalDim == "" || len(priorities) == 0 {
		return priorities
	}
	out := make([]planner.DimensionPriority, 0, len(priorities))
	var found *planner.DimensionPriority
	for i := range priorities {
		if priorities[i].Dim == goalDim {
			found = &priorities[i]
		} else {
			out = append(out, priorities[i])
		}
	}
	if found == nil {
		return priorities // goalDim not in priorities
	}
	return append([]planner.DimensionPriority{*found}, out...)
}

// ── Phase 8: dynamics (moved to dynamics.go) ────────────────────────────────────
// updateDynamics, updateResentment, and event emission helpers are in dynamics.go.
