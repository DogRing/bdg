package world

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/env/climate"
	"github.com/dogring/bdg/engine/env/decay"
	"github.com/dogring/bdg/engine/env/flora"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/space/navmap"
	"github.com/dogring/bdg/engine/space/scent"
)

func testFaunaRules(t *testing.T) *fauna.Rules {
	t.Helper()
	return fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		"deer": {
			Utilities: map[actions.ActionID]*expr.Program{
				"Forage": testNumProgram(t, "1"),
				"Eat":    testNumProgram(t, "0"),
			},
			Drives: []fauna.DriveRule{{ID: "hunger"}},
			Speed:  testNumProgram(t, "0"),
			TerrainCost: map[core.Tag]float64{
				"water": 3,
			},
			SmellRadius: 5,
			SightRadius: 5,
			FovArc:      3.14,
		},
		"wolf": {
			Utilities:   map[actions.ActionID]*expr.Program{"Forage": testNumProgram(t, "1")},
			Drives:      []fauna.DriveRule{{ID: "hunger"}},
			Speed:       testNumProgram(t, "0"),
			IsPredator:  true,
			SmellRadius: 5,
			SightRadius: 5,
			FovArc:      3.14,
		},
	})
}

func testAnimal(id core.ObjectID, species fauna.SpeciesID, pos core.Vec2) fauna.Animal {
	return fauna.Animal{
		ID:            id,
		Species:       species,
		Pos:           pos,
		Stats:         map[core.StatID]float64{"Agility": 80},
		Drives:        map[fauna.DriveID]float64{"hunger": 0.2, "Satiety": 0.1},
		Stamina:       1,
		Vital:         1,
		CurrentAction: "Forage",
	}
}

func testScentEmitters() map[core.Tag][]core.Tag {
	return map[core.Tag][]core.Tag{
		"deer":       {"scent:prey"},
		"carcass":    {"scent:carrion"},
		"grass":      {"scent:food"},
		"future":     {"scent:carrion"},
		"wolf":       {"scent:predator"},
		"wildflower": nil,
	}
}

func testCombatFaunaRules(t *testing.T) *fauna.Rules {
	t.Helper()
	return fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		"deer": {
			Utilities:   map[actions.ActionID]*expr.Program{"Forage": testNumProgram(t, "1")},
			Drives:      []fauna.DriveRule{{ID: "hunger"}},
			Speed:       testNumProgram(t, "0"),
			SmellRadius: 5,
			SightRadius: 5,
			FovArc:      3.14,
		},
		"wolf": {
			Utilities: map[actions.ActionID]*expr.Program{
				"Attack": testNumProgram(t, "1"),
				"Feed":   testNumProgram(t, "1"),
			},
			Drives:      []fauna.DriveRule{{ID: "hunger"}},
			Speed:       testNumProgram(t, "0"),
			IsPredator:  true,
			Feed:        testNumProgram(t, "0.5"),
			SmellRadius: 5,
			SightRadius: 5,
			FovArc:      3.14,
		},
	})
}

func installCombatActionRegistry(t *testing.T, fx *testFixture) {
	t.Helper()
	reg, err := actions.Load(strings.NewReader(`schema_version: 1
actions:
  - id: Forage
    tags: [effort:med, uses:Agility]
    duration: 12
    produces: [has_food]
  - id: Attack
    tags: [effort:med, uses:Agility, combat:attack]
    duration: 1
    produces: [near_other]
  - id: Feed
    tags: [effort:low, feed:carrion]
    duration: 1
    produces: [has_food]
`))
	if err != nil {
		t.Fatalf("actions.Load combat registry: %v", err)
	}
	fx.world.actReg = reg
	fx.actReg = reg
	fx.svc.Actions = reg
	fx.world.svc.Actions = reg
}

func installCombatFauna(t *testing.T, fx *testFixture, animals []fauna.Animal) {
	t.Helper()
	installCombatActionRegistry(t, fx)
	cfg := testEnvConfig()
	cfg.ScentCellSize = 5
	cfg.ScentSpread = 1
	cfg.FaunaDT = 1
	fx.world.InstallFauna(cfg, testCombatFaunaRules(t), testScentEmitters(), animals)
}

func TestFaunaOffAndInstalledEmptyNeutrality(t *testing.T) {
	run := func(install bool) string {
		fx := newFixtureSeeded(t, 31)
		if install {
			cfg := testEnvConfig()
			cfg.ScentCellSize = 5
			cfg.ScentSpread = 2
			cfg.FaunaDT = 1
			fx.world.InstallFauna(cfg, fauna.NewRules(nil), testScentEmitters(), nil)
		}
		fx.world.Tick()
		return worldDigest(fx.world)
	}
	off := run(false)
	empty := run(true)
	if off != empty {
		t.Fatalf("installed empty fauna changed digest\noff:\n%s\nempty:\n%s", off, empty)
	}
}

func TestFaunaSnapshotEnvAndMissingEnvPanic(t *testing.T) {
	fx := newFixtureSeeded(t, 32)
	cfg := testEnvConfig()
	cfg.ScentCellSize = 5
	cfg.ScentSpread = 2
	cfg.FaunaDT = 1
	climCfg := climate.Config{
		GridCols: 1, GridRows: 1,
		WorldMin: cfg.Min, WorldMax: cfg.Max,
		InitMoisture: 0.7, InitTemperature: 12,
		WindPrevailingDir: 1.25, WindMagMean: 0.4,
	}
	climState := climate.New(climCfg, func(core.Vec2) core.Tag { return "plain" })
	fx.world.InstallEnv(cfg, testNavMap(), climState, climate.NewRules(nil), nil, nil, nil, nil)
	fx.world.InstallFauna(cfg, testFaunaRules(t), testScentEmitters(), []fauna.Animal{
		testAnimal("an:1", "deer", core.Vec2{X: 3, Y: 4}),
		testAnimal("an:2", "deer", core.Vec2{X: 4, Y: 4}),
	})

	snap := fx.world.buildFaunaSnapshot()
	if len(snap.Animals) != 2 || snap.Animals[0].ID != "an:1" || snap.Animals[1].ID != "an:2" {
		t.Fatalf("snapshot animals not sorted: %+v", snap.Animals)
	}
	env1, env2 := snap.Env["an:1"], snap.Env["an:2"]
	if env1.Temperature != 12 || env1.Moisture != 0.7 || env1.Wind.Dir != 1.25 || env1.Wind.Mag != 0.4 {
		t.Fatalf("EnvSample = %+v, want climate temperature/moisture/wind", env1)
	}
	if env1 != env2 {
		t.Fatalf("animals in same climate cell got different env samples: %+v vs %+v", env1, env2)
	}
	delete(snap.Env, "an:1")
	defer func() {
		if recover() == nil {
			t.Fatalf("fauna.Step did not panic on missing EnvSample")
		}
	}()
	_ = fauna.Step(snap, fx.world.faunaRules, fx.world.envFork(fx.world.CurrentTick(), "fauna"))
}

func TestWorldTerrainSamplerSemantics(t *testing.T) {
	cfg := navmap.Config{CellSize: 5, MinX: 0, MinY: 0, MaxX: 20, MaxY: 20, WearCostMin: 0.5, WearMax: 1}
	types := map[navmap.TerrainID]navmap.TerrainType{
		"soil":  {BaseCost: 1, Passable: true},
		"water": {BaseCost: 5, Passable: true},
	}
	nav := navmap.New(cfg, func(p core.Vec2) navmap.TerrainID {
		if p.X >= 10 {
			return "water"
		}
		return "soil"
	}, types)
	nav.StampFootprint([]navmap.Cell{{X: 0, Y: 0}}, false)
	sampler := worldTerrainSampler{nav: nav}

	water := core.Vec2{X: 12, Y: 2}
	if sampler.FootprintBlocked(water) {
		t.Fatalf("water terrain was treated as footprint blocked")
	}
	if sampler.TerrainAt(water) != "water" || sampler.BaseCost(water) != 5 {
		t.Fatalf("water sample terrain=%s cost=%.1f, want water/5", sampler.TerrainAt(water), sampler.BaseCost(water))
	}
	if !sampler.FootprintBlocked(core.Vec2{X: 2, Y: 2}) {
		t.Fatalf("stamped wall footprint was not blocked")
	}
}

func TestAnimalApplyMoveEffectAndDeath(t *testing.T) {
	fx := newFixtureSeeded(t, 33)
	cfg := testEnvConfig()
	cfg.ScentCellSize = 5
	cfg.ScentSpread = 2
	cfg.FaunaDT = 1
	fx.world.InstallFauna(cfg, testFaunaRules(t), testScentEmitters(), []fauna.Animal{testAnimal("an:1", "deer", core.Vec2{})})

	fx.world.applyAnimalIntent(fauna.Intent{
		Animal:      "an:1",
		Action:      "Eat",
		NextPos:     core.Vec2{X: 6, Y: 7},
		NextHeading: 0.75,
		Drives:      map[fauna.DriveID]float64{"hunger": 0.5, "Satiety": 0.1},
		Stamina:     0.4,
		ActiveUntil: 9,
	})
	a := fx.world.animals["an:1"]
	if a.Pos.X != 6 || a.Pos.Y != 7 || a.Heading != 0.75 || a.Stamina != 0.4 || a.ActiveUntil != 9 || a.CurrentAction != "Eat" {
		t.Fatalf("animal state not committed: %+v", a)
	}
	if got := a.Drives["Satiety"]; got != 0.6 {
		t.Fatalf("action effect not layered onto matching drive: got %.2f want 0.60", got)
	}
	if pos, ok := fx.world.spatial.PosOf("an:1"); !ok || pos.X != 6 || pos.Y != 7 {
		t.Fatalf("spatial position not moved: pos=%+v ok=%v", pos, ok)
	}

	a.Vital = 0
	fx.world.applyAnimalIntent(fauna.Intent{Animal: "an:1", Action: "Forage", NextPos: a.Pos, Drives: a.Drives})
	if _, ok := fx.world.animals["an:1"]; ok {
		t.Fatalf("dead animal was not removed")
	}
	found := false
	for _, e := range fx.emit.events {
		if e.Type == "AnimalDied" {
			found = true
		}
	}
	if !found {
		t.Fatalf("AnimalDied event not emitted")
	}
}

func TestAnimalAttackAppliesTargetDamageAndMutualEngage(t *testing.T) {
	fx := newFixtureSeeded(t, 331)
	wolf := testAnimal("an:wolf", "wolf", core.Vec2{X: 1, Y: 1})
	deer := testAnimal("an:deer", "deer", core.Vec2{X: 2, Y: 1})
	deer.Vital = 0.9
	deer.VitalCap = 1
	installCombatFauna(t, fx, []fauna.Animal{deer, wolf})

	fx.world.applyAnimalIntent(fauna.Intent{
		Animal:               "an:wolf",
		Action:               "Attack",
		Target:               "an:deer",
		NextPos:              wolf.Pos,
		Drives:               wolf.Drives,
		Stamina:              0.7,
		Vital:                0.8,
		VitalCap:             1,
		EngagedWith:          "an:deer",
		NextExchangeTick:     12,
		EngageCooldownUntil:  30,
		Damage:               0.25,
		TargetVitalCapDamage: 0.1,
	})

	gotWolf := fx.world.animals["an:wolf"]
	gotDeer := fx.world.animals["an:deer"]
	if gotWolf.EngagedWith != "an:deer" || gotDeer.EngagedWith != "an:wolf" {
		t.Fatalf("mutual engage not set: wolf=%q deer=%q", gotWolf.EngagedWith, gotDeer.EngagedWith)
	}
	if gotDeer.NextExchangeTick != 12 || gotDeer.EngageCooldownUntil != 30 {
		t.Fatalf("target engage timing not committed: %+v", gotDeer)
	}
	if gotDeer.Vital != 0.65 || gotDeer.VitalCap != 0.9 {
		t.Fatalf("target vital/cap = %.2f/%.2f, want 0.65/0.90", gotDeer.Vital, gotDeer.VitalCap)
	}
	if gotWolf.Vital != 0.8 {
		t.Fatalf("actor proposed vital not committed: %.2f", gotWolf.Vital)
	}
}

// Regression: a fresh attack must engage the VICTIM regardless of sorted-id order. The victim's own
// same-tick intent (EngagedWith:"") must NOT clobber the attacker's cross-written mutual lock. Pre the
// two-pass apply this failed when attacker.id < victim.id (attacker applied first, then the victim's own
// commit reset EngagedWith to ""). Both orderings must now leave the victim engaged.
func TestCombatMutualEngageSurvivesVictimOwnCommit(t *testing.T) {
	check := func(t *testing.T, wolfID, deerID core.ObjectID) {
		fx := newFixtureSeeded(t, 424)
		wolf := testAnimal(wolfID, "wolf", core.Vec2{X: 1, Y: 1})
		deer := testAnimal(deerID, "deer", core.Vec2{X: 2, Y: 1})
		deer.Vital = 0.9
		deer.VitalCap = 1
		installCombatFauna(t, fx, []fauna.Animal{deer, wolf})

		attack := fauna.Intent{
			Animal: wolfID, Action: "Attack", Target: deerID,
			NextPos: wolf.Pos, Stamina: 0.7, Vital: 0.8, VitalCap: 1,
			EngagedWith: deerID, NextExchangeTick: 12, EngageCooldownUntil: 30,
			Damage: 0.2,
		}
		// The victim's OWN intent this tick: it did NOT plan to engage (fresh attack).
		victim := fauna.Intent{
			Animal: deerID, Action: "Forage",
			NextPos: deer.Pos, Stamina: 1, Vital: 0.9, VitalCap: 1,
			EngagedWith: "",
		}
		// Slice order is irrelevant (applyCombinedIntents sorts by id); the id VALUES drive the order.
		fx.world.applyCombinedIntents(nil, []fauna.Intent{attack, victim}, 2000, nil)

		gotDeer := fx.world.animals[deerID]
		if gotDeer == nil {
			t.Fatalf("victim %s unexpectedly removed", deerID)
		}
		if gotDeer.EngagedWith != wolfID {
			t.Fatalf("victim engage clobbered by own commit: got %q want %q (attacker=%s victim=%s)",
				gotDeer.EngagedWith, wolfID, wolfID, deerID)
		}
	}
	t.Run("attacker_id_lt_victim", func(t *testing.T) { check(t, "an:a_wolf", "an:z_deer") })
	t.Run("attacker_id_gt_victim", func(t *testing.T) { check(t, "an:z_wolf", "an:a_deer") })
}

func TestAnimalDeathSpawnsCarcassDecayLotAndCarrionScent(t *testing.T) {
	fx := newFixtureSeeded(t, 332)
	wolf := testAnimal("an:wolf", "wolf", core.Vec2{X: 1, Y: 1})
	deer := testAnimal("an:deer", "deer", core.Vec2{X: 6, Y: 1})
	deer.Vital = 0.2
	installCombatFauna(t, fx, []fauna.Animal{deer, wolf})
	fx.world.decayRules = decay.NewRules(map[decay.KindID]decay.KindRule{
		"carcass": {
			BaseRate: 1,
			Accel:    testNumProgram(t, "1"),
			States: []decay.StateRule{
				{Threshold: 0, Supply: map[core.Dimension]float64{"Satiety": 0.8}},
				{Threshold: 10},
			},
		},
	})

	fx.world.applyAnimalIntent(fauna.Intent{
		Animal:               "an:wolf",
		Action:               "Attack",
		Target:               "an:deer",
		NextPos:              wolf.Pos,
		Drives:               wolf.Drives,
		Stamina:              1,
		Vital:                1,
		VitalCap:             1,
		EngagedWith:          "an:deer",
		Damage:               0.3,
		TargetVitalCapDamage: 0.05,
	})

	if _, ok := fx.world.animals["an:deer"]; ok {
		t.Fatalf("dead target animal still present")
	}
	var carcassID core.ObjectID
	for _, id := range fx.world.objectIDs {
		obj := fx.world.objects[id]
		if obj.Kind == "carcass" {
			carcassID = id
			if obj.Pos != deer.Pos {
				t.Fatalf("carcass pos = %+v, want %+v", obj.Pos, deer.Pos)
			}
		}
	}
	if carcassID == "" {
		t.Fatalf("death did not spawn carcass object")
	}
	foundLot := false
	for _, lot := range fx.world.decayState.Lots() {
		if lot.ID == carcassID && lot.Kind == "carcass" {
			foundLot = true
		}
	}
	if !foundLot {
		t.Fatalf("death did not inject carcass decay lot %q", carcassID)
	}
	if _, ok := fx.world.decayEnvInputs()[carcassID]; !ok {
		t.Fatalf("carcass lot missing from decayEnvInputs")
	}
	fx.world.runScentEnv()
	if got := fx.world.scent.IntensityAt(scent.ChanCarrion, deer.Pos); got <= 0 {
		t.Fatalf("carcass did not deposit carrion scent")
	}
}

func TestAnimalFeedReducesHungerFromCarcassSupply(t *testing.T) {
	fx := newFixtureSeeded(t, 333)
	wolf := testAnimal("an:wolf", "wolf", core.Vec2{X: 2, Y: 2})
	wolf.Drives["hunger"] = 0.9
	installCombatFauna(t, fx, []fauna.Animal{wolf})
	fx.world.PlaceObject("obj:carcass", "carcass", core.Vec2{X: 2.5, Y: 2}, map[core.Dimension]float64{"Satiety": 0.8})
	fx.world.decayState = decay.New([]decay.Lot{{ID: "obj:carcass", Kind: "carcass", Qty: 1}})
	fx.world.decayLotPos["obj:carcass"] = core.Vec2{X: 2.5, Y: 2}
	fx.world.decayRules = decay.NewRules(map[decay.KindID]decay.KindRule{
		"carcass": {
			BaseRate: 1,
			Accel:    testNumProgram(t, "1"),
			States:   []decay.StateRule{{Threshold: 0, Supply: map[core.Dimension]float64{"Satiety": 0.8}}},
		},
	})

	fx.world.applyAnimalIntent(fauna.Intent{
		Animal:   "an:wolf",
		Action:   "Feed",
		Target:   "obj:carcass",
		NextPos:  wolf.Pos,
		Drives:   map[fauna.DriveID]float64{"hunger": 0.9},
		Stamina:  1,
		Vital:    1,
		VitalCap: 1,
	})

	if got := fx.world.animals["an:wolf"].Drives["hunger"]; got != 0.5 {
		t.Fatalf("hunger after feed = %.2f, want 0.50", got)
	}
	if _, ok := fx.world.objects["obj:carcass"]; ok {
		t.Fatalf("consumed carcass object still present")
	}
	for _, lot := range fx.world.decayState.Lots() {
		if lot.ID == "obj:carcass" {
			t.Fatalf("consumed carcass decay lot still present")
		}
	}
}

func TestScentChannelFromTagCarrion(t *testing.T) {
	ch, ok := scentChannelFromTag("scent:carrion")
	if !ok || ch != scent.ChanCarrion {
		t.Fatalf("scent:carrion mapped to channel=%d ok=%v, want ChanCarrion/true", ch, ok)
	}
}

func TestAnimalVitalRegenCommitRespectsVitalCap(t *testing.T) {
	fx := newFixtureSeeded(t, 334)
	wolf := testAnimal("an:wolf", "wolf", core.Vec2{})
	wolf.Vital = 0.4
	wolf.VitalCap = 0.6
	installCombatFauna(t, fx, []fauna.Animal{wolf})

	fx.world.applyAnimalIntent(fauna.Intent{
		Animal:   "an:wolf",
		Action:   "Forage",
		NextPos:  wolf.Pos,
		Drives:   wolf.Drives,
		Stamina:  1,
		Vital:    0.6,
		VitalCap: 0.6,
	})
	got := fx.world.animals["an:wolf"]
	if got.Vital != 0.6 || got.VitalCap != 0.6 {
		t.Fatalf("vital regen commit = %.2f/%.2f, want 0.60/0.60", got.Vital, got.VitalCap)
	}
}

func TestTwoPredatorsSameTargetConflictDeterministic(t *testing.T) {
	run := func(shuffle bool) string {
		fx := newFixtureSeeded(t, 335)
		low := testAnimal("an:a", "wolf", core.Vec2{X: 1})
		high := testAnimal("an:b", "wolf", core.Vec2{X: 2})
		deer := testAnimal("an:target", "deer", core.Vec2{X: 3})
		low.Stats["Agility"] = 70
		high.Stats["Agility"] = 60
		installCombatFauna(t, fx, []fauna.Animal{low, high, deer})
		intents := []fauna.Intent{
			{Animal: "an:a", Action: "Attack", Target: "an:target", NextPos: low.Pos, Drives: low.Drives, Stamina: 1, Vital: 1, VitalCap: 1, EngagedWith: "an:target", Damage: 0.2},
			{Animal: "an:b", Action: "Attack", Target: "an:target", NextPos: high.Pos, Drives: high.Drives, Stamina: 1, Vital: 1, VitalCap: 1, EngagedWith: "an:target", Damage: 0.4},
		}
		if shuffle {
			intents[0], intents[1] = intents[1], intents[0]
		}
		fx.world.applyCombinedIntents(nil, intents, 2000, nil)
		return animalDigest(fx.world.animals, fx.world.animalIDs)
	}
	if a, b := run(false), run(true); a != b {
		t.Fatalf("same-target predator conflict changed with collection order\nA:\n%s\nB:\n%s", a, b)
	}
}

func TestScentDepositCommitPostMoveAndLatency(t *testing.T) {
	fx := newFixtureSeeded(t, 34)
	cfg := testEnvConfig()
	cfg.ScentCellSize = 5
	cfg.ScentSpread = 2
	cfg.FaunaDT = 1
	fx.world.InstallFauna(cfg, testFaunaRules(t), testScentEmitters(), []fauna.Animal{
		testAnimal("an:w", "wolf", core.Vec2{X: 1, Y: 1}),
		testAnimal("an:d", "deer", core.Vec2{X: 2, Y: 2}),
	})
	fx.world.applyAnimalIntent(fauna.Intent{
		Animal:      "an:w",
		Action:      "Forage",
		NextPos:     core.Vec2{X: 11, Y: 1},
		NextHeading: 0,
		Drives:      map[fauna.DriveID]float64{"hunger": 0.2},
		Stamina:     1,
	})
	if got := fx.world.scent.IntensityAt(scent.ChanPredator, core.Vec2{X: 11, Y: 1}); got != 0 {
		t.Fatalf("scent visible before commit: %.3f", got)
	}
	beforePlan := fx.world.buildFaunaSnapshot()
	if got := beforePlan.Scent.IntensityAt(scent.ChanPredator, core.Vec2{X: 11, Y: 1}); got != 0 {
		t.Fatalf("tick T fauna snapshot saw pending predator scent: %.3f", got)
	}
	fx.world.runScentEnv()
	if old := fx.world.scent.IntensityAt(scent.ChanPredator, core.Vec2{X: 1, Y: 1}); old != 0 {
		t.Fatalf("predator scent deposited at old position: %.3f", old)
	}
	if got := fx.world.scent.IntensityAt(scent.ChanPredator, core.Vec2{X: 11, Y: 1}); got <= 0 {
		t.Fatalf("predator scent not deposited at post-move position")
	}
	if got := fx.world.scent.IntensityAt(scent.ChanPrey, core.Vec2{X: 2, Y: 2}); got <= 0 {
		t.Fatalf("prey scent not deposited on bulk cadence")
	}
	nextPlan := fx.world.buildFaunaSnapshot()
	if got := nextPlan.Scent.IntensityAt(scent.ChanPredator, core.Vec2{X: 11, Y: 1}); got <= 0 {
		t.Fatalf("tick T+1 fauna snapshot did not see committed predator scent")
	}
}

func TestScentEmitterClassificationUsesContentTags(t *testing.T) {
	fx := newFixtureSeeded(t, 340)
	cfg := testEnvConfig()
	cfg.ScentCellSize = 5
	cfg.ScentSpread = 1
	cfg.FaunaDT = 1
	fx.world.InstallEnv(
		cfg,
		testNavMap(),
		nil,
		nil,
		flora.New([]flora.Plant{
			{ID: "plant:grass", Species: "grass", Pos: core.Vec2{X: 1, Y: 11}, Length: 2, Width: 2},
			{ID: "plant:wildflower", Species: "wildflower", Pos: core.Vec2{X: 11, Y: 11}, Length: 2, Width: 2},
		}),
		nil,
		nil,
		nil,
	)
	fx.world.InstallFauna(cfg, testFaunaRules(t), testScentEmitters(), []fauna.Animal{
		testAnimal("an:w", "wolf", core.Vec2{X: 1, Y: 1}),
		testAnimal("an:d", "deer", core.Vec2{X: 11, Y: 1}),
		testAnimal("an:f", "future", core.Vec2{X: 30, Y: 1}),
	})

	fx.world.runScentEnv()
	if got := fx.world.scent.IntensityAt(scent.ChanPredator, core.Vec2{X: 1, Y: 1}); got <= 0 {
		t.Fatalf("predator tag did not deposit predator scent")
	}
	if got := fx.world.scent.IntensityAt(scent.ChanPrey, core.Vec2{X: 11, Y: 1}); got <= 0 {
		t.Fatalf("prey tag did not deposit prey scent")
	}
	if got := fx.world.scent.IntensityAt(scent.ChanPrey, core.Vec2{X: 30, Y: 1}); got != 0 {
		t.Fatalf("unknown future scent tag deposited a known channel: %.3f", got)
	}
	if got := fx.world.scent.IntensityAt(scent.ChanFood, core.Vec2{X: 1, Y: 11}); got <= 0 {
		t.Fatalf("food-tagged flora did not deposit food scent")
	}
	if got := fx.world.scent.IntensityAt(scent.ChanFood, core.Vec2{X: 11, Y: 11}); got != 0 {
		t.Fatalf("untagged flora deposited food scent: %.3f", got)
	}
}

func TestScentEmitterRegistryNilSuppressesDeposits(t *testing.T) {
	fx := newFixtureSeeded(t, 341)
	cfg := testEnvConfig()
	cfg.ScentCellSize = 5
	cfg.ScentSpread = 1
	cfg.FaunaDT = 1
	fx.world.InstallFauna(cfg, testFaunaRules(t), nil, []fauna.Animal{
		testAnimal("an:w", "wolf", core.Vec2{X: 1, Y: 1}),
		testAnimal("an:d", "deer", core.Vec2{X: 11, Y: 1}),
	})

	fx.world.runScentEnv()
	if got := fx.world.scent.IntensityAt(scent.ChanPredator, core.Vec2{X: 1, Y: 1}); got != 0 {
		t.Fatalf("nil emitter registry fell back to predator classification: %.3f", got)
	}
	if got := fx.world.scent.IntensityAt(scent.ChanPrey, core.Vec2{X: 11, Y: 1}); got != 0 {
		t.Fatalf("nil emitter registry fell back to prey classification: %.3f", got)
	}
}

func TestCombinedApplyConflictTieBySharedID(t *testing.T) {
	fx := newFixtureSeeded(t, 35)
	cfg := testEnvConfig()
	cfg.ScentCellSize = 5
	cfg.ScentSpread = 2
	cfg.FaunaDT = 1
	animal := testAnimal("an:1", "deer", core.Vec2{})
	animal.Stats["Agility"] = 80
	fx.world.InstallFauna(cfg, testFaunaRules(t), testScentEmitters(), []fauna.Animal{animal})
	hunter := fx.world.Spawn("zz_agent", core.Vec2{}, agent.DefaultConfig(), rng.New(350))
	hunter.RealStats["Agility"] = 80
	hunter.NeedIntensities["Satiety"] = 0.8

	fx.world.applyCombinedIntents(
		[]agent.Intent{{Kind: agent.IntentStart, Agent: "zz_agent", Action: "Forage", Target: "berry"}},
		[]fauna.Intent{{Animal: "an:1", Action: "Forage", Target: "berry", NextPos: core.Vec2{X: 4}, Drives: animal.Drives, Stamina: 1}},
		1000,
		nil,
	)
	if got := fx.world.animals["an:1"].Pos.X; got != 4 {
		t.Fatalf("animal should win tie by lower shared id and apply, pos.x=%.1f", got)
	}
	if hunter.Elapsed != 0 {
		t.Fatalf("agent conflict loser should not advance outcome, elapsed=%d", hunter.Elapsed)
	}
}

func TestCombinedApplyShuffledCollectionOrderDeterministic(t *testing.T) {
	run := func(shuffle bool) string {
		fx := newFixtureSeeded(t, 350)
		cfg := testEnvConfig()
		cfg.ScentCellSize = 5
		cfg.ScentSpread = 2
		cfg.FaunaDT = 1
		fx.world.InstallFauna(cfg, testFaunaRules(t), testScentEmitters(), []fauna.Animal{
			testAnimal("an:1", "deer", core.Vec2{}),
			testAnimal("an:2", "deer", core.Vec2{X: 1}),
		})
		a := fx.world.Spawn("agent_a", core.Vec2{X: 2}, agent.DefaultConfig(), rng.New(351))
		b := fx.world.Spawn("agent_b", core.Vec2{X: 3}, agent.DefaultConfig(), rng.New(352))
		a.RealStats["Agility"] = 70
		b.RealStats["Agility"] = 60
		a.NeedIntensities["Satiety"] = 0.8
		b.NeedIntensities["Satiety"] = 0.8

		agentIntents := []agent.Intent{
			{Kind: agent.IntentStart, Agent: "agent_a", Action: "Forage", Target: "berry_a"},
			{Kind: agent.IntentStart, Agent: "agent_b", Action: "Forage", Target: "berry_b"},
		}
		animalIntents := []fauna.Intent{
			{Animal: "an:1", Action: "Forage", Target: "berry_c", NextPos: core.Vec2{X: 4}, Drives: fx.world.animals["an:1"].Drives, Stamina: 1},
			{Animal: "an:2", Action: "Forage", Target: "berry_d", NextPos: core.Vec2{X: 5}, Drives: fx.world.animals["an:2"].Drives, Stamina: 1},
		}
		if shuffle {
			agentIntents[0], agentIntents[1] = agentIntents[1], agentIntents[0]
			animalIntents[0], animalIntents[1] = animalIntents[1], animalIntents[0]
		}
		fx.world.applyCombinedIntents(agentIntents, animalIntents, 1200, nil)
		return worldDigest(fx.world) + animalDigest(fx.world.animals, fx.world.animalIDs)
	}
	if a, b := run(false), run(true); a != b {
		t.Fatalf("combined apply changed with shuffled collection order\nA:\n%s\nB:\n%s", a, b)
	}
}

func TestFaunaForkIndependentOfAgentCount(t *testing.T) {
	run := func(agentCount int) float64 {
		fx := newFixtureSeeded(t, 36)
		for i := 0; i < agentCount; i++ {
			fx.world.Spawn(core.AgentID(fmt.Sprintf("agent_%d", i)), core.Vec2{X: float64(i)}, agent.DefaultConfig(), rng.New(int64(i+1)))
		}
		fx.world.Tick()
		return fx.world.envFork(fx.world.CurrentTick(), "fauna").Float64()
	}
	if a, b := run(0), run(4); a != b {
		t.Fatalf("fauna env fork changed with agent count: %.12f vs %.12f", a, b)
	}
}

func TestFaunaDeterminismGolden(t *testing.T) {
	run := func() string {
		fx := newFixtureSeeded(t, 37)
		cfg := testEnvConfig()
		cfg.ScentCellSize = 5
		cfg.ScentSpread = 2
		cfg.FaunaDT = 1
		fx.world.InstallFauna(cfg, testFaunaRules(t), testScentEmitters(), []fauna.Animal{
			testAnimal("an:1", "deer", core.Vec2{}),
			testAnimal("an:2", "wolf", core.Vec2{X: 5}),
		})
		for range 3 {
			fx.world.Tick()
		}
		var b strings.Builder
		b.WriteString(worldDigest(fx.world))
		b.WriteString(animalDigest(fx.world.animals, fx.world.animalIDs))
		b.WriteString(fmt.Sprintf("pred=%.3f prey=%.3f", fx.world.scent.IntensityAt(scent.ChanPredator, core.Vec2{X: 5}), fx.world.scent.IntensityAt(scent.ChanPrey, core.Vec2{})))
		return b.String()
	}
	if a, b := run(), run(); a != b {
		t.Fatalf("fauna determinism mismatch\nA:\n%s\nB:\n%s", a, b)
	}
}
