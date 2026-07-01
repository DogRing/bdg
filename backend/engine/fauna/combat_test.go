package fauna_test

import (
	"math"
	"testing"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/space/scent"
)

func combatRules(t *testing.T) *fauna.Rules {
	t.Helper()
	return fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		spCombatPred: {
			Utilities: map[actions.ActionID]*expr.Program{
				actAttack: mustNum(t, "hunger + scent.prey + 10 - target.threat"),
				actRest:   mustNum(t, "0"),
			},
			Drives:      []fauna.DriveRule{{ID: "hunger", Rate: 0}},
			AppTemp:     mustNum(t, "0"),
			Speed:       mustNum(t, "1"),
			AttackPower: mustNum(t, "Strength * 0.1", "Strength"),
			Hit:         mustNum(t, "Agility * 0.01", "Agility"),
			Diet:        []core.Tag{core.Tag(spCombatPrey)},
			IsPredator:  true,
			SmellRadius: 5,
			SightRadius: 5,
			FovArc:      math.Pi,
			SteerChannel: map[actions.ActionID]core.Tag{
				actAttack: fauna.TagAttack,
				actRest:   fauna.TagNoLoco,
			},
		},
		spCombatPrey: {
			Utilities: map[actions.ActionID]*expr.Program{
				actRest: mustNum(t, "0"),
			},
			Drives:       []fauna.DriveRule{{ID: "hunger", Rate: 0}},
			AppTemp:      mustNum(t, "0"),
			Speed:        mustNum(t, "0"),
			IsPredator:   false,
			SmellRadius:  5,
			SightRadius:  5,
			FovArc:       math.Pi,
			SteerChannel: map[actions.ActionID]core.Tag{actRest: fauna.TagNoLoco},
		},
	})
}

func combatParams() fauna.CombatParams {
	return fauna.CombatParams{
		ExchangeMinTicks:       10,
		ExchangeMaxTicks:       20,
		EngageCooldownMinTicks: 50,
		EngageCooldownMaxTicks: 100,
		DisengageRangeFactor:   2,
		StaminaDropThreshold:   0,
		VitalRegenPerTick:      0.001,
		VitalCapDamageFraction: 0.05,
	}
}

func withCombatParams(snap *fauna.Snapshot) *fauna.Snapshot {
	snap.Combat = combatParams()
	return snap
}

func combatPredAnimal(id core.ObjectID, pos core.Vec2) fauna.Animal {
	return fauna.Animal{
		ID:            id,
		Species:       spCombatPred,
		Pos:           pos,
		Stats:         map[core.StatID]float64{"Strength": 5, "Agility": 50},
		Drives:        map[fauna.DriveID]float64{"hunger": 0.9},
		Stamina:       1,
		Vital:         1,
		Heading:       0,
		CurrentAction: actRest,
	}
}

func combatPreyAnimal(id core.ObjectID, pos core.Vec2) fauna.Animal {
	return fauna.Animal{
		ID:            id,
		Species:       spCombatPrey,
		Pos:           pos,
		Stats:         map[core.StatID]float64{"Strength": 1, "Agility": 1},
		Drives:        map[fauna.DriveID]float64{"hunger": 0},
		Stamina:       1,
		Vital:         1,
		Heading:       math.Pi,
		CurrentAction: actRest,
	}
}

func intentByAnimal(t *testing.T, intents []fauna.Intent, id core.ObjectID) fauna.Intent {
	t.Helper()
	for _, it := range intents {
		if it.Animal == id {
			return it
		}
	}
	t.Fatalf("missing intent for animal %q", id)
	return fauna.Intent{}
}

func TestCombatUtilityPicksAttackAndEngages(t *testing.T) {
	rules := combatRules(t)
	pred := combatPredAnimal("predator", core.Vec2{X: 0, Y: 0})
	prey := combatPreyAnimal("prey", core.Vec2{X: 1, Y: 0})
	snap := withCombatParams(makeSnap([]fauna.Animal{prey, pred}, nil, nil, openTerrain, 100,
		emptyEnv("predator", "prey")))

	intents := fauna.Step(snap, rules, rng.New(99))
	it := intentByAnimal(t, intents, "predator")
	if it.Action != actAttack {
		t.Fatalf("combat predator action = %q, want %q", it.Action, actAttack)
	}
	if it.Target != "prey" || it.EngagedWith != "prey" {
		t.Fatalf("engage target/partner = %q/%q, want prey/prey", it.Target, it.EngagedWith)
	}
	if it.NextExchangeTick < 110 || it.NextExchangeTick > 120 {
		t.Fatalf("NextExchangeTick = %d, want tick+[10,20]", it.NextExchangeTick)
	}
	if it.EngageCooldownUntil < 150 || it.EngageCooldownUntil > 200 {
		t.Fatalf("EngageCooldownUntil = %d, want tick+[50,100]", it.EngageCooldownUntil)
	}
	if it.Damage != 0 {
		t.Fatalf("new engage proposed damage = %v, want 0", it.Damage)
	}
	if it.NextPos != pred.Pos || it.NextHeading != pred.Heading {
		t.Fatalf("engaged locomotion should be suppressed: pos %v heading %v", it.NextPos, it.NextHeading)
	}
}

func TestCombatExchangeDisengageAndCooldownDeterministic(t *testing.T) {
	rules := combatRules(t)
	pred := combatPredAnimal("predator", core.Vec2{X: 0, Y: 0})
	pred.CurrentAction = actAttack
	pred.EngagedWith = "prey"
	pred.NextExchangeTick = 200
	prey := combatPreyAnimal("prey", core.Vec2{X: 1, Y: 0})
	snap := withCombatParams(makeSnap([]fauna.Animal{pred, prey}, nil, nil, openTerrain, 200,
		emptyEnv("predator", "prey")))

	run := func() fauna.Intent {
		return intentByAnimal(t, fauna.Step(snap, rules, rng.New(42)), "predator")
	}
	it1 := run()
	it2 := run()
	if it1.Damage != it2.Damage || it1.NextExchangeTick != it2.NextExchangeTick {
		t.Fatalf("exchange not deterministic: %+v vs %+v", it1, it2)
	}
	wantDamage := 0.25
	if math.Abs(it1.Damage-wantDamage) > 1e-12 {
		t.Fatalf("damage = %v, want %v", it1.Damage, wantDamage)
	}
	if math.Abs(it1.TargetVitalCapDamage-wantDamage*0.05) > 1e-12 {
		t.Fatalf("target vital-cap damage = %v, want %v", it1.TargetVitalCapDamage, wantDamage*0.05)
	}
	if it1.NextExchangeTick < 210 || it1.NextExchangeTick > 220 {
		t.Fatalf("exchange reschedule = %d, want tick+[10,20]", it1.NextExchangeTick)
	}

	pred.Stamina = 0
	itDrop := intentByAnimal(t, fauna.Step(withCombatParams(makeSnap([]fauna.Animal{pred, prey}, nil, nil, openTerrain, 201,
		emptyEnv("predator", "prey"))), rules, rng.New(42)), "predator")
	if itDrop.EngagedWith != "" || itDrop.Damage != 0 {
		t.Fatalf("stamina-drop disengage = partner %q damage %v, want none/0", itDrop.EngagedWith, itDrop.Damage)
	}

	pred.Stamina = 1
	farPrey := combatPreyAnimal("prey", core.Vec2{X: 3, Y: 0})
	itFar := intentByAnimal(t, fauna.Step(withCombatParams(makeSnap([]fauna.Animal{pred, farPrey}, nil, nil, openTerrain, 201,
		emptyEnv("predator", "prey"))), rules, rng.New(42)), "predator")
	if itFar.EngagedWith != "" {
		t.Fatalf("range disengage partner = %q, want empty", itFar.EngagedWith)
	}

	cellScaledSnap := withCombatParams(makeSnap([]fauna.Animal{pred, farPrey}, nil, nil, openTerrain, 201,
		emptyEnv("predator", "prey")))
	cellScaledSnap.ScentCellSize = 2
	itCellScaled := intentByAnimal(t, fauna.Step(cellScaledSnap, rules, rng.New(42)), "predator")
	if itCellScaled.EngagedWith != "prey" {
		t.Fatalf("cell-scaled disengage range partner = %q, want prey", itCellScaled.EngagedWith)
	}

	cooling := combatPredAnimal("predator", core.Vec2{X: 0, Y: 0})
	cooling.EngageCooldownUntil = 300
	itCooling := intentByAnimal(t, fauna.Step(withCombatParams(makeSnap([]fauna.Animal{cooling, prey}, nil, nil, openTerrain, 250,
		emptyEnv("predator", "prey"))), rules, rng.New(42)), "predator")
	if itCooling.EngagedWith != "" || itCooling.Target != "" {
		t.Fatalf("cooldown should block engage: target %q partner %q", itCooling.Target, itCooling.EngagedWith)
	}
}

func TestCombatVitalCapRegenCap(t *testing.T) {
	rules := combatRules(t)
	pred := combatPredAnimal("predator", core.Vec2{})
	pred.Vital = 0.59
	pred.VitalCap = 0.6
	snap := withCombatParams(makeSnap([]fauna.Animal{pred}, nil, nil, openTerrain, 1, emptyEnv("predator")))
	snap.DT = 20

	it := intentByAnimal(t, fauna.Step(snap, rules, rng.New(1)), "predator")
	if it.VitalCap != 0.6 {
		t.Fatalf("VitalCap = %v, want 0.6", it.VitalCap)
	}
	if math.Abs(it.Vital-0.6) > 1e-12 {
		t.Fatalf("Vital regen = %v, want cap 0.6", it.Vital)
	}
}

func TestFeedSteersTowardCarrion(t *testing.T) {
	rules := fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		"scavenger": {
			Utilities: map[actions.ActionID]*expr.Program{
				actFeed: mustNum(t, "scent.carrion + 1"),
				actRest: mustNum(t, "0"),
			},
			Drives:      []fauna.DriveRule{{ID: "hunger", Rate: 0}},
			AppTemp:     mustNum(t, "0"),
			Speed:       mustNum(t, "1"),
			SmellRadius: 5,
			SightRadius: 1,
			FovArc:      math.Pi,
			SteerChannel: map[actions.ActionID]core.Tag{
				actFeed: fauna.TagFeed,
				actRest: fauna.TagNoLoco,
			},
		},
	})
	a := fauna.Animal{
		ID: "scavenger", Species: "scavenger", Pos: core.Vec2{},
		Stats: map[core.StatID]float64{}, Drives: map[fauna.DriveID]float64{"hunger": 0},
		Stamina: 1, Vital: 1, CurrentAction: actRest, ActiveUntil: 100,
	}
	sg := scent.New(1)
	sg.Deposit(scent.ChanCarrion, core.Vec2{X: 3, Y: 0}, 1)
	sg.Commit()

	it := intentByAnimal(t, fauna.Step(makeSnap([]fauna.Animal{a}, sg, nil, openTerrain, 1,
		emptyEnv("scavenger")), rules, rng.New(1)), "scavenger")
	if it.Action != actFeed {
		t.Fatalf("action = %q, want %q", it.Action, actFeed)
	}
	if it.NextPos.X <= a.Pos.X {
		t.Fatalf("Feed should steer toward carrion east of animal: next pos %v", it.NextPos)
	}
}

func TestCombatFieldsNeutralWithoutCombatActions(t *testing.T) {
	rules := newTestRules(t)
	a := predAnimal("wolf", core.Vec2{})
	snap := makeSnap([]fauna.Animal{a}, nil, nil, openTerrain, 1, emptyEnv("wolf"))

	it := intentByAnimal(t, fauna.Step(snap, rules, rng.New(1)), "wolf")
	if it.Damage != 0 || it.TargetVitalCapDamage != 0 || it.EngagedWith != "" || it.Target != "" {
		t.Fatalf("non-combat rules should not propose combat: %+v", it)
	}
}
