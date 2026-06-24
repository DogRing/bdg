# SPEC — `engine/navmap`

> Status: `DRAFT`
> Leaf level: `L1`  ·  Owner agent: `<filled by implementer>`

## Purpose
The navigation **cost field**: a grid-indexed traversal-cost model over continuous space. It holds
per-cell terrain base cost, building footprints (impassable cells + door portals), and a **sparse
`wear` field** (emergent trails). It answers "how expensive / passable is this location?" for
`pathfind` and accepts wear deposits/decay from `world`. It is an **index, not the world** (D11):
agent positions stay continuous `float`; this module only quantizes *cost*, exactly as `spatial`
quantizes *proximity*.

## Public Interface
```go
// Cell is the integer grid index of a continuous point (internal addressing only;
// never exposed as an agent position). CellOf maps a Vec2 → Cell deterministically.
type Cell struct{ X, Y int }

// TerrainID names a terrain type from content/terrain.yaml (e.g. "plain","water","steep").
type TerrainID = core.Tag

type NavMap struct{ /* opaque; see Owned Data */ }

// New builds a NavMap for a world of the given bounds and cell size, with a terrain
// sampler (per-run layout) and the terrain type table (base cost + required tags).
func New(cfg Config, terrainAt func(core.Vec2) TerrainID, types map[TerrainID]TerrainType) *NavMap

type Config struct {
    CellSize    float64 // grid cell edge in world units (≈ spatial hash cell; tunable)
    MinX, MinY  float64 // world bounds (cells outside bounds are impassable)
    MaxX, MaxY  float64
    WearOnUse   float64 // wear added to each traversed cell per traversal tick
    WearOnPave  float64 // wear added by an explicit Pave action (≫ WearOnUse)
    WearDecay   float64 // wear removed per tick (use-it-or-lose-it; trails fade)
    WearMax     float64 // wear clamp
    WearCostMin float64 // cost multiplier floor at WearMax (e.g. 0.3 ⇒ a paved cell is 30% of base)
}

type TerrainType struct {
    BaseCost     float64    // multiplies step length; ≥1 for rough terrain
    Passable     bool       // false ⇒ never traversable (deep water w/o swim, cliff)
    RequiredTags []core.Tag // traversal needs an action carrying one of these (Swim …); empty = none
}

// ── Queries (read-only; used by pathfind during the parallel plan phase) ──────────
func (m *NavMap) CellOf(p core.Vec2) Cell
func (m *NavMap) Passable(c Cell) bool                 // terrain passable AND no blocking footprint
func (m *NavMap) TerrainAt(c Cell) TerrainID
func (m *NavMap) StepCost(from, to Cell) float64       // base × wear-multiplier × geometric length; +Inf if impassable
func (m *NavMap) RequiredTags(c Cell) []core.Tag       // capability/action tags to enter c (terrain)

// ── Mutations (apply phase only; serial, world-owned) ─────────────────────────────
func (m *NavMap) Deposit(cells []Cell, amount float64) // add wear along a traversed path
func (m *NavMap) Decay()                               // one fixed-order pass; drops fully-faded cells
func (m *NavMap) StampFootprint(cells []Cell, passable bool) // building place/remove (door = passable gap)

// ── Snapshot (frozen read for the plan phase + serialization) ─────────────────────
func (m *NavMap) Snapshot() *NavMap                    // immutable copy-on-write view pathfind reads
func (m *NavMap) ActiveWear() []WearCell               // sparse: only cells with wear>0 (D12-sorted), for persist/stream
type WearCell struct{ Cell Cell; Wear float64 }
```

## Dependencies
- `engine/core` — `Vec2`, `Tag`.

## Owned Data
- **Terrain base-cost layer** — derived once from `terrainAt` + `types` at `New` (immutable after).
- **Footprint layer** — building-blocked cells + door portals; mutated only by `StampFootprint`.
- **`wear` field** — `map[Cell]float64`, **sparse** (only traversed/paved cells present; absent ⇒ 0).
  This is the *road*; it is emergent runtime state, **not** content (D2/D3).
- Ownership: `world` is the sole mutator (Deposit/Decay/StampFootprint in the apply phase). `pathfind`
  receives a `Snapshot()` and must never mutate.

## Invariants
- **D11** — `Cell` is an internal index; it is never written into an agent's `Pos`. Positions stay
  continuous. No public API snaps a position to a cell center.
- **D12** — No `map` iteration drives results: `Decay`, `ActiveWear`, and any cell enumeration iterate
  a **sorted** cell order (Y-major then X, or sorted keys). Float accumulation order is fixed.
- `StepCost` is symmetric only up to terrain; it returns `+Inf` (not an error) for impassable so
  `pathfind` prunes uniformly.
- `Snapshot()` is cheap and isolates the plan phase from concurrent apply-phase mutation (the plan
  phase is read-only on shared state — mirrors `world.currentSnap`).
- `wear` ∈ `[0, WearMax]`; `wear`-cost multiplier ∈ `[WearCostMin, 1]`, monotone decreasing in wear.

## Acceptance Criteria (testable)
- [ ] **Terrain cost** — `StepCost` over a `BaseCost=2` cell is 2× a `BaseCost=1` cell of equal length; table-driven.
- [ ] **Impassable** — `Passable` is false and `StepCost` is `+Inf` for `Passable:false` terrain and for stamped wall footprints; a door cell inside a wall is `Passable:true`.
- [ ] **Wear lowers cost** — after `Deposit` raising a cell to `WearMax`, its `StepCost` = base × `WearCostMin`; an untouched neighbour is unchanged.
- [ ] **Decay fades trails** — with no deposits, repeated `Decay` returns a cell's wear to 0 and **removes** it from `ActiveWear` (sparse); golden over N ticks.
- [ ] **Determinism (golden)** — same `(Config, terrain, deposit/decay sequence)` ⇒ byte-identical `ActiveWear()` ordering and values; no map-iteration leakage.
- [ ] **Bounds** — cells outside `[MinX,MaxX]×[MinY,MaxY]` are impassable.

## Out of Scope
- Path search itself → `engine/pathfind`.
- *When* to deposit/decay and *who* traverses → `engine/world` tick (`docs/map-plan.md §world`).
- Terrain **layout** generation and the terrain type **catalog** parsing → `platform/config` + `content/terrain.yaml` (this module receives `terrainAt`/`types` already built).
- Serialization wire format → `platform/persist` + `docs/data-contracts.md` (this module exposes `ActiveWear()` as the sparse source).

## Open Questions
- **Cell size** (P1-non-blocking): reuse `spatial` cell (8.0) or finer for path fidelity? Tradeoff fidelity vs memory/stream size. Default: start at the spatial cell, make it `Config.CellSize`.
- **8- vs 4-connectivity** is a `pathfind` concern, but `StepCost`'s geometric length term must agree (diagonal = √2). Decided in `pathfind` SPEC.

## Notes
- The wear model is the ant-trail / desire-path mechanic: `Deposit` on traversal, `Decay` each tick,
  `cost = base × f(wear)`. The "road = TTL object" intuition is realized as this decaying sparse field
  (`docs/design.md §5`), **not** a separate object system — pathfinding reads it inline.
- Keep `wear` sparse so persist/stream cost scales with *active trail length*, not world area.
