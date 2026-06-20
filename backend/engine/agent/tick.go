package agent

import (
	"sort"

	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/needs"
	"github.com/dogring/bdg/engine/planner"
	"github.com/dogring/bdg/engine/rng"
	"github.com/dogring/bdg/engine/values"
)

// ── Tick: the 8-step decision loop ─────────────────────────────────────────────

// Tick runs ONE tick of the decision loop for this agent and returns the intent(s)
// it wants the world to apply. It is READ-ONLY with respect to the shared world view
// (world is a snapshot, D12 read→plan→collect→apply). It MUST NOT mutate `world` or
// `Services`. It DOES mutate the receiver's own Body/coping/plan state.
//
// Determinism (D12): given identical (Agent state, world snapshot, rng state, Services),
// Tick produces byte-identical intents and identical receiver mutations.
//
// P3: phase 5 threads body scalars into the planner snapshot; phase 6 uses data-driven
// Stamina drain/regen; phase 8 adds crash stamina debt and Resentment accrual.
func (a *Agent) Tick(world WorldView, now core.Tick, rng *rng.RNG, svc Services, emit core.EventEmitter) []Intent {
	var intents []Intent

	// 1. perceive — sense the world (no state mutation, produces percepts for trace)
	_ = svc.Sensor.Sight(a.Pos, world)
	_ = svc.Sensor.Smell(a.Pos, world)
	_ = svc.Sensor.Hearing(a.Pos, world.SoundEvents())
	// Percepts are available for why-trace; the agent uses KnownObjects for appraisal.

	// 2. decay needs — grow need intensities for each consumable dimension (D9)
	a.decayNeeds(svc.Needs)

	// 3. appraise — compute Priority per dimension via engine/values (D5)
	priorities := a.appraise(svc.Needs, svc.Values)

	// 4. mediate goal — apply stickiness/deadband, decide whether to switch (owned here, D5)
	goalChanged := a.mediateGoal(priorities)

	// 5. plan (if needed) — delegate to engine/planner
	if a.needsReplan(goalChanged) {
		plan, trace, err := a.replan(world, now, rng, svc, priorities)
		if err != nil {
			// Enter coping cascade on planning failure.
			a.enterCopingCascade(err, now, priorities, svc.Stats, emit)
			// Fall through to execute — coping may produce a substitute goal.
		} else {
			a.Plan = plan
			a.PlanIdx = 0
			a.Elapsed = 0
			a.Coping = Idle
			a.FailStreak = 0 // P3: reset on plan success
			emitGoalSelected(emit, now, a.ID, trace)
			emitPlanBuilt(emit, now, a.ID, trace)
		}
	}

	// 6. execute — emit the intent for the current durative step
	intent := a.execute(now, svc.Actions)
	if intent.Kind != IntentNone {
		intents = append(intents, intent)
	}

	// 7. signal — emit signals for trade/social actions
	sigIntent := a.emitSignal(now, world)
	if sigIntent.Kind != IntentNone {
		intents = append(intents, sigIntent)
	}

	// 8. dynamics — update Adrenaline, Mood, Resentment, crash stamina debt (P3)
	a.updateDynamics(priorities)

	// P3: accrue Resentment from trigger events while in Latent state.
	triggers := world.ResentmentTriggers(a.ID)
	a.accrueResentment(triggers, svc.Stats, emit)

	return intents
}

// ── Phase 2: decay needs ───────────────────────────────────────────────────────

// decayNeeds grows need intensities for each consumable dimension by its per-tick
// rate. Consumable needs decay over time (D9); conditional needs are event-driven.
func (a *Agent) decayNeeds(needsReg *needs.Registry) {
	tickMinutes := core.GameMinutes(1) // P1: 1 tick = 1 game-minute (default ratio)

	for _, dim := range needsReg.Kinds(needs.Consumable) {
		def, ok := needsReg.Def(dim)
		if !ok {
			continue
		}
		current := a.NeedIntensities[dim]
		// Need intensity GROWS (gets worse) by rate × minutes.
		a.NeedIntensities[dim] = current + def.Rate*float64(tickMinutes)
	}
}

// ── Phase 3: appraise ──────────────────────────────────────────────────────────

// appraise computes the Priority-ordered Dimension list for goal mediation.
// For each dimension: Standing → Salience → Priority = Salience × weight (D5).
// The result is sorted by Priority DESC, ties by Dimension lexicographic (D12).
func (a *Agent) appraise(needsReg *needs.Registry, valsCfg *values.Config) []planner.DimensionPriority {
	dimIDs := needsReg.IDs()
	result := make([]planner.DimensionPriority, 0, len(dimIDs))

	for _, dim := range dimIDs {
		def, ok := needsReg.Def(dim)
		if !ok {
			continue
		}
		intensity := a.NeedIntensities[dim] // 0 if absent (default)
		standing := values.ComputeStanding(def, intensity)
		salience := values.ComputeSalience(standing)
		weight := valsCfg.Weight(dim)
		priority := values.ComputePriority(salience, weight)

		result = append(result, planner.DimensionPriority{
			Dim:      dim,
			Priority: priority,
			Salience: salience,
		})
	}

	// Sort: Priority DESC, ties by Dimension lexicographic (D12).
	sort.Slice(result, func(i, j int) bool {
		if result[i].Priority != result[j].Priority {
			return result[i].Priority > result[j].Priority // descending
		}
		return string(result[i].Dim) < string(result[j].Dim)
	})

	return result
}

// ── Phase 4: mediate goal ──────────────────────────────────────────────────────

// mediateGoal applies the cross-tick anti-thrash logic (Stickiness + goal_deadband,
// owned here — NOT in the planner, per planner SPEC OQ). Returns true if the goal
// changed (requiring a re-plan).
func (a *Agent) mediateGoal(priorities []planner.DimensionPriority) bool {
	if len(priorities) == 0 {
		// No dimensions to pursue.
		if a.Goal != "" {
			a.clearGoal()
			return true
		}
		return false
	}

	// Build a map for quick Priority lookup.
	prioMap := make(map[core.Dimension]values.Priority, len(priorities))
	for _, p := range priorities {
		prioMap[p.Dim] = p.Priority
	}

	// Apply stickiness bonus to current goal.
	currentPrio := values.Priority(0)
	if a.Goal != "" {
		if p, ok := prioMap[a.Goal]; ok {
			currentPrio = p + values.Priority(a.Cfg.Stickiness)
		}
	}

	topPrio := priorities[0].Priority
	topDim := priorities[0].Dim

	// If no current goal, adopt the top priority.
	if a.Goal == "" {
		a.Goal = topDim
		return true
	}

	// If current goal is still top after stickiness, keep it.
	if a.Goal == topDim {
		return false
	}

	// Check deadband: switch only if top rival beats current by > GoalDeadband.
	margin := float64(topPrio) - float64(currentPrio)
	if margin > a.Cfg.GoalDeadband {
		a.Goal = topDim
		return true
	}

	return false
}

// clearGoal resets the goal and plan state.
func (a *Agent) clearGoal() {
	a.Goal = ""
	a.Plan = planner.Plan{}
	a.PlanIdx = 0
	a.Elapsed = 0
}
