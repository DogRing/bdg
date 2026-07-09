package pathfind_test

import (
	"math"
	"reflect"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/space/navmap"
	"github.com/dogring/bdg/engine/space/pathfind"
)

// ── fixtures ──────────────────────────────────────────────────────────────────
//
// Hex (docs/plans/hex-grid.md): cells are flat-top axial. Walls are built by WORLD REGION (stamp every cell
// whose centre falls in a rectangle) instead of square "columns" — geometry-robust. A single hex column
// (e.g. x≈60 ⇒ axial q=4) fully separates left (q≤3) from right (q≥5): to cross q=3→q=5 you must enter
// q=4 (no direct neighbour). Adjacent-cell step cost is √3·CellSize, so hex path cost carries a modest
// overhead (≤ 2/√3 ≈ 1.155×) over the Euclidean distance — assertions use ranges, not exact values.

func testCfg() navmap.Config {
	return navmap.Config{
		CellSize: 10, MinX: 0, MinY: 0, MaxX: 100, MaxY: 100,
		WearOnUse: 1, WearOnPave: 5, WearDecay: 0.1, WearMax: 10, WearCostMin: 0.3,
	}
}

func plainTypes() map[navmap.TerrainID]navmap.TerrainType {
	return map[navmap.TerrainID]navmap.TerrainType{
		"plain": {BaseCost: 1, Passable: true},
		"water": {BaseCost: 1, Passable: true, RequiredTags: []core.Tag{"terrain:water"}},
	}
}

func uniformPlain() *navmap.NavMap {
	return navmap.New(testCfg(), func(core.Vec2) navmap.TerrainID { return "plain" }, plainTypes())
}

// wallRegion stamps every cell whose centre falls in world rect [x0,x1]×[y0,y1] as a wall. Samples
// finely (< hex inradius) so no in-region cell is missed.
func wallRegion(m *navmap.NavMap, x0, x1, y0, y1 float64) {
	seen := map[navmap.Cell]bool{}
	var cells []navmap.Cell
	for x := x0; x <= x1; x += 2 {
		for y := y0; y <= y1; y += 2 {
			c := m.CellOf(core.Vec2{X: x, Y: y})
			if !seen[c] {
				seen[c] = true
				cells = append(cells, c)
			}
		}
	}
	m.StampFootprint(cells, false)
}

// pathCells samples a waypoint polyline densely and returns the set of cells it passes through.
func pathCells(m *navmap.NavMap, wps []core.Vec2) map[navmap.Cell]bool {
	cells := map[navmap.Cell]bool{}
	for i := 0; i+1 < len(wps); i++ {
		a, b := wps[i], wps[i+1]
		d := b.Sub(a)
		dist := math.Hypot(d.X, d.Y)
		n := int(dist) + 1
		for k := 0; k <= n; k++ {
			p := a.Add(d.Scale(float64(k) / float64(n)))
			cells[m.CellOf(p)] = true
		}
	}
	return cells
}

// left/right endpoints used across the wall tests: start (0,3)-ish cell, goal (6,0)-ish cell, both y≈52.
var start = core.Vec2{X: 5, Y: 52}
var goal = core.Vec2{X: 95, Y: 52}

// ── AC: straight line on uniform cost ──────────────────────────────────────────

func TestStraightLineUniformCost(t *testing.T) {
	m := uniformPlain()
	wps, cost, ok := pathfind.Path(m, start, goal, pathfind.Caps{})
	if !ok {
		t.Fatal("expected a path on an open field")
	}
	// String-pulled: a clear straight line collapses to exactly [start, goal] — NOT cell-stair-stepped.
	if len(wps) != 2 {
		t.Errorf("expected 2 string-pulled waypoints, got %d: %v", len(wps), wps)
	}
	// Cost ≈ Euclidean × base, with hex overhead ≤ 2/√3 ≈ 1.155×.
	euclid := start.Distance(goal)
	if cost < euclid-1e-6 || cost > euclid*1.16 {
		t.Errorf("cost = %v, want in [%v, %v] (Euclidean × hex overhead)", cost, euclid, euclid*1.16)
	}
}

// ── AC: routes around a wall + straight through a door ──────────────────────────

func TestRoutesAroundWallAndDoor(t *testing.T) {
	straightCost := func() float64 {
		_, c, _ := pathfind.Path(uniformPlain(), start, goal, pathfind.Caps{})
		return c
	}()

	t.Run("detour around wall (door at the top)", func(t *testing.T) {
		m := uniformPlain()
		// Wall the separating column x≈60 for all rows EXCEPT a door gap at the top (y<26).
		wallRegion(m, 55, 65, 26, 100)
		wps, cost, ok := pathfind.Path(m, start, goal, pathfind.Caps{})
		if !ok {
			t.Fatal("goal should be reachable via the top door")
		}
		if cost <= straightCost {
			t.Errorf("detour cost = %v, want > straight %v (direct crossing blocked)", cost, straightCost)
		}
		cells := pathCells(m, wps)
		// The path must respect the walls and must reach up to the door band (some cell centre y<26).
		reachedDoor := false
		for c := range cells {
			if m.FootprintBlocked(c) {
				t.Errorf("path entered a walled cell %v", c)
			}
			if m.CellCenter(c).Y < 26 {
				reachedDoor = true
			}
		}
		if !reachedDoor {
			t.Error("path should detour up through the top door band (y<26)")
		}
	})

	t.Run("straight through a door on the direct row", func(t *testing.T) {
		m := uniformPlain()
		// Wall column x≈60 except a door on the direct row (q=4,r=1 spans y≈[43,60]): wall y≤42 and y≥62.
		wallRegion(m, 55, 65, 0, 42)
		wallRegion(m, 55, 65, 62, 100)
		wps, cost, ok := pathfind.Path(m, start, goal, pathfind.Caps{})
		if !ok {
			t.Fatal("goal should be reachable straight through the door")
		}
		// A clear door on the line string-pulls to 2 waypoints at ≈ the straight cost.
		if len(wps) != 2 {
			t.Errorf("a clear door line should string-pull to 2 waypoints, got %d: %v", len(wps), wps)
		}
		if math.Abs(cost-straightCost) > 1e-6 {
			t.Errorf("straight-through cost = %v, want ≈ straight %v", cost, straightCost)
		}
	})
}

// ── AC: prefers the worn (low-cost) door over an equal-length unworn one ─────────

func TestPrefersWornTrail(t *testing.T) {
	m := uniformPlain()
	// Wall the separating column x≈60 in the MIDDLE (y∈[30,72]) leaving TWO symmetric doors:
	// top (q=4,r=-1, centre y≈17) and bottom (q=4,r=3, centre y≈87).
	wallRegion(m, 55, 65, 30, 72)
	topDoor := navmap.Cell{Q: 4, R: -1}
	bottomDoor := navmap.Cell{Q: 4, R: 3}
	if m.FootprintBlocked(topDoor) || m.FootprintBlocked(bottomDoor) {
		t.Fatalf("doors must be open: top blocked=%v bottom blocked=%v",
			m.FootprintBlocked(topDoor), m.FootprintBlocked(bottomDoor))
	}

	// Wear the TOP door (and its two same-column neighbours) to the max → that crossing is cheapest.
	m.Deposit([]navmap.Cell{topDoor, {Q: 4, R: -2}, {Q: 3, R: -1}}, 100)

	wps, _, ok := pathfind.Path(m, start, goal, pathfind.Caps{})
	if !ok {
		t.Fatal("a door route should exist")
	}
	cells := pathCells(m, wps)
	if !cells[topDoor] {
		t.Errorf("expected the WORN top door %v to be used; cells=%v", topDoor, cells)
	}
	if cells[bottomDoor] {
		t.Errorf("did not expect the unworn bottom door %v to be used", bottomDoor)
	}
}

// ── AC: capability gate ─────────────────────────────────────────────────────────

func TestCapabilityGate(t *testing.T) {
	// A full-height river column at x≈60 (axial q=4) separates left from right. Passable but
	// RequiredTags=[terrain:water]. The sampler is position-based (x∈[55,65)), so every q=4 cell is water.
	build := func() *navmap.NavMap {
		return navmap.New(testCfg(), func(p core.Vec2) navmap.TerrainID {
			if p.X >= 55 && p.X < 65 {
				return "water"
			}
			return "plain"
		}, plainTypes())
	}

	t.Run("no swim capability ⇒ unreachable (river spans fully)", func(t *testing.T) {
		m := build()
		if _, _, ok := pathfind.Path(m, start, goal, pathfind.Caps{}); ok {
			t.Error("without terrain:water the river should be impassable and the goal unreachable")
		}
	})
	t.Run("with swim capability ⇒ crosses straight", func(t *testing.T) {
		m := build()
		caps := pathfind.Caps{Tags: map[core.Tag]bool{"terrain:water": true}}
		_, cost, ok := pathfind.Path(m, start, goal, caps)
		if !ok {
			t.Fatal("with terrain:water the river should be crossable")
		}
		euclid := start.Distance(goal)
		if cost < euclid-1e-6 || cost > euclid*1.16 {
			t.Errorf("crossing cost = %v, want ≈ straight (in [%v,%v])", cost, euclid, euclid*1.16)
		}
	})
}

// ── AC: unreachable (fully walled goal) ─────────────────────────────────────────

func TestUnreachable(t *testing.T) {
	m := uniformPlain()
	// Fully wall the separating column x≈45 (axial q=3), no door → left and right disconnected.
	wallRegion(m, 40, 50, 0, 100)
	if _, _, ok := pathfind.Path(m, start, goal, pathfind.Caps{}); ok {
		t.Error("a fully walled-off goal must return ok=false")
	}
}

// ── AC: determinism + EstimateCost ≤ Path cost ──────────────────────────────────

func TestDeterminismGolden(t *testing.T) {
	m := uniformPlain()
	wallRegion(m, 55, 65, 26, 100) // wall with a top door → a non-trivial detour path

	w1, c1, ok1 := pathfind.Path(m, start, goal, pathfind.Caps{})
	w2, c2, ok2 := pathfind.Path(m.Snapshot(), start, goal, pathfind.Caps{})
	if !ok1 || !ok2 {
		t.Fatal("expected a path both times")
	}
	if c1 != c2 || !reflect.DeepEqual(w1, w2) {
		t.Errorf("non-deterministic: run1=(%v,%v) run2=(%v,%v)", w1, c1, w2, c2)
	}

	// Admissibility: the cheap EstimateCost lower bound must never exceed the true Path cost.
	est := pathfind.EstimateCost(m, start, goal, pathfind.Caps{})
	if est > c1+1e-9 {
		t.Errorf("EstimateCost %v exceeds true Path cost %v (must be a lower bound)", est, c1)
	}
}
