package fauna_test

import (
	"math"
	"testing"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/space/spatial"
)

func TestHiddenPreySkippedUnlessWithinFlushRadius(t *testing.T) {
	rules := combatRules(t)
	pred := combatPredAnimal("predator", core.Vec2{})
	hidden := combatPreyAnimal("prey", core.Vec2{X: 3})
	hidden.HiddenUntil = 200

	snap := withCombatParams(makeSnap([]fauna.Animal{pred, hidden}, nil, nil, openTerrain, 100, emptyEnv("predator", "prey")))
	snap.Combat.HiddenFlushFactor = 1
	snap.ScentCellSize = 1
	it := intentByAnimal(t, fauna.Step(snap, rules, rng.New(1)), "predator")
	if it.Target != "" || it.EngagedWith != "" {
		t.Fatalf("hidden prey outside flush radius should not be acquired: %+v", it)
	}

	hidden.Pos = core.Vec2{X: 0.5}
	flushSnap := withCombatParams(makeSnap([]fauna.Animal{pred, hidden}, nil, nil, openTerrain, 100, emptyEnv("predator", "prey")))
	flushSnap.Combat.HiddenFlushFactor = 1
	flushSnap.ScentCellSize = 1
	it = intentByAnimal(t, fauna.Step(flushSnap, rules, rng.New(1)), "predator")
	if it.Target != "prey" || it.EngagedWith != "prey" {
		t.Fatalf("hidden prey inside flush radius should be acquired: %+v", it)
	}

	shuffled := withCombatParams(makeSnap([]fauna.Animal{hidden, pred}, nil, nil, openTerrain, 100, emptyEnv("predator", "prey")))
	shuffled.Combat.HiddenFlushFactor = 1
	shuffled.ScentCellSize = 1
	it = intentByAnimal(t, fauna.Step(shuffled, rules, rng.New(1)), "predator")
	if it.Target != "prey" || it.EngagedWith != "prey" {
		t.Fatalf("hidden flush acquisition should be input-order deterministic: %+v", it)
	}
}

func TestHiddenAnimalCrouchesUnlessFlushed(t *testing.T) {
	rules := hideSteerRules(t)
	prey := hidePrey(core.Vec2{})
	pred := fauna.Animal{
		ID: "predator", Species: "pred", Pos: core.Vec2{X: 5}, Heading: math.Pi,
		Stats: map[core.StatID]float64{}, Drives: map[fauna.DriveID]float64{}, Stamina: 1, Vital: 1,
	}
	sp := spatial.New(8)
	sp.Insert(prey.ID, prey.Pos)
	sp.Insert(pred.ID, pred.Pos)
	snap := makeSnap([]fauna.Animal{prey, pred}, nil, sp, openTerrain, 10, emptyEnv("prey", "predator"))
	snap.Combat.HiddenFlushFactor = 1
	snap.ScentCellSize = 1

	it := intentByAnimal(t, fauna.Step(snap, rules, rng.New(1)), "prey")
	if it.NextPos != prey.Pos || it.NextHeading != prey.Heading {
		t.Fatalf("hidden unflushed prey should crouch at %v/%v, got %v/%v", prey.Pos, prey.Heading, it.NextPos, it.NextHeading)
	}

	pred.Pos = core.Vec2{X: 0.5}
	sp = spatial.New(8)
	sp.Insert(prey.ID, prey.Pos)
	sp.Insert(pred.ID, pred.Pos)
	flushSnap := makeSnap([]fauna.Animal{prey, pred}, nil, sp, openTerrain, 10, emptyEnv("prey", "predator"))
	flushSnap.Combat.HiddenFlushFactor = 1
	flushSnap.ScentCellSize = 1
	it = intentByAnimal(t, fauna.Step(flushSnap, rules, rng.New(1)), "prey")
	if it.NextPos == prey.Pos {
		t.Fatalf("flushed hidden prey should bolt, got held position %+v", it)
	}

	prey.HiddenUntil = 0
	sp = spatial.New(8)
	sp.Insert(prey.ID, prey.Pos)
	freshSnap := makeSnap([]fauna.Animal{prey}, nil, sp, openTerrain, 10, emptyEnv("prey"))
	freshSnap.Combat.HiddenFlushFactor = 1
	freshSnap.ScentCellSize = 1
	it = intentByAnimal(t, fauna.Step(freshSnap, rules, rng.New(1)), "prey")
	if it.NextPos == prey.Pos {
		t.Fatalf("non-hidden prey should keep normal steer, got held position %+v", it)
	}
}

func TestHiddenDormantCheapPathCrouches(t *testing.T) {
	rules := hideSteerRules(t)
	prey := hidePrey(core.Vec2{})
	prey.ActiveUntil = 0
	snap := makeSnap([]fauna.Animal{prey}, nil, nil, openTerrain, 1, emptyEnv("prey"))
	snap.Cadence.DormantPeriod = 0

	it := intentByAnimal(t, fauna.Step(snap, rules, rng.New(1)), "prey")
	if it.NextPos != prey.Pos || it.NextHeading != prey.Heading {
		t.Fatalf("hidden dormant prey should crouch on cheap path: %+v", it)
	}
}

func TestRulesHideChanceNeutralAndEval(t *testing.T) {
	rules := hideSteerRules(t)
	ctx := hideCtx{stats: map[core.StatID]float64{"Agility": 10}, attrs: map[core.Tag]float64{"fear": 0.2}}
	if got := rules.HideChance("prey", ctx); math.Abs(got-0.7) > 1e-12 {
		t.Fatalf("HideChance = %v, want 0.7", got)
	}
	if got := rules.HideChance("pred", ctx); got != 0 {
		t.Fatalf("absent predator HideChance = %v, want 0", got)
	}
	negative := fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		"prey": {HideChance: mustNum(t, "0 - 1")},
	})
	if got := negative.HideChance("prey", ctx); got != 0 {
		t.Fatalf("negative HideChance = %v, want clamp to 0", got)
	}
}

func TestRulesCoverCostNeutralAndScalar(t *testing.T) {
	rules := fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		"prey": {CoverCost: 0.7},
	})
	if got := rules.CoverCost("prey"); got != 0.7 {
		t.Fatalf("CoverCost = %v, want 0.7", got)
	}
	if got := rules.CoverCost("pred"); got != 0 {
		t.Fatalf("absent species CoverCost = %v, want 0", got)
	}
	if got := (*fauna.Rules)(nil).CoverCost("prey"); got != 0 {
		t.Fatalf("nil rules CoverCost = %v, want 0", got)
	}
}

type hideCtx struct {
	stats map[core.StatID]float64
	attrs map[core.Tag]float64
}

func (c hideCtx) Stat(id core.StatID) float64 {
	return c.stats[id]
}

func (c hideCtx) Attr(id core.Tag) (float64, bool) {
	v, ok := c.attrs[id]
	return v, ok
}

func (c hideCtx) Pred(string, core.Tag) bool { return false }

func hideSteerRules(t *testing.T) *fauna.Rules {
	t.Helper()
	return fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		"prey": {
			Utilities: map[actions.ActionID]*expr.Program{
				actFlee: mustNum(t, "1"),
				actRest: mustNum(t, "0"),
			},
			Drives:      []fauna.DriveRule{{ID: "fear"}},
			AppTemp:     mustNum(t, "0"),
			Speed:       mustNum(t, "1"),
			HideChance:  mustNum(t, "0.5 + fear", "Agility"),
			Tags:        []core.Tag{"game"},
			SmellRadius: 5,
			SightRadius: 10,
			FovArc:      math.Pi,
			SteerChannel: map[actions.ActionID]core.Tag{
				actFlee: fauna.TagFleePred,
				actRest: fauna.TagNoLoco,
			},
		},
		"pred": {
			Utilities:   map[actions.ActionID]*expr.Program{actRest: mustNum(t, "0")},
			Drives:      []fauna.DriveRule{},
			AppTemp:     mustNum(t, "0"),
			Speed:       mustNum(t, "0"),
			IsPredator:  true,
			SmellRadius: 5,
			SightRadius: 10,
			FovArc:      math.Pi,
		},
	})
}

func hidePrey(pos core.Vec2) fauna.Animal {
	return fauna.Animal{
		ID: "prey", Species: "prey", Pos: pos, Heading: 0,
		Stats: map[core.StatID]float64{"Agility": 10}, Drives: map[fauna.DriveID]float64{"fear": 1},
		Stamina: 1, Vital: 1, CurrentAction: actFlee, ActiveUntil: 100, HiddenUntil: 100,
	}
}
