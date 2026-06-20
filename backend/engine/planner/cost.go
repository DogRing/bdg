package planner

import (
	"sort"

	"github.com/dogring/bdg/engine/core"
)

// computeCost returns the tag-derived cost of an action.
// cost(action) = Σ TagCosts[tag] for tag in action.Tags, iterating sorted tag keys
// (D12). A tag absent from TagCosts contributes 0. The tagCosts map is looked up
// but NOT ranged — iteration is over the precomputed sorted tag-cost keys (D12).
func computeCost(tags []core.Tag, sortedTagKeys []core.Tag, tagCosts map[core.Tag]float64) float64 {
	var total float64
	// Build a set of the action's tags for O(len(tags)) lookup.
	tagSet := make(map[core.Tag]struct{}, len(tags))
	for _, t := range tags {
		tagSet[t] = struct{}{}
	}
	// Iterate the precomputed sorted keys, NOT the map (D12).
	for _, k := range sortedTagKeys {
		if _, ok := tagSet[k]; ok {
			total += tagCosts[k]
		}
	}
	return total
}

// sortedTagCostKeys returns the sorted keys of tagCosts for D12-ordered iteration.
func sortedTagCostKeys(tagCosts map[core.Tag]float64) []core.Tag {
	keys := make([]core.Tag, 0, len(tagCosts))
	for k := range tagCosts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return string(keys[i]) < string(keys[j])
	})
	return keys
}
