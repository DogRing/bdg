package fauna

import (
	"math"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/space/scent"
)

// M3 cover-seeking steer (`seek:cover`). Before it existed, hiding required "fleeing AND already
// within cover reach", and those are anti-correlated — flee steers straight away from the predator,
// so the one moment the hide roll can fire is the moment the animal is leaving its bush. Measured on
// the balance bed: near cover 10.1% of prey-ticks, fleeing 2.1%, both at once 0.01%.

// A cover-seeking animal steers straight at the bush, not away from the predator.
func TestSeekCoverSteersTowardCover(t *testing.T) {
	a := Animal{ID: "an:rabbit", Species: "rabbit", Pos: core.Vec2{X: 0, Y: 0}, Heading: 0}
	// Predator to the EAST, cover to the NORTH: plain flight would go west, cover-seeking goes north.
	predator := core.Vec2{X: 10, Y: 0}
	cover := core.Vec2{X: 0, Y: 10}

	dir := baseSteerDir(a, TagSteerCover, scent.Reading{}, &predator, nil, 1, nil, nil, &cover)

	if math.Abs(dir.X) > 1e-9 || math.Abs(dir.Y-1) > 1e-9 {
		t.Errorf("cover steer = %+v, want the unit vector toward the cover at %+v (0,1)", dir, cover)
	}
}

// With no cover in range, choosing to hide must NOT be worse than plain flight: the steer falls back
// to the away-from-threat direction rather than holding heading. Without this, content would need a
// cover-proximity operand just to know when the action is safe to pick.
func TestSeekCoverWithoutCoverFallsBackToFleeing(t *testing.T) {
	a := Animal{ID: "an:rabbit", Species: "rabbit", Pos: core.Vec2{X: 0, Y: 0}, Heading: 0}
	predator := core.Vec2{X: 10, Y: 0} // due east ⇒ flight is due west

	cover := baseSteerDir(a, TagSteerCover, scent.Reading{}, &predator, nil, 1, nil, nil, nil)
	flee := baseSteerDir(a, TagFleePred, scent.Reading{}, &predator, nil, 1, nil, nil, nil)

	if cover != flee {
		t.Errorf("cover steer with no cover = %+v, want the flee direction %+v", cover, flee)
	}
	if math.Abs(cover.X+1) > 1e-9 || math.Abs(cover.Y) > 1e-9 {
		t.Errorf("fallback = %+v, want due west (-1,0) away from the predator", cover)
	}
}

// Off-lever: a species that authors no cover-seeking action is untouched — every other steer channel
// ignores the cover argument entirely.
func TestCoverArgumentDoesNotDisturbOtherChannels(t *testing.T) {
	a := Animal{ID: "an:deer", Species: "deer", Pos: core.Vec2{X: 0, Y: 0}, Heading: 0.7}
	cover := core.Vec2{X: 0, Y: 10}
	reading := scent.Reading{}
	reading.Food.Intensity = 1
	reading.Food.Dir = core.Vec2{X: 1, Y: 0}

	withCover := baseSteerDir(a, TagSteerFood, reading, nil, nil, 0, nil, nil, &cover)
	without := baseSteerDir(a, TagSteerFood, reading, nil, nil, 0, nil, nil, nil)

	if withCover != without {
		t.Errorf("food steer changed when a cover position was supplied: %+v vs %+v", withCover, without)
	}
}
