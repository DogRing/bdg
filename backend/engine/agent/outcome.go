package agent

import (
	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/rng"
	"github.com/dogring/bdg/engine/stats"
)

// ── ApplyOutcome: fold resolved action feedback back into the agent ─────────────

// ApplyOutcome folds the world's resolved outcome of this agent's just-applied action
// back into the agent: it advances/completes/interrupts the durative action, triggers
// per-stat self-calibration (β, D8), updates Mood from actual-vs-expected progress,
// applies realized need deltas, folds direct-observation evidence into ToM, and — on a
// completed goal — resets to Idle.
//
// P3 additions:
//   - FailStreak resets to 0 on any success (Succeeded + Completed).
//   - Apathy recovery: Coping==Apathy + Succeeded+Completed → Coping=Idle, FailStreak=0,
//     Mood += ApathyRecoverMood.
//
// It runs in the world's serial, fixed-AgentID apply phase (D12), AFTER all Tick
// intents are collected, so no two agents' updates interleave nondeterministically.
func (a *Agent) ApplyOutcome(outcome ActionOutcome, now core.Tick, rng *rng.RNG, cfg Config, reg *stats.Registry, emit core.EventEmitter) {
	_ = rng  // reserved for future disposition-perturbed responses
	_ = reg  // reserved for stat range clamping during evidence fold

	// ── 1. Handle durative action completion / interruption ──────────────────
	if outcome.Completed || outcome.Status == Interrupted {
		if outcome.Status == Interrupted {
			// DISCARD partial progress (P1 policy, design §4 OQ).
			a.Elapsed = 0
		}
		// Advance to the next action in the plan (if any).
		a.PlanIdx++
		a.Elapsed = 0

		// Emit ActionDone event.
		emitActionDone(emit, now, a.ID, outcome)
	}

	// ── 2. β self-calibration: fold evidence into ToM[self] (D8) ──────────
	// On Invisible: self-sealing — NO evidence folded (handled inside foldEvidence).
	a.foldEvidence(outcome)

	// ── 3. Mood update from actual-vs-expected progress ────────────────────
	// Mood += λ · (actual − expected)
	a.Mood += cfg.Lambda * (outcome.Actual - outcome.Expected)

	// ── 4. Apply realized need deltas (Effect) ─────────────────────────────
	// Deltas DECREASE intensity (satisfy the need).
	for dim, delta := range outcome.Effect {
		a.NeedIntensities[dim] = clamp(a.NeedIntensities[dim]-delta, 0, 1e9)
	}

	// ── 5. FailStreak reset on any success (P3) ───────────────────────────
	if outcome.Status == Succeeded && outcome.Completed {
		a.FailStreak = 0

		// Apathy recovery: single success pulls agent out of Apathy (P3).
		if a.Coping == Apathy {
			a.Coping = Idle
			a.Mood += cfg.ApathyRecoverMood
			if emit != nil {
				emitCopingEntered(emit, now, a.ID, Idle)
			}
		}
	}

	// ── 6. Handle completed goal → reset to Idle ──────────────────────────
	if outcome.Completed && a.PlanIdx >= len(a.Plan.Actions) {
		a.clearGoal()
		a.Coping = Idle
		a.Adrenaline = 0 // reset adrenaline on goal completion
	}
}

// ── Event emission helpers ─────────────────────────────────────────────────────

func emitActionDone(emit core.EventEmitter, now core.Tick, agentID core.AgentID, outcome ActionOutcome) {
	emit.Emit(core.Event{
		SchemaVersion: 1,
		Tick:          now,
		Seq:           0,
		AgentID:       agentID,
		Type:          "ActionDone",
		Payload: map[string]any{
			"action":    string(outcome.Action),
			"status":    int(outcome.Status),
			"completed": outcome.Completed,
			"actual":    outcome.Actual,
			"expected":  outcome.Expected,
		},
	})
}
