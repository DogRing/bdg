package world

import (
	"sort"
	"sync"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/mind/perception"
	"github.com/dogring/bdg/engine/mind/stats"
	"github.com/dogring/bdg/engine/mind/tom"
)

// ── Tick: the 4-phase loop (D12 read → plan → collect → apply) ─────────────────

// Tick advances the simulation by exactly ONE tick. It is deterministic: same
// (world state, root rng state, Config, Services, content) → byte-identical state
// and event sequence.
func (w *World) Tick() {
	// ── Phase 1: READ (snapshot) ──────────────────────────────────────────
	// Clear pending signals from the previous tick before taking the snapshot.
	// The snapshot freezes the cleared state; signals collected in this tick's
	// apply phase will be available through the NEXT tick's snapshot.
	w.pendingSignals = make(map[core.AgentID][]core.Signal)
	w.pendingFloraFrame = nil // WI-P4: this tick's flora_delta buffer (see renderframe.go)
	w.currentSnap = newSnapshot(w)

	// ── Phase 2: PLAN (per-agent Tick, read-only on shared state) ─────────
	// Advance root RNG once for this tick's plan phase (deterministic anchor).
	planSeed := int64(w.rootRNG.Float64() * 1e15)

	// Pre-allocate with estimated capacity (2 intents per agent is a safe upper bound).
	allIntents := make([]agent.Intent, 0, len(w.agentIDs)*2)

	var mu sync.Mutex
	var wg sync.WaitGroup

	// Iterate w.agentIDs (sorted, D12) to assign goroutines. Each goroutine
	// resolves its own agent reference from w.agents (read-only map access).
	for i, agentID := range w.agentIDs {

		// Time slicing (plan_interval): when PlanInterval > 1, only a subset
		// of agents plan each tick. Agents outside the current slice still
		// advance their current durative action by calling a.Tick (which is
		// read-only on shared state) but with a cheaper path (no replan).
		// The subset is: (i % PlanInterval) == (tick % PlanInterval).
		if w.cfg.PlanInterval > 1 && (i%w.cfg.PlanInterval) != (int(w.tick)%w.cfg.PlanInterval) {
			// This agent is outside the current planning slice.
			// Call a.Tick as normal — the agent will still execute its current
			// action (phase 6 of the agent loop) even if it doesn't replan.
			// Determinism preserved: same RNG fork regardless of slicing, so
			// an agent's own Tick outcome is independent of which tick slot it
			// occupies.
			wg.Add(1)
			go func(agentID core.AgentID, idx int) {
				defer wg.Done()
				fork := rng.New(planSeed + int64(idx))
				intents := w.agents[agentID].Tick(w.currentSnap, w.tick, fork, w.svc, w.emit)
				mu.Lock()
				allIntents = append(allIntents, intents...)
				mu.Unlock()
			}(agentID, i)
			continue
		}

		// Full planning path (or PlanInterval == 1, the default).
		wg.Add(1)
		go func(agentID core.AgentID, idx int) {
			defer wg.Done()
			fork := rng.New(planSeed + int64(idx))
			intents := w.agents[agentID].Tick(w.currentSnap, w.tick, fork, w.svc, w.emit)
			mu.Lock()
			allIntents = append(allIntents, intents...)
			mu.Unlock()
		}(agentID, i)
	}

	wg.Wait()

	animalIntents := w.planFaunaIntents()

	// ── Phase 3: COLLECT (stable-sort by AgentID, D12) ────────────────────
	sort.SliceStable(allIntents, func(i, j int) bool {
		return string(allIntents[i].Agent) < string(allIntents[j].Agent)
	})

	// ── Phase 4: APPLY (serial, sorted AgentID order, D12) ────────────────
	// Advance root RNG once for this tick's apply phase.
	applySeed := int64(w.rootRNG.Float64() * 1e15)

	// Collect sound events emitted this tick.
	var newSounds []perception.SoundEvent

	if w.faunaInstalled() {
		w.applyCombinedIntents(allIntents, animalIntents, applySeed, &newSounds)

		// ── Phase 4-ENV: APPLY env modules (serial, fixed order) ──────────────
		w.runEnvPhase()
		w.runRespawn()

		// ── Post-apply ────────────────────────────────────────────────────────
		w.tick++
		w.currentSounds = newSounds
		w.resentmentTriggers = w.conflictResentmentTriggers(allIntents, w.buildConflictGroups(allIntents))
		w.relianceScan()
		w.pruneToMBeliefs()
		w.emitTickDone()
		if w.cfg.BackupEveryTicks > 0 && int(w.tick)%w.cfg.BackupEveryTicks == 0 {
			w.emitSnapshotReady()
		}
		return
	}

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
			a.ApplyOutcome(outcome, w.tick, fork, a.Cfg, w.svc.Stats, w.emit)
		}
	}

	// ── Phase 4-ENV: APPLY env modules (serial, fixed order) ──────────────
	w.runEnvPhase()

	// ── Post-apply ────────────────────────────────────────────────────────
	w.tick++
	w.currentSounds = newSounds
	// P3: record this tick's resource-conflict losers → winners, for the NEXT
	// tick's plan phase to surface via WorldView.ResentmentTriggers.
	w.resentmentTriggers = w.conflictResentmentTriggers(allIntents, conflictGroups)
	w.relianceScan()

	// ToM prune: remove stale beliefs (memory bounding for long runs).
	// Never prunes if PruneThreshold == 0 (current behaviour).
	w.pruneToMBeliefs()

	// Emit TickDone.
	w.emitTickDone()

	// Emit SnapshotReady every BackupEveryTicks ticks.
	if w.cfg.BackupEveryTicks > 0 && int(w.tick)%w.cfg.BackupEveryTicks == 0 {
		w.emitSnapshotReady()
	}
}

// pruneToMBeliefs prunes stale ToM beliefs for every agent. Never prunes
// self-belief (D8). Skips if PruneThreshold == 0 (default = never prune).
func (w *World) pruneToMBeliefs() {
	if w.cfg.PruneThreshold <= 0 {
		return
	}
	maxAge := core.Tick(w.cfg.PruneThreshold)
	// Keep at least 5 beliefs for social context.
	const minKeep = 5
	for _, agentID := range w.agentIDs {
		a := w.agents[agentID]
		if a == nil {
			continue
		}
		a.ToM.PruneBeliefs(w.tick, maxAge, minKeep)
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
	return i != w.conflictWinnerIdx(indices, allIntents)
}

// conflictResentmentTriggers maps each resource-conflict loser to the winner(s)
// that beat them this tick (P3). The world reports a trigger for EVERY loser;
// only Latent agents act on it (agent.accrueResentment — separation of D2/D5).
// Deterministic: built in allIntents order (already AgentID-sorted), winners via
// conflictWinnerIdx (D12). Returns an empty map when no conflicts occurred.
func (w *World) conflictResentmentTriggers(allIntents []agent.Intent, groups map[conflictKey][]int) map[core.AgentID][]core.AgentID {
	triggers := make(map[core.AgentID][]core.AgentID)
	for i, intent := range allIntents {
		if !w.isConflictLoser(i, intent, groups, allIntents) {
			continue
		}
		indices := groups[conflictKey(intent.Target)]
		winner := allIntents[w.conflictWinnerIdx(indices, allIntents)].Agent
		triggers[intent.Agent] = append(triggers[intent.Agent], winner)
	}
	return triggers
}

// conflictWinnerIdx returns the index (into allIntents) of the winner among a
// conflict group. Winner: highest relevant Real Stat; tie-break by lower
// AgentID (D12). Pure over the group ordering, so the verdict is deterministic.
func (w *World) conflictWinnerIdx(indices []int, allIntents []agent.Intent) int {
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

	return winnerIdx
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

	// Check durative completion.
	var completed bool
	if isLocomotion(actDef.Produces) {
		// Locomotion (MoveTo/Approach) ends on ARRIVAL — when the agent is within
		// ArrivalEpsilon of its destination (Intent.Move) — so travel time scales with
		// distance (D11). a.Pos here is the PRE-move position for this tick (applyIntent
		// runs after resolveOutcome). The distance-derived cap guarantees termination so
		// a moving / unreachable target can never freeze the agent.
		dist := a.Pos.Distance(intent.Move)
		travelCap := core.GameMinutes(dist) + actDef.Duration + 1
		completed = dist <= w.cfg.ArrivalEpsilon || a.Elapsed >= travelCap
	} else {
		completed = a.Elapsed >= w.scaleDuration(actDef.Duration, actDef.Tags, a)
	}

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

	// Movement: a locomotion action (MoveTo/Approach, tag time:by_distance) steps the
	// agent a fraction of the remaining distance toward its destination (intent.Move).
	if isLocomotion(actDef.Produces) && (intent.Kind == agent.IntentStart || intent.Kind == agent.IntentContinue) {
		newPos := a.Pos.Add(intent.Move.Sub(a.Pos).Scale(w.cfg.MoveSpeedPerTick))
		a.Pos = newPos
		w.spatial.Move(core.ObjectID(intent.Agent), newPos)
	}
}

// isLocomotion reports whether an action is a travel action — one whose effect is a
// positional predicate: MoveTo (produces at_target) or Approach (produces near_other).
// engine/agent keys Intent.Move binding on the same signal, so movement, arrival-based
// completion, and destination binding stay in agreement.
func isLocomotion(produces []core.Pred) bool {
	for _, p := range produces {
		if p == "at_target" || p == "near_other" {
			return true
		}
	}
	return false
}

// ── Trade signal handling (P2) ────────────────────────────────────────────────

// applySignal processes an IntentSignal for trade actions and P6 Vote signals,
// updating ToM and emitting events. Trade signals (Offer/Accept/Reject) are
// directed at a specific receiver (sig.Toward); Vote signals are broadcast to
// every agent.
func (w *World) applySignal(intent agent.Intent) {
	sig := intent.Signal
	if sig == nil {
		return
	}

	senderID := intent.Agent
	tick := w.tick

	// Handle Vote signals separately — they broadcast, not directed at one receiver.
	if sig.Kind == "Vote" {
		w.applyVoteSignal(senderID, sig, tick)
		return
	}

	// Trade signals: require a specific receiver.
	if sig.Toward == "" {
		return
	}

	receiverID := core.AgentID(sig.Toward)
	receiver := w.agents[receiverID]
	if receiver == nil {
		return
	}
	sender := w.agents[senderID]
	if sender == nil {
		return
	}

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

// applyVoteSignal processes a Vote signal by broadcasting it to all agents.
// For each receiver agent (excluding the voter), it:
//  1. Directly adjusts the receiver's RelyOn toward the voted holder via AdjustRelyOn
//  2. Stores the signal in pendingSignals for agent-level processing (IncomingSignals)
//
// The voted holder is sig.Target; the delegated Function is sig.Function.
// The delta applied is from the world's config (VoteRelyOnDelta).
func (w *World) applyVoteSignal(senderID core.AgentID, sig *agent.Signal, tick core.Tick) {
	votedHolder := sig.Target
	if votedHolder == "" {
		return
	}
	fn := sig.Function
	if fn == "" {
		return
	}

	// Build the core.Signal for pendingSignals.
	coreSig := core.Signal{
		Kind:      core.SignalVote,
		Intensity: sig.Intensity,
		Function:  fn,
		Source:    senderID,
		Target:    votedHolder,
	}

	// Emit a VoteEvent for observability.
	w.emit.Emit(core.Event{
		SchemaVersion: 1,
		Tick:          tick,
		AgentID:       senderID,
		Type:          "VoteEmitted",
		Payload: map[string]any{
			"voter":     string(senderID),
			"target":    string(votedHolder),
			"function":  string(fn),
			"intensity": sig.Intensity,
		},
	})

	// Broadcast to all agents (excluding the voter).
	for _, agentID := range w.agentIDs {
		if agentID == senderID {
			continue
		}
		receiver := w.agents[agentID]
		if receiver == nil {
			continue
		}

		// Directly adjust RelyOn toward the voted holder using the receiver's
		// configured VoteRelyOnDelta.
		receiver.ToM.AdjustRelyOn(votedHolder, fn, receiver.Cfg.VoteRelyOnDelta)

		// Store in pendingSignals for agent-level processing (IncomingSignals).
		w.pendingSignals[agentID] = append(w.pendingSignals[agentID], coreSig)
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
	// TickDone is the per-tick render frame for the god-view: it carries every
	// agent's current pos/goal/mood/action so the frontend animates movement from
	// the SSE stream (no snapshot polling). Iterated in sorted agent-ID order (D12).
	// This event is dropped from the Postgres why-trace and stderr log (operational).
	agents := make([]map[string]any, 0, len(w.agentIDs))
	for _, id := range w.agentIDs {
		a := w.agents[id]
		if a == nil {
			continue
		}
		action := ""
		if a.PlanIdx >= 0 && a.PlanIdx < len(a.Plan.Actions) {
			action = string(a.Plan.Actions[a.PlanIdx])
		}
		agents = append(agents, map[string]any{
			"id":     string(a.ID),
			"pos":    a.Pos, // core.Vec2 → {"x","y"}
			"goal":   string(a.Goal),
			"mood":   a.Mood,
			"action": action,
		})
	}
	w.emit.Emit(core.Event{
		SchemaVersion: 1,
		Tick:          w.tick,
		AgentID:       "",
		Type:          "TickDone",
		Payload: map[string]any{
			"tick":         int64(w.tick),
			"agent_count":  len(w.agents),
			"intent_count": 0, // filled by caller
			"agents":       agents,
		},
	})

	// WI-P4: the real WorldFrame, built from live env state (renderframe.go).
	// No-op when env is OFF (data-contracts §4).
	w.emitWorldFrame()
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
