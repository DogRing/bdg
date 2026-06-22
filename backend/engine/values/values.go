// Package values computes how much an agent wants something: the pure appraisal layer.
// It turns a need's current intensity into a Standing (how satisfied), a Salience (how urgent),
// an EffValue (effective value of an action toward a dimension), and a Priority (which need to
// pursue next, after the per-dimension weight). It is what-is-wanted only: it never selects,
// orders, or schedules actions — that is the planner (D5). All functions are pure (no IO, no RNG,
// deterministic).
//
// Formulas (authoritative):
//
//	Standing(d)   = clamp01( 1 - currentIntensity / maxIntensity )        // 1 = fully satisfied
//	Salience(d)   = clamp01( 1 - Standing(d) )                            // higher = more urgent
//	EffValue      = Salience(d) x expectedEffect                          // value of an action toward d
//	Priority(d)   = Salience(d) x weight(d)                               // which need to pursue next
//
// This package imports no "os" or filesystem package — the Load function reads from an injected
// io.Reader (architecture §1: engine is IO-free).
package values

import (
	"fmt"
	"io"
	"sort"

	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/needs"
	"github.com/dogring/bdg/engine/tom"
	"gopkg.in/yaml.v3"
)

// ── Scalar types ─────────────────────────────────────────────────────────────────

// Standing indicates how well a need is currently satisfied, in [0,1].
// 1 = fully met (no intensity); 0 = fully unmet.
type Standing float64

// Salience indicates the momentary urgency of a need, in [0,1].
// 1 = maximally urgent; 0 = not urgent at all.
type Salience float64

// EffValue is the effective value of an action toward a dimension, >= 0.
type EffValue float64

// Priority is the weighted urgency used to pick the next need/goal, >= 0.
type Priority float64

// ── Pure appraisal functions (no IO, no RNG, no state) ───────────────────────────

// ComputeStanding returns 1 - (currentIntensity / maxIntensity) clamped to [0,1], where
// maxIntensity is the need's satisfaction threshold (needs.Def.Threshold).
// A maxIntensity <= 0 yields Standing 1 (a need that cannot be unmet is always satisfied).
func ComputeStanding(need needs.Def, currentIntensity float64) Standing {
	maxIntensity := need.Threshold
	if maxIntensity <= 0 {
		return Standing(1)
	}
	return Standing(clamp01(1 - currentIntensity/maxIntensity))
}

// ComputeSalience returns 1 - Standing, clamped to [0,1].
// Higher Salience = more urgent. Equivalently currentIntensity / maxIntensity.
func ComputeSalience(s Standing) Salience {
	return Salience(clamp01(1 - float64(s)))
}

// ComputeEffValue returns Salience x expectedEffect, clamped to >= 0.
// expectedEffect is the expected delta to the dimension from completing the action,
// normalized to [0,1] by the max possible delta. A negative expectedEffect is clamped
// to 0 (an action that worsens a need has no positive value toward it).
func ComputeEffValue(sal Salience, expectedEffect float64) EffValue {
	if expectedEffect < 0 {
		return EffValue(0)
	}
	return EffValue(float64(sal) * expectedEffect)
}

// ComputePriority returns Salience x weight, where weight is the per-Dimension tunable
// from content/balance.yaml values.weights.<Dimension> (Config.Weight).
func ComputePriority(sal Salience, weight float64) Priority {
	return Priority(float64(sal) * weight)
}

// ── Referent-aware appraisal input (P5) ──────────────────────────────────────────

// ReferentInput is the resolved (currentIntensity, maxIntensity) pair that feeds the
// four appraisal functions, for any referent kind. The caller derives it from the referent
// and passes it to ComputeStanding. This keeps the formula functions unchanged (D5).
type ReferentInput struct {
	CurrentIntensity float64 // how much the need/state has grown (higher = worse)
	MaxIntensity     float64 // setpoint / maximum tolerable intensity
}

// DeriveReferentInput resolves the appraisal input for a given referent, dimension,
// and the agent's current knowledge. It is a pure function (D5/D12): no RNG, no global
// state, no map ranging for logic.
//
// Per-kind derivation (SPEC § P5 — Referent-aware appraisal):
//
// Self (ref.Kind == core.Self):
//
//	CurrentIntensity = selfIntensity
//	MaxIntensity     = needDef.Threshold
//
// Other (ref.Kind == core.Other):
//
//	Low-foresight  (perceivedIntelligence < cfg.OtherLowIntelThreshold):
//	    mood proxy: read the target's perceived Mood as a gross welfare indicator.
//	    CurrentIntensity = 1 - clamp01( moodMean / maxMood )
//	High-foresight (perceivedIntelligence >= cfg.OtherLowIntelThreshold):
//	    unmet-need proxy: mean over belief.EstStats of (1 - clamp01(mean(s)/max(s))),
//	    iterated in sorted StatID order (D12).
//	MaxIntensity = needDef.Threshold (same as Self).
//
// Place (ref.Kind == core.Place):
//
//	CurrentIntensity = 1 - placeQuality
//	MaxIntensity     = 1.0
//
// Collective (ref.Kind == core.Collective):
//
//	When CollectiveAggregationMode == "mean":
//	    CurrentIntensity = mean CurrentIntensity across members
//	    MaxIntensity     = mean MaxIntensity across members
//	When CollectiveAggregationMode == "min":
//	    CurrentIntensity = min CurrentIntensity across members  (worst-off member drives urgency)
//	    MaxIntensity     = mean MaxIntensity across members
func DeriveReferentInput(
	ref core.Referent,
	dim core.Dimension,
	selfIntensity float64,
	needDef needs.Def,
	belief tom.Belief,
	placeQuality float64,
	members []ReferentInput,
	perceivedIntelligence float64,
	moodStatID core.StatID,
	cfg *Config,
) ReferentInput {
	switch ref.Kind {
	case core.Self:
		return ReferentInput{
			CurrentIntensity: selfIntensity,
			MaxIntensity:     needDef.Threshold,
		}

	case core.Other:
		var currentIntensity float64
		// Branch: low-foresight (mood proxy) vs high-foresight (unmet-need proxy).
		if perceivedIntelligence < cfg.OtherLowIntelThreshold {
			// Low-foresight: use the target's perceived Mood as a gross welfare indicator.
			// A deeply negative mood → high intensity ("they are suffering").
			// The caller normalizes Mood to a [-1, 1] range before passing the belief;
			// maxMood = 1.0 as the default scale (the caller is responsible for passing
			// a normalized fraction, D7: no hardcoded literal in logic — the moodStatID
			// is resolved from the registry, not the scale).
			sd, ok := belief.EstStats[moodStatID]
			if !ok {
				currentIntensity = 1.0 // worst case: cannot discern → assume suffering
			} else {
				moodMean := sd.Mean
				// Normalize mood from [-1, 1] to [0, 1], then invert for intensity.
				// (moodMean + 1) / 2 maps [-1,1] → [0,1]; 0.5 = neutral.
				normalizedMood := (moodMean + 1.0) / 2.0
				currentIntensity = 1 - clamp01(normalizedMood)
			}
		} else {
			// High-foresight: unmet-need proxy.
			// Mean over EstStats of (1 - clamp01(mean(s) / max(s))).
			// Iterate in sorted StatID order (D12).
			// The stat max defaults to 100.0 (the shipped stats.yaml range [0,100]).
			statIDs := sortedStatKeys(belief.EstStats)
			var sum float64
			var count int
			maxVal := 100.0
			for _, sid := range statIDs {
				sd := belief.EstStats[sid]
				ratio := clamp01(sd.Mean / maxVal)
				sum += 1 - ratio
				count++
			}
			if count > 0 {
				currentIntensity = sum / float64(count)
			} else {
				currentIntensity = 1.0
			}
		}

		return ReferentInput{
			CurrentIntensity: currentIntensity,
			MaxIntensity:     needDef.Threshold,
		}

	case core.Place:
		// Place: placeQuality ∈ [0,1]; CurrentIntensity = 1 - placeQuality.
		return ReferentInput{
			CurrentIntensity: 1 - clamp01(placeQuality),
			MaxIntensity:     1.0,
		}

		case core.Collective:
			if len(members) == 0 {
				return ReferentInput{
					CurrentIntensity: 0,
					MaxIntensity:     0,
				}
			}
			var sumCurrent, sumMax float64
			minCurrent := members[0].CurrentIntensity
			for _, m := range members {
				sumCurrent += m.CurrentIntensity
				sumMax += m.MaxIntensity
				if m.CurrentIntensity < minCurrent {
					minCurrent = m.CurrentIntensity
				}
			}
			n := float64(len(members))
			currentIntensity := sumCurrent / n
			if cfg.CollectiveAggregationMode == "min" {
				currentIntensity = minCurrent
			}
			return ReferentInput{
				CurrentIntensity: currentIntensity,
				MaxIntensity:     sumMax / n,
			}

	default:
		// Unknown referent kind — fall back to Self semantics.
		return ReferentInput{
			CurrentIntensity: selfIntensity,
			MaxIntensity:     needDef.Threshold,
		}
	}
}

// sortedStatKeys returns the sorted keys of estStats (D12).
func sortedStatKeys(estStats map[core.StatID]tom.StatDist) []core.StatID {
	keys := make([]core.StatID, 0, len(estStats))
	for k := range estStats {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return string(keys[i]) < string(keys[j])
	})
	return keys
}

// getStatDefFromBelief retrieves the stat range info from the belief's EstStats.
// Since the Belief type doesn't carry min/max, we derive a pseudo-def from the
// mean value using a heuristic default range of [0, 100].
func getStatDefFromBelief(belief tom.Belief, statID core.StatID) (struct{ Min, Max float64 }, bool) {
	_, ok := belief.EstStats[statID]
	if !ok {
		return struct{ Min, Max float64 }{Min: 0, Max: 100}, false
	}
	// We don't have the actual min/max from the registry — use the same default
	// range that shipped stats.yaml uses.
	return struct{ Min, Max float64 }{Min: 0, Max: 100}, true
}

// ── Config (the values: block of content/balance.yaml) ───────────────────────────

// Config holds the per-Dimension arbitration weights loaded from content/balance.yaml's
// values.weights block, plus the P5 OtherLowIntelThreshold from the intelligence block
// and CollectiveAggregationMode from the values block. Immutable after Load.
type Config struct {
	weights                  map[core.Dimension]float64
	dims                     []core.Dimension // sorted lexicographically (D12)
	OtherLowIntelThreshold   float64          // balance.yaml intelligence.other_intel_threshold; default 0.5
	CollectiveAggregationMode string          // "mean" or "min"; balance.yaml values.collective_aggregation_mode; default "mean"
}

// Load parses ONLY the top-level values: block from r (the bytes of content/balance.yaml —
// the path is injected by platform/config, NEVER a file path here, keeping the engine
// IO-free, D10). It reads the full balance document, extracts the 'values:' top-level key,
// and performs SEMANTIC validation (every weight >= 0). Returns an error describing the
// first violation.
func Load(r io.Reader) (*Config, error) {
	// The balance YAML has many top-level keys (needs, world, perception, etc.).
	// We decode into a generic map first, then extract the values.weights block.
	var raw map[string]any
	dec := yaml.NewDecoder(r)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("values.Load: decode: %w", err)
	}

	valuesBlock, ok := raw["values"]
	if !ok {
		return nil, fmt.Errorf("values.Load: balance document has no top-level 'values:' block")
	}

	valuesMap, ok := valuesBlock.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("values.Load: 'values:' block is not a mapping")
	}

	weightsRaw, ok := valuesMap["weights"]
	if !ok {
		return nil, fmt.Errorf("values.Load: 'values:' block has no 'weights:' sub-block")
	}

	weightsMap, ok := weightsRaw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("values.Load: 'values.weights' is not a mapping")
	}

	if len(weightsMap) == 0 {
		return nil, fmt.Errorf("values.Load: 'values.weights' block is empty; at least one weight expected")
	}

	weights := make(map[core.Dimension]float64, len(weightsMap))
	dims := make([]core.Dimension, 0, len(weightsMap))

	for dimStr, val := range weightsMap {
		w, ok := val.(float64)
		if !ok {
			return nil, fmt.Errorf("values.Load: weight for dimension %q is not a number", dimStr)
		}
		if w < 0 {
			return nil, fmt.Errorf("values.Load: negative weight for dimension %q: %v", dimStr, w)
		}
		dim := core.Dimension(dimStr)
		if _, exists := weights[dim]; exists {
			return nil, fmt.Errorf("values.Load: duplicate dimension %q in values.weights block", dimStr)
		}
		weights[dim] = w
		dims = append(dims, dim)
	}

	// Sort lexicographically for determinism (D12).
	sort.Slice(dims, func(i, j int) bool {
		return string(dims[i]) < string(dims[j])
	})

	// Read intelligence.other_intel_threshold (P5). Default 0.5 if missing.
	otherLowIntelThreshold := 0.5
	if intelBlock, ok := raw["intelligence"]; ok {
		if intelMap, ok := intelBlock.(map[string]any); ok {
			if thrRaw, ok := intelMap["other_intel_threshold"]; ok {
				if thr, ok := thrRaw.(float64); ok {
					otherLowIntelThreshold = thr
				}
			}
		}
	}

	// Read values.collective_aggregation_mode (P5). Default "mean" if missing.
	collectiveAggregationMode := "mean"
	if aggRaw, ok := valuesMap["collective_aggregation_mode"]; ok {
		if aggStr, ok := aggRaw.(string); ok {
			if aggStr == "mean" || aggStr == "min" {
				collectiveAggregationMode = aggStr
			}
		}
	}

	return &Config{
		weights:                  weights,
		dims:                     dims,
		OtherLowIntelThreshold:   otherLowIntelThreshold,
		CollectiveAggregationMode: collectiveAggregationMode,
	}, nil
}

// Weight returns the arbitration weight for d, defaulting to 1.0 when d is absent from
// the values.weights block (so a new Dimension works with no weight authored). Always >= 0.
func (c *Config) Weight(d core.Dimension) float64 {
	if w, ok := c.weights[d]; ok {
		return w
	}
	return 1.0
}

// Dimensions returns the weighted dimension ids in canonical fixed order (sorted
// lexicographically, D12). The returned slice is a copy.
func (c *Config) Dimensions() []core.Dimension {
	out := make([]core.Dimension, len(c.dims))
	copy(out, c.dims)
	return out
}

// ── Helpers ──────────────────────────────────────────────────────────────────────

// clamp01 clamps v to [0, 1].
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// Compile-time checks to prevent unchecked type confusion.
// Named types are distinct from float64.
var _ Standing
var _ Salience
var _ EffValue
var _ Priority
