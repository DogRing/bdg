package world

import (
	"fmt"
	"testing"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
)

// ── Plan-phase event sequence is scheduler-independent (SPEC-tick Phase 2, D12) ──
//
// agent.Tick emits (GoalSelected/PlanBuilt/Perceived/CopingEntered) DURING the
// parallel plan phase. Direct emission from those goroutines would interleave
// events across agents nondeterministically; the world must buffer per agent and
// flush in sorted agent-ID order. A sequential-Emit test cannot catch this — the
// test below runs the REAL parallel plan phase with many agents.

// planPhaseTypes are the event types agent.Tick emits during the plan phase.
var planPhaseTypes = map[string]bool{
	"GoalSelected": true, "PlanBuilt": true, "Perceived": true, "CopingEntered": true,
}

// spawnManyAgents spawns n agents with deterministic ids/positions/rngs.
func spawnManyAgents(t *testing.T, fx *testFixture, n int, seed int64) {
	t.Helper()
	cfg := agent.DefaultConfig()
	for i := range n {
		id := core.AgentID(fmt.Sprintf("agent_%02d", i))
		pos := core.Vec2{X: float64(i * 3), Y: float64(i * 2)}
		fx.world.Spawn(id, pos, cfg, rng.New(seed+int64(i)))
	}
}

// eventKey flattens the identity of one emitted event for sequence comparison.
func eventKey(e core.Event) string {
	return fmt.Sprintf("%d/%s/%s", int64(e.Tick), e.Type, e.AgentID)
}

func TestPlanPhaseEventOrder_SortedByAgentID(t *testing.T) {
	fx := newFixtureSeeded(t, 99)
	spawnManyAgents(t, fx, 12, 7)

	const ticks = 3
	for range ticks {
		fx.world.Tick()
	}

	// Within each tick, the plan-phase events must be grouped per agent in
	// sorted agent-ID order — never interleaved by goroutine scheduling.
	planEvents := 0
	lastTick := core.Tick(-1)
	lastAgent := ""
	for _, e := range fx.emit.events {
		if !planPhaseTypes[e.Type] {
			continue
		}
		planEvents++
		if e.Tick != lastTick {
			lastTick, lastAgent = e.Tick, ""
		}
		if string(e.AgentID) < lastAgent {
			t.Fatalf("tick %d: plan-phase event for %q emitted after %q — order not sorted by agent ID",
				int64(e.Tick), e.AgentID, lastAgent)
		}
		lastAgent = string(e.AgentID)
	}
	// Guard: the assertion above is vacuous unless several agents actually
	// emitted in the same ticks.
	if planEvents < 12 {
		t.Fatalf("only %d plan-phase events observed; test needs many concurrent emitters", planEvents)
	}
}

func TestPlanPhaseEventSequence_DeterministicAcrossRuns(t *testing.T) {
	run := func() []string {
		fx := newFixtureSeeded(t, 99)
		spawnManyAgents(t, fx, 12, 7)
		for range 3 {
			fx.world.Tick()
		}
		keys := make([]string, 0, len(fx.emit.events))
		for _, e := range fx.emit.events {
			keys = append(keys, eventKey(e))
		}
		return keys
	}

	a, b := run(), run()
	if len(a) != len(b) {
		t.Fatalf("event counts differ across identical-seed runs: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("event %d differs across identical-seed runs: %q vs %q (scheduler-dependent order)",
				i, a[i], b[i])
		}
	}
}
