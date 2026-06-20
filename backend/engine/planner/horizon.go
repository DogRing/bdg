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
//
//	horizon_ticks = floor(base_horizon * (perceived_intelligence / max_intelligence))
//
// If perceived_intelligence == 0: horizon_ticks = 1 (minimum viable foresight).
func computeHorizon(
	selfModel tom.Belief,
	intelligenceStatID core.StatID,
	statsReg *stats.Registry,
	baseHorizonTicks int,
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
		return 1 // minimum viable foresight
	}
	maxIntelligence := def.Max

	if perceivedIntelligence <= 0 {
		return 1
	}

	ratio := perceivedIntelligence / maxIntelligence
	horizon := int(math.Floor(float64(baseHorizonTicks) * ratio))

	if horizon < 1 {
		return 1
	}
	return horizon
}
