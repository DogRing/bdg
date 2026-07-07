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
