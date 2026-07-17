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

// PD1 scent-tracking confidence (P_fa4a, docs/plans/fauna.md §5). A grazer that ALWAYS picks Graze
// (seek:food ⇒ TagSteerFood) so the homing channel is exercised, with a controllable §6 scent_acuity
// keenness gain. Speed fixed at 1, no turn-rate clamp, no hazard field ⇒ the ONLY thing bending the steer
// off the exact scent Dir is the confidence blend. acuity "" ⇒ no program (the neutral off-lever).
func scentAcuityRules(t *testing.T, acuity string) *fauna.Rules {
	t.Helper()
	sr := fauna.SpeciesRule{
		Utilities: map[actions.ActionID]*expr.Program{
			actGraze:  mustNum(t, "1"), // always wins ⇒ steer channel is seek:food
			actMoveTo: mustNum(t, "0.1"),
		},
		Drives:      []fauna.DriveRule{},
		Speed:       mustNum(t, "1"),
		SmellRadius: 6, SightRadius: 4, FovArc: 1,
		SteerChannel: map[actions.ActionID]core.Tag{actGraze: fauna.TagSteerFood},
	}
	if acuity != "" {
		sr.ScentAcuity = mustNum(t, acuity, "Perception")
	}
	return fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{spHerb: sr})
}

// stepGrazerToFood runs one ACTIVE tick for a +Y-heading (π/2) grazer at (50,50) with food scent deposited
// to its EAST (+X). Food due east ⇒ the scent Dir ≈ +X (angle 0); the heading is +Y (angle π/2), so the
// blended steer angle atan2(dir) lies in (0, π/2): closer to 0 = the animal TRUSTS the scent (homes),
// closer to π/2 = it wanders (keeps heading, can't localize). Same seed ⇒ identical wander jitter is ADDED
// to every run, so comparing NextHeading across runs isolates the blend. `perception` sets the animal's
// Perception stat (for the §6 stat-composition case); 0 leaves it absent (⇒ Stat 0).
func stepGrazerToFood(t *testing.T, rules *fauna.Rules, foodAmount, perception float64) fauna.Intent {
	t.Helper()
	a := herbAnimal("g1", core.Vec2{X: 50, Y: 50}, map[fauna.DriveID]float64{})
	a.Heading = math.Pi / 2 // +Y (north): perpendicular to the +X scent so the blend angle is legible
	a.ActiveUntil = 1000
	a.CurrentAction = actGraze
	if perception != 0 {
		a.Stats["Perception"] = perception
	}
	sg := scent.New(1.0)
	sg.Deposit(scent.ChanFood, core.Vec2{X: 54, Y: 50}, foodAmount) // due east ⇒ Dir ≈ +X
	sg.Commit()
	snap := makeSnap([]fauna.Animal{a}, sg, nil, openTerrain, 1, emptyEnv("g1"))
	return fauna.Step(snap, rules, rng.New(1))[0]
}

// Neutral off-lever: with NO scent_acuity authored the blend is skipped ⇒ the animal homes on the EXACT
// scent Dir (heads east toward the food, byte-identical to pre-PD1). A blended animal (positive gain, finite
// intensity) can only be rotated AWAY from the scent, so its steer angle is strictly larger.
func TestScentAcuityNeutralHomesExactly(t *testing.T) {
	neutral := stepGrazerToFood(t, scentAcuityRules(t, ""), 2.0, 0)
	if neutral.NextPos.X <= 50 {
		t.Fatalf("neutral (no scent_acuity) must home east toward food: NextPos=%+v (want X>50)", neutral.NextPos)
	}
	blended := stepGrazerToFood(t, scentAcuityRules(t, "10"), 2.0, 0)
	// Same seed ⇒ same added wander; the blend rotates the steer toward the +Y heading, so its heading angle
	// is strictly greater than the exact-scent (neutral) heading.
	if blended.NextHeading <= neutral.NextHeading {
		t.Errorf("a finite-gain blend must steer LESS directly at food than the exact-scent neutral: blended h=%.4f, neutral h=%.4f", blended.NextHeading, neutral.NextHeading)
	}
}

// A FAINT scent gives a less-trusted direction than a STRONG one (same species/gain): faint ⇒ the steer
// leans toward the heading (wander); strong ⇒ it leans toward the scent (homes). This is the refugia lever —
// a predator cannot beeline onto a far/faint prey scent.
func TestScentAcuityFaintScentWandersStrongHomes(t *testing.T) {
	rules := scentAcuityRules(t, "10")
	faint := stepGrazerToFood(t, rules, 0.03, 0)
	strong := stepGrazerToFood(t, rules, 5.0, 0)
	// Heading angle in (0,π/2): smaller = more toward the +X scent. Strong must be the smaller angle.
	if strong.NextHeading >= faint.NextHeading {
		t.Errorf("strong scent should home more (smaller heading angle) than faint: strong h=%.4f, faint h=%.4f", strong.NextHeading, faint.NextHeading)
	}
	if strong.NextPos.X <= faint.NextPos.X {
		t.Errorf("strong scent should carry the grazer further EAST (toward food) than faint: strong X=%.4f, faint X=%.4f", strong.NextPos.X, faint.NextPos.X)
	}
}

// A KEENER nose (higher gain g) trusts the SAME faint scent more — at a fixed intensity, higher g ⇒ higher
// confidence ⇒ the steer leans further toward the scent. This is why a wolf out-tracks a bear on a faint
// trail (the g differentiation, PD1b §6 stat composition).
func TestScentAcuityKeenerNoseTrustsFaintScent(t *testing.T) {
	faint := 0.05
	keen := stepGrazerToFood(t, scentAcuityRules(t, "60"), faint, 0)
	dull := stepGrazerToFood(t, scentAcuityRules(t, "3"), faint, 0)
	if keen.NextHeading >= dull.NextHeading {
		t.Errorf("a keener nose (g=60) should home more on a faint scent than a dull one (g=3): keen h=%.4f, dull h=%.4f", keen.NextHeading, dull.NextHeading)
	}
}

// PD1b: the gain is §6-composed from base stats (D7). With scent_acuity "Perception*0.2", an animal with a
// higher Perception has a higher gain ⇒ homes more on the same faint scent — competence EMERGES from the
// base attribute, it is not a stored per-animal number.
func TestScentAcuityComposesPerceptionStat(t *testing.T) {
	rules := scentAcuityRules(t, "Perception*0.2")
	faint := 0.05
	sharp := stepGrazerToFood(t, rules, faint, 60) // g = 12
	dim := stepGrazerToFood(t, rules, faint, 5)    // g = 1
	if sharp.NextHeading >= dim.NextHeading {
		t.Errorf("higher Perception ⇒ keener nose ⇒ homes more: Perception60 h=%.4f, Perception5 h=%.4f", sharp.NextHeading, dim.NextHeading)
	}
}

// Off-lever byte-identity: an absent program, a zero gain, and a negative gain are ALL the neutral path
// (exact scent Dir) — same seed ⇒ identical intent. So a species opts IN by authoring a positive gain and
// every pre-PD1 golden holds.
func TestScentAcuityOffLeverByteIdentical(t *testing.T) {
	absent := stepGrazerToFood(t, scentAcuityRules(t, ""), 2.0, 0)
	zero := stepGrazerToFood(t, scentAcuityRules(t, "0"), 2.0, 0)
	neg := stepGrazerToFood(t, scentAcuityRules(t, "0 - 5"), 2.0, 0)
	if absent.NextPos != zero.NextPos || absent.NextHeading != zero.NextHeading {
		t.Errorf("gain 0 must be byte-identical to no program: %+v vs %+v", zero, absent)
	}
	if absent.NextPos != neg.NextPos || absent.NextHeading != neg.NextHeading {
		t.Errorf("negative gain must be byte-identical to no program (clamped off): %+v vs %+v", neg, absent)
	}
}

// Determinism (D12): the blend is pure arithmetic over the committed reading + §6 — no RNG of its own — so
// the same inputs and seed produce the identical steer every run.
func TestScentAcuityDeterministic(t *testing.T) {
	rules := scentAcuityRules(t, "12")
	a := stepGrazerToFood(t, rules, 0.2, 0)
	b := stepGrazerToFood(t, rules, 0.2, 0)
	if a.NextPos != b.NextPos || a.NextHeading != b.NextHeading {
		t.Errorf("same seed/inputs must give identical steer: %+v vs %+v", a, b)
	}
}
