package main

import (
	"math"
	"sync"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/world"
)

// ── Counting EventEmitter ───────────────────────────────────────────────────

// countedEvent records one event from the stream for post-hoc analysis.
type countedEvent struct {
	Type    string
	Tick    core.Tick
	AgentID core.AgentID
	Action  string
	Payload map[string]any
}

// countingEmitter wraps a NoopEmitter and records events for metric extraction.
// Thread-safe for concurrent plan-phase emission.
type countingEmitter struct {
	mu     sync.Mutex
	events []countedEvent
}

func newCountingEmitter() *countingEmitter {
	return &countingEmitter{}
}

func (c *countingEmitter) Emit(e core.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()

	action := ""
	if p := toMap(e.Payload); p != nil {
		if a, ok := p["action"]; ok {
			if s, ok2 := a.(string); ok2 {
				action = s
			}
		}
	}
	c.events = append(c.events, countedEvent{
		Type:    e.Type,
		Tick:    e.Tick,
		AgentID: e.AgentID,
		Action:  action,
		Payload: toMap(e.Payload),
	})
}

// toMap safely converts a payload to a map if it isn't already.
func toMap(payload any) map[string]any {
	if m, ok := payload.(map[string]any); ok {
		return m
	}
	return nil
}

// ── Metric collection ───────────────────────────────────────────────────────

// SeedResult holds the emergence metrics from a single run (one seed).
type SeedResult struct {
	Seed                     int64   `json:"seed"`
	CrimeRate                float64 `json:"crime_rate"`
	ReputationVariance       float64 `json:"reputation_variance"`
	RoleConvergence          bool    `json:"role_convergence"`
	SafetyMean               float64 `json:"safety_mean"`
	StarvationConcentration  float64 `json:"starvation_concentration"`
	TotalActions             int     `json:"total_actions"`
	TransgressiveActions     int     `json:"transgressive_actions"`
}

// transgressiveActions lists action IDs considered transgressive for crime-rate
// computation. Matches norm:transgressive tag from actions.yaml.
var transgressiveActions = map[string]bool{
	"Take":   true,
	"Attack": true,
}

// collectMetrics gathers emergence metrics from the world and event log after a run.
func collectMetrics(w *world.World, emitter *countingEmitter) SeedResult {
	// ── Crime rate from events (ActionDone events carry action name in payload) ──
	var totalActions, transgActions int
	for _, ev := range emitter.events {
		if ev.Type == "ActionDone" {
			totalActions++
			if transgressiveActions[ev.Action] {
				transgActions++
			}
		}
	}

	crimeRate := 0.0
	if totalActions > 0 {
		crimeRate = float64(transgActions) / float64(totalActions)
	}

	// ── Role convergence from events ──────────────────────────────────────
	var safetyRoleEmerged bool
	for _, ev := range emitter.events {
		if ev.Type == "RoleEmerged" && ev.Payload != nil {
			if fn, ok := ev.Payload["function"]; ok {
				if fnStr, ok2 := fn.(string); ok2 && fnStr == "Safety" {
					safetyRoleEmerged = true
				}
			}
		}
	}

	// ── Safety mean across agents ─────────────────────────────────────────
	safetyDim := core.Dimension("Safety")
	var safetySum float64
	var safetyCount int
	for _, id := range w.AgentIDs() {
		a, ok := w.AgentOf(id)
		if !ok {
			continue
		}
		if val, exists := a.NeedIntensities[safetyDim]; exists {
			safetySum += val
			safetyCount++
		}
	}
	safetyMean := 0.0
	if safetyCount > 0 {
		safetyMean = safetySum / float64(safetyCount)
	}

	return SeedResult{
		TotalActions:            totalActions,
		TransgressiveActions:    transgActions,
		CrimeRate:               round4(crimeRate),
		ReputationVariance:      round4(computeReputationVariance(w)),
		RoleConvergence:         safetyRoleEmerged,
		SafetyMean:              round4(safetyMean),
		StarvationConcentration: round4(computeStarvationConcentration(w)),
	}
}

// computeReputationVariance computes the mean per-agent variance of Honesty
// estimates across all subjects an agent knows about.
func computeReputationVariance(w *world.World) float64 {
	honestyStatID := core.StatID("Honesty")

	var totalVariance float64
	var agentCount int

	for _, id := range w.AgentIDs() {
		a, ok := w.AgentOf(id)
		if !ok {
			continue
		}

		// Collect Honesty means across all subjects this agent knows (excluding self).
		var honestyMeans []float64
		for _, subjectID := range a.ToM.Subjects() {
			if subjectID == id {
				continue // skip self
			}
			b, ok := a.ToM.Self(subjectID)
			if !ok {
				continue
			}
			if sd, exists := b.EstStats[honestyStatID]; exists {
				honestyMeans = append(honestyMeans, sd.Mean)
			}
		}

		if len(honestyMeans) < 2 {
			continue
		}

		// Compute variance of this agent's Honesty estimates.
		var sum, sumSq float64
		for _, m := range honestyMeans {
			sum += m
			sumSq += m * m
		}
		mean := sum / float64(len(honestyMeans))
		variance := (sumSq / float64(len(honestyMeans))) - mean*mean
		if variance < 0 {
			variance = 0
		}
		totalVariance += variance
		agentCount++
	}

	if agentCount == 0 {
		return 0
	}
	return totalVariance / float64(agentCount)
}

// computeStarvationConcentration returns the fraction of low-Intelligence agents
// (RealStats["Intelligence"] < 0.3) whose Satiety need intensity > 0.7.
func computeStarvationConcentration(w *world.World) float64 {
	intelStatID := core.StatID("Intelligence")
	satietyDim := core.Dimension("Satiety")

	var lowIntelCount, starvingLowIntelCount int

	for _, id := range w.AgentIDs() {
		a, ok := w.AgentOf(id)
		if !ok {
			continue
		}

		intel := a.RealStats.Get(intelStatID)
		if intel >= 0.3 {
			continue
		}
		lowIntelCount++

		if sat, exists := a.NeedIntensities[satietyDim]; exists && sat > 0.7 {
			starvingLowIntelCount++
		}
	}

	if lowIntelCount == 0 {
		return 0
	}
	return float64(starvingLowIntelCount) / float64(lowIntelCount)
}

// round4 rounds a float64 to 4 decimal places.
func round4(v float64) float64 {
	return math.Round(v*1e4) / 1e4
}
