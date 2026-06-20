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

// ── Config (the values: block of content/balance.yaml) ───────────────────────────

// Config holds the per-Dimension arbitration weights loaded from content/balance.yaml's
// values.weights block. Immutable after Load.
type Config struct {
	weights map[core.Dimension]float64
	dims    []core.Dimension // sorted lexicographically (D12)
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

	return &Config{
		weights: weights,
		dims:    dims,
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
