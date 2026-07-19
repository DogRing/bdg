package fauna

import (
	"math"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/space/scent"
	"github.com/dogring/bdg/engine/space/spatial"
)

// fleeRules builds a minimal Rules with a prey species and a predator species —
// sightQuery only needs IsPredator + the sight radius/arc it is passed directly.
func fleeRules() *Rules {
	return NewRules(map[SpeciesID]SpeciesRule{
		"prey": {IsPredator: false, SmellRadius: 5, SightRadius: 10, FovArc: math.Pi},
		"pred": {IsPredator: true, SmellRadius: 5, SightRadius: 10, FovArc: math.Pi},
	})
}

// fleeSnap places the prey + the given predators into a spatial hash and returns a
// minimal Snapshot (sightQuery reads only Animals + Spatial).
func fleeSnap(prey Animal, preds ...Animal) *Snapshot {
	sp := spatial.New(4.0)
	sp.Insert(prey.ID, prey.Pos)
	animals := []Animal{prey}
	for _, p := range preds {
		sp.Insert(p.ID, p.Pos)
		animals = append(animals, p)
	}
	return &Snapshot{Animals: animals, Spatial: sp}
}

// M7: with ≥2 predators simultaneously in FOV, sightQuery aggregates a distance-weighted
// repulsion so the prey flees the resultant threat field (sideways out of a pincer) rather
// than straight away from the single nearest — which would steer it toward the second.
func TestSightQueryPincerAggregatesFlee(t *testing.T) {
	rules := fleeRules()
	prey := Animal{ID: "prey", Species: "prey", Pos: core.Vec2{}, Heading: 0} // facing +x
	// Symmetric pincer: both predators east (in FOV), one NE one SE, equal distance.
	a := Animal{ID: "predA", Species: "pred", Pos: core.Vec2{X: 3, Y: 2}}
	b := Animal{ID: "predB", Species: "pred", Pos: core.Vec2{X: 3, Y: -2}}
	snap := fleeSnap(prey, a, b)

	sightPred, distPred, nearPredPos, fleeDir := sightQuery(prey, snap, rules, 10, math.Pi)

	if sightPred != 1 {
		t.Fatalf("sightPred = %v, want 1 (predators in FOV)", sightPred)
	}
	wantDist := math.Sqrt(13) // both are sqrt(3²+2²) away → nearest is sqrt(13)
	if math.Abs(distPred-wantDist) > 1e-12 {
		t.Errorf("distPred = %v, want nearest %v (fear/flush use nearest, not aggregate)", distPred, wantDist)
	}
	if nearPredPos == nil {
		t.Fatalf("nearPredPos = nil, want the nearest predator position")
	}
	if fleeDir == nil {
		t.Fatalf("fleeDir = nil, want an aggregated direction for 2 visible predators")
	}
	// Resultant of the two symmetric repulsions points due west (−1, 0): straight back
	// between both predators. Away-from-single-nearest would instead point NW or SW.
	if math.Abs(fleeDir.X-(-1)) > 1e-12 || math.Abs(fleeDir.Y-0) > 1e-12 {
		t.Errorf("aggregated fleeDir = %+v, want (-1, 0) (escape between both predators)", *fleeDir)
	}

	// baseSteerDir must return the aggregate when it is present.
	dir := baseSteerDir(prey, TagFleePred, scent.Reading{}, nearPredPos, fleeDir, sightPred, nil, nil)
	if dir != *fleeDir {
		t.Errorf("baseSteerDir = %+v, want the aggregated fleeDir %+v", dir, *fleeDir)
	}
}

// ≤1 visible predator: no aggregate (fleeDir nil), and baseSteerDir reproduces the exact
// pre-M7 away-from-nearest direction — the byte-identical single-target path.
func TestSightQuerySinglePredatorNoAggregate(t *testing.T) {
	rules := fleeRules()
	prey := Animal{ID: "prey", Species: "prey", Pos: core.Vec2{}, Heading: 0}
	pred := Animal{ID: "predA", Species: "pred", Pos: core.Vec2{X: 3, Y: 2}}
	snap := fleeSnap(prey, pred)

	sightPred, _, nearPredPos, fleeDir := sightQuery(prey, snap, rules, 10, math.Pi)
	if fleeDir != nil {
		t.Fatalf("fleeDir = %+v, want nil for a single visible predator", *fleeDir)
	}

	dir := baseSteerDir(prey, TagFleePred, scent.Reading{}, nearPredPos, fleeDir, sightPred, nil, nil)
	// Old formula: normalize(Pos − predPos).
	dx, dy := -3.0, -2.0
	mag := math.Sqrt(dx*dx + dy*dy)
	want := core.Vec2{X: dx / mag, Y: dy / mag}
	if dir != want {
		t.Errorf("single-predator flee dir = %+v, want away-from-nearest %+v (byte-identical)", dir, want)
	}
}

// D12: the repulsion sum accumulates in NearbyEntities' ObjectID order regardless of insert
// order, so the aggregated fleeDir is insert-order independent (deterministic).
func TestSightQueryPincerInsertOrderDeterministic(t *testing.T) {
	rules := fleeRules()
	prey := Animal{ID: "prey", Species: "prey", Pos: core.Vec2{}, Heading: 0}
	a := Animal{ID: "predA", Species: "pred", Pos: core.Vec2{X: 4, Y: 1}}
	b := Animal{ID: "predB", Species: "pred", Pos: core.Vec2{X: 2, Y: -3}}

	_, _, _, d1 := sightQuery(prey, fleeSnap(prey, a, b), rules, 10, math.Pi)
	_, _, _, d2 := sightQuery(prey, fleeSnap(prey, b, a), rules, 10, math.Pi)
	if d1 == nil || d2 == nil {
		t.Fatalf("expected aggregated fleeDir for 2 predators, got %v / %v", d1, d2)
	}
	if *d1 != *d2 {
		t.Errorf("fleeDir depends on insert order: %+v vs %+v (D12 violation)", *d1, *d2)
	}
}

// A symmetric head-on pincer (predators on exactly opposite sides, equal distance) sums to
// ~0 — the aggregate is degenerate, so we fall back to away-from-nearest rather than emit a
// zero direction. fleeDir must be nil and baseSteerDir must still produce a unit flee vector.
func TestSightQueryDegenerateCancelFallsBackToNearest(t *testing.T) {
	rules := fleeRules()
	prey := Animal{ID: "prey", Species: "prey", Pos: core.Vec2{}, Heading: 0}
	a := Animal{ID: "predA", Species: "pred", Pos: core.Vec2{X: 3, Y: 0}}
	b := Animal{ID: "predB", Species: "pred", Pos: core.Vec2{X: -3, Y: 0}}
	snap := fleeSnap(prey, a, b)

	sightPred, _, nearPredPos, fleeDir := sightQuery(prey, snap, rules, 10, math.Pi)
	if fleeDir != nil {
		t.Fatalf("fleeDir = %+v, want nil (opposite repulsions cancel)", *fleeDir)
	}
	dir := baseSteerDir(prey, TagFleePred, scent.Reading{}, nearPredPos, fleeDir, sightPred, nil, nil)
	if math.Abs(math.Hypot(dir.X, dir.Y)-1) > 1e-12 {
		t.Errorf("fallback flee dir = %+v, want a unit vector away from nearest", dir)
	}
}
