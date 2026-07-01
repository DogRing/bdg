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

// A 10×10 grid of 10-unit cells over [0,100]². WearCostMin 0.3 (a fully-worn cell costs 30% of base).
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

// column returns the cells of grid column x for rows in [y0,y1].
func column(x, y0, y1 int) []navmap.Cell {
	out := []navmap.Cell{}
	for y := y0; y <= y1; y++ {
		out = append(out, navmap.Cell{X: x, Y: y})
	}
	return out
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

// ── AC: straight line on uniform cost ──────────────────────────────────────────

func TestStraightLineUniformCost(t *testing.T) {
	m := uniformPlain()
	wps, cost, ok := pathfind.Path(m, core.Vec2{X: 5, Y: 5}, core.Vec2{X: 95, Y: 5}, pathfind.Caps{})
	if !ok {
		t.Fatal("expected a path on an open field")
	}
	// String-pulled: a clear straight line collapses to exactly [start, goal] — NOT cell-stair-stepped.
	if len(wps) != 2 {
		t.Errorf("expected 2 string-pulled waypoints, got %d: %v", len(wps), wps)
	}
	// Cost ≈ Euclidean × base(1) = 90.
	if math.Abs(cost-90) > 1e-6 {
		t.Errorf("cost = %v, want ≈ 90 (Euclidean × base)", cost)
	}
}

// ── AC: routes around a wall + straight through a door ──────────────────────────

func TestRoutesAroundWallAndDoor(t *testing.T) {
	start, goal := core.Vec2{X: 5, Y: 55}, core.Vec2{X: 95, Y: 55} // cells (0,5) → (9,5)

	t.Run("detour around wall (door off the direct row)", func(t *testing.T) {
		m := uniformPlain()
		// Wall column x=5 for every row EXCEPT a door at the top (5,0).
		m.StampFootprint(column(5, 1, 9), false)
		wps, cost, ok := pathfind.Path(m, start, goal, pathfind.Caps{})
		if !ok {
			t.Fatal("goal should be reachable via the top door")
		}
		if cost <= 90 {
			t.Errorf("detour cost = %v, want > 90 (the direct row is blocked)", cost)
		}
		// The path must pass through the door cell (5,0); no waypoint segment may enter a walled cell.
		cells := pathCells(m, wps)
		if !cells[navmap.Cell{X: 5, Y: 0}] {
			t.Errorf("path should pass through the door (5,0); cells=%v", cells)
		}
		for c := range cells {
			if c.X == 5 && c.Y != 0 {
				t.Errorf("path entered a walled cell %v", c)
			}
		}
	})

	t.Run("straight through a door on the direct row", func(t *testing.T) {
		m := uniformPlain()
		// Wall column x=5 except a door at (5,5) — on the direct row, so the path goes straight through.
		walls := append(column(5, 0, 4), column(5, 6, 9)...)
		m.StampFootprint(walls, false)
		wps, cost, ok := pathfind.Path(m, start, goal, pathfind.Caps{})
		if !ok {
			t.Fatal("goal should be reachable straight through the door")
		}
		if math.Abs(cost-90) > 1e-6 {
			t.Errorf("straight-through cost = %v, want ≈ 90", cost)
		}
		if len(wps) != 2 {
			t.Errorf("a clear door line should string-pull to 2 waypoints, got %d", len(wps))
		}
	})
}

// ── AC: prefers the worn (low-cost) corridor over an equal-length unworn one ─────

func TestPrefersWornTrail(t *testing.T) {
	// Maze: the ONLY routes from (0,4) to (9,4) are via the top corridor (row 3) or the bottom (row 5),
	// both length 11. Everything else is walled. We then wear the TOP corridor cheap.
	build := func() *navmap.NavMap {
		m := uniformPlain()
		walls := []navmap.Cell{}
		for x := 0; x <= 9; x++ {
			for y := 0; y <= 9; y++ {
				if y == 3 || y == 5 { // open corridors
					continue
				}
				if (x == 0 || x == 9) && y == 4 { // start/goal cells
					continue
				}
				walls = append(walls, navmap.Cell{X: x, Y: y})
			}
		}
		m.StampFootprint(walls, false)
		return m
	}
	start, goal := core.Vec2{X: 5, Y: 45}, core.Vec2{X: 95, Y: 45} // cells (0,4) → (9,4)

	m := build()
	// Wear the top corridor (row 3) to the max → it costs WearCostMin (0.3) of base, cheaper than the bottom.
	top := []navmap.Cell{}
	for x := 0; x <= 9; x++ {
		top = append(top, navmap.Cell{X: x, Y: 3})
	}
	m.Deposit(top, 100) // ≫ WearMax ⇒ clamped to WearMax ⇒ multiplier = WearCostMin

	wps, _, ok := pathfind.Path(m, start, goal, pathfind.Caps{})
	if !ok {
		t.Fatal("a corridor route should exist")
	}
	cells := pathCells(m, wps)
	usedTop, usedBottom := false, false
	for c := range cells {
		if c.Y == 3 {
			usedTop = true
		}
		if c.Y == 5 {
			usedBottom = true
		}
	}
	if !usedTop || usedBottom {
		t.Errorf("expected the WORN top corridor (row 3) and NOT the bottom (row 5); top=%v bottom=%v",
			usedTop, usedBottom)
	}
}

// ── AC: capability gate ─────────────────────────────────────────────────────────

func TestCapabilityGate(t *testing.T) {
	// A full-height river column at x=5 separates left from right. It is Passable but RequiredTags=[water].
	build := func() *navmap.NavMap {
		return navmap.New(testCfg(), func(p core.Vec2) navmap.TerrainID {
			if int(p.X/10) == 5 {
				return "water"
			}
			return "plain"
		}, plainTypes())
	}
	start, goal := core.Vec2{X: 5, Y: 55}, core.Vec2{X: 95, Y: 55}

	t.Run("no swim capability ⇒ unreachable (river spans fully, no detour)", func(t *testing.T) {
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
		if math.Abs(cost-90) > 1e-6 {
			t.Errorf("crossing cost = %v, want ≈ 90 (straight across)", cost)
		}
	})
}

// ── AC: unreachable (fully walled goal) ─────────────────────────────────────────

func TestUnreachable(t *testing.T) {
	m := uniformPlain()
	// Fully wall the column x=5 (no door) → left and right are disconnected.
	m.StampFootprint(column(5, 0, 9), false)
	if _, _, ok := pathfind.Path(m, core.Vec2{X: 5, Y: 55}, core.Vec2{X: 95, Y: 55}, pathfind.Caps{}); ok {
		t.Error("a fully walled-off goal must return ok=false")
	}
}

// ── AC: determinism + EstimateCost ≤ Path cost ──────────────────────────────────

func TestDeterminismGolden(t *testing.T) {
	m := uniformPlain()
	m.StampFootprint(column(5, 1, 9), false) // a wall with a top door → a non-trivial path
	start, goal := core.Vec2{X: 5, Y: 55}, core.Vec2{X: 95, Y: 55}

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
