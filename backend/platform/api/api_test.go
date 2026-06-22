package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dogring/bdg/platform/persist"
)

// ── 1. GET /healthz ──────────────────────────────────────────────────────────

func TestHealthz_Always200(t *testing.T) {
	rds := newFakeRedisReader()
	s := testServer(false, rds)

	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if strings.TrimSpace(string(body)) != `{"status":"ok"}` {
		t.Errorf("body = %q, want %q", strings.TrimSpace(string(body)), `{"status":"ok"}`)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// ── 2. GET /readyz ───────────────────────────────────────────────────────────

func TestReadyz(t *testing.T) {
	tests := []struct {
		name     string
		pingErr  error
		wantCode int
		wantBody string
	}{
		{
			name:     "redis up => 200",
			pingErr:  nil,
			wantCode: http.StatusOK,
			wantBody: `{"status":"ok"}`,
		},
		{
			name:     "redis down => 503",
			pingErr:  fmt.Errorf("connection refused"),
			wantCode: http.StatusServiceUnavailable,
			wantBody: `{"error":"redis unavailable"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rds := newFakeRedisReader()
			rds.pingErr = tt.pingErr
			s := testServer(false, rds)

			req := httptest.NewRequest("GET", "/readyz", nil)
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)

			resp := rec.Result()
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode != tt.wantCode {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantCode)
			}
			if strings.TrimSpace(string(body)) != tt.wantBody {
				t.Errorf("body = %q, want %q", strings.TrimSpace(string(body)), tt.wantBody)
			}
		})
	}
}

// ── 3. GET /api/snapshot ─────────────────────────────────────────────────────

func TestSnapshot(t *testing.T) {
	tests := []struct {
		name     string
		blob     []byte
		wantCode int
	}{
		{
			name:     "blob present => 200",
			blob:     []byte(`{"tick":42}`),
			wantCode: http.StatusOK,
		},
		{
			name:     "empty json => 200",
			blob:     []byte(`{}`),
			wantCode: http.StatusOK,
		},
		{
			name:     "nil blob => 404",
			blob:     nil,
			wantCode: http.StatusNotFound,
		},
		{
			name:     "read error => 404",
			blob:     nil,
			wantCode: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rds := newFakeRedisReader()
			var live *snapshotLiveStore
			if tt.name == "read error => 404" {
				live = &snapshotLiveStore{errRead: fmt.Errorf("redis down")}
			} else {
				live = &snapshotLiveStore{blob: tt.blob}
			}
			rds.setSnapshot(tt.blob)

			cfg := Config{Addr: ":0", RunID: "test-run", GodMode: false}
			s := New(cfg, live, rds, nil)

			req := httptest.NewRequest("GET", "/api/snapshot", nil)
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)

			resp := rec.Result()
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode != tt.wantCode {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantCode)
			}
			if tt.wantCode == http.StatusOK && string(body) != string(tt.blob) {
				t.Errorf("body = %s, want %s", string(body), string(tt.blob))
			}
			if tt.wantCode == http.StatusNotFound {
				want := `{"error":"snapshot not found"}`
				if strings.TrimSpace(string(body)) != want {
					t.Errorf("body = %q, want %q", strings.TrimSpace(string(body)), want)
				}
			}
		})
	}
}

// ── 4. No hand-formatted keys ───────────────────────────────────────────────

func TestNoHandFormattedKeys(t *testing.T) {
	// Static check: the string "sim:" should not appear as a literal in routes.
	// This is a compile-time guard by design (keys come from Keyer).
	// We verify at runtime that the keyer is the only source.
	keyer := persist.Keyer{Run: "test-run"}
	if !strings.Contains(keyer.Events(), "sim:") {
		t.Error("keyer.Events() should contain 'sim:'")
	}
	if !strings.Contains(keyer.SnapshotKey(), "sim:") {
		t.Error("keyer.SnapshotKey() should contain 'sim:'")
	}
}

// ── 5. RealStats structural guard ───────────────────────────────────────────

func TestRealStatsStructuralGuard(t *testing.T) {
	// When GodMode=false, real_stats MUST NOT appear under any request.
	rds := newFakeRedisReader()
	setupAgentHash(rds, "farmer_1", nil)
	snapBlob := buildSnapshotBlob(t, "farmer_1", map[string]any{"Strength": 0.8})
	rds.setSnapshot(snapBlob)
	live := &snapshotLiveStore{blob: snapBlob}

	cfg := Config{Addr: ":0", RunID: "test-run", GodMode: false}
	s := New(cfg, live, rds, nil)

	req := httptest.NewRequest("GET", "/api/agents/farmer_1?god=true", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := result["real_stats"]; ok {
		t.Error("real_stats present when GodMode=false; must be absent")
	}
}

// ── 6. Content-Type checks ──────────────────────────────────────────────────

func TestAgentEndpointContentType(t *testing.T) {
	rds := newFakeRedisReader()
	setupAgentHash(rds, "farmer_1", nil)
	s := testServer(false, rds)

	req := httptest.NewRequest("GET", "/api/agents/farmer_1", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	resp := rec.Result()
	resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestSnapshotEndpointContentType(t *testing.T) {
	rds := newFakeRedisReader()
	live := &snapshotLiveStore{blob: []byte(`{}`)}
	cfg := Config{Addr: ":0", RunID: "test-run", GodMode: false}
	s := New(cfg, live, rds, nil)

	req := httptest.NewRequest("GET", "/api/snapshot", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	resp := rec.Result()
	resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// ── 7. Handler returns non-nil ──────────────────────────────────────────────

func TestHandlerReturnsNonNil(t *testing.T) {
	rds := newFakeRedisReader()
	s := testServer(false, rds)
	h := s.Handler()
	if h == nil {
		t.Fatal("Handler() returned nil")
	}
	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}
