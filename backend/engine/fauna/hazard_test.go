package fauna_test

import (
	"testing"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/actions"
)

// mockHazard is a constant HazardSampler — a fixed repulsion vector (away-direction ×
// intensity), isolating the steer-blend arithmetic from field/navmap construction (P_move1/FM5).
type mockHazard struct{ rep core.Vec2 }

func (m mockHazard) Repulsion(core.Vec2) core.Vec2 { return m.rep }

const actMoveTo actions.ActionID = "MoveTo"

// hazardRules is a bare wanderer (MoveTo continues along heading — no steer channel)
// with a hazard-repulsion multiplier e = 2.
func hazardRules(t *testing.T) *fauna.Rules {
	t.Helper()
	return fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		spHerb: {
			Utilities:       map[actions.ActionID]*expr.Program{actMoveTo: mustNum(t, "0.1")},
			Drives:          []fauna.DriveRule{},
			Speed:           mustNum(t, "1"),
			HazardAvoidance: 2.0,
			SmellRadius:     5, SightRadius: 5, FovArc: 1,
			SteerChannel: map[actions.ActionID]core.Tag{}, // MoveTo absent ⇒ continue heading
		},
	})
}

// stepWander runs one tick for a lone heading-+X wanderer at (50,50) and returns its
// intent. hazard nil ⇒ no field injected. Same seed ⇒ the wander draw is identical
// across calls, so any NextPos difference is the hazard blend alone.
func stepWander(t *testing.T, rules *fauna.Rules, hazard fauna.HazardSampler) fauna.Intent {
	t.Helper()
	a := herbAnimal("h1", core.Vec2{X: 50, Y: 50}, map[fauna.DriveID]float64{})
	a.Heading = 0 // +X, toward a hazard on the right
	a.ActiveUntil = 1000
	a.CurrentAction = actMoveTo
	snap := makeSnap([]fauna.Animal{a}, nil, nil, openTerrain, 1, emptyEnv("h1"))
	if hazard != nil {
		snap.HazardField = hazard
	}
	return fauna.Step(snap, rules, rng.New(1))[0]
}

// A hazard whose repulsion points −X (away from a +X danger) bends a +X-heading
// wanderer's next step back toward −X — it does not walk into the danger (P_move1/FM5).
func TestHazardBlendBendsAwayFromDanger(t *testing.T) {
	rules := hazardRules(t)
	without := stepWander(t, rules, nil)
	with := stepWander(t, rules, mockHazard{rep: core.Vec2{X: -1, Y: 0}}) // strong away-push (−X)

	if !(with.NextPos.X < without.NextPos.X) {
		t.Errorf("hazard repulsion should bend NextPos toward −X (away): with.X=%.4f without.X=%.4f",
			with.NextPos.X, without.NextPos.X)
	}
	if with.NextPos.X >= 50 {
		t.Errorf("a strong away-push should move the wanderer to NextPos.X < 50, got %.4f", with.NextPos.X)
	}
}

// Off-levers: a zero repulsion (safe/flat field) and a nil field both leave the step
// byte-identical to the no-hazard baseline (same seed ⇒ same wander draw).
func TestHazardBlendOffLevers(t *testing.T) {
	rules := hazardRules(t)
	base := stepWander(t, rules, nil)

	flat := stepWander(t, rules, mockHazard{rep: core.Vec2{}}) // safe cell ⇒ zero repulsion
	if flat.NextPos != base.NextPos {
		t.Errorf("zero repulsion must not bend: %+v vs baseline %+v", flat.NextPos, base.NextPos)
	}
}
