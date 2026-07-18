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

// PD3 grace+bleed (P_fa4b, docs/plans/fauna.md §5). A lone wanderer whose hunger drive optionally couples
// to Vital via VitalDrain (r) / VitalDrainAbove (θ): while hunger ≥ θ the proposed Vital is regen − r.
// Rate is tiny so the START-OF-TICK hunger (what starveDrain reads) is essentially the value we set, and
// Speed 0 removes locomotion noise. combatParams() gives VitalRegenPerTick 0.001, DT 1 (see combat_test).
func starveRules(t *testing.T, drain, above float64) *fauna.Rules {
	t.Helper()
	return fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		spHerb: {
			Utilities:    map[actions.ActionID]*expr.Program{actMoveTo: mustNum(t, "0.1")},
			Drives:       []fauna.DriveRule{{ID: "hunger", Rate: 0.0001, VitalDrain: drain, VitalDrainAbove: above}},
			Speed:        mustNum(t, "0"),
			SmellRadius:  5, SightRadius: 5, FovArc: 1,
			SteerChannel: map[actions.ActionID]core.Tag{},
		},
	})
}

// stepStarverVital runs one ACTIVE tick for a lone herbivore with the given Vital + hunger and returns its
// proposed next Vital (Intent.Vital = nextVital).
func stepStarverVital(t *testing.T, rules *fauna.Rules, vital, hunger float64) float64 {
	t.Helper()
	a := herbAnimal("s1", core.Vec2{X: 50, Y: 50}, map[fauna.DriveID]float64{"hunger": hunger})
	a.Vital = vital
	a.VitalCap = 1
	a.ActiveUntil = 1000
	a.CurrentAction = actMoveTo
	snap := withCombatParams(makeSnap([]fauna.Animal{a}, nil, nil, openTerrain, 1, emptyEnv("s1")))
	return fauna.Step(snap, rules, rng.New(1))[0].Vital
}

const (
	starveRate  = 0.006 // r
	starveTheta = 0.98  // θ
	vitalRegen  = 0.001 // combatParams().VitalRegenPerTick
)

// A SATURATED hunger (≥ θ) bleeds Vital: the proposed Vital is regen − r, strictly BELOW the starting
// Vital (net negative since r > regen). This is the whole point — a starving animal loses condition.
func TestStarveSaturatedHungerBleeds(t *testing.T) {
	rules := starveRules(t, starveRate, starveTheta)
	got := stepStarverVital(t, rules, 0.5, 0.99)
	want := 0.5 + vitalRegen - starveRate
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("saturated hunger should bleed to regen−r: got %.6f, want %.6f", got, want)
	}
	if got >= 0.5 {
		t.Errorf("a starving animal must LOSE Vital (net drain > regen): got %.6f ≥ start 0.5", got)
	}
}

// A FED animal (hunger < θ) does NOT bleed — it regenerates (regen only), so its proposed Vital rises
// above the start. The grace is exactly this: until hunger climbs to θ, no vital cost.
func TestStarveFedRegensNoBleed(t *testing.T) {
	rules := starveRules(t, starveRate, starveTheta)
	got := stepStarverVital(t, rules, 0.5, 0.5) // hunger 0.5 < θ 0.98
	want := 0.5 + vitalRegen
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("fed animal should regen only (no drain): got %.6f, want %.6f", got, want)
	}
	if got <= 0.5 {
		t.Errorf("a fed animal should GAIN Vital: got %.6f ≤ start 0.5", got)
	}
}

// The threshold is inclusive (≥ θ): hunger exactly at θ bleeds; hunger just below θ does not.
func TestStarveThresholdBoundary(t *testing.T) {
	rules := starveRules(t, starveRate, starveTheta)
	atTheta := stepStarverVital(t, rules, 0.5, starveTheta)
	justBelow := stepStarverVital(t, rules, 0.5, starveTheta-0.001)
	if atTheta >= 0.5 {
		t.Errorf("hunger == θ should bleed (inclusive ≥): got %.6f", atTheta)
	}
	if justBelow <= 0.5 {
		t.Errorf("hunger just below θ should NOT bleed: got %.6f", justBelow)
	}
}

// Off-lever byte-identity: VitalDrain 0 (and a negative sentinel) is the neutral path — proposed Vital is
// pure regen regardless of hunger, byte-identical to a pre-P_fa4b animal. So a species opts IN only by
// authoring a positive VitalDrain, and every existing Vital golden holds.
func TestStarveOffLeverByteIdentical(t *testing.T) {
	regenOnly := 0.5 + vitalRegen
	zero := stepStarverVital(t, starveRules(t, 0, starveTheta), 0.5, 0.99)      // saturated but drain 0
	neg := stepStarverVital(t, starveRules(t, -0.01, starveTheta), 0.5, 0.99)   // negative sentinel
	if math.Abs(zero-regenOnly) > 1e-12 {
		t.Errorf("VitalDrain 0 must be regen-only even at saturated hunger: got %.9f, want %.9f", zero, regenOnly)
	}
	if zero != neg {
		t.Errorf("negative VitalDrain must be byte-identical to 0 (clamped off): %.12f vs %.12f", neg, zero)
	}
}

// Vital is clamped at 0 — the bleed cannot drive it negative (world removes the animal at ≤ 0).
func TestStarveClampsAtZero(t *testing.T) {
	rules := starveRules(t, starveRate, starveTheta)
	got := stepStarverVital(t, rules, 0.003, 0.99) // 0.003 + 0.001 − 0.006 = −0.002 → clamp 0
	if got != 0 {
		t.Errorf("bleed must clamp Vital at 0, not go negative: got %.6f", got)
	}
}

// Determinism (D12): the bleed is pure arithmetic over start-of-tick Drives + params — no RNG of its own —
// so identical inputs/seed give the identical proposed Vital every run.
func TestStarveDeterministic(t *testing.T) {
	rules := starveRules(t, starveRate, starveTheta)
	a := stepStarverVital(t, rules, 0.42, 0.985)
	b := stepStarverVital(t, rules, 0.42, 0.985)
	if a != b {
		t.Errorf("same inputs/seed must give identical Vital: %.12f vs %.12f", a, b)
	}
}
