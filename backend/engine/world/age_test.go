package world

import (
	"testing"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
)

// PD4-ii/P_fa4c: the world advances Animal.Age by one fauna time-step (FaunaDT) each tick, so a lone
// animal's age equals ticks·FaunaDT. This is the clock the §6 `maturity` operand reads.
func TestAgeAdvancesEachTick(t *testing.T) {
	fx := newFixtureSeeded(t, 913)
	cfg := testEnvConfig() // FaunaDT = 1
	deer := testAnimal("an:deer", "deer", core.Vec2{X: 10, Y: 10})
	fx.world.InstallFauna(cfg, testFaunaRules(t), testScentEmitters(), nil, []fauna.Animal{deer})
	if got := fx.world.animals["an:deer"].Age; got != 0 {
		t.Fatalf("fresh animal should start at age 0, got %v", got)
	}
	const ticks = 25
	for range ticks {
		fx.world.Tick()
	}
	if got := fx.world.animals["an:deer"].Age; got != ticks*cfg.FaunaDT {
		t.Errorf("age after %d ticks = %v, want %v (ticks·FaunaDT)", ticks, got, ticks*cfg.FaunaDT)
	}
}
