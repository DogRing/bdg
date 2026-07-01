// Package navmap implements the navigation cost field: a grid-indexed traversal-cost
// model over continuous space (D11). It is an *index*, not the world — agent positions
// stay float; this module only quantises *cost*, exactly as spatial quantises *proximity*.
//
// Three independent layers:
//   - Terrain base-cost layer: seeded from a terrainAt sampler at New, mutated only by
//     SetTerrain (climate transitions). TerrainOverrides exposes the sparse delta.
//   - Footprint layer: building-blocked cells. StampFootprint(cells, false) adds walls;
//     StampFootprint(cells, true) removes stamps ("door = passable gap"). Independent of terrain.
//   - Wear field: sparse map[Cell]float64; the emergent desire-path mechanic (D2/D3).
//     Deposit adds wear; Decay fades trails; trails fade absent use (use-it-or-lose-it).
//
// D12: all sorted output (ActiveWear / TerrainOverrides / Decay) uses Y-major then X order.
// No map-iteration drives observable results — raw map keys are sorted before any logic.
package navmap

import (
	"math"
	"sort"

	"github.com/dogring/bdg/engine/kernel/core"
)

// Cell is the integer grid index of a continuous point.
// It is an internal addressing token; it is NEVER written into an agent position (D11).
// CellOf maps a Vec2 → Cell deterministically.
type Cell struct{ X, Y int }

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
	CellSize    float64 // grid cell edge in world units (≈ spatial hash cell; tunable)
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
	return &NavMap{
		cfg:              cfg,
		types:            types,
		terrainSrc:       terrainAt,
		terrainOverrides: make(map[Cell]TerrainID),
		footprint:        make(map[Cell]struct{}),
		wear:             make(map[Cell]float64),
	}
}

// ── Internal helpers ───────────────────────────────────────────────────────────

// inBounds reports whether cell c falls within the configured world bounds.
func (m *NavMap) inBounds(c Cell) bool {
	return c.X >= 0 && c.Y >= 0 &&
		m.cfg.MinX+float64(c.X)*m.cfg.CellSize < m.cfg.MaxX &&
		m.cfg.MinY+float64(c.Y)*m.cfg.CellSize < m.cfg.MaxY
}

// cellCenter returns the centre of cell c in continuous world coordinates.
// Used to sample terrainSrc for the cell's base terrain.
func (m *NavMap) cellCenter(c Cell) core.Vec2 {
	return core.Vec2{
		X: m.cfg.MinX + (float64(c.X)+0.5)*m.cfg.CellSize,
		Y: m.cfg.MinY + (float64(c.Y)+0.5)*m.cfg.CellSize,
	}
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
		return 1.0
	}
	t := w / m.cfg.WearMax
	return 1.0 - t*(1.0-m.cfg.WearCostMin)
}

// sortedCellKeys extracts and sorts the keys of a map[Cell]T in D12 order (Y-major, then X).
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

// sortCells sorts a cell slice in-place Y-major then X (D12 canonical order).
func sortCells(cells []Cell) {
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].Y != cells[j].Y {
			return cells[i].Y < cells[j].Y
		}
		return cells[i].X < cells[j].X
	})
}

// ── Queries (read-only; safe for the parallel plan phase on a Snapshot) ───────

// CellOf maps a continuous Vec2 to its enclosing Cell.
// Deterministic; does not clamp to bounds (out-of-bounds cells are impassable).
// D11: this is the only quantisation point; the source Vec2 is never mutated.
func (m *NavMap) CellOf(p core.Vec2) Cell {
	return Cell{
		X: int(math.Floor((p.X - m.cfg.MinX) / m.cfg.CellSize)),
		Y: int(math.Floor((p.Y - m.cfg.MinY) / m.cfg.CellSize)),
	}
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
// Geometric-length model: octile / 8-connectivity, Euclidean cell-centre distance.
// Cardinal step (|dx|=1, |dy|=0): length = CellSize.
// Diagonal step (|dx|=1, |dy|=1): length = √2·CellSize.
// This is consistent with the 8-connectivity model decided in engine/space/pathfind.
func (m *NavMap) StepCost(from, to Cell) float64 {
	if !m.Passable(to) {
		return math.Inf(1)
	}
	dx := float64(to.X - from.X)
	dy := float64(to.Y - from.Y)
	geoLen := math.Sqrt(dx*dx+dy*dy) * m.cfg.CellSize

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
	return m.types[m.terrainIDAt(c)].RequiredTags
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
		return 0
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
// Iteration is in D12 sorted order (Y-major then X) — never raw map order.
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

// ── Snapshot & serialisation view ─────────────────────────────────────────────

// Snapshot returns a frozen deep-copy of the NavMap for the plan phase.
// pathfind receives a Snapshot and must never mutate it.
// A snapshot taken before a SetTerrain call shows the old terrain.
// The immutable cfg, types, and terrainSrc are shared (not copied).
func (m *NavMap) Snapshot() *NavMap {
	wearCopy := make(map[Cell]float64, len(m.wear))
	for k, v := range m.wear {
		wearCopy[k] = v
	}
	fpCopy := make(map[Cell]struct{}, len(m.footprint))
	for k := range m.footprint {
		fpCopy[k] = struct{}{}
	}
	overCopy := make(map[Cell]TerrainID, len(m.terrainOverrides))
	for k, v := range m.terrainOverrides {
		overCopy[k] = v
	}
	return &NavMap{
		cfg:              m.cfg,
		types:            m.types,      // shared; immutable
		terrainSrc:       m.terrainSrc, // shared; immutable
		terrainOverrides: overCopy,
		footprint:        fpCopy,
		wear:             wearCopy,
	}
}

// ActiveWear returns the sparse wear field in D12 sorted order (Y-major then X).
// Only cells with wear > 0 are included. Used for persist/stream.
func (m *NavMap) ActiveWear() []WearCell {
	cells := sortedWearKeys(m.wear)
	result := make([]WearCell, 0, len(cells))
	for _, c := range cells {
		result = append(result, WearCell{Cell: c, Wear: m.wear[c]})
	}
	return result
}

// TerrainOverrides returns the sparse terrain delta (cells changed by SetTerrain away from
// the New-time base layout) in D12 sorted order. Empty before any SetTerrain is called.
// Cells reverted to their base terrain are omitted (delta-only).
func (m *NavMap) TerrainOverrides() []TerrainCell {
	cells := sortedOverrideKeys(m.terrainOverrides)
	result := make([]TerrainCell, 0, len(cells))
	for _, c := range cells {
		result = append(result, TerrainCell{Cell: c, Terrain: m.terrainOverrides[c]})
	}
	return result
}
