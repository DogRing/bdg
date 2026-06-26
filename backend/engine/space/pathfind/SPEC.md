# SPEC — `engine/space/pathfind`

> Status: `DRAFT`
> Leaf level: `L2`  ·  Owner agent: `<filled by implementer>`

## Purpose
Deterministic shortest-path search over a `navmap` snapshot. Given a start and goal in continuous
space it returns a **waypoint path** (a polyline of `Vec2`) and its **total traversal cost**, routing
around impassable footprints/terrain and preferring low-`wear` (well-worn) cells. Pure query: it never
mutates the navmap. This is what makes "MoveTo cost reflects the real route" and "reroute around new
obstacles" true.

## Public Interface
```go
// Path finds the cheapest route from `start` to `goal` over the navmap snapshot.
// ok=false when the goal is unreachable (fully walled off / impassable terrain with
// no capability). `cost` is the summed StepCost; `waypoints` is start→…→goal in
// continuous coordinates (string-pulled / Theta* so it is NOT cell-stair-stepped).
func Path(m *navmap.NavMap, start, goal core.Vec2, caps Caps) (waypoints []core.Vec2, cost float64, ok bool)

// EstimateCost is a cheap lower-bound used for action selection / nearest-by-path
// target ranking when a full path is not needed (heuristic, never > true cost).
func EstimateCost(m *navmap.NavMap, start, goal core.Vec2, caps Caps) float64

// Caps describes which gated terrain the searcher may traverse, derived from the
// agent's available action tags / capabilities (e.g. can it Swim?). A cell whose
// navmap.RequiredTags is non-empty is traversable only if Caps satisfies one of them.
type Caps struct{ Tags map[core.Tag]bool }
```

## Dependencies
- `engine/kernel/core` — `Vec2`, `Tag`.
- `engine/space/navmap` — `NavMap` snapshot: `CellOf`, `Passable`, `StepCost`, `RequiredTags`.

## Owned Data
- None persistent. Per-call search state (open/closed sets) is created fresh and discarded — like
  `planner.searchState`. The caller owns the returned slice.

## Invariants
- **D12 determinism** — the priority queue breaks `f`-score ties by a **fixed cell ordering** (then by
  insertion is forbidden; use Cell (Y,X)); neighbour expansion is in a **fixed compass order**; no
  `map` iteration affects the result. Same `(navmap snapshot, start, goal, caps)` ⇒ identical path.
- **Admissible heuristic** — the A* heuristic (and `EstimateCost`) is a lower bound (octile/Euclidean ×
  cheapest terrain cost), so results are optimal and `EstimateCost ≤ Path cost`.
- **Read-only** — never mutates the navmap (wear deposit is `world`'s job, not the searcher's).
- **Bounded** — a `maxExpansions` budget guarantees termination; on exhaustion return `ok=false`
  (caller falls back to straight-line `Move`, never freezes — mirrors locomotion's safety cap).
- Movement cost agrees with `navmap.StepCost` connectivity (diagonal length √2).

## Acceptance Criteria (testable)
- [ ] **Straight line on uniform cost** — open field: `waypoints` ≈ `[start, goal]` (string-pulled), `cost` ≈ Euclidean × base; not cell-stair-stepped.
- [ ] **Routes around a wall** — an impassable footprint between start and goal yields a path whose cost > Euclidean and whose waypoints avoid the blocked cells; through a **door** (passable gap) it goes straight through.
- [ ] **Prefers worn trail** — given two equal-length corridors, one with high `wear` (low cost), `Path` picks the worn one; golden.
- [ ] **Capability gate** — a river (`RequiredTags:[terrain:water]`) is crossed when `Caps` has `terrain:water`, otherwise routed around (or `ok=false` if no detour).
- [ ] **Unreachable** — fully walled goal ⇒ `ok=false`.
- [ ] **Determinism (golden)** — repeated calls and symmetric layouts produce byte-identical waypoint sequences; tie cases resolve by cell order.

## Out of Scope
- Wear deposit/decay → `engine/space/navmap` (mutation) + `engine/world` (when).
- Following the path / advancing position → `engine/agent` execution + `engine/world` movement (`docs/map-plan.md §agent/§world`).
- Caching path results across ticks (perf) → caller (`agent`); see `docs/map-plan.md §perf`.

## Open Questions
- **Algorithm**: A* + post-hoc string-pulling vs **Theta*** (any-angle during search). Theta* gives
  nicer continuous paths but costs LoS checks. Default: A* + funnel/string-pull for P1; revisit.
- **Heuristic weighting**: pure admissible (optimal, slower) vs weighted-A* (faster, slightly
  suboptimal). P1: admissible. Non-blocking.

## Notes
- This is the leaf that turns the abstract "MoveTo" into a real route. `agent.bindTarget` uses
  `EstimateCost` to pick the *cheapest-to-reach* resource (not Euclidean-nearest); `agent.execute`
  consumes `Path` waypoints as the locomotion target sequence; `world` deposits wear along the cells
  the path actually crossed. See `docs/map-plan.md`.
