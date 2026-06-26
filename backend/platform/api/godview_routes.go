package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/dogring/bdg/engine/kernel/core"
)

// ── Gate guard ───────────────────────────────────────────────────────────────────

// godViewGate returns true if the request passes the god-view gate: startup GodMode
// AND per-request ?god=true. Otherwise it writes 403 and returns false.
// Called BEFORE any store read (the 403 path makes zero store calls).
func (s *Server) godViewGate(w http.ResponseWriter, r *http.Request) bool {
	if !s.godMode || r.URL.Query().Get("god") != "true" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"god mode disabled"}`))
		return false
	}
	return true
}

// ── GET /api/god/agent/{id}/divergence ───────────────────────────────────────────

func (s *Server) handleGodDivergence(w http.ResponseWriter, r *http.Request) {
	if !s.godViewGate(w, r) {
		return
	}
	agentID := core.AgentID(r.PathValue("id"))
	if agentID == "" {
		http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
		return
	}

	// Read snapshot blob.
	blob, err := s.live.ReadSnapshot(r.Context(), s.runID)
	if err != nil || blob == nil {
		http.Error(w, snapNotFound, http.StatusNotFound)
		return
	}

	// Decode the snapshot JSON.
	var doc struct {
		Tick  int64          `json:"tick"`
		World map[string]any `json:"world"`
	}
	if err := json.Unmarshal(blob, &doc); err != nil {
		http.Error(w, `{"error":"snapshot decode error"}`, http.StatusInternalServerError)
		return
	}
	worldDoc, ok := doc.World["world"].(map[string]any)
	if !ok {
		worldDoc = doc.World
	}

	// Locate the agent digest inside the world.
	agentDigest, err := findAgentInWorld(worldDoc, agentID)
	if err != nil {
		http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
		return
	}

	// Check for TomDigest — if absent, return 206 Partial.
	tomDigest := extractTomDigest(worldDoc)
	if tomDigest == nil {
		writePartial(w, "tom_digest not available in snapshot")
		return
	}

	// Build per-stat divergence.
	realStats, _ := agentDigest["real_stats"].(map[string]any)
	selfEstStats, _ := agentDigest["self_est_stats"].(map[string]any)

	// Collect all stat IDs present across real_stats and self_est_stats.
	statSet := make(map[string]bool)
	for k := range realStats {
		statSet[k] = true
	}
	for k := range selfEstStats {
		statSet[k] = true
	}

	perStat := make(map[core.StatID]StatTriple)
	for sid := range statSet {
		triple := StatTriple{}
		if rv, ok := realStats[sid]; ok {
			triple.Real = toFloat64(rv)
		}
		if sv, ok := selfEstStats[sid]; ok {
			if m, ok2 := sv.(map[string]any); ok2 {
				triple.SelfEstimate = toFloat64(m["mean"])
			}
		}

		// OthersEstimateMean: mean of ToM_X[id][sid].Mean over X != id.
		triple.OthersEstimateMean = othersEstimateMean(tomDigest, agentID, core.StatID(sid))

		perStat[core.StatID(sid)] = triple
	}

	resp := DivergenceResponse{
		AgentID: agentID,
		Tick:    core.Tick(doc.Tick),
		PerStat: perStat,
	}

	writeJSON(w, http.StatusOK, resp)
}

// ── GET /api/god/reputation/{id} ────────────────────────────────────────────────

func (s *Server) handleGodReputation(w http.ResponseWriter, r *http.Request) {
	if !s.godViewGate(w, r) {
		return
	}
	subjectID := core.AgentID(r.PathValue("id"))
	if subjectID == "" {
		http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
		return
	}

	blob, err := s.live.ReadSnapshot(r.Context(), s.runID)
	if err != nil || blob == nil {
		http.Error(w, snapNotFound, http.StatusNotFound)
		return
	}

	var doc struct {
		Tick  int64          `json:"tick"`
		World map[string]any `json:"world"`
	}
	if err := json.Unmarshal(blob, &doc); err != nil {
		http.Error(w, `{"error":"snapshot decode error"}`, http.StatusInternalServerError)
		return
	}
	worldDoc, ok := doc.World["world"].(map[string]any)
	if !ok {
		worldDoc = doc.World
	}

	// Verify subject exists.
	if _, err := findAgentInWorld(worldDoc, subjectID); err != nil {
		http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
		return
	}

	tomDigest := extractTomDigest(worldDoc)
	if tomDigest == nil {
		writePartial(w, "tom_digest not available in snapshot")
		return
	}

	// Build emerged-role holder lookup.
	emergedRoles := extractEmergedRoles(worldDoc)
	observerClusters := buildObserverClusters(tomDigest, emergedRoles)

	// Gather all observers (agents other than subject) from tomDigest.
	observers := make(map[core.AgentID]map[string]any)
	for obsIDStr, beliefsAny := range tomDigest {
		obsID := core.AgentID(obsIDStr)
		if obsID == subjectID {
			continue
		}
		beliefs, ok := beliefsAny.(map[string]any)
		if !ok {
			continue
		}
		if belief, ok := beliefs[string(subjectID)].(map[string]any); ok {
			observers[obsID] = belief
		}
	}

	if len(observers) == 0 {
		resp := ReputationResponse{
			SubjectID: subjectID,
			Tick:      core.Tick(doc.Tick),
			PerStat:   make(map[core.StatID]StatReputation),
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Collect all stat IDs across observers.
	statSet := make(map[core.StatID]bool)
	for _, belief := range observers {
		estStats, _ := belief["est_stats"].(map[string]any)
		for sid := range estStats {
			statSet[core.StatID(sid)] = true
		}
	}

	perStat := make(map[core.StatID]StatReputation)
	for sid := range statSet {
		sidStr := string(sid)
		var sum, sumSq float64
		count := 0
		factionSums := make(map[string]struct{ sum, count float64 })
		for obsID, belief := range observers {
			estStats, _ := belief["est_stats"].(map[string]any)
			statEntry, ok := estStats[sidStr]
			if !ok {
				continue
			}
			statMap, ok2 := statEntry.(map[string]any)
			if !ok2 {
				continue
			}
			mean := toFloat64(statMap["mean"])
			sum += mean
			sumSq += mean * mean
			count++

			clusterLabel := observerClusters[obsID]
			f := factionSums[clusterLabel]
			f.sum += mean
			f.count++
			factionSums[clusterLabel] = f
		}

		if count == 0 {
			continue
		}
		mean := sum / float64(count)
		variance := (sumSq / float64(count)) - (mean * mean)
		if variance < 0 {
			variance = 0
		}

		var factions []FactionRep
		for label, f := range factionSums {
			factionMean := f.sum / f.count
			var members []core.AgentID
			for oid, cl := range observerClusters {
				if cl == label {
					members = append(members, oid)
				}
			}
			sortAgentIDs(members)
			factions = append(factions, FactionRep{
				FactionID: label,
				Agents:    members,
				Mean:      factionMean,
			})
		}
		sortFactionRepsByID(factions)

		perStat[sid] = StatReputation{
			Mean:       mean,
			Variance:   variance,
			PerFaction: factions,
		}
	}

	resp := ReputationResponse{
		SubjectID: subjectID,
		Tick:      core.Tick(doc.Tick),
		PerStat:   perStat,
	}
	writeJSON(w, http.StatusOK, resp)
}

// ── GET /api/god/relations ──────────────────────────────────────────────────────

func (s *Server) handleGodRelations(w http.ResponseWriter, r *http.Request) {
	if !s.godViewGate(w, r) {
		return
	}

	blob, err := s.live.ReadSnapshot(r.Context(), s.runID)
	if err != nil || blob == nil {
		http.Error(w, snapNotFound, http.StatusNotFound)
		return
	}

	var doc struct {
		Tick  int64          `json:"tick"`
		World map[string]any `json:"world"`
	}
	if err := json.Unmarshal(blob, &doc); err != nil {
		http.Error(w, `{"error":"snapshot decode error"}`, http.StatusInternalServerError)
		return
	}
	worldDoc, ok := doc.World["world"].(map[string]any)
	if !ok {
		worldDoc = doc.World
	}

	tomDigest := extractTomDigest(worldDoc)
	if tomDigest == nil {
		writePartial(w, "tom_digest not available in snapshot")
		return
	}

	var edges []RelationEdge
	for _, fromID := range sortedTomKeys(tomDigest) {
		beliefs, _ := tomDigest[fromID].(map[string]any)
		for _, toID := range sortedTomKeys(beliefs) {
			if fromID == toID {
				continue
			}
			belief, _ := beliefs[toID].(map[string]any)
			if belief == nil {
				continue
			}

			affinity := toFloat64(belief["affinity"])
			trust := toFloat64(belief["trust"])

			relyOnRaw, _ := belief["rely_on"].(map[string]any)
			relyOn := make(map[string]float64)
			for _, fnKey := range sortedStringKeys(relyOnRaw) {
				relyOn[fnKey] = toFloat64(relyOnRaw[fnKey])
			}

			// Emit edge only when at least one of {Affinity, Trust, any RelyOn} is non-zero.
			if affinity == 0 && trust == 0 && len(relyOn) == 0 {
				continue
			}
			hasNonZeroRelyOn := false
			for _, v := range relyOn {
				if v != 0 {
					hasNonZeroRelyOn = true
					break
				}
			}
			if !hasNonZeroRelyOn {
				relyOn = nil
			}

			edges = append(edges, RelationEdge{
				From:     core.AgentID(fromID),
				To:       core.AgentID(toID),
				Affinity: affinity,
				Trust:    trust,
				RelyOn:   relyOn,
			})
		}
	}

	resp := RelationsResponse{
		Tick:  core.Tick(doc.Tick),
		Edges: edges,
	}
	writeJSON(w, http.StatusOK, resp)
}

// ── GET /api/god/why/{agent_id}/{tick} ───────────────────────────────────────────

func (s *Server) handleGodWhy(w http.ResponseWriter, r *http.Request) {
	if !s.godViewGate(w, r) {
		return
	}
	agentID := core.AgentID(r.PathValue("agent_id"))
	tickStr := r.PathValue("tick")

	tick, err := strconv.ParseInt(tickStr, 10, 64)
	if err != nil {
		http.Error(w, `{"error":"invalid tick"}`, http.StatusBadRequest)
		return
	}

	if s.gv == nil {
		http.Error(w, `{"error":"why-trace store unavailable"}`, http.StatusServiceUnavailable)
		return
	}

	events, err := s.gv.QueryEvents(r.Context(), s.runID, agentID, core.Tick(tick))
	if err != nil {
		log.Printf("api: QueryEvents error: %v", err)
		http.Error(w, `{"error":"why-trace query failed"}`, http.StatusInternalServerError)
		return
	}

	var goalSelected *GoalSelectedView
	var planBuilt *PlanBuiltView

	for _, ev := range events {
		switch ev.Type {
		case "GoalSelected":
			gs := decodeGoalSelected(ev.Payload)
			if gs != nil {
				goalSelected = gs
			}
		case "PlanBuilt":
			pb := decodePlanBuilt(ev.Payload)
			if pb != nil {
				planBuilt = pb
			}
		}
	}

	if goalSelected == nil && planBuilt == nil {
		http.Error(w, `{"error":"no why-trace for agent at tick"}`, http.StatusNotFound)
		return
	}

	resp := WhyResponse{
		AgentID:      agentID,
		Tick:         core.Tick(tick),
		GoalSelected: goalSelected,
		PlanBuilt:    planBuilt,
	}
	writeJSON(w, http.StatusOK, resp)
}
