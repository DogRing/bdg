# SPEC — `engine/spatial`

> Status: `READY`
> Leaf level: `L1`  ·  Owner agent: `implementer`

## Purpose

A free-coordinate spatial index (`SpatialHash`) over a **uniform grid of square buckets**
that answers locality queries — "which entities are within radius `r` of point `p`?" — in
roughly constant time. Implements D11: the world is **not tiled**; entity positions are
unbounded `core.Vec2` floats, and the hash is only an acceleration structure for proximity
(used by `engine/perception` and `engine/world` conflict/interaction checks). It stores
**no domain state** beyond `(id, pos)` pairs.

## Public Interface

```go
package spatial

import "github.com/dogring/bdg/backend/engine/core"

// Entity is the minimal record the index stores: an opaque id and a free coordinate.
// The caller (engine/world) owns the real entity; the index only tracks position.
type Entity struct {
    ID  core.ObjectID // agents, objects, animals share the ObjectID space here
    Pos core.Vec2
}

// SpatialHash is a uniform-grid spatial index over free Vec2 coordinates (D11).
// Not safe for concurrent mutation; the single-threaded apply phase guarantees that
// (engine/world). Reads (NearbyEntities) are safe to call concurrently only if no
// concurrent mutation occurs — the tick model already enforces read/apply separation.
type SpatialHash struct{ /* opaque: cellSize float64; buckets keyed by integer cell coords */ }

// New creates an empty index. cellSize is the square bucket edge length in world units,
// injected from content/balance.yaml (world.spatial_hash_cell) — NOT hardcoded (D10).
// Choose cellSize ≈ the typical query radius for best locality. Panics if cellSize ≤ 0.
func New(cellSize float64) *SpatialHash

// ── Mutation (apply phase only) ──────────────────────────────────────────────

// Insert adds id at pos. If id already exists, it is repositioned (Insert is idempotent
// on id: equivalent to Move for an existing id).
func (h *SpatialHash) Insert(id core.ObjectID, pos core.Vec2)

// Move relocates an existing id to pos. If id is absent it behaves like Insert.
func (h *SpatialHash) Move(id core.ObjectID, pos core.Vec2)

// Remove deletes id from the index. No-op if id is absent.
func (h *SpatialHash) Remove(id core.ObjectID)

// Len returns the number of indexed entities.
func (h *SpatialHash) Len() int

// ── Queries (read phase) ─────────────────────────────────────────────────────

// NearbyEntities returns every indexed entity whose position is within radius r
// (inclusive, Euclidean) of center. The result is sorted by ascending ObjectID for
// deterministic iteration (D12) — the caller never relies on map order. radius < 0
// returns an empty slice; radius == 0 returns only entities exactly at center.
func (h *SpatialHash) NearbyEntities(center core.Vec2, radius float64) []Entity

// NearbyIDs is a lighter variant returning only the ids (same ordering and radius
// semantics as NearbyEntities) for callers that re-look-up the full entity elsewhere.
func (h *SpatialHash) NearbyIDs(center core.Vec2, radius float64) []core.ObjectID

// PosOf returns the indexed position of id and whether it is present.
func (h *SpatialHash) PosOf(id core.ObjectID) (core.Vec2, bool)
```

> `NearbyEntities` performs an exact radius filter (it scans the ring of buckets covering the
> query disc, then keeps only entities with `center.DistSq(pos) ≤ r*r`). Buckets are only a
> coarse pre-filter; the returned set never contains false positives.

## Dependencies

- `engine/core` — `Vec2` (`DistSq`/`Distance`), `ObjectID`. No other imports beyond `sort`.

## Owned Data

- The bucket map (`map[cellKey][]record`) and the `id → cellKey` reverse map are **owned and
  encapsulated**; no other module may read or mutate them. Callers interact only through the
  Public Interface. Returned slices are fresh copies — the caller may retain/sort them freely
  without affecting the index.

## Invariants

- **Free coordinates (D11)**: positions are unbounded `float64`; no clamping to a grid, no
  world-size assumption. The bucket of a point is `floor(p / cellSize)` per axis (works for
  negative coordinates).
- **Deterministic query output (D12)**: `NearbyEntities` / `NearbyIDs` return results sorted
  by `ObjectID`. **Logic never iterates the bucket map in map order** — the candidate set is
  sorted before return. Two runs with the same insert/move/remove sequence yield byte-identical
  query results.
- **Exact radius**: no false positives — the final filter is `DistSq ≤ radius²` using
  `core.Vec2.DistSq` (no sqrt on the hot path).
- **Idempotent identity**: at most one record per `ObjectID`; re-`Insert` repositions rather
  than duplicating.
- **No domain coupling**: the index knows nothing of agents, stats, or perception — only
  `(ID, Pos)`. Sense modeling (LoS, gradients) lives in `engine/perception`.
- `cellSize > 0` always (panic at `New` otherwise); a degenerate cell size would break bucketing.

## Acceptance Criteria (testable)

- [ ] `Insert` then `NearbyEntities(center, r)` returns exactly the entities within `r`
  (table-driven: points inside, exactly on the boundary `dist == r`, and just outside).
- [ ] `NearbyEntities` ordering is ascending `ObjectID` regardless of insertion order
  (insert in shuffled order → assert sorted result) — guards D12.
- [ ] Negative and large-magnitude coordinates bucket correctly (entity at (-1000,-1000)
  is found by a query centered there; not found by a far query).
- [ ] `Move` updates locality: after moving an id out of a query disc it is no longer returned;
  moving it back in returns it again. No duplicate records after repeated `Move`.
- [ ] `Insert` on an existing id repositions (Len unchanged; old location no longer matches).
- [ ] `Remove` drops the id (`PosOf` returns `false`; absent from all queries); `Remove` of an
  absent id is a no-op.
- [ ] `radius < 0` → empty slice; `radius == 0` → only entities exactly at `center`.
- [ ] Cross-cell query: a query disc spanning multiple buckets returns all in-range entities
  across the covered bucket ring (no misses at bucket seams).
- [ ] Determinism / brute-force oracle: for 1 000 random points (seeded `engine/rng`), the
  `NearbyEntities` set equals a naïve O(N) `DistSq ≤ r²` scan, including ordering.
- [ ] Result-aliasing safety: mutating a returned slice does not corrupt the index
  (subsequent query unaffected).

## Out of Scope

- Sense modeling — line-of-sight, smell gradients, hearing falloff → `engine/perception`
  (which consumes this index plus `content/balance.yaml` perception radii).
- Movement/pathing and who-moves-where → `engine/agent` / `engine/world`.
- Conflict resolution when two agents grab the same resource → `engine/world`
  (this module only reports proximity; it does not arbitrate).
- k-nearest-neighbour, ray casts, or polygonal regions — add to this SPEC only if a real
  consumer needs them.

## Open Questions

- **Dynamic re-bucketing**: `cellSize` is fixed at `New`. If profiling at 30–200 agents
  (PRD scale) shows poor locality, a future `Rebuild(newCellSize)` could be added. Does **not**
  block P1 — `content/balance.yaml world.spatial_hash_cell` (8.0) is a reasonable default.

## Notes

- `cellSize` default comes from `content/balance.yaml world.spatial_hash_cell` (= 8.0,
  "~ avg perception radius"); inject it, do not hardcode (D10).
- Bucket key: encode `(floor(x/cellSize), floor(y/cellSize))` as a comparable struct key
  (`struct{cx, cy int}`), **not** a formatted string — avoids per-query allocation and keeps
  it deterministic.
- The query scans buckets in a deterministic nested loop over the integer cell range
  `[floor((center-r)/cellSize) … floor((center+r)/cellSize)]` on each axis, so the *candidate*
  gather order is already fixed; the final sort by `ObjectID` is the determinism guarantee
  callers depend on.
- Snapshot/serialization of positions is `platform/persist` (data-contracts §1 agent/object
  `pos`); the index itself is **derived state** and is rebuilt from positions on resume — it is
  never serialized.
