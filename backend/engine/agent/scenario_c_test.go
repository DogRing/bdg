package agent

// Scenario C — Deceptive Trade (agent-level unit test)
//
// Tests the fraud-detection layer in isolation from the world tick loop.
// Agent A has Honesty=30 (low); constructs a deceptive trade Signal where
// ClaimedValue (0.80) far exceeds the actual effect (0.20).
// Agent B detects the fraud via RecordFraud and updates ToM[A].Honesty.
// Agent C does not witness the trade; its ToM[A].Honesty is unchanged.
//
// Assertions (testing.md direction/existence criteria):
//   1. B's ToM[A].Honesty drops after fraud detection (direction < 0).
//   2. C's ToM[A].Honesty is UNCHANGED (non-witness isolation).
//
// NOTE: emitSignal / trade planning are P1 stubs. This test exercises the
// ToM update contract directly, documenting what P3 wiring will invoke.

import (
	"testing"

	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/rng"
	"github.com/dogring/bdg/engine/tom"
)

func TestScenarioC_AgentLevel_HonestyDrop(t *testing.T) {
	regs := makeTestRegs(t)

	rates := tom.DefaultRates()

	// B observes the world with moderate confidence.
	bRNG := rng.New(101)
	bStats := regs.stats.Defaults()
	bToM := tom.NewToM("agent-b", bStats, 0.6, bRNG, regs.stats, rates)

	// C is a non-witness agent.
	cRNG := rng.New(202)
	cStats := regs.stats.Defaults()
	cToM := tom.NewToM("agent-c", cStats, 0.5, cRNG, regs.stats, rates)

	// Seed initial beliefs so the midpoint estimate (≈50.0) is stable before the trade.
	bToM.Observe("agent-a", tom.StatEvidence{Stat: "Honesty", Weight: 1e-9})
	cToM.Observe("agent-a", tom.StatEvidence{Stat: "Honesty", Weight: 1e-9})

	bBefore, _ := bToM.Self("agent-a")
	cBefore, _ := cToM.Self("agent-a")
	bHonestyBefore := bBefore.EstStats["Honesty"].Mean
	cHonestyBefore := cBefore.EstStats["Honesty"].Mean

	// A's deceptive offer: ClaimedValue=0.80, actual effect=0.20.
	// diff = 0.60 > FraudThreshold(0.20) → RecordFraud fires.
	const claimedValue = 0.80
	const actualEffect = 0.20
	const tick = core.Tick(5)

	// B detects the fraud (witness).
	bToM.RecordFraud("agent-a", claimedValue, actualEffect, "Honesty", tick)
	// C does NOT detect it — not a witness.

	bAfter, _ := bToM.Self("agent-a")
	cAfter, _ := cToM.Self("agent-a")
	bHonestyAfter := bAfter.EstStats["Honesty"].Mean
	cHonestyAfter := cAfter.EstStats["Honesty"].Mean

	// 1. Direction: B's ToM[A].Honesty must drop.
	if bHonestyAfter >= bHonestyBefore {
		t.Errorf("B's ToM[A].Honesty should drop after fraud detection: before=%.4f after=%.4f",
			bHonestyBefore, bHonestyAfter)
	}
	t.Logf("GOLDEN B's ToM[A].Honesty: %.4f → %.4f (delta=%.4f)",
		bHonestyBefore, bHonestyAfter, bHonestyAfter-bHonestyBefore)

	// 2. Non-witness isolation: C's belief must be unchanged.
	if cHonestyAfter != cHonestyBefore {
		t.Errorf("C (non-witness) ToM[A].Honesty changed unexpectedly: before=%.4f after=%.4f",
			cHonestyBefore, cHonestyAfter)
	}
	t.Logf("GOLDEN C's ToM[A].Honesty: %.4f (unchanged, non-witness isolation)", cHonestyBefore)
}
