package spatial

import (
	"fmt"
	"math"
	"sort"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
)

// cellSize used throughout tests. 8.0 matches the content/balance.yaml default.
const testCellSize = 8.0

// ── AC1: Insert then NearbyEntities returns exactly entities within r ──────────

func TestNearbyEntities_RadiusFilter(t *testing.T) {
	tests := []struct {
		name     string
		entities []Entity
		center   core.Vec2
		radius   float64
		wantIDs  []core.ObjectID
	}{
		{
			name: "points inside radius",
			entities: []Entity{
				{ID: "A", Pos: core.Vec2{X: 0, Y: 0}},
				{ID: "B", Pos: core.Vec2{X: 3, Y: 4}}, // dist = 5
				{ID: "C", Pos: core.Vec2{X: 8, Y: 0}}, // dist = 8
			},
			center:  core.Vec2{X: 0, Y: 0},
			radius:  5.0,
			wantIDs: []core.ObjectID{"A", "B"},
		},
		{
			name: "exactly on boundary (dist == r)",
			entities: []Entity{
				{ID: "on_edge", Pos: core.Vec2{X: 6, Y: 8}}, // dist = 10
			},
			center:  core.Vec2{X: 0, Y: 0},
			radius:  10.0,
			wantIDs: []core.ObjectID{"on_edge"},
		},
		{
			name: "just outside boundary",
			entities: []Entity{
				{ID: "inside", Pos: core.Vec2{X: 6, Y: 8}},   // dist = 10
				{ID: "outside", Pos: core.Vec2{X: 6, Y: 8.1}}, // dist ≈ 10.005 > 10
			},
			center:  core.Vec2{X: 0, Y: 0},
			radius:  10.0,
			wantIDs: []core.ObjectID{"inside"},
		},
		{
			name:     "no entities nearby",
			entities: []Entity{{ID: "far", Pos: core.Vec2{X: 100, Y: 100}}},
			center:   core.Vec2{X: 0, Y: 0},
			radius:   10.0,
			wantIDs:  nil,
		},
		{
			name:     "empty index",
			entities: nil,
			center:   core.Vec2{X: 0, Y: 0},
			radius:   10.0,
			wantIDs:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := New(testCellSize)
			for _, e := range tt.entities {
				h.Insert(e.ID, e.Pos)
			}

			got := h.NearbyEntities(tt.center, tt.radius)

			if len(got) != len(tt.wantIDs) {
				t.Fatalf("NearbyEntities returned %d entities, want %d", len(got), len(tt.wantIDs))
			}
			for i, e := range got {
				if e.ID != tt.wantIDs[i] {
					t.Errorf("result[%d].ID = %s, want %s", i, e.ID, tt.wantIDs[i])
				}
			}
		})
	}
}

// ── AC2: NearbyEntities ordering is ascending ObjectID ────────────────────────

func TestNearbyEntities_DeterministicOrder(t *testing.T) {
	h := New(testCellSize)

	// Insert entities in shuffled order (B, A, C, D).
	// All are within the same cell and within radius of center.
	entities := []Entity{
		{ID: "B", Pos: core.Vec2{X: 1, Y: 1}},
		{ID: "A", Pos: core.Vec2{X: 0, Y: 0}},
		{ID: "D", Pos: core.Vec2{X: 3, Y: 2}},
		{ID: "C", Pos: core.Vec2{X: 2, Y: 1}},
	}
	for _, e := range entities {
		h.Insert(e.ID, e.Pos)
	}

	got := h.NearbyEntities(core.Vec2{X: 0, Y: 0}, 10.0)
	if len(got) != 4 {
		t.Fatalf("expected 4 entities, got %d", len(got))
	}

	wantIDs := []core.ObjectID{"A", "B", "C", "D"}
	for i, e := range got {
		if e.ID != wantIDs[i] {
			t.Errorf("result[%d].ID = %s, want %s", i, e.ID, wantIDs[i])
		}
	}

	// Verify the result is sorted even when we query a subset.
	// Insert an entity far away that bucketed separately.
	h.Insert(core.ObjectID("Z"), core.Vec2{X: 100, Y: 100})
	got = h.NearbyEntities(core.Vec2{X: 0, Y: 0}, 10.0)
	if len(got) != 4 {
		t.Fatalf("expected 4 entities near origin, got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].ID < got[i-1].ID {
			t.Errorf("result not sorted: %s < %s", got[i].ID, got[i-1].ID)
		}
	}
}

// ── AC3: Negative and large-magnitude coordinates bucket correctly ────────────

func TestNearbyEntities_NegativeCoordinates(t *testing.T) {
	h := New(testCellSize)

	h.Insert("far_neg", core.Vec2{X: -1000, Y: -1000})
	h.Insert("close_neg", core.Vec2{X: -5, Y: -5})

	// Should find close_neg at center and not far_neg.
	got := h.NearbyEntities(core.Vec2{X: -5, Y: -5}, 2.0)
	if len(got) != 1 || got[0].ID != "close_neg" {
		t.Errorf("expected [close_neg], got %v", entityIDs(got))
	}

	// Query centered at (-1000,-1000) should find it.
	got = h.NearbyEntities(core.Vec2{X: -1000, Y: -1000}, 1.0)
	if len(got) != 1 || got[0].ID != "far_neg" {
		t.Errorf("expected [far_neg], got %v", entityIDs(got))
	}

	// A far query from origin should NOT find far_neg (dist ≈ 1414).
	got = h.NearbyEntities(core.Vec2{X: 0, Y: 0}, 100.0)
	if len(got) != 1 || got[0].ID != "close_neg" {
		t.Errorf("expected [close_neg], got %v", entityIDs(got))
	}
}

// ── AC4: Move updates locality ─────────────────────────────────────────────────

func TestMove_UpdatesLocality(t *testing.T) {
	h := New(testCellSize)

	h.Insert("movable", core.Vec2{X: 0, Y: 0})
	h.Insert("static", core.Vec2{X: 3, Y: 0})

	// Both are within radius 5 of origin.
	got := h.NearbyEntities(core.Vec2{X: 0, Y: 0}, 5.0)
	if len(got) != 2 {
		t.Fatalf("expected 2 entities, got %d", len(got))
	}

	// Move 'movable' far away.
	h.Move("movable", core.Vec2{X: 100, Y: 100})
	got = h.NearbyEntities(core.Vec2{X: 0, Y: 0}, 5.0)
	if len(got) != 1 || got[0].ID != "static" {
		t.Errorf("expected [static] after move, got %v", entityIDs(got))
	}

	// Move 'movable' back — should reappear.
	h.Move("movable", core.Vec2{X: 1, Y: 1})
	got = h.NearbyEntities(core.Vec2{X: 0, Y: 0}, 5.0)
	if len(got) != 2 {
		t.Fatalf("expected 2 entities after moving back, got %d", len(got))
	}
}

func TestMove_NoDuplicateRecords(t *testing.T) {
	h := New(testCellSize)

	h.Insert("dup_test", core.Vec2{X: 0, Y: 0})
	// Move repeatedly within the same cell and across cells.
	for i := 0; i < 10; i++ {
		h.Move("dup_test", core.Vec2{X: float64(i), Y: float64(i)})
	}
	if h.Len() != 1 {
		t.Errorf("Len() = %d, want 1 (duplicate records after repeated Move)", h.Len())
	}

	// Check that NearbyEntities doesn't return duplicates.
	got := h.NearbyEntities(core.Vec2{X: 0, Y: 0}, 100.0)
	if len(got) != 1 {
		t.Errorf("NearbyEntities returned %d entries, want 1 (no duplicates)", len(got))
	}
}

// ── AC5: Insert on existing id repositions ─────────────────────────────────────

func TestInsert_RepositionsExisting(t *testing.T) {
	h := New(testCellSize)

	h.Insert("repos", core.Vec2{X: 0, Y: 0})
	h.Insert("other", core.Vec2{X: 2, Y: 2})

	// Sanity: both are nearby.
	if h.Len() != 2 {
		t.Fatalf("Len = %d, want 2", h.Len())
	}

	// Re-Insert "repos" at a far location.
	h.Insert("repos", core.Vec2{X: 100, Y: 100})

	if h.Len() != 2 {
		t.Errorf("Len = %d, want 2 (idempotent on id)", h.Len())
	}

	// "repos" should no longer be near origin.
	got := h.NearbyEntities(core.Vec2{X: 0, Y: 0}, 10.0)
	if len(got) != 1 || got[0].ID != "other" {
		t.Errorf("expected [other] after repos reposition, got %v", entityIDs(got))
	}

	// "repos" should be findable at its new location.
	got = h.NearbyEntities(core.Vec2{X: 100, Y: 100}, 1.0)
	if len(got) != 1 || got[0].ID != "repos" {
		t.Errorf("expected [repos] at new location, got %v", entityIDs(got))
	}
}

// ── AC6: Remove drops the id ───────────────────────────────────────────────────

func TestRemove_DropsID(t *testing.T) {
	h := New(testCellSize)

	h.Insert("gone", core.Vec2{X: 0, Y: 0})
	h.Insert("stays", core.Vec2{X: 2, Y: 2})

	// PosOf before remove.
	if _, ok := h.PosOf("gone"); !ok {
		t.Error("PosOf(gone) should be true before remove")
	}

	h.Remove("gone")

	// PosOf after remove.
	if _, ok := h.PosOf("gone"); ok {
		t.Error("PosOf(gone) should be false after remove")
	}
	if h.Len() != 1 {
		t.Errorf("Len = %d, want 1", h.Len())
	}

	// Query should not return "gone".
	got := h.NearbyEntities(core.Vec2{X: 0, Y: 0}, 5.0)
	if len(got) != 1 || got[0].ID != "stays" {
		t.Errorf("expected [stays], got %v", entityIDs(got))
	}
}

func TestRemove_AbsentID_Noop(t *testing.T) {
	h := New(testCellSize)
	h.Insert("a", core.Vec2{X: 0, Y: 0})
	h.Remove("nonexistent")
	if h.Len() != 1 {
		t.Errorf("Len = %d, want 1 (remove of absent id should be no-op)", h.Len())
	}
}

// ── AC7: radius < 0 → empty; radius == 0 → only exact matches ─────────────────

func TestRadiusEdgeCases(t *testing.T) {
	h := New(testCellSize)

	h.Insert("at_center", core.Vec2{X: 0, Y: 0})
	h.Insert("near", core.Vec2{X: 1, Y: 1})
	h.Insert("far", core.Vec2{X: 10, Y: 10})

	t.Run("negative radius returns empty", func(t *testing.T) {
		got := h.NearbyEntities(core.Vec2{X: 0, Y: 0}, -1.0)
		if got != nil {
			t.Errorf("expected nil for negative radius, got %v", entityIDs(got))
		}
	})

	t.Run("radius zero returns only exact match", func(t *testing.T) {
		got := h.NearbyEntities(core.Vec2{X: 0, Y: 0}, 0.0)
		if len(got) != 1 || got[0].ID != "at_center" {
			t.Errorf("expected [at_center], got %v", entityIDs(got))
		}
	})

	t.Run("zero radius no exact match returns empty", func(t *testing.T) {
		got := h.NearbyEntities(core.Vec2{X: 5, Y: 5}, 0.0)
		if len(got) != 0 {
			t.Errorf("expected empty for radius=0 no exact match, got %v", entityIDs(got))
		}
	})
}

// ── AC8: Cross-cell query — no misses at bucket seams ────────────────────────

func TestCrossCellQuery(t *testing.T) {
	// Use a small cellSize to force cross-cell coverage.
	h := New(4.0) // 4-unit cells

	// Place entities in a grid pattern covering multiple cells.
	// With cellSize=4, points at:
	//   (2, 2)   → cell (0, 0)
	//   (5, 2)   → cell (1, 0)
	//   (2, 5)   → cell (0, 1)
	//   (5, 5)   → cell (1, 1)
	//   (-1, -1) → cell (-1, -1)
	entities := []Entity{
		{ID: "a", Pos: core.Vec2{X: 2, Y: 2}},
		{ID: "b", Pos: core.Vec2{X: 5, Y: 2}},
		{ID: "c", Pos: core.Vec2{X: 2, Y: 5}},
		{ID: "d", Pos: core.Vec2{X: 5, Y: 5}},
		{ID: "neg", Pos: core.Vec2{X: -1, Y: -1}},
	}
	for _, e := range entities {
		h.Insert(e.ID, e.Pos)
	}

	// Query centered at (3.5, 3.5) with radius 3.0 should cover all four
	// entities in the positive quadrant (distances ≈ 2.1, 2.1, 2.1, 2.1).
	// The entity at (-1,-1) is at ≈ 6.4, outside the radius.
	got := h.NearbyEntities(core.Vec2{X: 3.5, Y: 3.5}, 3.0)
	if len(got) != 4 {
		t.Errorf("expected 4 entities in cross-cell query, got %d: %v", len(got), entityIDs(got))
	}
	// IDs should be sorted: a, b, c, d
	wantIDs := []core.ObjectID{"a", "b", "c", "d"}
	for i, e := range got {
		if e.ID != wantIDs[i] {
			t.Errorf("result[%d] = %s, want %s", i, e.ID, wantIDs[i])
		}
	}
}

// ── AC9: Determinism / brute-force oracle ──────────────────────────────────────

func TestDeterminism_BruteForceOracle(t *testing.T) {
	// Generate 1000 points and 50 query centers using a deterministic seeded PRNG.
	rng := newSeededRand(42)

	h := New(testCellSize)
	type pt struct {
		id  core.ObjectID
		pos core.Vec2
	}
	var points []pt

	// Generate 1000 points in a [-100, 100] square.
	for i := 0; i < 1000; i++ {
		id := core.ObjectID(fmt.Sprintf("pt-%05d", i))
		pos := core.Vec2{X: rng.Float64()*200 - 100, Y: rng.Float64()*200 - 100}
		points = append(points, pt{id, pos})
		h.Insert(id, pos)
	}

	// Run 50 queries at random positions within the square.
	for q := 0; q < 50; q++ {
		center := core.Vec2{X: rng.Float64()*160 - 80, Y: rng.Float64()*160 - 80}
		radius := rng.Float64()*25 + 5 // [5, 30)

		got := h.NearbyEntities(center, radius)

		// Brute-force: scan all points and keep those within radius.
		var expected []Entity
		for _, p := range points {
			if p.pos.DistSq(center) <= radius*radius {
				expected = append(expected, Entity{ID: p.id, Pos: p.pos})
			}
		}
		sort.Slice(expected, func(i, j int) bool {
			return expected[i].ID < expected[j].ID
		})
		if len(got) != len(expected) {
			t.Fatalf("query %d: got %d entities, brute-force %d", q, len(got), len(expected))
		}
		for i := range got {
			if got[i].ID != expected[i].ID {
				t.Fatalf("query %d: result[%d].ID = %s, expected %s", q, i, got[i].ID, expected[i].ID)
			}
		}

		// Verify that NearbyIDs matches the IDs from NearbyEntities.
		gotIDs := h.NearbyIDs(center, radius)
		if len(gotIDs) != len(got) {
			t.Fatalf("query %d: NearbyIDs returned %d, NearbyEntities %d", q, len(gotIDs), len(got))
		}
		for i := range gotIDs {
			if gotIDs[i] != got[i].ID {
				t.Fatalf("query %d: NearbyIDs[%d] = %s, NearbyEntities[%d].ID = %s", q, i, gotIDs[i], i, got[i].ID)
			}
		}
	}
}

// ── AC10: Result-aliasing safety ──────────────────────────────────────────────

func TestResultAliasingSafety(t *testing.T) {
	h := New(testCellSize)

	h.Insert("A", core.Vec2{X: 0, Y: 0})
	h.Insert("B", core.Vec2{X: 1, Y: 1})

	got := h.NearbyEntities(core.Vec2{X: 0, Y: 0}, 5.0)

	// Mutate the returned slice.
	got[0] = Entity{ID: "MUTATED", Pos: core.Vec2{X: 999, Y: 999}}

	// The index should be unaffected.
	got2 := h.NearbyEntities(core.Vec2{X: 0, Y: 0}, 5.0)
	if len(got2) != 2 {
		t.Fatalf("expected 2 entities after mutation, got %d", len(got2))
	}
	if got2[0].ID == "MUTATED" {
		t.Error("mutation of returned slice affected index — aliasing bug")
	}
}

// ── Additional tests: Move within same cell, Len, PosOf ────────────────────

func TestMove_SameCell(t *testing.T) {
	h := New(testCellSize)

	h.Insert("moving", core.Vec2{X: 0, Y: 0})
	// Move within the same cell.
	h.Move("moving", core.Vec2{X: 3, Y: 3})

	// Should still be findable nearby.
	got := h.NearbyEntities(core.Vec2{X: 0, Y: 0}, 10.0)
	if len(got) != 1 || got[0].ID != "moving" {
		t.Errorf("expected [moving], got %v", entityIDs(got))
	}

	// Position should be updated.
	pos, ok := h.PosOf("moving")
	if !ok {
		t.Fatal("PosOf(moving) should be true after move")
	}
	if pos.X != 3 || pos.Y != 3 {
		t.Errorf("PosOf(moving) = %v, want (3,3)", pos)
	}
}

func TestLen_And_PosOf(t *testing.T) {
	h := New(testCellSize)

	if h.Len() != 0 {
		t.Errorf("new hash should have Len = 0, got %d", h.Len())
	}

	h.Insert("a", core.Vec2{X: 0, Y: 0})
	h.Insert("b", core.Vec2{X: 1, Y: 1})
	if h.Len() != 2 {
		t.Errorf("Len = %d, want 2", h.Len())
	}

	// PosOf existing and non-existing.
	if _, ok := h.PosOf("a"); !ok {
		t.Error("PosOf(a) should be true")
	}
	if _, ok := h.PosOf("nonexistent"); ok {
		t.Error("PosOf(nonexistent) should be false")
	}

	h.Remove("a")
	if h.Len() != 1 {
		t.Errorf("Len after remove = %d, want 1", h.Len())
	}
	if _, ok := h.PosOf("a"); ok {
		t.Error("PosOf(a) should be false after remove")
	}
}

// ── New panics on cellSize <= 0 ────────────────────────────────────────────────

func TestNew_PanicsOnInvalidCellSize(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("New(0) should panic")
		}
	}()
	New(0)
}

func TestNew_PanicsOnNegativeCellSize(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("New(-1) should panic")
		}
	}()
	New(-1)
}

// ── NearbyIDs consistency with NearbyEntities ─────────────────────────────────

func TestNearbyIDs_Consistency(t *testing.T) {
	h := New(testCellSize)

	entities := []Entity{
		{ID: "Z", Pos: core.Vec2{X: 10, Y: 10}},
		{ID: "A", Pos: core.Vec2{X: 0, Y: 0}},
		{ID: "M", Pos: core.Vec2{X: 5, Y: 5}},
		{ID: "N", Pos: core.Vec2{X: -1, Y: -1}},
	}
	for _, e := range entities {
		h.Insert(e.ID, e.Pos)
	}

	gotIDs := h.NearbyIDs(core.Vec2{X: 0, Y: 0}, 8.0)
	got := h.NearbyEntities(core.Vec2{X: 0, Y: 0}, 8.0)

	if len(gotIDs) != len(got) {
		t.Fatalf("NearbyIDs len=%d, NearbyEntities len=%d", len(gotIDs), len(got))
	}
	for i := range gotIDs {
		if gotIDs[i] != got[i].ID {
			t.Errorf("NearbyIDs[%d]=%s, NearbyEntities[%d].ID=%s", i, gotIDs[i], i, got[i].ID)
		}
	}
}

// ── Large coordinate values ───────────────────────────────────────────────────

func TestLargeCoordinates(t *testing.T) {
	h := New(testCellSize)

	h.Insert("big", core.Vec2{X: 1e6, Y: 1e6})
	h.Insert("small", core.Vec2{X: 1, Y: 1})

	got := h.NearbyEntities(core.Vec2{X: 1e6, Y: 1e6}, 1.0)
	if len(got) != 1 || got[0].ID != "big" {
		t.Errorf("expected [big] at large coords, got %v", entityIDs(got))
	}

	got = h.NearbyEntities(core.Vec2{X: 0, Y: 0}, 2.0)
	if len(got) != 1 || got[0].ID != "small" {
		t.Errorf("expected [small] at origin, got %v", entityIDs(got))
	}
}

// ── Determinism repeatability: same inserts → same query results ──────────────

func TestDeterministicRepeatability(t *testing.T) {
	// Build two identical hashes and verify queries match.
	build := func() *SpatialHash {
		h := New(testCellSize)
		entities := []Entity{
			{ID: "C", Pos: core.Vec2{X: 10, Y: -5}},
			{ID: "A", Pos: core.Vec2{X: 0, Y: 0}},
			{ID: "B", Pos: core.Vec2{X: -3, Y: 7}},
			{ID: "D", Pos: core.Vec2{X: 4, Y: -2}},
		}
		for _, e := range entities {
			h.Insert(e.ID, e.Pos)
		}
		return h
	}

	h1 := build()
	h2 := build()

	queries := []struct {
		center core.Vec2
		radius float64
	}{
		{center: core.Vec2{X: 0, Y: 0}, radius: 5.0},
		{center: core.Vec2{X: 0, Y: 0}, radius: 10.0},
		{center: core.Vec2{X: 10, Y: -5}, radius: 1.0},
		{center: core.Vec2{X: 5, Y: 5}, radius: 8.0},
		{center: core.Vec2{X: -100, Y: -100}, radius: 50.0},
	}

	for qi, q := range queries {
		r1 := h1.NearbyEntities(q.center, q.radius)
		r2 := h2.NearbyEntities(q.center, q.radius)

		if len(r1) != len(r2) {
			t.Errorf("query %d: length mismatch %d vs %d", qi, len(r1), len(r2))
			continue
		}
		for i := range r1 {
			if r1[i].ID != r2[i].ID {
				t.Errorf("query %d, pos %d: id mismatch %s vs %s", qi, i, r1[i].ID, r2[i].ID)
			}
			if r1[i].Pos != r2[i].Pos {
				t.Errorf("query %d, pos %d: pos mismatch %v vs %v", qi, i, r1[i].Pos, r2[i].Pos)
			}
		}
	}
}

// ── Stress test: insert many, query many, verify sorting ──────────────────────

func TestNearbyEntities_SortedAcrossCells(t *testing.T) {
	h := New(2.0) // small cells

	// Place entities in multiple cells with IDs not in insertion order.
	pos := []core.Vec2{
		{X: 0, Y: 0},   // cell (0,0)
		{X: 3, Y: 0},   // cell (1,0) — crosses into bucket (1,0)
		{X: -3, Y: 0},  // cell (-2,0) — negative bucket
		{X: 0, Y: 3},   // cell (0,1)
		{X: 0, Y: -3},  // cell (0,-2)
		{X: 3, Y: 3},   // cell (1,1)
		{X: -3, Y: -3}, // cell (-2,-2)
	}
	ids := []core.ObjectID{"G", "D", "A", "F", "B", "E", "C"}

	for i := range ids {
		h.Insert(ids[i], pos[i])
	}

	// Query covering all seven points.
	got := h.NearbyEntities(core.Vec2{X: 0, Y: 0}, 6.0)
	if len(got) != 7 {
		t.Fatalf("expected 7 entities, got %d", len(got))
	}

	// IDs should be sorted.
	for i := 1; i < len(got); i++ {
		if got[i].ID < got[i-1].ID {
			t.Errorf("result not sorted at index %d: %s < %s", i, got[i].ID, got[i-1].ID)
		}
	}
}

// ── Helper functions ───────────────────────────────────────────────────────────

func entityIDs(entities []Entity) []core.ObjectID {
	ids := make([]core.ObjectID, len(entities))
	for i, e := range entities {
		ids[i] = e.ID
	}
	return ids
}

// ── Test for the floorDiv helper (not exported, so tested via its behavior) ───

func TestFloorDiv(t *testing.T) {
	tests := []struct {
		v, invScale float64
		want        int
	}{
		{v: 0.0, invScale: 0.125, want: 0},
		{v: 8.0, invScale: 0.125, want: 1},
		{v: 15.9, invScale: 0.125, want: 1},
		{v: 16.0, invScale: 0.125, want: 2},
		{v: -0.5, invScale: 0.125, want: -1},  // floor(-0.0625) = -1
		{v: -8.0, invScale: 0.125, want: -1},  // floor(-1.0) = -1
		{v: -8.1, invScale: 0.125, want: -2},  // floor(-1.0125) = -2
		{v: -16.0, invScale: 0.125, want: -2}, // floor(-2.0) = -2
		{v: 0.0, invScale: 0.25, want: 0},
		{v: -1.0, invScale: 0.25, want: -1},  // floor(-0.25) = -1
		{v: -2.0, invScale: 0.25, want: -1},  // floor(-0.5) = -1
		{v: -3.0, invScale: 0.25, want: -1},  // floor(-0.75) = -1
		{v: -4.0, invScale: 0.25, want: -1},  // floor(-1.0) = -1
		{v: -5.0, invScale: 0.25, want: -2},  // floor(-1.25) = -2
	}
	for _, tc := range tests {
		got := floorDiv(tc.v, tc.invScale)
		if got != tc.want {
			t.Errorf("floorDiv(%v, %v) = %d, want %d", tc.v, tc.invScale, got, tc.want)
		}
	}
}

// newSeededRand creates a deterministic PRNG for test data generation.
// Uses a linear congruential generator to avoid importing math/rand/v2 at package level.
func newSeededRand(seed int64) *seededRand {
	state := uint64(seed)
	// Advance the state a few times to mix it.
	for i := 0; i < 10; i++ {
		state = state*6364136223846793005 + 1442695040888963407
	}
	return &seededRand{state: state}
}

// seededRand is a minimal deterministic PRNG using a linear congruential generator.
// It is only used for test data generation, not for simulation logic.
type seededRand struct {
	state uint64
}

func (r *seededRand) step() {
	r.state = r.state*6364136223846793005 + 1442695040888963407
}

func (r *seededRand) Float64() float64 {
	r.step()
	// Use high 53 bits for uniform [0,1).
	return float64(r.state>>11) / float64(1<<53)
}

// Benchmark helpers — verify DistSq is faster than Distance (no sqrt).
var sink float64

func BenchmarkDistSq(b *testing.B) {
	a := core.Vec2{X: 0, Y: 0}
	o := core.Vec2{X: 100, Y: 200}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink = a.DistSq(o)
	}
	_ = sink
}

func BenchmarkDistance(b *testing.B) {
	a := core.Vec2{X: 0, Y: 0}
	o := core.Vec2{X: 100, Y: 200}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink = a.Distance(o)
	}
	_ = sink
}

// Ensure math is referenced (used by benchmark Distance which calls Distance which uses math.Sqrt).
var _ = math.Sqrt
