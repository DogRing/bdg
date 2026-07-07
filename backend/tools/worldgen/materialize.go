package worldgen

import (
	"fmt"
	"sort"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/space/navmap"
	"github.com/dogring/bdg/platform/config"
)

// genSeedSalt offsets the fixture seed for the load-time materialization stream so it
// cannot collide with the run root (rng.New(fx.Seed)) or the agent spawn streams
// (rng.New(fx.Seed+i+1)). One stream, fixed draw order (terrain → flora sorted by id →
// animals sorted by id) keeps same (fixture, seed) ⇒ same world (D12).
const genSeedSalt = 0x6d617431 // "mat1"

// maxPlaceTries bounds the rejection sampling for one pos-less placement; a layout with
// ~no soil fails loudly instead of spinning.
const maxPlaceTries = 10_000

// materialize resolves the generated parts of fx into a concrete fixture (SPEC §Load):
// a terrain{random:true} block becomes an explicit SimpleTerrain layout over the navmap
// offset dims, and pos-less flora/animal placements land on random soil hexes
// (rejection-sampled over bounds). The caller's Fixture is never written through
// (pointers/slices are replaced), so a rebuild with a different Seed re-rolls fresh —
// the /api/regen path.
func materialize(fx Fixture, navCfg navmap.Config) (Fixture, error) {
	random := fx.Terrain != nil && fx.Terrain.Random
	if !random && !hasPoslessPlacement(fx) {
		return fx, nil
	}
	r := rng.New(fx.Seed + genSeedSalt)

	if random {
		cols, rows := navmap.OffsetDimsOf(navCfg)
		if cols <= 0 || rows <= 0 {
			return fx, fmt.Errorf("worldgen: terrain random:true needs positive world bounds/cell size")
		}
		fx.Terrain = &TerrainLayout{Cols: cols, Rows: rows, Cells: SimpleTerrain(cols, rows, r)}
	}

	if hasPoslessPlacement(fx) {
		if fx.Terrain == nil || len(fx.Terrain.Cells) == 0 {
			return fx, fmt.Errorf("worldgen: flora/animal placements without pos require a terrain layout")
		}
		sample := layoutSampler(*fx.Terrain, navCfg)
		place := func(id core.ObjectID) (*Vec2, error) {
			for range maxPlaceTries {
				p := Vec2{
					navCfg.MinX + r.Float64()*(navCfg.MaxX-navCfg.MinX),
					navCfg.MinY + r.Float64()*(navCfg.MaxY-navCfg.MinY),
				}
				if sample(p.Core()) == "soil" {
					return &p, nil
				}
			}
			return nil, fmt.Errorf("worldgen: no soil hex found placing %s", id)
		}

		flora := sortedFlora(fx.Flora)
		for i := range flora {
			if flora[i].Pos != nil {
				continue
			}
			p, err := place(flora[i].ID)
			if err != nil {
				return fx, err
			}
			flora[i].Pos = p
		}
		fx.Flora = flora

		animals := sortedAnimals(fx.Animals)
		for i := range animals {
			if animals[i].Pos != nil {
				continue
			}
			p, err := place(animals[i].ID)
			if err != nil {
				return fx, err
			}
			animals[i].Pos = p
		}
		fx.Animals = animals
	}
	return fx, nil
}

func hasPoslessPlacement(fx Fixture) bool {
	for _, fp := range fx.Flora {
		if fp.Pos == nil {
			return true
		}
	}
	for _, ap := range fx.Animals {
		if ap.Pos == nil {
			return true
		}
	}
	return false
}

// fixtureRespawnTargets merges fx.RespawnTargets over the content carrying capacities
// into a fresh map (SPEC §Load). cfg is shared across rebuilds (restart/regen) and is
// never mutated.
func fixtureRespawnTargets(fx Fixture, cfg *config.LoadOutput) map[core.Tag]int {
	if len(fx.RespawnTargets) == 0 {
		return cfg.RespawnTargets
	}
	out := make(map[core.Tag]int, len(cfg.RespawnTargets)+len(fx.RespawnTargets))
	for sp, n := range cfg.RespawnTargets {
		out[sp] = n
	}
	for sp, n := range fx.RespawnTargets {
		out[sp] = n
	}
	return out
}

func validateRespawnTargets(fx Fixture, cfg *config.LoadOutput) error {
	if len(fx.RespawnTargets) == 0 {
		return nil
	}
	species := make([]core.Tag, 0, len(fx.RespawnTargets))
	for sp := range fx.RespawnTargets {
		species = append(species, sp)
	}
	sort.Slice(species, func(i, j int) bool { return species[i] < species[j] })
	for _, sp := range species {
		if fx.RespawnTargets[sp] < 0 {
			return fmt.Errorf("worldgen: respawn_targets[%s] must be >= 0", sp)
		}
		if cfg.FaunaRules == nil || len(cfg.FaunaRules.Candidates(fauna.SpeciesID(sp))) == 0 {
			return fmt.Errorf("worldgen: respawn_targets references unknown fauna species %s", sp)
		}
	}
	return nil
}
