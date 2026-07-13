package flora_test

// AC test coverage:
//  AC-1: Both axes integrate with suitability, independently
//  AC-2: Stage derived from Length (never width)
//  AC-3: Suitability = §6 from expr.Parse fixture (not hardcoded)
//  AC-4: Propagation = seed dispersal, immature-nothing, density/suitability weighting
//  AC-5: Death hysteresis — streak increments, span → Died, ≥θ resets
//  AC-6: Shade radius/opacity from Width §6; unknown id ok=false
//  AC-7: Yield scales with Length + seeded + Dexterity + immature-nothing
//  AC-8: Ownership seam inert (Owner field has no P1 effect)
//  AC-9: Flora-off neutrality (empty Rules)
// AC-10: Sorted-order determinism (D12)
// AC-11: Determinism golden (two runs, same seed → byte-identical digest)
// AC-12: Resume invariant (snapshot at T, resume → same as uninterrupted)
// AC-13: Missing SiteInput panics
// AC-14: No forbidden imports (go/parser)
// AC-15: No hardcoded species/item-name constants in implementation

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

	"github.com/dogring/bdg/engine/env/flora"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/kernel/rng"
)

// ── Test helpers ──────────────────────────────────────────────────────────────

// testStats satisfies expr.StatSet, recognising only Dexterity (used in yield formulas).
type testStats struct{}

func (testStats) Has(id core.StatID) bool { return id == "Dexterity" }

// noStats satisfies expr.StatSet with an empty set (suitability / rate formulas use no stats).
type noStats struct{}

func (noStats) Has(core.StatID) bool { return false }

// mustNum compiles a §6 numeric formula using the supplied StatSet.
func mustNum(t *testing.T, text string, ss expr.StatSet) *expr.Program {
	t.Helper()
	prog, err := expr.Parse(text, expr.KindNum, ss, nil)
	if err != nil {
		t.Fatalf("expr.Parse(%q): %v", text, err)
	}
	return prog
}

// constNum compiles a non-negative numeric constant as a §6 Program.
// Uses fmt.Sprintf to avoid any hardcoded literal in test logic.
func constNum(t *testing.T, v float64) *expr.Program {
	t.Helper()
	return mustNum(t, fmt.Sprintf("%.15g", v), noStats{})
}

// makeSpecies builds a SpeciesRule with the given parameters, compiling formulas via expr.Parse.
// All formula text is passed in (test fixture); no formula is hardcoded in flora logic.
//
// suitFormula references moisture and temperature (lowercase attrs, noStats).
// chanceFormula references Dexterity (stat, testStats).
func makeSpecies(t *testing.T,
	suitFormula string,
	lrFormula string, // LengthRate
	wrFormula string, // WidthRate
	shadeRFormula string, // ShadeRadius = §6(width)
	shadeOFormula string, // ShadeOpacity = §6(width)
	stages []float64,
	yieldStage, propStage int,
	propR float64, propC float64,
	deathThresh float64, deathHys int,
	itemID core.Tag, chanceFormula string, qtyMin, qtyMax int,
) flora.SpeciesRule {
	t.Helper()
	return flora.SpeciesRule{
		Suitability:     mustNum(t, suitFormula, noStats{}),
		LengthRate:      mustNum(t, lrFormula, noStats{}),
		WidthRate:       mustNum(t, wrFormula, noStats{}),
		ShadeRadius:     mustNum(t, shadeRFormula, noStats{}),
		ShadeOpacity:    mustNum(t, shadeOFormula, noStats{}),
		Stages:          stages,
		YieldStage:      yieldStage,
		PropagateStage:  propStage,
		PropRadius:      constNum(t, propR),
		PropChance:      constNum(t, propC),
		DeathThreshold:  deathThresh,
		DeathHysteresis: deathHys,
		Yields: []flora.YieldRule{{
			Item:   itemID,
			Chance: mustNum(t, chanceFormula, testStats{}),
			QtyMin: qtyMin,
			QtyMax: qtyMax,
		}},
	}
}

// plant builds a Plant for tests.
func plant(id, sp string, pos core.Vec2, length, width float64) flora.Plant {
	return flora.Plant{
		ID:      core.ObjectID(id),
		Species: flora.SpeciesID(sp),
		Pos:     pos,
		Length:  length,
		Width:   width,
	}
}

// site builds a SiteInput for tests.
func site(moisture, temperature float64) flora.SiteInput {
	return flora.SiteInput{
		Terrain:      "grassland",
		TerrainAttrs: map[core.Tag]float64{"slope": 0.1},
		Moisture:     moisture,
		Temperature:  temperature,
	}
}

// inputs1 builds a one-plant inputs map.
func inputs1(id string, s flora.SiteInput) map[core.ObjectID]flora.SiteInput {
	return map[core.ObjectID]flora.SiteInput{core.ObjectID(id): s}
}

// seqAlloc returns a deterministic idAlloc that mints IDs as "cN" (N=1,2,3,…).
// The counter is a local variable; a fresh call to seqAlloc gives a fresh counter from 1.
func seqAlloc(prefix string) func() core.ObjectID {
	n := 0
	return func() core.ObjectID {
		n++
		return core.ObjectID(fmt.Sprintf("%s%d", prefix, n))
	}
}

// nopAlloc panics if called — used when no propagation is expected.
func nopAlloc() func() core.ObjectID {
	return func() core.ObjectID { panic("idAlloc called unexpectedly") }
}

// ── AC-1: Both axes integrate with suitability, independently ─────────────────

func TestBothAxesIntegrateIndependently(t *testing.T) {
	// Tall-narrow species: LengthRate=0.4, WidthRate=0.1, suitability=moisture
	// Flat-wide species:   LengthRate=0.1, WidthRate=0.4, suitability=moisture
	const sp1, sp2 = "tall_sp", "flat_sp"

	rules := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{
		sp1: makeSpecies(t,
			"moisture", "0.4", "0.1",
			"width", "width * 0.3",
			[]float64{1.0, 3.0}, 2, 99,
			1.0, 0.0, 0.1, 10, "item_a", "Dexterity", 1, 3),
		sp2: makeSpecies(t,
			"moisture", "0.1", "0.4",
			"width", "width * 0.3",
			[]float64{1.0, 3.0}, 2, 99,
			1.0, 0.0, 0.1, 10, "item_a", "Dexterity", 1, 3),
	})

	highSite := site(1.0, 0.0) // moisture=1.0 → suitability=1.0
	lowSite := site(0.2, 0.0)  // moisture=0.2 → suitability=0.2
	highIn := map[core.ObjectID]flora.SiteInput{"h": highSite}
	lowIn := map[core.ObjectID]flora.SiteInput{"l": lowSite}

	const steps = 5
	r := rng.New(1)

	// High-suitability plant (tall species)
	sh := flora.New([]flora.Plant{plant("h", sp1, core.Vec2{}, 0, 0)})
	for i := 0; i < steps; i++ {
		sh, _ = flora.Step(sh, highIn, rules, nopAlloc(), r)
	}
	// Low-suitability plant (tall species)
	sl := flora.New([]flora.Plant{plant("l", sp1, core.Vec2{}, 0, 0)})
	for i := 0; i < steps; i++ {
		sl, _ = flora.Step(sl, lowIn, rules, nopAlloc(), r)
	}
	ph := sh.Plants()[0]
	pl := sl.Plants()[0]
	if ph.Length <= pl.Length {
		t.Errorf("high-suit Length=%.4f should exceed low-suit Length=%.4f", ph.Length, pl.Length)
	}
	if ph.Width <= pl.Width {
		t.Errorf("high-suit Width=%.4f should exceed low-suit Width=%.4f", ph.Width, pl.Width)
	}

	// Tall-narrow: after steps, Length should exceed Width (rate ratio 4:1)
	rtall := flora.New([]flora.Plant{plant("h", sp1, core.Vec2{}, 0, 0)})
	r2 := rng.New(2)
	for i := 0; i < steps; i++ {
		rtall, _ = flora.Step(rtall, highIn, rules, nopAlloc(), r2)
	}
	ptall := rtall.Plants()[0]
	if ptall.Length <= ptall.Width {
		t.Errorf("tall-narrow: expected Length(%.4f) > Width(%.4f) after %d steps", ptall.Length, ptall.Width, steps)
	}

	// Flat-wide: after steps, Width should exceed Length (rate ratio 1:4)
	rflat := flora.New([]flora.Plant{plant("l", sp2, core.Vec2{}, 0, 0)})
	r3 := rng.New(3)
	for i := 0; i < steps; i++ {
		rflat, _ = flora.Step(rflat, lowIn, rules, nopAlloc(), r3)
	}
	pflat := rflat.Plants()[0]
	if pflat.Width <= pflat.Length {
		t.Errorf("flat-wide: expected Width(%.4f) > Length(%.4f) after %d steps", pflat.Width, pflat.Length, steps)
	}

	// Both ≥ 0 (clamp test: zero suitability keeps values at zero, does not go negative)
	zeroSite := site(0.0, 0.0)
	zeroIn := map[core.ObjectID]flora.SiteInput{"z": zeroSite}
	sz := flora.New([]flora.Plant{plant("z", sp1, core.Vec2{}, 0, 0)})
	r4 := rng.New(4)
	for i := 0; i < steps; i++ {
		sz, _ = flora.Step(sz, zeroIn, rules, nopAlloc(), r4)
	}
	pz := sz.Plants()[0]
	if pz.Length < 0 || pz.Width < 0 {
		t.Errorf("zero-suitability: Length=%.4f Width=%.4f should both be ≥ 0", pz.Length, pz.Width)
	}

	// GrowthDelta carries absolute new values (not increments)
	sr := flora.New([]flora.Plant{plant("h", sp1, core.Vec2{}, 0, 0)})
	r5 := rng.New(5)
	_, d := flora.Step(sr, highIn, rules, nopAlloc(), r5)
	if len(d.Grown) == 0 {
		t.Fatal("expected at least one GrowthDelta")
	}
	gd := d.Grown[0]
	// After 1 step at suitability=1.0: Length=0.4, Width=0.1
	want := struct{ l, w float64 }{0.4, 0.1}
	if math.Abs(gd.Length-want.l) > 1e-12 || math.Abs(gd.Width-want.w) > 1e-12 {
		t.Errorf("GrowthDelta: got L=%.15f W=%.15f, want L=%.15f W=%.15f", gd.Length, gd.Width, want.l, want.w)
	}
}

// ── AC-2: Stage derived from Length (never width) ─────────────────────────────

func TestStageDerivedFromLength(t *testing.T) {
	const sp = "stage_sp"
	stages := []float64{1.0, 3.0, 7.0}
	rules := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{
		sp: makeSpecies(t,
			"moisture", "0", "0",
			"width", "width * 0.1",
			stages, 3, 99, 0.0, 0.0, 0.1, 10, "item_a", "Dexterity", 1, 2),
	})

	table := []struct {
		length    float64
		wantStage int
	}{
		{0.0, 0}, {0.9, 0},
		{1.0, 1}, {2.9, 1},
		{3.0, 2}, {6.9, 2},
		{7.0, 3}, {100.0, 3},
	}
	for _, tc := range table {
		got := rules.Stage(sp, tc.length)
		if got != tc.wantStage {
			t.Errorf("Stage(length=%.1f) = %d, want %d", tc.length, got, tc.wantStage)
		}
	}

	// Two plants with equal Length but different Width share the same Stage (D9)
	s1 := rules.Stage(sp, 3.5)
	s2 := rules.Stage(sp, 3.5) // width is not a parameter of Stage
	if s1 != s2 || s1 != 2 {
		t.Errorf("Stage ignores width: s1=%d s2=%d, both want 2", s1, s2)
	}
}

// ── AC-3: Suitability = §6 from expr.Parse fixture ───────────────────────────

func TestSuitabilityUsesExprFormula(t *testing.T) {
	// Formula references moisture and temperature (both lowercase attrs, noStats).
	// Two sites differing only in Moisture should yield formula-predicted values.
	const sp = "suit_sp"
	// f(moisture, temperature) = moisture * 0.5 + temperature * 0.02
	suitFormula := "moisture * 0.5 + temperature * 0.02"
	rules := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{
		sp: makeSpecies(t,
			suitFormula, "0", "0",
			"0", "0",
			nil, 0, 99, 0.0, 0.0, 0.1, 10, "item_a", "Dexterity", 1, 1),
	})

	type tc struct {
		moisture, temperature float64
		wantSuit              float64
	}
	tests := []tc{
		{0.8, 20.0, math.Min(1.0, 0.8*0.5+20.0*0.02)}, // 0.4+0.4 = 0.8
		{0.4, 20.0, math.Min(1.0, 0.4*0.5+20.0*0.02)}, // 0.2+0.4 = 0.6
		{0.0, 0.0, 0.0},   // 0+0 = 0
		{1.0, 25.0, 1.0},  // 0.5+0.5=1.0, clamped at 1
		{0.0, 100.0, 1.0}, // 0+2.0=2.0, clamped at 1
	}
	for _, tc := range tests {
		in := site(tc.moisture, tc.temperature)
		got := rules.Suitability(sp, in)
		if math.Abs(got-tc.wantSuit) > 1e-12 {
			t.Errorf("Suitability(m=%.2f t=%.2f)=%.15f, want %.15f",
				tc.moisture, tc.temperature, got, tc.wantSuit)
		}
	}

	// Verify the formula is NOT hardcoded: change formula → different result
	const sp2 = "suit_sp2"
	rules2 := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{
		sp2: makeSpecies(t,
			"moisture * 0.9", "0", "0",
			"0", "0",
			nil, 0, 99, 0.0, 0.0, 0.1, 10, "item_a", "Dexterity", 1, 1),
	})
	in := site(0.8, 20.0)
	got1 := rules.Suitability(sp, in)   // 0.8
	got2 := rules2.Suitability(sp2, in) // 0.72
	if got1 == got2 {
		t.Errorf("different formulas should produce different suitability: both gave %.4f", got1)
	}
}

// ── AC-4: Propagation = seed dispersal near parent ───────────────────────────

func TestPropagationSeedDispersal(t *testing.T) {
	const sp = "prop_sp"
	const propRadius = 10.0
	// PropagateStage=1 (stages=[0.5] → stage 1 at length≥0.5)
	// PropChance=1.0 (always propagates at full suitability/density)
	rules := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{
		sp: makeSpecies(t,
			"moisture", "0", "0",
			"0", "0",
			[]float64{0.5}, 0, 1, // yieldStage=0, propStage=1
			propRadius, 1.0, // propRadius, propChance
			0.1, 10, "item_a", "Dexterity", 1, 1),
	})

	parentPos := core.Vec2{X: 50, Y: 50}
	// Mature plant: length=1.0 → stage=1 >= PropagateStage=1
	mature := plant("p1", sp, parentPos, 1.0, 0.0)
	// Immature plant: length=0.0 → stage=0 < PropagateStage=1
	immature := plant("p0", sp, parentPos, 0.0, 0.0)

	inMature := site(1.0, 0.0) // suitability=1.0 → densityWeight=1/(1+0)=1 → spawnProb=1.0
	inputs := map[core.ObjectID]flora.SiteInput{
		"p1": inMature,
		"p0": inMature,
	}

	const seed = int64(42)

	// ── Immature alone: no spawn ──
	sImm := flora.New([]flora.Plant{immature})
	rImm := rng.New(seed)
	_, d := flora.Step(sImm, inputs1("p0", inMature), rules, nopAlloc(), rImm)
	if len(d.Spawned) != 0 {
		t.Errorf("immature parent: expected 0 spawned, got %d", len(d.Spawned))
	}

	// ── Mature alone: spawns within PropRadius ──
	sMat := flora.New([]flora.Plant{mature})
	rMat := rng.New(seed)
	_, dMat := flora.Step(sMat, inputs1("p1", inMature), rules, seqAlloc("c"), rMat)
	if len(dMat.Spawned) != 1 {
		t.Fatalf("mature parent (propChance=1,suit=1,n=0): expected 1 spawn, got %d", len(dMat.Spawned))
	}
	child := dMat.Spawned[0]
	distFromParent := child.Pos.Distance(parentPos)
	if distFromParent > propRadius+1e-9 {
		t.Errorf("child pos dist=%.4f exceeds PropRadius=%.4f", distFromParent, propRadius)
	}
	// Child starts at zero Length/Width, empty Owner
	if child.Length != 0 || child.Width != 0 {
		t.Errorf("child: want Length=0 Width=0, got L=%.4f W=%.4f", child.Length, child.Width)
	}
	if child.Owner != "" {
		t.Errorf("child: want empty Owner, got %q", child.Owner)
	}
	if child.DeathStreak != 0 {
		t.Errorf("child: want DeathStreak=0, got %d", child.DeathStreak)
	}

	// ── Fixed seed reproduces exact child set ──
	sMat2 := flora.New([]flora.Plant{mature})
	rMat2 := rng.New(seed)
	_, dMat2 := flora.Step(sMat2, inputs1("p1", inMature), rules, seqAlloc("c"), rMat2)
	if len(dMat2.Spawned) != 1 || dMat2.Spawned[0].Pos != child.Pos {
		t.Errorf("second run (same seed): Pos mismatch: got %+v, want %+v", dMat2.Spawned[0].Pos, child.Pos)
	}

	// ── Immature alongside mature: mature draws same RNG as if immature absent ──
	sBoth := flora.New([]flora.Plant{immature, mature})
	rBoth := rng.New(seed)
	_, dBoth := flora.Step(sBoth, inputs, rules, seqAlloc("c"), rBoth)
	if len(dBoth.Spawned) != 1 {
		t.Fatalf("mature+immature: expected 1 spawn (immature skips RNG), got %d", len(dBoth.Spawned))
	}
	if dBoth.Spawned[0].Pos != child.Pos {
		t.Errorf("immature alongside mature: child Pos differs (immature consumed RNG draws): got %+v want %+v",
			dBoth.Spawned[0].Pos, child.Pos)
	}

	// ── High NeighborCount reduces spawn probability ──
	// With NeighborCount=0: spawnProb = 1.0*1.0*1.0 = 1.0 → always spawns (Float64<1.0 always)
	// With NeighborCount=large: spawnProb → 0 → rarely spawns
	// Test: NeighborCount=0 gives guaranteed spawn (propChance=1.0, suit=1.0, dw=1.0)
	inN0 := flora.SiteInput{Terrain: "g", TerrainAttrs: nil, Moisture: 1.0, NeighborCount: 0}
	sN0 := flora.New([]flora.Plant{mature})
	rN0 := rng.New(99)
	_, dN0 := flora.Step(sN0, inputs1("p1", inN0), rules, seqAlloc("c"), rN0)
	if len(dN0.Spawned) != 1 {
		t.Error("NeighborCount=0: expected guaranteed spawn (propChance=1, suit=1, dw=1)")
	}

	// NeighborCount=0 vs NeighborCount=9: over many seeds, N=0 spawns more
	spawnsN0, spawnsN9 := 0, 0
	inN9 := flora.SiteInput{Terrain: "g", TerrainAttrs: nil, Moisture: 1.0, NeighborCount: 9}
	for seed2 := int64(0); seed2 < 50; seed2++ {
		r0 := rng.New(seed2)
		s0 := flora.New([]flora.Plant{mature})
		_, d0 := flora.Step(s0, inputs1("p1", inN0), rules, seqAlloc("c"), r0)
		spawnsN0 += len(d0.Spawned)
		r9 := rng.New(seed2)
		s9 := flora.New([]flora.Plant{mature})
		_, d9 := flora.Step(s9, inputs1("p1", inN9), rules, seqAlloc("c"), r9)
		spawnsN9 += len(d9.Spawned)
	}
	if spawnsN0 <= spawnsN9 {
		t.Errorf("NeighborCount=0 should spawn more than NeighborCount=9: N0=%d N9=%d", spawnsN0, spawnsN9)
	}
}

// ── AC: Carrying-capacity density weight (1k) ────────────────────────────────

// TestPropagationCarryingCapacity verifies the K>0 logistic density weight max(0,1−n/K):
// a parent at NeighborCount≥K spawns nothing, the rate is linear below K, and
// K never changes the RNG draw count (so child positions are reproducible for spawns that fire).
func TestPropagationCarryingCapacity(t *testing.T) {
	const sp = "cap_sp"
	const propRadius = 10.0
	const K = 4

	// Base species: mature (stage 1 at length≥0.5), propChance=1, suitability=1.
	base := makeSpecies(t,
		"moisture", "0", "0",
		"0", "0",
		[]float64{0.5}, 0, 1, // yieldStage=0, propStage=1
		propRadius, 1.0,
		0.1, 10, "item_a", "Dexterity", 1, 1)
	base.CarryingCapacity = constNum(t, float64(K)) // §6 constant program; terrain-independent here
	rules := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{sp: base})

	mature := plant("p1", sp, core.Vec2{X: 50, Y: 50}, 1.0, 0.0)
	siteAt := func(n int) flora.SiteInput {
		return flora.SiteInput{Terrain: "g", Moisture: 1.0, NeighborCount: n}
	}

	// n = K: weight = max(0, 1 − K/K) = 0 → spawnProb 0 → never spawns, regardless of seed.
	for seed := int64(0); seed < 30; seed++ {
		s := flora.New([]flora.Plant{mature})
		_, d := flora.Step(s, inputs1("p1", siteAt(K)), rules, seqAlloc("c"), rng.New(seed))
		if len(d.Spawned) != 0 {
			t.Fatalf("n=K=%d: weight must be 0 (no spawn), got %d spawns at seed %d", K, len(d.Spawned), seed)
		}
	}

	// n > K: still clamped to 0 (max(0, …)), never negative-weight weirdness.
	for seed := int64(0); seed < 30; seed++ {
		s := flora.New([]flora.Plant{mature})
		_, d := flora.Step(s, inputs1("p1", siteAt(K+5)), rules, seqAlloc("c"), rng.New(seed))
		if len(d.Spawned) != 0 {
			t.Fatalf("n>K: weight clamped to 0, got %d spawns at seed %d", len(d.Spawned), seed)
		}
	}

	// n = 0: weight = 1 → guaranteed spawn (propChance·suit·1 = 1).
	sFull := flora.New([]flora.Plant{mature})
	_, dFull := flora.Step(sFull, inputs1("p1", siteAt(0)), rules, seqAlloc("c"), rng.New(7))
	if len(dFull.Spawned) != 1 {
		t.Fatalf("n=0: weight=1 → guaranteed spawn, got %d", len(dFull.Spawned))
	}

	// Table-driven frequency check over every point n=0..K. With chance=suitability=1,
	// the expected spawn frequency is exactly the density weight 1-n/K.
	const samples = 1000
	for n := 0; n <= K; n++ {
		n := n
		t.Run(fmt.Sprintf("linear_n_%d", n), func(t *testing.T) {
			spawned := 0
			for seed := int64(0); seed < samples; seed++ {
				s := flora.New([]flora.Plant{mature})
				_, d := flora.Step(s, inputs1("p1", siteAt(n)), rules, seqAlloc("c"), rng.New(seed))
				spawned += len(d.Spawned)
			}
			got := float64(spawned) / samples
			want := 1 - float64(n)/K
			if math.Abs(got-want) > 0.06 {
				t.Fatalf("n=%d: spawn frequency=%g, want approximately linear weight %g", n, got, want)
			}
		})
	}

	// K does NOT shift the RNG draw sequence: a spawn that fires lands at the same position
	// whether K is set or not (only the spawn-test threshold differs). Compare K=4 vs legacy
	// (nil = absent) at n=0, where BOTH weights = 1 → both spawn → identical child position.
	legacy := base
	legacy.CarryingCapacity = nil
	rulesLegacy := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{sp: legacy})
	sK := flora.New([]flora.Plant{mature})
	_, dK := flora.Step(sK, inputs1("p1", siteAt(0)), rules, seqAlloc("c"), rng.New(123))
	sL := flora.New([]flora.Plant{mature})
	_, dL := flora.Step(sL, inputs1("p1", siteAt(0)), rulesLegacy, seqAlloc("c"), rng.New(123))
	if len(dK.Spawned) != 1 || len(dL.Spawned) != 1 || dK.Spawned[0].Pos != dL.Spawned[0].Pos {
		t.Errorf("K must not shift RNG draws: K child=%+v legacy child=%+v", dK.Spawned, dL.Spawned)
	}

	// K=0 preserves the complete legacy 1/(1+n) decision and child position for n>0.
	// Reproduce the documented three draws independently for representative neighbor counts.
	for _, n := range []int{1, 3, 9} {
		for seed := int64(0); seed < 100; seed++ {
			wantRNG := rng.New(seed)
			angle := wantRNG.Float64() * 2 * math.Pi
			dist := wantRNG.Float64() * propRadius
			wantSpawn := wantRNG.Float64() < 1/float64(1+n)
			wantPos := core.Vec2{X: mature.Pos.X + dist*math.Cos(angle), Y: mature.Pos.Y + dist*math.Sin(angle)}

			s := flora.New([]flora.Plant{mature})
			_, got := flora.Step(s, inputs1("p1", siteAt(n)), rulesLegacy, seqAlloc("c"), rng.New(seed))
			if (len(got.Spawned) == 1) != wantSpawn {
				t.Fatalf("legacy n=%d seed=%d: spawned=%v, want %v", n, seed, len(got.Spawned) == 1, wantSpawn)
			}
			if wantSpawn && got.Spawned[0].Pos != wantPos {
				t.Fatalf("legacy n=%d seed=%d: child pos=%+v, want %+v", n, seed, got.Spawned[0].Pos, wantPos)
			}
		}
	}
}

// TestCarryingCapacityTerrainDependent verifies K = §6(terrain attrs): the SAME plant reaches a
// DIFFERENT equilibrium density on different terrain, so density is terrain-controlled (1k, option B).
// carrying_capacity = "10 * (1 - depth)": dry land (depth=0) → K=10 (dense), shallow water
// (depth=0.5) → K=5, deep water (depth=1) → K=0 (no establishment).
func TestCarryingCapacityTerrainDependent(t *testing.T) {
	const sp = "terr_sp"
	base := makeSpecies(t,
		"1.0", "0", "0", // suitability=1 (survives everywhere) so ONLY K differs by terrain
		"0", "0",
		[]float64{0.5}, 0, 1,
		10.0, 1.0, // propRadius, propChance=1
		0.0, 0, "item_a", "Dexterity", 1, 1)
	base.CarryingCapacity = mustNum(t, "10 * (1 - depth)", noStats{}) // §6 over the `depth` terrain attr
	rules := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{sp: base})
	mature := plant("p1", sp, core.Vec2{X: 50, Y: 50}, 1.0, 0.0)

	siteDepth := func(depth float64, n int) flora.SiteInput {
		return flora.SiteInput{Terrain: "g", Moisture: 1.0, TerrainAttrs: map[core.Tag]float64{"depth": depth}, NeighborCount: n}
	}

	// Deep water (depth=1 → K=0): never spawns at any neighbor count, any seed.
	for seed := int64(0); seed < 40; seed++ {
		s := flora.New([]flora.Plant{mature})
		_, d := flora.Step(s, inputs1("p1", siteDepth(1.0, 0)), rules, seqAlloc("c"), rng.New(seed))
		if len(d.Spawned) != 0 {
			t.Fatalf("depth=1 → K=0: must not establish, spawned %d at seed %d", len(d.Spawned), seed)
		}
	}

	// Same neighbor count n=5: dry land (K=10 → weight 0.5) spawns MORE than shallow water
	// (K=5 → weight 0), proving terrain sets the equilibrium density.
	dry, wet := 0, 0
	for seed := int64(0); seed < 300; seed++ {
		sd := flora.New([]flora.Plant{mature})
		_, dd := flora.Step(sd, inputs1("p1", siteDepth(0.0, 5)), rules, seqAlloc("c"), rng.New(seed))
		dry += len(dd.Spawned)
		sw := flora.New([]flora.Plant{mature})
		_, dw := flora.Step(sw, inputs1("p1", siteDepth(0.5, 5)), rules, seqAlloc("c"), rng.New(seed))
		wet += len(dw.Spawned)
	}
	if !(dry > wet) {
		t.Errorf("terrain-dependent K: dry land (K=10) must out-spawn shallow water (K=5) at equal n: dry=%d wet=%d", dry, wet)
	}
	if wet != 0 {
		t.Errorf("depth=0.5 → K=5, n=5 → weight 0: expected no spawns, got %d", wet)
	}
}

// ── AC-5: Death hysteresis ────────────────────────────────────────────────────

func TestDeathHysteresis(t *testing.T) {
	const sp = "death_sp"
	const deathThresh = 0.5
	const deathHys = 3
	rules := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{
		sp: makeSpecies(t,
			"moisture", "0", "0",
			"0", "0",
			nil, 0, 99, 0.0, 0.0,
			deathThresh, deathHys, "item_a", "Dexterity", 1, 1),
	})

	// Low-suitability site: moisture=0.1 < 0.5=θ → streak increments
	badSite := site(0.1, 0.0)
	// Good-suitability site: moisture=0.9 ≥ 0.5=θ → streak resets
	goodSite := site(0.9, 0.0)

	p := plant("p1", sp, core.Vec2{}, 2.0, 0.5)
	s := flora.New([]flora.Plant{p})
	r := rng.New(0)

	// Step 1: bad → streak=1, NOT in Died
	badIn := inputs1("p1", badSite)
	s, d1 := flora.Step(s, badIn, rules, nopAlloc(), r)
	if len(d1.Died) != 0 {
		t.Errorf("step1 bad: want 0 Died, got %d", len(d1.Died))
	}
	if s.Plants()[0].DeathStreak != 1 {
		t.Errorf("step1 bad: want DeathStreak=1, got %d", s.Plants()[0].DeathStreak)
	}

	// Step 2: bad → streak=2, NOT in Died
	s, d2 := flora.Step(s, badIn, rules, nopAlloc(), r)
	if len(d2.Died) != 0 {
		t.Errorf("step2 bad: want 0 Died, got %d", len(d2.Died))
	}
	if s.Plants()[0].DeathStreak != 2 {
		t.Errorf("step2 bad: want DeathStreak=2, got %d", s.Plants()[0].DeathStreak)
	}

	// Step 3: bad → streak=3 >= DeathHysteresis(3) → Died
	s, d3 := flora.Step(s, badIn, rules, nopAlloc(), r)
	if len(d3.Died) != 1 || d3.Died[0] != "p1" {
		t.Errorf("step3 bad: want Died=[p1], got %v", d3.Died)
	}
	if len(s.Plants()) != 0 {
		t.Errorf("after death: want 0 plants, got %d", len(s.Plants()))
	}

	// Reset: verify suitability≥θ resets DeathStreak to 0 (no flicker)
	p2 := plant("p2", sp, core.Vec2{}, 2.0, 0.5)
	s2 := flora.New([]flora.Plant{p2})
	r2 := rng.New(0)
	badIn2 := inputs1("p2", badSite)
	goodIn2 := inputs1("p2", goodSite)

	// 2 bad steps → streak=2
	s2, _ = flora.Step(s2, badIn2, rules, nopAlloc(), r2)
	s2, _ = flora.Step(s2, badIn2, rules, nopAlloc(), r2)
	if s2.Plants()[0].DeathStreak != 2 {
		t.Errorf("reset test: want streak=2 before good step, got %d", s2.Plants()[0].DeathStreak)
	}

	// 1 good step → streak resets to 0 (no flicker)
	s2, dGood := flora.Step(s2, goodIn2, rules, nopAlloc(), r2)
	if len(dGood.Died) != 0 {
		t.Errorf("good step: unexpected death after reset")
	}
	if s2.Plants()[0].DeathStreak != 0 {
		t.Errorf("good step: want DeathStreak=0 after reset, got %d", s2.Plants()[0].DeathStreak)
	}

	// 1 more bad step → streak=1 (fresh start, hysteresis holds)
	s2, dAfter := flora.Step(s2, badIn2, rules, nopAlloc(), r2)
	if len(dAfter.Died) != 0 {
		t.Errorf("post-reset bad step: unexpected death (streak should be 1, hysteresis=3)")
	}
	if s2.Plants()[0].DeathStreak != 1 {
		t.Errorf("post-reset bad step: want streak=1, got %d", s2.Plants()[0].DeathStreak)
	}
}

// ── AC-6: Shade from Width §6; unknown id ok=false ────────────────────────────

func TestShadeFromWidthExpr(t *testing.T) {
	const sp = "shade_sp"
	// ShadeRadius = width * 2.0 ; ShadeOpacity = width * 0.4
	rules := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{
		sp: makeSpecies(t,
			"moisture", "0.1", "0.1",
			"width * 2", "width * 0.4",
			nil, 0, 99, 0.0, 0.0, 0.1, 10, "item_a", "Dexterity", 1, 1),
	})

	// Build a state with rules via Step (so rules is carried into next state)
	p := plant("p1", sp, core.Vec2{X: 3, Y: 7}, 0, 1.5)
	s := flora.New([]flora.Plant{p})
	r := rng.New(0)
	s, _ = flora.Step(s, inputs1("p1", site(0.9, 0.0)), rules, nopAlloc(), r)

	// After step with moisture=0.9, suitability=0.9, LengthRate=0.1, WidthRate=0.1:
	// Width = 0 + 0.1*0.9 = 0.09 ... wait, initial Width=1.5, so Width = 1.5 + 0.1*0.9 = 1.59
	// Actually the plant has initial Width=1.5, so ShadeOf should use current Width.

	sh, ok := s.ShadeOf("p1")
	if !ok {
		t.Fatal("ShadeOf(p1): expected ok=true")
	}
	if sh.ID != "p1" {
		t.Errorf("Shade.ID=%q, want p1", sh.ID)
	}
	currentWidth := s.Plants()[0].Width
	wantRadius := currentWidth * 2.0
	wantOpacity := currentWidth * 0.4
	if math.Abs(sh.Radius-wantRadius) > 1e-12 {
		t.Errorf("ShadeOf Radius=%.6f, want %.6f (width*2)", sh.Radius, wantRadius)
	}
	if math.Abs(sh.Opacity-wantOpacity) > 1e-12 {
		t.Errorf("ShadeOf Opacity=%.6f, want %.6f (width*0.4)", sh.Opacity, wantOpacity)
	}

	// Wider plant → larger/denser shade
	pWide := plant("pw", sp, core.Vec2{}, 0, 5.0)
	sWide := flora.New([]flora.Plant{pWide})
	r2 := rng.New(0)
	sWide, _ = flora.Step(sWide, inputs1("pw", site(0.9, 0.0)), rules, nopAlloc(), r2)
	shWide, _ := sWide.ShadeOf("pw")
	if shWide.Radius <= sh.Radius {
		t.Errorf("wider plant: Radius=%.4f should exceed narrower Radius=%.4f", shWide.Radius, sh.Radius)
	}

	// Two plants with equal Width but different Length cast identical shade
	pA := plant("pa", sp, core.Vec2{X: 1, Y: 1}, 1.0, 2.0)
	pB := plant("pb", sp, core.Vec2{X: 2, Y: 2}, 5.0, 2.0)
	sAB := flora.New([]flora.Plant{pA, pB})
	r3 := rng.New(0)
	insAB := map[core.ObjectID]flora.SiteInput{"pa": site(0.9, 0.0), "pb": site(0.9, 0.0)}
	sAB, _ = flora.Step(sAB, insAB, rules, nopAlloc(), r3)
	// After step: Width_a = 2.0 + 0.1*0.9 = 2.09, Width_b = 2.0 + 0.1*0.9 = 2.09
	shA, _ := sAB.ShadeOf("pa")
	shB, _ := sAB.ShadeOf("pb")
	if math.Abs(shA.Radius-shB.Radius) > 1e-12 || math.Abs(shA.Opacity-shB.Opacity) > 1e-12 {
		t.Errorf("equal-Width plants: shade differs (A R=%.6f O=%.6f; B R=%.6f O=%.6f)",
			shA.Radius, shA.Opacity, shB.Radius, shB.Opacity)
	}

	// Unknown id → ok=false
	_, okUnk := s.ShadeOf("UNKNOWN")
	if okUnk {
		t.Error("ShadeOf(unknown): expected ok=false")
	}

	// Opacity clamping: wide plant with high-coefficient formula → capped at 1.0
	rulesHighO := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{
		sp: makeSpecies(t,
			"moisture", "0", "0",
			"width * 2", "width * 3",
			nil, 0, 99, 0.0, 0.0, 0.1, 10, "item_a", "Dexterity", 1, 1),
	})
	pHigh := plant("ph", sp, core.Vec2{}, 0, 5.0)
	sHigh := flora.New([]flora.Plant{pHigh})
	r4 := rng.New(0)
	sHigh, _ = flora.Step(sHigh, inputs1("ph", site(0.9, 0.0)), rules, nopAlloc(), r4)
	// Use rulesHighO for ShadeOf: rebuild state from rules with high opacity
	sHighO := flora.New([]flora.Plant{pHigh})
	r5 := rng.New(0)
	sHighO, _ = flora.Step(sHighO, inputs1("ph", site(0.9, 0.0)), rulesHighO, nopAlloc(), r5)
	shHigh, _ := sHighO.ShadeOf("ph")
	if shHigh.Opacity > 1.0 {
		t.Errorf("ShadeOf Opacity=%.4f exceeds 1.0 (should be clamped)", shHigh.Opacity)
	}
}

// ── AC-7: Yield scales with Length + seeded + Dexterity + immature=nil ────────

func TestYieldScalesWithLength(t *testing.T) {
	const sp = "yield_sp"
	// Stages=[1.0] → stage 0 below 1.0, stage 1 at ≥1.0
	// YieldStage=1 → only stage≥1 yields
	// Chance="Dexterity" (100% at dex=1.0, 0% at dex=0.0)
	rules := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{
		sp: makeSpecies(t,
			"moisture", "0", "0",
			"0", "0",
			[]float64{1.0}, 1, 99, 0.0, 0.0, 0.1, 10, "item_harvest", "Dexterity", 1, 3),
	})

	// Immature (length=0.5 → stage=0 < YieldStage=1) → nil
	r := rng.New(42)
	items := rules.Yield(sp, 0.5, 1.0, r)
	if items != nil {
		t.Errorf("immature: want nil, got %v", items)
	}

	// Zero dexterity → chance=0 → never yields (regardless of maturity)
	r = rng.New(42)
	items = rules.Yield(sp, 2.0, 0.0, r)
	if items != nil {
		t.Errorf("dex=0: want nil, got %v", items)
	}

	// Dexterity=1.0 → chance=1.0 → always yields at stage≥1
	r = rng.New(42)
	items = rules.Yield(sp, 2.0, 1.0, r)
	if len(items) == 0 {
		t.Fatal("dex=1 at mature: want items, got nil/empty")
	}
	if items[0].Item != "item_harvest" {
		t.Errorf("Yield.Item=%q, want item_harvest", items[0].Item)
	}
	if items[0].Qty < 1 {
		t.Errorf("Yield.Qty=%d < QtyMin=1", items[0].Qty)
	}

	// Fixed seed reproduces exact items
	r1 := rng.New(99)
	items1 := rules.Yield(sp, 2.0, 1.0, r1)
	r2 := rng.New(99)
	items2 := rules.Yield(sp, 2.0, 1.0, r2)
	if len(items1) != len(items2) || (len(items1) > 0 && items1[0].Qty != items2[0].Qty) {
		t.Errorf("same seed: items1=%v items2=%v differ", items1, items2)
	}

	// Qty scales with length: taller plant yields more on average over many seeds
	// effectiveMax(length=1.0) = QtyMax * (1+floor(1.0)) = 3*2 = 6
	// effectiveMax(length=5.0) = QtyMax * (1+floor(5.0)) = 3*6 = 18
	// E[qty at length=1] = (1+6)/2 = 3.5; E[qty at length=5] = (1+18)/2 = 9.5
	totalShort, totalTall := 0, 0
	for seed := int64(0); seed < 100; seed++ {
		rShort := rng.New(seed)
		its := rules.Yield(sp, 1.0, 1.0, rShort)
		for _, it := range its {
			totalShort += it.Qty
		}
		rTall := rng.New(seed)
		its = rules.Yield(sp, 5.0, 1.0, rTall)
		for _, it := range its {
			totalTall += it.Qty
		}
	}
	if totalTall <= totalShort {
		t.Errorf("taller plant should yield more over 100 seeds: short=%d tall=%d", totalShort, totalTall)
	}

	// Higher dexterity yields more/more-often over many seeds (chance = Dexterity, no clamping issue)
	totalLowDex, totalHighDex := 0, 0
	for seed := int64(0); seed < 100; seed++ {
		rL := rng.New(seed)
		its := rules.Yield(sp, 2.0, 0.3, rL)
		for _, it := range its {
			totalLowDex += it.Qty
		}
		rH := rng.New(seed)
		its = rules.Yield(sp, 2.0, 0.9, rH)
		for _, it := range its {
			totalHighDex += it.Qty
		}
	}
	if totalHighDex <= totalLowDex {
		t.Errorf("higher dexterity should yield more over 100 seeds: low=%d high=%d", totalLowDex, totalHighDex)
	}
}

// ── AC-8: Ownership seam inert ────────────────────────────────────────────────

func TestOwnershipSeamInert(t *testing.T) {
	const sp = "own_sp"
	rules := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{
		sp: makeSpecies(t,
			"moisture", "0.2", "0.1",
			"width * 0.5", "width * 0.2",
			[]float64{0.5}, 0, 99, 0.0, 0.0, 0.1, 10, "item_a", "Dexterity", 1, 2),
	})

	in := site(0.8, 15.0)
	pWild := flora.Plant{ID: "p1", Species: sp, Pos: core.Vec2{X: 1, Y: 1}, Length: 0.2, Width: 0.1}
	pOwned := flora.Plant{ID: "p1", Species: sp, Pos: core.Vec2{X: 1, Y: 1}, Length: 0.2, Width: 0.1,
		Owner: core.AgentID("agentA")}

	sWild := flora.New([]flora.Plant{pWild})
	sOwned := flora.New([]flora.Plant{pOwned})
	inp := inputs1("p1", in)

	rWild := rng.New(7)
	rOwned := rng.New(7)
	sWild, dWild := flora.Step(sWild, inp, rules, nopAlloc(), rWild)
	sOwned, dOwned := flora.Step(sOwned, inp, rules, nopAlloc(), rOwned)

	if len(dWild.Died) != len(dOwned.Died) ||
		len(dWild.Grown) != len(dOwned.Grown) ||
		len(dWild.Spawned) != len(dOwned.Spawned) {
		t.Errorf("owner vs wild: delta count mismatch wild=%+v owned=%+v", dWild, dOwned)
	}
	if len(dWild.Grown) > 0 {
		gW, gO := dWild.Grown[0], dOwned.Grown[0]
		if math.Abs(gW.Length-gO.Length) > 1e-12 || math.Abs(gW.Width-gO.Width) > 1e-12 {
			t.Errorf("owner vs wild: GrowthDelta differs: wild=%+v owned=%+v", gW, gO)
		}
	}

	// ShadeOf is identical (RESOLVED 1f: Owner field inert in P1)
	shW, _ := sWild.ShadeOf("p1")
	shO, _ := sOwned.ShadeOf("p1")
	if math.Abs(shW.Radius-shO.Radius) > 1e-12 || math.Abs(shW.Opacity-shO.Opacity) > 1e-12 {
		t.Errorf("ShadeOf: owner vs wild differs: wild=%+v owned=%+v", shW, shO)
	}
}

// ── AC-9: Flora-off neutrality ────────────────────────────────────────────────

func TestFloraOffNeutrality(t *testing.T) {
	const sp = "any_sp"
	emptyRules := flora.NewRules(nil)

	p := plant("p1", sp, core.Vec2{X: 3, Y: 4}, 2.5, 1.2)
	s := flora.New([]flora.Plant{p})
	in := inputs1("p1", site(0.8, 20.0))

	r := rng.New(1)
	for i := 0; i < 5; i++ {
		var d flora.StepDeltas
		s, d = flora.Step(s, in, emptyRules, nopAlloc(), r)
		if len(d.Spawned) != 0 || len(d.Died) != 0 || len(d.Grown) != 0 {
			t.Errorf("step %d flora-off: expected empty deltas, got Spawned=%d Died=%d Grown=%d",
				i, len(d.Spawned), len(d.Died), len(d.Grown))
		}
	}

	// Length/Width/DeathStreak unchanged
	plants := s.Plants()
	if len(plants) != 1 {
		t.Fatalf("flora-off: expected 1 plant, got %d", len(plants))
	}
	if plants[0].Length != p.Length || plants[0].Width != p.Width || plants[0].DeathStreak != 0 {
		t.Errorf("flora-off: morphology changed: got L=%.4f W=%.4f DS=%d",
			plants[0].Length, plants[0].Width, plants[0].DeathStreak)
	}

	// ShadeOf returns zero-radius Shade (perception LoS unaffected)
	sh, ok := s.ShadeOf("p1")
	if !ok {
		t.Fatal("ShadeOf flora-off: expected ok=true")
	}
	if sh.Radius != 0 || sh.Opacity != 0 {
		t.Errorf("flora-off ShadeOf: want Radius=0 Opacity=0, got Radius=%.4f Opacity=%.4f", sh.Radius, sh.Opacity)
	}

	// nil rules: same behavior
	sNil := flora.New([]flora.Plant{p})
	rNil := rng.New(1)
	for i := 0; i < 3; i++ {
		var dNil flora.StepDeltas
		sNil, dNil = flora.Step(sNil, in, nil, nopAlloc(), rNil)
		if len(dNil.Spawned)+len(dNil.Died)+len(dNil.Grown) != 0 {
			t.Errorf("nil rules step %d: expected empty deltas", i)
		}
	}
	shNil, okNil := sNil.ShadeOf("p1")
	if !okNil || shNil.Radius != 0 || shNil.Opacity != 0 {
		t.Errorf("nil rules ShadeOf: want zero shade, got ok=%v R=%.4f O=%.4f", okNil, shNil.Radius, shNil.Opacity)
	}
}

// ── AC-10: Sorted-order determinism ──────────────────────────────────────────

func TestSortedOrderDeterminism(t *testing.T) {
	const sp = "sort_sp"
	rules := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{
		sp: makeSpecies(t,
			"moisture", "0.2", "0.1",
			"width", "width * 0.2",
			[]float64{0.5}, 0, 1, 5.0, 0.9, // propStage=1, propR=5, propC=0.9
			0.4, 3, "item_a", "Dexterity", 1, 2),
	})

	// Three plants in non-sorted insertion order: p3, p1, p2
	plants := []flora.Plant{
		plant("p3", sp, core.Vec2{X: 30, Y: 30}, 1.0, 0.5),
		plant("p1", sp, core.Vec2{X: 10, Y: 10}, 1.0, 0.5),
		plant("p2", sp, core.Vec2{X: 20, Y: 20}, 0.0, 0.0), // immature
	}
	in := map[core.ObjectID]flora.SiteInput{
		"p3": site(0.9, 0.0),
		"p1": site(0.9, 0.0),
		"p2": site(0.9, 0.0),
	}

	r := rng.New(42)
	s := flora.New(plants)
	s, d := flora.Step(s, in, rules, seqAlloc("c"), r)

	// Plants() is sorted by ID
	ps := s.Plants()
	for i := 1; i < len(ps); i++ {
		if ps[i].ID <= ps[i-1].ID {
			t.Errorf("Plants(): not sorted at index %d: %q >= %q", i, ps[i-1].ID, ps[i].ID)
		}
	}

	// Grown is sorted by ID
	for i := 1; i < len(d.Grown); i++ {
		if d.Grown[i].ID <= d.Grown[i-1].ID {
			t.Errorf("Grown: not sorted at index %d: %q >= %q", i, d.Grown[i-1].ID, d.Grown[i].ID)
		}
	}

	// Spawned is sorted by ID
	for i := 1; i < len(d.Spawned); i++ {
		if d.Spawned[i].ID <= d.Spawned[i-1].ID {
			t.Errorf("Spawned: not sorted at index %d: %q >= %q", i, d.Spawned[i-1].ID, d.Spawned[i].ID)
		}
	}

	// Shuffling inputs map insertion order gives byte-identical deltas
	// (in Go, maps are accessed by key — insertion order has no effect on key lookup)
	in2 := map[core.ObjectID]flora.SiteInput{
		"p1": site(0.9, 0.0),
		"p3": site(0.9, 0.0),
		"p2": site(0.9, 0.0),
	}
	r2 := rng.New(42)
	s2 := flora.New(plants)
	s2, d2 := flora.Step(s2, in2, rules, seqAlloc("c"), r2)

	if digestState(s) != digestState(s2) {
		t.Error("shuffled inputs map: Plants() digest differs")
	}
	if digestDeltas(d) != digestDeltas(d2) {
		t.Error("shuffled inputs map: StepDeltas digest differs")
	}
}

// ── AC-11: Determinism golden ─────────────────────────────────────────────────

func TestDeterminismGolden(t *testing.T) {
	// Flora-off golden (RESOLVED 1g: golden established flora-off first).
	// Uses a species with PropagateStage=99 so no propagation occurs (no idAlloc calls),
	// and valid growth rules to exercise the growth integration path.
	const sp = "golden_sp"
	rules := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{
		sp: makeSpecies(t,
			"moisture * 0.9 + temperature * 0.02",
			"0.3", "0.1",
			"width * 2", "width * 0.4",
			[]float64{1.0, 3.0}, 2, 99, // propStage=99: never propagates
			5.0, 0.8, 0.1, 5, "item_golden", "Dexterity", 1, 4),
	})

	plants := []flora.Plant{
		plant("pA", sp, core.Vec2{X: 10, Y: 10}, 0, 0),
		plant("pB", sp, core.Vec2{X: 20, Y: 20}, 1.5, 0.5),
	}
	inMap := map[core.ObjectID]flora.SiteInput{
		"pA": site(0.8, 20.0),
		"pB": site(0.6, 15.0),
	}

	run := func() string {
		r := rng.New(42)
		s := flora.New(plants)
		var sb strings.Builder
		for i := 0; i < 10; i++ {
			var d flora.StepDeltas
			s, d = flora.Step(s, inMap, rules, nopAlloc(), r)
			sb.WriteString(digestState(s))
			sb.WriteString(digestDeltas(d))
			sb.WriteByte('\n')
		}
		h := sha256.Sum256([]byte(sb.String()))
		return hex.EncodeToString(h[:])
	}

	d1 := run()
	d2 := run()
	if d1 != d2 {
		t.Errorf("determinism golden: two runs with same seed differ:\n  run1: %s\n  run2: %s", d1, d2)
	}
	t.Logf("flora golden digest (seed=42, steps=10): %s", d1)
}

// ── AC-12: Resume invariant ───────────────────────────────────────────────────

func TestResumeInvariant(t *testing.T) {
	const sp = "resume_sp"
	rules := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{
		sp: makeSpecies(t,
			"moisture * 0.8 + temperature * 0.01",
			"0.25", "0.12",
			"width", "width * 0.3",
			[]float64{1.0, 3.0}, 2, 99, // PropagateStage=99: no spawn, idAlloc never called
			5.0, 0.0, 0.15, 4, "item_r", "Dexterity", 1, 3),
	})

	plants := []flora.Plant{
		plant("r1", sp, core.Vec2{X: 5, Y: 5}, 0, 0),
		plant("r2", sp, core.Vec2{X: 15, Y: 15}, 0.5, 0.2),
	}
	inMap := map[core.ObjectID]flora.SiteInput{
		"r1": site(0.7, 18.0),
		"r2": site(0.5, 10.0),
	}

	const T = 8
	const K = 5

	// Uninterrupted run: 0 → T+K
	rA := rng.New(13)
	sA := flora.New(plants)
	for i := 0; i < T+K; i++ {
		sA, _ = flora.Step(sA, inMap, rules, nopAlloc(), rA)
	}

	// Run to T, capture state + RNG, then continue from T → T+K
	rB := rng.New(13)
	sB := flora.New(plants)
	for i := 0; i < T; i++ {
		sB, _ = flora.Step(sB, inMap, rules, nopAlloc(), rB)
	}
	savedRNG := rB.State()
	savedState := sB // safe: Step never mutates prev

	rB.Restore(savedRNG)
	sResume := savedState
	for i := 0; i < K; i++ {
		sResume, _ = flora.Step(sResume, inMap, rules, nopAlloc(), rB)
	}

	if digestState(sA) != digestState(sResume) {
		t.Errorf("resume invariant: uninterrupted vs resumed final state differ:\n  unint: %s\n  resumed: %s",
			digestState(sA), digestState(sResume))
	}
}

// ── AC-13: Missing SiteInput panics ──────────────────────────────────────────

func TestMissingSiteInputPanics(t *testing.T) {
	defer func() {
		if rec := recover(); rec == nil {
			t.Error("Step with missing SiteInput: expected panic, got nil")
		}
	}()
	p := plant("p1", "any", core.Vec2{}, 0, 0)
	s := flora.New([]flora.Plant{p})
	r := rng.New(0)
	// inputs is empty — "p1" is missing
	flora.Step(s, map[core.ObjectID]flora.SiteInput{}, nil, nopAlloc(), r)
}

// ── AC-14: No forbidden imports (go/parser) ───────────────────────────────────

func TestNoForbiddenImports(t *testing.T) {
	implFiles := []string{"flora.go", "rules.go", "step.go"}

	// Forbidden import paths — checked against parsed import specs (NOT raw source text),
	// so comments mentioning a package name do NOT false-positive (mirrors climate pattern).
	forbidden := map[string]string{
		"time":         "wall-clock import (D12 violation)",
		"math/rand":    "global rand v1 (D12: must use injected *rng.RNG)",
		"math/rand/v2": "global rand v2 (D12: must use injected *rng.RNG)",
		"github.com/dogring/bdg/engine/space/navmap":    "forbidden navmap import (one-way wiring)",
		"github.com/dogring/bdg/engine/env/climate":     "forbidden climate import (one-way wiring)",
		"github.com/dogring/bdg/engine/world":           "forbidden world import (one-way wiring)",
		"github.com/dogring/bdg/engine/mind/perception": "forbidden perception import (one-way wiring)",
		"github.com/dogring/bdg/engine/mind/gates":      "forbidden gates import (one-way wiring)",
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
				t.Fatalf("unquote import in %s: %v", f, uerr)
			}
			if desc, bad := forbidden[path]; bad {
				t.Errorf("file %s: forbidden import %q (%s)", f, path, desc)
			}
		}
	}
}

// ── AC-15: No hardcoded species/item-name constants (D10 guard) ───────────────

func TestNoHardcodedContentIDs(t *testing.T) {
	implFiles := []string{"flora.go", "rules.go", "step.go"}

	// Species and item-name string literals must NOT appear in implementation code (D10 guard).
	// All species IDs, item IDs, and domain thresholds flow from Rules (injected by platform/config).
	// Note: the attribute names "moisture"/"temperature"/"width"/"length"/"Dexterity" are
	// INFRASTRUCTURE (expr.Context adapter bridge, not domain content) and are expected to appear.
	forbiddenLiterals := []string{
		`"oak"`, `"pine"`, `"grass"`, `"berry_bush"`, `"shrub"`, `"fern"`,
		`"wood"`, `"berry"`, `"fiber"`, `"stone"`, `"log"`,
		`"sp1"`, `"sp2"`, `"test_sp"`, `"tree"`,
	}

	for _, f := range implFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", f, err)
		}
		src := string(data)
		for _, lit := range forbiddenLiterals {
			if strings.Contains(src, lit) {
				t.Errorf("file %s contains hardcoded content ID %q (D10 violation)", f, lit)
			}
		}
	}
}

// ── Digest helpers ────────────────────────────────────────────────────────────

func digestState(s *flora.State) string {
	var sb strings.Builder
	for _, p := range s.Plants() {
		fmt.Fprintf(&sb, "P{%s %s %.15f %.15f %d %s}\n",
			p.ID, p.Species, p.Length, p.Width, p.DeathStreak, p.Owner)
	}
	h := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(h[:])
}

func digestDeltas(d flora.StepDeltas) string {
	var sb strings.Builder
	for _, id := range d.Died {
		fmt.Fprintf(&sb, "D{%s}\n", id)
	}
	for _, g := range d.Grown {
		fmt.Fprintf(&sb, "G{%s %.15f %.15f %d}\n", g.ID, g.Length, g.Width, g.DeathStreak)
	}
	for _, sp := range d.Spawned {
		fmt.Fprintf(&sb, "S{%s %s %.15f %.15f}\n", sp.ID, sp.Species, sp.Pos.X, sp.Pos.Y)
	}
	h := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(h[:])
}
