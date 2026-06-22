package agent

import (
	"sort"

	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/tom"
)

// ── P6: Emergent reliance, delegation Vote, and Influence weighting ──────────

// handleRelianceTrigger runs after a failed/burdened plan to potentially shift
// reliance toward a better provider for the Function corresponding to the agent's
// current goal dimension. If the planner returned ErrUnreachable/ErrBudgetExceeded
// OR the cheapest plan trace's cost exceeds Cfg.RelyCostThreshold, the agent picks
// the BestProviderFor among known agents (excluding itself) and AdjustRelyOn toward
// that provider by Cfg.RelyOnDelta.
func (a *Agent) handleRelianceTrigger(world WorldView, planCost float64, planErr error) {
	// Trigger conditions:
	// 1. Plan returned ErrUnreachable/ErrBudgetExceeded, OR
	// 2. planCost > a.Cfg.RelyCostThreshold (plan was solvable but prohibitively costly).
	if planErr == nil && planCost <= a.Cfg.RelyCostThreshold {
		return // no trigger — the agent can self-solve affordably
	}

	// Resolve the Function for the current goal.
	fn := goalToFunction(a.Goal)
	if fn == "" {
		return
	}

	// Gather candidates: all known agents excluding self.
	knownIDs := world.AgentIDs()
	if len(knownIDs) == 0 {
		return
	}

	// Filter out self and sort for deterministic evaluation (D12).
	candidates := make([]core.AgentID, 0, len(knownIDs))
	for _, id := range knownIDs {
		if id != a.ID {
			candidates = append(candidates, id)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return string(candidates[i]) < string(candidates[j])
	})

	selfStats, _ := a.ToM.Self(a.ToM.SelfID())

	// Build the statSet for BestProviderFor from the agent's own relevant stats.
	var statSet []core.StatID
	for sid := range selfStats.EstStats {
		statSet = append(statSet, sid)
	}
	sortStatIDs(statSet)

	target, score := a.ToM.BestProviderFor(fn, statSet, candidates)
	if target == "" || score <= 0 {
		return
	}

	a.ToM.AdjustRelyOn(target, fn, a.Cfg.RelyOnDelta)
}

// emitVoteIfEligible checks whether the agent's private reliance and the collective
// distributed urgency both exceed their thresholds. If so, it returns an IntentSignal
// with the Vote payload. Returns IntentNone if conditions are not met.
func (a *Agent) emitVoteIfEligible(now core.Tick, world WorldView) Intent {
	fn := core.FuncSafety

	// Condition 1: find the agent we rely on most for Safety, and check its strength.
	relyTarget, relyStrength := a.bestRelyOnFor(fn)
	if relyTarget == "" || relyStrength <= a.Cfg.VoteRelyThreshold {
		return Intent{Kind: IntentNone, Agent: a.ID, Tick: now}
	}

	// Condition 2: distributed urgency above threshold.
	urgency := distributedUrgency(world)
	if urgency <= a.Cfg.VoteUrgencyThreshold {
		return Intent{Kind: IntentNone, Agent: a.ID, Tick: now}
	}

	return Intent{
		Kind:  IntentSignal,
		Agent: a.ID,
		Tick:  now,
		Signal: &Signal{
			Kind:      SignalKind("Vote"),
			Toward:    "",        // broadcast — no specific receiver
			Valence:   0.5,
			Intensity: relyStrength,
			Function:  fn,
			Target:    relyTarget, // the voted holder
		},
	}
}

// processIncomingSignals iterates signals addressed to this agent and processes
// Vote signals by folding them into the agent's RelyOn.
func (a *Agent) processIncomingSignals(world WorldView) {
	signals := world.IncomingSignals(a.ID)
	if len(signals) == 0 {
		return
	}
	for _, sig := range signals {
		a.processVoteSignal(sig)
	}
}

// processVoteSignal handles an incoming SignalVote from another agent (delivered
// via WorldView.IncomingSignals). It folds a RelyOn delta toward the voted holder
// (signal.Target) for the given Function, but only if the sender has positive Trust
// (Trust > 0) — untrusted senders are ignored.
//
// The signal's Intensity field carries the voter's own RelyOn strength toward the
// voted holder; the delta applied is Config.VoteRelyOnDelta (a constant independent
// of the voter's own strength — the vote is a signal, not a transfer of reliance).
func (a *Agent) processVoteSignal(sig core.Signal) {
	if sig.Kind != core.SignalVote {
		return
	}

	fn := sig.Function
	if fn == "" {
		return
	}

	votedHolder := sig.Target
	if votedHolder == "" {
		return
	}

	sourceID := sig.Source
	if sourceID == "" {
		return
	}

	// Check sender Trust — ignore votes from untrusted agents.
	senderBelief, ok := a.ToM.Self(sourceID)
	if !ok || senderBelief.Trust <= 0 {
		return
	}

	// Adjust RelyOn toward the voted holder for this Function.
	a.ToM.AdjustRelyOn(votedHolder, fn, a.Cfg.VoteRelyOnDelta)
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// bestRelyOnFor iterates the agent's ToM subjects (sorted, D12) and returns the
// subject with the highest RelyOn[fn] value, and that value. Returns ("", 0) if
// no subject has a non-zero RelyOn for fn.
func (a *Agent) bestRelyOnFor(fn core.Function) (core.AgentID, float64) {
	bestID := core.AgentID("")
	bestScore := float64(-1)

	for _, subj := range a.ToM.Subjects() {
		if subj == a.ToM.SelfID() {
			continue
		}
		b, ok := a.ToM.Self(subj)
		if !ok {
			continue
		}
		if b.RelyOn == nil {
			continue
		}
		score := b.RelyOn[fn]
		if score > bestScore {
			bestScore = score
			bestID = subj
		}
		// Ties break by lower AgentID (sorted Subjects already handles this).
	}
	if bestID == "" || bestScore <= 0 {
		return "", 0
	}
	return bestID, bestScore
}

// distributedUrgency computes the urgency derived from collective need state.
// Higher when collective Safety is low (many agents suffering). Returns the
// urgency in [0,1] where 1 = maximum distress.
func distributedUrgency(world WorldView) float64 {
	members := world.MemberNeedIntensities()
	if len(members) == 0 {
		return 0
	}

	var safetySum float64
	var count int
	for _, aid := range sortedAgentIDs(members) {
		intensities := members[aid]
		if v, ok := intensities["Safety"]; ok {
			safetySum += v
			count++
		}
	}
	if count == 0 {
		return 0
	}
	mean := safetySum / float64(count)
	// Low safety (high intensity) → high urgency.
	return clamp01(mean)
}

// goalToFunction maps a goal Dimension to a canonical Function. For now, Safety
// maps to FuncSafety; all others map to FuncKnowledge (generic problem-solving).
// This is a simple heuristic — "what Function does this goal serve?"
func goalToFunction(dim core.Dimension) core.Function {
	switch dim {
	case "Safety":
		return core.FuncSafety
	default:
		// All non-Safety goals default to Knowledge (the agent needs know-how to
		// satisfy them). A finer mapping (e.g. Satiety→Knowledge) is content-driven
		// and would come from an injected FunctionSpec table (future work).
		return core.FuncKnowledge
	}
}

// allBeliefs returns all beliefs held in the agent's ToM (excluding self) as a
// slice, in sorted SubjectID order (D12). Used to compute Influence distributions.
func (a *Agent) allBeliefs() []tom.Belief {
	subjects := a.ToM.Subjects()
	beliefs := make([]tom.Belief, 0, len(subjects))
	for _, subj := range subjects {
		if subj == a.ToM.SelfID() {
			continue
		}
		if b, ok := a.ToM.Self(subj); ok {
			beliefs = append(beliefs, b)
		}
	}
	return beliefs
}
