// Package navmap_test covers AC1–AC7, snapshot isolation, and shared test fixtures.
package navmap_test

import (
	"math"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/space/navmap"
)

// ── Shared test fixtures (also used by navmap_golden_test.go) ─────────────────

// testTypes is the terrain-type catalog used across tests.
var testTypes = map[navmap.TerrainID]navmap.TerrainType{
	"plain":  {BaseCost: 1.0, Passable: true, RequiredTags: nil},
	"forest": {BaseCost: 2.0, Passable: true, RequiredTags: nil},
	"swamp":  {BaseCost: 3.0, Passable: true, RequiredTags: []core.Tag{"Swim"}},
	"water":  {BaseCost: 4.0, Passable: false, RequiredTags: nil},
}

// testCfg is the default config for tests: 100×100 world, 10-unit cells (10×10 grid).
var testCfg = navmap.Config{
	CellSize: 10,
	MinX:     0, MinY: 0, MaxX: 100, MaxY: 100,
	WearOnUse:   1,
	WearOnPave:  5,
	WearDecay:   1,
	WearMax:     10,
	WearCostMin: 0.3,
}

// newMapAllPlain creates a NavMap where every in-bounds cell is "plain".
func newMapAllPlain() *navmap.NavMap {
	return navmap.New(testCfg, func(_ core.Vec2) navmap.TerrainID { return "plain" }, testTypes)
}

// newMapFn creates a NavMap with a custom terrainAt function.
func newMapFn(fn func(core.Vec2) navmap.TerrainID) *navmap.NavMap {
	return navmap.New(testCfg, fn, testTypes)
}

// ── AC1: Terrain cost ──────────────────────────────────────────────────────────

func TestTerrainCost(t *testing.T) {
	t.Parallel()
	// column x<50 → "plain" (BaseCost=1), x≥50 → "forest" (BaseCost=2).
	m := newMapFn(func(p core.Vec2) navmap.TerrainID {
		if p.X < 50 {
			return "plain"
		}
		return "forest"
	})
	plain := navmap.Cell{X: 2, Y: 2}  // centre at (25,25) → plain
	forest := navmap.Cell{X: 7, Y: 2} // centre at (75,25) → forest

	costPlain := m.StepCost(navmap.Cell{X: 2, Y: 3}, plain)
	costForest := m.StepCost(navmap.Cell{X: 7, Y: 3}, forest)

	// Both are cardinal steps; geometric length = CellSize = 10.
	wantPlain := 1.0 * 1.0 * 10.0  // baseCost × wearMult × geoLen
	wantForest := 2.0 * 1.0 * 10.0 // forest is 2× plain

	if math.Abs(costPlain-wantPlain) > 1e-9 {
		t.Errorf("plain StepCost = %v, want %v", costPlain, wantPlain)
	}
	if math.Abs(costForest-wantForest) > 1e-9 {
		t.Errorf("forest StepCost = %v, want %v", costForest, wantForest)
	}
	if math.Abs(costForest/costPlain-2.0) > 1e-9 {
		t.Errorf("forest cost should be 2× plain: ratio = %v", costForest/costPlain)
	}

	// Diagonal step: geoLen = √2 × CellSize.
	costDiag := m.StepCost(plain, navmap.Cell{X: 3, Y: 3})
	wantDiag := 1.0 * 1.0 * (math.Sqrt2 * 10.0)
	if math.Abs(costDiag-wantDiag) > 1e-9 {
		t.Errorf("diagonal StepCost = %v, want %v", costDiag, wantDiag)
	}
}

// ── AC2: Impassable ────────────────────────────────────────────────────────────

func TestImpassable(t *testing.T) {
	t.Parallel()
	waterCell := navmap.Cell{X: 5, Y: 5}
	m := newMapFn(func(p core.Vec2) navmap.TerrainID {
		if p.X >= 50 && p.X < 60 && p.Y >= 50 && p.Y < 60 {
			return "water"
		}
		return "plain"
	})

	// Water terrain: Passable:false → impassable.
	if m.Passable(waterCell) {
		t.Error("water cell should be impassable via terrain")
	}
	if !math.IsInf(m.StepCost(navmap.Cell{X: 4, Y: 5}, waterCell), 1) {
		t.Error("StepCost into water should be +Inf")
	}

	// Wall footprint on a plain cell → impassable.
	wallCell := navmap.Cell{X: 2, Y: 2}
	m.StampFootprint([]navmap.Cell{wallCell}, false)
	if m.Passable(wallCell) {
		t.Error("wall-stamped cell should be impassable")
	}
	if !math.IsInf(m.StepCost(navmap.Cell{X: 1, Y: 2}, wallCell), 1) {
		t.Error("StepCost into wall should be +Inf")
	}

	// Door gap: cell NOT stamped as wall within a walled area is passable.
	doorCell := navmap.Cell{X: 3, Y: 3}
	m.StampFootprint([]navmap.Cell{{X: 3, Y: 2}, {X: 3, Y: 4}, {X: 2, Y: 3}, {X: 4, Y: 3}}, false)
	if !m.Passable(doorCell) {
		t.Error("door-gap cell (not stamped) should be passable")
	}
}

// ── AC3: Wear lowers cost ──────────────────────────────────────────────────────

func TestWearLowersCost(t *testing.T) {
	t.Parallel()
	m := newMapAllPlain()
	target := navmap.Cell{X: 4, Y: 4}
	src := navmap.Cell{X: 3, Y: 4}

	wantBase := 1.0 * 1.0 * 10.0
	if got := m.StepCost(src, target); math.Abs(got-wantBase) > 1e-9 {
		t.Errorf("pre-wear StepCost = %v, want %v", got, wantBase)
	}

	// Deposit to WearMax → multiplier becomes WearCostMin.
	m.Deposit([]navmap.Cell{target}, testCfg.WearMax)
	wantMax := 1.0 * testCfg.WearCostMin * 10.0
	if got := m.StepCost(src, target); math.Abs(got-wantMax) > 1e-9 {
		t.Errorf("max-wear StepCost = %v, want %v", got, wantMax)
	}

	// Untouched neighbour is unchanged.
	if got := m.StepCost(target, navmap.Cell{X: 5, Y: 4}); math.Abs(got-wantBase) > 1e-9 {
		t.Errorf("untouched neighbour StepCost = %v, want %v", got, wantBase)
	}
}

// ── AC5: SetTerrain transitions cost/TerrainAt/RequiredTags ───────────────────

func TestSetTerrainTransitions(t *testing.T) {
	t.Parallel()
	type tc struct {
		name         string
		to           navmap.TerrainID
		wantPassable bool
		wantTags     []core.Tag
	}
	cases := []tc{
		{"to-forest", "forest", true, nil},
		{"to-swamp", "swamp", true, []core.Tag{"Swim"}},
		{"to-water", "water", false, nil},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			m := newMapAllPlain()
			cell := navmap.Cell{X: 4, Y: 4}
			src := navmap.Cell{X: 3, Y: 4}

			m.Deposit([]navmap.Cell{cell}, 3.0) // pre-transition wear
			m.SetTerrain([]navmap.Cell{cell}, c.to)

			if got := m.Passable(cell); got != c.wantPassable {
				t.Errorf("Passable = %v, want %v", got, c.wantPassable)
			}
			if got := m.TerrainAt(cell); got != c.to {
				t.Errorf("TerrainAt = %q, want %q", got, c.to)
			}
			gotTags := m.RequiredTags(cell)
			if len(gotTags) != len(c.wantTags) {
				t.Errorf("RequiredTags len = %d, want %d", len(gotTags), len(c.wantTags))
			}
			for i, tag := range c.wantTags {
				if i < len(gotTags) && gotTags[i] != tag {
					t.Errorf("RequiredTags[%d] = %q, want %q", i, gotTags[i], tag)
				}
			}

			// StepCost +Inf for impassable; correct with wear for passable.
			gotCost := m.StepCost(src, cell)
			if !c.wantPassable {
				if !math.IsInf(gotCost, 1) {
					t.Errorf("StepCost into impassable = %v, want +Inf", gotCost)
				}
			} else {
				wearMult := 1.0 - (3.0/testCfg.WearMax)*(1.0-testCfg.WearCostMin)
				wantCost := testTypes[c.to].BaseCost * wearMult * 10.0
				if math.Abs(gotCost-wantCost) > 1e-9 {
					t.Errorf("StepCost = %v, want %v", gotCost, wantCost)
				}
			}

			// Wear value is preserved across SetTerrain (layers are independent).
			aw := m.ActiveWear()
			if len(aw) != 1 || aw[0].Cell != cell || math.Abs(aw[0].Wear-3.0) > 1e-9 {
				t.Errorf("ActiveWear = %v; want [{%v 3}]", aw, cell)
			}

			// Neighbour is unchanged (still plain).
			if got := m.TerrainAt(navmap.Cell{X: 5, Y: 4}); got != "plain" {
				t.Errorf("neighbour TerrainAt = %q, want plain", got)
			}
		})
	}
}

// ── AC6: SetTerrain × footprint independence ───────────────────────────────────

func TestSetTerrainFootprintIndependence(t *testing.T) {
	t.Parallel()
	m := newMapAllPlain()
	cell := navmap.Cell{X: 5, Y: 5}

	m.StampFootprint([]navmap.Cell{cell}, false) // wall
	if m.Passable(cell) {
		t.Fatal("wall should be impassable")
	}

	// SetTerrain to passable type: footprint still blocks.
	m.SetTerrain([]navmap.Cell{cell}, "forest")
	if m.Passable(cell) {
		t.Error("footprint-blocked cell must stay impassable after SetTerrain to passable terrain")
	}
	if m.TerrainAt(cell) != "forest" {
		t.Errorf("TerrainAt = %q, want forest", m.TerrainAt(cell))
	}

	// Un-stamp → terrain (forest, passable) now determines passability.
	m.StampFootprint([]navmap.Cell{cell}, true)
	if !m.Passable(cell) {
		t.Error("un-stamped cell with passable terrain should be passable")
	}
	if m.TerrainAt(cell) != "forest" {
		t.Errorf("TerrainAt after un-stamp = %q, want forest", m.TerrainAt(cell))
	}
}

// ── AC7: TerrainOverrides sparse + D12-sorted ──────────────────────────────────

func TestTerrainOverridesSparseAndSorted(t *testing.T) {
	t.Parallel()
	m := newMapAllPlain()

	// Empty before any SetTerrain.
	if ovr := m.TerrainOverrides(); len(ovr) != 0 {
		t.Errorf("TerrainOverrides before SetTerrain = %v", ovr)
	}

	// Transition two cells (not in sorted order) — TerrainOverrides must sort Y-major/X.
	c1 := navmap.Cell{X: 3, Y: 1}             // Y=1 → should come first
	c2 := navmap.Cell{X: 1, Y: 3}             // Y=3
	m.SetTerrain([]navmap.Cell{c2}, "forest") // insert Y=3 first (reverse order)
	m.SetTerrain([]navmap.Cell{c1}, "swamp")

	ovr := m.TerrainOverrides()
	if len(ovr) != 2 {
		t.Fatalf("TerrainOverrides len = %d, want 2", len(ovr))
	}
	if ovr[0].Cell != c1 || ovr[0].Terrain != "swamp" {
		t.Errorf("ovr[0] = %v, want {%v swamp}", ovr[0], c1)
	}
	if ovr[1].Cell != c2 || ovr[1].Terrain != "forest" {
		t.Errorf("ovr[1] = %v, want {%v forest}", ovr[1], c2)
	}

	// Revert c1 to base terrain → drops from override list.
	m.SetTerrain([]navmap.Cell{c1}, "plain")
	ovr2 := m.TerrainOverrides()
	if len(ovr2) != 1 || ovr2[0].Cell != c2 {
		t.Errorf("after revert: TerrainOverrides = %v, want [{%v forest}]", ovr2, c2)
	}
}

// ── Snapshot isolation ─────────────────────────────────────────────────────────

func TestSnapshotIsolation(t *testing.T) {
	t.Parallel()
	m := newMapAllPlain()
	cell := navmap.Cell{X: 3, Y: 3}

	snap := m.Snapshot()

	// Mutations after snapshot do NOT affect the snapshot.
	m.SetTerrain([]navmap.Cell{cell}, "forest")
	m.Deposit([]navmap.Cell{cell}, 5.0)
	m.StampFootprint([]navmap.Cell{{X: 2, Y: 2}}, false)

	if snap.TerrainAt(cell) != "plain" {
		t.Errorf("snapshot TerrainAt = %q, want plain", snap.TerrainAt(cell))
	}
	if aw := snap.ActiveWear(); len(aw) != 0 {
		t.Errorf("snapshot ActiveWear = %v, want empty", aw)
	}
	if snap.FootprintBlocked(navmap.Cell{X: 2, Y: 2}) {
		t.Error("snapshot should not see post-snapshot StampFootprint")
	}
}
