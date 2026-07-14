package climate_test

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

	"github.com/dogring/bdg/engine/env/climate"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/kernel/rng"
)

// noStats satisfies expr.StatSet with an empty set (climate rules use no stats).
type noStats struct{}

func (noStats) Has(core.StatID) bool { return false }

// mustParseBool compiles a §6 boolean formula for climate rule conditions.
func mustParseBool(t *testing.T, text string) *expr.Program {
	t.Helper()
	prog, err := expr.Parse(text, expr.KindBool, noStats{}, nil)
	if err != nil {
		t.Fatalf("expr.Parse(%q): %v", text, err)
	}
	return prog
}

// ── AC: Transition fires on °C/moisture threshold ─────────────────────────────

func TestTransitionFiresOnThreshold(t *testing.T) {
	cfg := testCfg()
	cfg.RainProbPerHour = 0.0
	// Set temperature to a fixed positive value using AnnualMid with no amplitude.
	cfg.AnnualAmp = 0.0
	cfg.AnnualMid = 20.0 // T = 20°C always (no daily delta, no rain drop with p=0)
	cfg.TempDayPeak = 0.0
	cfg.TempNightLow = 0.0
	cfg.InitMoisture = 0.9 // above the threshold τ=0.8

	rules := climate.NewRules([]climate.TransitionRule{
		{
			From: "forest",
			When: mustParseBool(t, "moisture > 0.8"),
			To:   "swamp",
		},
	})

	r := rng.New(1)
	s := climate.New(cfg, fixedTerrainAt("forest"))

	// First Step: moisture is 0.9 > 0.8 → transition fires for all cells.
	s, trans := climate.Step(s, climate.Forcing{AbsHour: 0, HourOfDay: 12}, rules, r)
	if len(trans) != 4 {
		t.Errorf("expected 4 transitions (2x2 grid, all moisture>0.8), got %d", len(trans))
	}
	for _, tr := range trans {
		if tr.From != "forest" || tr.To != "swamp" {
			t.Errorf("transition %v→%v, want forest→swamp", tr.From, tr.To)
		}
	}
	// After transition terrain is swamp.
	for _, gcs := range s.Cells() {
		if gcs.State.Terrain != "swamp" {
			t.Errorf("cell %v terrain=%v after transition, want swamp", gcs.Cell, gcs.State.Terrain)
		}
	}

	// Second Step: terrain is now swamp, no swamp rule → no further transitions.
	s2, trans2 := climate.Step(s, climate.Forcing{AbsHour: 1, HourOfDay: 12}, rules, r)
	if len(trans2) != 0 {
		t.Errorf("second step: expected 0 transitions (no swamp rule), got %d", len(trans2))
	}
	_ = s2

	// Temperature-conditioned rule: temperature < 0 → frozen.
	cfg2 := testCfg()
	cfg2.RainProbPerHour = 0.0
	cfg2.AnnualAmp = 0.0
	cfg2.AnnualMid = -5.0 // T = -5°C (sub-zero, CA3)
	cfg2.TempDayPeak = 0.0
	cfg2.TempNightLow = 0.0

	rules2 := climate.NewRules([]climate.TransitionRule{
		{
			From: "grassland",
			When: mustParseBool(t, "temperature < 0"),
			To:   "frozen",
		},
	})
	r2 := rng.New(2)
	s3 := climate.New(cfg2, fixedTerrainAt("grassland"))
	_, trans3 := climate.Step(s3, climate.Forcing{AbsHour: 0, HourOfDay: 12}, rules2, r2)
	if len(trans3) != 4 {
		t.Errorf("temperature<0 rule: expected 4 transitions, got %d", len(trans3))
	}
	for _, tr := range trans3 {
		if tr.From != "grassland" || tr.To != "frozen" {
			t.Errorf("got %v→%v, want grassland→frozen", tr.From, tr.To)
		}
	}

	// Below threshold (T > 0, moisture < 0.8): no transition.
	cfg3 := testCfg()
	cfg3.RainProbPerHour = 0.0
	cfg3.AnnualAmp = 0.0
	cfg3.AnnualMid = 15.0 // T = 15°C
	cfg3.TempDayPeak = 0.0
	cfg3.TempNightLow = 0.0
	cfg3.InitMoisture = 0.5 // < 0.8

	r3 := rng.New(3)
	s4 := climate.New(cfg3, fixedTerrainAt("forest"))
	_, trans4 := climate.Step(s4, climate.Forcing{AbsHour: 0, HourOfDay: 12}, rules, r3)
	if len(trans4) != 0 {
		t.Errorf("below threshold: expected 0 transitions, got %d", len(trans4))
	}
}

// ── AC: First matching rule wins, sorted-cell order ──────────────────────────

func TestFirstMatchWinsSortedOrder(t *testing.T) {
	cfg := testCfg()
	cfg.RainProbPerHour = 0.0
	cfg.AnnualAmp = 0.0
	cfg.AnnualMid = 20.0
	cfg.TempDayPeak = 0.0
	cfg.TempNightLow = 0.0
	cfg.InitMoisture = 0.9

	// Two rules for "meadow": first fires when moisture > 0.8 (→ "bog"),
	// second fires when moisture > 0.5 (→ "marsh"). First should win.
	rules := climate.NewRules([]climate.TransitionRule{
		{From: "meadow", When: mustParseBool(t, "moisture > 0.8"), To: "bog"},
		{From: "meadow", When: mustParseBool(t, "moisture > 0.5"), To: "marsh"},
	})

	r := rng.New(1)
	s := climate.New(cfg, fixedTerrainAt("meadow"))
	_, trans := climate.Step(s, climate.Forcing{AbsHour: 0, HourOfDay: 12}, rules, r)

	for _, tr := range trans {
		if tr.To != "bog" {
			t.Errorf("first rule should win: got To=%v, want bog", tr.To)
		}
	}
	if len(trans) != 4 {
		t.Errorf("expected 4 transitions, got %d", len(trans))
	}

	// Verify sorted GridCell order: (0,0),(1,0),(0,1),(1,1) — Y-major then X.
	expected := []climate.GridCell{{0, 0}, {1, 0}, {0, 1}, {1, 1}}
	for i, tr := range trans {
		if tr.Cell != expected[i] {
			t.Errorf("transition[%d]: cell=%v, want %v", i, tr.Cell, expected[i])
		}
	}
}

// ── AC: Determinism golden ────────────────────────────────────────────────────

func digestStep(cells []climate.GridCellState, rain climate.RainProcess, wind climate.Wind, trans []climate.Transition) string {
	var sb strings.Builder
	for _, gcs := range cells {
		fmt.Fprintf(&sb, "G{%d,%d}M%.15f T%.15f R:%s F:%s\n",
			gcs.Cell.X, gcs.Cell.Y, gcs.State.Moisture, gcs.State.Temperature,
			gcs.State.Terrain, gcs.State.FrozenFrom)
	}
	fmt.Fprintf(&sb, "Rain{%v %d %.15f %d}\n", rain.Raining, rain.RainEndsAtHour, rain.PRain, rain.HoursSinceRain)
	fmt.Fprintf(&sb, "Wind{%.15f %.15f}\n", wind.Dir, wind.Mag)
	for _, tr := range trans {
		fmt.Fprintf(&sb, "T{%d,%d %s->%s}\n", tr.Cell.X, tr.Cell.Y, tr.From, tr.To)
	}
	h := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(h[:])
}

func runDigest(cfg climate.Config, rules *climate.Rules, seed int64, n int) string {
	r := rng.New(seed)
	s := climate.New(cfg, fixedTerrainAt("plains"))
	var allDigests strings.Builder
	for i := 0; i < n; i++ {
		var trans []climate.Transition
		s, trans = climate.Step(s, forcing(int64(i)), rules, r)
		allDigests.WriteString(digestStep(s.Cells(), s.Rain(), s.Wind(), trans))
		allDigests.WriteByte('\n')
	}
	h := sha256.Sum256([]byte(allDigests.String()))
	return hex.EncodeToString(h[:])
}

func TestDeterminismGolden(t *testing.T) {
	cfg := testCfg()
	cfg.RainProbPerHour = 0.005
	cfg.InitMoisture = 0.6

	rules := climate.NewRules([]climate.TransitionRule{
		{From: "plains", When: mustParseBool(t, "moisture > 0.7"), To: "wetland"},
		{From: "wetland", When: mustParseBool(t, "moisture < 0.3"), To: "plains"},
	})

	const seed = 42
	const steps = 30

	d1 := runDigest(cfg, rules, seed, steps)
	d2 := runDigest(cfg, rules, seed, steps)
	if d1 != d2 {
		t.Errorf("two runs with same seed produced different digests:\n  run1: %s\n  run2: %s", d1, d2)
	}
	t.Logf("golden digest (seed=%d, steps=%d): %s", seed, steps, d1)
}

// ── AC: Resume invariant ─────────────────────────────────────────────────────

func TestResumeInvariant(t *testing.T) {
	cfg := testCfg()
	cfg.RainProbPerHour = 0.01

	rules := climate.NewRules([]climate.TransitionRule{
		{From: "plains", When: mustParseBool(t, "moisture > 0.6"), To: "wetland"},
	})

	const seed = 99
	const T = 15 // snapshot point
	const K = 10 // steps after snapshot

	// Uninterrupted run: 0 → T+K
	rA := rng.New(seed)
	sA := climate.New(cfg, fixedTerrainAt("plains"))
	var transA [][]climate.Transition
	for i := 0; i < T+K; i++ {
		var tr []climate.Transition
		sA, tr = climate.Step(sA, forcing(int64(i)), rules, rA)
		transA = append(transA, tr)
	}

	// Run to T, capture state + RNG, then continue.
	rB := rng.New(seed)
	sB := climate.New(cfg, fixedTerrainAt("plains"))
	for i := 0; i < T; i++ {
		sB, _ = climate.Step(sB, forcing(int64(i)), rules, rB)
	}
	savedRNG := rB.State()
	savedState := sB

	// Resume from saved state + RNG.
	rB.Restore(savedRNG)
	sResume := savedState
	for i := 0; i < K; i++ {
		var trR []climate.Transition
		sResume, trR = climate.Step(sResume, forcing(int64(T+i)), rules, rB)

		// Compare transitions.
		trA := transA[T+i]
		if len(trR) != len(trA) {
			t.Errorf("resume step T+%d: transitions count mismatch: %d vs %d", i, len(trR), len(trA))
			continue
		}
		for j := range trR {
			if trR[j] != trA[j] {
				t.Errorf("resume step T+%d transition[%d]: %+v vs %+v", i, j, trR[j], trA[j])
			}
		}
	}

	// Compare final state via digest.
	dA := digestStep(sA.Cells(), sA.Rain(), sA.Wind(), nil)
	dR := digestStep(sResume.Cells(), sResume.Rain(), sResume.Wind(), nil)
	if dA != dR {
		t.Errorf("resume final state differs:\n  uninterrupted: %s\n  resumed:       %s", dA, dR)
	}
}

// ── AC: No wall-clock / no global rand / no forbidden imports ─────────────────

func TestNoForbiddenPatterns(t *testing.T) {
	implFiles := []string{"climate.go", "step.go", "rules.go"}

	// Forbidden IMPORT paths — checked against the parsed import specs (NOT raw source
	// text), so a comment mentioning a package name (e.g. "does NOT import navmap") does
	// not false-positive. This is the precise meaning of the "no forbidden import" AC.
	forbiddenImports := map[string]string{
		"time":         "wall-clock import",
		"math/rand":    "global rand import (v1)",
		"math/rand/v2": "global rand import (v2, should be injected)",
		"github.com/dogring/bdg/engine/space/navmap":     "forbidden cross-import (navmap)",
		"github.com/dogring/bdg/engine/kernel/worldtime": "forbidden cross-import (worldtime)",
		"github.com/dogring/bdg/engine/mind/gates":       "forbidden cross-import (gates)",
	}
	// NOTE: a usage substring scan (time.Now / rand.Float64 / rand.Intn) is intentionally
	// omitted — those calls are UNCALLABLE without importing time/math/rand, which the
	// import check above rejects, so the import check subsumes a usage scan (and a raw-text
	// scan would false-positive on the SPEC-explaining comments in climate.go).
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

// ── AC: No hardcoded terrain IDs or domain constants in implementation ────────

func TestNoHardcodedTerrainIDs(t *testing.T) {
	implFiles := []string{"climate.go", "step.go", "rules.go"}

	// Terrain-name string literals must NOT appear in implementation code (D10 guard).
	// All terrain IDs flow from Config.terrainAt and Rules.TransitionRule, never literals.
	forbiddenIDs := []string{
		`"forest"`, `"swamp"`, `"grassland"`, `"desert"`,
		`"wetland"`, `"bog"`, `"marsh"`, `"plains"`,
		`"frozen"`, `"meadow"`, `"alpha"`, `"beta"`,
	}

	for _, f := range implFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", f, err)
		}
		src := string(data)
		for _, id := range forbiddenIDs {
			if strings.Contains(src, id) {
				t.Errorf("file %s contains hardcoded terrain ID %q (D10 violation)", f, id)
			}
		}
	}
}

// Prevent unused import of math in this file.
var _ = math.Pi
