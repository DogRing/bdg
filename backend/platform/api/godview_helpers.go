package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/dogring/bdg/engine/core"
)

// ── TomDigest extraction helpers ─────────────────────────────────────────────────

// extractTomDigest pulls the tom_digest map from the world document.
// Returns nil if absent (triggering 206 partial fallback).
func extractTomDigest(worldDoc map[string]any) map[string]any {
	td, ok := worldDoc["tom_digest"]
	if !ok {
		return nil
	}
	tdMap, ok := td.(map[string]any)
	if !ok {
		return nil
	}
	return tdMap
}

// extractEmergedRoles pulls the emerged_roles from the world document.
// Returns map[core.Function]core.AgentID.
func extractEmergedRoles(worldDoc map[string]any) map[core.Function]core.AgentID {
	er, ok := worldDoc["emerged_roles"]
	if !ok {
		return nil
	}
	erList, ok := er.([]any)
	if !ok {
		return nil
	}
	out := make(map[core.Function]core.AgentID)
	for _, item := range erList {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		fn := core.Function(fmt.Sprint(entry["function"]))
		holder := core.AgentID(fmt.Sprint(entry["holder"]))
		if fn != "" && holder != "" {
			out[fn] = holder
		}
	}
	return out
}

// buildObserverClusters partitions each observer into a cluster based on their
// highest RelyOn value across the emerged-role functions. Returns observerID -> "cluster_<holder>".
func buildObserverClusters(tomDigest map[string]any, emergedRoles map[core.Function]core.AgentID) map[core.AgentID]string {
	out := make(map[core.AgentID]string)
	for obsID, beliefs := range tomDigest {
		obsID := core.AgentID(obsID)
		beliefsMap, ok := beliefs.(map[string]any)
		if !ok {
			out[obsID] = "cluster_unknown"
			continue
		}

		bestHolder := ""
		bestShare := 0.0
		for fn, holder := range emergedRoles {
			holderBelief, ok := beliefsMap[string(holder)].(map[string]any)
			if !ok {
				continue
			}
			relyOnRaw, _ := holderBelief["rely_on"].(map[string]any)
			if relyOnRaw == nil {
				continue
			}
			share := toFloat64(relyOnRaw[string(fn)])
			if share > bestShare {
				bestShare = share
				bestHolder = string(holder)
			}
		}

		if bestHolder == "" {
			out[obsID] = "cluster_unknown"
		} else {
			out[obsID] = "cluster_" + bestHolder
		}
	}
	return out
}

// othersEstimateMean computes the mean of ToM_X[id][sid].Mean for all X != id.
func othersEstimateMean(tomDigest map[string]any, subjectID core.AgentID, statID core.StatID) float64 {
	var sum, count float64
	sidStr := string(statID)
	subjStr := string(subjectID)
	for observerID, beliefs := range tomDigest {
		if observerID == subjStr {
			continue
		}
		beliefsMap, ok := beliefs.(map[string]any)
		if !ok {
			continue
		}
		subjectBelief, ok := beliefsMap[subjStr].(map[string]any)
		if !ok {
			continue
		}
		estStats, ok := subjectBelief["est_stats"].(map[string]any)
		if !ok {
			continue
		}
		statEntry, ok := estStats[sidStr].(map[string]any)
		if !ok {
			continue
		}
		mean := toFloat64(statEntry["mean"])
		sum += mean
		count++
	}
	if count == 0 {
		return 0
	}
	return sum / count
}

// findAgentInWorld locates an agent's digest map inside the world document.
func findAgentInWorld(worldDoc map[string]any, id core.AgentID) (map[string]any, error) {
	idStr := string(id)

	// Try agents as array.
	if agents, ok := worldDoc["agents"].([]any); ok {
		for _, a := range agents {
			am, ok := a.(map[string]any)
			if !ok {
				continue
			}
			if aid := fmt.Sprint(am["id"]); aid == idStr {
				return am, nil
			}
		}
	}

	// Try agents as map keyed by ID.
	if agents, ok := worldDoc["agents"].(map[string]any); ok {
		if entry, ok := agents[idStr].(map[string]any); ok {
			return entry, nil
		}
	}

	// Try Agents (capitalized, Go struct encoding).
	if agents, ok := worldDoc["Agents"].([]any); ok {
		for _, a := range agents {
			am, ok := a.(map[string]any)
			if !ok {
				continue
			}
			if aid := fmt.Sprint(am["id"]); aid == idStr {
				return am, nil
			}
		}
	}

	return nil, fmt.Errorf("agent not found")
}

// ── Event payload decoders ──────────────────────────────────────────────────────

// decodeGoalSelected converts a GoalSelected event payload into GoalSelectedView.
func decodeGoalSelected(payload any) *GoalSelectedView {
	p, ok := payload.(map[string]any)
	if !ok {
		return nil
	}

	view := &GoalSelectedView{
		Dimension: core.Dimension(fmt.Sprint(p["dimension"])),
		EffValue:  toFloat64(p["eff_value"]),
	}

	if target, ok := p["target"]; ok && target != nil {
		if ts, ok2 := target.(string); ok2 {
			objID := core.ObjectID(ts)
			view.Target = &objID
		}
	}

	if cc, ok := p["competing_candidates"]; ok {
		if ccList, ok2 := cc.([]any); ok2 {
			for _, c := range ccList {
				if cm, ok3 := c.(map[string]any); ok3 {
					var cand GoalCandidate
					cand.Dimension = core.Dimension(fmt.Sprint(cm["dimension"]))
					cand.EffValue = toFloat64(cm["eff_value"])
					if t, ok4 := cm["target"]; ok4 && t != nil {
						if ts, ok5 := t.(string); ok5 {
							objID := core.ObjectID(ts)
							cand.Target = &objID
						}
					}
					view.CompetingCandidates = append(view.CompetingCandidates, cand)
				}
			}
		}
	}
	if view.CompetingCandidates == nil {
		view.CompetingCandidates = []GoalCandidate{}
	}

	return view
}

// decodePlanBuilt converts a PlanBuilt event payload into PlanBuiltView.
func decodePlanBuilt(payload any) *PlanBuiltView {
	p, ok := payload.(map[string]any)
	if !ok {
		return nil
	}

	return &PlanBuiltView{
		Steps:       toStringSlice(p["steps"]),
		TotalCost:   toFloat64(p["total_cost"]),
		Provisioned: toStringSlice(p["provisioned"]),
	}
}

// ── Sorting helpers (D12) ────────────────────────────────────────────────────────

func sortedTomKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return keys
}

func sortedStringKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return keys
}

func sortAgentIDs(ids []core.AgentID) {
	sortSlice(ids, func(i, j int) bool { return string(ids[i]) < string(ids[j]) })
}

func sortFactionRepsByID(reps []FactionRep) {
	sortSlice(reps, func(i, j int) bool { return reps[i].FactionID < reps[j].FactionID })
}

// sortStrings sorts a string slice in ascending order (D12: stable ordering).
func sortStrings(s []string) {
	n := len(s)
	for i := 0; i < n-1; i++ {
		for j := i + 1; j < n; j++ {
			if s[i] > s[j] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

// sortSlice sorts any slice with the given less function (D12: stable ordering).
func sortSlice[T any](s []T, less func(i, j int) bool) {
	n := len(s)
	for i := 0; i < n-1; i++ {
		for j := i + 1; j < n; j++ {
			if less(j, i) {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

// ── Conversion helpers ───────────────────────────────────────────────────────────

func toFloat64(v any) float64 {
	switch tv := v.(type) {
	case float64:
		return tv
	case int:
		return float64(tv)
	case int64:
		return float64(tv)
	case uint64:
		return float64(tv)
	case uint32:
		return float64(tv)
	case int32:
		return float64(tv)
	case float32:
		return float64(tv)
	case json.Number:
		f, _ := tv.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(tv, 64)
		return f
	}
	return 0
}

func toStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	sl, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, len(sl))
	for i, item := range sl {
		out[i] = fmt.Sprint(item)
	}
	return out
}

// ── Response writers ─────────────────────────────────────────────────────────────

func writePartial(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusPartialContent)
	_ = json.NewEncoder(w).Encode(PartialResponse{
		Partial: true,
		Reason:  reason,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
