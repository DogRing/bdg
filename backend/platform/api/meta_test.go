package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/dogring/bdg/platform/persist"
)

// ── GET /api/meta ─────────────────────────────────────────────────────────────

func TestMetaRouteForwardsHash(t *testing.T) {
	rds := newFakeRedisReader()
	keyer := persist.Keyer{Run: "test-run"}
	rds.setAgentHash(keyer.Meta(), map[string]string{
		"tick":           "42",
		"schema_version": "1",
		"started_at":     "2026-07-11T00:00:00Z",
		"status":         "running",
		"world_revision": "7",
		"terrain":        "on",
	})
	s := testServer(false, rds)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/meta", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var doc map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}
	if doc["world_revision"] != "7" || doc["terrain"] != "on" || doc["status"] != "running" {
		t.Errorf("meta = %v, want world_revision=7 terrain=on status=running", doc)
	}
}

func TestMetaRoute404WhenAbsent(t *testing.T) {
	rds := newFakeRedisReader()
	s := testServer(false, rds)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/meta", nil))
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404 for an absent meta hash", rec.Code)
	}
}

// ── stream ID helpers ─────────────────────────────────────────────────────────

func TestStreamIDHelpers(t *testing.T) {
	for _, tc := range []struct {
		id    string
		valid bool
	}{
		{"0-0", true}, {"1718000000000-12", true},
		{"$", false}, {"", false}, {"12", false}, {"a-1", false}, {"1-2-3", false},
	} {
		if got := validStreamID(tc.id); got != tc.valid {
			t.Errorf("validStreamID(%q) = %t, want %t", tc.id, got, tc.valid)
		}
	}

	for _, tc := range []struct {
		a, b string
		less bool
	}{
		{"1-0", "2-0", true},
		{"2-0", "1-9", false},
		{"5-1", "5-2", true},  // same ms ⇒ seq decides
		{"5-2", "5-2", false}, // equal ⇒ not less
		{"9-0", "10-0", true}, // numeric, not lexicographic
		{"10-0", "9-99", false},
	} {
		if got := streamIDLess(tc.a, tc.b); got != tc.less {
			t.Errorf("streamIDLess(%q, %q) = %t, want %t", tc.a, tc.b, got, tc.less)
		}
	}
}
