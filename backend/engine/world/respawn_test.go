package world

import (
	"testing"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/mind/actions"
)

func TestRespawnTopsUpToTarget(t *testing.T) {
	fx := newFixtureSeeded(t, 900)
	installCombatActionRegistry(t, fx)
	rules := fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		"deer": {
			Utilities:   map[actions.ActionID]*expr.Program{"Forage": testNumProgram(t, "1")},
			Drives:      []fauna.DriveRule{{ID: "hunger", Rate: 0.001}},
			Speed:       testNumProgram(t, "0"),
			SmellRadius: 5, SightRadius: 5, FovArc: 3.14,
		},
	})
	cfg := testEnvConfig()
	cfg.ScentCellSize = 5
	cfg.ScentSpread = 1
	cfg.FaunaDT = 1
	// install 2 deer
	fx.world.InstallFauna(cfg, rules, testScentEmitters(), nil, []fauna.Animal{
		testAnimal("an:deer1", "deer", core.Vec2{X: 10, Y: 10}),
		testAnimal("an:deer2", "deer", core.Vec2{X: 12, Y: 10}),
	})
	tpl := testAnimal("", "deer", core.Vec2{})
	tpl.Stats = map[core.StatID]float64{"Agility": 50}
	tpl.Drives = map[fauna.DriveID]float64{"hunger": 0.2}
	tpl.Vital = 1
	fx.world.InstallRespawn(
		map[core.Tag]fauna.Animal{"deer": tpl},
		map[core.Tag]int{"deer": 6},
		map[core.Tag]core.Vec2{"deer": {X: 10, Y: 10}},
		10, // cadence
	)

	count := func() int {
		n := 0
		for _, a := range fx.world.Animals() {
			if a.Species == "deer" {
				n++
			}
		}
		return n
	}
	if count() != 2 {
		t.Fatalf("start deer = %d, want 2", count())
	}
	// tick to the first respawn cadence
	for range 10 {
		fx.world.Tick()
	}
	if got := count(); got != 6 {
		t.Fatalf("after respawn cadence deer = %d, want 6 (topped up to target)", got)
	}
	// remove one, tick again → tops back up
	for _, a := range fx.world.Animals() {
		if a.Species == "deer" {
			fx.world.removeAnimal(a.ID)
			break
		}
	}
	if count() != 5 {
		t.Fatalf("after manual remove deer = %d, want 5", count())
	}
	for range 10 {
		fx.world.Tick()
	}
	if got := count(); got != 6 {
		t.Fatalf("respawn did not top back up: deer = %d, want 6", got)
	}
}
