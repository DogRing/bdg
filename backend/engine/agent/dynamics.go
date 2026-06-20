package agent

import (
	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/planner"
)

// ── Phase 8: dynamics ──────────────────────────────────────────────────────────

// updateDynamics updates Adrenaline (surge/crash), Mood (pull toward baseline),
// Resentment drift from latent goals, and the crash stamina debt (P3).
func (a *Agent) updateDynamics(priorities []planner.DimensionPriority) {
	// Compute current Urgency.
	maxSalience := 0.0
	for _, p := range priorities {
		if float64(p.Salience) > maxSalience {
			maxSalience = float64(p.Salience)
		}
	}
	urgency := clamp01(maxSalience * a.Cfg.UrgencyFromDeficit)

	// Adrenaline surge / crash.
	if urgency > a.Cfg.AdrTriggerUrgency {
		a.Adrenaline = clamp(a.Adrenaline+a.Cfg.AdrSurge, 0, a.Cfg.AdrMax)
	} else {
		// Crash: drain toward 0 with stamina debt (P3).
		a.Adrenaline = clamp(a.Adrenaline-a.Cfg.AdrDecay, 0, a.Cfg.AdrMax)
		// Mood dip from adrenaline crash (λ × decay as penalty).
		a.Mood += a.Cfg.Lambda * (-a.Cfg.AdrDecay)
		// NEW P3: crash stamina debt — adrenaline crash drains Stamina.
		a.Stamina = clamp(a.Stamina-a.Cfg.AdrDecay*a.Cfg.CrashStaminaPenalty, 0, a.Cfg.StaminaMax)
	}

	// Mood: pull toward baseline.
	a.Mood += a.Cfg.MoodDecay * (a.Cfg.MoodBaseline - a.Mood)

	// Resentment drift from latent goals (P1/P2 path — updated in P3 via accrueResentment).
	a.updateResentment()
}

// ── Resentment drift ───────────────────────────────────────────────────────────

// updateResentment applies the slow Aggression drift and Affinity erosion from
// any latent (unmet) goals. This is emergent, not hardcoded (D2).
// P3: the actual persisted Affinity writes now route through accrueResentment
// which calls tom.AdjustAffinity. This function handles the per-tick latent
// intensity-based drift.
func (a *Agent) updateResentment() {
	if len(a.Latent) == 0 {
		return
	}

	// Accumulate latent intensity.
	var totalLatentIntensity float64
	for _, lg := range a.Latent {
		totalLatentIntensity += lg.Intensity
	}
	latentFactor := clamp01(totalLatentIntensity)

	// Slow aggression drift (D2: resentment is emergent, no "grudge" type).
	// The drift is applied within accrueResentment when trigger events occur.
	// Here we apply the baseline passive drift from latent goals alone.
	_ = latentFactor // reserved for future passive drift expansion
}

// ── Event emission helpers ─────────────────────────────────────────────────────

func emitGoalSelected(emit core.EventEmitter, now core.Tick, agentID core.AgentID, trace planner.Trace) {
	emit.Emit(core.Event{
		SchemaVersion: 1,
		Tick:          now,
		Seq:           0, // seq assigned by platform
		AgentID:       agentID,
		Type:          "GoalSelected",
		Payload: map[string]any{
			"dimension":  string(trace.GoalDim),
			"priority":   float64(0),
			"eff_value":  float64(0),
			"total_cost": trace.TotalCost,
		},
	})
}

func emitPlanBuilt(emit core.EventEmitter, now core.Tick, agentID core.AgentID, trace planner.Trace) {
	steps := make([]string, len(trace.Candidates))
	for i, c := range trace.Candidates {
		if c.Chosen {
			steps[i] = string(c.Action)
		}
	}
	emit.Emit(core.Event{
		SchemaVersion: 1,
		Tick:          now,
		Seq:           0,
		AgentID:       agentID,
		Type:          "PlanBuilt",
		Payload: map[string]any{
			"steps":       steps,
			"total_cost":  trace.TotalCost,
			"provisioned": trace.Provisioned,
			"relaxed":     trace.Relaxed,
		},
	})
}

// emitCopingEntered emits a CopingEntered event for the why-trace.
// P3: emits canonical mode strings per data-contracts §4, not int(state).
func emitCopingEntered(emit core.EventEmitter, now core.Tick, agentID core.AgentID, state CopingState) {
	mode := copingModeString(state)
	emit.Emit(core.Event{
		SchemaVersion: 1,
		Tick:          now,
		Seq:           0,
		AgentID:       agentID,
		Type:          "CopingEntered",
		Payload: map[string]any{
			"mode": mode,
		},
	})
}

// copingModeString returns the canonical mode string for a CopingState (data-contracts §4).
func copingModeString(state CopingState) string {
	switch state {
	case Idle:
		return "idle"
	case Rebinding:
		return "rebind"
	case Longing:
		return "longing"
	case Latent:
		return "latent"
	case Apathy:
		return "apathy"
	default:
		return "unknown"
	}
}
