package world

import (
	"testing"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/mind/actions"
)

func TestRescueOnlyOnExtinction(t *testing.T) {
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
	// A surviving population is LEFT ALONE, however far below the number it once held. Under the old
	// thermostat this topped straight back up to 6 every cadence, which made population size a
	// property of the knob rather than of the ecosystem: births, starvation and predation were all
	// cosmetic above the target (PD5 / P_fa4c-3).
	for range 30 {
		fx.world.Tick()
	}
	if got := count(); got != 2 {
		t.Fatalf("rescue fired on a LIVING population: deer = %d, want the 2 survivors left alone", got)
	}

	// Extinction — and only extinction — brings immigrants, and they arrive as a founder GROUP.
	// Breeding is 2-parent (P_fa4c-2), so re-introducing a lone animal would just replay the
	// extinction with extra steps.
	for _, a := range fx.world.Animals() {
		if a.Species == "deer" {
			fx.world.removeAnimal(a.ID)
		}
	}
	if count() != 0 {
		t.Fatalf("setup: deer are not extinct")
	}
	for range 10 {
		fx.world.Tick()
	}
	got := count()
	if got != 6 {
		t.Fatalf("extinct species was not re-introduced: deer = %d, want its 6 founders", got)
	}
	if got < 2 {
		t.Fatalf("a founder group of %d cannot breed at all (2-parent mating)", got)
	}
}
