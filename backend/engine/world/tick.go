package world

import (
	"sort"

	"github.com/dogring/bdg/engine/actions"
	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/perception"
	"github.com/dogring/bdg/engine/rng"
	"github.com/dogring/bdg/engine/stats"
	"github.com/dogring/bdg/engine/tom"
)

// ── Tick: the 4-phase loop (D12 read → plan → collect → apply) ─────────────────

// Tick advances the simulation by exactly ONE tick. It is deterministic: same
// (world state, root rng state, Config, Services, content) → byte-identical state
// and event sequence.
func (w *World) Tick() {
	// ── Phase 1: READ (snapshot) ──────────────────────────────────────────
	w.currentSnap = newSnapshot(w)

	// ── Phase 2: PLAN (per-agent Tick, read-only on shared state) ─────────
	// Advance root RNG once for this tick's plan phase (deterministic anchor).
	planSeed := int64(w.rootRNG.Float64() * 1e15)

	var allIntents []agent.Intent
	for i, agentID := range w.agentIDs {
		a := w.agents[agentID]
		fork := rng.New(planSeed + int64(i))
		intents := a.Tick(w.currentSnap, w.tick, fork, w.svc, w.emit)
		allIntents = append(allIntents, intents...)
	}

	// ── Phase 3: COLLECT (stable-sort by AgentID, D12) ────────────────────
	sort.SliceStable(allIntents, func(i, j int) bool {
		return string(allIntents[i].Agent) < string(allIntents[j].Agent)
	})

	// ── Phase 4: APPLY (serial, sorted AgentID order, D12) ────────────────
	// Advance root RNG once for this tick's apply phase.
	applySeed := int64(w.rootRNG.Float64() * 1e15)

	// Collect sound events emitted this tick.
	var newSounds []perception.SoundEvent

	// Track conflicting targets: target → []intentIndex.
	conflictGroups := w.buildConflictGroups(allIntents)

	for i, intent := range allIntents {
		if intent.Kind == agent.IntentNone {
			continue
		}
		if intent.Kind == agent.IntentSignal {
			w.applySignal(intent)
			continue
		}

		fork := rng.New(applySeed + int64(i))

		// Resolve conflicts for this intent.
		outcomeStatus := agent.Succeeded
		if w.isConflictLoser(i, intent, conflictGroups, allIntents) {
			outcomeStatus = agent.Interrupted
		}

		// Build the outcome.
		outcome := w.resolveOutcome(intent, outcomeStatus, fork)

		// Apply world state changes for successful intents.
		if outcome.Status == agent.Succeeded {
			w.applyIntent(intent, outcome, &newSounds)
		}

		// Deliver outcome back to the agent.
		a := w.agents[intent.Agent]
		if a != nil {
			a.ApplyOutcome(outcome, fork, a.Cfg, w.svc.Stats, w.emit)
		}
	}

	// ── Post-apply ────────────────────────────────────────────────────────
	w.tick++
	w.currentSounds = newSounds
	w.relianceScan()

	// Emit TickDone.
	w.emitTickDone()

	// Emit SnapshotReady every BackupEveryTicks ticks.
	if w.cfg.BackupEveryTicks > 0 && int(w.tick)%w.cfg.BackupEveryTicks == 0 {
		w.emitSnapshotReady()
	}
}

// ── Conflict resolution ───────────────────────────────────────────────────────

// conflictKey is the resource target for conflict detection.
type conflictKey string

// buildConflictGroups groups intents by their target for conflict detection.
// Only intents targeting the same resource (same target ObjectID) conflict.
func (w *World) buildConflictGroups(intents []agent.Intent) map[conflictKey][]int {
	groups := make(map[conflictKey][]int)
	for i, intent := range intents {
		if intent.Kind == agent.IntentNone || intent.Kind == agent.IntentSignal {
			continue // signals don't conflict over resources
		}
		key := conflictKey(intent.Target)
		if key == "" {
			continue // no target → no conflict
		}
		groups[key] = append(groups[key], i)
	}
	return groups
}

// isConflictLoser checks whether the intent at index i loses a conflict.
// Winner: highest relevant Real Stat; tie-break by lower AgentID (D12).
func (w *World) isConflictLoser(i int, intent agent.Intent, groups map[conflictKey][]int, allIntents []agent.Intent) bool {
	key := conflictKey(intent.Target)
	indices, ok := groups[key]
	if !ok || len(indices) <= 1 {
		return false // no conflict
	}

	// Find the winner among the group.
	winnerIdx := indices[0]
	winnerStat := w.conflictStat(allIntents[winnerIdx])

	for _, idx := range indices[1:] {
		otherStat := w.conflictStat(allIntents[idx])
		if otherStat > winnerStat {
			winnerIdx = idx
			winnerStat = otherStat
		} else if otherStat == winnerStat {
			// Tie-break by lower AgentID (D12).
			if string(allIntents[idx].Agent) < string(allIntents[winnerIdx].Agent) {
				winnerIdx = idx
				winnerStat = otherStat
			}
		}
	}

	return i != winnerIdx
}

// conflictStat returns the relevant Real Stat for conflict resolution.
// Derived from the action's uses:<StatID> tags (D4).
func (w *World) conflictStat(intent agent.Intent) float64 {
	a, ok := w.agents[intent.Agent]
	if !ok {
		return 0
	}
	actDef, ok := w.actReg.Get(intent.Action)
	if !ok {
		return 0
	}
	statIDs := statsFromTags(actDef.Tags, w.svc.Stats)
	if len(statIDs) == 0 {
		// Fallback: use the alphabetically-first capability stat.
		caps := w.svc.Stats.Kinds(stats.Capability)
		if len(caps) > 0 {
			return a.RealStats.Get(caps[0])
		}
		return 0
	}
	return composeStat(a.RealStats, statIDs)
}

// ── Outcome resolution ────────────────────────────────────────────────────────

// resolveOutcome determines the outcome of an intent against the agent's RealStats
// (D8 — the ONLY place RealStats are read for outcome decisions).
func (w *World) resolveOutcome(intent agent.Intent, status agent.OutcomeStatus, fork *rng.RNG) agent.ActionOutcome {
	_ = fork // reserved for stochastic outcomes

	a := w.agents[intent.Agent]
	if a == nil {
		return agent.ActionOutcome{Action: intent.Action, Status: agent.Failed}
	}

	actDef, ok := w.actReg.Get(intent.Action)
	if !ok {
		return agent.ActionOutcome{Action: intent.Action, Status: agent.Failed}
	}

	// If already Interrupted by conflict, return early.
	if status == agent.Interrupted {
		return agent.ActionOutcome{
			Action:    intent.Action,
			Status:    agent.Interrupted,
			Completed: false,
			StatsUsed: statsFromTags(actDef.Tags, w.svc.Stats),
		}
	}

	// Resolve against RealStats (D8).
	statIDs := statsFromTags(actDef.Tags, w.svc.Stats)
	effortLevel := effortLevelFromTags(actDef.Tags)
	difficulty := w.cfg.OutcomeDifficultyBase * (1.0 + effortLevel)

	var outcomeStatus agent.OutcomeStatus
	var actual float64
	if len(statIDs) == 0 {
		// No uses:X tag — no capability requirement; action always succeeds
		// (e.g., Eat, basic social actions).
		outcomeStatus = agent.Succeeded
		actual = 1.0
	} else {
		realLevel := composeStat(a.RealStats, statIDs)
		if realLevel >= difficulty {
			outcomeStatus = agent.Succeeded
			actual = clamp01(realLevel / difficulty)
		} else {
			outcomeStatus = agent.Failed
			actual = clamp01(realLevel / difficulty)
		}
	}

	// Check durative completion: has the action run its full duration?
	scaledDuration := w.scaleDuration(actDef.Duration, actDef.Tags, a)
	completed := a.Elapsed >= scaledDuration

	// Compute expected progress (from the agent's perspective, ToM[self]).
	expected := w.computeExpected(a, statIDs, difficulty)

	// Build evidence for self-calibration.
	evidence := w.buildEvidence(a, statIDs, actual, a.Elapsed)

	// Compute realized need deltas (Effect or consumed item supply, D9).
	effect := w.computeEffect(actDef)

	return agent.ActionOutcome{
		Action:    intent.Action,
		Status:    outcomeStatus,
		Completed: completed,
		StatsUsed: statIDs,
		Expected:  expected,
		Actual:    actual,
		Effect:    effect,
		Evidence:  evidence,
	}
}

// computeExpected derives the agent's pre-action expected progress from ToM[self] (D8).
func (w *World) computeExpected(a *agent.Agent, statIDs []core.StatID, difficulty float64) float64 {
	selfBelief, ok := a.ToM.Self(a.ToM.SelfID())
	if !ok {
		return 1.0 // neutral expectation: assume success
	}
	var sum float64
	var count int
	for _, sid := range statIDs {
		if sd, ok := selfBelief.EstStats[sid]; ok {
			sum += sd.Mean
			count++
		}
	}
	if count == 0 {
		return 1.0 // neutral expectation: no stat data → assume success
	}
	selfLevel := sum / float64(count)
	return clamp01(selfLevel / difficulty)
}

// buildEvidence constructs per-stat evidence from the outcome for β self-calibration.
func (w *World) buildEvidence(a *agent.Agent, statIDs []core.StatID, actual float64, elapsed core.GameMinutes) []tom.StatEvidence {
	// Evidence: for each used stat, attribute the observed value.
	// Observed = the stat value implied by the actual progress.
	var evidence []tom.StatEvidence
	for _, sid := range statIDs {
		// Map actual progress to an implied stat observation.
		// P1: observed ~ RealStat × actual (the world's attribution).
		realVal := a.RealStats.Get(sid)
		observed := realVal * actual
		if def, ok := w.svc.Stats.Def(sid); ok {
			observed = def.Clamp(observed)
		}
		evidence = append(evidence, tom.StatEvidence{
			Stat:     sid,
			Observed: observed,
			Weight:   clamp01(actual),
			Tick:     w.tick,
		})
	}
	return evidence
}

// computeEffect returns the need deltas from the action's Effect or EffectPerMinute (D9).
// P1: EffectPerMinute is applied as a flat per-tick delta (1 tick = 1 game-minute).
// Consumption actions (ConsumesItem) use their direct Effect field; full item-supply
// resolution (objects.yaml supply → inventory → consume) is deferred to a later phase.
func (w *World) computeEffect(actDef actions.ActionDef) map[core.Dimension]float64 {
	effect := make(map[core.Dimension]float64)
	for dim, val := range actDef.Effect {
		effect[dim] += val
	}
	for dim, val := range actDef.EffectPerMinute {
		effect[dim] += val // 1 tick = 1 game-minute in P1
	}
	return effect
}

// scaleDuration returns the stat/distance-scaled action duration in game-minutes.
// P1: uses base duration (scaling reserved for later tuning).
func (w *World) scaleDuration(baseDuration core.GameMinutes, tags []core.Tag, a *agent.Agent) core.GameMinutes {
	// P1: base duration, no scaling.
	_ = tags
	_ = a
	return baseDuration
}

// ── Apply intent to world state ────────────────────────────────────────────────

// applyIntent mutates world state for a successful intent.
func (w *World) applyIntent(intent agent.Intent, outcome agent.ActionOutcome, sounds *[]perception.SoundEvent) {
	a := w.agents[intent.Agent]
	if a == nil {
		return
	}

	actDef, ok := w.actReg.Get(intent.Action)
	if !ok {
		return
	}

	// Check for loud actions → emit sound events for next tick.
	for _, t := range actDef.Tags {
		if t == "noise:high" || t == "noise:med" {
			*sounds = append(*sounds, perception.SoundEvent{
				SourceID: core.ObjectID(intent.Agent),
				ActionID: intent.Action,
				Pos:      a.Pos,
			})
			break
		}
	}

	// Movement: update position in spatial hash.
	isMove := false
	for _, pred := range actDef.Produces {
		if pred == "at_target" {
			isMove = true
			break
		}
	}
	if isMove && (intent.Kind == agent.IntentStart || intent.Kind == agent.IntentContinue) {
		// Move toward intent.Move.
		newPos := a.Pos.Add(intent.Move.Sub(a.Pos).Scale(w.cfg.MoveSpeedPerTick))
		a.Pos = newPos
		w.spatial.Move(core.ObjectID(intent.Agent), newPos)
	}
}

// ── Trade signal handling (P2) ────────────────────────────────────────────────

// applySignal processes an IntentSignal for trade actions, updating ToM and
// emitting trade events. Offer signals store a pending offer; Accept/Reject
// resolve it and fire the appropriate RecordTrade* calls.
func (w *World) applySignal(intent agent.Intent) {
	sig := intent.Signal
	if sig == nil || sig.Toward == "" {
		return
	}

	senderID := intent.Agent
	receiverID := core.AgentID(sig.Toward)

	receiver := w.agents[receiverID]
	if receiver == nil {
		return
	}
	sender := w.agents[senderID]
	if sender == nil {
		return
	}

	tick := w.tick

	switch sig.Kind {
	case "Offer":
		// Store pending offer indexed by the intended receiver.
		w.pendingOffers[receiverID] = pendingOffer{
			Offeror:      senderID,
			ClaimedValue: sig.ClaimedValue,
			Truth:        sig.Truth,
			Tick:         tick,
		}
		w.emitTradeEvent(senderID, receiverID, "TradeOffered", sig.ClaimedValue, tick)

	case "Accept":
		// Both sides' Trust rises on trade completion.
		sender.ToM.RecordTradeSuccess(receiverID, tick)
		receiver.ToM.RecordTradeSuccess(senderID, tick)

		// If there was a pending offer, check for fraud (god-view: Truth is known).
		offer, hasOffer := w.pendingOffers[senderID]
		if hasOffer {
			sender.ToM.RecordFraud(offer.Offeror, offer.ClaimedValue, offer.Truth, "Honesty", tick)
			delete(w.pendingOffers, senderID)
		}

		w.emitTradeEvent(senderID, receiverID, "TradeCompleted", sig.ClaimedValue, tick)

	case "Reject":
		// Offeror's Affinity in the rejector's view drops.
		offer, hasOffer := w.pendingOffers[senderID]
		if hasOffer {
			sender.ToM.RecordTradeRejection(offer.Offeror, tick)
			delete(w.pendingOffers, senderID)
		} else {
			sender.ToM.RecordTradeRejection(receiverID, tick)
		}

		w.emitTradeEvent(senderID, receiverID, "TradeRejected", 0, tick)
	}
}

// emitTradeEvent emits a structured trade-protocol event.
func (w *World) emitTradeEvent(sender, receiver core.AgentID, eventType string, value float64, tick core.Tick) {
	w.emit.Emit(core.Event{
		SchemaVersion: 1,
		Tick:          tick,
		AgentID:       sender,
		Type:          eventType,
		Payload: map[string]any{
			"sender":   string(sender),
			"receiver": string(receiver),
			"value":    value,
		},
	})
}

// ── Event emission ─────────────────────────────────────────────────────────────

func (w *World) emitTickDone() {
	w.emit.Emit(core.Event{
		SchemaVersion: 1,
		Tick:          w.tick,
		AgentID:       "",
		Type:          "TickDone",
		Payload: map[string]any{
			"tick":         int64(w.tick),
			"agent_count":  len(w.agents),
			"intent_count": 0, // filled by caller
		},
	})
}

func (w *World) emitSnapshotReady() {
	w.emit.Emit(core.Event{
		SchemaVersion: 1,
		Tick:          w.tick,
		AgentID:       "",
		Type:          "SnapshotReady",
		Payload: map[string]any{
			"tick": int64(w.tick),
		},
	})
}

// ── Helpers ────────────────────────────────────────────────────────────────────

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
