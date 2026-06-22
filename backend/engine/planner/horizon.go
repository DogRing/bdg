package planner

import (
	"math"

	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/stats"
	"github.com/dogring/bdg/engine/tom"
)

// computeHorizon returns the Intelligence-gated forward-sim lookahead (horizon_ticks).
//
//	perceived_intelligence = SelfModel mean estimate for the Intelligence StatID (ToM[self], D8)
//	max_intelligence       = stats.Registry.Def(Intelligence).Max
//	base_horizon           = PlannerConfig.BaseHorizonTicks
//	lookahead_threshold    = PlannerConfig.LookaheadThreshold  (P5)
//
//	if perceived_intelligence < lookahead_threshold:
//	    horizon_ticks = 0   -- HARD SKIP: no forward-sim, no provisioning (P5)
//	else:
//	    horizon_ticks = max(1, floor( base_horizon * (perceived_intelligence / max_intelligence) ))
func computeHorizon(
	selfModel tom.Belief,
	intelligenceStatID core.StatID,
	statsReg *stats.Registry,
	baseHorizonTicks int,
	lookaheadThreshold float64,
) int {
	// Read perceived intelligence from ToM[self] (D8).
	sd, ok := selfModel.EstStats[intelligenceStatID]
	perceivedIntelligence := 0.0
	if ok {
		perceivedIntelligence = sd.Mean
	}

	// Get the max intelligence from the stat registry.
	def, ok := statsReg.Def(intelligenceStatID)
	if !ok || def.Max <= 0 {
		// Unknown stat → no basis for lookahead.
		if perceivedIntelligence < lookaheadThreshold {
			return 0
		}
		return 1 // minimum viable foresight (above threshold fallback)
	}
	maxIntelligence := def.Max

	// Normalize perceived intelligence to [0, 1] fraction.
	normalizedIntel := perceivedIntelligence / maxIntelligence

	// P5: hard skip below threshold.
	if normalizedIntel < lookaheadThreshold {
		return 0
	}

	if maxIntelligence <= 0 {
		return 1
	}

	ratio := normalizedIntel
	horizon := int(math.Floor(float64(baseHorizonTicks) * ratio))

	if horizon < 1 {
		return 1
	}
	return horizon
}
