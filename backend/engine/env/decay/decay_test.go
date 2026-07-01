package decay_test

// AC test coverage:
//  AC-1:  Discrete state derived from DecayAge (Q1/Dm1/D9) — StateAt table-driven
//  AC-2:  Effective-rate accumulation (Dm1/Dm2) — DecayAge += elapsedTicks·rate; table-driven
//  AC-3:  Env-coupled accel is multiplicative §6 (Q2/Dm2/Dm3) — warm+wet vs cold+dry via expr.Parse fixture
//  AC-4:  Storage rate multiplier slows/halts decay (Dm2) — StorageRateMult < 1 and = 0; table-driven
//  AC-5:  Transform on transition (Q3/D9) — crossing a state emits products; multi-state emits all; terminal gone; table-driven
//  AC-6:  Transform qty scales with lot Qty (Dm5) — n-lot produces n·rule.Qty; lots never auto-merge
//  AC-7:  Per-state supply override (D9) — SupplyAt returns override when present, nil otherwise
//  AC-8:  Owner-agnostic sorted-order determinism (Dm4/D12) — shuffled inputs map → same sorted deltas
//  AC-9:  Determinism golden (D12) — fixed scenario × 2 runs → byte-identical digest
//  AC-10: Resume invariant — snapshot at T, continue → same as uninterrupted
//  AC-11: Missing EnvInput panics (world-contract guard)
//  AC-12: Decay-off neutrality — empty Rules → no transitions/transforms, DecayAge unchanged
//  AC-13: No forbidden imports (go/parser guard)
//  AC-14: No hardcoded constants/IDs in implementation (D10 grep guard)

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/env/decay"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/kernel/rng"
)

// ── Test helpers ──────────────────────────────────────────────────────────────

// noStats satisfies expr.StatSet with an empty set (decay accel formulas reference no stats).
type noStats struct{}

func (noStats) Has(core.StatID) bool { return false }

// mustNum compiles a §6 numeric formula for use as an accel Program.
func mustNum(t *testing.T, text string) *expr.Program {
	t.Helper()
	prog, err := expr.Parse(text, expr.KindNum, noStats{}, nil)
	if err != nil {
		t.Fatalf("expr.Parse(%q): %v", text, err)
	}
	return prog
}

// constAccel compiles a constant-valued accel program (avoids hardcoded literal in tests).
func constAccel(t *testing.T, v float64) *expr.Program {
	t.Helper()
	return mustNum(t, fmt.Sprintf("%.15g", v))
}

// makeKind builds a KindRule with the given parameters.
// states is a list of (threshold, supply, transforms).
func makeKind(baseRate float64, accel *expr.Program, states []decay.StateRule) decay.KindRule {
	return decay.KindRule{
		BaseRate: baseRate,
		Accel:    accel,
		States:   states,
	}
}

// freshLot builds a Lot for tests.
func freshLot(id string, kind decay.KindID, qty int) decay.Lot {
	return decay.Lot{
		ID:       core.ObjectID(id),
		Kind:     kind,
		Qty:      qty,
		DecayAge: 0,
	}
}

// lotWithAge builds a Lot with a pre-set DecayAge.
func lotWithAge(id string, kind decay.KindID, qty int, age float64) decay.Lot {
	return decay.Lot{
		ID:       core.ObjectID(id),
		Kind:     kind,
		Qty:      qty,
		DecayAge: age,
	}
}

// envInput builds a neutral EnvInput (temperature=15, moisture=0.5, storage=1).
func neutralEnv() decay.EnvInput {
	return decay.EnvInput{Temperature: 15.0, Moisture: 0.5, StorageRateMult: 1.0}
}

// envFor builds an inputs map for a single lot with the given EnvInput.
func envFor(id string, in decay.EnvInput) map[core.ObjectID]decay.EnvInput {
	return map[core.ObjectID]decay.EnvInput{core.ObjectID(id): in}
}

// envMany builds an inputs map from a list of (id, input) pairs (no map-range — built
// from a slice for determinism in test helper construction).
func envMany(pairs ...interface{}) map[core.ObjectID]decay.EnvInput {
	m := make(map[core.ObjectID]decay.EnvInput)
	for i := 0; i+1 < len(pairs); i += 2 {
		id := core.ObjectID(pairs[i].(string))
		in := pairs[i+1].(decay.EnvInput)
		m[id] = in
	}
	return m
}

// testRNG returns a fresh seeded RNG for test use.
func testRNG() *rng.RNG { return rng.New(42) }

// ── AC-1: Discrete state derived from DecayAge (Q1/Dm1/D9) ───────────────────

func TestStateAtDerivedFromDecayAge(t *testing.T) {
	// Three-state kind: fresh (thresh 0), stale (thresh 5), gone (thresh 10, terminal).
	// StateAt table: below thresh 5 → 0; at/above 5 → 1; at/above 10 → 2.
	rules := decay.NewRules(map[decay.KindID]decay.KindRule{
		"fruit": makeKind(0, nil, []decay.StateRule{
			{Threshold: 0},  // state 0: fresh
			{Threshold: 5},  // state 1: stale
			{Threshold: 10}, // state 2: gone (terminal)
		}),
	})

	cases := []struct {
		age    float64
		wantSt int
		desc   string
	}{
		{0.0, 0, "below first non-zero threshold → state 0"},
		{4.9, 0, "just below stale threshold → state 0"},
		{5.0, 1, "at stale threshold → state 1"},
		{5.1, 1, "above stale, below terminal → state 1"},
		{9.9, 1, "just below terminal threshold → state 1"},
		{10.0, 2, "at terminal threshold → state 2"},
		{100.0, 2, "far above terminal → state 2 (capped)"},
	}
	for _, tc := range cases {
		got := rules.StateAt("fruit", tc.age)
		if got != tc.wantSt {
			t.Errorf("StateAt(%q, %.1f): got %d, want %d (%s)", "fruit", tc.age, got, tc.wantSt, tc.desc)
		}
	}

	// Unknown kind → 0 (decay-off).
	if got := rules.StateAt("no_such_kind", 99); got != 0 {
		t.Errorf("unknown kind: got %d, want 0", got)
	}
	// nil rules → 0.
	var nilRules *decay.Rules
	if got := nilRules.StateAt("fruit", 5); got != 0 {
		t.Errorf("nil rules: got %d, want 0", got)
	}
}

// ── AC-2: Effective-rate accumulation (Dm1/Dm2) ──────────────────────────────

func TestEffectiveRateAccumulation(t *testing.T) {
	// A kind with constant accel=1, StorageRateMult=1: age advances = elapsedTicks * baseRate.
	cases := []struct {
		baseRate     float64
		accel        float64
		storageMult  float64
		elapsedTicks int64
		wantAge      float64 // after one step from age=0
		desc         string
	}{
		{1.0, 1.0, 1.0, 1, 1.0, "base 1, accel 1, storage 1, 1 tick"},
		{0.5, 1.0, 1.0, 1, 0.5, "base 0.5, 1 tick"},
		{1.0, 1.0, 1.0, 2, 2.0, "doubling elapsedTicks doubles the advance"},
		{2.0, 1.0, 1.0, 1, 2.0, "higher baseRate ages faster"},
		{1.0, 2.0, 1.0, 1, 2.0, "accel=2 doubles the advance"},
		{1.0, 1.0, 0.5, 1, 0.5, "storageMult=0.5 halves the advance"},
		{1.0, 1.0, 0.0, 5, 0.0, "storageMult=0 halts aging entirely"},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			kind := decay.KindID("perishable")
			rules := decay.NewRules(map[decay.KindID]decay.KindRule{
				kind: makeKind(tc.baseRate, constAccel(t, tc.accel), []decay.StateRule{
					{Threshold: 0},
					{Threshold: 999}, // terminal far away
				}),
			})
			lot := freshLot("L", kind, 1)
			in := decay.EnvInput{
				Temperature:     0,
				Moisture:        0,
				StorageRateMult: tc.storageMult,
			}
			s := decay.New([]decay.Lot{lot})
			s, deltas := decay.Step(s, envFor("L", in), tc.elapsedTicks, rules, testRNG())

			// After one step the lot should be in Aged (state unchanged) or Transitioned.
			var gotAge float64
			switch {
			case len(deltas.Aged) > 0:
				if deltas.Aged[0].ID != "L" {
					t.Fatalf("unexpected aged ID")
				}
				gotAge = deltas.Aged[0].DecayAge
			case len(deltas.Transitioned) > 0:
				gotAge = deltas.Transitioned[0].DecayAge
			default:
				// No delta: age unchanged (storageMult=0 case)
				gotAge = s.Lots()[0].DecayAge
			}

			const eps = 1e-12
			if diff := gotAge - tc.wantAge; diff < -eps || diff > eps {
				t.Errorf("DecayAge: got %.15g, want %.15g", gotAge, tc.wantAge)
			}

			// AgeDelta/TransitionDelta carries the new ABSOLUTE DecayAge.
			// Also verify that the next State contains the same age.
			if len(s.Lots()) > 0 {
				if diff := s.Lots()[0].DecayAge - tc.wantAge; diff < -eps || diff > eps {
					t.Errorf("next State DecayAge: got %.15g, want %.15g", s.Lots()[0].DecayAge, tc.wantAge)
				}
			}
		})
	}
}

// ── AC-3: Env-coupled accel is multiplicative §6 (Q2/Dm2/Dm3) ────────────────

func TestEnvCoupledAccelMultiplicative(t *testing.T) {
	// accel formula: "temperature * 0.05 + moisture * 0.5"
	// warm+wet (T=30, M=1.0) → accel = 30*0.05 + 1.0*0.5 = 1.5 + 0.5 = 2.0
	// cold+dry (T=5, M=0.1)  → accel = 5*0.05 + 0.1*0.5  = 0.25 + 0.05 = 0.30
	//
	// This formula is compiled via expr.Parse — NOT hardcoded in logic (D10).
	accelProg := mustNum(t, "temperature * 0.05 + moisture * 0.5")

	kind := decay.KindID("veggie")
	const baseRate = 1.0
	rules := decay.NewRules(map[decay.KindID]decay.KindRule{
		kind: makeKind(baseRate, accelProg, []decay.StateRule{
			{Threshold: 0},
			{Threshold: 9999}, // terminal far away
		}),
	})

	// Verify Accel() method directly.
	warmWet := decay.EnvInput{Temperature: 30, Moisture: 1.0, StorageRateMult: 1.0}
	coldDry := decay.EnvInput{Temperature: 5, Moisture: 0.1, StorageRateMult: 1.0}

	const eps = 1e-10
	wantWarm := 2.0
	wantCold := 0.30
	if got := rules.Accel(kind, warmWet); abs64(got-wantWarm) > eps {
		t.Errorf("Accel warm+wet: got %.15g, want %.15g", got, wantWarm)
	}
	if got := rules.Accel(kind, coldDry); abs64(got-wantCold) > eps {
		t.Errorf("Accel cold+dry: got %.15g, want %.15g", got, wantCold)
	}

	// Simulate two lots differing only in env over 10 steps.
	const steps = 10
	lotW := freshLot("warm", kind, 1)
	lotC := freshLot("cold", kind, 1)

	sw := decay.New([]decay.Lot{lotW})
	sc := decay.New([]decay.Lot{lotC})
	r := testRNG()
	for i := 0; i < steps; i++ {
		sw, _ = decay.Step(sw, envFor("warm", warmWet), 1, rules, r)
		sc, _ = decay.Step(sc, envFor("cold", coldDry), 1, rules, r)
	}

	warmAge := sw.Lots()[0].DecayAge // ~= 10 * 1.0 * 2.0 * 1.0 = 20.0
	coldAge := sc.Lots()[0].DecayAge // ~= 10 * 1.0 * 0.30 * 1.0 = 3.0
	if warmAge <= coldAge {
		t.Errorf("warm+wet should age faster: warmAge=%.4f, coldAge=%.4f", warmAge, coldAge)
	}

	// effectiveRate = baseRate · accel · StorageRateMult (Dm2(a))
	wantWarmAge := float64(steps) * baseRate * wantWarm * 1.0
	wantColdAge := float64(steps) * baseRate * wantCold * 1.0
	if abs64(warmAge-wantWarmAge) > eps {
		t.Errorf("warm lot age: got %.15g, want %.15g", warmAge, wantWarmAge)
	}
	if abs64(coldAge-wantColdAge) > eps {
		t.Errorf("cold lot age: got %.15g, want %.15g", coldAge, wantColdAge)
	}
}

// ── AC-4: Storage rate multiplier slows/halts decay (Dm2) ────────────────────

func TestStorageRateMultiplier(t *testing.T) {
	kind := decay.KindID("meat")
	const baseRate = 1.0
	rules := decay.NewRules(map[decay.KindID]decay.KindRule{
		kind: makeKind(baseRate, constAccel(t, 1.0), []decay.StateRule{
			{Threshold: 0},
			{Threshold: 9999},
		}),
	})

	cases := []struct {
		mult    float64
		ticks   int64
		wantAge float64
		desc    string
	}{
		{1.0, 5, 5.0, "neutral storage: full decay"},
		{0.5, 5, 2.5, "half-speed storage: half decay"},
		{0.1, 5, 0.5, "cold cellar: one-tenth decay"},
		{0.0, 5, 0.0, "halted: no aging at all"},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			lot := freshLot("L", kind, 1)
			in := decay.EnvInput{Temperature: 0, Moisture: 0, StorageRateMult: tc.mult}
			s := decay.New([]decay.Lot{lot})
			s, _ = decay.Step(s, envFor("L", in), tc.ticks, rules, testRNG())
			gotAge := s.Lots()[0].DecayAge
			const eps = 1e-12
			if abs64(gotAge-tc.wantAge) > eps {
				t.Errorf("DecayAge: got %.15g, want %.15g (%s)", gotAge, tc.wantAge, tc.desc)
			}
		})
	}
}

// ── AC-5: Transform on transition (Q3/D9) ────────────────────────────────────

func TestTransformOnTransition(t *testing.T) {
	// Four-state kind:
	//   state 0 (thresh 0): fresh  — no transform
	//   state 1 (thresh 5): stale  — transform: 2 × "seeds"
	//   state 2 (thresh 8): rotten — transform: 1 × "compost"
	//   state 3 (thresh 12): gone  — terminal, no transform
	kind := decay.KindID("apple")
	rules := decay.NewRules(map[decay.KindID]decay.KindRule{
		kind: makeKind(1.0, constAccel(t, 1.0), []decay.StateRule{
			{Threshold: 0}, // 0: fresh
			{Threshold: 5, Transform: []decay.TransformRule{{Item: "seeds", Qty: 2}}},   // 1: stale
			{Threshold: 8, Transform: []decay.TransformRule{{Item: "compost", Qty: 1}}}, // 2: rotten
			{Threshold: 12}, // 3: gone (terminal, no transform)
		}),
	})

	t.Run("single state transition emits transform", func(t *testing.T) {
		// Start at age 4 (state 0), step by 2 ticks → age 6 (state 1: stale).
		lot := lotWithAge("A", kind, 1, 4.0)
		in := decay.EnvInput{Temperature: 0, Moisture: 0, StorageRateMult: 1.0}
		s := decay.New([]decay.Lot{lot})
		_, deltas := decay.Step(s, envFor("A", in), 2, rules, testRNG())

		if len(deltas.Transitioned) != 1 || deltas.Transitioned[0].ID != "A" {
			t.Fatalf("expected 1 Transitioned, got %d", len(deltas.Transitioned))
		}
		if deltas.Transitioned[0].State != 1 {
			t.Errorf("Transitioned.State: got %d, want 1", deltas.Transitioned[0].State)
		}
		if len(deltas.Transformed) != 1 {
			t.Fatalf("expected 1 Transformed, got %d", len(deltas.Transformed))
		}
		tr := deltas.Transformed[0]
		if tr.Item != "seeds" || tr.Qty != 2 || tr.State != 1 || tr.SourceID != "A" {
			t.Errorf("Transformed: got {%v,%v,%v}, want {seeds,2,state1}", tr.Item, tr.Qty, tr.State)
		}
		if len(deltas.Gone) != 0 {
			t.Errorf("should not be Gone yet, got %d", len(deltas.Gone))
		}
	})

	t.Run("multi-state crossing emits all transforms in ascending state order", func(t *testing.T) {
		// Start at age 0 (state 0), step by 10 ticks → age 10 (state 2: rotten).
		// Crossed state 1 (stale, seeds) and state 2 (rotten, compost) in one step.
		lot := freshLot("B", kind, 1)
		in := decay.EnvInput{Temperature: 0, Moisture: 0, StorageRateMult: 1.0}
		s := decay.New([]decay.Lot{lot})
		_, deltas := decay.Step(s, envFor("B", in), 10, rules, testRNG())

		if len(deltas.Transitioned) != 1 || deltas.Transitioned[0].ID != "B" {
			t.Fatalf("expected 1 Transitioned")
		}
		if deltas.Transitioned[0].State != 2 {
			t.Errorf("Transitioned.State: got %d, want 2 (rotten)", deltas.Transitioned[0].State)
		}
		// Transformed: seeds (state 1) first, then compost (state 2) — ascending state order.
		if len(deltas.Transformed) != 2 {
			t.Fatalf("expected 2 Transformed (one per crossed state), got %d", len(deltas.Transformed))
		}
		// Transformed[0] is the first in ascending state order: state 1 = seeds.
		if deltas.Transformed[0].Item != "seeds" || deltas.Transformed[0].State != 1 {
			t.Errorf("[0] expected state 1 seeds, got state %d %v", deltas.Transformed[0].State, deltas.Transformed[0].Item)
		}
		// After sort: SourceID "B" (only one lot), State ascending: state 1 = seeds, state 2 = compost.
		if deltas.Transformed[0].State > deltas.Transformed[1].State {
			t.Errorf("transforms not in ascending state order: states %d, %d",
				deltas.Transformed[0].State, deltas.Transformed[1].State)
		}
		// Verify state 1 = seeds and state 2 = compost.
		byState := map[int]decay.TransformOut{}
		for _, tr := range deltas.Transformed {
			byState[tr.State] = tr
		}
		if byState[1].Item != "seeds" {
			t.Errorf("state 1 transform: got %v, want seeds", byState[1].Item)
		}
		if byState[2].Item != "compost" {
			t.Errorf("state 2 transform: got %v, want compost", byState[2].Item)
		}
		if len(deltas.Gone) != 0 {
			t.Errorf("should not be Gone (state 2 is not terminal), got %d", len(deltas.Gone))
		}
	})

	t.Run("terminal gone state: no transform, mass removed", func(t *testing.T) {
		// Start at age 10 (state 2), step by 3 ticks → age 13 (state 3: gone, terminal).
		lot := lotWithAge("C", kind, 1, 10.0)
		in := decay.EnvInput{Temperature: 0, Moisture: 0, StorageRateMult: 1.0}
		s := decay.New([]decay.Lot{lot})
		s2, deltas := decay.Step(s, envFor("C", in), 3, rules, testRNG())

		if len(deltas.Gone) != 1 || deltas.Gone[0] != "C" {
			t.Errorf("expected Gone[C], got %v", deltas.Gone)
		}
		// No transform for the terminal gone state.
		for _, tr := range deltas.Transformed {
			if tr.State == 3 {
				t.Errorf("terminal gone state should produce no transform, got %+v", tr)
			}
		}
		// Lot should be removed from next State.
		if len(s2.Lots()) != 0 {
			t.Errorf("lot should be gone from next State, got %d lots", len(s2.Lots()))
		}
	})

	t.Run("multi-state crossing into terminal emits pre-terminal transforms then Gone", func(t *testing.T) {
		// Start at age 0, step by 15 → age 15 (crosses state 1, 2, and terminal 3).
		lot := freshLot("D", kind, 1)
		in := decay.EnvInput{Temperature: 0, Moisture: 0, StorageRateMult: 1.0}
		s := decay.New([]decay.Lot{lot})
		s2, deltas := decay.Step(s, envFor("D", in), 15, rules, testRNG())

		// Should be in Gone and Transitioned (terminal).
		if len(deltas.Gone) != 1 || deltas.Gone[0] != "D" {
			t.Errorf("expected Gone[D], got %v", deltas.Gone)
		}
		// Transforms: state 1 (seeds) and state 2 (compost) — state 3 has no transform.
		if len(deltas.Transformed) != 2 {
			t.Fatalf("expected 2 transforms (states 1+2), got %d", len(deltas.Transformed))
		}
		// Lot removed from state.
		if len(s2.Lots()) != 0 {
			t.Errorf("lot should be gone from next State")
		}
	})
}

// ── AC-6: Transform qty scales with lot Qty (Dm5) ────────────────────────────

func TestTransformQtyScalesWithLotQty(t *testing.T) {
	kind := decay.KindID("grain")
	// One transition at threshold 5 that produces 3 × "flour".
	rules := decay.NewRules(map[decay.KindID]decay.KindRule{
		kind: makeKind(1.0, constAccel(t, 1.0), []decay.StateRule{
			{Threshold: 0}, // state 0: fresh
			{Threshold: 5, Transform: []decay.TransformRule{{Item: "flour", Qty: 3}}},
			{Threshold: 999}, // terminal
		}),
	})

	for _, qty := range []int{1, 4, 10} {
		t.Run(fmt.Sprintf("qty=%d", qty), func(t *testing.T) {
			lot := freshLot("L", kind, qty)
			in := decay.EnvInput{Temperature: 0, Moisture: 0, StorageRateMult: 1.0}
			s := decay.New([]decay.Lot{lot})
			_, deltas := decay.Step(s, envFor("L", in), 5, rules, testRNG())

			wantQty := 3 * qty
			if len(deltas.Transformed) != 1 {
				t.Fatalf("expected 1 Transformed, got %d", len(deltas.Transformed))
			}
			if deltas.Transformed[0].Qty != wantQty {
				t.Errorf("Qty: got %d, want %d (3 × %d)", deltas.Transformed[0].Qty, wantQty, qty)
			}
		})
	}

	// Lots never auto-merge: two lots of the same kind age independently.
	t.Run("no auto-merge: two lots age independently", func(t *testing.T) {
		lot1 := lotWithAge("L1", kind, 5, 4.0) // near transition
		lot2 := freshLot("L2", kind, 5)        // just created, far from transition
		in := decay.EnvInput{Temperature: 0, Moisture: 0, StorageRateMult: 1.0}
		s := decay.New([]decay.Lot{lot1, lot2})
		inputs := envMany("L1", in, "L2", in)
		_, deltas := decay.Step(s, inputs, 2, rules, testRNG())

		// Only lot1 transitions (age 4+2=6 >= 5); lot2 stays at age 2 (no transition).
		if len(deltas.Transitioned) != 1 || deltas.Transitioned[0].ID != "L1" {
			t.Errorf("only L1 should transition, got %v", deltas.Transitioned)
		}
		if len(deltas.Aged) != 1 || deltas.Aged[0].ID != "L2" {
			t.Errorf("only L2 should age, got %v", deltas.Aged)
		}
	})
}

// ── AC-7: Per-state supply override (D9) ────────────────────────────────────

func TestPerStateSupplyOverride(t *testing.T) {
	kind := decay.KindID("bread")
	freshSupply := map[core.Dimension]float64{"satiety": 10.0}
	staleSupply := map[core.Dimension]float64{"satiety": 4.0}
	rules := decay.NewRules(map[decay.KindID]decay.KindRule{
		kind: makeKind(1.0, constAccel(t, 1.0), []decay.StateRule{
			{Threshold: 0, Supply: freshSupply}, // state 0: fresh (10 satiety)
			{Threshold: 5, Supply: staleSupply}, // state 1: stale (4 satiety)
			{Threshold: 10},                     // state 2: gone (nil supply)
		}),
	})

	// State 0: returns fresh supply.
	s0 := rules.SupplyAt(kind, 0)
	if s0["satiety"] != 10.0 {
		t.Errorf("state 0 supply: got %v, want 10.0", s0["satiety"])
	}

	// State 1: returns stale supply (decayed food feeds less, D9).
	s1 := rules.SupplyAt(kind, 1)
	if s1["satiety"] != 4.0 {
		t.Errorf("state 1 supply: got %v, want 4.0", s1["satiety"])
	}

	// State 2 (terminal gone): nil supply (content defines no override).
	s2 := rules.SupplyAt(kind, 2)
	if s2 != nil {
		t.Errorf("state 2 supply: got %v, want nil", s2)
	}

	// Unknown kind → nil.
	if got := rules.SupplyAt("no_such", 0); got != nil {
		t.Errorf("unknown kind SupplyAt: got %v, want nil", got)
	}
}

// ── AC-8: Owner-agnostic sorted-order determinism (Dm4/D12) ─────────────────

func TestOwnerAgnosticSortedOrderDeterminism(t *testing.T) {
	// Mix of lots with different IDs to verify alphabetical sort of output slices.
	// IDs chosen to test ordering: "z_lot" < "a_lot" lexicographically — wait no:
	// "a_lot" < "z_lot" in ASCII. Let's use IDs that would reveal wrong order.
	kind := decay.KindID("fish")
	rules := decay.NewRules(map[decay.KindID]decay.KindRule{
		kind: makeKind(1.0, constAccel(t, 1.0), []decay.StateRule{
			{Threshold: 0},
			{Threshold: 999},
		}),
	})

	// Three lots in non-alphabetical construction order.
	lots := []decay.Lot{
		freshLot("z_lot", kind, 1),
		freshLot("a_lot", kind, 1),
		freshLot("m_lot", kind, 1),
	}
	in := decay.EnvInput{Temperature: 0, Moisture: 0, StorageRateMult: 1.0}
	inputs := envMany("z_lot", in, "a_lot", in, "m_lot", in)

	s := decay.New(lots)
	_, deltas := decay.Step(s, inputs, 1, rules, testRNG())

	// Aged slice must be in ascending ObjectID order.
	if len(deltas.Aged) != 3 {
		t.Fatalf("expected 3 Aged, got %d", len(deltas.Aged))
	}
	wantOrder := []core.ObjectID{"a_lot", "m_lot", "z_lot"}
	for i, id := range wantOrder {
		if deltas.Aged[i].ID != id {
			t.Errorf("Aged[%d].ID = %q, want %q", i, deltas.Aged[i].ID, id)
		}
	}

	// Re-run with inputs map built in a DIFFERENT order → must produce identical deltas.
	// (inputs map has no defined iteration order; we build it differently here.)
	inputs2 := map[core.ObjectID]decay.EnvInput{
		"a_lot": in,
		"m_lot": in,
		"z_lot": in,
	}
	s2 := decay.New(lots)
	_, deltas2 := decay.Step(s2, inputs2, 1, rules, testRNG())

	if len(deltas2.Aged) != 3 {
		t.Fatalf("second run: expected 3 Aged, got %d", len(deltas2.Aged))
	}
	for i := range deltas.Aged {
		if deltas.Aged[i] != deltas2.Aged[i] {
			t.Errorf("deltas differ at Aged[%d]: %+v vs %+v", i, deltas.Aged[i], deltas2.Aged[i])
		}
	}
}

func TestWithLotInsertsSortedAndPure(t *testing.T) {
	kind := decay.KindID("carcass")
	prev := decay.New([]decay.Lot{
		freshLot("lot_a", kind, 1),
		freshLot("lot_z", kind, 1),
	})
	next := prev.WithLot(lotWithAge("lot_m", kind, 2, 0.25))

	prevLots := prev.Lots()
	if len(prevLots) != 2 || prevLots[0].ID != "lot_a" || prevLots[1].ID != "lot_z" {
		t.Fatalf("prev State mutated or misordered: %+v", prevLots)
	}
	nextLots := next.Lots()
	want := []core.ObjectID{"lot_a", "lot_m", "lot_z"}
	if len(nextLots) != len(want) {
		t.Fatalf("next lots len=%d, want %d: %+v", len(nextLots), len(want), nextLots)
	}
	for i, id := range want {
		if nextLots[i].ID != id {
			t.Fatalf("next lot[%d].ID=%q, want %q; lots=%+v", i, nextLots[i].ID, id, nextLots)
		}
	}
	if nextLots[1].Kind != kind || nextLots[1].Qty != 2 || nextLots[1].DecayAge != 0.25 {
		t.Fatalf("inserted lot changed: %+v", nextLots[1])
	}
}

func TestWithLotDuplicatePanics(t *testing.T) {
	s := decay.New([]decay.Lot{freshLot("lot_a", "carcass", 1)})
	defer func() {
		if recover() == nil {
			t.Fatal("WithLot did not panic on duplicate ObjectID")
		}
	}()
	_ = s.WithLot(freshLot("lot_a", "carcass", 1))
}

func TestWithLotInjectedLotDecaysOnStep(t *testing.T) {
	kind := decay.KindID("carcass")
	rules := decay.NewRules(map[decay.KindID]decay.KindRule{
		kind: makeKind(1.0, constAccel(t, 1.0), []decay.StateRule{
			{Threshold: 0},
			{Threshold: 3, Transform: []decay.TransformRule{{Item: "bone", Qty: 1}}},
			{Threshold: 9},
		}),
	})
	prev := decay.New([]decay.Lot{freshLot("lot_a", kind, 1)})
	injected := lotWithAge("lot_b", kind, 2, 1)
	withInjected := prev.WithLot(injected)

	next, deltas := decay.Step(
		withInjected,
		envMany("lot_a", neutralEnv(), "lot_b", neutralEnv()),
		2,
		rules,
		testRNG(),
	)
	nextLots := next.Lots()
	if len(nextLots) != 2 {
		t.Fatalf("next lots len=%d, want 2: %+v", len(nextLots), nextLots)
	}
	if nextLots[1].ID != "lot_b" || abs64(nextLots[1].DecayAge-3) > 1e-12 {
		t.Fatalf("injected lot did not decay like an existing lot: %+v", nextLots[1])
	}
	if len(deltas.Transitioned) != 1 || deltas.Transitioned[0].ID != "lot_b" || deltas.Transitioned[0].State != 1 {
		t.Fatalf("injected lot transition missing/wrong: %+v", deltas.Transitioned)
	}
	if len(deltas.Transformed) != 1 ||
		deltas.Transformed[0].SourceID != "lot_b" ||
		deltas.Transformed[0].Item != "bone" ||
		deltas.Transformed[0].Qty != 2 {
		t.Fatalf("injected lot transform missing/wrong: %+v", deltas.Transformed)
	}
}

// ── AC-9: Determinism golden (D12) ──────────────────────────────────────────

// digestStep produces a deterministic hash of one step's outputs.
func digestStep(lots []decay.Lot, d decay.StepDeltas) string {
	var sb strings.Builder
	// Lots in sorted order.
	for _, l := range lots {
		fmt.Fprintf(&sb, "L{%s %s qty=%d age=%.15f}\n", l.ID, l.Kind, l.Qty, l.DecayAge)
	}
	// Aged.
	for _, a := range d.Aged {
		fmt.Fprintf(&sb, "A{%s age=%.15f}\n", a.ID, a.DecayAge)
	}
	// Transitioned.
	for _, tr := range d.Transitioned {
		fmt.Fprintf(&sb, "T{%s state=%d age=%.15f}\n", tr.ID, tr.State, tr.DecayAge)
	}
	// Transformed.
	for _, tx := range d.Transformed {
		fmt.Fprintf(&sb, "TX{%s state=%d item=%s qty=%d}\n", tx.SourceID, tx.State, tx.Item, tx.Qty)
	}
	// Gone.
	for _, g := range d.Gone {
		fmt.Fprintf(&sb, "G{%s}\n", g)
	}
	h := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(h[:])
}

func runDecayScenario(t *testing.T, seed int64, steps int) string {
	t.Helper()
	// A multi-lot, multi-step scenario: 3 lots of different kinds, varying env.
	// accel formula uses temperature and moisture (compiled via expr.Parse — not hardcoded).
	accel := mustNum(t, "temperature * 0.01 + moisture * 0.5")

	kind1 := decay.KindID("kd1")
	kind2 := decay.KindID("kd2")
	rules := decay.NewRules(map[decay.KindID]decay.KindRule{
		kind1: makeKind(0.3, accel, []decay.StateRule{
			{Threshold: 0},
			{Threshold: 2.0, Transform: []decay.TransformRule{{Item: "by1", Qty: 1}}},
			{Threshold: 4.0}, // terminal
		}),
		kind2: makeKind(0.1, constAccel(t, 1.0), []decay.StateRule{
			{Threshold: 0},
			{Threshold: 5.0},
		}),
	})

	lots := []decay.Lot{
		freshLot("lot_a", kind1, 2),
		freshLot("lot_b", kind2, 1),
		lotWithAge("lot_c", kind1, 3, 1.5),
	}

	envA := decay.EnvInput{Temperature: 20, Moisture: 0.6, StorageRateMult: 1.0}
	envB := decay.EnvInput{Temperature: 10, Moisture: 0.3, StorageRateMult: 0.5}
	envC := decay.EnvInput{Temperature: 25, Moisture: 0.8, StorageRateMult: 1.0}

	s := decay.New(lots)
	r := rng.New(seed)
	var allDigests strings.Builder
	for i := 0; i < steps; i++ {
		inputs := map[core.ObjectID]decay.EnvInput{
			"lot_a": envA,
			"lot_b": envB,
			"lot_c": envC,
		}
		// Remove lots that have been gone (not in s.Lots() anymore).
		live := make(map[core.ObjectID]struct{})
		for _, l := range s.Lots() {
			live[l.ID] = struct{}{}
		}
		for k := range inputs {
			if _, ok := live[k]; !ok {
				delete(inputs, k)
			}
		}
		var deltas decay.StepDeltas
		s, deltas = decay.Step(s, inputs, 1, rules, r)
		allDigests.WriteString(digestStep(s.Lots(), deltas))
		allDigests.WriteByte('\n')
	}
	h := sha256.Sum256([]byte(allDigests.String()))
	return hex.EncodeToString(h[:])
}

func TestDeterminismGolden(t *testing.T) {
	const seed = 99
	const steps = 20

	d1 := runDecayScenario(t, seed, steps)
	d2 := runDecayScenario(t, seed, steps)
	if d1 != d2 {
		t.Errorf("two runs with same seed produced different digests:\n  run1: %s\n  run2: %s", d1, d2)
	}
	t.Logf("golden digest (seed=%d, steps=%d): %s", seed, steps, d1)
}

// ── AC-10: Resume invariant ──────────────────────────────────────────────────

func TestResumeInvariant(t *testing.T) {
	accel := mustNum(t, "temperature * 0.01 + moisture * 0.2")
	kind := decay.KindID("herb")
	rules := decay.NewRules(map[decay.KindID]decay.KindRule{
		kind: makeKind(0.4, accel, []decay.StateRule{
			{Threshold: 0},
			{Threshold: 3.0, Transform: []decay.TransformRule{{Item: "dried_herb", Qty: 1}}},
			{Threshold: 8.0}, // terminal
		}),
	})

	lots := []decay.Lot{
		freshLot("h1", kind, 2),
		freshLot("h2", kind, 1),
	}
	envH := decay.EnvInput{Temperature: 20, Moisture: 0.5, StorageRateMult: 1.0}
	makeInputs := func(s *decay.State) map[core.ObjectID]decay.EnvInput {
		m := make(map[core.ObjectID]decay.EnvInput)
		for _, l := range s.Lots() {
			m[l.ID] = envH
		}
		return m
	}

	const T = 10
	const K = 8
	const seed = 7

	// Uninterrupted run 0 → T+K.
	rA := rng.New(seed)
	sA := decay.New(lots)
	var transA []decay.StepDeltas
	for i := 0; i < T+K; i++ {
		var d decay.StepDeltas
		sA, d = decay.Step(sA, makeInputs(sA), 1, rules, rA)
		transA = append(transA, d)
	}

	// Run 0 → T, capture state + RNG, then continue.
	rB := rng.New(seed)
	sB := decay.New(lots)
	for i := 0; i < T; i++ {
		sB, _ = decay.Step(sB, makeInputs(sB), 1, rules, rB)
	}
	savedRNG := rB.State()
	savedLots := sB.Lots()

	// Resume from saved state + RNG.
	rB.Restore(savedRNG)
	sResume := decay.New(savedLots)
	for i := 0; i < K; i++ {
		var dR decay.StepDeltas
		sResume, dR = decay.Step(sResume, makeInputs(sResume), 1, rules, rB)
		dA := transA[T+i]

		// Compare deltas at each step T+i.
		if len(dR.Aged) != len(dA.Aged) {
			t.Errorf("resume step T+%d: Aged count mismatch %d vs %d", i, len(dR.Aged), len(dA.Aged))
			continue
		}
		for j := range dR.Aged {
			if dR.Aged[j] != dA.Aged[j] {
				t.Errorf("resume step T+%d Aged[%d] mismatch: %+v vs %+v", i, j, dR.Aged[j], dA.Aged[j])
			}
		}
	}

	// Compare final state via digest.
	dA := digestStep(sA.Lots(), decay.StepDeltas{})
	dR := digestStep(sResume.Lots(), decay.StepDeltas{})
	if dA != dR {
		t.Errorf("resume final state differs:\n  uninterrupted: %s\n  resumed:       %s", dA, dR)
	}
}

// ── AC-11: Missing EnvInput panics ───────────────────────────────────────────

func TestMissingEnvInputPanics(t *testing.T) {
	kind := decay.KindID("cheese")
	rules := decay.NewRules(map[decay.KindID]decay.KindRule{
		kind: makeKind(1.0, constAccel(t, 1.0), []decay.StateRule{
			{Threshold: 0},
			{Threshold: 999},
		}),
	})
	lot := freshLot("X", kind, 1)
	s := decay.New([]decay.Lot{lot})

	// Pass an empty inputs map (missing entry for "X").
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for missing EnvInput, got none")
		}
	}()
	decay.Step(s, map[core.ObjectID]decay.EnvInput{}, 1, rules, testRNG())
}

// ── AC-12: Decay-off neutrality ──────────────────────────────────────────────

func TestDecayOffNeutrality(t *testing.T) {
	// Empty Rules → no transitions, no transforms, DecayAge unchanged.
	emptyRules := decay.NewRules(nil)

	lot1 := lotWithAge("P", "any_kind", 2, 5.0)
	lot2 := lotWithAge("Q", "other_kind", 1, 3.0)
	in := decay.EnvInput{Temperature: 30, Moisture: 1.0, StorageRateMult: 1.0}
	inputs := envMany("P", in, "Q", in)

	s := decay.New([]decay.Lot{lot1, lot2})
	const steps = 5
	for i := 0; i < steps; i++ {
		var deltas decay.StepDeltas
		s, deltas = decay.Step(s, inputs, 1, emptyRules, testRNG())
		if len(deltas.Transitioned) != 0 {
			t.Errorf("step %d: expected 0 Transitioned with empty Rules, got %d", i, len(deltas.Transitioned))
		}
		if len(deltas.Transformed) != 0 {
			t.Errorf("step %d: expected 0 Transformed with empty Rules, got %d", i, len(deltas.Transformed))
		}
		if len(deltas.Gone) != 0 {
			t.Errorf("step %d: expected 0 Gone with empty Rules, got %d", i, len(deltas.Gone))
		}
	}

	// DecayAge unchanged after all steps.
	finalLots := s.Lots()
	if len(finalLots) != 2 {
		t.Fatalf("expected 2 lots, got %d", len(finalLots))
	}
	// Sort by ID for stable access (build a map).
	byID := make(map[core.ObjectID]decay.Lot)
	for _, l := range finalLots {
		byID[l.ID] = l
	}
	if byID["P"].DecayAge != 5.0 {
		t.Errorf("P DecayAge: got %.4f, want 5.0 (unchanged)", byID["P"].DecayAge)
	}
	if byID["Q"].DecayAge != 3.0 {
		t.Errorf("Q DecayAge: got %.4f, want 3.0 (unchanged)", byID["Q"].DecayAge)
	}
}

// ── AC-13: No forbidden imports (go/parser guard) ────────────────────────────

func TestNoForbiddenImports(t *testing.T) {
	implFiles := []string{"decay.go", "rules.go", "step.go"}

	// Forbidden IMPORT paths — checked against parsed import specs (NOT raw source text),
	// so a comment mentioning a package name does NOT false-positive (go/parser approach
	// mirrors climate/transition_test.go).
	forbiddenImports := map[string]string{
		"time":         "wall-clock import",
		"math/rand":    "global rand import (v1)",
		"math/rand/v2": "global rand import (v2, should be injected)",
		"github.com/dogring/bdg/engine/env/climate":  "forbidden cross-import (climate)",
		"github.com/dogring/bdg/engine/env/flora":    "forbidden cross-import (flora)",
		"github.com/dogring/bdg/engine/world":        "forbidden cross-import (world)",
		"github.com/dogring/bdg/engine/space/navmap": "forbidden cross-import (navmap)",
		"github.com/dogring/bdg/engine/agent":        "forbidden cross-import (agent)",
		"github.com/dogring/bdg/engine/mind/gates":   "forbidden cross-import (gates)",
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

// ── AC-14: No hardcoded constants/IDs (D10 grep guard) ───────────────────────

func TestNoHardcodedConstants(t *testing.T) {
	implFiles := []string{"decay.go", "rules.go", "step.go"}

	// Kind-name and item-name string literals must NOT appear in implementation code (D10).
	// All rates, thresholds, and IDs flow from Rules (injected by platform/config), never literals.
	// We check the exact quoted-string forms that would appear in Go source.
	forbiddenLiterals := []string{
		// Rate values that should live in content not code.
		`"0.5"`, `"0.1"`, `"0.3"`,
		// Specific kind/item IDs that should only appear in content/objects.yaml.
		`"berries"`, `"raw_meat"`, `"bread"`, `"grain"`, `"fish"`,
		`"fresh"`, `"stale"`, `"rotten"`, `"gone"`,
		`"seeds"`, `"compost"`, `"flour"`, `"dried_herb"`,
		// accel operand names hardcoded as rates (the names themselves are fine as string
		// constants in the context adapter, but numeric rate literals must not appear).
		// NOTE: we do NOT forbid "temperature" or "moisture" as those are correct operand
		// name strings in the expr.Context adapter (decayContext). We forbid rate-like
		// numeric patterns instead.
	}

	for _, f := range implFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", f, err)
		}
		src := string(data)
		for _, lit := range forbiddenLiterals {
			if strings.Contains(src, lit) {
				t.Errorf("file %s contains hardcoded literal %q (D10 violation)", f, lit)
			}
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func abs64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
