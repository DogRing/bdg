package fauna_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/parser"
	"go/token"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/space/scent"
	"github.com/dogring/bdg/engine/space/spatial"
)

// ── test helpers ─────────────────────────────────────────────────────────────

// testStatSet satisfies expr.StatSet for the test programs.
type testStatSet struct{ ids map[core.StatID]bool }

func (s testStatSet) Has(id core.StatID) bool { return s.ids[id] }

func withStats(ids ...core.StatID) expr.StatSet {
	m := make(map[core.StatID]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return testStatSet{m}
}

var _ expr.StatSet = testStatSet{} // compile-time interface check

func mustNum(t *testing.T, text string, stats ...core.StatID) *expr.Program {
	t.Helper()
	p, err := expr.Parse(text, expr.KindNum, withStats(stats...), nil)
	if err != nil {
		t.Fatalf("expr.Parse(%q): %v", text, err)
	}
	return p
}

// mockTerrain adapts functions to the TerrainSampler interface.
type mockTerrain struct {
	blocked  func(core.Vec2) bool
	terrainF func(core.Vec2) core.Tag
	costF    func(core.Vec2) float64
}

func (m mockTerrain) FootprintBlocked(p core.Vec2) bool { return m.blocked(p) }
func (m mockTerrain) TerrainAt(p core.Vec2) core.Tag    { return m.terrainF(p) }
func (m mockTerrain) BaseCost(p core.Vec2) float64      { return m.costF(p) }

var openTerrain = mockTerrain{
	blocked:  func(core.Vec2) bool { return false },
	terrainF: func(core.Vec2) core.Tag { return "soil" },
	costF:    func(core.Vec2) float64 { return 1.0 },
}

func defaultCadence() fauna.Cadence {
	return fauna.Cadence{DormantPeriod: 10, WakeCooldown: 5}
}

func emptyEnv(ids ...core.ObjectID) map[core.ObjectID]fauna.EnvSample {
	m := make(map[core.ObjectID]fauna.EnvSample, len(ids))
	for _, id := range ids {
		m[id] = fauna.EnvSample{}
	}
	return m
}

// makeSnap builds a minimal Snapshot for testing.
func makeSnap(animals []fauna.Animal, sg *scent.Grid, sp *spatial.SpatialHash,
	terrain fauna.TerrainSampler, tick core.Tick, env map[core.ObjectID]fauna.EnvSample,
) *fauna.Snapshot {
	if sg == nil {
		sg = scent.New(1.0)
	}
	if sp == nil {
		sp = spatial.New(8.0)
	}
	if terrain == nil {
		terrain = openTerrain
	}
	return &fauna.Snapshot{
		Animals:       animals,
		Scent:         sg,
		Spatial:       sp,
		Terrain:       terrain,
		Env:           env,
		Tick:          tick,
		Cadence:       defaultCadence(),
		ScentCellSize: 1.0,
		DT:            1.0,
	}
}

// ── fixture rule builder ──────────────────────────────────────────────────────

const (
	spHerb       fauna.SpeciesID = "herb"
	spPred       fauna.SpeciesID = "pred"
	spSwimmer    fauna.SpeciesID = "swimmer"
	spFish       fauna.SpeciesID = "fish"
	spCombatPred fauna.SpeciesID = "combat_pred"
	spCombatPrey fauna.SpeciesID = "combat_prey"

	actGraze  actions.ActionID = "Graze"
	actFlee   actions.ActionID = "Flee"
	actWary   actions.ActionID = "Wary"
	actRest   actions.ActionID = "Rest"
	actHunt   actions.ActionID = "Hunt"
	actAttack actions.ActionID = "Attack"
	actFeed   actions.ActionID = "Feed"
	actA      actions.ActionID = "ActionA"
	actB      actions.ActionID = "ActionB"
)

func herbRules(t *testing.T) fauna.SpeciesRule {
	t.Helper()
	return fauna.SpeciesRule{
		Utilities: map[actions.ActionID]*expr.Program{
			actGraze: mustNum(t, "scent.food + 0.1"),
			actFlee:  mustNum(t, "fear * sight.predator"),
			actWary:  mustNum(t, "fear * scent.predator"),
			actRest:  mustNum(t, "is_current * 0.05"),
		},
		Drives: []fauna.DriveRule{
			{ID: "hunger", Rate: 0.01},
			{ID: "fear", Decay: 0.05, WaryLevel: 0.4, FleeLevel: 0.9},
			{ID: "thermal"},
		},
		Speed:       mustNum(t, "0.5"),
		AppTemp:     mustNum(t, "temperature * 0.5"),
		IsPredator:  false,
		SmellRadius: 5.0,
		SightRadius: 8.0,
		FovArc:      math.Pi / 2, // 90°
		TerrainCost: map[core.Tag]float64{"water": 3.0},
		Impassable:  nil,
		SteerChannel: map[actions.ActionID]core.Tag{
			actGraze: fauna.TagSteerFood,
			actFlee:  fauna.TagFleePred,
			actWary:  fauna.TagWaryPred,
			actRest:  fauna.TagNoLoco,
		},
	}
}

func predRules(t *testing.T) fauna.SpeciesRule {
	t.Helper()
	return fauna.SpeciesRule{
		Utilities: map[actions.ActionID]*expr.Program{
			actHunt: mustNum(t, "scent.prey + 0.5"),
			actRest: mustNum(t, "is_current * 0.05"),
		},
		Drives: []fauna.DriveRule{
			{ID: "hunger", Rate: 0.01},
			{ID: "fear"},
		},
		Speed:       mustNum(t, "0.8"),
		AppTemp:     mustNum(t, "temperature * 0.5"),
		IsPredator:  true,
		SmellRadius: 6.0,
		SightRadius: 10.0,
		FovArc:      math.Pi / 3,
		TerrainCost: nil,
		Impassable:  nil,
		SteerChannel: map[actions.ActionID]core.Tag{
			actHunt: fauna.TagSteerPrey,
			actRest: fauna.TagNoLoco,
		},
	}
}

func swimmerRules(t *testing.T) fauna.SpeciesRule {
	t.Helper()
	return fauna.SpeciesRule{
		Utilities: map[actions.ActionID]*expr.Program{
			actGraze: mustNum(t, "0.5"),
		},
		Drives:       []fauna.DriveRule{{ID: "hunger", Rate: 0.01}},
		Speed:        mustNum(t, "1.0"),
		AppTemp:      mustNum(t, "0.5"),
		IsPredator:   false,
		SmellRadius:  3.0,
		SightRadius:  5.0,
		FovArc:       math.Pi / 2,
		TerrainCost:  map[core.Tag]float64{"water": 0.2},
		Impassable:   nil,
		SteerChannel: map[actions.ActionID]core.Tag{actGraze: fauna.TagSteerFood},
	}
}

func fishRules(t *testing.T) fauna.SpeciesRule {
	t.Helper()
	return fauna.SpeciesRule{
		Utilities: map[actions.ActionID]*expr.Program{
			actGraze: mustNum(t, "0.5"),
		},
		Drives:       []fauna.DriveRule{{ID: "hunger", Rate: 0.01}},
		Speed:        mustNum(t, "1.0"),
		AppTemp:      mustNum(t, "0.5"),
		IsPredator:   false,
		SmellRadius:  3.0,
		SightRadius:  5.0,
		FovArc:       math.Pi / 2,
		TerrainCost:  map[core.Tag]float64{"water": 0.1},
		Impassable:   []core.Tag{"soil"},
		SteerChannel: map[actions.ActionID]core.Tag{actGraze: fauna.TagSteerFood},
	}
}

func newTestRules(t *testing.T) *fauna.Rules {
	t.Helper()
	return fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		spHerb:    herbRules(t),
		spPred:    predRules(t),
		spSwimmer: swimmerRules(t),
		spFish:    fishRules(t),
	})
}

func herbAnimal(id core.ObjectID, pos core.Vec2, drives map[fauna.DriveID]float64) fauna.Animal {
	if drives == nil {
		drives = map[fauna.DriveID]float64{"hunger": 0.0, "fear": 0.0, "thermal": 0.0}
	}
	return fauna.Animal{
		ID:            id,
		Species:       spHerb,
		Pos:           pos,
		Stats:         map[core.StatID]float64{"Strength": 1.0},
		Drives:        drives,
		Stamina:       1.0,
		Vital:         1.0,
		Heading:       0.0,
		CurrentAction: actRest,
		ActiveUntil:   0,
	}
}

func predAnimal(id core.ObjectID, pos core.Vec2) fauna.Animal {
	return fauna.Animal{
		ID:            id,
		Species:       spPred,
		Pos:           pos,
		Stats:         map[core.StatID]float64{"Strength": 1.0},
		Drives:        map[fauna.DriveID]float64{"hunger": 0.0, "fear": 0.0},
		Stamina:       1.0,
		Vital:         1.0,
		Heading:       0.0,
		CurrentAction: actHunt,
		ActiveUntil:   0,
	}
}

// ── AC: Utility max + ID tie-break determinism ────────────────────────────────

func TestUtilityMaxAndTieBreak(t *testing.T) {
	// Two actions with the same utility formula → tie → lexicographically smaller wins.
	ties := fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		"sp": {
			Utilities: map[actions.ActionID]*expr.Program{
				actA: mustNum(t, "0.5"),
				actB: mustNum(t, "0.5"),
			},
			Drives:       []fauna.DriveRule{{ID: "hunger", Rate: 0.0}},
			Speed:        mustNum(t, "0.0"),
			AppTemp:      mustNum(t, "0.0"),
			SteerChannel: map[actions.ActionID]core.Tag{actA: fauna.TagNoLoco, actB: fauna.TagNoLoco},
			SmellRadius:  1.0, SightRadius: 1.0,
		},
	})
	a := fauna.Animal{
		ID:      "a1",
		Species: "sp",
		Pos:     core.Vec2{},
		Stats:   map[core.StatID]float64{},
		Drives:  map[fauna.DriveID]float64{"hunger": 0.0},
		Stamina: 1.0, Vital: 1.0,
		CurrentAction: actB,
		ActiveUntil:   100, // force ACTIVE so full scoring pipeline runs
	}
	snap := makeSnap([]fauna.Animal{a}, nil, nil, openTerrain, 1, emptyEnv("a1"))
	r := rng.New(42)

	intents := fauna.Step(snap, ties, r)
	if len(intents) != 1 {
		t.Fatalf("expected 1 intent, got %d", len(intents))
	}
	// actA < actB lexicographically → actA wins the tie.
	if intents[0].Action != actA {
		t.Errorf("tie-break: want %q, got %q", actA, intents[0].Action)
	}

	// Higher utility wins strictly (actA utility 0.9, actB utility 0.5).
	higher := fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		"sp": {
			Utilities: map[actions.ActionID]*expr.Program{
				actA: mustNum(t, "0.9"),
				actB: mustNum(t, "0.5"),
			},
			Drives: []fauna.DriveRule{{ID: "hunger", Rate: 0.0}},
			Speed:  mustNum(t, "0.0"), AppTemp: mustNum(t, "0.0"),
			SteerChannel: map[actions.ActionID]core.Tag{actA: fauna.TagNoLoco, actB: fauna.TagNoLoco},
			SmellRadius:  1.0, SightRadius: 1.0,
		},
	})
	r2 := rng.New(42)
	snap2 := makeSnap([]fauna.Animal{a}, nil, nil, openTerrain, 1, emptyEnv("a1"))
	intents2 := fauna.Step(snap2, higher, r2)
	if intents2[0].Action != actA {
		t.Errorf("strict higher: want %q, got %q", actA, intents2[0].Action)
	}

	// Deterministic: two calls same seed → same result.
	r3 := rng.New(42)
	snap3 := makeSnap([]fauna.Animal{a}, nil, nil, openTerrain, 1, emptyEnv("a1"))
	intents3 := fauna.Step(snap3, higher, r3)
	if intents3[0].Action != intents2[0].Action {
		t.Error("not deterministic across calls with same seed")
	}
}

// ── AC: One intent per animal, sorted (D12) ───────────────────────────────────

func TestOneIntentPerAnimalSorted(t *testing.T) {
	rules := newTestRules(t)
	a1 := herbAnimal("z_late", core.Vec2{X: 1}, nil)
	a2 := herbAnimal("a_early", core.Vec2{X: 2}, nil)
	a3 := predAnimal("m_mid", core.Vec2{X: 3})

	animals := []fauna.Animal{a1, a2, a3}
	env := emptyEnv("z_late", "a_early", "m_mid")

	// Order 1: z, a, m
	snap1 := makeSnap(animals, nil, nil, openTerrain, 1, env)
	r1 := rng.New(7)
	intents1 := fauna.Step(snap1, rules, r1)
	if len(intents1) != 3 {
		t.Fatalf("got %d intents, want 3", len(intents1))
	}
	// Must be sorted by ObjectID.
	for i := 1; i < len(intents1); i++ {
		if intents1[i].Animal <= intents1[i-1].Animal {
			t.Errorf("intents not sorted: [%d]=%q [%d]=%q", i-1, intents1[i-1].Animal, i, intents1[i].Animal)
		}
	}

	// Order 2: a, m, z (shuffled) → same intents.
	snap2 := makeSnap([]fauna.Animal{a2, a3, a1}, nil, nil, openTerrain, 1, env)
	r2 := rng.New(7)
	intents2 := fauna.Step(snap2, rules, r2)
	for i := range intents1 {
		if intents1[i].Animal != intents2[i].Animal || intents1[i].Action != intents2[i].Action {
			t.Errorf("shuffled input: intent[%d] mismatch: %v vs %v", i, intents1[i], intents2[i])
		}
	}
}

// ── AC: F45 adaptive cadence ──────────────────────────────────────────────────

func TestF45Cadence(t *testing.T) {
	rules := newTestRules(t)

	// Compute phase for "herbA" with period 10.
	ph := phase("herbA", 10)

	// Herbivore with no predator scent and not in cooldown.
	a := herbAnimal("herbA", core.Vec2{}, nil)
	a.CurrentAction = actGraze
	a.ActiveUntil = 0

	// Off-boundary ticks: CHEAP PATH → holds actGraze, still steers.
	// On re-arb tick: FULL pipeline → may change action.
	for tick := core.Tick(1); tick <= 30; tick++ {
		sg := scent.New(1.0) // no predator scent
		snap := makeSnap([]fauna.Animal{a}, sg, nil, openTerrain, tick, emptyEnv("herbA"))
		r := rng.New(int64(tick))
		intents := fauna.Step(snap, rules, r)
		if len(intents) != 1 {
			t.Fatalf("tick %d: expected 1 intent", tick)
		}
		itnt := intents[0]
		isReArb := (tick+ph)%10 == 0
		if !isReArb {
			// Off-boundary: must hold CurrentAction (actGraze).
			if itnt.Action != actGraze {
				t.Errorf("tick %d: dormant off-boundary should hold actGraze, got %q", tick, itnt.Action)
			}
			// NOT frozen: NextPos may differ from Pos (cheap steer still moves).
			// (With speed=0.5 and DT=1, NextPos should be away from Pos unless blocked.)
		}
		// In all cases, intent.Animal == "herbA".
		if itnt.Animal != "herbA" {
			t.Errorf("tick %d: wrong animal ID %q", tick, itnt.Animal)
		}
	}

	// Predator-scent wake: set predator scent in animal's cell → goes ACTIVE.
	sg := scent.New(1.0)
	sg.Deposit(scent.ChanPredator, a.Pos, 1.0)
	sg.Commit()
	a.ActiveUntil = 0
	wakeSnap := makeSnap([]fauna.Animal{a}, sg, nil, openTerrain, 7, emptyEnv("herbA"))
	rWake := rng.New(42)
	wakeIntents := fauna.Step(wakeSnap, rules, rWake)
	// ActiveUntil should be set to tick + WakeCooldown = 7 + 5 = 12.
	if wakeIntents[0].ActiveUntil != 12 {
		t.Errorf("wake: ActiveUntil want 12, got %d", wakeIntents[0].ActiveUntil)
	}

	// Cooldown: while Tick ≤ ActiveUntil, animal stays ACTIVE.
	a.ActiveUntil = 12
	a.CurrentAction = actGraze
	for tick := core.Tick(7); tick <= 12; tick++ {
		snap := makeSnap([]fauna.Animal{a}, scent.New(1.0), nil, openTerrain, tick, emptyEnv("herbA"))
		// Off re-arb tick but in cooldown: should run full pipeline (ACTIVE).
		r := rng.New(int64(tick))
		intents := fauna.Step(snap, rules, r)
		_ = intents // just verify no panic
	}

	// Predator species: always ACTIVE regardless of phase.
	pred := predAnimal("wolfX", core.Vec2{})
	for tick := core.Tick(1); tick <= 30; tick++ {
		snap := makeSnap([]fauna.Animal{pred}, scent.New(1.0), nil, openTerrain, tick, emptyEnv("wolfX"))
		r := rng.New(int64(tick))
		intents := fauna.Step(snap, rules, r)
		// Predator should always produce an intent (ACTIVE, runs full pipeline every tick).
		if len(intents) != 1 {
			t.Fatalf("predator tick %d: want 1 intent, got %d", tick, len(intents))
		}
	}
}

// phase is the test-visible version of the internal phase function.
// Uses FNV-1a to match the implementation.
func phase(id core.ObjectID, period int) core.Tick {
	// Replicate the implementation's FNV-1a computation.
	const (
		offset32 uint32 = 2166136261
		prime32  uint32 = 16777619
	)
	h := offset32
	for _, b := range []byte(id) {
		h ^= uint32(b)
		h *= prime32
	}
	if period <= 0 {
		return 0
	}
	return core.Tick(h % uint32(period))
}

// ── AC: Drive integration per F25(c) ─────────────────────────────────────────

func TestDriveIntegration(t *testing.T) {
	rules := newTestRules(t)

	// Accumulator: hunger rises by rate * dt = 0.01 * 1.0 per tick.
	a := herbAnimal("h1", core.Vec2{}, map[fauna.DriveID]float64{"hunger": 0.0, "fear": 0.0, "thermal": 0.0})
	a.ActiveUntil = 1000 // force ACTIVE for all ticks

	const N = 10
	for tick := core.Tick(1); tick <= N; tick++ {
		snap := makeSnap([]fauna.Animal{a}, scent.New(1.0), nil, openTerrain, tick, emptyEnv("h1"))
		r := rng.New(int64(tick))
		intents := fauna.Step(snap, rules, r)
		a.Drives = intents[0].Drives
	}
	wantHunger := float64(N) * 0.01
	if math.Abs(a.Drives["hunger"]-wantHunger) > 1e-10 {
		t.Errorf("hunger after %d ticks: got %v, want %v", N, a.Drives["hunger"], wantHunger)
	}

	// Fear SET from scent.predator → wary level 0.4.
	b := herbAnimal("h2", core.Vec2{}, map[fauna.DriveID]float64{"hunger": 0.0, "fear": 0.0, "thermal": 0.0})
	b.ActiveUntil = 1000
	sg := scent.New(1.0)
	sg.Deposit(scent.ChanPredator, b.Pos, 2.0) // deposit predator scent in same cell
	sg.Commit()
	snap := makeSnap([]fauna.Animal{b}, sg, nil, openTerrain, 1, emptyEnv("h2"))
	intents := fauna.Step(snap, rules, rng.New(1))
	if math.Abs(intents[0].Drives["fear"]-0.4) > 1e-10 {
		t.Errorf("fear with scent.predator: got %v, want 0.4 (wary level)", intents[0].Drives["fear"])
	}

	// Fear SET from sight.predator → flee level 0.9 (predator in FOV).
	sp := spatial.New(1.0)
	predPos := core.Vec2{X: 3, Y: 0} // within sightRadius=8, in front
	sp.Insert("predAnimal", predPos)
	c := herbAnimal("h3", core.Vec2{}, map[fauna.DriveID]float64{"hunger": 0.0, "fear": 0.0, "thermal": 0.0})
	c.Heading = 0 // facing east (+x)
	c.ActiveUntil = 1000
	pAnimal := predAnimal("predAnimal", predPos)
	snap3 := &fauna.Snapshot{
		Animals: []fauna.Animal{c, pAnimal},
		Scent:   scent.New(1.0),
		Spatial: sp,
		Terrain: openTerrain,
		Env:     emptyEnv("h3", "predAnimal"),
		Tick:    1, Cadence: defaultCadence(), DT: 1.0,
	}
	intents3 := fauna.Step(snap3, rules, rng.New(1))
	// h3's fear should be set to flee level (0.9) because predator is in sight.
	for _, it := range intents3 {
		if it.Animal == "h3" {
			if math.Abs(it.Drives["fear"]-0.9) > 1e-10 {
				t.Errorf("sight.predator: fear want 0.9 (flee level), got %v", it.Drives["fear"])
			}
		}
	}

	// Fear decays when neither scent nor sight: from 0.5 → 0.5 - 0.05*1 = 0.45.
	d := herbAnimal("h4", core.Vec2{}, map[fauna.DriveID]float64{"hunger": 0.0, "fear": 0.5, "thermal": 0.0})
	d.ActiveUntil = 1000
	snapD := makeSnap([]fauna.Animal{d}, scent.New(1.0), nil, openTerrain, 1, emptyEnv("h4"))
	intentsD := fauna.Step(snapD, rules, rng.New(1))
	wantFear := 0.5 - 0.05*1.0
	if math.Abs(intentsD[0].Drives["fear"]-wantFear) > 1e-10 {
		t.Errorf("fear decay: got %v, want %v", intentsD[0].Drives["fear"], wantFear)
	}

	// Thermal stays 0 (climate OFF): temperature=0 → AppTemp=0 → thermal=0.
	if math.Abs(intentsD[0].Drives["thermal"]-0.0) > 1e-10 {
		t.Errorf("thermal with climate-OFF: got %v, want 0.0", intentsD[0].Drives["thermal"])
	}

	// Dormant off-boundary: only accumulators + fear decay (no scent SET).
	e := herbAnimal("h5", core.Vec2{}, map[fauna.DriveID]float64{"hunger": 0.0, "fear": 0.5, "thermal": 0.0})
	e.ActiveUntil = 0
	e.CurrentAction = actGraze
	// Use tick not on re-arb boundary.
	ph := phase("h5", 10)
	var cheapTick core.Tick
	for tick := core.Tick(1); tick <= 100; tick++ {
		if (tick+ph)%10 != 0 {
			cheapTick = tick
			break
		}
	}
	// Dormant CHEAP PATH: place predator scent far from own cell so it does NOT wake
	// the animal (IntensityAt at own cell = 0). Fear must decay, NOT be SET from context.
	sgE2 := scent.New(1.0)
	sgE2.Deposit(scent.ChanPredator, core.Vec2{X: 100, Y: 100}, 2.0)
	sgE2.Commit()
	snapE2 := makeSnap([]fauna.Animal{e}, sgE2, nil, openTerrain, cheapTick, emptyEnv("h5"))
	intentsE := fauna.Step(snapE2, rules, rng.New(1))
	// Cheap path: hunger advances by rate (0.01), fear decays from 0.5 → 0.5 - 0.05 = 0.45.
	if math.Abs(intentsE[0].Drives["hunger"]-0.01) > 1e-10 {
		t.Errorf("cheap path hunger: got %v, want 0.01", intentsE[0].Drives["hunger"])
	}
	if math.Abs(intentsE[0].Drives["fear"]-0.45) > 1e-10 {
		t.Errorf("cheap path fear decay: got %v, want 0.45", intentsE[0].Drives["fear"])
	}
}

// ── AC: Seeded steering reproducible + PER-SPECIES terrain cost ───────────────

func TestSeededSteeringAndTerrainCost(t *testing.T) {
	rules := newTestRules(t)

	// Two runs with same seed → identical NextPos/NextHeading.
	a := herbAnimal("ha", core.Vec2{X: 5, Y: 5}, nil)
	a.ActiveUntil = 100

	run := func(seed int64) (core.Vec2, float64) {
		snap := makeSnap([]fauna.Animal{a}, nil, nil, openTerrain, 1, emptyEnv("ha"))
		r := rng.New(seed)
		intents := fauna.Step(snap, rules, r)
		return intents[0].NextPos, intents[0].NextHeading
	}
	p1, h1 := run(42)
	p2, h2 := run(42)
	if p1 != p2 || h1 != h2 {
		t.Errorf("same seed → different steering: pos %v vs %v, heading %v vs %v", p1, p2, h1, h2)
	}
	p3, h3 := run(99)
	// Different seed → likely different (not guaranteed but almost certainly so with wander).
	t.Logf("seed 42: pos=%v h=%v; seed 99: pos=%v h=%v", p1, h1, p3, h3)
	_ = p3
	_ = h3

	// Blocked terrain: NextPos == Pos.
	blockedTerrain := mockTerrain{
		blocked:  func(core.Vec2) bool { return true },
		terrainF: func(core.Vec2) core.Tag { return "wall" },
		costF:    func(core.Vec2) float64 { return 1.0 },
	}
	a.ActiveUntil = 100
	snapBlocked := makeSnap([]fauna.Animal{a}, nil, nil, blockedTerrain, 1, emptyEnv("ha"))
	intentsB := fauna.Step(snapBlocked, rules, rng.New(42))
	if intentsB[0].NextPos != a.Pos {
		t.Errorf("blocked terrain: NextPos should equal Pos %v, got %v", a.Pos, intentsB[0].NextPos)
	}

	// Rest ⇒ NextPos == Pos.
	// Force Rest to win by giving it a very high utility and others very low.
	restOnly := fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		"sp": {
			Utilities:    map[actions.ActionID]*expr.Program{actRest: mustNum(t, "0.9")},
			Drives:       []fauna.DriveRule{{ID: "hunger", Rate: 0.0}},
			Speed:        mustNum(t, "1.0"),
			AppTemp:      mustNum(t, "0.0"),
			SteerChannel: map[actions.ActionID]core.Tag{actRest: fauna.TagNoLoco},
			SmellRadius:  1.0, SightRadius: 1.0,
		},
	})
	aRest := fauna.Animal{
		ID: "ar", Species: "sp", Pos: core.Vec2{X: 10, Y: 10},
		Stats: map[core.StatID]float64{}, Drives: map[fauna.DriveID]float64{"hunger": 0.0},
		Stamina: 1.0, Vital: 1.0, CurrentAction: actRest, ActiveUntil: 100,
	}
	snapR := makeSnap([]fauna.Animal{aRest}, nil, nil, openTerrain, 1, emptyEnv("ar"))
	intentsR := fauna.Step(snapR, restOnly, rng.New(1))
	if intentsR[0].NextPos != aRest.Pos {
		t.Errorf("Rest action: NextPos should == Pos %v, got %v", aRest.Pos, intentsR[0].NextPos)
	}

	// PER-SPECIES terrain cost: swimmer (low river mult) vs herbivore (high river mult).
	// Terrain: water (river). Swimmer has mult=0.2, herbivore has mult=3.0.
	// Same animal, same DT, same speed program, but different species → different effective speed.
	waterTerrain := mockTerrain{
		blocked:  func(core.Vec2) bool { return false },
		terrainF: func(core.Vec2) core.Tag { return "water" },
		costF:    func(core.Vec2) float64 { return 1.0 },
	}
	swimA := fauna.Animal{
		ID: "swim1", Species: spSwimmer, Pos: core.Vec2{X: 5, Y: 5},
		Stats: map[core.StatID]float64{}, Drives: map[fauna.DriveID]float64{"hunger": 0.0},
		Stamina: 1.0, Vital: 1.0, Heading: 0.0,
		CurrentAction: actGraze, ActiveUntil: 100,
	}
	herbA := fauna.Animal{
		ID: "herb1", Species: spHerb, Pos: core.Vec2{X: 5, Y: 5},
		Stats:   map[core.StatID]float64{"Strength": 1.0},
		Drives:  map[fauna.DriveID]float64{"hunger": 0.0, "fear": 0.0, "thermal": 0.0},
		Stamina: 1.0, Vital: 1.0, Heading: 0.0,
		CurrentAction: actGraze, ActiveUntil: 100,
	}
	snapSwim := makeSnap([]fauna.Animal{swimA}, nil, nil, waterTerrain, 1, emptyEnv("swim1"))
	intentsSwim := fauna.Step(snapSwim, rules, rng.New(42))
	snapHerb := makeSnap([]fauna.Animal{herbA}, nil, nil, waterTerrain, 1, emptyEnv("herb1"))
	intentsHerb := fauna.Step(snapHerb, rules, rng.New(42))
	// Swimmer should move further (lower cost mult → higher effective speed).
	dSwim := swimA.Pos.Distance(intentsSwim[0].NextPos)
	dHerb := herbA.Pos.Distance(intentsHerb[0].NextPos)
	t.Logf("swim dist=%v herb dist=%v in water", dSwim, dHerb)
	if dSwim <= dHerb {
		t.Errorf("swimmer should move further in water than herbivore: swim=%v herb=%v", dSwim, dHerb)
	}

	// Fish: soil is impassable → stays at Pos on land.
	fishA := fauna.Animal{
		ID: "fish1", Species: spFish, Pos: core.Vec2{X: 5, Y: 5},
		Stats: map[core.StatID]float64{}, Drives: map[fauna.DriveID]float64{"hunger": 0.0},
		Stamina: 1.0, Vital: 1.0, Heading: 0.0,
		CurrentAction: actGraze, ActiveUntil: 100,
	}
	// openTerrain returns "soil" which is impassable for fish.
	snapFish := makeSnap([]fauna.Animal{fishA}, nil, nil, openTerrain, 1, emptyEnv("fish1"))
	intentsFish := fauna.Step(snapFish, rules, rng.New(42))
	if intentsFish[0].NextPos != fishA.Pos {
		t.Errorf("fish on soil (impassable): NextPos should == Pos %v, got %v",
			fishA.Pos, intentsFish[0].NextPos)
	}
}

// ── AC: Sight FOV predator query ──────────────────────────────────────────────

func TestSightFOVQuery(t *testing.T) {
	rules := newTestRules(t)

	// Animal facing east (heading=0), fovArc=π/2.
	// Predator at (3,0) — directly in front within FOV → sightPred should trigger fear.
	sp := spatial.New(1.0)
	sp.Insert("wolf1", core.Vec2{X: 3, Y: 0})

	herbH := herbAnimal("deer1", core.Vec2{X: 0, Y: 0}, map[fauna.DriveID]float64{"hunger": 0.0, "fear": 0.0, "thermal": 0.0})
	herbH.Heading = 0
	herbH.ActiveUntil = 100
	wolf1 := predAnimal("wolf1", core.Vec2{X: 3, Y: 0})

	snapFront := &fauna.Snapshot{
		Animals: []fauna.Animal{herbH, wolf1},
		Scent:   scent.New(1.0), Spatial: sp, Terrain: openTerrain,
		Env: emptyEnv("deer1", "wolf1"), Tick: 1, Cadence: defaultCadence(), DT: 1.0,
	}
	intFront := fauna.Step(snapFront, rules, rng.New(1))
	var deerFrontFear float64
	for _, it := range intFront {
		if it.Animal == "deer1" {
			deerFrontFear = it.Drives["fear"]
		}
	}
	if math.Abs(deerFrontFear-0.9) > 1e-10 {
		t.Errorf("predator in front: fear want 0.9 (flee level), got %v", deerFrontFear)
	}

	// Predator directly behind (bearing=π, |diff from heading 0| = π > π/2 = fovArc) → blind spot.
	sp2 := spatial.New(1.0)
	sp2.Insert("wolf2", core.Vec2{X: -3, Y: 0})
	wolf2 := predAnimal("wolf2", core.Vec2{X: -3, Y: 0})
	deer2 := herbAnimal("deer2", core.Vec2{X: 0, Y: 0}, map[fauna.DriveID]float64{"hunger": 0.0, "fear": 0.0, "thermal": 0.0})
	deer2.Heading = 0
	deer2.ActiveUntil = 100
	snapBack := &fauna.Snapshot{
		Animals: []fauna.Animal{deer2, wolf2},
		Scent:   scent.New(1.0), Spatial: sp2, Terrain: openTerrain,
		Env: emptyEnv("deer2", "wolf2"), Tick: 1, Cadence: defaultCadence(), DT: 1.0,
	}
	intBack := fauna.Step(snapBack, rules, rng.New(1))
	var deerBackFear float64
	for _, it := range intBack {
		if it.Animal == "deer2" {
			deerBackFear = it.Drives["fear"]
		}
	}
	// No sight predator → fear stays at 0 (no scent either), or decays from 0.
	if deerBackFear >= 0.9 {
		t.Errorf("predator behind (blind spot): fear should be < 0.9, got %v", deerBackFear)
	}

	// Non-predator nearby entity: herbivore next to herbivore → no sightPred effect.
	sp3 := spatial.New(1.0)
	sp3.Insert("deer3", core.Vec2{X: 3, Y: 0})
	deer3A := herbAnimal("deer3A", core.Vec2{X: 0, Y: 0}, map[fauna.DriveID]float64{"hunger": 0.0, "fear": 0.0, "thermal": 0.0})
	deer3A.Heading = 0
	deer3A.ActiveUntil = 100
	deer3B := herbAnimal("deer3", core.Vec2{X: 3, Y: 0}, map[fauna.DriveID]float64{"hunger": 0.0, "fear": 0.0, "thermal": 0.0})
	snapNonPred := &fauna.Snapshot{
		Animals: []fauna.Animal{deer3A, deer3B},
		Scent:   scent.New(1.0), Spatial: sp3, Terrain: openTerrain,
		Env: emptyEnv("deer3A", "deer3"), Tick: 1, Cadence: defaultCadence(), DT: 1.0,
	}
	intNonPred := fauna.Step(snapNonPred, rules, rng.New(1))
	for _, it := range intNonPred {
		if it.Animal == "deer3A" {
			if it.Drives["fear"] >= 0.9 {
				t.Errorf("non-predator nearby: fear should be < 0.9, got %v", it.Drives["fear"])
			}
		}
	}
}

// ── AC: Context bridge (Stat/Attr/is_current) ─────────────────────────────────

func TestContextBridge(t *testing.T) {
	// Both actions share the same program: Strength*0.5 + is_current*0.3.
	// With Strength=2.0, base=1.0; the action whose ID == CurrentAction gets +0.3 boost.
	// CurrentAction=actA → actA=1.3 > actB=1.0 → actA wins.
	// CurrentAction=actB → actB=1.3 > actA=1.0 → actB wins.
	// This verifies: Stat lookup (Strength) + is_current Attr (0/1 per candidate).
	statRules := fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		"sp": {
			Utilities: map[actions.ActionID]*expr.Program{
				actA: mustNum(t, "Strength * 0.5 + is_current * 0.3", "Strength"),
				actB: mustNum(t, "Strength * 0.5 + is_current * 0.3", "Strength"),
			},
			Drives: []fauna.DriveRule{{ID: "hunger", Rate: 0.0}},
			Speed:  mustNum(t, "0.0"), AppTemp: mustNum(t, "0.0"),
			SteerChannel: map[actions.ActionID]core.Tag{
				actA: fauna.TagNoLoco, actB: fauna.TagNoLoco,
			},
			SmellRadius: 1.0, SightRadius: 1.0,
		},
	})
	// CurrentAction=actA: actA=1.3 wins.
	a := fauna.Animal{
		ID: "s1", Species: "sp", Pos: core.Vec2{},
		Stats:   map[core.StatID]float64{"Strength": 2.0},
		Drives:  map[fauna.DriveID]float64{"hunger": 0.0},
		Stamina: 1.0, Vital: 1.0, CurrentAction: actA, ActiveUntil: 100,
	}
	snap := makeSnap([]fauna.Animal{a}, nil, nil, openTerrain, 1, emptyEnv("s1"))
	intents := fauna.Step(snap, statRules, rng.New(1))
	if intents[0].Action != actA {
		t.Errorf("is_current boost (CurrentAction=actA): want actA to win, got %q", intents[0].Action)
	}

	// CurrentAction=actB: actB=1.3 wins over actA=1.0.
	a.CurrentAction = actB
	snap2 := makeSnap([]fauna.Animal{a}, nil, nil, openTerrain, 1, emptyEnv("s1"))
	intents2 := fauna.Step(snap2, statRules, rng.New(1))
	if intents2[0].Action != actB {
		t.Errorf("is_current boost (CurrentAction=actB): want actB to win, got %q", intents2[0].Action)
	}

	// AttrOperands: verify the returned list is sorted and contains expected operands.
	ops := fauna.AttrOperands()
	prev := ""
	for _, op := range ops {
		if string(op) <= prev {
			t.Errorf("AttrOperands not sorted: %q after %q", op, prev)
		}
		prev = string(op)
	}
	// Must contain "is_current", "scent.food", "dist.predator", etc.
	want := map[core.Tag]bool{
		"is_current": true, "scent.food": true, "dist.predator": true,
		"sight.predator": true, "apparent_temp": true, "wind.dir": true,
		"scent.carrion": true, "target.threat": true,
	}
	opSet := make(map[core.Tag]bool, len(ops))
	for _, op := range ops {
		opSet[op] = true
	}
	for op := range want {
		if !opSet[op] {
			t.Errorf("AttrOperands missing %q", op)
		}
	}
}

// ── AC: fauna-OFF neutrality ──────────────────────────────────────────────────

func TestFaunaOffNeutrality(t *testing.T) {
	// Empty rules: no candidates for any species.
	emptyRules := fauna.NewRules(nil)
	a := herbAnimal("h1", core.Vec2{}, nil)
	snap := makeSnap([]fauna.Animal{a}, nil, nil, openTerrain, 1, emptyEnv("h1"))
	r := rng.New(1)
	intents := fauna.Step(snap, emptyRules, r)
	if len(intents) != 1 {
		t.Errorf("empty rules: want 1 intent (dormant cheappath), got %d", len(intents))
	}
	// Action should be held (CurrentAction = actRest).
	if intents[0].Action != actRest {
		t.Errorf("empty rules: want held CurrentAction %q, got %q", actRest, intents[0].Action)
	}

	// Zero animals: returns nil/empty.
	snap0 := makeSnap(nil, nil, nil, openTerrain, 1, emptyEnv())
	intents0 := fauna.Step(snap0, emptyRules, rng.New(1))
	if len(intents0) != 0 {
		t.Errorf("zero animals: want 0 intents, got %d", len(intents0))
	}

	// nil rules: zero animals → no panic.
	intentsNil := fauna.Step(snap0, nil, rng.New(1))
	if len(intentsNil) != 0 {
		t.Errorf("nil rules + zero animals: want 0 intents, got %d", len(intentsNil))
	}
}

// ── AC: Read-only inputs ──────────────────────────────────────────────────────

func TestReadOnlyInputs(t *testing.T) {
	rules := newTestRules(t)
	a := herbAnimal("h1", core.Vec2{}, nil)
	a.ActiveUntil = 100

	sg := scent.New(2.0)
	sg.Deposit(scent.ChanFood, core.Vec2{X: 1, Y: 0}, 1.0)
	sg.Commit()
	sp := spatial.New(8.0)
	sp.Insert("h1", a.Pos)

	snap := makeSnap([]fauna.Animal{a}, sg, sp, openTerrain, 1, emptyEnv("h1"))

	// Capture pre-state.
	preFoodIntensity := sg.IntensityAt(scent.ChanFood, core.Vec2{X: 1, Y: 0})
	preNearby := sp.NearbyEntities(a.Pos, 10.0)
	preCands := rules.Candidates(spHerb)
	preAnimals := make([]fauna.Animal, len(snap.Animals))
	copy(preAnimals, snap.Animals)

	fauna.Step(snap, rules, rng.New(42))

	// Post-check.
	if sg.IntensityAt(scent.ChanFood, core.Vec2{X: 1, Y: 0}) != preFoodIntensity {
		t.Error("scent grid was mutated by Step")
	}
	postNearby := sp.NearbyEntities(a.Pos, 10.0)
	if len(postNearby) != len(preNearby) {
		t.Error("spatial index was mutated by Step")
	}
	postCands := rules.Candidates(spHerb)
	if len(postCands) != len(preCands) {
		t.Error("Rules was mutated by Step")
	}
	if len(snap.Animals) != len(preAnimals) || snap.Animals[0].ID != preAnimals[0].ID {
		t.Error("snap.Animals was mutated by Step")
	}
}

// ── AC: Missing EnvSample panics ─────────────────────────────────────────────

func TestMissingEnvSamplePanic(t *testing.T) {
	rules := newTestRules(t)
	a := herbAnimal("h1", core.Vec2{}, nil)
	snap := &fauna.Snapshot{
		Animals: []fauna.Animal{a},
		Scent:   scent.New(1.0),
		Spatial: spatial.New(8.0),
		Terrain: openTerrain,
		Env:     map[core.ObjectID]fauna.EnvSample{}, // missing "h1"
		Tick:    1, Cadence: defaultCadence(), DT: 1.0,
	}
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for missing EnvSample, got none")
		}
	}()
	fauna.Step(snap, rules, rng.New(1))
}

// ── AC: Determinism golden (D12) ──────────────────────────────────────────────

func intentDigest(intents []fauna.Intent) string {
	var sb strings.Builder
	for _, it := range intents {
		fmt.Fprintf(&sb, "A:%s Act:%s Pos:(%.15g,%.15g) H:%.15g AU:%d\n",
			it.Animal, it.Action, it.NextPos.X, it.NextPos.Y, it.NextHeading, it.ActiveUntil)
		// drives in sorted key order.
		driveKeys := make([]string, 0, len(it.Drives))
		for k := range it.Drives {
			driveKeys = append(driveKeys, string(k))
		}
		// sort without package import
		for i := 0; i < len(driveKeys); i++ {
			for j := i + 1; j < len(driveKeys); j++ {
				if driveKeys[j] < driveKeys[i] {
					driveKeys[i], driveKeys[j] = driveKeys[j], driveKeys[i]
				}
			}
		}
		for _, k := range driveKeys {
			fmt.Fprintf(&sb, "  D:%s=%.15g\n", k, it.Drives[fauna.DriveID(k)])
		}
	}
	h := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(h[:])
}

func TestDeterminismGolden(t *testing.T) {
	rules := newTestRules(t)

	a1 := herbAnimal("deer_a", core.Vec2{X: 5, Y: 5}, nil)
	a1.ActiveUntil = 0 // starts dormant
	a2 := predAnimal("wolf_b", core.Vec2{X: 20, Y: 20})

	runN := func(seed int64, N int) string {
		r := rng.New(seed)
		animals := []fauna.Animal{a1, a2}
		var allDigests strings.Builder
		for tick := core.Tick(1); tick <= core.Tick(N); tick++ {
			snap := makeSnap(animals, scent.New(1.0), spatial.New(8.0), openTerrain, tick,
				emptyEnv("deer_a", "wolf_b"))
			intents := fauna.Step(snap, rules, r)
			allDigests.WriteString(intentDigest(intents))
			allDigests.WriteByte('\n')
			// Advance animal drives for next tick.
			for _, it := range intents {
				for i := range animals {
					if animals[i].ID == it.Animal {
						animals[i].Drives = it.Drives
						animals[i].Pos = it.NextPos
						animals[i].Heading = it.NextHeading
						animals[i].ActiveUntil = it.ActiveUntil
					}
				}
			}
		}
		h := sha256.Sum256([]byte(allDigests.String()))
		return hex.EncodeToString(h[:])
	}

	d1 := runN(42, 20)
	d2 := runN(42, 20)
	if d1 != d2 {
		t.Errorf("determinism golden mismatch:\n  run1: %s\n  run2: %s", d1, d2)
	}
	t.Logf("golden digest (seed=42, N=20): %s", d1)
}

// ── AC: No wall-clock / no global rand / no forbidden imports ─────────────────

func TestNoForbiddenImports(t *testing.T) {
	implFiles := []string{"fauna.go", "rules.go", "rules_combat.go", "context.go", "step.go", "combat.go", "cheap.go"}

	forbiddenImports := map[string]string{
		"time":         "wall-clock import",
		"math/rand":    "global rand v1",
		"math/rand/v2": "global rand v2 (should be injected)",

		"github.com/dogring/bdg/engine/world":           "forbidden (world)",
		"github.com/dogring/bdg/engine/agent":           "forbidden (agent)",
		"github.com/dogring/bdg/engine/mind/planner":    "forbidden (planner)",
		"github.com/dogring/bdg/engine/space/navmap":    "forbidden (navmap)",
		"github.com/dogring/bdg/engine/space/field":     "forbidden (field — inject via HazardSampler interface, F35/D11)",
		"github.com/dogring/bdg/engine/env/climate":     "forbidden (climate)",
		"github.com/dogring/bdg/engine/mind/needs":      "forbidden (needs)",
		"github.com/dogring/bdg/engine/mind/stats":      "forbidden (stats)",
		"github.com/dogring/bdg/engine/mind/tom":        "forbidden (tom)",
		"github.com/dogring/bdg/engine/mind/values":     "forbidden (values)",
		"github.com/dogring/bdg/engine/mind/gates":      "forbidden (gates)",
		"github.com/dogring/bdg/engine/mind/perception": "forbidden (perception)",
	}

	fset := token.NewFileSet()
	for _, f := range implFiles {
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, imp := range af.Imports {
			path, uerr := strconv.Unquote(imp.Path.Value)
			if uerr != nil {
				t.Fatalf("unquote import %s in %s: %v", imp.Path.Value, f, uerr)
			}
			if desc, bad := forbiddenImports[path]; bad {
				t.Errorf("file %s imports forbidden %q (%s)", f, path, desc)
			}
		}
	}
}

// ── AC: No hardcoded species-name / action-name / drive-name in logic ─────────

func TestNoHardcodedIDs(t *testing.T) {
	// Implementation files must not contain hardcoded species names, action names,
	// or drive names that should come from content/Rules (D10).
	// Context.go and rules.go contain the module's own VOCABULARY (AttrOperands,
	// steer-channel tags) — those are checked via AttrOperands() and constants.
	implFiles := []string{"fauna.go", "rules.go", "rules_combat.go", "context.go", "step.go", "combat.go", "cheap.go"}

	// Species names that must NOT appear as literals in implementation logic.
	forbiddenSpecies := []string{`"deer"`, `"wolf"`, `"rabbit"`, `"goat"`, `"bear"`, `"fish_species"`}
	// Action names that must NOT appear as literals.
	forbiddenActions := []string{`"Graze"`, `"Flee"`, `"Wary"`, `"Hunt"`, `"MoveTo"`, `"Rest"`}
	// Drive names that must NOT appear as literals.
	forbiddenDrives := []string{`"hunger"`, `"fatigue"`, `"repro_readiness"`}

	for _, f := range implFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", f, err)
		}
		// Scan only non-comment lines so that doc examples in comments are not
		// flagged. Lines starting with // (after trimming) are documentation, not logic.
		for lineNum, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, s := range forbiddenSpecies {
				if strings.Contains(line, s) {
					t.Errorf("file %s line %d contains hardcoded species name %q (D10 violation)", f, lineNum+1, s)
				}
			}
			for _, s := range forbiddenActions {
				if strings.Contains(line, s) {
					t.Errorf("file %s line %d contains hardcoded action name %q (D10 violation)", f, lineNum+1, s)
				}
			}
			for _, s := range forbiddenDrives {
				if strings.Contains(line, s) {
					t.Errorf("file %s line %d contains hardcoded drive name %q (D10 violation)", f, lineNum+1, s)
				}
			}
		}
	}
}

// ── AC: Scent controller-side read ───────────────────────────────────────────

func TestScentControllerSide(t *testing.T) {
	rules := newTestRules(t)

	// Deposit predator scent in the herbivore's own cell → wakes the animal + sets fear.
	sg := scent.New(1.0)
	sg.Deposit(scent.ChanPredator, core.Vec2{X: 0, Y: 0}, 2.0)
	sg.Commit()

	a := herbAnimal("deer1", core.Vec2{X: 0, Y: 0},
		map[fauna.DriveID]float64{"hunger": 0.0, "fear": 0.0, "thermal": 0.0})
	a.ActiveUntil = 0 // dormant, no cooldown

	snap := makeSnap([]fauna.Animal{a}, sg, nil, openTerrain, 5, emptyEnv("deer1"))
	intents := fauna.Step(snap, rules, rng.New(1))
	it := intents[0]

	// Animal should be WOKEN: ActiveUntil = 5 + 5 = 10.
	if it.ActiveUntil != 10 {
		t.Errorf("predator-scent wake: ActiveUntil want 10, got %d", it.ActiveUntil)
	}
	// Fear should be set to WaryLevel (0.4) from scent.predator.
	if math.Abs(it.Drives["fear"]-0.4) > 1e-10 {
		t.Errorf("scent.predator: fear want 0.4, got %v", it.Drives["fear"])
	}
}

// ── AC: Shared-registry scoring ───────────────────────────────────────────────

func TestSharedRegistryScoring(t *testing.T) {
	// Build an actions.Registry from YAML text to verify Candidates ⊆ IDs().
	yamlText := `
schema_version: 1
actions:
  - id: Graze
    tags: [fauna, feed]
    duration: 5
  - id: Flee
    tags: [fauna, locomotion]
    duration: 1
  - id: Wary
    tags: [fauna, locomotion]
    duration: 1
  - id: Rest
    tags: [fauna, rest]
    duration: 3
  - id: Hunt
    tags: [fauna, attack]
    duration: 5
`
	reg, err := actions.Load(strings.NewReader(yamlText))
	if err != nil {
		t.Fatalf("actions.Load: %v", err)
	}
	regIDs := make(map[actions.ActionID]bool, reg.Len())
	for _, id := range reg.IDs() {
		regIDs[id] = true
	}

	rules := newTestRules(t)
	// All candidates for each species must be in the registry.
	for _, sp := range []fauna.SpeciesID{spHerb, spPred} {
		for _, cand := range rules.Candidates(sp) {
			if !regIDs[cand] {
				t.Errorf("species %q candidate %q not in actions.Registry.IDs()", sp, cand)
			}
		}
	}
}
