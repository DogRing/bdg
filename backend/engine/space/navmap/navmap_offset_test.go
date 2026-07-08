package navmap_test

import (
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/space/navmap"
)

// The offset(col,row) ↔ axial layout API navmap exposes for the render/wire (docs/hex-grid.md Q5).

func TestOffsetLayoutAPI(t *testing.T) {
	t.Parallel()
	m := newMapAllPlain() // 100×100, CellSize 10

	if got := m.Orientation(); got != "flat" {
		t.Errorf("Orientation = %q, want flat", got)
	}

	cols, rows := m.OffsetDims()
	if cols <= 0 || rows <= 0 {
		t.Fatalf("OffsetDims = %d×%d, want positive", cols, rows)
	}
	// The grid must cover the world: the far corner's offset lands inside the dims.
	col, row := m.CellToOffset(m.CellOf(core.Vec2{X: 99, Y: 99}))
	if col < 0 || col >= cols || row < 0 || row >= rows {
		t.Errorf("far-corner offset (%d,%d) outside grid %d×%d", col, row, cols, rows)
	}

	// OffsetToCell ∘ CellToOffset is identity over the grid; and the mapped cell is in-bounds terrain.
	for r := range rows {
		for c := range cols {
			cell := m.OffsetToCell(c, r)
			if gc, gr := m.CellToOffset(cell); gc != c || gr != r {
				t.Fatalf("offset round-trip (%d,%d)→%v→(%d,%d)", c, r, cell, gc, gr)
			}
		}
	}
}

// TestPureOffsetHelpersMatchInstance asserts the Config-level pure helpers (used by authoring tools like
// worldgen BEFORE a NavMap exists) agree with the instance methods — one flat-top odd-q convention
// (docs/hex-grid.md; navmap is the hex authority).
func TestPureOffsetHelpersMatchInstance(t *testing.T) {
	t.Parallel()
	m := newMapAllPlain() // 100×100, CellSize 10
	cfg := testCfg

	pc, pr := navmap.OffsetDimsOf(cfg)
	ic, ir := m.OffsetDims()
	if pc != ic || pr != ir {
		t.Fatalf("OffsetDimsOf = %d×%d, instance OffsetDims = %d×%d", pc, pr, ic, ir)
	}

	// OffsetIndexAt(cfg, p) must equal CellToOffset(CellOf(p)) for points across the world.
	for _, p := range []core.Vec2{{X: 0, Y: 0}, {X: 5, Y: 5}, {X: 33, Y: 71}, {X: 50, Y: 50}, {X: 99, Y: 1}, {X: 1, Y: 99}, {X: 99, Y: 99}} {
		wc, wr := navmap.OffsetIndexAt(cfg, p)
		ic, ir := m.CellToOffset(m.CellOf(p))
		if wc != ic || wr != ir {
			t.Errorf("OffsetIndexAt(%v) = (%d,%d), want CellToOffset∘CellOf = (%d,%d)", p, wc, wr, ic, ir)
		}
	}
}

// TestOffsetCenterRoundTrip asserts OffsetCenterOf is OffsetIndexAt's inverse read: the centre of
// every grid cell maps back to that cell (the coordinate an authoring tool samples fields at).
func TestOffsetCenterRoundTrip(t *testing.T) {
	t.Parallel()
	cfg := testCfg
	cols, rows := navmap.OffsetDimsOf(cfg)
	for r := range rows {
		for c := range cols {
			p := navmap.OffsetCenterOf(cfg, c, r)
			gc, gr := navmap.OffsetIndexAt(cfg, p)
			if gc != c || gr != r {
				t.Fatalf("centre round-trip (%d,%d) → %v → (%d,%d)", c, r, p, gc, gr)
			}
		}
	}
}

// TestOffsetNeighborsMutual asserts hex adjacency is symmetric and 6-way: if B is a neighbour of A,
// A is a neighbour of B, and all 6 entries are distinct and ≠ self.
func TestOffsetNeighborsMutual(t *testing.T) {
	t.Parallel()
	for _, cell := range [][2]int{{0, 0}, {1, 0}, {0, 1}, {3, 4}, {4, 3}, {7, 7}} {
		nbrs := navmap.OffsetNeighborsOf(cell[0], cell[1])
		seen := map[[2]int]bool{}
		for _, n := range nbrs {
			if n == cell {
				t.Fatalf("cell %v lists itself as neighbour", cell)
			}
			if seen[n] {
				t.Fatalf("cell %v has duplicate neighbour %v", cell, n)
			}
			seen[n] = true
			back := navmap.OffsetNeighborsOf(n[0], n[1])
			mutual := false
			for _, b := range back {
				if b == cell {
					mutual = true
					break
				}
			}
			if !mutual {
				t.Fatalf("adjacency not symmetric: %v→%v but not back", cell, n)
			}
		}
	}
}
