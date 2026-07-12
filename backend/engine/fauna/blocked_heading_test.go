package fauna_test

import (
	"testing"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
)

// FM13 (docs/plans/fauna.md §4.4): when the tentative step lands on impassable terrain, the animal HOLDS
// position but still COMMITS the turned heading (wander + any blend already applied), instead of freezing
// its old heading and re-proposing the same blocked step forever (the "pinned against the obstacle" bug).
// Position is held identically to before; only the heading is no longer frozen — and it equals exactly the
// heading the same animal would have turned to on open terrain (same seed ⇒ same wander draw).
func TestFM13BlockedTerrainHoldsPositionButCommitsHeading(t *testing.T) {
	rules := hazardRules(t) // bare +X wanderer, speed 1, MoveTo continues along heading

	// Reference on open terrain: the turned heading (and a real move).
	free := stepWander(t, rules, nil)
	if free.NextHeading == 0 {
		t.Fatalf("precondition: the open-terrain wander should turn the heading off 0, got 0")
	}

	// Same wanderer, but terrain is impassable everywhere ⇒ the step is blocked.
	blockedAll := mockTerrain{
		blocked:  func(core.Vec2) bool { return true },
		terrainF: func(core.Vec2) core.Tag { return "soil" },
		costF:    func(core.Vec2) float64 { return 1.0 },
	}
	a := herbAnimal("h1", core.Vec2{X: 50, Y: 50}, map[fauna.DriveID]float64{})
	a.Heading = 0
	a.ActiveUntil = 1000
	a.CurrentAction = actMoveTo
	snap := makeSnap([]fauna.Animal{a}, nil, nil, blockedAll, 1, emptyEnv("h1"))
	got := fauna.Step(snap, rules, rng.New(1))[0]

	if got.NextPos != (core.Vec2{X: 50, Y: 50}) {
		t.Errorf("blocked terrain must HOLD position, got %+v", got.NextPos)
	}
	if got.NextHeading == a.Heading {
		t.Errorf("blocked step must COMMIT the turned heading, not freeze at %v (FM13)", a.Heading)
	}
	if got.NextHeading != free.NextHeading {
		t.Errorf("blocked heading must equal the unblocked turned heading (only position held): blocked=%v free=%v",
			got.NextHeading, free.NextHeading)
	}
}
