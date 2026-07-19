package world

import (
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/env/flora"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/space/scent"
	"github.com/dogring/bdg/engine/mind/actions"
)

// grazeExhaustionFixture builds a world with two food plants in reach of one hungry herbivore:
// `bare` (cropped to nothing) sits closer, `lush` (full biomass) sits further out but still inside
// the one-scent-cell graze reach.
func grazeExhaustionFixture(t *testing.T, seed int64, bareLen, bareWidth float64, lushPos core.Vec2) *testFixture {
	t.Helper()
	fx := newFixtureSeeded(t, seed)
	reg, err := actions.Load(strings.NewReader(
		"schema_version: 1\nactions:\n  - id: Graze\n    tags: [uses:Agility, seek:food]\n    duration: 10\n    produces: [grazed]\n"))
	if err != nil {
		t.Fatal(err)
	}
	fx.world.actReg = reg
	fx.svc.Actions = reg
	fx.world.svc.Actions = reg

	rules := fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		"deer": {
			Utilities:    map[actions.ActionID]*expr.Program{"Graze": testNumProgram(t, "1")},
			// Rate must stay > 0: a DriveRule with rate 0 and no wary/flee level is the THERMAL
			// shape, which DriveUpdate SETs from the comfort band (i.e. wipes hunger every tick).
			Drives:       []fauna.DriveRule{{ID: "hunger", Rate: 0.0001}},
			Speed:        testNumProgram(t, "0"), // pinned: isolates the food CHOICE from movement
			Graze:        testNumProgram(t, "0.1"),
			SteerChannel: map[actions.ActionID]core.Tag{"Graze": fauna.TagSteerFood},
			SmellRadius:  5, SightRadius: 5, FovArc: 3.14,
		},
	})
	floraRules := map[flora.SpeciesID]flora.SpeciesRule{
		"grass": {
			Suitability:    testNumProgram(t, "1"),
			LengthRate:     testNumProgram(t, "0"), // no regrowth: the bare plant STAYS bare
			WidthRate:      testNumProgram(t, "0"),
			ShadeRadius:    testNumProgram(t, "0"),
			ShadeOpacity:   testNumProgram(t, "0"),
			Stages:         []float64{1},
			PropagateStage: 99,
			PropRadius:     testNumProgram(t, "0"),
			PropChance:     testNumProgram(t, "0"),
		},
	}
	state := flora.New([]flora.Plant{
		{ID: "bare", Species: "grass", Pos: core.Vec2{X: 2, Y: 1}, Length: bareLen, Width: bareWidth},
		{ID: "lush", Species: "grass", Pos: lushPos, Length: 8, Width: 1},
	})
	fx.world.PlaceObject("bare", "grass", core.Vec2{X: 2, Y: 1}, nil)
	fx.world.PlaceObject("lush", "grass", lushPos, nil)

	cfg := testEnvConfig()
	cfg.ScentCellSize = 5
	cfg.ScentSpread = 1
	cfg.FaunaDT = 1
	cfg.FaunaCombat.GrazeDepletion = 2

	deer := testAnimal("an:deer", "deer", core.Vec2{X: 1, Y: 1})
	deer.Drives = map[fauna.DriveID]float64{"hunger": 0.9}
	fx.world.InstallEnv(cfg, testNavMap(), nil, nil, state, flora.NewRules(floraRules), nil, nil)
	fx.world.InstallFauna(cfg, rules, testScentEmitters(), nil, []fauna.Animal{deer})
	return fx
}

// TestGrazePassesOverExhaustedPlant is the regression for the herbivore feeding trap: once an animal
// crops its nearest plant to zero biomass, that plant stays the NEAREST food emitter forever, so a
// "nearest food-emitting plant" lookup keeps re-picking it and every subsequent Graze silently no-ops
// (GrazeLength caps removal at the remaining Length). Measured on the live meadow before the fix, the
// nearest food plant was an exhausted one for 95–99% of samples across rabbit/deer/goat/fish, while a
// plant WITH biomass sat inside the same reach ~80% of the time — i.e. animals starved on top of food.
//
// An exhausted plant is not food: the lookup must skip it and take the next plant in reach.
func TestGrazePassesOverExhaustedPlant(t *testing.T) {
	// bare: no Length, but Width (and so scent) remains; lush sits further out, still inside reach.
	fx := grazeExhaustionFixture(t, 901, 0, 1, core.Vec2{X: 4, Y: 1})
	const start = 0.9
	for range 20 {
		fx.world.Tick()
	}
	if got := fx.world.animals["an:deer"].Drives["hunger"]; got >= start {
		t.Fatalf("hunger never dropped: the animal re-picked the exhausted plant instead of the lush one in reach (%.3f)", got)
	}
	if p, _ := fx.world.floraState.PlantByID("lush"); p.Length >= 8 {
		t.Fatalf("the lush plant was never cropped (Length %.3f) — the exhausted plant is still winning the lookup", p.Length)
	}
}

// TestFloraScentTracksBiomass pins the SPEC's depletion feedback loop (SPEC-world-fauna, Graze/PD2):
// "overgrazing shrinks plants → weaker food scent (mag = Length+Width) → herbivores can't home →
// migration pressure". That loop only exists if a plant's food scent actually falls with its biomass.
//
// Flora plants live in BOTH floraState and w.objects (env.go PlaceObject on spawn), so a per-object
// scent deposit adds a flat magnitude on top of the biomass-scaled one — which would peg an eaten-bare
// plant's scent at a constant and silently delete the feedback the SPEC describes.
func TestFloraScentTracksBiomass(t *testing.T) {
	// The lush plant is parked far outside the scent grid's reach so the reading at `bare` is
	// exactly one plant's contribution (a neighbour in the same cell would mask the difference).
	at := func(bareLen, bareWidth float64) float64 {
		fx := grazeExhaustionFixture(t, 902, bareLen, bareWidth, core.Vec2{X: 500, Y: 500})
		fx.world.Tick()
		return fx.world.ScentIntensityAt(scent.ChanFood, core.Vec2{X: 2, Y: 1})
	}
	full, empty := at(6, 1), at(0, 0)
	if empty >= full {
		t.Fatalf("food scent does not fall with biomass: bare plant %.3f vs full plant %.3f", empty, full)
	}
	if empty != 0 {
		t.Errorf("a plant with no biomass still emits food scent (%.3f) — a constant deposit is masking depletion", empty)
	}
}
