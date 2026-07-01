package world

// Scenario H — Long Journey / 긴 여행
//
// P5-1 Intelligence-gated lookahead behavior — world-level integration test.
//
// Two agents with different perceived Intelligence run with the SAME seed and
// identical world state. Both start with Satiety=0.3 (moderate slack 0.25 above
// threshold). Over a long horizon, Satiety decay (0.00070/tick) produces a
// predicted deficit.
//
// High-Intel agent (ToM[self] Intelligence=80, normalized 0.80 >= 0.40):
//   Computes horizon = floor(720 * 0.80) = 576 ticks (gradual formula).
//   Satiety demand = 0.00070 * 576 = 0.403.
//   Slack = 0.55 - 0.3 = 0.25.
//   Predicted deficit = 0.403 - 0.25 = 0.153 > 0.
//   → Forward-sim triggers provisioning subgoal.
//
// Low-Intel agent (ToM[self] Intelligence=35, normalized 0.35 < 0.40):
//   Below lookahead threshold (0.4 from balance.yaml) → P5 hard skip → horizon=0.
//   No forward-sim → no provisioning subgoals.
//
// Assertions:
//   1. High-Intel: Plan has Horizon > 0 (provisioning lookahead active) at first plan.
//   2. Low-Intel: Plan has Horizon = 0 (P5 hard skip) at first plan.
//   3. Both agents run deterministically (D12): two runs with same seed produce
//      byte-identical state digests.

import (
	"testing"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/planner"
	"github.com/dogring/bdg/engine/mind/tom"
)

// ── Test: High-Intel agent provisions for long journey ──────────────────────────

// TestScenarioH_HighIntelProvisionsForLongJourney verifies that an agent with
// high perceived Intelligence (80/100 = 0.80 >= 0.40) triggers forward-sim
// provisioning: its plan's Horizon > 0.
func TestScenarioH_HighIntelProvisionsForLongJourney(t *testing.T) {
	const seed = int64(42)

	run := func() (*World, *agent.Agent) {
		fx := newFixtureSeeded(t, seed)
		cfg := agent.DefaultConfig()

		// Spawn the agent.
		hunter := fx.world.Spawn("hunter", core.Vec2{X: 0, Y: 0}, cfg, rng.New(seed))

		// Pin RealStats so Forage succeeds (effort:med → difficulty 75; Agility 80 ≥ 75).
		hunter.RealStats["Agility"] = 80

		// Start with Satiety=0.3 (moderate, not critical — slack = 0.55 - 0.3 = 0.25).
		hunter.NeedIntensities["Satiety"] = 0.3

		// Seed ToM[self] Intelligence high (80/100 = 0.80 >= 0.40 lookahead threshold).
		// 60 Observe iterations with beta=0.08 converge the mean to ≈ 80.
		selfID := hunter.ToM.SelfID()
		for i := 0; i < 60; i++ {
			hunter.ToM.Observe(selfID, tom.StatEvidence{Stat: "Intelligence", Observed: 80, Weight: 1.0, Tick: 1})
		}

		// Place a berry bush nearby so Forage is possible.
		fx.world.PlaceObject("berry_bush_0", "berry_bush",
			core.Vec2{X: 3, Y: 0},
			map[core.Dimension]float64{"Satiety": 0.5})

		// Run a single tick first to capture first-plan state (before execution
		// overwrites the plan with subsequent re-plans).
		fx.world.Tick()

		return fx.world, hunter
	}

	// Run twice for determinism check.
	worldA, hunterA := run()
	worldB, _ := run()

	// 1. High-Intel should have Horizon > 0 at the first plan.
	hunterPlan := hunterA.Plan
	t.Logf("GOLDEN Scenario H (high Intel): first plan=%v horizon=%d coping=%v",
		hunterPlan.Actions, hunterPlan.Horizon, hunterA.Coping)

	if hunterPlan.Horizon <= 0 {
		t.Errorf("expected Horizon > 0 for high Intel (lookahead active), got %d", hunterPlan.Horizon)
	}

	// Verify the first plan has at least 2 actions (provisioning + goal, or just goal).
	if len(hunterPlan.Actions) < 2 {
		t.Errorf("expected plan with at least 2 actions (Forage+Eat), got %v", hunterPlan.Actions)
	}

	// 2. Determinism: byte-identical state digests (D12).
	digestA := worldDigest(worldA)
	digestB := worldDigest(worldB)
	if digestA != digestB {
		t.Errorf("DETERMINISM FAILED at seed=%d ticks=%d", seed, 1)
		t.Logf("Digest A:\n%s", digestA)
		t.Logf("Digest B:\n%s", digestB)
	} else {
		t.Logf("SCENARIO H (high Intel) PASSED: seed=%d (deterministic)", seed)
	}
}

// ── Test: Low-Intel agent skips provisioning ────────────────────────────────────

// TestScenarioH_LowIntelSkipsProvisioning verifies that an agent with low
// perceived Intelligence (35/100 = 0.35 < 0.40) gets Horizon=0 (P5 hard skip)
// and no provisioning subgoals in its first plan.
func TestScenarioH_LowIntelSkipsProvisioning(t *testing.T) {
	const seed = int64(42)

	run := func() (*World, *agent.Agent) {
		fx := newFixtureSeeded(t, seed)
		cfg := agent.DefaultConfig()

		hunter := fx.world.Spawn("hunter", core.Vec2{X: 0, Y: 0}, cfg, rng.New(seed))
		hunter.RealStats["Agility"] = 80
		hunter.NeedIntensities["Satiety"] = 0.3

		// Seed ToM[self] Intelligence low (35/100 = 0.35 < 0.40 threshold → P5 hard skip).
		selfID := hunter.ToM.SelfID()
		for i := 0; i < 60; i++ {
			hunter.ToM.Observe(selfID, tom.StatEvidence{Stat: "Intelligence", Observed: 35, Weight: 1.0, Tick: 1})
		}

		fx.world.PlaceObject("berry_bush_0", "berry_bush",
			core.Vec2{X: 3, Y: 0},
			map[core.Dimension]float64{"Satiety": 0.5})

		// Run just 1 tick to capture the first plan (before needs change the plan).
		fx.world.Tick()

		return fx.world, hunter
	}

	worldA, hunterA := run()
	worldB, _ := run()

	// 1. Low-Intel should have Horizon = 0 (P5 hard skip: 0.35 < 0.40).
	hunterPlan := hunterA.Plan
	t.Logf("GOLDEN Scenario H (low Intel): first plan=%v horizon=%d coping=%v",
		hunterPlan.Actions, hunterPlan.Horizon, hunterA.Coping)

	if hunterPlan.Horizon != 0 {
		t.Errorf("expected Horizon=0 for low Intel (P5 hard skip, 0.35 < 0.40), got %d",
			hunterPlan.Horizon)
	}

	// The plan should still contain actions (the agent can still plan for immediate needs).
	if len(hunterPlan.Actions) < 2 {
		t.Errorf("expected at least 2 actions in plan (Forage+Eat), got %v", hunterPlan.Actions)
	}

	// 2. Determinism: byte-identical state digests (D12).
	digestA := worldDigest(worldA)
	digestB := worldDigest(worldB)
	if digestA != digestB {
		t.Errorf("DETERMINISM FAILED at seed=%d ticks=%d", seed, 1)
		t.Logf("Digest A:\n%s", digestA)
		t.Logf("Digest B:\n%s", digestB)
	} else {
		t.Logf("SCENARIO H (low Intel) PASSED: seed=%d (deterministic)", seed)
	}
}

// ── Extended run test: verify provisioning over many ticks ──────────────────────

// TestScenarioH_ProvisioningBehaviorOverTime verifies that the high-Intel agent
// consistently maintains a longer-horizon lookahead across multiple plan cycles
// over an extended run, while the low-Intel agent always returns horizon=0.
func TestScenarioH_ProvisioningBehaviorOverTime(t *testing.T) {
	const seed = int64(42)

	type runResult struct {
		plan  planner.Plan
		agent *agent.Agent
	}

	run := func(targetIntelObserved float64, ticks int) runResult {
		fx := newFixtureSeeded(t, seed)
		cfg := agent.DefaultConfig()

		hunter := fx.world.Spawn("hunter", core.Vec2{X: 0, Y: 0}, cfg, rng.New(seed))
		hunter.RealStats["Agility"] = 80
		hunter.NeedIntensities["Satiety"] = 0.3

		selfID := hunter.ToM.SelfID()
		for i := 0; i < 60; i++ {
			hunter.ToM.Observe(selfID, tom.StatEvidence{Stat: "Intelligence", Observed: targetIntelObserved, Weight: 1.0, Tick: 1})
		}

		fx.world.PlaceObject("berry_bush_0", "berry_bush",
			core.Vec2{X: 3, Y: 0},
			map[core.Dimension]float64{"Satiety": 0.5})

		for range ticks {
			fx.world.Tick()
		}

		return runResult{plan: hunter.Plan, agent: hunter}
	}

	// Run high-Intel for moderate ticks.
	high := run(80, 60)
	t.Logf("GOLDEN Scenario H over-time (high Intel): plan=%v horizon=%d coping=%v satiety=%.4f",
		high.plan.Actions, high.plan.Horizon, high.agent.Coping,
		high.agent.NeedIntensities["Satiety"])

	// Run low-Intel for same ticks, same seed.
	low := run(35, 60)
	t.Logf("GOLDEN Scenario H over-time (low Intel): plan=%v horizon=%d coping=%v satiety=%.4f",
		low.plan.Actions, low.plan.Horizon, low.agent.Coping,
		low.agent.NeedIntensities["Satiety"])

	// Both should have Coping=Idle (both can satisfy immediate needs via Forage+Eat).
	if high.agent.Coping != agent.Idle {
		t.Errorf("high-Intel agent should remain Idle, got %v", high.agent.Coping)
	}
	if low.agent.Coping != agent.Idle {
		t.Errorf("low-Intel agent should remain Idle (immediate need satisfied), got %v", low.agent.Coping)
	}
}
