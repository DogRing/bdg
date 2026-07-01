// Package config accessor helpers — Balance struct methods that assemble engine
// config structs from the parsed balance.yaml fields. These are pure translations:
// no IO, no RNG, no side effects.
package config

import (
	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/worldtime"
	"github.com/dogring/bdg/engine/mind/needs"
	"github.com/dogring/bdg/engine/mind/planner"
	"github.com/dogring/bdg/engine/mind/stats"
	"github.com/dogring/bdg/engine/mind/tom"
	"github.com/dogring/bdg/engine/world"
)

// AgentConfig builds agent.Config from the Balance fields.
// It resolves SafetyDim, threat tags, effort levels, and tom.Rates
// exactly as main.go's agentConfigFromBalance.
func (b *Balance) AgentConfig(needReg *needs.Registry, statReg *stats.Registry) agent.Config {
	// Resolve SafetyDim: the single Conditional PreventBelow dimension.
	var safetyDim core.Dimension
	for _, id := range needReg.Kinds(needs.Conditional) {
		if d, ok := needReg.Def(id); ok && d.Posture == needs.PreventBelow {
			safetyDim = id
			break
		}
	}

	// Resolve RestDim: the consumable Rest dimension (canonical glossary id).
	restDim := core.Dimension("Rest")
	if !needReg.Has(needs.NeedID(restDim)) {
		restDim = ""
	}

	// Resolve stat IDs from the stats registry (D10: no hardcoded StatID literals in engine code).
	intelSID := resolveStatByKind(statReg, stats.Capability)
	vindSID := resolveStatByKind(statReg, stats.Disposition)       // first disposition
	aggrSID := resolveSecondStatByKind(statReg, stats.Disposition) // second disposition

	// Resolve ThreatTags from hostile_tags strings.
	threatTags := make([]core.Tag, len(b.Threats.HostileTags))
	for i, s := range b.Threats.HostileTags {
		threatTags[i] = core.Tag(s)
	}

	return agent.Config{
		// mood
		Lambda:       b.Mood.Lambda,
		MoodDecay:    b.Mood.Decay,
		MoodBaseline: b.Mood.Baseline,

		// adrenaline
		AdrTriggerUrgency: b.Adrenaline.TriggerUrgency,
		AdrSurge:          b.Adrenaline.Surge,
		AdrDecay:          b.Adrenaline.Decay,
		AdrMax:            b.Adrenaline.Max,

		// stamina
		StaminaMax:     b.Stamina.Max,
		DrainPerEffort: b.Stamina.DrainPerEffort,
		RegenRest:      b.Stamina.RegenRest,
		RegenSleep:     b.Stamina.RegenSleep,

		// urgency
		UrgencyFromDeficit: b.Urgency.FromDeficit,
		BudgetPenalty:      b.Urgency.BudgetPenalty,

		// self-calibration
		Beta: b.SelfCalibration.Beta,

		// resentment
		AffinityDrop:    b.Resentment.AffinityDrop,
		AggressionDrift: b.Resentment.AggressionDrift,

		// planning (cross-tick)
		Stickiness:            b.Planning.Stickiness,
		GoalDeadband:          b.Planning.GoalDeadband,
		BudgetBase:            b.Planning.BudgetBase,
		BudgetPerIntelligence: b.Planning.BudgetPerIntelligence,

		// ToM rates
		Rates: tom.Rates{
			Alpha:                   b.Gossip.Alpha,
			Beta:                    b.SelfCalibration.Beta,
			MinTrust:                b.Gossip.MinTrust,
			InitialBeliefNoise:      b.Generation.InitialBeliefNoise,
			TradeSuccessTrustDelta:  0.05,
			TradeRejectAffinityDrop: 0.02,
			FraudHonestyDrop:        0.10,
			FraudThreshold:          0.20,
			InfluenceWeight:         b.Politics.InfluenceWeight,
		},

		// P3: adrenaline stamina coupling
		CrashStaminaPenalty: b.Adrenaline.CrashStaminaPenalty,

		// P3: effort levels
		EffortLevels: tagToFloatMap(b.TagLevels.Effort),

		// coping
		RebindMinIntelligence: b.Intelligence.RebindThreshold,
		ApathyFailStreak:      b.Coping.ApathyFailStreak,
		ApathyRecoverMood:     b.Coping.ApathyRecoverMood,
		ApathyBudgetPenalty:   b.Coping.ApathyBudgetPenalty,

		// resentment
		ResentmentPerTrigger: b.Resentment.PerTrigger,
		ResentmentThreshold:  b.Resentment.Threshold,

		// P5: trade
		ClaimInflateMin: b.Trade.ClaimInflateMin,
		ClaimInflateMax: b.Trade.ClaimInflateMax,

		// P5: social bonds
		BondAffinityGain:    b.Social.BondAffinityGain,
		MinCareThreshold:    b.Social.MinCareThreshold,
		MaxPossiblePriority: 2.5,

		// P5/P6: threats
		SafetyThreatThreshold: b.Threats.SafetyThreatThreshold,
		ThreatTags:            threatTags,
		SafetyDim:             safetyDim,
		RestDim:               restDim,
		IntelligenceStatID:    intelSID,
		VindictivenessStatID:  vindSID,
		AggressionStatID:      aggrSID,
		ThreatPerThreatGain:   b.Threats.PerThreatIntensity,
		ThreatSafetyDecay:     b.Threats.SafetyDecay,

		// P6: politics
		RelyCostThreshold: b.Politics.RelyCostThreshold,
		RelyOnDelta:       b.Politics.RelyOnDelta,
		VoteRelyThreshold: b.Politics.VoteRelyThreshold,
		UrgencyThreshold:  b.Politics.VoteUrgencyThreshold,
		VoteRelyOnDelta:   b.Politics.VoteRelyOnDelta,
		InfluenceWeight:   b.Politics.InfluenceWeight,
	}
}

// WorldConfig builds world.Config from the Balance fields.
func (b *Balance) WorldConfig() world.Config {
	// Locomotion arrival tolerance: a missing/zero key falls back to the engine
	// default so existing balance fixtures (and tests) keep working.
	arrivalEpsilon := b.World.ArrivalEpsilon
	if arrivalEpsilon <= 0 {
		arrivalEpsilon = 1.0
	}
	return world.Config{
		SpatialHashCell:          b.World.SpatialHashCell,
		RoleConvergenceThreshold: b.Politics.RoleConvergenceThreshold,
		OutcomeDifficultyBase:    b.World.OutcomeDifficultyBase,
		BackupEveryTicks:         b.World.BackupEveryTicks,
		MoveSpeedPerTick:         0.5,
		ArrivalEpsilon:           arrivalEpsilon,
		PlanInterval:             b.World.PlanInterval,
		PruneThreshold:           b.World.PruneThreshold,
	}
}

// PlannerConfig builds planner.PlannerConfig from the Balance planner: block.
func (b *Balance) PlannerConfig() planner.PlannerConfig {
	return planner.PlannerConfig{
		Budget: planner.Budget{
			MaxDepth:   b.Planner.Budget.MaxDepth,
			MaxActions: b.Planner.Budget.MaxActions,
			MaxNodes:   b.Planner.Budget.MaxNodes,
		},
		BaseHorizonTicks:   b.Planner.BaseHorizonTicks,
		UrgencyThreshold:   b.Planner.UrgencyThreshold,
		TagCosts:           stringToTagMap(b.Planner.TagCosts),
		LookaheadThreshold: b.Intelligence.LookaheadThreshold,
	}
}

// ClockConfig builds worldtime.Config from the Balance world: block.
func (b *Balance) ClockConfig() worldtime.Config {
	return worldtime.Config{
		TickMinutes:    int64(b.World.TickMinutes),
		DayMinutes:     int64(b.World.DayMinutes),
		DaysPerSeason:  30,
		SeasonsPerYear: 4,
	}
}

// tagToFloatMap converts a string to float64 map to core.Tag to float64.
func tagToFloatMap(raw map[string]float64) map[core.Tag]float64 {
	out := make(map[core.Tag]float64, len(raw))
	for k, v := range raw {
		out[core.Tag(k)] = v
	}
	return out
}

// stringToTagMap is a semantic alias for tagToFloatMap; used where the input is
// explicitly planner tag_costs (same map shape, distinct conceptual source).
func stringToTagMap(raw map[string]float64) map[core.Tag]float64 { return tagToFloatMap(raw) }

// resolveStatByKind returns the first stat ID of the given kind from the registry
// (D10: resolves by metadata, no hardcoded StatID literal).
func resolveStatByKind(statReg *stats.Registry, kind stats.Kind) core.StatID {
	ids := statReg.Kinds(kind)
	if len(ids) > 0 {
		return ids[0]
	}
	// Fallback: first registered stat.
	all := statReg.IDs()
	if len(all) > 0 {
		return all[0]
	}
	return ""
}

// resolveSecondStatByKind returns the second stat ID of the given kind from the
// registry (D10: metadata-iterated). Used when two distinct stats of the same kind
// are needed (e.g., Vindictiveness + Aggression are both disposition).
func resolveSecondStatByKind(statReg *stats.Registry, kind stats.Kind) core.StatID {
	ids := statReg.Kinds(kind)
	if len(ids) > 1 {
		return ids[1]
	}
	// Fallback: last registered stat of any kind.
	all := statReg.IDs()
	if len(all) > 1 {
		return all[len(all)-1]
	}
	return ""
}
