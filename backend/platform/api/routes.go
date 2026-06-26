package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/dogring/bdg/engine/kernel/core"
)

const (
	agentNotFound = `{"error":"agent not found"}`
	snapNotFound  = `{"error":"snapshot not found"}`
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
