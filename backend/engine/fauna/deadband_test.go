package fauna_test

import (
	"testing"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/actions"
)

// deadbandRules is a lone wanderer whose §6 speed is a FIXED constant, so the FM9
// locomotion deadband (docs/plans/fauna.md §4.3) can be exercised against a known
// speed. MoveTo has no steer channel ⇒ continues along heading (no drive coupling).
func deadbandRules(t *testing.T, speed string) *fauna.Rules {
	t.Helper()
	return fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		spHerb: {
			Utilities:   map[actions.ActionID]*expr.Program{actMoveTo: mustNum(t, "0.1")},
			Drives:      []fauna.DriveRule{},
			Speed:       mustNum(t, speed),
			SmellRadius: 5, SightRadius: 5, FovArc: 1,
			SteerChannel: map[actions.ActionID]core.Tag{}, // MoveTo absent ⇒ continue heading
		},
	})
}

// deadbandRulesSpecies is like deadbandRules but authors a per-species MoveDeadband (FM14a).
func deadbandRulesSpecies(t *testing.T, speed string, perSpecies float64) *fauna.Rules {
	t.Helper()
	return fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		spHerb: {
			Utilities:    map[actions.ActionID]*expr.Program{actMoveTo: mustNum(t, "0.1")},
			Drives:       []fauna.DriveRule{},
			Speed:        mustNum(t, speed),
			MoveDeadband: perSpecies,
			SmellRadius:  5, SightRadius: 5, FovArc: 1,
			SteerChannel: map[actions.ActionID]core.Tag{},
		},
	})
}

// stepDeadband runs one ACTIVE tick for a lone heading-+X wanderer at (50,50) with the
// given MoveDeadband and returns its intent. Same seed ⇒ identical wander draw.
func stepDeadband(t *testing.T, rules *fauna.Rules, deadband float64) fauna.Intent {
	t.Helper()
	a := herbAnimal("h1", core.Vec2{X: 50, Y: 50}, map[fauna.DriveID]float64{})
	a.Heading = 0 // +X
	a.ActiveUntil = 1000
	a.CurrentAction = actMoveTo
	snap := makeSnap([]fauna.Animal{a}, nil, nil, openTerrain, 1, emptyEnv("h1"))
	snap.MoveDeadband = deadband
	return fauna.Step(snap, rules, rng.New(1))[0]
}

// FM9: a §6 speed BELOW the deadband HOLDS position (energy conservation — "no salient
// threat/need ⇒ mostly doesn't move").
func TestDeadbandHoldsBelowThreshold(t *testing.T) {
	rules := deadbandRules(t, "0.5") // idle-ish speed
	held := stepDeadband(t, rules, 1.0)
	if held.NextPos != (core.Vec2{X: 50, Y: 50}) {
		t.Errorf("speed 0.5 < deadband 1.0 must hold position, got %+v", held.NextPos)
	}
	if held.NextHeading != 0 {
		t.Errorf("holding must keep heading, got %v", held.NextHeading)
	}
}

// FM9: a §6 speed ABOVE the deadband moves normally (deadband only tightens the floor).
func TestDeadbandMovesAboveThreshold(t *testing.T) {
	rules := deadbandRules(t, "0.5")
	moved := stepDeadband(t, rules, 0.1)
	if moved.NextPos.X <= 50 {
		t.Errorf("speed 0.5 > deadband 0.1 must move +X, got %+v", moved.NextPos)
	}
}

// FM14a: a per-species MoveDeadband OVERRIDES the global. Species deadband 1.0 holds a
// speed-0.5 wanderer even though the global is 0 (off) — so a fast idler can be stopped
// species-by-species without a global that would freeze slower foragers.
func TestPerSpeciesDeadbandOverridesGlobal(t *testing.T) {
	rules := deadbandRulesSpecies(t, "0.5", 1.0) // per-species deadband 1.0 > speed 0.5
	held := stepDeadband(t, rules, 0.0)          // global OFF
	if held.NextPos != (core.Vec2{X: 50, Y: 50}) {
		t.Errorf("per-species deadband 1.0 must hold a 0.5 wanderer even with global 0, got %+v", held.NextPos)
	}
}

// FM14a: an UNAUTHORED per-species deadband (0) falls back to the global — so the global
// stays the fallback lever and the off-lever (global 0) is byte-identical.
func TestPerSpeciesDeadbandZeroFallsBackToGlobal(t *testing.T) {
	rules := deadbandRulesSpecies(t, "0.5", 0.0) // species unset ⇒ use global
	movedGlobalOff := stepDeadband(t, rules, 0.0)
	if movedGlobalOff.NextPos.X <= 50 {
		t.Errorf("species deadband 0 + global 0 ⇒ off ⇒ should move, got %+v", movedGlobalOff.NextPos)
	}
	heldGlobalOn := stepDeadband(t, rules, 1.0) // global 1.0 applies to the species
	if heldGlobalOn.NextPos != (core.Vec2{X: 50, Y: 50}) {
		t.Errorf("species deadband 0 must fall back to the global 1.0 (hold), got %+v", heldGlobalOn.NextPos)
	}
}

// FM9 off-lever: MoveDeadband ≤ 0 is byte-identical to no deadband (same seed) — the
// pre-existing speed ≤ 0 hold is the only floor (existing goldens hold).
func TestDeadbandOffLeverByteIdentical(t *testing.T) {
	rules := deadbandRules(t, "0.5")
	off := stepDeadband(t, rules, 0.0)
	neg := stepDeadband(t, rules, -1.0) // negative sentinel also off
	if off.NextPos != neg.NextPos {
		t.Errorf("deadband 0 and <0 must both be OFF (identical): %+v vs %+v", off.NextPos, neg.NextPos)
	}
	if off.NextPos.X <= 50 {
		t.Errorf("with deadband off the wanderer should move +X, got %+v", off.NextPos)
	}
}
