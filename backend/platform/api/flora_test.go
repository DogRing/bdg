package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/platform/persist"
)

// ── GET /api/flora (WI-P4) ─────────────────────────────────────────────────────

// TestFlora_ReturnsStoredBytesVerbatim verifies the handler forwards the
// sim:{run}:flora value byte-for-byte (persist.FloraDoc is written already
// shaped as the response — no reshaping in api) and that the blob carries the
// world_revision tag + full render rows the frontend baseline needs.
func TestFlora_ReturnsStoredBytesVerbatim(t *testing.T) {
	rds := newFakeRedisReader()
	stored, _ := json.Marshal(persist.FloraDoc{
		WorldRevision: 42,
		Flora: []persist.FloraView{
			{ID: "grass_1", Species: "grass", Pos: core.Vec2{X: 129, Y: 87}, Stage: 2, Width: 0.35},
		},
	})
	keyer := persist.Keyer{Run: "test-run"}
	rds.blobs[keyer.Flora()] = stored

	s := testServer(false, rds)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/flora", nil))

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != string(stored) {
		t.Fatalf("body not verbatim:\n got %s\nwant %s", got, stored)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}

	// The frontend baseline contract: world_revision (revision gate) + full render
	// rows {object_id, species, pos, stage, width}; no god-view field (length,
	// death_streak) leaks through.
	var doc struct {
		WorldRevision int64 `json:"world_revision"`
		Flora         []struct {
			ID      string  `json:"object_id"`
			Species string  `json:"species"`
			Stage   int     `json:"stage"`
			Width   float64 `json:"width"`
		} `json:"flora"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	if doc.WorldRevision != 42 || len(doc.Flora) != 1 ||
		doc.Flora[0].ID != "grass_1" || doc.Flora[0].Species != "grass" ||
		doc.Flora[0].Stage != 2 || doc.Flora[0].Width != 0.35 {
		t.Fatalf("shape mismatch: %+v", doc)
	}
}

// TestFlora_EmptyInstalledIsServable verifies an installed-but-no-plants flora
// baseline ({world_revision, flora:[]}) is a 200 the frontend applies as an
// authoritative-empty replacement — distinct from the 404 env-off case.
func TestFlora_EmptyInstalledIsServable(t *testing.T) {
	rds := newFakeRedisReader()
	stored, _ := json.Marshal(persist.FloraDoc{WorldRevision: 7, Flora: []persist.FloraView{}})
	keyer := persist.Keyer{Run: "test-run"}
	rds.blobs[keyer.Flora()] = stored

	s := testServer(false, rds)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/flora", nil))

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200 (installed-but-empty)", rec.Code)
	}
	if got := rec.Body.String(); got != string(stored) {
		t.Fatalf("body not verbatim:\n got %s\nwant %s", got, stored)
	}
}

// TestFlora_AbsentKey404 verifies env-off neutrality: no sim:{run}:flora key
// (flora not installed) ⇒ 404 + {"error":"flora not found"}.
func TestFlora_AbsentKey404(t *testing.T) {
	rds := newFakeRedisReader() // no blobs ⇒ Get returns nil

	s := testServer(false, rds)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/flora", nil))

	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var doc map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || doc["error"] != "flora not found" {
		t.Fatalf("body = %q, want {\"error\":\"flora not found\"}", rec.Body.String())
	}
}

// TestFlora_NotOnSSEServer verifies the SSE-only server (NewSSE) does NOT
// register /api/flora — the read-only SSE deployment exposes only the probes
// and /sse.
func TestFlora_NotOnSSEServer(t *testing.T) {
	rds := newFakeRedisReader()
	keyer := persist.Keyer{Run: "test-run"}
	rds.blobs[keyer.Flora()] = []byte(`{"world_revision":1,"flora":[]}`)

	s := NewSSE(Config{Addr: ":0", RunID: "test-run"}, rds)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/flora", nil))

	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404 (route absent on SSE-only server)", rec.Code)
	}
}
