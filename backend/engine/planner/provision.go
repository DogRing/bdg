package planner

import (
	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/needs"
)

// forwardSimProvision checks each consumable dimension for a predicted deficit
// within horizon and, if any, records it as a dimension requiring a provisioning
// subgoal. The provisioning subgoal will be inserted BEFORE the goal action by
// the search logic.
//
// Algorithm (SPEC § Forward-sim provisioning D9):
//
//	For each consumable dimension d (needs.Kinds(Consumable), in IDs() order):
//	    current_intensity(d)    = agent.NeedIntensities[d]
//	    satisfaction_threshold(d) = Def.Threshold
//	    current_slack(d)        = threshold - current_intensity
//	    predicted_deficit(d)    = Def.Demand(horizon_ticks) - current_slack(d)
//	    if predicted_deficit(d) > 0:
//	        record d for provisioning
//
// Returns the ordered list of dimensions that need provisioning, in needs.IDs()
// order (D12).
func forwardSimProvision(
	needsReg *needs.Registry,
	needIntensities map[core.Dimension]float64,
	horizonTicks int,
) []core.Dimension {
	consumableIDs := needsReg.Kinds(needs.Consumable)
	provisioned := make([]core.Dimension, 0)

	for _, d := range consumableIDs {
		def, ok := needsReg.Def(d)
		if !ok {
			continue
		}

		currentIntensity := needIntensities[d]
		threshold := def.Threshold
		currentSlack := threshold - currentIntensity

		// Demand for the horizon (rate * predicted-time).
		predictedDeficit := def.Demand(core.GameMinutes(horizonTicks)) - currentSlack

		if predictedDeficit > 0 {
			provisioned = append(provisioned, d)
		}
	}

	return provisioned
}

// dimensionToProducerPredicate maps a Dimension to the predicate that a producer
// must satisfy to reduce that dimension's intensity. This follows the Open
// Questions resolution in the SPEC: consumers that cannot wait (consumables)
// use the dimension's own predicate convention. For consumable dimensions, the
// predicate is "has_<dimension>" (e.g. has_Satiety, has_Hydration, has_Rest).
func dimensionToProducerPredicate(dim core.Dimension) core.Pred {
	return core.Pred("has_" + string(dim))
}
