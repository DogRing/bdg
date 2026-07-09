package navmap

import (
	"math"

	"github.com/dogring/bdg/engine/kernel/core"
)

// Offset (col,row) layout ↔ axial — the rectangular grid the render/wire use (docs/plans/hex-grid.md Q5).
// navmap is the hex authority: worldgen (fixture layout), world (render view + terrain_delta), and
// persist/api all go through THESE methods instead of re-deriving the flat-top odd-q convention.
// The layout is row-major (index i = row*Cols + col); offset(0,0) is the (MinX,MinY) corner (axial 0,0).

// ── Pure Config-level geometry (no NavMap instance / terrain needed) ───────────
// These let an authoring tool (worldgen) index an offset-layout terrain array BEFORE a NavMap exists
// (navmap.New samples terrainAt at construction, so the layout index must be derivable from Config
// alone). The instance methods below delegate to these so there is ONE flat-top odd-q convention.

// OffsetDimsOf returns the offset-grid dims (cols × rows) covering a config's world bounds. See
// OffsetDims for the geometry; returns 0,0 for a degenerate config.
func OffsetDimsOf(cfg Config) (cols, rows int) {
	worldW := cfg.MaxX - cfg.MinX
	worldH := cfg.MaxY - cfg.MinY
	if worldW <= 0 || worldH <= 0 || cfg.CellSize <= 0 {
		return 0, 0
	}
	cols = int(math.Ceil(worldW/(1.5*cfg.CellSize))) + 1
	rows = int(math.Ceil(worldH/(sqrt3*cfg.CellSize))) + 1
	return cols, rows
}

// OffsetIndexAt maps a world point to the offset (col,row) of the flat-top hex containing it, using
// ONLY the config geometry. Mirrors (*NavMap).CellToOffset ∘ CellOf. Out-of-bounds points yield an
// out-of-range offset; callers clamp against OffsetDimsOf before indexing.
func OffsetIndexAt(cfg Config, p core.Vec2) (col, row int) {
	q, r := pixelToHex(p.X-cfg.MinX, p.Y-cfg.MinY, cfg.CellSize)
	return axialToOffset(q, r)
}

// OffsetCenterOf is the inverse read of OffsetIndexAt: the continuous world-space centre of the hex at
// offset(col,row). An authoring tool (worldgen GenerateTerrain) samples noise/fields here so generated
// terrain is isotropic in WORLD space, not grid space. OffsetIndexAt(cfg, OffsetCenterOf(cfg,c,r)) == (c,r).
func OffsetCenterOf(cfg Config, col, row int) core.Vec2 {
	q, r := offsetToAxial(col, row)
	x, y := hexToPixel(q, r, cfg.CellSize)
	return core.Vec2{X: cfg.MinX + x, Y: cfg.MinY + y}
}

// OffsetNeighborsOf enumerates the 6 hex-adjacent offset coords of offset(col,row) in the canonical
// hexDirs order (fixed, D12). Convention-only (no Config — offset adjacency is geometry-independent);
// entries may fall outside the grid, callers bound-check against OffsetDimsOf.
func OffsetNeighborsOf(col, row int) [6][2]int {
	q, r := offsetToAxial(col, row)
	var out [6][2]int
	for i, d := range hexDirs {
		nc, nr := axialToOffset(q+d[0], r+d[1])
		out[i] = [2]int{nc, nr}
	}
	return out
}

// Orientation reports the hex orientation the wire + frontend must mirror ("flat" = flat-top).
func (m *NavMap) Orientation() string { return "flat" }

// OffsetDims returns the offset-grid dimensions (cols × rows) that cover the world bounds. Columns are
// 1.5·CellSize apart in x; rows √3·CellSize apart in y (flat-top). Slightly generous at the far edges
// (a partial-hex fringe) — out-of-range cells render as the default terrain. Returns 0,0 for a
// degenerate config.
func (m *NavMap) OffsetDims() (cols, rows int) { return OffsetDimsOf(m.cfg) }

// OffsetToCell converts an offset(col,row) layout index to its axial Cell (flat-top odd-q).
func (m *NavMap) OffsetToCell(col, row int) Cell {
	q, r := offsetToAxial(col, row)
	return Cell{Q: q, R: r}
}

// CellToOffset converts an axial Cell to its offset(col,row) layout index (flat-top odd-q). The inverse
// of OffsetToCell; callers bound-check col/row against OffsetDims before indexing the render array.
func (m *NavMap) CellToOffset(c Cell) (col, row int) {
	return axialToOffset(c.Q, c.R)
}
