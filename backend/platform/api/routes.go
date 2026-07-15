package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/dogring/bdg/engine/kernel/core"
)

const (
	agentNotFound      = `{"error":"agent not found"}`
	snapNotFound       = `{"error":"snapshot not found"}`
	terrainNotFound    = `{"error":"terrain not found"}`
	floraNotFound      = `{"error":"flora not found"}`
	metaNotFound       = `{"error":"meta not found"}`
	restartUnavailable = `{"error":"restart unavailable"}`
	regenUnavailable   = `{"error":"regen unavailable"}`
	regenInvalidSeed   = `{"error":"invalid seed"}`
)

// ── GET /healthz ──────────────────────────────────────────────────────────────

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// ── GET /readyz ───────────────────────────────────────────────────────────────

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if err := s.rds.Ping(r.Context()); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"redis unavailable"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// ── GET /api/meta ────────────────────────────────────────────────────────────

// handleMeta forwards the sim:{run}:meta hash as a flat JSON object of strings:
// {tick, schema_version, started_at, status, world_revision?, terrain?}.
// world_revision is the PUBLISHED single-world revision marker (data-contracts
// §2) — written last, after the revision's snapshot+terrain baselines are
// servable — so a poller that observes a new value may immediately load the
// matching baselines. terrain ("on"/"off") is the published revision's explicit
// env-terrain availability: clients must never infer env-off from a failed
// /api/terrain fetch.
func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	hash, err := s.rds.HGetAll(r.Context(), s.keyer.Meta())
	if err != nil || len(hash) == 0 {
		http.Error(w, metaNotFound, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(hash)
}

// ── GET /api/snapshot ─────────────────────────────────────────────────────────

func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	blob, err := s.live.ReadSnapshot(r.Context(), s.runID)
	if err != nil {
		http.Error(w, snapNotFound, http.StatusNotFound)
		return
	}
	if blob == nil {
		http.Error(w, snapNotFound, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(blob)
}

// ── GET /api/terrain ─────────────────────────────────────────────────────────

// handleTerrain forwards the sim:{run}:terrain bytes verbatim (WI-P4,
// data-contracts §2): persist.TerrainView is written to the key already shaped
// exactly as this route's response ({cell_size, size:{w,h}, terrain[], wear?[]}
// — frontend/SPEC.md TerrainGrid), so no reshaping happens here. The key exists
// only when env (navmap) is installed; absence is served as 404, which the
// frontend treats as "no terrain layer" (env-off neutrality).
func (s *Server) handleTerrain(w http.ResponseWriter, r *http.Request) {
	blob, err := s.rds.Get(r.Context(), s.keyer.Terrain())
	if err != nil || len(blob) == 0 {
		http.Error(w, terrainNotFound, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(blob)
}

// ── GET /api/flora ─────────────────────────────────────────────────────────

// handleFlora forwards the sim:{run}:flora bytes verbatim (WI-P4,
// data-contracts §2): persist.FloraDoc is written to the key already shaped
// exactly as this route's response ({world_revision,stream_cursor,flora:[{object_id,species,
// pos,stage,width}]} — the frontend flora baseline), so no reshaping happens
// here. The key exists only when flora is installed; absence is served as 404,
// which the frontend treats as "no flora layer" for this revision (env-off
// neutrality). Like /api/terrain, the blob carries world_revision so the client
// verifies it against the snapshot's before applying (mid-regen guard).
func (s *Server) handleFlora(w http.ResponseWriter, r *http.Request) {
	blob, err := s.rds.Get(r.Context(), s.keyer.Flora())
	if err != nil || len(blob) == 0 {
		http.Error(w, floraNotFound, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(blob)
}

// ── POST /api/restart ────────────────────────────────────────────────────────

// handleRestart forwards a restart signal to the sim writer via the injected
// cfg.Restart callback and returns immediately (202). It mutates nothing itself
// — the world rebuild runs on the sim's tick goroutine (D12 single-writer).
// Wired only when the sim injects a callback; nil (e.g. NewSSE-less tests) ⇒ 503.
func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if s.restart == nil {
		http.Error(w, restartUnavailable, http.StatusServiceUnavailable)
		return
	}
	s.restart()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte(`{"status":"restarting"}`))
}

// ── POST /api/regen ──────────────────────────────────────────────────────────

// handleRegen forwards a regenerate signal (rebuild the world from a NEW seed —
// random terrain/placements re-rolled) to the sim writer via the injected
// cfg.Regen callback and returns immediately (202). Optional ?seed=<int64> pins
// the seed for reproducibility; absent/0 ⇒ the writer draws one. Like restart it
// mutates nothing itself (D12 single-writer); nil callback (scenario mode) ⇒ 503.
func (s *Server) handleRegen(w http.ResponseWriter, r *http.Request) {
	if s.regen == nil {
		http.Error(w, regenUnavailable, http.StatusServiceUnavailable)
		return
	}
	var seed int64
	if v := r.URL.Query().Get("seed"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			http.Error(w, regenInvalidSeed, http.StatusBadRequest)
			return
		}
		seed = n
	}
	s.regen(seed)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte(`{"status":"regenerating"}`))
}

// ── GET /api/agents/{id} ─────────────────────────────────────────────────────

func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	if agentID == "" {
		http.Error(w, agentNotFound, http.StatusNotFound)
		return
	}

	// Read the agent hash (AgentView fields).
	hash, err := s.rds.HGetAll(r.Context(), s.keyer.Agent(core.AgentID(agentID)))
	if err != nil {
		http.Error(w, agentNotFound, http.StatusNotFound)
		return
	}
	if len(hash) == 0 {
		http.Error(w, agentNotFound, http.StatusNotFound)
		return
	}

	// Build the response. Start with the AgentView fields from the hash.
	resp := make(map[string]any, len(hash)+2)
	for k, v := range hash {
		resp[k] = v
	}

	// God-view merge: only when BOTH GodMode and ?god=true.
	if s.godMode && r.URL.Query().Get("god") == "true" {
		if rs, err := s.readRealStats(r.Context(), core.AgentID(agentID)); err == nil && rs != nil {
			resp["real_stats"] = rs
		}
		// If the snapshot blob is absent or the agent not found inside it, we still
		// return the agent — the god-view merge is best-effort.
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// readRealStats decodes the snapshot blob and extracts this agent's real_stats.
// Returns nil if the snapshot is missing or the agent id is not found (best-effort).
func (s *Server) readRealStats(ctx context.Context, id core.AgentID) (any, error) {
	blob, err := s.live.ReadSnapshot(ctx, s.runID)
	if err != nil || blob == nil {
		return nil, err
	}

	var doc map[string]any
	if err := json.Unmarshal(blob, &doc); err != nil {
		return nil, err
	}

	world, ok := doc["world"].(map[string]any)
	if !ok {
		return nil, nil
	}

	// Try both `agents` (data-contracts spec) and `Agents` (current Go struct encoding).
	var agents []any
	if a, ok := world["agents"]; ok {
		agents, _ = a.([]any)
	}
	if agents == nil {
		if a, ok := world["Agents"]; ok {
			agents, _ = a.([]any)
		}
	}
	if agents == nil {
		return nil, nil
	}
	agentIDStr := string(id)
	for _, a := range agents {
		am, ok := a.(map[string]any)
		if !ok {
			continue
		}
		// Match by id field (array-of-structs shape: {"id":"...", "real_stats":{...}}).
		if idVal, ok := am["id"]; ok && fmt.Sprint(idVal) == agentIDStr {
			if rs, ok := am["real_stats"]; ok {
				return rs, nil
			}
		}
		// Also check nested map keyed by agent ID (future shape).
		if inner, ok := am[agentIDStr].(map[string]any); ok {
			if rs, ok := inner["real_stats"]; ok {
				return rs, nil
			}
		}
	}

	// Fallback: the snapshot may store agents as a map keyed by agent ID at top level.
	if ags, ok := world["agents"].(map[string]any); ok {
		if entry, ok := ags[agentIDStr].(map[string]any); ok {
			if rs, ok := entry["real_stats"]; ok {
				return rs, nil
			}
		}
	}

	return nil, nil
}
