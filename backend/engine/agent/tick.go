package agent

import (
	"slices"
	"sort"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/mind/needs"
	"github.com/dogring/bdg/engine/mind/perception"
	"github.com/dogring/bdg/engine/mind/planner"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/stats"
	"github.com/dogring/bdg/engine/mind/tom"
	"github.com/dogring/bdg/engine/mind/values"
)

// Tick: the 8-step decision loop

// Tick runs ONE tick of the decision loop for this agent and returns the intent(s)
// it wants the world to apply. It is READ-ONLY with respect to the shared world view
// (world is a snapshot, D12 read-plan-collect-apply). It MUST NOT mutate world or
// Services. It DOES mutate the receiver's own Body/coping/plan state.
//
// P5: phase 3 now also computes the max Other-referent Priority across cared-for
// agents and folds it into the urgency proxy used in replan. Phase 8 (updateDynamics)
// continues to use self-only urgency for the physiological adrenaline loop.
//
// P6: phase 0 processes incoming signals (Vote, hearsay). Phase 5a runs the reliance
// trigger on plan failure/high cost. Phase 7 emits Vote signals for delegation.
func (a *Agent) Tick(world WorldView, now core.Tick, rng *rng.RNG, svc Services, emit core.EventEmitter) []Intent {
	var intents []Intent

	// P6: 0. process incoming signals — fold vote/hearsay from other agents
	a.processIncomingSignals(world)

	// 1. perceive — sense the world (no state mutation, produces percepts for trace)
	seen := svc.Sensor.Sight(a.Pos, world)
	_ = svc.Sensor.Smell(a.Pos, world)
	_ = svc.Sensor.Hearing(a.Pos, world.SoundEvents())

	// P6 BLOCKER-1: phase-1 threat scan — scan perceived entities for threat tags,
	// update the conditional Safety intensity. MUST run BEFORE phase-3 appraise so
	// the appraisal chain sees the fresh Safety pressure the same tick.
	threats := a.scanThreats(seen)
	a.driveConditionalSafety(threats, svc.Needs)

	// P5/6: defensive-goal reflex: when a threat is perceived, forcibly select the
	// Safety dimension and clear the current plan so the planner re-plans for Safety.
	// This runs AFTER mediateGoal (phase 4) to truly override the mediated goal.
	goalForced := len(threats) > 0 && a.Cfg.SafetyDim != ""
	if goalForced {
		a.Plan = planner.Plan{} // clear plan so needsReplan returns true
		emit.Emit(core.Event{
			SchemaVersion: 1,
			Tick:          now,
			Seq:           0,
			AgentID:       a.ID,
			Type:          "GoalSelected",
			Payload: map[string]any{
				"dimension":  string(a.Cfg.SafetyDim),
				"priority":   1.0,
				"eff_value":  1.0,
			},
		})
	}

	// 2. decay needs — grow need intensities for each consumable dimension (D9)
	a.decayNeeds(svc.Needs)

	// 3. appraise — compute Priority per dimension via engine/values (D5)
	priorities := a.appraise(svc.Needs, svc.Values)
	// P5: also compute max Other-referent priority across cared-for Others.
	maxOther := a.appraiseOthers(world, svc.Needs, svc.Values, svc.Stats)
	// P5 (Scenario E): also compute max Place-referent priority across place values.
	maxPlace := a.appraisePlace(world, svc.Needs, svc.Values)
	// P5 (Scenario G): also compute max Collective-referent priority across collective values.
	maxCollective := a.appraiseCollective(world, svc.Needs, svc.Values)
	// Aggregate maxPriority for the urgency proxy (max of self, other, place, collective).
	selfMax := float64(priorities[0].Priority)
	combinedPriority := max(selfMax, maxOther, maxPlace, maxCollective)

	// 4. mediate goal — apply stickiness/deadband, decide whether to switch (owned here, D5)
	goalChanged := a.mediateGoal(priorities)
	// P5 (BLOCKER-2): collective-safety defensive override. When the village's mean
	// collective safety for a held Collective value drops below the threat threshold,
	// the holder adopts that dimension as a defensive goal (→ Patrol), overriding the
	// self-need mediation above. Only agents that hold such a value are affected.
	if defDim, fire := a.defensiveCollectiveGoal(world, svc.Needs); fire && a.Goal != defDim {
		a.Goal = defDim
		goalChanged = true
	}
	// P5 (BLOCKER-1): Per-threat defensive override — if a threat was detected in phase 1,
	// force the Safety dimension as the goal, overriding the normal mediation.
	if goalForced && a.Goal != a.Cfg.SafetyDim {
		a.Goal = a.Cfg.SafetyDim
		goalChanged = true
	}

	// 5. plan (if needed) — delegate to engine/planner
	if a.needsReplan(goalChanged) {
		plan, trace, err := a.replan(world, now, rng, svc, priorities, combinedPriority)
		if err != nil {
			a.enterCopingCascade(err, now, priorities, svc.Stats, emit)
			// P6: reliance trigger on plan failure (gap-closure §G-emit: pass emit)
			a.handleRelianceTrigger(world, 0, err, emit)
		} else {
			a.Plan = plan
			a.PlanIdx = 0
			a.Elapsed = 0
			a.Coping = Idle
			a.FailStreak = 0
			emitGoalSelected(emit, now, a.ID, trace)
			emitPlanBuilt(emit, now, a.ID, trace)
			// P6: reliance trigger on high plan cost (gap-closure §G-emit: pass emit)
			a.handleRelianceTrigger(world, trace.TotalCost, nil, emit)
		}
	}

	// 6. execute — emit the intent for the current durative step
	intent := a.execute(now, svc.Actions, world)
	if intent.Kind != IntentNone {
		intents = append(intents, intent)
	}

	// 7. signal — emit signals for trade/social actions + P6 vote delegation
	sigIntent := a.emitSignal(now, world)
	if sigIntent.Kind != IntentNone {
		intents = append(intents, sigIntent)
	}
	// P6: emit Vote signal if reliance + urgency thresholds are met.
	// Pass the already-computed combinedPriority (gap-closure §H: no recompute).
	voteIntent := a.emitVoteIfEligible(now, world, combinedPriority)
	if voteIntent.Kind != IntentNone {
		intents = append(intents, voteIntent)
	}

	// 8. dynamics — update Adrenaline, Mood, Resentment, crash stamina debt (P3)
	// NOTE: updateDynamics uses SELF-ONLY urgency (priorities), NOT the combined
	// urgency proxy. Adrenaline surge is physiological and responds only to own deficit.
	a.updateDynamics(priorities)

	// P3: accrue Resentment from trigger events while in Latent state.
	triggers := world.ResentmentTriggers(a.ID)
	a.accrueResentment(triggers, svc.Stats, emit)

	return intents
}

// ── Phase 1b: threat perception (P6 BLOCKER-1) ──────────────────────────────

// scanThreats scans the perceived entities for any whose tags intersect with
// Config.ThreatTags. Returns a deduplicated, AgentID-sorted list of threatening
// agent IDs (D12 count-stability for UpdateConditionalNeeds). Non-agent entities
// are excluded (only entities that could match the "agent" tag are threats).
func (a *Agent) scanThreats(seen []perception.PerceivedEntity) []core.AgentID {
	if len(a.Cfg.ThreatTags) == 0 {
		return nil
	}
	// Collect matching agent IDs.
	seenSet := make(map[core.AgentID]struct{})
	for _, e := range seen {
		// Only agents are threats (entities tagged "agent").
		if !hasTag(e.Tags, "agent") {
			continue
		}
		if tagsIntersect(e.Tags, a.Cfg.ThreatTags) {
			seenSet[core.AgentID(e.ID)] = struct{}{}
		}
	}
	if len(seenSet) == 0 {
		return nil
	}
	// Deduplicate and sort by AgentID (D12).
	result := make([]core.AgentID, 0, len(seenSet))
	for id := range seenSet {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool {
		return string(result[i]) < string(result[j])
	})
	return result
}

// driveConditionalSafety drives the conditional Safety intensity using the
// perceived threat list. Must run BEFORE phase-3 appraise so the appraisal
// chain sees the fresh Safety pressure. Also implements the defensive-goal
// reflex: if a threat is perceived, forces the Safety dimension as the goal.
//
// threatPerceived is returned for the caller's defensive-goal override.
func (a *Agent) driveConditionalSafety(threats []core.AgentID, needsReg *needs.Registry) {
	if a.Cfg.SafetyDim == "" {
		return
	}
	def, ok := needsReg.Def(a.Cfg.SafetyDim)
	if !ok {
		return
	}
	cur := a.NeedIntensities[a.Cfg.SafetyDim]
	a.NeedIntensities[a.Cfg.SafetyDim] = def.UpdateConditionalNeeds(
		cur, threats, a.Cfg.ThreatPerThreatGain, a.Cfg.ThreatSafetyDecay,
	)
}

// hasTag reports whether a tag with the given prefix is present in the slice.
func hasTag(tags []core.Tag, prefix string) bool {
	for _, t := range tags {
		if string(t) == prefix {
			return true
		}
	}
	return false
}

// tagsIntersect reports whether any tag in `tags` matches any tag in `needles`.
func tagsIntersect(tags []core.Tag, needles []core.Tag) bool {
	for _, t := range tags {
		if slices.Contains(needles, t) {
			return true
		}
	}
	return false
}

// Phase 2: decay needs

func (a *Agent) decayNeeds(needsReg *needs.Registry) {
	tickMinutes := core.GameMinutes(1)
	for _, dim := range needsReg.Kinds(needs.Consumable) {
		def, ok := needsReg.Def(dim)
		if !ok {
			continue
		}
		a.NeedIntensities[dim] += def.Rate * float64(tickMinutes)
	}
}

// Phase 3: appraise

// appraise computes the Priority-ordered Dimension list for goal mediation.
func (a *Agent) appraise(needsReg *needs.Registry, valsCfg *values.Config) []planner.DimensionPriority {
	dimIDs := needsReg.IDs()
	result := make([]planner.DimensionPriority, 0, len(dimIDs))

	for _, dim := range dimIDs {
		def, ok := needsReg.Def(dim)
		if !ok {
			continue
		}
		intensity := a.NeedIntensities[dim]
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

	sort.Slice(result, func(i, j int) bool {
		if result[i].Priority != result[j].Priority {
			return result[i].Priority > result[j].Priority
		}
		return string(result[i].Dim) < string(result[j].Dim)
	})

	return result
}

// appraiseOthers computes the max Other-referent Priority across all cared-for
// agents (those with Affinity > MinCareThreshold). Returns 0 if no cared-for
// agent is known. This is the P5 wiring that feeds care for others into the
// planner's conscience-loosening urgency proxy.
func (a *Agent) appraiseOthers(world WorldView, needsReg *needs.Registry, valsCfg *values.Config, statsReg *stats.Registry) float64 {
	moodStatID := resolveOtherMoodStatID(statsReg)
	perceivedIntel := a.normalizedIntelligence(statsReg)
	subjects := a.ToM.Subjects()
	var maxPriority float64

	for _, subject := range subjects {
		if subject == a.ToM.SelfID() {
			continue
		}

		// Look up the belief about this subject using ToM.Self().
		// ToM.Self(key) returns the Belief for any subject key, not
		// just the owning agent's self-belief — the parameter name is a
		// historical misnomer; it is a map lookup by SubjectID.
		otherBelief, ok := a.ToM.Self(subject)
		if !ok {
			continue
		}

		// Filter: only care about Others with sufficient Affinity.
		if otherBelief.Affinity <= a.Cfg.MinCareThreshold {
			continue
		}

		// For each need dimension, derive referent input and compute priority.
		for _, dim := range needsReg.IDs() {
			def, ok := needsReg.Def(dim)
			if !ok {
				continue
			}

			ref := core.Referent{Kind: core.Other, ID: core.ObjectID(subject)}
			selfIntensity := a.NeedIntensities[dim]

			ri := values.DeriveReferentInput(
				ref, dim, selfIntensity, def, otherBelief,
				0, nil, perceivedIntel, moodStatID, valsCfg,
			)

			standing := values.ComputeStanding(def, ri.CurrentIntensity)
			salience := values.ComputeSalience(standing)
			baseWeight := valsCfg.Weight(dim)

			// Apply bond multiplier: 1 + Affinity * BondAffinityGain.
			bondMultiplier := 1.0 + otherBelief.Affinity*a.Cfg.BondAffinityGain
			priority := values.ComputePriority(salience, baseWeight*bondMultiplier)

			if float64(priority) > maxPriority {
				maxPriority = float64(priority)
			}
		}
	}

	return maxPriority
}

// appraisePlace computes the max Place-referent Priority across all Place values
// the agent holds. For each Place value (e.g. Openness at home), it derives the
// place quality from the world and computes the urgency. Returns 0 if no Place
// value is held. This is the P5 wiring for Scenario E: when a Place's quality
// drops (e.g. view blocked by a neighbour's build), it raises urgency.
func (a *Agent) appraisePlace(world WorldView, needsReg *needs.Registry, valsCfg *values.Config) float64 {
	if len(a.Values) == 0 {
		return 0
	}

	var maxPriority float64

	// Iterate in value order — the caller keeps Values sorted by Dimension (D12).
	for _, val := range a.Values {
		if val.Ref.Kind != core.Place {
			continue
		}

		dim := val.Dimension
		def, ok := needsReg.Def(dim)
		if !ok {
			continue
		}

		// Derive place quality from the world.
		placeQuality := world.PlaceQuality(val.Ref.ID)
		ref := core.Referent{Kind: core.Place, ID: val.Ref.ID}

		// The posture transforms the quality BEFORE DeriveReferentInput.
		// For Maximize (e.g. Openness): quality = current quality.
		// For MaintainAbove: if quality < setpoint, the deficit is the intensity.
		var mappedQuality float64
		switch val.Posture {
		case core.Maximize:
			// Want as much Openness as possible: deficit = 1 - quality.
			mappedQuality = placeQuality
		case core.MaintainAbove:
			// Want quality >= setpoint: deficit relative to setpoint.
			if placeQuality >= val.Setpoint {
				mappedQuality = 1.0 // no deficit
			} else {
				// Deficit grows as quality drops below setpoint.
				deficit := (val.Setpoint - placeQuality) / val.Setpoint
				mappedQuality = 1.0 - clamp01(deficit)
			}
		case core.PreventBelow:
			// Prevent falling below setpoint.
			if placeQuality >= val.Setpoint {
				mappedQuality = 1.0 // no threat
			} else {
				deficit := (val.Setpoint - placeQuality) / val.Setpoint
				mappedQuality = 1.0 - clamp01(deficit)
			}
		}

		ri := values.DeriveReferentInput(
			ref, dim, 0, def, tom.Belief{},
			mappedQuality, nil, 0, "", valsCfg,
		)

		standing := values.ComputeStanding(def, ri.CurrentIntensity)
		salience := values.ComputeSalience(standing)
		weight := valsCfg.Weight(dim)
		priority := values.ComputePriority(salience, weight)

		if float64(priority) > maxPriority {
			maxPriority = float64(priority)
		}
	}

	return maxPriority
}

// appraiseCollective computes the max Collective-referent Priority across all Collective
// values the agent holds. For each Collective value (e.g. Safety for village), it gathers
// member need intensities from the world, derives referent input, and computes the priority.
// Returns 0 if no Collective value is held or the world has no member data. This is the P5
// wiring for Scenario G: when collective safety drops, defensive/pro-social goals fire.
func (a *Agent) appraiseCollective(world WorldView, needsReg *needs.Registry, valsCfg *values.Config) float64 {
	if len(a.Values) == 0 {
		return 0
	}

	// Get the member need intensities from the world.
	memberIntensities := world.MemberNeedIntensities()
	if len(memberIntensities) == 0 {
		return 0 // world doesn't track member data
	}

	var maxPriority float64

	// Iterate in value order — the caller keeps Values sorted by Dimension (D12).
	for _, val := range a.Values {
		if val.Ref.Kind != core.Collective {
			continue
		}

		dim := val.Dimension
		def, ok := needsReg.Def(dim)
		if !ok {
			continue
		}

		// Gather ReferentInput for each member.
		members := make([]values.ReferentInput, 0, len(memberIntensities))
		for _, agentID := range sortedAgentIDs(memberIntensities) {
			intensities := memberIntensities[agentID]
			memberIntensity := intensities[dim]
			memberInput := values.ReferentInput{
				CurrentIntensity: memberIntensity,
				MaxIntensity:     def.Threshold,
			}
			members = append(members, memberInput)
		}

		ref := core.Referent{Kind: core.Collective, ID: val.Ref.ID}
		ri := values.DeriveReferentInput(
			ref, dim, 0, def, tom.Belief{},
			0, members, 0, "", valsCfg,
		)

		standing := values.ComputeStanding(def, ri.CurrentIntensity)
		salience := values.ComputeSalience(standing)
		weight := valsCfg.Weight(dim)
		priority := values.ComputePriority(salience, weight)

		if float64(priority) > maxPriority {
			maxPriority = float64(priority)
		}
	}

	return maxPriority
}

// defensiveCollectiveGoal reports whether the agent should adopt a defensive goal
// because the village's collective standing on a dimension it protects has fallen
// below the threat threshold (balance.yaml threats.safety_threat_threshold, P5
// BLOCKER-2). It returns the dimension to pursue and true when:
//   - the agent holds a Collective value with a protective Posture (MaintainAbove /
//     PreventBelow) on some dimension D, AND
//   - the mean collective satisfaction for D across village members — defined as
//     1 − mean(member need-intensity) — has dropped below Cfg.SafetyThreatThreshold.
//
// Values are iterated in held order (kept sorted by the caller, D12); the first
// breached protective Collective value wins. Agents holding no such value (the
// random-spawn default) are never affected — backward compatible.
func (a *Agent) defensiveCollectiveGoal(world WorldView, needsReg *needs.Registry) (core.Dimension, bool) {
	if a.Cfg.SafetyThreatThreshold <= 0 || len(a.Values) == 0 {
		return "", false
	}
	members := world.MemberNeedIntensities()
	if len(members) == 0 {
		return "", false
	}

	for _, val := range a.Values {
		if val.Ref.Kind != core.Collective {
			continue
		}
		if val.Posture != core.MaintainAbove && val.Posture != core.PreventBelow {
			continue
		}
		dim := val.Dimension
		if _, ok := needsReg.Def(dim); !ok {
			continue
		}

		var sum float64
		var n int
		for _, aid := range sortedAgentIDs(members) {
			sum += members[aid][dim]
			n++
		}
		if n == 0 {
			continue
		}
		meanIntensity := sum / float64(n)
		satisfaction := 1.0 - meanIntensity
		if satisfaction < a.Cfg.SafetyThreatThreshold {
			return dim, true
		}
	}
	return "", false
}

// sortedAgentIDs returns the keys of a map[core.AgentID] ... in sorted order (D12).
func sortedAgentIDs(m map[core.AgentID]map[core.Dimension]float64) []core.AgentID {
	keys := make([]core.AgentID, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return string(keys[i]) < string(keys[j])
	})
	return keys
}

// Phase 4: mediate goal

func (a *Agent) mediateGoal(priorities []planner.DimensionPriority) bool {
	if len(priorities) == 0 {
		if a.Goal != "" {
			a.clearGoal()
			return true
		}
		return false
	}

	prioMap := make(map[core.Dimension]values.Priority, len(priorities))
	for _, p := range priorities {
		prioMap[p.Dim] = p.Priority
	}

	currentPrio := values.Priority(0)
	if a.Goal != "" {
		if p, ok := prioMap[a.Goal]; ok {
			currentPrio = p + values.Priority(a.Cfg.Stickiness)
		}
	}

	topPrio := priorities[0].Priority
	topDim := priorities[0].Dim

	if a.Goal == "" {
		a.Goal = topDim
		return true
	}
	if a.Goal == topDim {
		return false
	}

	margin := float64(topPrio) - float64(currentPrio)
	if margin > a.Cfg.GoalDeadband {
		a.Goal = topDim
		return true
	}
	return false
}

func (a *Agent) clearGoal() {
	a.Goal = ""
	a.Plan = planner.Plan{}
	a.PlanIdx = 0
	a.Elapsed = 0
}

// resolveOtherMoodStatID returns a disposition stat from the registry to serve as
// the mood proxy for the Other-referent low-Intelligence branch. D10: no hardcoded
// StatID literal — iterates registry metadata to find the first disposition stat.
func resolveOtherMoodStatID(statsReg *stats.Registry) core.StatID {
	if statsReg == nil {
		return ""
	}
	// Iterate registry metadata: pick the first disposition stat as mood proxy.
	ids := statsReg.IDs()
	for _, id := range ids {
		def, ok := statsReg.Def(id)
		if !ok {
			continue
		}
		if def.Kind == stats.Disposition {
			return id
		}
	}
	// Fallback: return the first registered stat.
	if len(ids) > 0 {
		return ids[0]
	}
	return ""
}
