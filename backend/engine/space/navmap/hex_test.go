package navmap

import (
	"math"
	"math/rand"
	"testing"
)

// H2 property/oracle tests for the flat-top hex primitive (docs/hex-grid.md). These gate H3: the
// primitive must be rock-solid BEFORE navmap/pathfind depend on it (cube rounding is the classic
// hex bug source).

var hexSizes = []float64{1, 8, 12.5}

// A hex centre must map back to its own hex (the exact-centre round-trip).
func TestHexAxialCentreRoundTrip(t *testing.T) {
	for _, size := range hexSizes {
		for q := -25; q <= 25; q++ {
			for r := -25; r <= 25; r++ {
				x, y := hexToPixel(q, r, size)
				gq, gr := pixelToHex(x, y, size)
				if gq != q || gr != r {
					t.Fatalf("size=%v centre round-trip (%d,%d)→(%.4f,%.4f)→(%d,%d)", size, q, r, x, y, gq, gr)
				}
			}
		}
	}
}

// Any point maps to a hex whose centre is within the circumradius (the point is inside that hex),
// incl. negative + fractional coords. This is the "snap to containing hex" guarantee.
func TestHexPixelInsideCell(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, size := range hexSizes {
		for range 8000 {
			x := (rng.Float64()*2 - 1) * 300
			y := (rng.Float64()*2 - 1) * 300
			q, r := pixelToHex(x, y, size)
			cx, cy := hexToPixel(q, r, size)
			if d := math.Hypot(x-cx, y-cy); d > size+1e-9 {
				t.Fatalf("size=%v (%.5f,%.5f)→hex(%d,%d) centre(%.5f,%.5f) dist=%.6f > size", size, x, y, q, r, cx, cy, d)
			}
		}
	}
}

// offset(col,row) ↔ axial(q,r) is a bijection both directions (the wire/fixture ↔ engine bridge).
func TestHexOffsetAxialBijection(t *testing.T) {
	for q := -40; q <= 40; q++ {
		for r := -40; r <= 40; r++ {
			col, row := axialToOffset(q, r)
			if gq, gr := offsetToAxial(col, row); gq != q || gr != r {
				t.Fatalf("axial→offset→axial (%d,%d)→(%d,%d)→(%d,%d)", q, r, col, row, gq, gr)
			}
		}
	}
	for col := -40; col <= 40; col++ {
		for row := -40; row <= 40; row++ {
			q, r := offsetToAxial(col, row)
			if gc, gr := axialToOffset(q, r); gc != col || gr != row {
				t.Fatalf("offset→axial→offset (%d,%d)→(%d,%d)→(%d,%d)", col, row, q, r, gc, gr)
			}
		}
	}
}

// The 6 neighbours are the fixed canonical set, each exactly one step away, all distinct.
func TestHexNeighboursCanonical(t *testing.T) {
	want := [6][2]int{{+1, 0}, {+1, -1}, {0, -1}, {-1, 0}, {-1, +1}, {0, +1}}
	if hexDirs != want {
		t.Fatalf("hexDirs canonical order changed: %v", hexDirs)
	}
	seen := map[[2]int]bool{}
	for _, d := range hexDirs {
		if got := hexDistance(0, 0, d[0], d[1]); got != 1 {
			t.Fatalf("neighbour %v distance=%d, want 1", d, got)
		}
		if seen[d] {
			t.Fatalf("duplicate neighbour %v", d)
		}
		seen[d] = true
	}
	if len(seen) != 6 {
		t.Fatalf("want 6 distinct neighbours, got %d", len(seen))
	}
}

// hexDistance agrees with a BFS over hexDirs (validates distance + neighbours are mutually
// consistent — a cube-round or neighbour-set bug surfaces as a mismatch here). BFS assigns true
// graph distance by level, independent of the formula under test.
func TestHexDistanceMatchesBFS(t *testing.T) {
	const R = 15
	type qr struct{ q, r int }
	dist := map[qr]int{{0, 0}: 0}
	frontier := []qr{{0, 0}}
	for depth := 1; depth <= R; depth++ {
		var next []qr
		for _, c := range frontier {
			for _, d := range hexDirs {
				n := qr{c.q + d[0], c.r + d[1]}
				if _, ok := dist[n]; ok {
					continue
				}
				dist[n] = depth
				next = append(next, n)
			}
		}
		frontier = next
	}
	for c, bfs := range dist {
		if got := hexDistance(0, 0, c.q, c.r); got != bfs {
			t.Fatalf("distance(0,0→%d,%d): formula=%d bfs=%d", c.q, c.r, got, bfs)
		}
	}
}

// The offset(col,row) layout matches the flat-top pixel geometry the renderer relies on: columns are
// 1.5·size apart in x; each row is √3·size apart in y; ODD columns are interlocked by half a row
// (√3/2·size). Locks offset↔axial↔pixel as one coherent system (a wrong offset formula that still
// round-trips would still be caught here).
func TestHexOffsetColumnGeometry(t *testing.T) {
	const size = 8.0
	const eps = 1e-9
	for col := -3; col <= 3; col++ {
		for row := -3; row <= 3; row++ {
			q, r := offsetToAxial(col, row)
			x, y := hexToPixel(q, r, size)
			wantX := 1.5 * size * float64(col)
			wantY := sqrt3*size*float64(row) - float64(col&1)*sqrt3/2*size
			if math.Abs(x-wantX) > eps || math.Abs(y-wantY) > eps {
				t.Fatalf("offset(%d,%d)→pixel(%.6f,%.6f), want (%.6f,%.6f)", col, row, x, y, wantX, wantY)
			}
		}
	}
}

// Pure + deterministic: repeated calls (incl. cell-boundary and large negative points) are identical.
func TestHexDeterministic(t *testing.T) {
	pts := [][2]float64{{0, 0}, {1.5, 0}, {0.75, sqrt3 / 2}, {-1000.5, 7.2}, {12, sqrt3 * 8}, {3, -3 * sqrt3}}
	for _, p := range pts {
		q1, r1 := pixelToHex(p[0], p[1], 8)
		q2, r2 := pixelToHex(p[0], p[1], 8)
		if q1 != q2 || r1 != r2 {
			t.Fatalf("non-deterministic pixelToHex(%.4f,%.4f): (%d,%d) vs (%d,%d)", p[0], p[1], q1, r1, q2, r2)
		}
	}
}
