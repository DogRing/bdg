// Package navmap implements the navigation cost field: a FLAT-TOP HEXAGONAL (axial q,r) grid-indexed
// traversal-cost model over continuous space (D11, docs/hex-grid.md). It is an *index*, not the world —
// agent positions stay float; this module only quantises *cost*, exactly as spatial quantises
// *proximity*. Hex geometry lives in hex.go; navmap is the single hex-convention authority (Neighbors,
// CellCenter, sort) that pathfind + the frontend consume and never re-derive.
//
// Three independent layers:
//   - Terrain base-cost layer: seeded from a terrainAt sampler at New, mutated only by
//     SetTerrain (climate transitions). TerrainOverrides exposes the sparse delta.
//   - Footprint layer: building-blocked cells. StampFootprint(cells, false) adds walls;
//     StampFootprint(cells, true) removes stamps ("door = passable gap"). Independent of terrain.
//   - Wear field: sparse map[Cell]float64; the emergent desire-path mechanic (D2/D3).
//     Deposit adds wear; Decay fades trails; trails fade absent use (use-it-or-lose-it).
//
// D12: all sorted output (ActiveWear / TerrainOverrides / Decay) uses R-major then Q order (hex).
// No map-iteration drives observable results — raw map keys are sorted before any logic.
package navmap

import (
	"math"
	"sort"

	"github.com/dogring/bdg/engine/kernel/core"
)

const (
	noWearMultiplier = 1.0
	minCostFallback  = 0.0
)

// Cell is the AXIAL (q,r) index of a flat-top hexagon containing a continuous point.
// It is an internal addressing token; it is NEVER written into an agent position (D11).
// CellOf maps a Vec2 → Cell via the hex primitive (hex.go). The grid origin (axial 0,0 centre)
// is the (MinX,MinY) corner of the world bounds.
type Cell struct{ Q, R int }

// TerrainID names a terrain type from content/terrain.yaml (e.g. "plain", "water", "steep").
type TerrainID = core.Tag

// TerrainType carries the passability and cost metadata for one terrain type.
// Loaded from content/terrain.yaml by platform/config; passed to New.
type TerrainType struct {
	BaseCost     float64    // step-length multiplier; ≥1 for rough terrain
	Passable     bool       // false ⇒ never traversable (deep water without Swim, cliff)
	RequiredTags []core.Tag // traversal needs an action carrying one of these; empty = none
}

// WearCell is one entry in the sparse ActiveWear result (D12-sorted).
type WearCell struct {
	Cell Cell
	Wear float64
}

// TerrainCell is one entry in the sparse TerrainOverrides result (D12-sorted).
type TerrainCell struct {
	Cell    Cell
	Terrain TerrainID
}

// Config holds all tunable knobs for a NavMap.
// All geometry and cost constants must flow from here — no magic numbers in logic (D10).
type Config struct {
	CellSize    float64 // hex circumradius (centre→vertex) in world units; tunable (hex-grid.md Q3)
	MinX, MinY  float64 // world bounds — cells outside are impassable
	MaxX, MaxY  float64
	WearOnUse   float64 // wear the world should pass to Deposit per traversal tick
	WearOnPave  float64 // wear the world should pass to Deposit per Pave action (≫ WearOnUse)
	WearDecay   float64 // wear removed per Decay tick
	WearMax     float64 // wear clamp upper bound
	WearCostMin float64 // multiplier floor at WearMax (e.g. 0.3 ⇒ paved cell = 30% of base cost)
}

// NavMap is the navigation cost field. See package-level docs for layer semantics.
// Ownership: world is the sole mutator (apply phase).
// pathfind receives a Snapshot() and must never mutate.
type NavMap struct {
	cfg        Config
	types      map[TerrainID]TerrainType // immutable; shared across snapshots
	terrainSrc func(core.Vec2) TerrainID // immutable base-layout sampler; shared

	// Terrain override delta (sparse): only cells where SetTerrain changed terrain
	// away from the New-time base. Absent ⇒ query terrainSrc at cell centre.
	terrainOverrides map[Cell]TerrainID

	// Footprint layer: set of footprint-blocked (wall) cells.
	// Present ⇒ impassable regardless of terrain.
	// Absent ⇒ no footprint; terrain decides.
	// "Door" = passable gap = the cell is simply absent from this set.
	footprint map[Cell]struct{}

	// Wear layer: sparse trail field.  Absent ⇒ wear=0.  Clamped to [0, WearMax].
	wear map[Cell]float64
}

// New builds a NavMap for a world of the given bounds and cell size.
//
//   - terrainAt is the per-run layout sampler: continuous Vec2 → TerrainID.
//     It is called with cell centres; its result is the "base" terrain for SetTerrain revert detection.
//   - types is the terrain-type catalog (content/terrain.yaml); all TerrainIDs returned by
//     terrainAt must appear in this map (validated by platform/config at load time).
//
// Both terrainAt and types must remain valid for the lifetime of the NavMap.
func New(cfg Config, terrainAt func(core.Vec2) TerrainID, types map[TerrainID]TerrainType) *NavMap {
	typeCopy := make(map[TerrainID]TerrainType, len(types))
	for id, tt := range types {
		tt.RequiredTags = append([]core.Tag(nil), tt.RequiredTags...)
		typeCopy[id] = tt
	}
	return &NavMap{
		cfg:              cfg,
		types:            typeCopy,
		terrainSrc:       terrainAt,
		terrainOverrides: make(map[Cell]TerrainID),
		footprint:        make(map[Cell]struct{}),
		wear:             make(map[Cell]float64),
	}
}

// ── Internal helpers ───────────────────────────────────────────────────────────

// inBounds reports whether cell c's CENTRE falls within the configured world bounds (a hex is
// in-bounds iff its centre is inside [MinX,MaxX)×[MinY,MaxY) — flat-top hexes near the edge whose
// centre is outside are out of bounds, hex-grid.md Q4).
func (m *NavMap) inBounds(c Cell) bool {
	ctr := m.cellCenter(c)
	return ctr.X >= m.cfg.MinX && ctr.X < m.cfg.MaxX &&
		ctr.Y >= m.cfg.MinY && ctr.Y < m.cfg.MaxY
}

// cellCenter returns the centre of hex cell c in continuous world coordinates (flat-top axial→pixel
// via hex.go, offset by the grid origin = (MinX,MinY)). Used to sample terrainSrc + as the geometric
// anchor for StepCost/bounds.
func (m *NavMap) cellCenter(c Cell) core.Vec2 {
	x, y := hexToPixel(c.Q, c.R, m.cfg.CellSize)
	return core.Vec2{X: m.cfg.MinX + x, Y: m.cfg.MinY + y}
}

// terrainIDAt returns the current TerrainID for cell c.
// Checks terrainOverrides first; falls back to the terrainSrc sampler.
// Does not check bounds; caller is responsible.
func (m *NavMap) terrainIDAt(c Cell) TerrainID {
	if id, ok := m.terrainOverrides[c]; ok {
		return id
	}
	return m.terrainSrc(m.cellCenter(c))
}

// wearMultiplier returns the cost multiplier for cell c derived from its wear.
// Linear from 1.0 at wear=0 to WearCostMin at wear=WearMax (monotone decreasing in wear).
// Multiplier ∈ [WearCostMin, 1].
func (m *NavMap) wearMultiplier(c Cell) float64 {
	w := m.wear[c] // absent ⇒ 0
	if w <= 0 {
		return noWearMultiplier
	}
	t := w / m.cfg.WearMax
	return noWearMultiplier - t*(noWearMultiplier-m.cfg.WearCostMin)
}

// sortedCellKeys extracts and sorts the keys of a map[Cell]T in D12 order (R-major, then Q — hex).
// This indirection is the canonical way to avoid driving logic by map-iteration order (D12).
func sortedWearKeys(m map[Cell]float64) []Cell {
	cells := make([]Cell, 0, len(m))
	for c := range m {
		cells = append(cells, c)
	}
	sortCells(cells)
	return cells
}

func sortedOverrideKeys(m map[Cell]TerrainID) []Cell {
	cells := make([]Cell, 0, len(m))
	for c := range m {
		cells = append(cells, c)
	}
	sortCells(cells)
	return cells
}

// sortCells sorts a cell slice in-place R-major then Q (D12 canonical hex order).
func sortCells(cells []Cell) {
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].R != cells[j].R {
			return cells[i].R < cells[j].R
		}
		return cells[i].Q < cells[j].Q
	})
}

// ── Queries (read-only; safe for the parallel plan phase on a Snapshot) ───────

// CellOf maps a continuous Vec2 to its enclosing Cell.
// Deterministic; does not clamp to bounds (out-of-bounds cells are impassable).
// D11: this is the only quantisation point; the source Vec2 is never mutated.
func (m *NavMap) CellOf(p core.Vec2) Cell {
	q, r := pixelToHex(p.X-m.cfg.MinX, p.Y-m.cfg.MinY, m.cfg.CellSize)
	return Cell{Q: q, R: r}
}

// Neighbors returns c's 6 flat-top axial neighbors in the fixed canonical order (hexDirs) — the SOLE
// neighbor authority. pathfind consumes this and hardcodes no offsets (no hex-convention drift, D12).
func (m *NavMap) Neighbors(c Cell) []Cell {
	out := make([]Cell, len(hexDirs))
	for i, d := range hexDirs {
		out[i] = Cell{Q: c.Q + d[0], R: c.R + d[1]}
	}
	return out
}

// Passable reports whether cell c can be entered.
// Returns false for: out-of-bounds, footprint-blocked (wall stamp), or terrain-impassable cells.
func (m *NavMap) Passable(c Cell) bool {
	if !m.inBounds(c) {
		return false
	}
	if _, blocked := m.footprint[c]; blocked {
		return false
	}
	return m.types[m.terrainIDAt(c)].Passable
}

// TerrainAt returns the current TerrainID for cell c.
// Returns "" for out-of-bounds cells (terrain undefined outside world bounds).
func (m *NavMap) TerrainAt(c Cell) TerrainID {
	if !m.inBounds(c) {
		return ""
	}
	return m.terrainIDAt(c)
}

// StepCost returns the cost to move from cell `from` to cell `to`.
//
//	cost = BaseCost(to) × wearMultiplier(to) × geometricLength(from→to)
//
// Returns +Inf if `to` is impassable (out-of-bounds, footprint-blocked, or terrain-impassable).
//
// Geometric-length model: flat-top hex, Euclidean cell-centre distance. The 6 hex neighbors are
// EQUIDISTANT (√3·CellSize centre-to-centre) — the square √2-diagonal case is gone. General for any
// from/to (uses the hex cell centres, hex-grid.md).
func (m *NavMap) StepCost(from, to Cell) float64 {
	if !m.Passable(to) {
		return math.Inf(1)
	}
	geoLen := m.cellCenter(from).Distance(m.cellCenter(to))

	baseCost := m.types[m.terrainIDAt(to)].BaseCost
	wearMult := m.wearMultiplier(to)
	return baseCost * wearMult * geoLen
}

// RequiredTags returns the capability/action tags needed to enter cell c.
// Returns nil for out-of-bounds cells or terrain with no requirements.
func (m *NavMap) RequiredTags(c Cell) []core.Tag {
	if !m.inBounds(c) {
		return nil
	}
	return append([]core.Tag(nil), m.types[m.terrainIDAt(c)].RequiredTags...)
}

// FootprintBlocked reports whether cell c carries an impassable wall footprint.
// Returns true ONLY for explicit wall stamps — deep-water or other impassable terrain is NOT
// reported here. This enables the fauna TerrainSampler (RESOLVED #FA-navmap option b) to
// distinguish hard walls from high-cost terrain traversable by animals.
func (m *NavMap) FootprintBlocked(c Cell) bool {
	_, blocked := m.footprint[c]
	return blocked
}

// BaseCost returns the terrain base cost for cell c (≥1 for rough terrain per content).
// Ignores wear and footprint. Returns 0 for out-of-bounds cells (undefined).
// Used by the fauna TerrainSampler to compute traversal cost independent of passability.
func (m *NavMap) BaseCost(c Cell) float64 {
	if !m.inBounds(c) {
		return minCostFallback
	}
	return m.types[m.terrainIDAt(c)].BaseCost
}

// CellCenter returns the centre of cell c in continuous world coordinates (the inverse of CellOf,
// up to cell quantisation). Required by engine/space/pathfind to turn a cell path back into
// continuous Vec2 waypoints (navmap is the geometry authority — pathfind never recomputes the cell
// layout). Pure read; valid for any cell index (does not bound-check — pathfind only calls it for
// cells it reached via in-bounds search). D11: this is an index→coordinate read, not a snap.
func (m *NavMap) CellCenter(c Cell) core.Vec2 {
	return m.cellCenter(c)
}

// MinCostFactor returns a guaranteed LOWER BOUND on a cell's effective cost per unit of geometric
// length (= cfg.WearCostMin). Since terrain BaseCost ≥ 1 and the wear multiplier ∈ [WearCostMin, 1],
// the cheapest any step can cost is WearCostMin × its geometric length. engine/space/pathfind uses it
// for an ADMISSIBLE A* heuristic and for EstimateCost (so EstimateCost ≤ true Path cost). Pure read.
func (m *NavMap) MinCostFactor() float64 {
	return m.cfg.WearCostMin
}

// ── Mutations (apply phase only; serial, world-owned) ─────────────────────────

// Deposit adds amount to the wear of each cell in cells.
// Wear is clamped to [0, WearMax] after addition.
// The world calls this with cfg.WearOnUse per traversal tick or cfg.WearOnPave per Pave action.
// Cell order within the slice does not affect results (each cell is updated independently).
func (m *NavMap) Deposit(cells []Cell, amount float64) {
	for _, c := range cells {
		w := m.wear[c] + amount
		if w > m.cfg.WearMax {
			w = m.cfg.WearMax
		}
		if w < 0 {
			w = 0
		}
		m.wear[c] = w
	}
}

// Decay subtracts cfg.WearDecay from every cell in the wear field.
// Iteration is in D12 sorted order (R-major then Q, hex) — never raw map order.
// Cells whose wear reaches ≤0 are removed from the sparse map (fully faded).
func (m *NavMap) Decay() {
	cells := sortedWearKeys(m.wear)
	for _, c := range cells {
		w := m.wear[c] - m.cfg.WearDecay
		if w <= 0 {
			delete(m.wear, c)
		} else {
			m.wear[c] = w
		}
	}
}

// StampFootprint stamps cells as footprint-blocked walls (passable=false) or removes
// them from the footprint layer (passable=true → un-stamp).
//
// Protocol:
//   - Place building: StampFootprint(wall_cells, false)  — cells not in wall_cells are gaps/doors.
//   - Remove building: StampFootprint(all_building_cells, true) — removes all stamps.
//
// The footprint layer is independent of terrain: a footprint-blocked cell stays
// impassable regardless of any SetTerrain calls.
func (m *NavMap) StampFootprint(cells []Cell, passable bool) {
	for _, c := range cells {
		if passable {
			delete(m.footprint, c)
		} else {
			m.footprint[c] = struct{}{}
		}
	}
}

// SetTerrain rewrites the terrain base-cost layer for the given cells to t.
// t must be a key in the types table passed to New; unknown IDs panic (content contract — D10).
//
// The cells slice must be in D12 sorted order (world's responsibility; same convention as
// StampFootprint) — last-write order is fixed and reproducible.
//
// If t equals a cell's New-time base terrain, the override is removed (revert to base);
// TerrainOverrides then omits that cell so the delta stays sparse.
// Does NOT touch the wear or footprint layers.
func (m *NavMap) SetTerrain(cells []Cell, t TerrainID) {
	if _, ok := m.types[t]; !ok {
		panic("navmap: SetTerrain: unknown TerrainID: " + string(t))
	}
	for _, c := range cells {
		base := m.terrainSrc(m.cellCenter(c))
		if t == base {
			delete(m.terrainOverrides, c)
		} else {
			m.terrainOverrides[c] = t
		}
	}
}
