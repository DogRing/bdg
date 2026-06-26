// Package spatial implements a free-coordinate spatial hash index (D11).
//
// The SpatialHash maps ObjectID → Vec2 over a uniform grid of square buckets.
// It answers "which entities are within radius r of point p?" in roughly constant time
// by using the grid as a coarse pre-filter, then filtering with an exact Euclidean check.
//
// The index stores no domain state beyond (id, pos) pairs. It is not safe for
// concurrent mutation; reads (NearbyEntities) are safe only during the read phase
// when no concurrent mutation occurs (engine/world tick model).
package spatial

import (
	"sort"

	"github.com/dogring/bdg/engine/kernel/core"
)

// Entity is the minimal record the index stores: an opaque id and a free coordinate.
// The caller owns the real entity; the index only tracks position.
type Entity struct {
	ID  core.ObjectID
	Pos core.Vec2
}

// cellKey identifies a grid bucket via integer cell coordinates.
// It is a comparable struct (not a formatted string) to avoid per-query allocation
// and to keep iteration deterministic (map iteration is never used for logic — D12).
type cellKey struct {
	cx, cy int
}

// bucketEntry stores one entity in a bucket. We keep both ID and Pos here so that
// NearbyEntities can perform the exact-distance filter without a secondary lookup.
type bucketEntry struct {
	ID  core.ObjectID
	Pos core.Vec2
}

// SpatialHash is a uniform-grid spatial index over free Vec2 coordinates (D11).
// It is an acceleration structure for proximity queries; the final filter is always
// the exact Euclidean distance (DistSq ≤ radius²), so there are no false positives.
type SpatialHash struct {
	cellSize float64
	// invCellSize is 1 / cellSize, precomputed to turn division into multiplication.
	invCellSize float64

	// buckets maps cell coordinates to the entities in that cell.
	buckets map[cellKey][]bucketEntry

	// idToCell is a reverse-index from entity ID to the cell it occupies,
	// used by Move/Remove/PosOf for O(1) lookup. Without this, Move would need
	// to scan all buckets to find an entity.
	idToCell map[core.ObjectID]cellKey

	// entities stores the authoritative position of each indexed entity.
	// This is the single source of truth for PosOf and for rebuilding on resume.
	entities map[core.ObjectID]core.Vec2
}

// New creates an empty index with the given cellSize (square bucket edge length).
// cellSize must be > 0 (panics otherwise). A reasonable default is 8.0,
// matching content/balance.yaml world.spatial_hash_cell (~ average perception radius).
// Choose cellSize ≈ typical query radius for best locality.
func New(cellSize float64) *SpatialHash {
	if cellSize <= 0 {
		panic("spatial: cellSize must be positive")
	}
	return &SpatialHash{
		cellSize:    cellSize,
		invCellSize: 1.0 / cellSize,
		buckets:     make(map[cellKey][]bucketEntry),
		idToCell:    make(map[core.ObjectID]cellKey),
		entities:    make(map[core.ObjectID]core.Vec2),
	}
}

// cellOf returns the integer cell coordinates for position p.
// Uses floor division so that negative coordinates bucket correctly
// (e.g., -0.5 / 8.0 = -0.0625 → floor → -1).
func (h *SpatialHash) cellOf(p core.Vec2) cellKey {
	return cellKey{
		cx: floorDiv(p.X, h.invCellSize),
		cy: floorDiv(p.Y, h.invCellSize),
	}
}

// floorDiv computes floor(v * invScale) efficiently.
// For positive v this is just int(v * invScale).
// For negative v we need to handle the floor correctly since Go's int()
// truncates toward zero.
func floorDiv(v, invScale float64) int {
	f := v * invScale
	if f >= 0 {
		return int(f)
	}
	// For negative values, int() truncates toward zero (ceil for negatives),
	// but we want floor. If f is not an integer, subtract 1.
	// Note: math.Floor is available but we avoid the import.
	// For f < 0, if f != int(f), then int(f) is ceil(f), so subtract 1.
	i := int(f)
	if float64(i) != f {
		return i - 1
	}
	return i
}

// Insert adds id at pos. If id already exists, it is repositioned
// (Insert is idempotent on id: equivalent to Move for an existing id).
func (h *SpatialHash) Insert(id core.ObjectID, pos core.Vec2) {
	// Remove any existing record first (idempotent on id).
	if oldCell, exists := h.idToCell[id]; exists {
		h.removeFromBucket(oldCell, id)
	}
	ck := h.cellOf(pos)
	h.buckets[ck] = append(h.buckets[ck], bucketEntry{ID: id, Pos: pos})
	h.idToCell[id] = ck
	h.entities[id] = pos
}

// Move relocates an existing id to pos. If id is absent it behaves like Insert.
func (h *SpatialHash) Move(id core.ObjectID, pos core.Vec2) {
	ck := h.cellOf(pos)
	oldCell, exists := h.idToCell[id]
	if exists {
		if oldCell == ck {
			// Same cell — just update the position in the bucket entry.
			h.updateInBucket(ck, id, pos)
			h.entities[id] = pos
			return
		}
		h.removeFromBucket(oldCell, id)
	}

	h.buckets[ck] = append(h.buckets[ck], bucketEntry{ID: id, Pos: pos})
	h.idToCell[id] = ck
	h.entities[id] = pos
}

// removeFromBucket removes the entry with the given id from the specified cell.
// No-op if id is not found in the cell.
func (h *SpatialHash) removeFromBucket(ck cellKey, id core.ObjectID) {
	entries := h.buckets[ck]
	for i, e := range entries {
		if e.ID == id {
			// Remove by swapping with the last element and slicing.
			entries[i] = entries[len(entries)-1]
			h.buckets[ck] = entries[:len(entries)-1]
			return
		}
	}
}

// updateInBucket updates the position of the entry with the given id in the
// specified cell. Assumes id exists in the cell.
func (h *SpatialHash) updateInBucket(ck cellKey, id core.ObjectID, pos core.Vec2) {
	entries := h.buckets[ck]
	for i := range entries {
		if entries[i].ID == id {
			entries[i].Pos = pos
			return
		}
	}
}

// Remove deletes id from the index. No-op if id is absent.
func (h *SpatialHash) Remove(id core.ObjectID) {
	oldCell, exists := h.idToCell[id]
	if !exists {
		return
	}
	h.removeFromBucket(oldCell, id)
	delete(h.idToCell, id)
	delete(h.entities, id)
}

// Len returns the number of indexed entities.
func (h *SpatialHash) Len() int {
	return len(h.entities)
}

// PosOf returns the indexed position of id and whether it is present.
func (h *SpatialHash) PosOf(id core.ObjectID) (core.Vec2, bool) {
	pos, ok := h.entities[id]
	return pos, ok
}

// NearbyEntities returns every indexed entity within radius r (inclusive, Euclidean)
// of center. Results are sorted by ascending ObjectID for deterministic iteration (D12).
//
// radius < 0 returns an empty slice; radius == 0 returns only entities exactly at center.
//
// The returned slice is a fresh copy — the caller may retain or sort it freely
// without affecting the index.
func (h *SpatialHash) NearbyEntities(center core.Vec2, radius float64) []Entity {
	if radius < 0 {
		return nil
	}

	radiusSq := radius * radius

	// Determine the integer cell range covering the query disc.
	// The disc of radius r around center spans cells from
	// floor((center - r) / cellSize) to floor((center + r) / cellSize) on each axis.
	minCK := h.cellOf(core.Vec2{X: center.X - radius, Y: center.Y - radius})
	maxCK := h.cellOf(core.Vec2{X: center.X + radius, Y: center.Y + radius})

	// Collect candidates from all covered cells.
	// The nested loop over [minCX, maxCX] × [minCY, maxCY] is deterministic
	// because we iterate integer ranges, not a map.
	var result []Entity
	for cx := minCK.cx; cx <= maxCK.cx; cx++ {
		for cy := minCK.cy; cy <= maxCK.cy; cy++ {
			ck := cellKey{cx: cx, cy: cy}
			entries, ok := h.buckets[ck]
			if !ok {
				continue
			}
			for _, e := range entries {
				// Exact Euclidean distance check — no false positives.
				if e.Pos.DistSq(center) <= radiusSq {
					result = append(result, Entity{ID: e.ID, Pos: e.Pos})
				}
			}
		}
	}

	// Sort by ObjectID for deterministic output (D12).
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})

	return result
}

// NearbyIDs is a lighter variant returning only the ids (same ordering and radius
// semantics as NearbyEntities) for callers that re-look-up the full entity elsewhere.
func (h *SpatialHash) NearbyIDs(center core.Vec2, radius float64) []core.ObjectID {
	entities := h.NearbyEntities(center, radius)
	ids := make([]core.ObjectID, len(entities))
	for i, e := range entities {
		ids[i] = e.ID
	}
	return ids
}
