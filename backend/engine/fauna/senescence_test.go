package fauna_test

import (
	"math"
	"testing"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/actions"
)

// PD11 / §7 aging. `senescence` is the falling limb of the Age axis that `maturity` opens:
// clamp01((age − prime_age)/(lifespan − prime_age)). These tests exercise it end-to-end through Step —
// the operand is read by a §6 `speed` program, which is exactly how content declares WHAT ages (D10).

// senescenceRules builds a species whose speed is `1 - senescence`, so the measured NextPos step size
// reads the operand directly: prime animal moves 1.0/tick, fully senescent one 0.0.
func senescenceRules(t *testing.T, primeAge, lifespan float64) *fauna.Rules {
	t.Helper()
	return fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		spHerb: {
			Utilities:    map[actions.ActionID]*expr.Program{actA: mustNum(t, "1")},
			Drives:       []fauna.DriveRule{},
			Speed:        mustNum(t, "1 - senescence"),
			PrimeAge:     primeAge,
			Lifespan:     lifespan,
			SmellRadius:  5,
			SightRadius:  5,
			FovArc:       1,
			SteerChannel: map[actions.ActionID]core.Tag{},
		},
	})
}

// stepSpeed returns the distance an animal of the given age actually moves in one tick.
func stepSpeed(t *testing.T, rules *fauna.Rules, age float64) float64 {
	t.Helper()
	a := herbAnimal("s1", core.Vec2{X: 50, Y: 50}, map[fauna.DriveID]float64{})
	a.Age = age
	a.ActiveUntil = 1000
	a.CurrentAction = actA
	snap := makeSnap([]fauna.Animal{a}, nil, nil, openTerrain, 1, emptyEnv("s1"))
	intent := fauna.Step(snap, rules, rng.New(1))[0]
	return intent.NextPos.Distance(core.Vec2{X: 50, Y: 50})
}

// The curve: flat 0 through youth AND prime, then a linear ramp to 1 at lifespan. The flat head is the
// whole point of computing this in Go instead of authoring `age/lifespan` in §6 — a bare ratio would
// start decaying an animal the day it was born.
func TestSenescenceIsFlatThroughPrimeThenRamps(t *testing.T) {
	rules := senescenceRules(t, 100, 200) // senescence = clamp01((age-100)/100)
	cases := []struct {
		age  float64
		want float64 // speed = 1 - senescence
	}{
		{0, 1.0},    // newborn
		{50, 1.0},   // youth — no decline
		{100, 1.0},  // exactly at prime_age: the ramp starts here, still 0
		{150, 0.5},  // half way down the ramp
		{200, 0.0},  // lifespan: fully senescent
		{1000, 0.0}, // clamped — an animal past its lifespan does not go negative
	}
	for _, c := range cases {
		if got := stepSpeed(t, rules, c.age); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("age %g: speed %.6f, want %.6f (senescence %.2f)", c.age, got, 1-c.want, 1-c.want)
		}
	}
}

// The off-lever: a species that authors no lifespan does not age at all, and reads a constant 0 — so
// every pre-PD11 world is byte-identical. This is what lets aging ship without re-baselining goldens.
func TestSenescenceOffWithoutLifespan(t *testing.T) {
	rules := senescenceRules(t, 0, 0)
	for _, age := range []float64{0, 500, 100000} {
		if got := stepSpeed(t, rules, age); math.Abs(got-1.0) > 1e-9 {
			t.Errorf("age %g with no lifespan authored: speed %.6f, want 1.0 (senescence must be 0)", age, got)
		}
	}
}

// Old age is a MORTALITY channel, not just a slowdown (PD11-ii): past the θ threshold the animal's Vital
// bleeds, reusing the exact (θ, r) shape starvation uses. Below θ it is merely declining, not dying —
// which is the distinction that keeps "an old animal" and "a dying animal" separate states.
func TestSenescenceBleedsVitalOnlyPastThreshold(t *testing.T) {
	rules := fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		spHerb: {
			Utilities:                 map[actions.ActionID]*expr.Program{actA: mustNum(t, "1")},
			Drives:                    []fauna.DriveRule{},
			Speed:                     mustNum(t, "0"),
			PrimeAge:                  100,
			Lifespan:                  200,
			SenescenceVitalDrain:      0.01,
			SenescenceVitalDrainAbove: 0.5, // bleeds from age 150
			SmellRadius:               5,
			SightRadius:               5,
			FovArc:                    1,
			SteerChannel:              map[actions.ActionID]core.Tag{},
		},
	})
	vitalAfterTick := func(age float64) float64 {
		a := herbAnimal("v1", core.Vec2{X: 50, Y: 50}, map[fauna.DriveID]float64{})
		a.Age, a.Vital, a.VitalCap, a.ActiveUntil, a.CurrentAction = age, 1.0, 1.0, 1000, actA
		snap := makeSnap([]fauna.Animal{a}, nil, nil, openTerrain, 1, emptyEnv("v1"))
		return fauna.Step(snap, rules, rng.New(1))[0].Vital
	}
	// Below θ: no bleed (Vital is already capped at 1, so it simply stays there).
	if got := vitalAfterTick(140); got < 1.0 {
		t.Errorf("age 140 (senescence 0.4 < θ 0.5) lost Vital (%.4f) — the bleed started too early", got)
	}
	// Past θ: bleeding. The species has no VitalRegenPerTick configured in this snapshot, so the drop is
	// the raw drain.
	if got := vitalAfterTick(160); got >= 1.0 {
		t.Errorf("age 160 (senescence 0.6 ≥ θ 0.5) did NOT bleed Vital (%.4f) — old age is not killing", got)
	}
}

// Attribution, not precedence (PD11-ii): the world labels a non-combat death by the LARGER of the two
// bleeds, so adding aging can never be misread as a famine in the telemetry. VitalDrains is the split it
// reads. A tie keeps starvation, the pre-existing label.
func TestVitalDrainsSplitsTheTwoChannels(t *testing.T) {
	rules := fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		spHerb: {
			Utilities:                 map[actions.ActionID]*expr.Program{actA: mustNum(t, "1")},
			Drives:                    []fauna.DriveRule{{ID: "hunger", Rate: 0.001, VitalDrain: 0.006, VitalDrainAbove: 0.9}},
			Speed:                     mustNum(t, "0"),
			PrimeAge:                  100,
			Lifespan:                  200,
			SenescenceVitalDrain:      0.004,
			SenescenceVitalDrainAbove: 0.5,
			SmellRadius:               5, SightRadius: 5, FovArc: 1,
			SteerChannel: map[actions.ActionID]core.Tag{},
		},
	})
	drains := func(age, hunger float64) (float64, float64) {
		a := herbAnimal("d1", core.Vec2{X: 50, Y: 50}, map[fauna.DriveID]float64{"hunger": hunger})
		a.Age = age
		return rules.VitalDrains(a, 1)
	}
	// Young and starving → starvation is the only channel.
	if starve, senesce := drains(50, 1.0); starve <= 0 || senesce != 0 {
		t.Errorf("young starving animal: drains = (%.4f, %.4f), want starvation only", starve, senesce)
	}
	// Old and fed → senescence is the only channel.
	if starve, senesce := drains(180, 0.1); starve != 0 || senesce <= 0 {
		t.Errorf("old fed animal: drains = (%.4f, %.4f), want senescence only", starve, senesce)
	}
	// Old AND starving → both bleed, and they SUM in nextVital (an old starving animal dies faster than
	// either alone). The larger one wins the label; here starvation does, honestly.
	starve, senesce := drains(180, 1.0)
	if starve <= 0 || senesce <= 0 {
		t.Fatalf("old starving animal: drains = (%.4f, %.4f), want both channels bleeding", starve, senesce)
	}
	if senesce >= starve {
		t.Errorf("old starving animal: senescence %.4f ≥ starvation %.4f — this content authors starvation "+
			"as the sharper channel, so the death must be labelled starvation", senesce, starve)
	}
}
