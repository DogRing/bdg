package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/platform/persist"
)

// ── GET /api/agents/{id} tests ────────────────────────────────────────────────

func TestAgent_GodViewCombinations(t *testing.T) {
	tests := []struct {
		name          string
		godMode       bool
		hasGodParam   bool
		wantRealStats bool
	}{
		{name: "godMode=false, no god param", godMode: false, hasGodParam: false, wantRealStats: false},
		{name: "godMode=false, with god param", godMode: false, hasGodParam: true, wantRealStats: false},
		{name: "godMode=true, no god param", godMode: true, hasGodParam: false, wantRealStats: false},
		{name: "godMode=true, with god param", godMode: true, hasGodParam: true, wantRealStats: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rds := newFakeRedisReader()
			setupAgentHash(rds, "farmer_1", nil)

			realStats := map[string]any{"Strength": 0.8, "Agility": 0.4}
			snapBlob := buildSnapshotBlob(t, "farmer_1", realStats)
			rds.setSnapshot(snapBlob)

			live := &snapshotLiveStore{blob: snapBlob}
			cfg := Config{Addr: ":0", RunID: "test-run", GodMode: tt.godMode}
			s := New(cfg, live, rds, nil)

			path := "/api/agents/farmer_1"
			if tt.hasGodParam {
				path += "?god=true"
			}
			req := httptest.NewRequest("GET", path, nil)
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

			_, hasRS := result["real_stats"]
			if tt.wantRealStats && !hasRS {
				t.Error("expected real_stats, but it was absent")
			}
			if !tt.wantRealStats && hasRS {
				t.Error("expected NO real_stats, but it was present")
			}
		})
	}
}

func TestAgent_UnknownReturns404(t *testing.T) {
	rds := newFakeRedisReader()
	s := testServer(false, rds)

	req := httptest.NewRequest("GET", "/api/agents/unknown", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	if strings.TrimSpace(string(body)) != `{"error":"agent not found"}` {
		t.Errorf("body = %q, want %q", strings.TrimSpace(string(body)), `{"error":"agent not found"}`)
	}
}

func TestAgent_EmptyHashReturns404(t *testing.T) {
	rds := newFakeRedisReader()
	keyer := persist.Keyer{Run: "test-run"}
	rds.setAgentHash(keyer.Agent(core.AgentID("ghost")), map[string]string{})

	s := testServer(false, rds)
	req := httptest.NewRequest("GET", "/api/agents/ghost", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	if strings.TrimSpace(string(body)) != `{"error":"agent not found"}` {
		t.Errorf("body = %q, want %q", strings.TrimSpace(string(body)), `{"error":"agent not found"}`)
	}
}
