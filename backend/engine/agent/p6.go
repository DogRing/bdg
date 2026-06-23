package agent

import (
	"sort"

	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/tom"
)

// ── P6: Emergent reliance, delegation Vote, and Influence weighting ──────────

// functionForGoal returns the FunctionSpec whose Dim matches the given goal Dimension.
// Scans Config.Functions in order (a small fixed table, D12); returns zero-value
// FunctionSpec and false if none match. Replaces the hardcoded goalToFunction mapping
// (gap-closure §G).
func (a *Agent) functionForGoal(dim core.Dimension) (FunctionSpec, bool) {
	for _, fn := range a.Cfg.Functions {
		if fn.Dim == dim {
			return fn, true
		}
	}
	return FunctionSpec{}, false
}

// handleRelianceTrigger runs after a failed/burdened plan to potentially shift
// reliance toward a better provider for the Function corresponding to the agent's
// current goal dimension. If the planner returned ErrUnreachable/ErrBudgetExceeded
// OR the cheapest plan trace's cost exceeds Cfg.RelyCostThreshold, the agent picks
// the BestProviderFor among known agents (excluding itself) and AdjustRelyOn toward
// that provider by Cfg.RelyOnDelta. Emits BeliefUpdated on a successful reliance
// shift (gap-closure §G-emit).
func (a *Agent) handleRelianceTrigger(world WorldView, planCost float64, planErr error, emit core.EventEmitter) {
	// Trigger conditions:
	// 1. Plan returned ErrUnreachable/ErrBudgetExceeded, OR
	// 2. planCost > a.Cfg.RelyCostThreshold (plan was solvable but prohibitively costly).
	if planErr == nil && planCost <= a.Cfg.RelyCostThreshold {
		return // no trigger — the agent can self-solve affordably
	}

	// Resolve the Function and capability stat-set for the current goal via
	// Config.Functions (D7/D10: no hardcoded mapping — gap-closure §G).
	fnSpec, ok := a.functionForGoal(a.Goal)
	if !ok {
		return // no Function registered for this goal Dimension
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

	// Use injected stat-set from FunctionSpec (D7: no hardcoded stat ids).
	statSet := make([]core.StatID, len(fnSpec.Stats))
	copy(statSet, fnSpec.Stats)
	sortStatIDs(statSet)

	target, score := a.ToM.BestProviderFor(fnSpec.ID, statSet, candidates)
	if target == "" || score <= 0 {
		return
	}

	a.ToM.AdjustRelyOn(target, fnSpec.ID, a.Cfg.RelyOnDelta)

	// Emit BeliefUpdated for the reliance trace (gap-closure §G-emit).
	if emit != nil {
		emit.Emit(core.Event{
			SchemaVersion: 1,
			AgentID:       a.ID,
			Type:          "BeliefUpdated",
			Payload: map[string]any{
				"about": string(target),
				"field": "RelyOn[" + string(fnSpec.ID) + "]",
				"cause": "reliance",
				"delta": a.Cfg.RelyOnDelta,
			},
		})
	}
}

// emitVoteIfEligible checks whether the agent's private reliance and the phase-3
// combinedPriority both exceed their thresholds. If so, it returns an IntentSignal
// with the Vote payload. Takes the already-computed combinedPriority (gap-closure §H)
// so distributedUrgency is not recomputed. Returns IntentNone if conditions are not met.
func (a *Agent) emitVoteIfEligible(now core.Tick, world WorldView, combinedPriority float64) Intent {
	// Resolve the Function for the SafetyDim via Config.Functions.
	fnSpec, ok := a.functionForGoal(a.Cfg.SafetyDim)
	if !ok {
		return Intent{Kind: IntentNone, Agent: a.ID, Tick: now}
	}
	fn := fnSpec.ID

	// Condition 1: find the agent we rely on most for this Function.
	relyTarget, relyStrength := a.bestRelyOnFor(fn)
	if relyTarget == "" || relyStrength < a.Cfg.VoteRelyThreshold {
		return Intent{Kind: IntentNone, Agent: a.ID, Tick: now}
	}

	// Condition 2: use the pre-computed combined urgency proxy from phase 3 (§H).
	urgency := clamp01(combinedPriority / a.Cfg.MaxPossiblePriority)
	if urgency <= a.Cfg.UrgencyThreshold {
		return Intent{Kind: IntentNone, Agent: a.ID, Tick: now}
	}

	return Intent{
		Kind:  IntentSignal,
		Agent: a.ID,
		Tick:  now,
		Signal: &Signal{
			Kind:      SignalKind("Vote"),
			Toward:    "",           // broadcast — no specific receiver
			Valence:   0.5,
			Intensity: relyStrength,
			Function:  fn,
			Target:    relyTarget, // the voted holder
		},
	}
}

// processIncomingSignals iterates signals addressed to this agent. Vote signals are
// folded into the agent's RelyOn; other signals (gossip/hearsay) are folded with
// Influence-weighted credibility (gap-closure §I-fold).
func (a *Agent) processIncomingSignals(world WorldView) {
	signals := world.IncomingSignals(a.ID)
	if len(signals) == 0 {
		return
	}
	// Pre-compute sorted observers for Influence weighting (D12).
	observers := a.allBeliefs()

	for _, sig := range signals {
		switch sig.Kind {
		case core.SignalVote:
			a.processVoteSignal(sig)
		default:
			// Gossip/hearsay: fold with Influence-weighted credibility.
			a.processGossipSignal(sig, observers)
		}
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

// processGossipSignal folds a hearsay/gossip signal with Influence-weighted credibility
// (gap-closure §I-fold). signalWeight = clamp01(trust · (1 + InfluenceWeight · Influence)).
func (a *Agent) processGossipSignal(sig core.Signal, observers []tom.Belief) {
	sourceID := sig.Source
	if sourceID == "" {
		return
	}
	// The subject is who the gossip is ABOUT (may differ from the source).
	subjectID := sig.Target
	if subjectID == "" {
		subjectID = sourceID
	}

	// Get source's trust from this agent's ToM.
	sourceBelief, ok := a.ToM.Self(sourceID)
	if !ok || sourceBelief.Trust <= 0 {
		return // ignore unknown / untrusted sources
	}
	trust := clamp01(sourceBelief.Trust)

	// Compute Influence of the source over the Knowledge function (the credibility function
	// for generic gossip — the function that gossip typically concerns). D7: FuncKnowledge
	// is defined in core and is the content-canonical gossip credibility function.
	influence := a.ToM.Influence(sourceID, core.FuncKnowledge, observers)
	signalWeight := clamp01(trust * (1.0 + a.Cfg.InfluenceWeight*influence))

	// Get the source's belief about the subject for GossipUpdate.
	// sourceBeliefAboutSubject: what the source believes about the subject.
	var subjectBelief tom.Belief
	if sourceID == subjectID {
		subjectBelief = sourceBelief
	} else {
		subjectBelief, _ = a.ToM.Self(subjectID)
	}

	a.ToM.GossipUpdate(subjectID, subjectBelief, signalWeight)
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
