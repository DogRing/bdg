package world

import (
	"math"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/env/flora"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/mind/actions"
)

// grazeDepletionRules is a deer that always Grazes (seek:food), with a fixed Graze §6 recovery so the PD2
// depletion arithmetic is exact.
func grazeDepletionRules(t *testing.T) *fauna.Rules {
	t.Helper()
	return fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		"deer": {
			Utilities:    map[actions.ActionID]*expr.Program{"Graze": testNumProgram(t, "1")},
			Drives:       []fauna.DriveRule{{ID: "hunger", Rate: 0}},
			Speed:        testNumProgram(t, "0"),
			Graze:        testNumProgram(t, "0.1"),
			SteerChannel: map[actions.ActionID]core.Tag{"Graze": fauna.TagSteerFood},
			SmellRadius:  5, SightRadius: 5, FovArc: 3.14,
		},
	})
}

// PD2 flora depletion (P_fa4b): applyAnimalGraze crops the grazed flora PLANT's Length by mult·k and
// recovers hunger by removed/k. This white-box test drives applyAnimalGraze directly (bypassing the action
// arbitration) against a floraState plant to pin the depletion + availability-scaled recovery + off-lever.
func TestGrazeDepletesFloraBiomass(t *testing.T) {
	setup := func(k, plantLen float64) (*World, *fauna.Animal) {
		fx := newFixtureSeeded(t, 861)
		cfg := testEnvConfig()
		cfg.ScentCellSize = 5
		cfg.FaunaCombat.GrazeDepletion = k
		deer := testAnimal("an:deer", "deer", core.Vec2{X: 1, Y: 1})
		deer.Drives = map[fauna.DriveID]float64{"hunger": 0.8}
		fx.world.InstallFauna(cfg, grazeDepletionRules(t), testScentEmitters(), nil, []fauna.Animal{deer})
		fx.world.PlaceObject("grass_1", "grass", core.Vec2{X: 2, Y: 1}, nil) // food emitter within one scent cell
		fx.world.floraState = flora.New([]flora.Plant{
			{ID: "grass_1", Species: "grass", Pos: core.Vec2{X: 2, Y: 1}, Length: plantLen, Width: 2},
		})
		return fx.world, fx.world.animals["an:deer"]
	}

	// Healthy plant, k=2: one graze crops Length by mult·k=0.2 and recovers the FULL mult=0.1 (removed/k).
	w, deer := setup(2.0, 5.0)
	w.applyAnimalGraze(deer)
	if p, _ := w.floraState.PlantByID("grass_1"); math.Abs(p.Length-4.8) > 1e-9 {
		t.Errorf("graze should crop Length by mult·k=0.2 (5.0→4.8): got %.6f", p.Length)
	}
	if math.Abs(deer.Drives["hunger"]-0.7) > 1e-9 {
		t.Errorf("healthy-plant graze should recover full mult=0.1 (0.8→0.7): got %.6f", deer.Drives["hunger"])
	}

	// Nearly-depleted plant (Length 0.1 < mult·k 0.2): crops to 0 and recovers only removed/k=0.05 — the
	// availability scaling that turns overgrazing into local famine.
	wLow, deerLow := setup(2.0, 0.1)
	wLow.applyAnimalGraze(deerLow)
	if p, _ := wLow.floraState.PlantByID("grass_1"); p.Length != 0 {
		t.Errorf("grazing an almost-empty plant crops it to 0: got %.6f", p.Length)
	}
	if math.Abs(deerLow.Drives["hunger"]-0.75) > 1e-9 {
		t.Errorf("depleted-plant graze should give PARTIAL recovery removed/k=0.05 (0.8→0.75): got %.6f", deerLow.Drives["hunger"])
	}

	// Off-lever k=0: hunger still recovers the full §6 value, but the plant is NOT depleted (byte-identical
	// to pre-P_fa4b grazing).
	w0, deer0 := setup(0, 5.0)
	w0.applyAnimalGraze(deer0)
	if p, _ := w0.floraState.PlantByID("grass_1"); p.Length != 5.0 {
		t.Errorf("off-lever (k=0) must not deplete flora: Length got %.6f, want 5.0", p.Length)
	}
	if math.Abs(deer0.Drives["hunger"]-0.7) > 1e-9 {
		t.Errorf("off-lever graze should still recover mult=0.1: hunger got %.6f", deer0.Drives["hunger"])
	}
}

// PD3 starvation (P_fa4b): an animal that cannot find food keeps hunger saturated, so nextVital bleeds its
// Vital to 0 and the world removes it with cause="starvation" (a combat kill would be labelled predation
// inside applyAnimalAttack). This is the end-to-end death path through Tick().
func TestStarvationKillsAndLabelsStarvation(t *testing.T) {
	fx := newFixtureSeeded(t, 862)
	reg, err := actions.Load(strings.NewReader("schema_version: 1\nactions:\n  - id: Graze\n    tags: [uses:Agility, seek:food]\n    duration: 10\n"))
	if err != nil {
		t.Fatal(err)
	}
	fx.world.actReg = reg
	fx.svc.Actions = reg
	fx.world.svc.Actions = reg

	rules := fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		"deer": {
			Utilities:    map[actions.ActionID]*expr.Program{"Graze": testNumProgram(t, "1")},
			Drives:       []fauna.DriveRule{{ID: "hunger", Rate: 0.05, VitalDrain: 0.05, VitalDrainAbove: 0.98}},
			Speed:        testNumProgram(t, "0"),
			Graze:        testNumProgram(t, "0.1"),
			SteerChannel: map[actions.ActionID]core.Tag{"Graze": fauna.TagSteerFood},
			SmellRadius:  5, SightRadius: 5, FovArc: 3.14,
		},
	})
	cfg := testEnvConfig()
	cfg.FaunaCombat.VitalRegenPerTick = 0.001
	deer := testAnimal("an:deer", "deer", core.Vec2{X: 10, Y: 10})
	deer.Drives = map[fauna.DriveID]float64{"hunger": 0.99} // already saturated (≥ θ)
	deer.Vital = 0.1
	fx.world.InstallFauna(cfg, rules, testScentEmitters(), nil, []fauna.Animal{deer})
	// NO food object anywhere ⇒ hunger only rises (clamped at 1.0) ⇒ Vital bleeds to 0.

	for range 100 {
		fx.world.Tick()
		if _, alive := fx.world.animals["an:deer"]; !alive {
			break
		}
	}
	if a, alive := fx.world.animals["an:deer"]; alive {
		t.Fatalf("a starving deer (no food, saturated hunger) should die within 100 ticks; Vital=%.4f", a.Vital)
	}

	found := false
	for i := range fx.emit.events {
		e := fx.emit.events[i]
		if e.Type != "AnimalDied" {
			continue
		}
		p, _ := e.Payload.(map[string]any)
		if p["object_id"] == "an:deer" && p["cause"] == "starvation" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an AnimalDied{object_id:an:deer, cause:starvation} event")
	}
}
