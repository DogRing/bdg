package worldgen

import (
	"fmt"
	"sort"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/world"
	"github.com/dogring/bdg/platform/config"
)

// TestFloraDensityByTerrain seeds a uniform grass grid over a random varied-terrain world, runs it
// to near-equilibrium, and REPORTS the final grass count + per-cell density PER TERRAIN TYPE — so we
// can read the terrain-dependent carrying_capacity (§6 formula) at work. Run
// `go test ./tools/worldgen -run FloraDensityByTerrain -v` to read the table.
//
// It also guards the wiring: grass must be much DENSER on soil than on sea (deep salt water, where
// carrying_capacity's (1−salinity)·(1−depth) → 0). If terrain attrs stop reaching flora SiteInput
// (the historical "terrainAttrs unfed" bug), every terrain fills to the same moisture-only density
// and this tripwire fails.
func TestFloraDensityByTerrain(t *testing.T) {
	const (
		mapSize = 180.0
		spacing = 9.0   // initial grass grid spacing — sparse; grows UP toward each terrain's K
		ticks   = 12000 // flora_step=60 → ~200 flora steps
		seed    = 42
	)

	contentDir := findExisting(t, "../../../content", "../content", "content")
	cfg, err := config.Load(contentDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	var flora []FloraPlacement
	n := 0
	for x := spacing / 2; x < mapSize; x += spacing {
		for y := spacing / 2; y < mapSize; y += spacing {
			n++
			p := Vec2{x, y}
			flora = append(flora, FloraPlacement{
				ID: core.ObjectID(fmt.Sprintf("g%d", n)), Species: "grass", Pos: &p, Length: 0.4, Width: 0.3,
			})
		}
	}

	fx := Fixture{
		SchemaVersion: 1,
		Seed:          seed,
		Bounds:        &Bounds{Min: Vec2{0, 0}, Max: Vec2{mapSize, mapSize}},
		Terrain:       &TerrainLayout{Random: true},
		Flora:         flora,
	}
	w, err := Load(fx, cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	initial := grassByTerrain(w)
	for range ticks {
		w.Tick()
	}
	final := grassByTerrain(w)

	cells := map[string]int{}
	if rv := w.RenderView(); rv.Terrain != nil {
		for _, tid := range rv.Terrain.Terrain {
			cells[tid]++
		}
	}

	terrains := make([]string, 0, len(cells))
	for tid := range cells {
		if tid != "" {
			terrains = append(terrains, tid)
		}
	}
	sort.Strings(terrains)

	t.Logf("── grass census over %d ticks, %.0f×%.0f random world (seed %d) ──", ticks, mapSize, mapSize, seed)
	t.Logf("initial grass = %d (uniform grid, spacing %.0f)", n, spacing)
	t.Logf("%-10s %6s %7s %8s %10s", "terrain", "cells", "init", "final", "per-cell")
	perCell := map[string]float64{}
	for _, tid := range terrains {
		if cells[tid] > 0 {
			perCell[tid] = float64(final[tid]) / float64(cells[tid])
		}
		t.Logf("%-10s %6d %7d %8d %10.2f", tid, cells[tid], initial[tid], final[tid], perCell[tid])
	}

	// Tripwire: terrain-dependent density is active. Soil (fertile) must carry a far higher
	// per-cell grass density than sea (K≈0 via salinity+depth). Equal densities ⇒ attrs unwired.
	if perCell["soil"] <= 3*perCell["sea"] {
		t.Errorf("terrain-dependent density not active: soil %.2f/cell vs sea %.2f/cell (expected soil ≫ sea)",
			perCell["soil"], perCell["sea"])
	}
}

// grassByTerrain groups the live grass plants by the terrain type at each plant's position.
func grassByTerrain(w *world.World) map[string]int {
	out := map[string]int{}
	for _, p := range w.RenderView().Flora {
		if p.Species == "grass" {
			out[string(w.TerrainAt(p.Pos))]++
		}
	}
	return out
}
