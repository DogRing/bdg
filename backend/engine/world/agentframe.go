package world

import (
	"sort"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/kernel/core"
)

type agentFrameState struct {
	Pos    core.Vec2
	Goal   string
	Mood   float64
	Action string
}

func (w *World) emitAgentFrame() {
	current := make(map[core.AgentID]agentFrameState, len(w.agents))
	agents := make([]map[string]any, 0, len(w.agentIDs))

	for _, id := range w.agentIDs {
		a := w.agents[id]
		if a == nil {
			continue
		}
		next := agentFrameState{
			Pos:    a.Pos,
			Goal:   string(a.Goal),
			Mood:   a.Mood,
			Action: currentAction(a),
		}
		current[id] = next

		prev, ok := w.lastAgentFrame[id]
		if ok && prev == next {
			continue
		}
		delta := map[string]any{"id": string(id)}
		if !ok || prev.Pos != next.Pos {
			delta["pos"] = next.Pos
		}
		if !ok || prev.Goal != next.Goal {
			delta["goal"] = next.Goal
		}
		if !ok || prev.Mood != next.Mood {
			delta["mood"] = next.Mood
		}
		if !ok || prev.Action != next.Action {
			delta["action"] = next.Action
		}
		agents = append(agents, delta)
	}

	removed := removedAgentIDs(w.lastAgentFrame, current)
	w.lastAgentFrame = current
	if len(agents) == 0 && len(removed) == 0 {
		return
	}

	w.emit.Emit(core.Event{
		SchemaVersion: 1,
		Tick:          w.tick,
		AgentID:       "",
		Type:          "AgentFrame",
		Payload: map[string]any{
			"tick":    int64(w.tick),
			"agents":  agents,
			"removed": removed,
		},
	})
}

func currentAction(a *agent.Agent) string {
	if a.PlanIdx >= 0 && a.PlanIdx < len(a.Plan.Actions) {
		return string(a.Plan.Actions[a.PlanIdx])
	}
	return ""
}

func removedAgentIDs(prev, current map[core.AgentID]agentFrameState) []string {
	if len(prev) == 0 {
		return []string{}
	}
	removed := make([]string, 0)
	for id := range prev {
		if _, ok := current[id]; !ok {
			removed = append(removed, string(id))
		}
	}
	sort.Strings(removed)
	return removed
}
