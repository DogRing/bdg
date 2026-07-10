package world

// Scenario A — Hungry Hunter
//
// A single agent with high Agility starts starving (Satiety=0.8, threshold=0.55).
// A berry bush is visible nearby. The agent must autonomously plan Forage→Eat,
// execute both actions, and end with lower Satiety intensity than it started.
//
// Assertions:
//   1. Satiety intensity DECREASES within 25 ticks (direction/existence).
//   2. Two runs with the same seed produce byte-identical state (D12 determinism).
//
// Uses the same fixture as world_test.go (effort:med Forage, direct-effect Eat).
// Hunter Agility is pinned to 80 post-spawn: difficulty=75 (effort:med) → succeeds.

import (
	"testing"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
)

func TestScenarioA_HungryHunter(t *testing.T) {
	const seed = int64(42)
	const ticks = 25

	run := func() (*World, *agent.Agent) {
		fx := newFixtureSeeded(t, seed)

		cfg := agent.DefaultConfig()
		hunter := fx.world.Spawn("hunter", core.Vec2{X: 0, Y: 0}, cfg, rng.New(seed))

		// Pin Agility to guarantee Forage succeeds.
		// effort:med → difficulty = OutcomeDifficultyBase(50) × 1.5 = 75; 80 ≥ 75 ✓
		hunter.RealStats["Agility"] = 80

		// Start starving: intensity well above the 0.55 threshold.
		hunter.NeedIntensities["Satiety"] = 0.8

		// Berry bush in line-of-sight (sight radius = 18 world units).
		fx.world.PlaceObject("berry_bush_0", "berry_bush",
			core.Vec2{X: 3, Y: 0},
			map[core.Dimension]float64{"Satiety": 0.5})

		for range ticks {
			fx.world.Tick()
		}
		return fx.world, hunter
	}

	worldA, hunterA := run()
	worldB, _ := run()

	// 1. Direction assertion: Satiety must have DECREASED (agent ate).
	const initialSatiety = 0.8
	finalSatiety := hunterA.NeedIntensities["Satiety"]
	if finalSatiety >= initialSatiety {
		t.Errorf("Satiety did not decrease: initial=%.4f final=%.4f (Forage+Eat may have failed)",
			initialSatiety, finalSatiety)
	}
	t.Logf("Satiety: initial=%.4f → final=%.4f (delta=%.4f)",
		initialSatiety, finalSatiety, initialSatiety-finalSatiety)

	// 2. Determinism: identical seed → byte-identical state digest (D12).
	assertWorldDigestsEqual(t, "Scenario A", worldA, worldB)
}
