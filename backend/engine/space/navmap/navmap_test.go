// Package navmap_test covers AC1–AC7, snapshot isolation, and shared test fixtures.
// Hex (docs/plans/hex-grid.md): cells are flat-top axial (q,r). Geometry-dependent tests derive cells via
// CellOf(position)+Neighbors instead of hand-computed square coords; adjacent-cell geometric length is
// √3·CellSize (the 6 hex neighbours are equidistant — the square √2-diagonal case is gone).
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

// testCfg is the default config for tests: 100×100 world, hex circumradius 10.
var testCfg = navmap.Config{
	CellSize: 10,
	MinX:     0, MinY: 0, MaxX: 100, MaxY: 100,
	WearOnUse:   1,
	WearOnPave:  5,
	WearDecay:   1,
	WearMax:     10,
	WearCostMin: 0.3,
}

// adjLen is the geometric length of a step between adjacent flat-top hexes = √3·CellSize.
var adjLen = math.Sqrt(3) * testCfg.CellSize

// newMapAllPlain creates a NavMap where every cell is "plain".
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
	// x<50 → "plain" (BaseCost=1), x≥50 → "forest" (BaseCost=2). Position-based sampler, so it works
	// identically over hex cells (each cell samples at its centre).
	m := newMapFn(func(p core.Vec2) navmap.TerrainID {
		if p.X < 50 {
			return "plain"
		}
		return "forest"
	})
	plain := m.CellOf(core.Vec2{X: 20, Y: 50})  // centre x<50 → plain
	forest := m.CellOf(core.Vec2{X: 80, Y: 50}) // centre x≥50 → forest

	costPlain := m.StepCost(m.Neighbors(plain)[0], plain)
	costForest := m.StepCost(m.Neighbors(forest)[0], forest)

	wantPlain := 1.0 * 1.0 * adjLen  // baseCost × wearMult × geoLen(√3·CellSize)
	wantForest := 2.0 * 1.0 * adjLen // forest is 2× plain
	if math.Abs(costPlain-wantPlain) > 1e-9 {
		t.Errorf("plain StepCost = %v, want %v", costPlain, wantPlain)
	}
	if math.Abs(costForest-wantForest) > 1e-9 {
		t.Errorf("forest StepCost = %v, want %v", costForest, wantForest)
	}
	if math.Abs(costForest/costPlain-2.0) > 1e-9 {
		t.Errorf("forest cost should be 2× plain: ratio = %v", costForest/costPlain)
	}

	// Hex equidistance: every one of the 6 neighbours is exactly √3·CellSize away (no √2 diagonal).
	for _, n := range m.Neighbors(plain) {
		if got := m.StepCost(plain, n); math.Abs(got-adjLen) > 1e-9 {
			t.Errorf("neighbour step into %v = %v, want %v (equidistant)", n, got, adjLen)
		}
	}
}

// ── AC2: Impassable ────────────────────────────────────────────────────────────

func TestImpassable(t *testing.T) {
	t.Parallel()
	// x≥50 → water (impassable), else plain.
	m := newMapFn(func(p core.Vec2) navmap.TerrainID {
		if p.X >= 50 {
			return "water"
		}
		return "plain"
	})

	waterCell := m.CellOf(core.Vec2{X: 75, Y: 50})
	if m.Passable(waterCell) {
		t.Error("water cell should be impassable via terrain")
	}
	if !math.IsInf(m.StepCost(m.Neighbors(waterCell)[0], waterCell), 1) {
		t.Error("StepCost into water should be +Inf")
	}

	// Wall footprint on a plain cell → impassable.
	wallCell := m.CellOf(core.Vec2{X: 25, Y: 25})
	m.StampFootprint([]navmap.Cell{wallCell}, false)
	if m.Passable(wallCell) {
		t.Error("wall-stamped cell should be impassable")
	}
	if !math.IsInf(m.StepCost(m.Neighbors(wallCell)[0], wallCell), 1) {
		t.Error("StepCost into wall should be +Inf")
	}

	// Door gap: a cell whose 6 neighbours are all walled but itself is unstamped stays passable.
	doorCell := m.CellOf(core.Vec2{X: 35, Y: 35})
	m.StampFootprint(m.Neighbors(doorCell), false)
	if !m.Passable(doorCell) {
		t.Error("door-gap cell (not stamped) should be passable")
	}
}

// ── AC3: Wear lowers cost ──────────────────────────────────────────────────────

func TestWearLowersCost(t *testing.T) {
	t.Parallel()
	m := newMapAllPlain()
	target := m.CellOf(core.Vec2{X: 50, Y: 50})
	nbrs := m.Neighbors(target)
	src := nbrs[0]

	wantBase := 1.0 * 1.0 * adjLen
	if got := m.StepCost(src, target); math.Abs(got-wantBase) > 1e-9 {
		t.Errorf("pre-wear StepCost = %v, want %v", got, wantBase)
	}

	// Deposit to WearMax → multiplier becomes WearCostMin.
	m.Deposit([]navmap.Cell{target}, testCfg.WearMax)
	wantMax := 1.0 * testCfg.WearCostMin * adjLen
	if got := m.StepCost(src, target); math.Abs(got-wantMax) > 1e-9 {
		t.Errorf("max-wear StepCost = %v, want %v", got, wantMax)
	}

	// An untouched neighbour is unchanged (wear is per-cell).
	if got := m.StepCost(target, nbrs[1]); math.Abs(got-wantBase) > 1e-9 {
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
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			m := newMapAllPlain()
			cell := m.CellOf(core.Vec2{X: 50, Y: 50})
			nbrs := m.Neighbors(cell)
			src := nbrs[0]

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
				wantCost := testTypes[c.to].BaseCost * wearMult * adjLen
				if math.Abs(gotCost-wantCost) > 1e-9 {
					t.Errorf("StepCost = %v, want %v", gotCost, wantCost)
				}
			}

			// Wear value is preserved across SetTerrain (layers are independent).
			aw := m.ActiveWear()
			if len(aw) != 1 || aw[0].Cell != cell || math.Abs(aw[0].Wear-3.0) > 1e-9 {
				t.Errorf("ActiveWear = %v; want [{%v 3}]", aw, cell)
			}

			// A neighbour is unchanged (still plain).
			if got := m.TerrainAt(nbrs[1]); got != "plain" {
				t.Errorf("neighbour TerrainAt = %q, want plain", got)
			}
		})
	}
}

// ── AC6: SetTerrain × footprint independence ───────────────────────────────────

func TestSetTerrainFootprintIndependence(t *testing.T) {
	t.Parallel()
	m := newMapAllPlain()
	cell := m.CellOf(core.Vec2{X: 55, Y: 55})

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

// ── AC7: TerrainOverrides sparse + D12-sorted (R-major then Q, hex) ────────────

func TestTerrainOverridesSparseAndSorted(t *testing.T) {
	t.Parallel()
	m := newMapAllPlain()

	// Empty before any SetTerrain.
	if ovr := m.TerrainOverrides(); len(ovr) != 0 {
		t.Errorf("TerrainOverrides before SetTerrain = %v", ovr)
	}

	// Two in-bounds cells with different R — TerrainOverrides must sort R-major then Q.
	c1 := navmap.Cell{Q: 3, R: 1}             // R=1 → should come first
	c2 := navmap.Cell{Q: 1, R: 3}             // R=3
	m.SetTerrain([]navmap.Cell{c2}, "forest") // insert R=3 first (reverse of sorted order)
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
	cell := navmap.Cell{Q: 3, R: 1}
	wall := navmap.Cell{Q: 2, R: 2}

	snap := m.Snapshot()

	// Mutations after snapshot do NOT affect the snapshot.
	m.SetTerrain([]navmap.Cell{cell}, "forest")
	m.Deposit([]navmap.Cell{cell}, 5.0)
	m.StampFootprint([]navmap.Cell{wall}, false)

	if snap.TerrainAt(cell) != "plain" {
		t.Errorf("snapshot TerrainAt = %q, want plain", snap.TerrainAt(cell))
	}
	if aw := snap.ActiveWear(); len(aw) != 0 {
		t.Errorf("snapshot ActiveWear = %v, want empty", aw)
	}
	if snap.FootprintBlocked(wall) {
		t.Error("snapshot should not see post-snapshot StampFootprint")
	}
}

// ── Hex geometry: Neighbors + CellOf↔CellCenter round-trip (navmap-level SPEC ACs) ─

func TestNeighborsCanonicalAndAdjacent(t *testing.T) {
	t.Parallel()
	m := newMapAllPlain()
	c := m.CellOf(core.Vec2{X: 50, Y: 50})
	ns := m.Neighbors(c)
	if len(ns) != 6 {
		t.Fatalf("Neighbors returned %d, want 6", len(ns))
	}
	// Each neighbour is distinct, ≠ centre, and adjacent (StepCost = √3·CellSize on plain).
	seen := map[navmap.Cell]bool{}
	for _, n := range ns {
		if n == c {
			t.Errorf("neighbour equals centre %v", c)
		}
		if seen[n] {
			t.Errorf("duplicate neighbour %v", n)
		}
		seen[n] = true
		if got := m.StepCost(c, n); math.Abs(got-adjLen) > 1e-9 {
			t.Errorf("neighbour %v not adjacent: StepCost=%v want %v", n, got, adjLen)
		}
	}
	// Canonical order is stable across calls.
	ns2 := m.Neighbors(c)
	for i := range ns {
		if ns[i] != ns2[i] {
			t.Errorf("Neighbors order not stable at %d: %v vs %v", i, ns[i], ns2[i])
		}
	}
}

func TestCellRoundTrip(t *testing.T) {
	t.Parallel()
	m := newMapAllPlain()
	// CellOf(CellCenter(CellOf(p))) == CellOf(p) — the (MinX,MinY) origin offset is applied
	// consistently in both directions (a hex centre maps back to its own hex).
	for _, p := range []core.Vec2{{X: 5, Y: 5}, {X: 50, Y: 50}, {X: 95, Y: 5}, {X: 33, Y: 77}} {
		c := m.CellOf(p)
		if got := m.CellOf(m.CellCenter(c)); got != c {
			t.Errorf("round-trip for p=%v: CellOf(CellCenter(%v))=%v, want %v", p, c, got, c)
		}
	}
}
