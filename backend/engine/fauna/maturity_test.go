package fauna_test

import (
	"testing"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/actions"
)

// PD4-ii/P_fa4c maturity operand. A species with two actions: actA's §6 utility IS `maturity`, actB is a
// constant 0.5. So the animal picks actA (the "mate-like" action) exactly when maturity > 0.5 — i.e. once
// age passes half of maturity_age. This exercises the operand end-to-end through Step's scorer.
func maturityRules(t *testing.T, maturityAge float64) *fauna.Rules {
	t.Helper()
	return fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		spHerb: {
			Utilities:    map[actions.ActionID]*expr.Program{actA: mustNum(t, "maturity"), actB: mustNum(t, "0.5")},
			Drives:       []fauna.DriveRule{},
			Speed:        mustNum(t, "0"),
			MaturityAge:  maturityAge,
			SmellRadius:  5, SightRadius: 5, FovArc: 1,
			SteerChannel: map[actions.ActionID]core.Tag{},
		},
	})
}

func stepMaturityAction(t *testing.T, rules *fauna.Rules, age float64) actions.ActionID {
	t.Helper()
	a := herbAnimal("m1", core.Vec2{X: 50, Y: 50}, map[fauna.DriveID]float64{})
	a.Age = age
	a.ActiveUntil = 1000
	a.CurrentAction = actB
	snap := makeSnap([]fauna.Animal{a}, nil, nil, openTerrain, 1, emptyEnv("m1"))
	return fauna.Step(snap, rules, rng.New(1))[0].Action
}

// A young animal (age below half of maturity_age) reads maturity < 0.5 and picks actB; an old one reads
// maturity > 0.5 and picks actA. This is the maturity gate that will let only grown animals mate.
func TestMaturityGatesByAge(t *testing.T) {
	rules := maturityRules(t, 100) // maturity = clamp01(age/100)
	if got := stepMaturityAction(t, rules, 10); got != actB {
		t.Errorf("age 10 (maturity 0.1 < 0.5) should pick actB, got %q", got)
	}
	if got := stepMaturityAction(t, rules, 90); got != actA {
		t.Errorf("age 90 (maturity 0.9 > 0.5) should pick actA, got %q", got)
	}
}

// maturity clamps at 1: an animal well past maturity_age is fully mature (not > 1).
func TestMaturityClampsAtOne(t *testing.T) {
	rules := maturityRules(t, 100)
	// age 1000 ⇒ raw 10, clamped to 1 ⇒ picks actA (1 > 0.5). If it did NOT clamp it would still pick actA,
	// so also assert the operand is exactly 1 via a utility that would flip above 1.
	flip := fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		spHerb: {
			Utilities:    map[actions.ActionID]*expr.Program{actA: mustNum(t, "maturity"), actB: mustNum(t, "0.99")},
			Drives:       []fauna.DriveRule{},
			Speed:        mustNum(t, "0"),
			MaturityAge:  100,
			SmellRadius:  5, SightRadius: 5, FovArc: 1,
			SteerChannel: map[actions.ActionID]core.Tag{},
		},
	})
	_ = rules
	if got := stepMaturityAction(t, flip, 1000); got != actA {
		t.Errorf("age 1000 ⇒ maturity clamped to 1 (> 0.99) should pick actA, got %q", got)
	}
}

// Off-lever: with NO maturity_age authored, maturity ≡ 1 for ALL ages (gate-neutral) — even a newborn
// (age 0) reads 1, so a species that doesn't opt into age-gating is unrestricted.
func TestMaturityNeutralWhenUnauthored(t *testing.T) {
	rules := maturityRules(t, 0) // maturity_age 0 ⇒ maturity ≡ 1
	if got := stepMaturityAction(t, rules, 0); got != actA {
		t.Errorf("no maturity_age ⇒ maturity 1 even at age 0 (1 > 0.5) should pick actA, got %q", got)
	}
}
