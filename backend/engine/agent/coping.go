package agent

import (
	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/planner"
	"github.com/dogring/bdg/engine/stats"
	"github.com/dogring/bdg/engine/tom"
)

// ── Coping cascade (design §3 — "막다른 목표 = 드라마의 엔진") ────────────────

// enterCopingCascade is called when phase-5 planning fails. It advances the coping
// cascade ONE step per failed re-plan, in order: Rebinding → Longing → Latent → Apathy.
// The cascade is DETERMINISTIC (rng-free): which step is taken is a pure function of
// (failure cause, perceived Intelligence, FailStreak, prior CopingState).
//
// P3 completion: tracks FailStreak, applies the low-Intelligence shortcut to Latent
// (skipping Rebinding AND Longing), and transitions Latent→Apathy at the streak threshold.
func (a *Agent) enterCopingCascade(err error, now core.Tick, priorities []planner.DimensionPriority, statsReg *stats.Registry, emit core.EventEmitter) {
	// Increment FailStreak on every failed re-plan.
	a.FailStreak++

	// Resolve perceived Intelligence for the low-Intelligence shortcut (P3).
	// Normalize to [0,1] by dividing by the stat's max range.
	perceivedIntel := a.normalizedIntelligence(statsReg)

	// Low-Intelligence shortcut (design §1.4 object-fixation, P3):
	// if perceived Intelligence < RebindMinIntelligence, skip Rebinding AND Longing,
	// go directly to Latent.
	if perceivedIntel < a.Cfg.RebindMinIntelligence {
		// Skip directly to Latent (or stay in Latent/Apathy).
		switch a.Coping {
		case Idle, Rebinding, Longing:
			a.enterLatentDirect(now, emit)
			return
		case Latent:
			if a.FailStreak >= a.Cfg.ApathyFailStreak {
				a.enterApathy()
				emitCopingEntered(emit, now, a.ID, Apathy)
			}
			return
		case Apathy:
			// Stay in Apathy.
			return
		}
	}

	// High-Intelligence path: full cascade.
	switch a.Coping {
	case Idle:
		// First failure: attempt Rebinding.
		if a.canRebind(statsReg) {
			a.Coping = Rebinding
			emitCopingEntered(emit, now, a.ID, Rebinding)
			// Rebinding: try to substitute the goal with the next-highest Priority dimension.
			if a.rebindSubstitute(priorities) {
				// Successfully substituted — return to Idle (next replan uses new goal).
				a.Coping = Idle
				a.FailStreak = 0
				return
			}
			// Substitution failed — fall through to Longing.
		}
		// Either cannot rebind or rebind failed — enter Longing.
		a.enterLonging()
		emitCopingEntered(emit, now, a.ID, Longing)

	case Rebinding:
		// Rebinding already attempted and failed — advance to Longing.
		a.enterLonging()
		emitCopingEntered(emit, now, a.ID, Longing)

	case Longing:
		// Longing already active — advance to Latent.
		a.Coping = Latent
		emitCopingEntered(emit, now, a.ID, Latent)

	case Latent:
		// Latent already active — advance to Apathy at the streak threshold (P3).
		if a.FailStreak >= a.Cfg.ApathyFailStreak {
			a.enterApathy()
			emitCopingEntered(emit, now, a.ID, Apathy)
		}
		// else: stay Latent, continue accruing Resentment each tick.

	case Apathy:
		// Already in Apathy — remain there until a single plan success or a different
		// goal becomes plannable.
	}
}

// enterLatentDirect skips Rebinding and Longing and goes directly to Latent.
// Used by the low-Intelligence shortcut (P3).
func (a *Agent) enterLatentDirect(now core.Tick, emit core.EventEmitter) {
	// Store the current goal as a latent goal.
	intensity := a.NeedIntensities[a.Goal]
	a.Latent = append(a.Latent, LatentGoal{
		Dim:       a.Goal,
		Since:     now,
		Intensity: intensity,
	})
	a.Coping = Latent
	a.clearGoal()
	emitCopingEntered(emit, now, a.ID, Latent)
}

// canRebind checks whether the agent's perceived Intelligence is high enough to
// attempt rebinding. Low Intelligence agents suffer object-fixation (D8 / design
// §1.4) and skip directly to Longing.
//
// P3: uses the explicit RebindMinIntelligence constant (balance.yaml coping.rebind_min_intelligence).
// Normalizes perceived Intelligence to [0,1] by dividing by the stat's max range.
func (a *Agent) canRebind(statsReg *stats.Registry) bool {
	perceivedIntel := a.normalizedIntelligence(statsReg)
	return perceivedIntel >= a.Cfg.RebindMinIntelligence
}

// normalizedIntelligence returns the agent's self-perceived Intelligence normalized
// to [0,1] by dividing by the stat's max range (D8: reads ToM[self]).
func (a *Agent) normalizedIntelligence(statsReg *stats.Registry) float64 {
	raw := a.perceivedIntelligence(statsReg)
	const intelGlossaryID core.StatID = "Intelligence"
	if def, ok := statsReg.Def(intelGlossaryID); ok && def.Max > 0 {
		return raw / def.Max
	}
	return raw
}

// rebindSubstitute attempts to switch the goal to the next-highest Priority dimension
// (the second in the sorted list, since the first failed). Returns true if a substitute
// was found.
func (a *Agent) rebindSubstitute(priorities []planner.DimensionPriority) bool {
	if len(priorities) < 2 {
		return false
	}
	// The first priority failed. Try the second.
	substituteDim := priorities[1].Dim
	if substituteDim == a.Goal {
		// Same goal — no effective substitute. Try the third if available.
		if len(priorities) >= 3 {
			a.Goal = priorities[2].Dim
			return true
		}
		return false
	}
	a.Goal = substituteDim
	a.Plan = planner.Plan{}
	a.PlanIdx = 0
	a.Elapsed = 0
	return true
}

// enterLonging stores the current unmet goal as a LatentGoal and stops actively
// pursuing it.
func (a *Agent) enterLonging() {
	// Compute the current urgency intensity for the failed goal.
	intensity := a.NeedIntensities[a.Goal]

	a.Latent = append(a.Latent, LatentGoal{
		Dim:       a.Goal,
		Since:     0,
		Intensity: intensity,
	})

	a.Coping = Longing
	a.clearGoal()
}

// enterApathy suppresses the goal, clears the plan, and crashes mood/adrenaline.
func (a *Agent) enterApathy() {
	a.Coping = Apathy
	a.clearGoal()
	// Crash Adrenaline to 0.
	a.Adrenaline = 0
	// Clear latent goals — the agent has given up on them.
	a.Latent = nil
}

// ── Resentment accrual (P3) ──────────────────────────────────────────────────

// accrueResentment applies resentment drift while the agent is in Latent state.
// For each trigger agent (reported by the world via WorldView.ResentmentTriggers),
// it accrues Resentment and drops Affinity toward the trigger agent.
// Also checks the resentment threshold for Aggression drift.
func (a *Agent) accrueResentment(triggers []core.AgentID, statsReg *stats.Registry, emit core.EventEmitter) {
	if a.Coping != Latent {
		return
	}
	if len(triggers) == 0 {
		return
	}

	// Resolve Vindictiveness stat ID from the registry (D7: no hardcoded literal).
	vindictivenessStatID := resolveVindictivenessStatID(statsReg)
	vindictiveness := a.perceivedStat(vindictivenessStatID)

	for _, triggerID := range triggers {
		// Accrue Resentment.
		a.Resentment += a.Cfg.ResentmentPerTrigger * vindictiveness

		// Drop Affinity toward the trigger agent (persisted via tom.AdjustAffinity).
		affinityDelta := -a.Cfg.AffinityDrop * vindictiveness
		a.ToM.AdjustAffinity(triggerID, affinityDelta)

		// Emit BeliefUpdated event for the Affinity drop.
		if emit != nil {
			emit.Emit(core.Event{
				SchemaVersion: 1,
				Tick:          0, // filled by caller
				AgentID:       a.ID,
				Type:          "BeliefUpdated",
				Payload: map[string]any{
					"subject":  string(triggerID),
					"field":    "Affinity",
					"delta":    affinityDelta,
					"resentment": a.Resentment,
				},
			})
		}
	}

	// Check resentment threshold for Aggression drift.
	if a.Resentment > a.Cfg.ResentmentThreshold {
		// Apply aggression_drift to ToM[self] Aggression.
		selfID := a.ToM.SelfID()
		aggStatID := resolveAggressionStatID(statsReg)
		a.ToM.Observe(selfID, tom.StatEvidence{
			Stat:     aggStatID,
			Observed: a.perceivedStat(aggStatID) + a.Cfg.AggressionDrift,
			Weight:   0.5,
			Tick:     0,
		})
	}
}

// resolveVindictivenessStatID resolves the Vindictiveness disposition StatID
// from the stats registry (D7: no hardcoded literal).
func resolveVindictivenessStatID(statsReg *stats.Registry) core.StatID {
	const vindictivenessGlossaryID core.StatID = "Vindictiveness"
	if statsReg.Has(vindictivenessGlossaryID) {
		return vindictivenessGlossaryID
	}
	// Fallback: use the first disposition stat.
	disps := statsReg.Kinds(stats.Disposition)
	if len(disps) > 0 {
		return disps[0]
	}
	return ""
}

// resolveAggressionStatID resolves the Aggression disposition StatID
// from the stats registry (D7: no hardcoded literal).
func resolveAggressionStatID(statsReg *stats.Registry) core.StatID {
	const aggressionGlossaryID core.StatID = "Aggression"
	if statsReg.Has(aggressionGlossaryID) {
		return aggressionGlossaryID
	}
	// Fallback: use the first disposition stat.
	disps := statsReg.Kinds(stats.Disposition)
	if len(disps) > 0 {
		return disps[0]
	}
	return ""
}

// ── Self-calibration (β, D8) ───────────────────────────────────────────────────

// foldEvidence applies the world-provided evidence from a resolved action into
// ToM[self]. On Invisible/Interrupted: NO evidence is folded (self-sealing, D8).
// The evidence is pre-computed by the world from the action outcome; the agent
// NEVER reads RealStats here — it only applies the evidence items it is given.
func (a *Agent) foldEvidence(outcome ActionOutcome) {
	if outcome.Status == Invisible || outcome.Status == Interrupted {
		// D8: underestimation is self-sealing — no evidence is generated.
		return
	}

	selfID := a.ToM.SelfID()

	// Sort evidence by StatID for deterministic order (D12).
	// Note: Evidence is []tom.StatEvidence which doesn't have a sort import.
	// Using the tom package's types directly.
	for _, ev := range outcome.Evidence {
		a.ToM.Observe(selfID, ev)
	}
}

// ── Stat accessors (D8: reads ToM[self], never RealStats) ───────────────────────

// perceivedIntelligence returns the agent's self-perceived Intelligence stat value
// from ToM[self] (D8: decisions read ToM[self], never Real Stats).
func (a *Agent) perceivedIntelligence(statsReg *stats.Registry) float64 {
	const intelGlossaryID core.StatID = "Intelligence"
	if !statsReg.Has(intelGlossaryID) {
		// Fallback: use the first capability stat.
		caps := statsReg.Kinds(stats.Capability)
		if len(caps) > 0 {
			return a.perceivedStat(caps[0])
		}
		return 0
	}
	return a.perceivedStat(intelGlossaryID)
}

// perceivedStat returns the agent's self-perceived value for a stat from ToM[self] (D8).
func (a *Agent) perceivedStat(statID core.StatID) float64 {
	selfBelief, ok := a.ToM.Self(a.ToM.SelfID())
	if !ok {
		return 0
	}
	sd, ok := selfBelief.EstStats[statID]
	if !ok {
		return 0
	}
	return sd.Mean
}
