package worldgen

import (
	"fmt"
	"math"
	"sort"

	"github.com/dogring/bdg/engine/env/flora"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/space/navmap"
	"github.com/dogring/bdg/platform/config"
)

// Density placement (scaling.md SC4 / docs/plans/world-gen.md WG5): a fixture asks for a
// per-species POPULATION by giving a density (entities per world unit²); materialize resolves
// it to count = round(density · bounds area) and places that many deterministically over the
// terrain layout. Flora sites are weighted by §6 carrying-capacity; animal sites are filtered
// by terrain passability (F18 allow-list). Draws come from the same fx.Seed sub-stream in a
// fixed order (see materialize), so same (fixture, seed) ⇒ same world (D12).

// floraDensityKRef normalizes §6 carrying-capacity K into an acceptance probability
// (accept ∝ clamp(K/KRef, 0, 1)). It only shapes WHERE within a species' viable range it
// lands (and sampling cost) — the COUNT is fixed by density·area — so the exact value is a
// build-time tuning constant (world-gen.md §2 OQ-DENS), not a mechanism gate. K=0 sites
// (water/steep via the (1−depth)/(1−slope) factors) are always rejected.
const floraDensityKRef = 4.0

// densityMaxTriesPerSlot bounds rejection sampling for one density slot; a slot that never
// finds a viable site is skipped (best-effort), so a hostile map yields fewer than target
// instead of spinning. Deterministic: same seed ⇒ same skips.
const densityMaxTriesPerSlot = 64

func hasDensity(fx Fixture) bool {
	return len(fx.FloraDensity) > 0 || len(fx.AnimalDensity) > 0
}

// densityCount = round(density · continuous bounds area), floored at 0 (D11: area is over
// continuous space, not cells).
func densityCount(density float64, navCfg navmap.Config) int {
	if density <= 0 {
		return 0
	}
	area := (navCfg.MaxX - navCfg.MinX) * (navCfg.MaxY - navCfg.MinY)
	n := int(math.Round(density * area))
	if n < 0 {
		return 0
	}
	return n
}

// sortedDensityKeys returns a density map's species keys in sorted order (D12: never range a
// map for logic).
func sortedDensityKeys(m map[core.Tag]float64) []core.Tag {
	keys := make([]core.Tag, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

// densityMoistureSampler bridges the generated moisture field to a continuous read for §6
// carrying-capacity. A nil field (explicit terrain, no hydrology) ⇒ 0 everywhere, so
// moisture-driven species place nothing — density placement is designed for random terrain.
func densityMoistureSampler(initMoisture []float64, cols, rows int, navCfg navmap.Config) func(core.Vec2) float64 {
	if len(initMoisture) == 0 {
		return func(core.Vec2) float64 { return 0 }
	}
	return gridSampler(initMoisture, cols, rows, navCfg)
}

// floraSiteAt builds the §6 evaluation site at a continuous position from the terrain layout
// (type → attrs) + generated moisture — the same inputs world feeds flora at runtime, minus the
// climate temperature (carrying-capacity is temperature-free for every shipped species, see
// flora.Rules.CarryingCapacity).
func floraSiteAt(cfg *config.LoadOutput, terrainAt func(core.Vec2) core.Tag, moistureAt func(core.Vec2) float64, p core.Vec2) flora.SiteInput {
	terrain := terrainAt(p)
	return flora.SiteInput{
		Terrain:      terrain,
		TerrainAttrs: cfg.TerrainAttrs[navmap.TerrainID(terrain)],
		Moisture:     moistureAt(p),
	}
}

// randPos draws a uniform position in the continuous world bounds (D11).
func randPos(navCfg navmap.Config, r *rng.RNG) Vec2 {
	return Vec2{
		navCfg.MinX + r.Float64()*(navCfg.MaxX-navCfg.MinX),
		navCfg.MinY + r.Float64()*(navCfg.MaxY-navCfg.MinY),
	}
}

// placeFloraDensity procedurally places each FloraDensity species over the terrain layout,
// weighting sites by §6 carrying-capacity K (accept ∝ clamp(K/KRef, 0, 1)). Density-placed
// plants start at seedling size (0,0) — the state a propagated seedling starts in — and grow
// in under the sim. IDs are `<species>_g<NNNNN>` (collision-safe vs listed entities).
func placeFloraDensity(fx Fixture, navCfg navmap.Config, cfg *config.LoadOutput, terrainAt func(core.Vec2) core.Tag, moistureAt func(core.Vec2) float64, r *rng.RNG) []FloraPlacement {
	var out []FloraPlacement
	for _, sp := range sortedDensityKeys(fx.FloraDensity) {
		count := densityCount(fx.FloraDensity[sp], navCfg)
		species := flora.SpeciesID(sp)
		for i := 0; i < count; i++ {
			for try := 0; try < densityMaxTriesPerSlot; try++ {
				p := randPos(navCfg, r)
				in := floraSiteAt(cfg, terrainAt, moistureAt, p.Core())
				accept := cfg.FloraRules.CarryingCapacity(species, in) / floraDensityKRef
				if accept > 1 {
					accept = 1
				}
				if r.Float64() < accept {
					pos := p
					out = append(out, FloraPlacement{
						ID:      core.ObjectID(fmt.Sprintf("%s_g%05d", sp, i)),
						Species: sp,
						Pos:     &pos,
					})
					break
				}
			}
		}
	}
	return out
}

// placeAnimalDensity procedurally places each AnimalDensity species, filtered to terrain the
// species can occupy — passable at the terrain level AND not in the species' impassable set
// (F18 allow-list via passability). Fish (land impassable) land only in water; land species
// avoid deep sea. Each gets a seeded random heading (mirrors the pos-less spawn, §materialize).
func placeAnimalDensity(fx Fixture, navCfg navmap.Config, cfg *config.LoadOutput, terrainAt func(core.Vec2) core.Tag, r *rng.RNG) []AnimalPlacement {
	var out []AnimalPlacement
	for _, sp := range sortedDensityKeys(fx.AnimalDensity) {
		count := densityCount(fx.AnimalDensity[sp], navCfg)
		species := fauna.SpeciesID(sp)
		for i := 0; i < count; i++ {
			for try := 0; try < densityMaxTriesPerSlot; try++ {
				p := randPos(navCfg, r)
				if !animalCanOccupy(cfg, species, terrainAt(p.Core())) {
					continue
				}
				pos := p
				out = append(out, AnimalPlacement{
					ID:      core.ObjectID(fmt.Sprintf("%s_g%05d", sp, i)),
					Species: sp,
					Pos:     &pos,
					Heading: r.Float64() * 2 * math.Pi,
				})
				break
			}
		}
	}
	return out
}

// animalCanOccupy reports whether species sp may spawn on a terrain type: passable at the
// terrain level AND not in the species' impassable set (reusing fauna TerrainCost's passable
// flag — no new content schema; the F18 allow-list decision, scaling.md SC4).
func animalCanOccupy(cfg *config.LoadOutput, sp fauna.SpeciesID, terrain core.Tag) bool {
	if tt, ok := cfg.TerrainTypes[navmap.TerrainID(terrain)]; ok && !tt.Passable {
		return false
	}
	if cfg.FaunaRules != nil {
		if _, passable := cfg.FaunaRules.TerrainCost(sp, terrain); !passable {
			return false
		}
	}
	return true
}
