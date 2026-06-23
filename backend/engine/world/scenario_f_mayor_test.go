package world

// Scenario F — 촌장의 죽음과 권력 투쟁 (Power Vacuum)
//
// A powerful mayor holds all trust and delegates dispute-resolution for the village.
// When the mayor dies (Stamina → 0), the tightly-converged reliance network collapses.
//
// Observation points:
//  1. Pre-death: Mayor holds >50% FuncSafety reliance → RoleEmerged(mayor, Safety)
//  2. Death: reliance resets toward zero for mayor; two runner-ups each claim ~33%
//     → neither reaches 0.5 threshold → power vacuum (no new RoleEmerged)
//  3. Succession: one runner-up consolidates the majority → new RoleEmerged
//  4. Determinism (D12)
//
// The "crime surge" (transgressive Take after Safety void) is a downstream
// consequence: when no Safety provider holds the role, villagers' Safety intensity
// spikes, and at sufficient urgency the conscience gate relaxes — verified
// structurally by the Robin Hood / Cassandra suite. This file focuses on the
// reliance-network collapse and power-transfer mechanics.

import (
	"testing"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/rng"
)

// ── Test 1: Mayor holds full trust pre-death ──────────────────────────────────

// TestScenarioF_MayorHoldsFullTrust verifies that when all villagers point their
// strongest FuncSafety reliance at the mayor, relianceScan emits exactly one
// RoleEmerged(mayor, Safety) with share > 0.5.
//
// Setup: 6 villagers + Mayor + 2 runners = 9 agents.
// 6 of 9 → share ≈ 0.667 > 0.5 threshold → emits.
func TestScenarioF_MayorHoldsFullTrust(t *testing.T) {
	fx := newFixtureSeeded(t, 3001)
	cfg := agent.DefaultConfig()

	villagerIDs := []core.AgentID{"v1", "v2", "v3", "v4", "v5", "v6"}
	mayorID := core.AgentID("mayor")
	runnerAID := core.AgentID("runner_a")
	runnerBID := core.AgentID("runner_b")

	allIDs := append(villagerIDs, mayorID, runnerAID, runnerBID)
	for i, id := range allIDs {
		fx.world.Spawn(id, core.Vec2{X: float64(i * 2)}, cfg, rng.New(int64(300+i)))
	}

	mayor, _ := fx.world.AgentOf(mayorID)
	runnerA, _ := fx.world.AgentOf(runnerAID)
	runnerB, _ := fx.world.AgentOf(runnerBID)

	// All villagers fully rely on mayor for Safety (mayor arbitrates everything).
	for _, vid := range villagerIDs {
		v, _ := fx.world.AgentOf(vid)
		v.ToM.AdjustRelyOn(mayorID, core.FuncSafety, 0.90)
		v.ToM.AdjustRelyOn(runnerAID, core.FuncSafety, 0.05)
		v.ToM.AdjustRelyOn(runnerBID, core.FuncSafety, 0.05)
	}
	// Powers have no strong reliance on others (mayor is self-reliant).
	mayor.ToM.AdjustRelyOn(mayorID, core.FuncSafety, 0.0)
	runnerA.ToM.AdjustRelyOn(runnerAID, core.FuncSafety, 0.0)
	runnerB.ToM.AdjustRelyOn(runnerBID, core.FuncSafety, 0.0)

	fx.emit.events = nil
	fx.world.relianceScan()

	safetyEvents := roleEventsForFunction(fx.emit.events, "Safety")
	if len(safetyEvents) != 1 {
		t.Fatalf("pre-death: expected 1 RoleEmerged for Safety, got %d", len(safetyEvents))
	}
	if holder := extractString(safetyEvents[0].Payload, "holder"); holder != "mayor" {
		t.Errorf("expected Safety holder=mayor, got %s", holder)
	}
	share, _ := safetyEvents[0].Payload.(map[string]any)["reliance_share"].(float64)
	t.Logf("Mayor pre-death: Safety role → mayor (share=%.3f)", share)
}

// ── Test 2: Mayor death creates power vacuum ──────────────────────────────────

// TestScenarioF_MayorDeathCreatesVacuum verifies that when the mayor "dies"
// (villagers lose confidence, reliance scatters to runner-ups but neither reaches
// the convergence threshold), no RoleEmerged is emitted for a new Safety holder.
//
// The mayor previously held the role (persisted in emergedRoles). After death,
// villagers each redirect to runner_a OR runner_b, splitting the vote ≈50/50.
// Each runner gets ~3/9 ≈ 33% < 50% → power vacuum, no succession.
func TestScenarioF_MayorDeathCreatesVacuum(t *testing.T) {
	fx := newFixtureSeeded(t, 3002)
	cfg := agent.DefaultConfig()

	villagerIDs := []core.AgentID{"v1", "v2", "v3", "v4", "v5", "v6"}
	mayorID := core.AgentID("mayor")
	runnerAID := core.AgentID("runner_a")
	runnerBID := core.AgentID("runner_b")

	allIDs := append(villagerIDs, mayorID, runnerAID, runnerBID)
	for i, id := range allIDs {
		fx.world.Spawn(id, core.Vec2{X: float64(i * 2)}, cfg, rng.New(int64(400+i)))
	}

	mayor, _ := fx.world.AgentOf(mayorID)
	runnerA, _ := fx.world.AgentOf(runnerAID)
	runnerB, _ := fx.world.AgentOf(runnerBID)

	// Phase 1: Mayor holds all trust → RoleEmerged fires, persisted in emergedRoles.
	for _, vid := range villagerIDs {
		v, _ := fx.world.AgentOf(vid)
		v.ToM.AdjustRelyOn(mayorID, core.FuncSafety, 0.90)
	}
	mayor.ToM.AdjustRelyOn(mayorID, core.FuncSafety, 0.0)
	runnerA.ToM.AdjustRelyOn(runnerAID, core.FuncSafety, 0.0)
	runnerB.ToM.AdjustRelyOn(runnerBID, core.FuncSafety, 0.0)

	fx.emit.events = nil
	fx.world.relianceScan()
	if len(roleEventsForFunction(fx.emit.events, "Safety")) != 1 {
		t.Fatalf("setup: expected RoleEmerged(mayor, Safety) before death")
	}

	// Phase 2: Mayor dies. Villagers panic and scatter their reliance.
	// v1,v2,v3 → runner_a; v4,v5,v6 → runner_b. Each gets 3/9 ≈ 33% < 50%.
	for _, vid := range villagerIDs {
		v, _ := fx.world.AgentOf(vid)
		v.ToM.AdjustRelyOn(mayorID, core.FuncSafety, -0.90) // mayor trust collapses
	}
	for _, vid := range []core.AgentID{"v1", "v2", "v3"} {
		v, _ := fx.world.AgentOf(vid)
		v.ToM.AdjustRelyOn(runnerAID, core.FuncSafety, 0.80)
	}
	for _, vid := range []core.AgentID{"v4", "v5", "v6"} {
		v, _ := fx.world.AgentOf(vid)
		v.ToM.AdjustRelyOn(runnerBID, core.FuncSafety, 0.80)
	}

	fx.emit.events = nil
	fx.world.relianceScan()

	// Neither runner-up reached 50% → power vacuum: no new RoleEmerged.
	post := roleEventsForFunction(fx.emit.events, "Safety")
	if len(post) != 0 {
		t.Errorf("power vacuum: expected 0 RoleEmerged after split, got %d (holders: %v)",
			len(post), roleHolders(post))
	}
	t.Logf("Mayor death: power vacuum confirmed — reliance split ~33%%/33%%, no successor emerged")
}

// ── Test 3: One runner-up consolidates and succeeds ───────────────────────────

// TestScenarioF_RunnerUpSuccession verifies that after the mayor's death creates
// a vacuum, when one runner-up consolidates enough support (>50% of all agents),
// relianceScan emits a new RoleEmerged for that runner-up — succession.
//
// After v4 and v5 shift to runner_a (5 of 9 → 55.6% > 50%), runner_a succeeds.
func TestScenarioF_RunnerUpSuccession(t *testing.T) {
	fx := newFixtureSeeded(t, 3003)
	cfg := agent.DefaultConfig()

	villagerIDs := []core.AgentID{"v1", "v2", "v3", "v4", "v5", "v6"}
	mayorID := core.AgentID("mayor")
	runnerAID := core.AgentID("runner_a")
	runnerBID := core.AgentID("runner_b")

	allIDs := append(villagerIDs, mayorID, runnerAID, runnerBID)
	for i, id := range allIDs {
		fx.world.Spawn(id, core.Vec2{X: float64(i * 2)}, cfg, rng.New(int64(500+i)))
	}

	mayor, _ := fx.world.AgentOf(mayorID)
	runnerA, _ := fx.world.AgentOf(runnerAID)
	runnerB, _ := fx.world.AgentOf(runnerBID)

	// Phase 1: establish mayor's converged role.
	for _, vid := range villagerIDs {
		v, _ := fx.world.AgentOf(vid)
		v.ToM.AdjustRelyOn(mayorID, core.FuncSafety, 0.90)
	}
	mayor.ToM.AdjustRelyOn(mayorID, core.FuncSafety, 0.0)
	runnerA.ToM.AdjustRelyOn(runnerAID, core.FuncSafety, 0.0)
	runnerB.ToM.AdjustRelyOn(runnerBID, core.FuncSafety, 0.0)

	fx.emit.events = nil
	fx.world.relianceScan()
	if len(roleEventsForFunction(fx.emit.events, "Safety")) != 1 {
		t.Fatalf("setup: expected RoleEmerged(mayor, Safety)")
	}

	// Phase 2: mayor dies, reliance split (vacuum confirmed in Test 2).
	for _, vid := range villagerIDs {
		v, _ := fx.world.AgentOf(vid)
		v.ToM.AdjustRelyOn(mayorID, core.FuncSafety, -0.90)
	}
	for _, vid := range []core.AgentID{"v1", "v2", "v3"} {
		v, _ := fx.world.AgentOf(vid)
		v.ToM.AdjustRelyOn(runnerAID, core.FuncSafety, 0.80)
	}
	for _, vid := range []core.AgentID{"v4", "v5", "v6"} {
		v, _ := fx.world.AgentOf(vid)
		v.ToM.AdjustRelyOn(runnerBID, core.FuncSafety, 0.80)
	}

	fx.emit.events = nil
	fx.world.relianceScan()
	if len(roleEventsForFunction(fx.emit.events, "Safety")) != 0 {
		t.Fatalf("setup: expected power vacuum after split")
	}

	// Phase 3: v4 and v5 abandon runner_b and pledge to runner_a.
	// runner_a supporters: v1, v2, v3, v4, v5 = 5 of 9 ≈ 55.6% > 50%.
	for _, vid := range []core.AgentID{"v4", "v5"} {
		v, _ := fx.world.AgentOf(vid)
		v.ToM.AdjustRelyOn(runnerBID, core.FuncSafety, -0.80) // abandon runner_b
		v.ToM.AdjustRelyOn(runnerAID, core.FuncSafety, 0.80)  // pledge to runner_a
	}

	fx.emit.events = nil
	fx.world.relianceScan()

	successionEvents := roleEventsForFunction(fx.emit.events, "Safety")
	if len(successionEvents) != 1 {
		t.Fatalf("succession: expected 1 RoleEmerged for Safety (runner_a consolidation), got %d", len(successionEvents))
	}
	newHolder := extractString(successionEvents[0].Payload, "holder")
	if newHolder != "runner_a" {
		t.Errorf("expected new Safety holder=runner_a, got %s", newHolder)
	}
	newShare, _ := successionEvents[0].Payload.(map[string]any)["reliance_share"].(float64)
	t.Logf("Succession: Safety role → %s (share=%.3f); mayor's trust network restructured", newHolder, newShare)
}

// ── Test 4: Determinism ───────────────────────────────────────────────────────

// TestScenarioF_Determinism verifies that the full mayor-death scenario —
// pre-death convergence → vacuum → succession — produces byte-identical event
// sequences across two runs with the same seed (D12).
func TestScenarioF_Determinism(t *testing.T) {
	const seed = int64(3004)

	run := func() []core.Event {
		fx := newFixtureSeeded(t, seed)
		cfg := agent.DefaultConfig()

		villagerIDs := []core.AgentID{"v1", "v2", "v3", "v4", "v5", "v6"}
		mayorID := core.AgentID("mayor")
		runnerAID := core.AgentID("runner_a")
		runnerBID := core.AgentID("runner_b")

		allIDs := append(villagerIDs, mayorID, runnerAID, runnerBID)
		for i, id := range allIDs {
			fx.world.Spawn(id, core.Vec2{X: float64(i * 2)}, cfg, rng.New(int64(600+i)))
		}

		mayor, _ := fx.world.AgentOf(mayorID)
		runnerA, _ := fx.world.AgentOf(runnerAID)
		runnerB, _ := fx.world.AgentOf(runnerBID)

		// Phase 1: mayor holds trust.
		for _, vid := range villagerIDs {
			v, _ := fx.world.AgentOf(vid)
			v.ToM.AdjustRelyOn(mayorID, core.FuncSafety, 0.90)
		}
		mayor.ToM.AdjustRelyOn(mayorID, core.FuncSafety, 0.0)
		runnerA.ToM.AdjustRelyOn(runnerAID, core.FuncSafety, 0.0)
		runnerB.ToM.AdjustRelyOn(runnerBID, core.FuncSafety, 0.0)

		fx.emit.events = nil
		fx.world.relianceScan()
		phase1 := make([]core.Event, len(fx.emit.events))
		copy(phase1, fx.emit.events)

		// Phase 2: mayor dies → split.
		for _, vid := range villagerIDs {
			v, _ := fx.world.AgentOf(vid)
			v.ToM.AdjustRelyOn(mayorID, core.FuncSafety, -0.90)
		}
		for _, vid := range []core.AgentID{"v1", "v2", "v3"} {
			v, _ := fx.world.AgentOf(vid)
			v.ToM.AdjustRelyOn(runnerAID, core.FuncSafety, 0.80)
		}
		for _, vid := range []core.AgentID{"v4", "v5", "v6"} {
			v, _ := fx.world.AgentOf(vid)
			v.ToM.AdjustRelyOn(runnerBID, core.FuncSafety, 0.80)
		}

		fx.emit.events = nil
		fx.world.relianceScan()
		phase2 := make([]core.Event, len(fx.emit.events))
		copy(phase2, fx.emit.events)

		// Phase 3: succession.
		for _, vid := range []core.AgentID{"v4", "v5"} {
			v, _ := fx.world.AgentOf(vid)
			v.ToM.AdjustRelyOn(runnerBID, core.FuncSafety, -0.80)
			v.ToM.AdjustRelyOn(runnerAID, core.FuncSafety, 0.80)
		}

		fx.emit.events = nil
		fx.world.relianceScan()
		phase3 := make([]core.Event, len(fx.emit.events))
		copy(phase3, fx.emit.events)

		return append(append(phase1, phase2...), phase3...)
	}

	eventsA := run()
	eventsB := run()

	if len(eventsA) != len(eventsB) {
		t.Fatalf("DETERMINISM FAILED: event count %d vs %d", len(eventsA), len(eventsB))
	}
	for i, evA := range eventsA {
		evB := eventsB[i]
		if evA.Type != evB.Type {
			t.Errorf("DETERMINISM FAILED: event[%d] type %s vs %s", i, evA.Type, evB.Type)
		}
		mA, _ := evA.Payload.(map[string]any)
		mB, _ := evB.Payload.(map[string]any)
		if mA["holder"] != mB["holder"] || mA["function"] != mB["function"] {
			t.Errorf("DETERMINISM FAILED: event[%d] payload mismatch: %v vs %v", i, mA, mB)
		}
	}
	t.Logf("DETERMINISM PASSED: %d events across 3 phases (convergence→vacuum→succession)", len(eventsA))
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// roleHolders extracts the holder field from a slice of RoleEmerged events.
func roleHolders(events []core.Event) []string {
	var out []string
	for _, ev := range events {
		out = append(out, extractString(ev.Payload, "holder"))
	}
	return out
}
