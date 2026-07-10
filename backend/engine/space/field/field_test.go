package field_test

import (
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/space/field"
	"github.com/dogring/bdg/engine/space/navmap"
)

// ── fixtures (mirror engine/space/pathfind test setup: flat-top hex, 10×10 grid) ──

func testCfg() navmap.Config {
	return navmap.Config{
		CellSize: 10, MinX: 0, MinY: 0, MaxX: 100, MaxY: 100,
		WearOnUse: 1, WearOnPave: 5, WearDecay: 0.1, WearMax: 10, WearCostMin: 0.3,
	}
}

func plainTypes() map[navmap.TerrainID]navmap.TerrainType {
	return map[navmap.TerrainID]navmap.TerrainType{"plain": {BaseCost: 1, Passable: true}}
}

func uniformPlain() *navmap.NavMap {
	return navmap.New(testCfg(), func(core.Vec2) navmap.TerrainID { return "plain" }, plainTypes())
}

const testDecay = 0.02 // per-world-unit intensity decay; weight/decay = reach (weight-1 ≫ weight-0.2)

// leftColumn builds weighted source cells along the world's left edge (x≈5) — a
// vertical "hazard line" so intensity is ~1D: high at the left, decaying toward +X.
func leftColumn(m *navmap.NavMap, weight float64) []field.Source {
	seen := map[navmap.Cell]bool{}
	var cs []field.Source
	for y := 0.0; y <= 100; y += 2 {
		c := m.CellOf(core.Vec2{X: 5, Y: y})
		if !seen[c] {
			seen[c] = true
			cs = append(cs, field.Source{Cell: c, Weight: weight})
		}
	}
	return cs
}

// ── tests ───────────────────────────────────────────────────────────────────────

// A left-edge hazard line makes intensity DECAY toward +X; Gradient points back
// toward the source (−X) and Repulsion (= intensity·away) points AWAY (+X).
func TestIntensityDecaysAndRepulsionPointsAway(t *testing.T) {
	m := uniformPlain()
	f := field.Build(m, leftColumn(m, 1.0), testDecay, nil)

	near := f.IntensityAt(core.Vec2{X: 15, Y: 50})
	far := f.IntensityAt(core.Vec2{X: 40, Y: 50})
	if !(near > far) {
		t.Fatalf("intensity should decay away from the source: near(x=15)=%.3f far(x=40)=%.3f", near, far)
	}
	if near <= 0 {
		t.Fatalf("a cell near the source should carry positive intensity, got %.3f", near)
	}

	rep := f.Repulsion(core.Vec2{X: 25, Y: 50})
	if rep.X <= 0 {
		t.Errorf("repulsion should point +X (away from the left hazard): got %+v", rep)
	}
	if rep.Y > 0.2 || rep.Y < -0.2 {
		t.Errorf("repulsion should be ~horizontal for a vertical hazard line: got %+v", rep)
	}
	if g := f.Gradient(core.Vec2{X: 25, Y: 50}); g.X >= 0 {
		t.Errorf("gradient should point −X toward the source: got %+v", g)
	}
}

// Severity: a STRONGER source is both more intense at a shared point AND reaches
// farther (weight 0.4 dies out by ~x=45; weight 1 still bites past x=70).
func TestStrongerSourceIsMoreIntenseAndReachesFarther(t *testing.T) {
	m := uniformPlain()
	strong := field.Build(m, leftColumn(m, 1.0), testDecay, nil) // reach ~50 units
	weak := field.Build(m, leftColumn(m, 0.2), testDecay, nil)   // reach ~10 units (dies near the source)

	if !(strong.IntensityAt(core.Vec2{X: 10, Y: 50}) > weak.IntensityAt(core.Vec2{X: 10, Y: 50})) {
		t.Errorf("stronger source must be more intense at a shared near point")
	}
	// A point past the weak source's reach but within the strong one's: strong bites, weak is gone.
	if weak.IntensityAt(core.Vec2{X: 35, Y: 50}) != 0 {
		t.Errorf("weak source (short reach) should have died out by x=35, got %.3f", weak.IntensityAt(core.Vec2{X: 35, Y: 50}))
	}
	if strong.IntensityAt(core.Vec2{X: 35, Y: 50}) <= 0 {
		t.Errorf("strong source (long reach) should still bite at x=35, got %.3f", strong.IntensityAt(core.Vec2{X: 35, Y: 50}))
	}
}

// Same (navmap, sources, decay, passable) ⇒ byte-identical field (D12).
func TestDeterministic(t *testing.T) {
	m := uniformPlain()
	src := leftColumn(m, 1.0)
	a := field.Build(m, src, testDecay, nil)
	b := field.Build(m, src, testDecay, nil)
	for x := 5.0; x < 100; x += 7 {
		for y := 5.0; y < 100; y += 7 {
			p := core.Vec2{X: x, Y: y}
			if a.IntensityAt(p) != b.IntensityAt(p) {
				t.Fatalf("intensity not deterministic at %+v: %.9f vs %.9f", p, a.IntensityAt(p), b.IntensityAt(p))
			}
			if a.Repulsion(p) != b.Repulsion(p) {
				t.Fatalf("repulsion not deterministic at %+v: %+v vs %+v", p, a.Repulsion(p), b.Repulsion(p))
			}
		}
	}
}

// A source cell may itself be IMPASSABLE (a cliff / deep-water hazard IS the origin):
// passable neighbours still learn danger and get pushed away from the obstacle.
func TestImpassableSourceStillRepels(t *testing.T) {
	m := uniformPlain()
	src := leftColumn(m, 1.0)
	cells := make([]navmap.Cell, len(src))
	for i, s := range src {
		cells[i] = s.Cell
	}
	m.StampFootprint(cells, false) // the source line is now a hard blocker (impassable)

	f := field.Build(m, src, testDecay, m.Passable)
	if f.IntensityAt(core.Vec2{X: 25, Y: 50}) <= 0 {
		t.Fatalf("passable cell next to an impassable source got 0 intensity — source did not seed")
	}
	if rep := f.Repulsion(core.Vec2{X: 35, Y: 50}); rep.X <= 0 {
		t.Errorf("repulsion should still push +X away from the impassable hazard line: got %+v", rep)
	}
}

// No sources (or weight-0 / no decay) ⇒ zero intensity and zero repulsion.
func TestEmptyOrZeroWeightIsFlat(t *testing.T) {
	m := uniformPlain()
	p := core.Vec2{X: 50, Y: 50}

	empty := field.Build(m, nil, testDecay, nil)
	if empty.IntensityAt(p) != 0 || empty.Repulsion(p) != (core.Vec2{}) {
		t.Errorf("empty field should be flat: intensity %.3f rep %+v", empty.IntensityAt(p), empty.Repulsion(p))
	}

	zero := field.Build(m, leftColumn(m, 0), testDecay, nil)
	if zero.IntensityAt(core.Vec2{X: 5, Y: 50}) != 0 {
		t.Errorf("weight-0 sources should contribute no intensity")
	}
}
