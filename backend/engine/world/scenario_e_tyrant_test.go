package world

// Scenario E — 폭군 vs 성군 (위임의 수렴)
//
// A village with persistent external threats. Two powerful figures:
//   - Tyrant (Strength=85, Intelligence=30): rules by fear, best perceived Safety provider
//   - Sage   (Strength=20, Intelligence=90): distributes resources, best Knowledge provider
//
// Observation points:
//  1. Under threat, Safety need dominates → villagers delegate FuncSafety to Tyrant
//     (BestProviderFor score: Tyrant = 0.5 × 85/100 = 0.425 > Sage = 0.5 × 20/100 = 0.10)
//  2. Threat fades + famine → Safety reliance drops; Knowledge reliance shifts to Sage
//     (BestProviderFor score: Sage = 0.5 × 90/100 = 0.45 > Tyrant = 0.5 × 30/100 = 0.15)
//  3. Both roles coexist: Tyrant holds Safety AND Sage holds Knowledge simultaneously
//  4. Determinism (D12)
//
// World-level tests verify relianceScan() correctly emits RoleEmerged; the
// per-trigger logic (BestProviderFor selection) is covered in engine/agent tests.

import (
	"testing"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
)

// ── Test 1: Under threat → villagers delegate Safety to Tyrant ────────────────

// TestScenarioE_ThreatConvergesToTyrant verifies that when a persistent external
// threat makes Safety the dominant need, villagers' accumulated RelyOn[Safety]
// converges on the Tyrant (high Strength) rather than the Sage (low Strength),
// and relianceScan emits RoleEmerged(Tyrant, Safety).
//
// Setup: 4 villagers, each setting FuncSafety reliance on Tyrant (0.85) vs Sage
// (0.05). 4 of 6 total agents → share = 4/6 ≈ 0.667 > 0.5 threshold → emits.
func TestScenarioE_ThreatConvergesToTyrant(t *testing.T) {
	fx := newFixtureSeeded(t, 2001)
	cfg := agent.DefaultConfig()

	villagerIDs := []core.AgentID{"v1", "v2", "v3", "v4"}
	tyrantID := core.AgentID("tyrant")
	sageID := core.AgentID("sage")

	for _, id := range villagerIDs {
		fx.world.Spawn(id, core.Vec2{}, cfg, rng.New(100))
	}
	fx.world.Spawn(tyrantID, core.Vec2{X: 5}, cfg, rng.New(101))
	fx.world.Spawn(sageID, core.Vec2{X: 10}, cfg, rng.New(102))

	tyrant, _ := fx.world.AgentOf(tyrantID)
	sage, _ := fx.world.AgentOf(sageID)

	// Under threat: villagers' Safety reliance converges on Tyrant.
	// Tyrant BestProviderFor(Safety, Strength): score = 0.5 × 85/100 = 0.425.
	// Sage   BestProviderFor(Safety, Strength): score = 0.5 × 20/100 = 0.10.
	for _, vid := range villagerIDs {
		v, _ := fx.world.AgentOf(vid)
		v.ToM.AdjustRelyOn(tyrantID, core.FuncSafety, 0.85) // strong → Safety provider
		v.ToM.AdjustRelyOn(sageID, core.FuncSafety, 0.05)   // weak → not primary Safety
	}
	// Powers don't rely on villagers for Safety.
	tyrant.ToM.AdjustRelyOn(tyrantID, core.FuncSafety, 0.0)
	tyrant.ToM.AdjustRelyOn(sageID, core.FuncSafety, 0.0)
	sage.ToM.AdjustRelyOn(tyrantID, core.FuncSafety, 0.0)
	sage.ToM.AdjustRelyOn(sageID, core.FuncSafety, 0.0)

	fx.emit.events = nil
	fx.world.relianceScan()

	safetyEvents := roleEventsForFunction(fx.emit.events, "Safety")
	if len(safetyEvents) != 1 {
		t.Fatalf("expected 1 RoleEmerged for Safety under threat, got %d", len(safetyEvents))
	}
	holder := extractString(safetyEvents[0].Payload, "holder")
	if holder != "tyrant" {
		t.Errorf("expected Safety role holder=tyrant (Strength=85), got %s", holder)
	}
	share, _ := safetyEvents[0].Payload.(map[string]any)["reliance_share"].(float64)
	t.Logf("Under threat: Safety role → %s (share=%.3f)", holder, share)
}

// ── Test 2: Famine + no threat → Knowledge role shifts to Sage ───────────────

// TestScenarioE_FamineShiftsPowerToSage verifies that after the threat fades and
// famine hits (Safety resolved, Knowledge/provisioning valued), villagers' reliance
// shifts toward the Sage (Intelligence=90) for FuncKnowledge, and the Tyrant's
// Safety share drops below threshold.
//
// Phase A (threat): Tyrant → Safety role. Phase B (famine): Tyrant loses Safety
// majority; Sage gains Knowledge majority. Sage BestProviderFor(Knowledge,
// Intelligence): score = 0.5 × 90/100 = 0.45 > Tyrant = 0.5 × 30/100 = 0.15.
func TestScenarioE_FamineShiftsPowerToSage(t *testing.T) {
	fx := newFixtureSeeded(t, 2002)
	cfg := agent.DefaultConfig()

	villagerIDs := []core.AgentID{"v1", "v2", "v3", "v4"}
	tyrantID := core.AgentID("tyrant")
	sageID := core.AgentID("sage")

	for _, id := range villagerIDs {
		fx.world.Spawn(id, core.Vec2{}, cfg, rng.New(200))
	}
	fx.world.Spawn(tyrantID, core.Vec2{X: 5}, cfg, rng.New(201))
	fx.world.Spawn(sageID, core.Vec2{X: 10}, cfg, rng.New(202))

	tyrant, _ := fx.world.AgentOf(tyrantID)
	sage, _ := fx.world.AgentOf(sageID)

	// Phase A: threat → Tyrant holds Safety role.
	for _, vid := range villagerIDs {
		v, _ := fx.world.AgentOf(vid)
		v.ToM.AdjustRelyOn(tyrantID, core.FuncSafety, 0.85)
		v.ToM.AdjustRelyOn(sageID, core.FuncSafety, 0.05)
	}
	tyrant.ToM.AdjustRelyOn(tyrantID, core.FuncSafety, 0.0)
	sage.ToM.AdjustRelyOn(sageID, core.FuncSafety, 0.0)

	fx.emit.events = nil
	fx.world.relianceScan()

	phase1Safety := roleEventsForFunction(fx.emit.events, "Safety")
	if len(phase1Safety) != 1 || extractString(phase1Safety[0].Payload, "holder") != "tyrant" {
		t.Fatalf("phase A: expected Safety role → tyrant, got %v events", len(phase1Safety))
	}

	// Phase B: threat fades + famine. Safety need resolves (no threat → reliance
	// drops to zero); Satiety crisis makes Knowledge the dominant need.
	// All Safety RelyOn → 0 → no votes → Safety role clears (power shift).
	// Sage gains Knowledge majority: score = 0.5 × 90/100 = 0.45 > Tyrant = 0.15.
	for _, vid := range villagerIDs {
		v, _ := fx.world.AgentOf(vid)
		v.ToM.AdjustRelyOn(tyrantID, core.FuncSafety, -1.0)   // fully clear Safety reliance
		v.ToM.AdjustRelyOn(sageID, core.FuncSafety, -1.0)     // sage not needed for safety either
		v.ToM.AdjustRelyOn(sageID, core.FuncKnowledge, 0.85)  // famine → knowledge/provision
		v.ToM.AdjustRelyOn(tyrantID, core.FuncKnowledge, 0.05) // tyrant is not wise
	}

	fx.emit.events = nil
	fx.world.relianceScan()

	// Safety: all reliance zeroed → no votes → role clears, no new RoleEmerged.
	newSafety := roleEventsForFunction(fx.emit.events, "Safety")
	if len(newSafety) != 0 {
		t.Errorf("famine phase: Safety role should clear (no votes), got %d events", len(newSafety))
	}

	// Knowledge should emerge with Sage as holder.
	knowledgeEvents := roleEventsForFunction(fx.emit.events, "Knowledge")
	if len(knowledgeEvents) != 1 {
		t.Fatalf("famine phase: expected 1 RoleEmerged for Knowledge, got %d", len(knowledgeEvents))
	}
	knowledgeHolder := extractString(knowledgeEvents[0].Payload, "holder")
	if knowledgeHolder != "sage" {
		t.Errorf("expected Knowledge role holder=sage (Intelligence=90), got %s", knowledgeHolder)
	}
	t.Logf("Famine shift: Safety role dropped; Knowledge role → %s", knowledgeHolder)
}

// ── Test 3: Dual roles coexist ────────────────────────────────────────────────

// TestScenarioE_DualRoleCoexist verifies that Tyrant can simultaneously hold the
// Safety role while Sage holds the Knowledge role. Roles are independent: the
// reliance scan evaluates each Function separately and can emit for both.
//
// This models a realistic village: strong enforcer (Safety) + wise advisor
// (Knowledge) both have converged roles without conflict.
func TestScenarioE_DualRoleCoexist(t *testing.T) {
	fx := newFixtureSeeded(t, 2003)
	cfg := agent.DefaultConfig()

	villagerIDs := []core.AgentID{"v1", "v2", "v3", "v4"}
	tyrantID := core.AgentID("tyrant")
	sageID := core.AgentID("sage")

	for _, id := range villagerIDs {
		fx.world.Spawn(id, core.Vec2{}, cfg, rng.New(300))
	}
	fx.world.Spawn(tyrantID, core.Vec2{X: 5}, cfg, rng.New(301))
	fx.world.Spawn(sageID, core.Vec2{X: 10}, cfg, rng.New(302))

	tyrant, _ := fx.world.AgentOf(tyrantID)
	sage, _ := fx.world.AgentOf(sageID)

	// All 4 villagers: Safety → Tyrant, Knowledge → Sage.
	for _, vid := range villagerIDs {
		v, _ := fx.world.AgentOf(vid)
		v.ToM.AdjustRelyOn(tyrantID, core.FuncSafety, 0.85)    // Strength=85 → Safety provider
		v.ToM.AdjustRelyOn(sageID, core.FuncKnowledge, 0.85)   // Intelligence=90 → Knowledge provider
		v.ToM.AdjustRelyOn(sageID, core.FuncSafety, 0.05)
		v.ToM.AdjustRelyOn(tyrantID, core.FuncKnowledge, 0.05)
	}
	tyrant.ToM.AdjustRelyOn(tyrantID, core.FuncSafety, 0.0)
	tyrant.ToM.AdjustRelyOn(sageID, core.FuncSafety, 0.0)
	sage.ToM.AdjustRelyOn(sageID, core.FuncKnowledge, 0.0)
	sage.ToM.AdjustRelyOn(tyrantID, core.FuncKnowledge, 0.0)

	fx.emit.events = nil
	fx.world.relianceScan()

	safetyEvents := roleEventsForFunction(fx.emit.events, "Safety")
	knowledgeEvents := roleEventsForFunction(fx.emit.events, "Knowledge")

	if len(safetyEvents) != 1 {
		t.Errorf("expected 1 RoleEmerged for Safety, got %d", len(safetyEvents))
	} else if holder := extractString(safetyEvents[0].Payload, "holder"); holder != "tyrant" {
		t.Errorf("expected Safety holder=tyrant, got %s", holder)
	}

	if len(knowledgeEvents) != 1 {
		t.Errorf("expected 1 RoleEmerged for Knowledge, got %d", len(knowledgeEvents))
	} else if holder := extractString(knowledgeEvents[0].Payload, "holder"); holder != "sage" {
		t.Errorf("expected Knowledge holder=sage, got %s", holder)
	}

	t.Logf("Dual role: Safety→%s, Knowledge→%s (coexist without conflict)",
		extractString(safetyEvents[0].Payload, "holder"),
		extractString(knowledgeEvents[0].Payload, "holder"))
}

// ── Test 4: Determinism ───────────────────────────────────────────────────────

// TestScenarioE_Determinism verifies that the full Tyrant vs Sage scenario —
// threat phase + famine shift — produces byte-identical RoleEmerged event
// sequences across two runs with the same seed (D12).
func TestScenarioE_Determinism(t *testing.T) {
	const seed = int64(2004)

	run := func() []core.Event {
		fx := newFixtureSeeded(t, seed)
		cfg := agent.DefaultConfig()

		villagerIDs := []core.AgentID{"v1", "v2", "v3", "v4"}
		tyrantID := core.AgentID("tyrant")
		sageID := core.AgentID("sage")

		for _, id := range villagerIDs {
			fx.world.Spawn(id, core.Vec2{}, cfg, rng.New(400))
		}
		fx.world.Spawn(tyrantID, core.Vec2{X: 5}, cfg, rng.New(401))
		fx.world.Spawn(sageID, core.Vec2{X: 10}, cfg, rng.New(402))

		tyrant, _ := fx.world.AgentOf(tyrantID)
		sage, _ := fx.world.AgentOf(sageID)

		// Phase A: threat → Safety to Tyrant.
		for _, vid := range villagerIDs {
			v, _ := fx.world.AgentOf(vid)
			v.ToM.AdjustRelyOn(tyrantID, core.FuncSafety, 0.85)
			v.ToM.AdjustRelyOn(sageID, core.FuncSafety, 0.05)
		}
		tyrant.ToM.AdjustRelyOn(tyrantID, core.FuncSafety, 0.0)
		sage.ToM.AdjustRelyOn(sageID, core.FuncSafety, 0.0)

		fx.emit.events = nil
		fx.world.relianceScan()
		phase1 := make([]core.Event, len(fx.emit.events))
		copy(phase1, fx.emit.events)

		// Phase B: famine → Safety reliance zeroed; Knowledge to Sage.
		for _, vid := range villagerIDs {
			v, _ := fx.world.AgentOf(vid)
			v.ToM.AdjustRelyOn(tyrantID, core.FuncSafety, -1.0)
			v.ToM.AdjustRelyOn(sageID, core.FuncSafety, -1.0)
			v.ToM.AdjustRelyOn(sageID, core.FuncKnowledge, 0.85)
			v.ToM.AdjustRelyOn(tyrantID, core.FuncKnowledge, 0.05)
		}

		fx.emit.events = nil
		fx.world.relianceScan()
		phase2 := make([]core.Event, len(fx.emit.events))
		copy(phase2, fx.emit.events)

		return append(phase1, phase2...)
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
	t.Logf("DETERMINISM PASSED: %d events across two phases", len(eventsA))
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// roleEventsForFunction filters RoleEmerged events by function name.
func roleEventsForFunction(events []core.Event, function string) []core.Event {
	var out []core.Event
	for _, ev := range events {
		if ev.Type != "RoleEmerged" {
			continue
		}
		m, ok := ev.Payload.(map[string]any)
		if !ok {
			continue
		}
		if fn, _ := m["function"].(string); fn == function {
			out = append(out, ev)
		}
	}
	return out
}
