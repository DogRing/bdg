package world

import (
	"sort"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/stats"
)

// ── Stat sampling ──────────────────────────────────────────────────────────────

// sampleRealStats samples one value per stat from its GenSpec using rng, in
// stats.IDs() order (D12). Returns a fresh Stats map.
func sampleRealStats(statReg *stats.Registry, rng *rng.RNG) stats.Stats {
	s := make(stats.Stats, statReg.Len())
	for _, sid := range statReg.IDs() {
		def, _ := statReg.Def(sid)
		val := sampleFromGen(def, rng)
		s[sid] = def.Clamp(val)
	}
	return s
}

// sampleFromGen draws a single value from the stat's generation distribution.
func sampleFromGen(def stats.Def, rng *rng.RNG) float64 {
	switch def.Gen.Dist {
	case "normal":
		return def.Gen.Mean + rng.NormFloat64()*def.Gen.SD
	case "uniform":
		// Uniform in [Mean - SD, Mean + SD] (SD = half-width for uniform).
		return def.Gen.Mean + (rng.Float64()*2-1)*def.Gen.SD
	default:
		return def.Default
	}
}

// ── Tag parsing (D4: stat resolution from action Tags) ─────────────────────────

// statsFromTags extracts the stat IDs referenced by uses:<StatID> tags from an
// action's tag list, in sorted order (D12).
func statsFromTags(tags []core.Tag, statReg *stats.Registry) []core.StatID {
	seen := make(map[core.StatID]bool)
	var result []core.StatID
	for _, t := range tags {
		s := string(t)
		if len(s) > 5 && s[:5] == "uses:" {
			sid := core.StatID(s[5:])
			if statReg.Has(sid) && !seen[sid] {
				seen[sid] = true
				result = append(result, sid)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return string(result[i]) < string(result[j])
	})
	return result
}

// composeStat returns a composed capability value from RealStats for the given
// stat IDs. P1 uses the mean of capability stats (multi-stat actions).
func composeStat(realStats stats.Stats, statIDs []core.StatID) float64 {
	if len(statIDs) == 0 {
		return 0
	}
	var sum float64
	for _, sid := range statIDs {
		sum += realStats.Get(sid)
	}
	return sum / float64(len(statIDs))
}

// ── Effort level extraction ────────────────────────────────────────────────────

// effortLevelFromTags extracts the effort level from action tags.
func effortLevelFromTags(tags []core.Tag) float64 {
	for _, t := range tags {
		switch t {
		case "effort:high":
			return 0.90
		case "effort:med":
			return 0.50
		case "effort:low":
			return 0.20
		}
	}
	return 0.0
}
