package world

// Scenario C — Deceptive Trade
//
// Agent A has surplus meat and low Honesty (30/100). In a trade offer to B,
// A inflates ClaimedValue (0.80) but the actual supply effect is only 0.20
// (fraud; difference 0.60 exceeds FraudThreshold=0.20).
// Agent C is 1000 units away — not a witness.
//
// Assertions (testing.md direction/existence criteria):
//   1. After B detects the fraud, B's ToM[A].Honesty drops (direction < 0).
//   2. C's ToM[A].Honesty is UNCHANGED (non-witness isolation).
//   3. Two runs with the same seed produce byte-identical world state (D12).
//
// NOTE: emitSignal is a P1 stub; fraud detection is applied manually via
// RecordFraud, documenting the contract that P3 wiring will automate.

import (
	"testing"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/tom"
)

func TestScenarioC_DeceptiveTrade(t *testing.T) {
	const seed = int64(99)
	const ticks = 10

	// A claims meat worth 0.80; actual supply is 0.20.
	// claimedValue − actualEffect = 0.60 > FraudThreshold(0.20) → fraud fires.
	const claimedValue = 0.80
	const actualEffect = 0.20

	type result struct {
		bHonestyBefore float64
		bHonestyAfter  float64
		cHonestyBefore float64
		cHonestyAfter  float64
	}

	run := func() (*World, result) {
		fx := newFixtureSeeded(t, seed)

		agentA := fx.world.Spawn("agent-a", core.Vec2{X: 0, Y: 0}, agent.DefaultConfig(), rng.New(seed))
		agentA.RealStats["Honesty"] = 30.0 // low honesty

		agentB := fx.world.Spawn("agent-b", core.Vec2{X: 1, Y: 0}, agent.DefaultConfig(), rng.New(seed+1))
		agentB.RealStats["Intelligence"] = 60.0

		agentC := fx.world.Spawn("agent-c", core.Vec2{X: 1000, Y: 1000}, agent.DefaultConfig(), rng.New(seed+2))

		// Seed beliefs so the initial midpoint estimate (≈50.0) exists before any trade.
		agentB.ToM.Observe("agent-a", tom.StatEvidence{Stat: "Honesty", Weight: 1e-9})
		agentC.ToM.Observe("agent-a", tom.StatEvidence{Stat: "Honesty", Weight: 1e-9})

		bBeforeBelief, _ := agentB.ToM.Self("agent-a")
		cBeforeBelief, _ := agentC.ToM.Self("agent-a")
		bHonestyBefore := bBeforeBelief.EstStats["Honesty"].Mean
		cHonestyBefore := cBeforeBelief.EstStats["Honesty"].Mean

		for range ticks {
			fx.world.Tick()
		}

		// Simulate fraud detection: B discovers A's deception post-trade.
		agentB.ToM.RecordFraud("agent-a", claimedValue, actualEffect, "Honesty", core.Tick(ticks))

		bAfterBelief, _ := agentB.ToM.Self("agent-a")
		cAfterBelief, _ := agentC.ToM.Self("agent-a")

		return fx.world, result{
			bHonestyBefore: bHonestyBefore,
			bHonestyAfter:  bAfterBelief.EstStats["Honesty"].Mean,
			cHonestyBefore: cHonestyBefore,
			cHonestyAfter:  cAfterBelief.EstStats["Honesty"].Mean,
		}
	}

	worldA, res := run()
	worldB, _ := run()

	// 1. Direction assertion: B's ToM[A].Honesty must have dropped.
	if res.bHonestyAfter >= res.bHonestyBefore {
		t.Errorf("B's ToM[A].Honesty should drop after fraud detection: before=%.4f after=%.4f",
			res.bHonestyBefore, res.bHonestyAfter)
	}
	t.Logf("GOLDEN B's ToM[A].Honesty: %.4f → %.4f (delta=%.4f)",
		res.bHonestyBefore, res.bHonestyAfter, res.bHonestyAfter-res.bHonestyBefore)

	// 2. Non-witness isolation: C's belief about A is unchanged.
	if res.cHonestyAfter != res.cHonestyBefore {
		t.Errorf("C (non-witness) ToM[A].Honesty should be unchanged: before=%.4f after=%.4f",
			res.cHonestyBefore, res.cHonestyAfter)
	}
	t.Logf("GOLDEN C's ToM[A].Honesty: %.4f (unchanged, non-witness isolation)", res.cHonestyBefore)

	// 3. Determinism: identical seed → byte-identical world state (D12).
	assertWorldDigestsEqual(t, "Scenario C", worldA, worldB)
}
