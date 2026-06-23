package agent

import (
	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/planner"
	"github.com/dogring/bdg/engine/tom"
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

// updateResentment runs every tick from updateDynamics and applies the
// per-tick Aggression-Drift threshold check (§B-drift gap-closure). When
// Resentment exceeds the threshold, fold AggressionDrift × latentFactor into
// ToM[self] Aggression — even on ticks with NO new trigger events. The drift
// magnitude scales by the latent intensity so a fully-resolved agent (no latent
// goals) never drifts even if Resentment hasn't decayed yet.
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

	// Per-tick Aggression-Drift threshold check (gap-closure §B-drift).
	// Runs regardless of whether trigger events arrived this tick.
	if a.Resentment > a.Cfg.ResentmentThreshold {
		aggStatID := resolveAggressionStatID(a.Cfg)
		selfID := a.ToM.SelfID()
		currentAgg := a.perceivedStat(aggStatID)
		a.ToM.Observe(selfID, tom.StatEvidence{
			Stat:     aggStatID,
			Observed: currentAgg + a.Cfg.AggressionDrift*latentFactor,
			Weight:   0.5,
			Tick:     0,
		})
	}
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
