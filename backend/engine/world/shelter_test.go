package world

import (
	"math"
	"testing"

	"github.com/dogring/bdg/engine/env/climate"
	"github.com/dogring/bdg/engine/env/exposure"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/space/navmap"
)

func shelterNavMap() *navmap.NavMap {
	cfg := navmap.Config{CellSize: 5, MinX: 0, MinY: 0, MaxX: 200, MaxY: 200, WearCostMin: 0.5, WearMax: 1}
	types := map[navmap.TerrainID]navmap.TerrainType{"plain": {BaseCost: 1, Passable: true}}
	return navmap.New(cfg, func(core.Vec2) navmap.TerrainID { return "plain" }, types)
}

// Shelter-OFF (no InstallShelter): localWindAt returns the global wind unchanged everywhere.
func TestLocalWindOffNeutral(t *testing.T) {
	w := &World{nav: shelterNavMap()}
	g := climate.Wind{Dir: 1.25, Mag: 0.7}
	for _, p := range []core.Vec2{{X: 10, Y: 10}, {X: 50, Y: 60}, {X: 3, Y: 9}} {
		if lw := w.localWindAt(p, g); lw.Dir != g.Dir || lw.Mag != g.Mag {
			t.Errorf("OFF localWindAt(%v) = %+v, want {Dir 1.25, Mag 0.7}", p, lw)
		}
	}
}

// With a blocker installed, the cell downwind of it (located via the same Topology the field uses,
// so the test is robust to hex orientation) gets a reduced wind magnitude, while the blocker cell
// itself and the upwind cell keep the raw magnitude. Direction is always preserved.
func TestLocalWindInjectionDownwind(t *testing.T) {
	nav := shelterNavMap()
	w := &World{nav: nav}

	blockerCell := exposure.Cell{Q: 5, R: 5}
	cfg := exposure.Config{ShadowCellsPerHeight: 1, ShadowFalloff: 0.5, MinEpsilon: 0}
	w.InstallShelter(cfg, []exposure.Blocker{{
		ID: "wall", Footprint: []exposure.Cell{blockerCell}, Height: 3, Opacity: 1,
	}})

	wind := climate.Wind{Dir: math.Pi / 6, Mag: 0.8} // 30°, aligned to a flat-top hex neighbor
	sector := exposure.SectorOf(exposure.Wind{Dir: wind.Dir, Mag: wind.Mag})
	topo := newNavTopo(nav)

	center := func(c exposure.Cell) core.Vec2 { return nav.CellCenter(navmap.Cell{Q: c.Q, R: c.R}) }

	// d=1 leeward cell: epsilon 0 → fully calm, direction preserved.
	down := topo.Neighbors(blockerCell)[sector]
	if lw := w.localWindAt(center(down), wind); math.Abs(lw.Mag) > 1e-9 || lw.Dir != wind.Dir {
		t.Errorf("downwind localWind = %+v, want {Dir %.4f, Mag ~0}", lw, wind.Dir)
	}
	// The blocker cell itself is not shadowed → raw magnitude.
	if lw := w.localWindAt(center(blockerCell), wind); math.Abs(lw.Mag-0.8) > 1e-9 {
		t.Errorf("blocker-cell Mag = %v, want raw 0.8 (not self-shadowed)", lw.Mag)
	}
	// The opposite (upwind) neighbor keeps the raw magnitude.
	up := topo.Neighbors(blockerCell)[(sector+3)%exposure.NumSectors]
	if lw := w.localWindAt(center(up), wind); math.Abs(lw.Mag-0.8) > 1e-9 {
		t.Errorf("upwind Mag = %v, want raw 0.8", lw.Mag)
	}
}
