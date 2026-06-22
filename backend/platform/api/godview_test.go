package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/core"
)

// ── Fake GodViewStore ────────────────────────────────────────────────────────────

type fakeGodViewStore struct {
	events map[string][]core.Event // key: "agentID:tick"
}

func (f *fakeGodViewStore) QueryEvents(_ context.Context, run core.RunID, agentID core.AgentID, tick core.Tick) ([]core.Event, error) {
	key := fmt.Sprintf("%s:%s:%d", run, agentID, tick)
	evs, ok := f.events[key]
	if !ok {
		return nil, nil
	}
	return evs, nil
}

// ── Gate table (cross-cutting, per SPEC) ───────────────────────────────────────

func TestGodViewGate_Table(t *testing.T) {
	routes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/god/agent/farmer_1/divergence"},
		{"GET", "/api/god/reputation/farmer_1"},
		{"GET", "/api/god/relations"},
		{"GET", "/api/god/why/farmer_1/42"},
	}

	tests := []struct {
		name     string
		godMode  bool
		hasGod   bool
		want403  bool // true => expect 403; false => expect NOT 403 (gate passed)
		wantBody string
	}{
		{
			name:    "godMode=false, god=true => 403",
			godMode: false,
			hasGod:  true,
			want403: true,
		},
		{
			name:    "godMode=true, god=false => 403",
			godMode: true,
			hasGod:  false,
			want403: true,
		},
		{
			name:    "godMode=true, god=true => proceeds",
			godMode: true,
			hasGod:  true,
			want403: false,
		},
	}

	for _, rt := range routes {
		t.Run(rt.path, func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					rds := newFakeRedisReader()
					live := &snapshotLiveStore{blob: []byte(`{"tick":42,"world":{"Agents":[],"tom_digest":{}}}`)}
					cfg := Config{Addr: ":0", RunID: "test-run", GodMode: tt.godMode}
					gv := &fakeGodViewStore{events: make(map[string][]core.Event)}
					s := New(cfg, live, rds, gv)

					path := rt.path
					if tt.hasGod {
						path += "?god=true"
					}
					req := httptest.NewRequest(rt.method, path, nil)
					rec := httptest.NewRecorder()
					s.Handler().ServeHTTP(rec, req)

					resp := rec.Result()
					body, _ := io.ReadAll(resp.Body)
					resp.Body.Close()

					if tt.want403 {
						if resp.StatusCode != http.StatusForbidden {
							t.Errorf("status = %d, want 403; body=%s", resp.StatusCode, string(body))
						}
						if !strings.Contains(string(body), "god mode disabled") {
							t.Errorf("body = %q, want contains 'god mode disabled'", string(body))
						}
					} else {
						// "proceeds" — must NOT be 403; actual status depends on store
						if resp.StatusCode == http.StatusForbidden {
							t.Errorf("status = %d, gate should have passed; body=%s", resp.StatusCode, string(body))
						}
					}
				})
			}
		})
	}
}

func TestGodViewGate_GodModeFalseNoStoreCall(t *testing.T) {
	// Assert that when GodMode=false, the gate responds 403 before reading any store.
	rds := newFakeRedisReader()
	rds.setSnapshot([]byte(`{"tick":42}`))
	live := &snapshotLiveStore{blob: []byte(`{"tick":42}`)}
	cfg := Config{Addr: ":0", RunID: "test-run", GodMode: false}
	s := New(cfg, live, rds, nil)

	req := httptest.NewRequest("GET", "/api/god/agent/farmer_1/divergence?god=true", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
	if strings.TrimSpace(string(body)) != `{"error":"god mode disabled"}` {
		t.Errorf("body = %q, want %q", strings.TrimSpace(string(body)), `{"error":"god mode disabled"}`)
	}
}

// ── Divergence endpoint tests ────────────────────────────────────────────────────

func TestGodDivergence_PartialFallback(t *testing.T) {
	// 206 Partial when TomDigest is absent.
	rds := newFakeRedisReader()
	snapBlob := buildSnapshotBlob(t, "farmer_1", map[string]any{"Strength": 0.8})
	rds.setSnapshot(snapBlob)
	live := &snapshotLiveStore{blob: snapBlob}
	cfg := Config{Addr: ":0", RunID: "test-run", GodMode: true}
	s := New(cfg, live, rds, nil)

	req := httptest.NewRequest("GET", "/api/god/agent/farmer_1/divergence?god=true", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		t.Errorf("status = %d, want 206; body=%s", resp.StatusCode, string(body))
	}

	var partial PartialResponse
	if err := json.Unmarshal(body, &partial); err != nil {
		t.Fatalf("unmarshal partial: %v", err)
	}
	if !partial.Partial {
		t.Error("partial.Partial = false, want true")
	}
	if !strings.Contains(partial.Reason, "tom_digest") {
		t.Errorf("partial.Reason = %q, want contains 'tom_digest'", partial.Reason)
	}
}

func TestGodDivergence_MissingSnapshot(t *testing.T) {
	// 404 when snapshot is absent.
	rds := newFakeRedisReader()
	live := &snapshotLiveStore{errRead: fmt.Errorf("no snapshot")}
	cfg := Config{Addr: ":0", RunID: "test-run", GodMode: true}
	s := New(cfg, live, rds, nil)

	req := httptest.NewRequest("GET", "/api/god/agent/farmer_1/divergence?god=true", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	resp := rec.Result()
	resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestGodDivergence_UnknownAgent(t *testing.T) {
	// 404 when agent ID not in snapshot.
	rds := newFakeRedisReader()
	snapBlob := buildSnapshotBlob(t, "farmer_1", map[string]any{"Strength": 0.8})
	rds.setSnapshot(snapBlob)
	live := &snapshotLiveStore{blob: snapBlob}
	cfg := Config{Addr: ":0", RunID: "test-run", GodMode: true}
	s := New(cfg, live, rds, nil)

	req := httptest.NewRequest("GET", "/api/god/agent/unknown/divergence?god=true", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body=%s", resp.StatusCode, string(body))
	}
}

func TestGodDivergence_D8ChannelsNotBlended(t *testing.T) {
	// Fixture where RealStats[Strength]=0.72, SelfEstStats[Strength].Mean=0.55,
	// and cross-agent mean=0.60 — verify self_estimate is NOT the Real value.
	rds := newFakeRedisReader()

	// Build snapshot with tom_digest containing two observers with mean=0.60 each.
	td := map[string]any{
		"farmer_2": map[string]any{
			"farmer_1": map[string]any{
				"est_stats": map[string]any{
					"Strength": map[string]any{
						"mean": 0.60,
					},
				},
				"affinity": 0.0,
				"trust":    0.5,
				"rely_on":  map[string]any{},
			},
		},
		"farmer_3": map[string]any{
			"farmer_1": map[string]any{
				"est_stats": map[string]any{
					"Strength": map[string]any{
						"mean": 0.60,
					},
				},
				"affinity": 0.0,
				"trust":    0.5,
				"rely_on":  map[string]any{},
			},
		},
	}

	snapBlob := buildSnapshotWithTomDigest(t, "farmer_1", map[string]any{"Strength": 0.72}, map[string]any{"Strength": map[string]any{"mean": 0.55, "variance": 0.0}}, td)
	rds.setSnapshot(snapBlob)
	live := &snapshotLiveStore{blob: snapBlob}
	cfg := Config{Addr: ":0", RunID: "test-run", GodMode: true}
	s := New(cfg, live, rds, nil)

	req := httptest.NewRequest("GET", "/api/god/agent/farmer_1/divergence?god=true", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(body))
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	perStat, ok := result["per_stat"].(map[string]any)
	if !ok {
		t.Fatal("per_stat not present or not a map")
	}
	str, ok := perStat["Strength"].(map[string]any)
	if !ok {
		t.Fatal("Strength not in per_stat")
	}
	realVal := str["real"].(float64)
	selfEst := str["self_estimate"].(float64)
	others := str["others_estimate_mean"].(float64)

	if realVal != 0.72 {
		t.Errorf("real = %f, want 0.72", realVal)
	}
	if selfEst != 0.55 {
		t.Errorf("self_estimate = %f, want 0.55 (NOT 0.72 — D8)", selfEst)
	}
	if others != 0.60 {
		t.Errorf("others_estimate_mean = %f, want 0.60", others)
	}

	// Verify all three keys are present.
	for _, k := range []string{"real", "self_estimate", "others_estimate_mean"} {
		if _, ok := str[k]; !ok {
			t.Errorf("missing key %q in StatTriple", k)
		}
	}
}

// ── Reputation endpoint tests ────────────────────────────────────────────────────

func TestGodReputation_PartialFallback(t *testing.T) {
	// 206 when TomDigest absent.
	rds := newFakeRedisReader()
	snapBlob := buildSnapshotBlob(t, "farmer_1", map[string]any{"Strength": 0.8})
	rds.setSnapshot(snapBlob)
	live := &snapshotLiveStore{blob: snapBlob}
	cfg := Config{Addr: ":0", RunID: "test-run", GodMode: true}
	s := New(cfg, live, rds, nil)

	req := httptest.NewRequest("GET", "/api/god/reputation/farmer_1?god=true", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		t.Errorf("status = %d, want 206; body=%s", resp.StatusCode, string(body))
	}
}

func TestGodReputation_D6ShapeNoScalar(t *testing.T) {
	// D6: no top-level scalar, only {mean, variance, per_faction} per stat.
	rds := newFakeRedisReader()

	td := map[string]any{
		"farmer_2": map[string]any{
			"farmer_1": map[string]any{
				"est_stats": map[string]any{
					"Strength": map[string]any{"mean": 0.80},
				},
				"affinity": 0.0,
				"trust":    0.5,
				"rely_on":  map[string]any{},
			},
		},
		"farmer_3": map[string]any{
			"farmer_1": map[string]any{
				"est_stats": map[string]any{
					"Strength": map[string]any{"mean": 0.30},
				},
				"affinity": 0.0,
				"trust":    0.5,
				"rely_on":  map[string]any{},
			},
		},
	}

	snapBlob := buildSnapshotWithTomDigest(t, "farmer_1", map[string]any{"Strength": 0.8}, nil, td)
	rds.setSnapshot(snapBlob)
	live := &snapshotLiveStore{blob: snapBlob}
	cfg := Config{Addr: ":0", RunID: "test-run", GodMode: true}
	s := New(cfg, live, rds, nil)

	req := httptest.NewRequest("GET", "/api/god/reputation/farmer_1?god=true", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(body))
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Assert no top-level scalar "reputation" or "mean" key.
	for _, badKey := range []string{"reputation", "mean", "variance"} {
		if _, ok := result[badKey]; ok {
			t.Errorf("found top-level key %q — D6 forbids scalars", badKey)
		}
	}

	// Must have per_stat.
	perStat, ok := result["per_stat"].(map[string]any)
	if !ok {
		t.Fatal("per_stat not present or not a map (D6)")
	}
	for k, v := range perStat {
		sr, ok := v.(map[string]any)
		if !ok {
			t.Errorf("per_stat[%s] is not a map", k)
			continue
		}
		if _, ok := sr["mean"]; !ok {
			t.Errorf("per_stat[%s] missing mean (D6)", k)
		}
		if _, ok := sr["variance"]; !ok {
			t.Errorf("per_stat[%s] missing variance (D6)", k)
		}
		if _, ok := sr["per_faction"]; !ok {
			t.Errorf("per_stat[%s] missing per_faction (D6)", k)
		}
		if _, ok := sr["reputation"]; ok {
			t.Errorf("per_stat[%s] has scalar 'reputation' key — D6", k)
		}
	}
}

func TestGodReputation_FactionDerivedD2(t *testing.T) {
	rds := newFakeRedisReader()

	td := map[string]any{
		"farmer_2": map[string]any{
			"farmer_1": map[string]any{
				"est_stats": map[string]any{
					"Strength": map[string]any{"mean": 0.80},
				},
				"affinity": 0.0,
				"trust":    0.5,
				"rely_on":  map[string]any{},
			},
		},
		"farmer_3": map[string]any{
			"farmer_1": map[string]any{
				"est_stats": map[string]any{
					"Strength": map[string]any{"mean": 0.30},
				},
				"affinity": 0.0,
				"trust":    0.5,
				"rely_on":  map[string]any{},
			},
		},
	}

	snapBlob := buildSnapshotWithTomDigest(t, "farmer_1", map[string]any{"Strength": 0.8}, nil, td)
	rds.setSnapshot(snapBlob)
	live := &snapshotLiveStore{blob: snapBlob}
	cfg := Config{Addr: ":0", RunID: "test-run", GodMode: true}
	s := New(cfg, live, rds, nil)

	req := httptest.NewRequest("GET", "/api/god/reputation/farmer_1?god=true", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	perStat := result["per_stat"].(map[string]any)
	for _, v := range perStat {
		sr := v.(map[string]any)
		pf := sr["per_faction"].([]any)
		for _, f := range pf {
			fr := f.(map[string]any)
			fid := fr["faction_id"].(string)
			if !strings.HasPrefix(fid, "cluster_") {
				t.Errorf("faction_id = %q, want 'cluster_<holder>' (D2)", fid)
			}
			if strings.Contains(fid, "Faction") || strings.Contains(fid, "Role") {
				t.Errorf("faction_id = %q contains forbidden type name (D2)", fid)
			}
		}
	}
}

// ── Relations endpoint tests ─────────────────────────────────────────────────────

func TestGodRelations_PartialFallback(t *testing.T) {
	rds := newFakeRedisReader()
	snapBlob := buildSnapshotBlob(t, "farmer_1", map[string]any{"Strength": 0.8})
	rds.setSnapshot(snapBlob)
	live := &snapshotLiveStore{blob: snapBlob}
	cfg := Config{Addr: ":0", RunID: "test-run", GodMode: true}
	s := New(cfg, live, rds, nil)

	req := httptest.NewRequest("GET", "/api/god/relations?god=true", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		t.Errorf("status = %d, want 206; body=%s", resp.StatusCode, string(body))
	}
}

func TestGodRelations_EdgeFilter(t *testing.T) {
	// Build a tom_digest with a mix of non-zero pairs and zero-only pairs.
	td := map[string]any{
		"farmer_1": map[string]any{
			"farmer_2": map[string]any{
				"affinity": 0.5,
				"trust":    0.3,
				"rely_on":  map[string]any{},
			},
			"farmer_3": map[string]any{
				"affinity": 0.0,
				"trust":    0.0,
				"rely_on":  map[string]any{},
			},
		},
		"farmer_2": map[string]any{
			"farmer_1": map[string]any{
				"affinity": 0.0,
				"trust":    0.0,
				"rely_on":  map[string]any{},
			},
		},
		"farmer_3": map[string]any{
			"farmer_1": map[string]any{
				"affinity": 0.75,
				"trust":    0.0,
				"rely_on": map[string]any{
					"Safety": 0.3,
				},
			},
		},
	}

	snapJSON := fmt.Sprintf(`{"tick":42,"world":{"Tick":42,"RNGState":{"Data":"dGVzdA=="},"Agents":[{"id":"farmer_1","pos":{"x":10,"y":20},"real_stats":{"Strength":0.8}},{"id":"farmer_2","pos":{"x":10,"y":20},"real_stats":{"Strength":0.5}},{"id":"farmer_3","pos":{"x":10,"y":20},"real_stats":{"Strength":0.5}}],"tom_digest":%s}}`, toJSON(t, td))

	rds := newFakeRedisReader()
	rds.setSnapshot([]byte(snapJSON))
	live := &snapshotLiveStore{blob: []byte(snapJSON)}
	cfg := Config{Addr: ":0", RunID: "test-run", GodMode: true}
	s := New(cfg, live, rds, nil)

	req := httptest.NewRequest("GET", "/api/god/relations?god=true", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(body))
	}

	var result RelationsResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Expected edges (non-zero):
	// - farmer_1 -> farmer_2 (affinity=0.5)
	// - farmer_3 -> farmer_1 (affinity=0.75, rely_on={Safety:0.3})
	if len(result.Edges) != 2 {
		t.Fatalf("got %d edges, want 2", len(result.Edges))
	}

	// Edges sorted by (from, to).
	if result.Edges[0].From != "farmer_1" || result.Edges[0].To != "farmer_2" {
		t.Errorf("edge[0] = %s->%s, want farmer_1->farmer_2", result.Edges[0].From, result.Edges[0].To)
	}
	if result.Edges[1].From != "farmer_3" || result.Edges[1].To != "farmer_1" {
		t.Errorf("edge[1] = %s->%s, want farmer_3->farmer_1", result.Edges[1].From, result.Edges[1].To)
	}

	// Check edge[0] affinity/trust.
	if result.Edges[0].Affinity != 0.5 {
		t.Errorf("edge[0].affinity = %f, want 0.5", result.Edges[0].Affinity)
	}
	if result.Edges[0].Trust != 0.3 {
		t.Errorf("edge[0].trust = %f, want 0.3", result.Edges[0].Trust)
	}

	// Check edge[1] rely_on content.
	if v, ok := result.Edges[1].RelyOn["Safety"]; !ok || v != 0.3 {
		t.Errorf("edge[1].rely_on[Safety] = %v, want 0.3", result.Edges[1].RelyOn["Safety"])
	}
}

func TestGodRelations_DeterministicSort(t *testing.T) {
	// Build a tom_digest and marshal twice — results must be byte-identical.
	td := map[string]any{
		"farmer_2": map[string]any{
			"farmer_1": map[string]any{
				"affinity": 0.5,
				"trust":    0.0,
				"rely_on":  map[string]any{},
			},
		},
		"farmer_1": map[string]any{
			"farmer_2": map[string]any{
				"affinity": 0.3,
				"trust":    0.7,
				"rely_on":  map[string]any{},
			},
		},
	}

	snapJSON := fmt.Sprintf(`{"tick":42,"world":{"Tick":42,"RNGState":{"Data":"dGVzdA=="},"Agents":[{"id":"farmer_1","pos":{"x":10,"y":20},"real_stats":{"Strength":0.8}},{"id":"farmer_2","pos":{"x":10,"y":20},"real_stats":{"Strength":0.5}}],"tom_digest":%s}}`, toJSON(t, td))

	rds := newFakeRedisReader()
	rds.setSnapshot([]byte(snapJSON))
	live := &snapshotLiveStore{blob: []byte(snapJSON)}
	cfg := Config{Addr: ":0", RunID: "test-run", GodMode: true}
	s := New(cfg, live, rds, nil)

	req := httptest.NewRequest("GET", "/api/god/relations?god=true", nil)
	rec1 := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec1, req)

	req2 := httptest.NewRequest("GET", "/api/god/relations?god=true", nil)
	rec2 := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec2, req2)

	body1, _ := io.ReadAll(rec1.Result().Body)
	rec1.Result().Body.Close()
	body2, _ := io.ReadAll(rec2.Result().Body)
	rec2.Result().Body.Close()

	if string(body1) != string(body2) {
		t.Error("two marshals of the same snapshot produced different JSON — D12 violation")
	}

	// Verify sorted: farmer_1->farmer_2 before farmer_2->farmer_1.
	var result RelationsResponse
	if err := json.Unmarshal(body1, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Edges) >= 2 {
		e0 := string(result.Edges[0].From) + ":" + string(result.Edges[0].To)
		e1 := string(result.Edges[1].From) + ":" + string(result.Edges[1].To)
		if e0 > e1 {
			t.Errorf("edges not sorted: %s before %s", e0, e1)
		}
	}
}

func TestGodRelations_RelyOnContent(t *testing.T) {
	// Edge with rely_on containing {safety:0.3, judgment:0.1} serialized sorted.
	td := map[string]any{
		"farmer_1": map[string]any{
			"farmer_2": map[string]any{
				"affinity": 0.0,
				"trust":    0.0,
				"rely_on": map[string]any{
					"judgment": 0.1,
					"safety":   0.3,
				},
			},
		},
	}

	snapJSON := fmt.Sprintf(`{"tick":42,"world":{"Tick":42,"RNGState":{"Data":"dGVzdA=="},"Agents":[{"id":"farmer_1","pos":{"x":10,"y":20},"real_stats":{"Strength":0.8}},{"id":"farmer_2","pos":{"x":10,"y":20},"real_stats":{"Strength":0.5}}],"tom_digest":%s}}`, toJSON(t, td))

	rds := newFakeRedisReader()
	rds.setSnapshot([]byte(snapJSON))
	live := &snapshotLiveStore{blob: []byte(snapJSON)}
	cfg := Config{Addr: ":0", RunID: "test-run", GodMode: true}
	s := New(cfg, live, rds, nil)

	req := httptest.NewRequest("GET", "/api/god/relations?god=true", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(body), `"judgment":0.1`) {
		t.Error("response should contain judgment key (sorted keys)")
	}
	if !strings.Contains(string(body), `"safety":0.3`) {
		t.Error("response should contain safety key (sorted keys)")
	}
}

// ── Why endpoint tests ──────────────────────────────────────────────────────────

func TestGodWhy_NilStore(t *testing.T) {
	// gv == nil -> 503
	rds := newFakeRedisReader()
	live := &snapshotLiveStore{blob: []byte(`{}`)}
	cfg := Config{Addr: ":0", RunID: "test-run", GodMode: true}
	s := New(cfg, live, rds, nil)

	req := httptest.NewRequest("GET", "/api/god/why/farmer_1/42?god=true", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body=%s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), "why-trace store unavailable") {
		t.Errorf("body = %q, want 'why-trace store unavailable'", string(body))
	}
}

func TestGodWhy_InvalidTick(t *testing.T) {
	rds := newFakeRedisReader()
	live := &snapshotLiveStore{blob: []byte(`{}`)}
	gv := &fakeGodViewStore{events: make(map[string][]core.Event)}
	cfg := Config{Addr: ":0", RunID: "test-run", GodMode: true}
	s := New(cfg, live, rds, gv)

	req := httptest.NewRequest("GET", "/api/god/why/farmer_1/notatick?god=true", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body=%s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), "invalid tick") {
		t.Errorf("body = %q, want 'invalid tick'", string(body))
	}
}

func TestGodWhy_NoTrace(t *testing.T) {
	rds := newFakeRedisReader()
	live := &snapshotLiveStore{blob: []byte(`{}`)}
	gv := &fakeGodViewStore{events: make(map[string][]core.Event)}
	cfg := Config{Addr: ":0", RunID: "test-run", GodMode: true}
	s := New(cfg, live, rds, gv)

	req := httptest.NewRequest("GET", "/api/god/why/farmer_1/42?god=true", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body=%s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), "no why-trace") {
		t.Errorf("body = %q, want 'no why-trace for agent at tick'", string(body))
	}
}

func TestGodWhy_GoalSelectedAndPlanBuilt(t *testing.T) {
	rds := newFakeRedisReader()
	live := &snapshotLiveStore{blob: []byte(`{}`)}
	gv := &fakeGodViewStore{
		events: map[string][]core.Event{
			"test-run:farmer_1:42": {
				{
					SchemaVersion: 1,
					Tick:          42,
					Seq:           0,
					AgentID:       "farmer_1",
					Type:          "GoalSelected",
					Payload: map[string]any{
						"dimension": "Satiety",
						"target":    "berry_bush_1",
						"eff_value": 0.85,
					},
				},
				{
					SchemaVersion: 1,
					Tick:          42,
					Seq:           1,
					AgentID:       "farmer_1",
					Type:          "PlanBuilt",
					Payload: map[string]any{
						"steps":       []any{"MoveTo(berry_bush_1)", "Gather(berry_bush_1)"},
						"total_cost":  15.0,
						"provisioned": []any{"berry_bush_1"},
					},
				},
			},
		},
	}
	cfg := Config{Addr: ":0", RunID: "test-run", GodMode: true}
	s := New(cfg, live, rds, gv)

	req := httptest.NewRequest("GET", "/api/god/why/farmer_1/42?god=true", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(body))
	}

	var result WhyResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if result.GoalSelected == nil {
		t.Fatal("goal_selected is nil")
	}
	if result.GoalSelected.Dimension != "Satiety" {
		t.Errorf("goal_selected.dimension = %q, want Satiety", result.GoalSelected.Dimension)
	}
	if result.GoalSelected.Target == nil || string(*result.GoalSelected.Target) != "berry_bush_1" {
		t.Errorf("goal_selected.target = %v, want berry_bush_1", result.GoalSelected.Target)
	}
	if result.GoalSelected.EffValue != 0.85 {
		t.Errorf("goal_selected.eff_value = %f, want 0.85", result.GoalSelected.EffValue)
	}

	if result.PlanBuilt == nil {
		t.Fatal("plan_built is nil")
	}
	if len(result.PlanBuilt.Steps) != 2 {
		t.Errorf("plan_built.steps = %v, want 2 steps", result.PlanBuilt.Steps)
	}
	if result.PlanBuilt.TotalCost != 15.0 {
		t.Errorf("plan_built.total_cost = %f, want 15.0", result.PlanBuilt.TotalCost)
	}
}

func TestGodWhy_CompetingCandidatesPresent(t *testing.T) {
	rds := newFakeRedisReader()
	live := &snapshotLiveStore{blob: []byte(`{}`)}
	gv := &fakeGodViewStore{
		events: map[string][]core.Event{
			"test-run:farmer_1:42": {
				{
					SchemaVersion: 1,
					Tick:          42,
					Seq:           0,
					AgentID:       "farmer_1",
					Type:          "GoalSelected",
					Payload: map[string]any{
						"dimension": "Satiety",
						"target":    "berry_bush_1",
						"eff_value": 0.85,
						"competing_candidates": []any{
							map[string]any{
								"dimension": "Hydration",
								"target":    "water_source_1",
								"eff_value": 0.65,
							},
							map[string]any{
								"dimension": "Rest",
								"target":    nil,
								"eff_value": 0.40,
							},
						},
					},
				},
			},
		},
	}
	cfg := Config{Addr: ":0", RunID: "test-run", GodMode: true}
	s := New(cfg, live, rds, gv)

	req := httptest.NewRequest("GET", "/api/god/why/farmer_1/42?god=true", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(body))
	}

	var result WhyResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if result.GoalSelected == nil {
		t.Fatal("goal_selected is nil")
	}
	if len(result.GoalSelected.CompetingCandidates) != 2 {
		t.Fatalf("competing_candidates = %+v, want 2 candidates", result.GoalSelected.CompetingCandidates)
	}

	cand0 := result.GoalSelected.CompetingCandidates[0]
	if cand0.Dimension != "Hydration" {
		t.Errorf("candidate[0].dimension = %q, want Hydration", cand0.Dimension)
	}
	if cand0.Target == nil || string(*cand0.Target) != "water_source_1" {
		t.Errorf("candidate[0].target = %v, want water_source_1", cand0.Target)
	}
	if cand0.EffValue != 0.65 {
		t.Errorf("candidate[0].eff_value = %f, want 0.65", cand0.EffValue)
	}

	cand1 := result.GoalSelected.CompetingCandidates[1]
	if cand1.Dimension != "Rest" {
		t.Errorf("candidate[1].dimension = %q, want Rest", cand1.Dimension)
	}
	if cand1.Target != nil {
		t.Errorf("candidate[1].target = %v, want nil (D1 nullable)", *cand1.Target)
	}
	if cand1.EffValue != 0.40 {
		t.Errorf("candidate[1].eff_value = %f, want 0.40", cand1.EffValue)
	}
}

func TestGodWhy_OldRowDegradation(t *testing.T) {
	// A GoalSelected row WITHOUT competing_candidates (pre-bump schema) decodes
	// with an empty competing_candidates slice and still returns 200.
	rds := newFakeRedisReader()
	live := &snapshotLiveStore{blob: []byte(`{}`)}
	gv := &fakeGodViewStore{
		events: map[string][]core.Event{
			"test-run:farmer_1:42": {
				{
					SchemaVersion: 1,
					Tick:          42,
					Seq:           0,
					AgentID:       "farmer_1",
					Type:          "GoalSelected",
					Payload: map[string]any{
						"dimension": "Satiety",
						"eff_value": 0.85,
						// no target field
						// no competing_candidates field
					},
				},
			},
		},
	}
	cfg := Config{Addr: ":0", RunID: "test-run", GodMode: true}
	s := New(cfg, live, rds, gv)

	req := httptest.NewRequest("GET", "/api/god/why/farmer_1/42?god=true", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(body))
	}

	var result WhyResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if result.GoalSelected == nil {
		t.Fatal("goal_selected is nil")
	}
	// competing_candidates should be an empty slice (not nil).
	if result.GoalSelected.CompetingCandidates == nil {
		t.Error("competing_candidates is nil, want empty slice")
	}
	if len(result.GoalSelected.CompetingCandidates) != 0 {
		t.Errorf("competing_candidates = %+v, want empty", result.GoalSelected.CompetingCandidates)
	}
	// Target should be nil (D1 nullable).
	if result.GoalSelected.Target != nil {
		t.Errorf("target = %v, want nil (absent from old row)", *result.GoalSelected.Target)
	}
}

// ── Divergence calculation test (user requirement) ───────────────────────────────

func TestGodDivergence_CalculationWithTomDigest(t *testing.T) {
	// Create a snapshot with known real/self/others values and verify 3-way divergence.
	// real=0.72, self_estimate=0.55, others_estimate_mean=0.60
	td := map[string]any{
		"farmer_2": map[string]any{
			"farmer_1": map[string]any{
				"est_stats": map[string]any{
					"Strength": map[string]any{
						"mean": 0.60,
					},
				},
				"affinity": 0.0,
				"trust":    0.5,
				"rely_on":  map[string]any{},
			},
		},
		"farmer_3": map[string]any{
			"farmer_1": map[string]any{
				"est_stats": map[string]any{
					"Strength": map[string]any{
						"mean": 0.60,
					},
				},
				"affinity": 0.0,
				"trust":    0.5,
				"rely_on":  map[string]any{},
			},
		},
	}

	snapJSON := fmt.Sprintf(`{"schema_version":1,"run_id":"test-run","tick":42,"world":{"Tick":42,"RNGState":{"Data":"dGVzdA=="},"Agents":[{"id":"farmer_1","pos":{"x":10,"y":20},"real_stats":{"Strength":0.72},"self_est_stats":{"Strength":{"mean":0.55,"variance":0.0}}},{"id":"farmer_2","pos":{"x":10,"y":20},"real_stats":{"Strength":0.5}},{"id":"farmer_3","pos":{"x":10,"y":20},"real_stats":{"Strength":0.5}}],"tom_digest":%s}}`, toJSON(t, td))

	rds := newFakeRedisReader()
	rds.setSnapshot([]byte(snapJSON))
	live := &snapshotLiveStore{blob: []byte(snapJSON)}
	cfg := Config{Addr: ":0", RunID: "test-run", GodMode: true}
	s := New(cfg, live, rds, nil)

	req := httptest.NewRequest("GET", "/api/god/agent/farmer_1/divergence?god=true", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	resp := rec.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, string(body))
	}

	var result DivergenceResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if result.AgentID != "farmer_1" {
		t.Errorf("agent_id = %q, want farmer_1", result.AgentID)
	}
	if result.Tick != 42 {
		t.Errorf("tick = %d, want 42", result.Tick)
	}

	triple, ok := result.PerStat["Strength"]
	if !ok {
		t.Fatal("per_stat missing Strength")
	}

	if triple.Real != 0.72 {
		t.Errorf("real = %f, want 0.72", triple.Real)
	}
	if triple.SelfEstimate != 0.55 {
		t.Errorf("self_estimate = %f, want 0.55 (D8: NOT real)", triple.SelfEstimate)
	}
	if triple.OthersEstimateMean != 0.60 {
		t.Errorf("others_estimate_mean = %f, want 0.60", triple.OthersEstimateMean)
	}

	// D8: verify three channels are NOT blended.
	if triple.SelfEstimate == triple.Real {
		t.Error("D8 violation: self_estimate == real, they must be separate channels")
	}
}

// ── Test helpers ─────────────────────────────────────────────────────────────────

// buildSnapshotWithTomDigest builds a snapshot with real_stats, self_est_stats, and tom_digest.
func buildSnapshotWithTomDigest(t *testing.T, agentID string, realStats map[string]any, selfEstStats map[string]any, tomDigest map[string]any) []byte {
	t.Helper()
	agentEntry := map[string]any{
		"id":         agentID,
		"pos":        map[string]any{"x": 10.0, "y": 20.0},
		"real_stats": realStats,
	}
	if selfEstStats != nil {
		agentEntry["self_est_stats"] = selfEstStats
	}

	w := map[string]any{
		"Tick":     42,
		"RNGState": map[string]any{"Data": "dGVzdA=="},
		"Agents":   []any{agentEntry},
	}
	if tomDigest != nil {
		w["tom_digest"] = tomDigest
	}

	doc := map[string]any{
		"schema_version": 1,
		"run_id":         "test-run",
		"tick":           42,
		"world":          w,
	}
	blob, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return blob
}

// toJSON marshals v to a JSON string, failing the test on error.
func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("toJSON: %v", err)
	}
	return string(b)
}
