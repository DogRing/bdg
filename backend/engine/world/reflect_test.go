package world

import (
	"math"
	"testing"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
)

// FM13 (docs/plans/fauna.md §4.4): an animal whose intent drives it past a world wall has its position
// clamped to the wall AND its heading reflected to point back INTO the map — so it bounces off the edge
// instead of pinning against it and sliding along (the "everyone rubs the wall" bug). Bounds are [0,0]..[20,20].
func TestFM13ReflectAtBoundsBouncesHeadingInward(t *testing.T) {
	fx := newFixtureSeeded(t, 91)
	cfg := testEnvConfig()
	fx.world.InstallFauna(cfg, testFaunaRules(t), testScentEmitters(), nil, []fauna.Animal{
		testAnimal("an:deer", "deer", core.Vec2{X: 18, Y: 10}),
	})

	// Intent: straight +X (heading 0) past the +X wall (NextPos.X = 25 > Max.X 20).
	fx.world.applyAnimalIntent(fauna.Intent{
		Animal: "an:deer", Action: "Forage",
		NextPos: core.Vec2{X: 25, Y: 10}, NextHeading: 0,
		Drives: map[fauna.DriveID]float64{}, Stamina: 1, Vital: 1, VitalCap: 1,
	})

	a := fx.world.animals["an:deer"]
	if a.Pos.X != 20 {
		t.Fatalf("position must clamp to the +X wall (20), got %+v", a.Pos)
	}
	if math.Cos(a.Heading) >= 0 {
		t.Fatalf("heading must reflect to point back inside (−X, cos<0), got heading=%v cos=%v", a.Heading, math.Cos(a.Heading))
	}
}

// FM13 corner: overshooting BOTH walls reflects both heading components (heading points into the −X,−Y quadrant).
func TestFM13ReflectAtBoundsCornerReflectsBothAxes(t *testing.T) {
	fx := newFixtureSeeded(t, 93)
	cfg := testEnvConfig()
	fx.world.InstallFauna(cfg, testFaunaRules(t), testScentEmitters(), nil, []fauna.Animal{
		testAnimal("an:deer", "deer", core.Vec2{X: 19, Y: 19}),
	})
	fx.world.applyAnimalIntent(fauna.Intent{
		Animal: "an:deer", Action: "Forage",
		NextPos: core.Vec2{X: 25, Y: 25}, NextHeading: math.Pi / 4, // +X+Y into the far corner
		Drives: map[fauna.DriveID]float64{}, Stamina: 1, Vital: 1, VitalCap: 1,
	})
	a := fx.world.animals["an:deer"]
	if a.Pos != (core.Vec2{X: 20, Y: 20}) {
		t.Fatalf("position must clamp to the corner (20,20), got %+v", a.Pos)
	}
	if math.Cos(a.Heading) >= 0 || math.Sin(a.Heading) >= 0 {
		t.Fatalf("corner hit must reflect both axes inward (cos<0, sin<0), got heading=%v", a.Heading)
	}
}

// FM13 neutrality: an INTERIOR intent commits position and heading exactly — byte-identical to the old
// clamp path (the reflect helper is a no-op away from the walls).
func TestFM13ReflectInteriorLeavesPositionAndHeadingExact(t *testing.T) {
	fx := newFixtureSeeded(t, 92)
	cfg := testEnvConfig()
	fx.world.InstallFauna(cfg, testFaunaRules(t), testScentEmitters(), nil, []fauna.Animal{
		testAnimal("an:deer", "deer", core.Vec2{X: 10, Y: 10}),
	})
	intended := core.Vec2{X: 12, Y: 13}
	fx.world.applyAnimalIntent(fauna.Intent{
		Animal: "an:deer", Action: "Forage",
		NextPos: intended, NextHeading: 0.7,
		Drives: map[fauna.DriveID]float64{}, Stamina: 1, Vital: 1, VitalCap: 1,
	})
	a := fx.world.animals["an:deer"]
	if a.Pos != intended {
		t.Fatalf("interior move must commit intended pos exactly, got %+v want %+v", a.Pos, intended)
	}
	if a.Heading != 0.7 {
		t.Fatalf("interior move must leave heading exact, got %v want 0.7", a.Heading)
	}
}
