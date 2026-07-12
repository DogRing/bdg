package world

import (
	"testing"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
)

// lastAgentFrame returns the single AgentFrame recorded since the events slice
// was last cleared, or nil when none was emitted.
func lastAgentFrame(t *testing.T, fx *testFixture) map[string]any {
	t.Helper()
	var payload map[string]any
	for _, ev := range fx.emit.events {
		if ev.Type != "AgentFrame" {
			continue
		}
		if payload != nil {
			t.Fatal("more than one AgentFrame emitted for a single frame")
		}
		payload = ev.Payload.(map[string]any)
	}
	return payload
}

// SPEC-tick.md "AgentFrame is a SPARSE delta": full entry for a never-seen
// agent, no event when nothing changed, changed-fields-only entries, and
// removed agents reported exactly once in sorted removed[].
func TestAgentFrameSparseDelta(t *testing.T) {
	fx := newFixtureSeeded(t, 42)
	cfg := agent.DefaultConfig()
	a := fx.world.Spawn("agent_1", core.Vec2{X: 10, Y: 10}, cfg, rng.New(1))

	// Frame 1: never-seen agent ⇒ full entry.
	fx.emit.events = nil
	fx.world.emitAgentFrame()
	payload := lastAgentFrame(t, fx)
	if payload == nil {
		t.Fatal("first frame: expected an AgentFrame")
	}
	agents := payload["agents"].([]map[string]any)
	if len(agents) != 1 {
		t.Fatalf("first frame agents = %+v, want one full entry", agents)
	}
	for _, key := range []string{"id", "pos", "goal", "mood", "action"} {
		if _, ok := agents[0][key]; !ok {
			t.Errorf("first frame entry missing %q: %+v", key, agents[0])
		}
	}

	// Frame 2: nothing changed ⇒ NO AgentFrame at all.
	fx.emit.events = nil
	fx.world.emitAgentFrame()
	if payload := lastAgentFrame(t, fx); payload != nil {
		t.Fatalf("unchanged frame emitted an AgentFrame: %+v", payload)
	}

	// Frame 3: only pos changed ⇒ entry carries id+pos, omits goal/mood/action.
	a.Pos = core.Vec2{X: 11, Y: 10}
	fx.emit.events = nil
	fx.world.emitAgentFrame()
	payload = lastAgentFrame(t, fx)
	if payload == nil {
		t.Fatal("pos change: expected an AgentFrame")
	}
	agents = payload["agents"].([]map[string]any)
	if len(agents) != 1 {
		t.Fatalf("pos change agents = %+v, want one sparse entry", agents)
	}
	if _, ok := agents[0]["pos"]; !ok {
		t.Errorf("pos change entry missing pos: %+v", agents[0])
	}
	for _, key := range []string{"goal", "mood", "action"} {
		if _, ok := agents[0][key]; ok {
			t.Errorf("pos-only change leaked unchanged field %q: %+v", key, agents[0])
		}
	}
	if removed := payload["removed"].([]string); len(removed) != 0 {
		t.Errorf("removed = %v, want empty", removed)
	}
}

func TestAgentFrameRemovedSorted(t *testing.T) {
	fx := newFixtureSeeded(t, 42)
	cfg := agent.DefaultConfig()
	fx.world.Spawn("agent_b", core.Vec2{X: 10, Y: 10}, cfg, rng.New(1))
	fx.world.Spawn("agent_a", core.Vec2{X: 20, Y: 20}, cfg, rng.New(2))

	fx.world.emitAgentFrame() // baseline: both agents seen

	// Both agents leave the world (no public removal API yet — the frame layer
	// only contracts on the maps' contents).
	delete(fx.world.agents, "agent_a")
	delete(fx.world.agents, "agent_b")
	fx.world.agentIDs = nil

	fx.emit.events = nil
	fx.world.emitAgentFrame()
	payload := lastAgentFrame(t, fx)
	if payload == nil {
		t.Fatal("removal frame: expected an AgentFrame")
	}
	if agents := payload["agents"].([]map[string]any); len(agents) != 0 {
		t.Errorf("removal frame agents = %+v, want empty", agents)
	}
	removed := payload["removed"].([]string)
	if len(removed) != 2 || removed[0] != "agent_a" || removed[1] != "agent_b" {
		t.Errorf("removed = %v, want [agent_a agent_b] (each once, sorted by ID)", removed)
	}

	// Next frame: nothing tracked, nothing changed ⇒ no event (removal reported
	// exactly once).
	fx.emit.events = nil
	fx.world.emitAgentFrame()
	if payload := lastAgentFrame(t, fx); payload != nil {
		t.Fatalf("post-removal frame emitted again: %+v", payload)
	}
}
