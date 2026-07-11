package fauna_test

import (
	"testing"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/actions"
)

const actDrink actions.ActionID = "Drink"

// mockWater is a constant WaterSampler — a fixed attraction gradient (unit toward water) — isolating the
// thirst-seek steer arithmetic from field/navmap construction (FM4), like mockHazard for the hazard blend.
type mockWater struct{ grad core.Vec2 }

func (m mockWater) Gradient(core.Vec2) core.Vec2 { return m.grad }

// waterRules: a species whose Drink action is thirst-gated ("thirst * 5") and steers on the seek:water
// channel; MoveTo is the wander baseline. So a thirsty animal picks Drink (⇒ water steer) while a neutral
// one picks MoveTo (⇒ continue heading, no water pull). `thirst` is carried on the Animal (no DriveRule, so
// DriveUpdate preserves it unchanged — a rate-0/no-fear rule would be misread as a thermal drive).
func waterRules(t *testing.T) *fauna.Rules {
	t.Helper()
	return fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		spHerb: {
			Utilities: map[actions.ActionID]*expr.Program{
				actDrink:  mustNum(t, "thirst * 5"),
				actMoveTo: mustNum(t, "0.1"),
			},
			Drives:      []fauna.DriveRule{},
			Speed:       mustNum(t, "1"),
			SmellRadius: 5, SightRadius: 5, FovArc: 1,
			SteerChannel: map[actions.ActionID]core.Tag{actDrink: fauna.TagSteerWater},
		},
	})
}

// stepDrinker runs one ACTIVE tick for a lone +X-heading animal at (50,50) with the given thirst and water
// field, returning its intent. Same seed ⇒ identical wander draw, so any NextPos change is the water steer.
func stepDrinker(t *testing.T, rules *fauna.Rules, thirst float64, water fauna.WaterSampler) fauna.Intent {
	t.Helper()
	a := herbAnimal("d1", core.Vec2{X: 50, Y: 50}, map[fauna.DriveID]float64{"thirst": thirst})
	a.Heading = 0 // +X: heads AWAY from water placed on the −X side
	a.ActiveUntil = 1000
	a.CurrentAction = actDrink
	snap := makeSnap([]fauna.Animal{a}, nil, nil, openTerrain, 1, emptyEnv("d1"))
	snap.WaterField = water
	return fauna.Step(snap, rules, rng.New(1))[0]
}

// FM4: a THIRSTY animal picks Drink and steers toward the water gradient (−X here), bending its +X heading
// back west toward water.
func TestThirstySteersTowardWater(t *testing.T) {
	rules := waterRules(t)
	west := mockWater{grad: core.Vec2{X: -1, Y: 0}} // water to the −X
	thirsty := stepDrinker(t, rules, 1.0, west)
	if thirsty.Action != actDrink {
		t.Fatalf("thirst 1.0 should pick Drink, got %q", thirsty.Action)
	}
	if thirsty.NextPos.X >= 50 {
		t.Errorf("a thirsty drinker should steer toward water (−X): NextPos.X=%.4f (want <50)", thirsty.NextPos.X)
	}
}

// FM4: a NON-thirsty animal picks the wander baseline (not Drink) and is NOT pulled toward water even with
// the SAME field present — the field is queried only for the seek:water channel.
func TestNeutralNotPulledToWater(t *testing.T) {
	rules := waterRules(t)
	west := mockWater{grad: core.Vec2{X: -1, Y: 0}}
	neutral := stepDrinker(t, rules, 0.0, west)
	if neutral.Action == actDrink {
		t.Fatalf("thirst 0.0 should NOT pick Drink, got %q", neutral.Action)
	}
	if neutral.NextPos.X <= 50 {
		t.Errorf("a non-thirsty wanderer should keep heading +X (unpulled): NextPos.X=%.4f (want >50)", neutral.NextPos.X)
	}
}

// FM4 off-lever: a thirsty animal with NO water field (nil) falls through to continue-heading — no spurious
// pull, byte-identical to a pre-FM4 wander.
func TestThirstyNoFieldNoPull(t *testing.T) {
	rules := waterRules(t)
	got := stepDrinker(t, rules, 1.0, nil)
	if got.NextPos.X <= 50 {
		t.Errorf("no water field ⇒ Drink falls through to +X heading: NextPos.X=%.4f (want >50)", got.NextPos.X)
	}
}
