package exposure

import (
	"go/parser"
	"go/token"
	"io/fs"
	"math"
	"strings"
	"testing"
)

// ── mock Topology: a bounded n×n axial hex grid ──────────────────────────────

// dirs are the 6 axial neighbor offsets in sector order (index s = wind Sector s).
var dirs = [6]Cell{{1, 0}, {1, -1}, {0, -1}, {-1, 0}, {-1, 1}, {0, 1}}

type gridTopo struct{ n int }

func (g gridTopo) Neighbors(c Cell) [6]Cell {
	var out [6]Cell
	for i, d := range dirs {
		out[i] = Cell{c.Q + d.Q, c.R + d.R}
	}
	return out
}

func (g gridTopo) InBounds(c Cell) bool {
	return c.Q >= 0 && c.Q < g.n && c.R >= 0 && c.R < g.n
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-12 }

// ── SectorOf ─────────────────────────────────────────────────────────────────

func TestSectorOf(t *testing.T) {
	cases := []struct {
		dir  float64
		want Sector
	}{
		{0, 0},
		{math.Pi / 6, 0},        // 30° → bin 0 [0,60)
		{math.Pi / 3, 1},        // 60° → bin 1
		{math.Pi, 3},            // 180° → bin 3
		{5 * math.Pi / 3, 5},    // 300° → bin 5
		{2*math.Pi - 1e-9, 5},   // just below 360° stays in bin 5
		{-math.Pi / 6, 5},       // -30° wraps to 330° → bin 5
		{2*math.Pi + math.Pi, 3}, // wraps to 180° → bin 3
	}
	for _, c := range cases {
		if got := SectorOf(Wind{Dir: c.dir}); got != c.want {
			t.Errorf("SectorOf(%.4f) = %d, want %d", c.dir, got, c.want)
		}
	}
}

// ── Empty-field neutrality (AC) ──────────────────────────────────────────────

func TestBuildEmptyNeutral(t *testing.T) {
	f := Build(Config{}, gridTopo{10}, 0, nil, nil)
	if e := f.Epsilon(Cell{3, 3}); e != 1 {
		t.Errorf("empty field Epsilon = %v, want 1", e)
	}
	if a := f.Active(); len(a) != 0 {
		t.Errorf("empty field Active = %v, want empty", a)
	}
	in := Wind{Dir: 1.25, Mag: 0.7}
	if got := f.LocalWind(Cell{3, 3}, in); got != in {
		t.Errorf("empty field LocalWind = %+v, want input %+v", got, in)
	}
}

// ── Directional shadow (AC) — the SPEC's worked example ──────────────────────

func TestBuildDirectionalShadow(t *testing.T) {
	cfg := Config{ShadowCellsPerHeight: 1, ShadowFalloff: 0.5, MinEpsilon: 0}
	b := Blocker{ID: "b", Footprint: []Cell{{2, 2}}, Height: 3, Opacity: 1}
	f := Build(cfg, gridTopo{10}, 0 /* wind +Q */, []Blocker{b}, nil)

	want := map[Cell]float64{
		{3, 2}: 0,   // d=1: atten 1 → ε 0
		{4, 2}: 0.5, // d=2: atten 0.5 → ε 0.5
		{5, 2}: 1,   // d=3: atten 0 → untouched
		{1, 2}: 1,   // upwind
		{2, 2}: 1,   // the blocker cell itself
	}
	for c, w := range want {
		if got := f.Epsilon(c); !approx(got, w) {
			t.Errorf("Epsilon(%v) = %v, want %v", c, got, w)
		}
	}
	// Only (3,2) and (4,2) are non-1, sorted.
	act := f.Active()
	if len(act) != 2 || act[0].Cell != (Cell{3, 2}) || act[1].Cell != (Cell{4, 2}) {
		t.Errorf("Active = %+v, want [(3,2)=0 (4,2)=0.5]", act)
	}
}

// ── Falloff clamps to MinEpsilon (AC) ────────────────────────────────────────

func TestBuildMinEpsilonClamp(t *testing.T) {
	cfg := Config{ShadowCellsPerHeight: 1, ShadowFalloff: 0.5, MinEpsilon: 0.2}
	b := Blocker{ID: "b", Footprint: []Cell{{2, 2}}, Height: 3, Opacity: 1}
	f := Build(cfg, gridTopo{10}, 0, []Blocker{b}, nil)
	if got := f.Epsilon(Cell{3, 2}); !approx(got, 0.2) { // raw 0 clamps up to MinEpsilon
		t.Errorf("Epsilon(3,2) = %v, want MinEpsilon 0.2", got)
	}
	if got := f.Epsilon(Cell{4, 2}); !approx(got, 0.5) {
		t.Errorf("Epsilon(4,2) = %v, want 0.5", got)
	}
}

// ── Interior override (AC) ───────────────────────────────────────────────────

func TestBuildInteriorOverride(t *testing.T) {
	// No blocker shadows (5,5); an Interior forces it to 0 anyway.
	in := Interior{ID: "cave", Cells: []Cell{{5, 5}}, Epsilon: 0}
	f := Build(Config{}, gridTopo{10}, 0, nil, []Interior{in})
	if got := f.Epsilon(Cell{5, 5}); got != 0 {
		t.Errorf("interior Epsilon(5,5) = %v, want 0", got)
	}
	// Interior applied AFTER blockers: overwrites a shadowed cell.
	cfg := Config{ShadowCellsPerHeight: 1, ShadowFalloff: 0, MinEpsilon: 0}
	b := Blocker{ID: "b", Footprint: []Cell{{2, 2}}, Height: 1, Opacity: 0.5}
	in2 := Interior{ID: "c2", Cells: []Cell{{3, 2}}, Epsilon: 0}
	f2 := Build(cfg, gridTopo{10}, 0, []Blocker{b}, []Interior{in2})
	if got := f2.Epsilon(Cell{3, 2}); got != 0 {
		t.Errorf("interior over shadow Epsilon(3,2) = %v, want 0 (overwrite)", got)
	}
}

// ── Multiplicative combination + order independence (AC / D12) ────────────────

func TestBuildMultiplicativeOrderIndependent(t *testing.T) {
	cfg := Config{ShadowCellsPerHeight: 1, ShadowFalloff: 0, MinEpsilon: 0}
	// Two blockers on the SAME footprint each cast atten 0.5 onto (3,2): ε = 0.5*0.5 = 0.25.
	b1 := Blocker{ID: "a", Footprint: []Cell{{2, 2}}, Height: 1, Opacity: 0.5}
	b2 := Blocker{ID: "b", Footprint: []Cell{{2, 2}}, Height: 1, Opacity: 0.5}

	fwd := Build(cfg, gridTopo{10}, 0, []Blocker{b1, b2}, nil)
	rev := Build(cfg, gridTopo{10}, 0, []Blocker{b2, b1}, nil)

	if got := fwd.Epsilon(Cell{3, 2}); !approx(got, 0.25) {
		t.Errorf("multiplicative Epsilon(3,2) = %v, want 0.25", got)
	}
	if a, b := fwd.Active(), rev.Active(); len(a) != len(b) {
		t.Fatalf("order changed Active length: %d vs %d", len(a), len(b))
	} else {
		for i := range a {
			if a[i] != b[i] {
				t.Errorf("order-dependent Active[%d]: %+v vs %+v", i, a[i], b[i])
			}
		}
	}
}

// ── Out-of-bounds stops the shadow walk ──────────────────────────────────────

func TestBuildShadowStopsAtBounds(t *testing.T) {
	cfg := Config{ShadowCellsPerHeight: 1, ShadowFalloff: 0, MinEpsilon: 0}
	// Footprint at the +Q edge: the first downwind step leaves the grid.
	b := Blocker{ID: "b", Footprint: []Cell{{9, 2}}, Height: 3, Opacity: 1}
	f := Build(cfg, gridTopo{10}, 0, []Blocker{b}, nil)
	if a := f.Active(); len(a) != 0 {
		t.Errorf("edge blocker produced shadow %v, want none (out of bounds)", a)
	}
}

// ── LocalWind ────────────────────────────────────────────────────────────────

func TestLocalWind(t *testing.T) {
	cfg := Config{ShadowCellsPerHeight: 1, ShadowFalloff: 0.5, MinEpsilon: 0}
	b := Blocker{ID: "b", Footprint: []Cell{{2, 2}}, Height: 3, Opacity: 1}
	f := Build(cfg, gridTopo{10}, 0, []Blocker{b}, nil)

	g := Wind{Dir: 0.9, Mag: 0.8}
	// (4,2) has ε 0.5 → Mag halved, Dir preserved.
	lw := f.LocalWind(Cell{4, 2}, g)
	if lw.Dir != g.Dir || !approx(lw.Mag, 0.4) {
		t.Errorf("LocalWind(4,2) = %+v, want {Dir 0.9, Mag 0.4}", lw)
	}
	// (3,2) has ε 0 → calm.
	if lw0 := f.LocalWind(Cell{3, 2}, g); !approx(lw0.Mag, 0) {
		t.Errorf("LocalWind(3,2) Mag = %v, want 0", lw0.Mag)
	}
}

// ── Sector cache reuse + invalidation (AC) ───────────────────────────────────

func TestCacheReuseAndInvalidate(t *testing.T) {
	cfg := Config{ShadowCellsPerHeight: 1, ShadowFalloff: 0.5, MinEpsilon: 0}
	c := NewCache(cfg, gridTopo{10})
	bl := []Blocker{{ID: "b", Footprint: []Cell{{2, 2}}, Height: 3, Opacity: 1}}

	f1 := c.Field(0, bl, nil)
	f2 := c.Field(0, bl, nil)
	if f1 != f2 { // same sector, no Invalidate → identical cached pointer (no recompute)
		t.Errorf("cache did not reuse the sector field")
	}
	// A different sector builds a distinct field.
	if f3 := c.Field(3, bl, nil); f3 == f1 {
		t.Errorf("different sector returned the same field")
	}

	c.Invalidate()
	f4 := c.Field(0, nil, nil) // rebuilt with no blockers → neutral
	if f4 == f1 {
		t.Errorf("Invalidate did not drop the cached field")
	}
	if e := f4.Epsilon(Cell{3, 2}); e != 1 {
		t.Errorf("post-invalidate Epsilon(3,2) = %v, want 1 (no blockers)", e)
	}
}

// ── Leaf purity: no forbidden imports (AC) ───────────────────────────────────

func TestNoForbiddenImports(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	forbidden := []string{
		"space/navmap", "env/climate", "space/scent", "engine/fauna",
		"engine/world", "kernel/rng",
	}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			for _, imp := range file.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				if path == "time" {
					t.Errorf("%s imports forbidden %q", name, path)
				}
				for _, bad := range forbidden {
					if strings.Contains(path, bad) {
						t.Errorf("%s imports forbidden %q", name, path)
					}
				}
			}
		}
	}
}
