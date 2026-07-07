package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/dogring/bdg/platform/persist"
)

// ── GET /api/terrain (WI-P4) ───────────────────────────────────────────────────

// TestTerrain_ReturnsStoredBytesVerbatim verifies the handler forwards the
// sim:{run}:terrain value byte-for-byte (persist.TerrainView is written already
// shaped as the response — no reshaping in api).
func TestTerrain_ReturnsStoredBytesVerbatim(t *testing.T) {
	rds := newFakeRedisReader()
	stored, _ := json.Marshal(persist.TerrainView{
		CellSize:    2,
		Orientation: "flat",
		Size:        persist.TerrainSize{Cols: 2, Rows: 2},
		Terrain:     []string{"grass", "grass", "water", "forest"},
		Wear:        []float64{0, 0.5, 0, 0},
	})
	keyer := persist.Keyer{Run: "test-run"}
	rds.blobs[keyer.Terrain()] = stored

	s := testServer(false, rds)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/terrain", nil))

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != string(stored) {
		t.Fatalf("body not verbatim:\n got %s\nwant %s", got, stored)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}

	// The frontend contract check (frontend/SPEC.md TerrainGrid): terrain length
	// equals cols*rows and cell_size/orientation present.
	var doc struct {
		CellSize    float64 `json:"cell_size"`
		Orientation string  `json:"orientation"`
		Size        struct{ Cols, Rows int }
		Terrain     []string `json:"terrain"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	if doc.CellSize != 2 || doc.Orientation != "flat" || len(doc.Terrain) != doc.Size.Cols*doc.Size.Rows {
		t.Fatalf("shape mismatch: cell_size=%v orientation=%q len(terrain)=%d cols*rows=%d",
			doc.CellSize, doc.Orientation, len(doc.Terrain), doc.Size.Cols*doc.Size.Rows)
	}
}

// TestTerrain_AbsentKey404 verifies env-off neutrality: no sim:{run}:terrain key
// (env/navmap not installed) ⇒ 404 + {"error":"terrain not found"}.
func TestTerrain_AbsentKey404(t *testing.T) {
	rds := newFakeRedisReader() // no blobs, no snapshotBlob ⇒ Get returns nil

	s := testServer(false, rds)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/terrain", nil))

	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var doc map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || doc["error"] != "terrain not found" {
		t.Fatalf("body = %q, want {\"error\":\"terrain not found\"}", rec.Body.String())
	}
}

// TestTerrain_NotOnSSEServer verifies the SSE-only server (NewSSE) does NOT
// register /api/terrain — the read-only SSE deployment exposes only the probes
// and /sse.
func TestTerrain_NotOnSSEServer(t *testing.T) {
	rds := newFakeRedisReader()
	keyer := persist.Keyer{Run: "test-run"}
	rds.blobs[keyer.Terrain()] = []byte(`{"cell_size":1}`)

	s := NewSSE(Config{Addr: ":0", RunID: "test-run"}, rds)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/terrain", nil))

	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404 (route absent on SSE-only server)", rec.Code)
	}
}
