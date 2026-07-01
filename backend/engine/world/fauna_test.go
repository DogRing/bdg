package world

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/env/climate"
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
		"grass":      {"scent:food"},
		"future":     {"scent:carrion"},
		"wolf":       {"scent:predator"},
		"wildflower": nil,
	}
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
