package fauna_test

import (
	"testing"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/space/scent"
)

const actSleep actions.ActionID = "Sleep"
const actMove actions.ActionID = "MoveTo"

// sleepHerbRules is a herbivore whose Sleep action is the state:sleep torpor channel (P_sleep1):
// SteerChannel maps Sleep → TagSleep (no-loco + high wake threshold), and the Sleep utility is
// daylight-driven ("1 - daylight") so it wins at night and loses by day (diurnal emerges from the
// §6 sign, D2/D10). MoveTo is a small always-on wander baseline so day has a clear non-Sleep winner.
func sleepHerbRules(t *testing.T) *fauna.Rules {
	t.Helper()
	return fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		spHerb: {
			Utilities: map[actions.ActionID]*expr.Program{
				actGraze: mustNum(t, "scent.food"),
				actSleep: mustNum(t, "1 - daylight"),
				actMove:  mustNum(t, "0.1"),
			},
			Drives: []fauna.DriveRule{
				{ID: "hunger", Rate: 0},
				{ID: "fatigue", Rate: 0},
				{ID: "fear", Decay: 0.05, WaryLevel: 0.4, FleeLevel: 0.9},
				{ID: "thermal"},
			},
			Speed:       mustNum(t, "0.5"),
			AppTemp:     mustNum(t, "temperature * 0"),
			SmellRadius: 5, SightRadius: 8, FovArc: 1.57,
			SteerChannel: map[actions.ActionID]core.Tag{
				actGraze: fauna.TagSteerFood,
				actSleep: fauna.TagSleep,
			},
		},
	})
}

func sleepTestAnimal(action actions.ActionID, pos core.Vec2) fauna.Animal {
	return fauna.Animal{
		ID: "herbA", Species: spHerb, Pos: pos,
		Stats:         map[core.StatID]float64{},
		Drives:        map[fauna.DriveID]float64{"hunger": 0, "fatigue": 0, "fear": 0, "thermal": 0},
		Stamina:       1,
		Vital:         1,
		VitalCap:      1,
		CurrentAction: action,
	}
}

// TestSleepWakeThresholdGatesFaintScent — SS3/FM12 torpor wake gate. A SLEEPING animal
// (CurrentAction is the state:sleep action) wakes to a predator scent only when it is ≥
// Cadence.SleepWakeScentThreshold; a non-sleeping animal ignores the threshold (any scent wakes).
// Observable: Intent.ActiveUntil is set to Tick+WakeCooldown iff the F45 wake fired.
func TestSleepWakeThresholdGatesFaintScent(t *testing.T) {
	rules := sleepHerbRules(t)
	pos := core.Vec2{X: 0, Y: 0}

	sg := scent.New(1.0)
	sg.Deposit(scent.ChanPredator, pos, 1.0)
	sg.Commit()
	predI := sg.IntensityAt(scent.ChanPredator, pos)
	if predI <= 0 {
		t.Fatalf("setup: predator scent intensity should be > 0, got %v", predI)
	}

	// tick 7, WakeCooldown 5 ⇒ a wake sets ActiveUntil = 12.
	run := func(a fauna.Animal, threshold float64) core.Tick {
		snap := makeSnap([]fauna.Animal{a}, sg, nil, openTerrain, 7, emptyEnv("herbA"))
		snap.Cadence = fauna.Cadence{DormantPeriod: 10, WakeCooldown: 5, SleepWakeScentThreshold: threshold}
		return fauna.Step(snap, rules, rng.New(1))[0].ActiveUntil
	}

	// Deep sleeper: threshold ABOVE the scent ⇒ does NOT wake (ActiveUntil stays 0).
	if au := run(sleepTestAnimal(actSleep, pos), predI*2); au != 0 {
		t.Errorf("deep sleeper (threshold %.3f > scent %.3f) must NOT wake: ActiveUntil=%d, want 0", predI*2, predI, au)
	}
	// Light sleeper: threshold BELOW the scent ⇒ wakes (ActiveUntil = 7+5 = 12).
	if au := run(sleepTestAnimal(actSleep, pos), predI*0.5); au != 12 {
		t.Errorf("light sleeper (threshold %.3f < scent %.3f) must wake: ActiveUntil=%d, want 12", predI*0.5, predI, au)
	}
	// Non-sleeper (Graze): the threshold is ignored ⇒ any predator scent wakes it.
	if au := run(sleepTestAnimal(actGraze, pos), predI*2); au != 12 {
		t.Errorf("non-sleeper must wake on any predator scent regardless of threshold: ActiveUntil=%d, want 12", au)
	}
}

// TestDaylightDrivesSleepSelectionAndStaysPut — FM11 daylight operand + torpor stay-put. At night
// (daylight 0) the diurnal Sleep utility (1-daylight) wins and the animal stays put (NextPos==Pos);
// by day (daylight 1) Sleep scores 0, loses to the MoveTo baseline, and the animal moves.
func TestDaylightDrivesSleepSelectionAndStaysPut(t *testing.T) {
	rules := sleepHerbRules(t)
	pos := core.Vec2{X: 5, Y: 5}

	run := func(daylight float64) fauna.Intent {
		a := sleepTestAnimal(actMove, pos)
		a.Heading = 0
		a.ActiveUntil = 100 // ACTIVE ⇒ full re-arbitration this tick
		env := map[core.ObjectID]fauna.EnvSample{"herbA": {Daylight: daylight}}
		snap := makeSnap([]fauna.Animal{a}, scent.New(1.0), nil, openTerrain, 1, env)
		return fauna.Step(snap, rules, rng.New(1))[0]
	}

	night := run(0)
	if night.Action != actSleep {
		t.Errorf("night (daylight 0): chosen action = %q, want Sleep", night.Action)
	}
	if night.NextPos != pos {
		t.Errorf("night: a sleeping animal (torpor) must stay put: NextPos=%v, want %v", night.NextPos, pos)
	}

	day := run(1)
	if day.Action == actSleep {
		t.Errorf("day (daylight 1): Sleep utility is 0, animal must NOT sleep, got Sleep")
	}
	if day.NextPos == pos {
		t.Errorf("day: an awake (MoveTo) animal must move, but NextPos stayed %v", pos)
	}
}
