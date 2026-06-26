package expr_test

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
)

// ── Test helpers ──────────────────────────────────────────────────────────────

// statSet wraps a string set as a StatSet.
type statSet map[core.StatID]struct{}

func (s statSet) Has(id core.StatID) bool { _, ok := s[id]; return ok }

func makeStats(ids ...string) statSet {
	m := make(statSet, len(ids))
	for _, id := range ids {
		m[core.StatID(id)] = struct{}{}
	}
	return m
}

// stub is a simple Context for testing. Attr absence (absent key) → ok=false.
// Pred ignores the arg (sufficient for testing predicate truth-value plumbing).
type stub struct {
	stats map[core.StatID]float64
	attrs map[core.Tag]float64
	preds map[string]bool
}

func (s *stub) Stat(id core.StatID) float64 { return s.stats[id] }
func (s *stub) Attr(name core.Tag) (float64, bool) {
	v, ok := s.attrs[name]
	return v, ok
}
func (s *stub) Pred(name string, _ core.Tag) bool { return s.preds[name] }

func mustParse(t *testing.T, text string, want expr.Kind, ks expr.StatSet, kp []expr.KnownPred) *expr.Program {
	t.Helper()
	p, err := expr.Parse(text, want, ks, kp)
	if err != nil {
		t.Fatalf("Parse(%q): %v", text, err)
	}
	return p
}

// ── AC: Arithmetic → numeric (#5/#7) ─────────────────────────────────────────

func TestArithmetic(t *testing.T) {
	ks := makeStats("Strength", "Agility")

	cases := []struct {
		formula  string
		str, agi float64
		want     float64
	}{
		{"Strength * 0.5", 0.8, 0.0, 0.4},
		{"Strength + Agility", 0.8, 0.6, 1.4},
		{"Strength * 0.5 + Agility * 0.3", 0.8, 0.6, 0.58},    // operator precedence
		{"(Strength + Agility) * 0.5", 0.8, 0.6, 0.70},         // parens override
		{"Strength - Agility", 0.8, 0.6, 0.2},
		{"Strength / 2", 0.8, 0.0, 0.4},
		{"Strength + Agility * 2", 0.5, 0.3, 1.1},              // * before +
		{"Strength - Agility * 0.5", 1.0, 0.6, 0.70},           // * before -
		{"(Strength + Agility) * (Strength - Agility)", 0.8, 0.2, 0.6}, // nested parens
	}
	for _, c := range cases {
		t.Run(c.formula, func(t *testing.T) {
			prog := mustParse(t, c.formula, expr.KindNum, ks, nil)
			ctx := &stub{stats: map[core.StatID]float64{"Strength": c.str, "Agility": c.agi}}
			got := prog.EvalNumber(ctx)
			if math.Abs(got-c.want) > 1e-12 {
				t.Errorf("EvalNumber = %v, want %v", got, c.want)
			}
		})
	}
}

// ── AC: Comparison / logical → boolean (#5/#7) ───────────────────────────────

func TestComparisonLogical(t *testing.T) {
	ks := makeStats("Strength", "Agility")
	kp := expr.BasePreds()

	cases := []struct {
		formula        string
		str, agi, depth float64
		want           bool
	}{
		{"Strength > 0.5", 0.8, 0, 0, true},
		{"Strength > 0.5", 0.4, 0, 0, false},
		{"Strength < Agility", 0.4, 0.6, 0, true},
		{"Strength >= 0.8", 0.8, 0, 0, true},
		{"Strength >= 0.8", 0.79, 0, 0, false},
		{"Strength <= Agility", 0.6, 0.6, 0, true},
		{"Strength == 0.8", 0.8, 0, 0, true},
		{"Strength != 0.8", 0.9, 0, 0, true},
		// logical AND
		{"(Strength > 0.5) & (Agility > 0.3)", 0.8, 0.6, 0, true},
		{"(Strength > 0.9) & (Agility > 0.3)", 0.8, 0.6, 0, false},
		// logical OR
		{"(Strength > 0.9) | (Agility > 0.3)", 0.8, 0.6, 0, true},
		{"(Strength > 0.9) | (Agility > 0.9)", 0.8, 0.6, 0, false},
		// unary !
		{"!(Strength > 0.5)", 0.8, 0, 0, false},
		{"!(Strength > 0.5)", 0.4, 0, 0, true},
		{"!!(Strength > 0.5)", 0.8, 0, 0, true},
		// §6 SPEC example
		{"(Strength * 0.5 + Agility * 0.3 > 0.5) | (Agility > terrain.depth)", 0.8, 0.6, 0.5, true},
		{"(Strength * 0.5 + Agility * 0.3 > 0.5) | (Agility > terrain.depth)", 0.2, 0.1, 0.5, false},
	}
	for _, c := range cases {
		t.Run(c.formula, func(t *testing.T) {
			prog := mustParse(t, c.formula, expr.KindBool, ks, kp)
			ctx := &stub{
				stats: map[core.StatID]float64{"Strength": c.str, "Agility": c.agi},
				attrs: map[core.Tag]float64{"terrain.depth": c.depth},
			}
			if got := prog.EvalBool(ctx); got != c.want {
				t.Errorf("EvalBool = %v, want %v", got, c.want)
			}
		})
	}

	// Short-circuit is side-effect-free: result independent of evaluation order
	// because Context is pure. Verify left-wins and right-wins both give correct result.
	t.Run("ShortCircuitSideEffectFree", func(t *testing.T) {
		prog := mustParse(t, "(Strength > 0.5) | (Agility > 0.5)", expr.KindBool, ks, kp)
		// left=true (short-circuits right)
		if !prog.EvalBool(&stub{stats: map[core.StatID]float64{"Strength": 0.8, "Agility": 0.0}}) {
			t.Error("OR: true | false should be true")
		}
		// left=false, right=true
		if !prog.EvalBool(&stub{stats: map[core.StatID]float64{"Strength": 0.2, "Agility": 0.8}}) {
			t.Error("OR: false | true should be true")
		}
		// both false
		if prog.EvalBool(&stub{stats: map[core.StatID]float64{"Strength": 0.2, "Agility": 0.2}}) {
			t.Error("OR: false | false should be false")
		}
	})
}

// ── AC: Predicate calls via Context.Pred (#3) ─────────────────────────────────

func TestPredicateCalls(t *testing.T) {
	ks := makeStats("Strength")
	kp := expr.BasePreds()

	// §9 portal access formula
	const formula = "has(key) | Strength > door.lockStrength | paid(toll) | isOwner"
	prog := mustParse(t, formula, expr.KindBool, ks, kp)

	cases := []struct {
		name    string
		str     float64
		lock    float64
		has     bool
		paid    bool
		isOwner bool
		want    bool
	}{
		{"stat wins", 1.0, 0.5, false, false, false, true},
		{"stat loses all false", 0.3, 0.5, false, false, false, false},
		{"has flips", 0.3, 0.5, true, false, false, true},
		{"paid flips", 0.3, 0.5, false, true, false, true},
		{"isOwner flips", 0.3, 0.5, false, false, true, true},
		{"all preds true", 1.0, 0.5, true, true, true, true},
		{"all false all lose", 0.3, 0.9, false, false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := &stub{
				stats: map[core.StatID]float64{"Strength": c.str},
				attrs: map[core.Tag]float64{"door.lockStrength": c.lock},
				preds: map[string]bool{"has": c.has, "paid": c.paid, "isOwner": c.isOwner},
			}
			if got := prog.EvalBool(ctx); got != c.want {
				t.Errorf("EvalBool = %v, want %v", got, c.want)
			}
		})
	}

	// Bare arity-0 isOwner is routed through Context.Pred(name, "")
	t.Run("BareIsOwner", func(t *testing.T) {
		p := mustParse(t, "isOwner", expr.KindBool, ks, kp)
		if !p.EvalBool(&stub{preds: map[string]bool{"isOwner": true}}) {
			t.Error("isOwner Pred=true should return true")
		}
		if p.EvalBool(&stub{preds: map[string]bool{"isOwner": false}}) {
			t.Error("isOwner Pred=false should return false")
		}
	})
}

// ── AC: Undefined identifier → load failure (D10) ────────────────────────────

func TestUndefinedIdentifier(t *testing.T) {
	kp := expr.BasePreds()

	cases := []struct {
		name      string
		formula   string
		ks        expr.StatSet
		kp        []expr.KnownPred
		wantInErr string
	}{
		// Uppercase-initial token not in knownStats → undefined stat
		{"uppercase not in stats", "UNDEFINED * 0.5", makeStats(), nil, "UNDEFINED"},
		{"uppercase typo", "Strength + TYPO", makeStats("Strength"), nil, "TYPO"},
		// Lowercase-initial bare → Attr (NOT a load failure; tested implicitly elsewhere)

		// Unknown predicate name (call form)
		{"unknown pred call", "unknownPred(x)", makeStats(), kp, "unknownPred"},
		{"pred missing from empty table", "has(key)", makeStats(), nil, "has"},

		// Wrong arity: arity-0 isOwner called as arity-1
		{"arity-0 called with arg", "isOwner(x)", makeStats(), kp, "isOwner"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Use KindNum as want (will fail before result-kind check)
			prog, err := expr.Parse(c.formula, expr.KindNum, c.ks, c.kp)
			if err == nil {
				t.Fatalf("Parse(%q) succeeded, want error containing %q", c.formula, c.wantInErr)
			}
			if prog != nil {
				t.Error("on error, Parse must return nil Program")
			}
			if !strings.Contains(err.Error(), c.wantInErr) {
				t.Errorf("error %q does not name %q", err.Error(), c.wantInErr)
			}
		})
	}

	// Lowercase-initial bare identifier → Attr (no error) — confirms OQ-A case rule
	t.Run("LowercaseBareIsAttr_NoError", func(t *testing.T) {
		// moisture is lowercase → Attr, so parse succeeds even with empty knownStats
		p, err := expr.Parse("moisture * 0.5", expr.KindNum, makeStats(), nil)
		if err != nil {
			t.Fatalf("lowercase attr should not fail: %v", err)
		}
		if p == nil {
			t.Fatal("expected non-nil Program")
		}
		// Attr is absent in stub → contributes 0 → moisture*0.5 = 0
		if v := p.EvalNumber(&stub{}); v != 0 {
			t.Errorf("EvalNumber = %v, want 0 (absent attr)", v)
		}
	})
}

// ── AC: Type clash → load failure (#5/#7) ────────────────────────────────────

func TestTypeClash(t *testing.T) {
	ks := makeStats("Strength", "Agility")
	kp := expr.BasePreds()

	cases := []struct {
		formula string
		want    expr.Kind
		desc    string
	}{
		{"moisture & 0.5", expr.KindBool, "logical & over numeric operands"},
		{"has(key) + 1", expr.KindNum, "arithmetic over Bool predicate result"},
		{"!Strength", expr.KindBool, "unary ! over numeric Stat"},
		{"has(key) > 0.5", expr.KindBool, "comparison with Bool left operand"},
		{"Strength * 0.5", expr.KindBool, "want=Bool but formula is Num"},
		{"Strength > 0.5", expr.KindNum, "want=Num but formula is Bool"},
		{"Strength & (Agility > 0.5)", expr.KindBool, "& with Num left"},
		{"(Strength > 0.5) + 1", expr.KindNum, "arithmetic over Bool comparison"},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			prog, err := expr.Parse(c.formula, c.want, ks, kp)
			if err == nil {
				t.Fatalf("Parse(%q) succeeded, want type-clash error", c.formula)
			}
			if prog != nil {
				t.Error("on type-clash error, Parse must return nil Program")
			}
		})
	}
}

// ── AC: ResultKind + want assertion (#5) ──────────────────────────────────────

func TestResultKindWant(t *testing.T) {
	ks := makeStats("Strength")

	numProg := mustParse(t, "Strength * 0.5", expr.KindNum, ks, nil)
	if numProg.ResultKind() != expr.KindNum {
		t.Errorf("numeric formula ResultKind = %v, want KindNum", numProg.ResultKind())
	}

	boolProg := mustParse(t, "Strength > 0.5", expr.KindBool, ks, nil)
	if boolProg.ResultKind() != expr.KindBool {
		t.Errorf("bool formula ResultKind = %v, want KindBool", boolProg.ResultKind())
	}

	if _, err := expr.Parse("Strength * 0.5", expr.KindBool, ks, nil); err == nil {
		t.Error("numeric formula with want=KindBool should fail")
	}
	if _, err := expr.Parse("Strength > 0.5", expr.KindNum, ks, nil); err == nil {
		t.Error("bool formula with want=KindNum should fail")
	}
}

// ── AC: Div-0 / NaN policy (#6, D12) ─────────────────────────────────────────

func TestDivZeroNaN(t *testing.T) {
	ks := makeStats("Strength")

	cases := []struct {
		name    string
		formula string
		ctx     *stub
		want    float64
	}{
		{
			"div by zero stat",
			"2 / Strength",
			&stub{stats: map[core.StatID]float64{"Strength": 0}},
			0,
		},
		{
			"div by zero literal",
			"Strength / 0",
			&stub{stats: map[core.StatID]float64{"Strength": 5.0}},
			0,
		},
		{
			"div by missing attr",
			"Strength / missing",
			&stub{stats: map[core.StatID]float64{"Strength": 1.0}},
			0,
		},
		{
			"missing attr contributes zero",
			"Strength + absent",
			&stub{stats: map[core.StatID]float64{"Strength": 0.5}},
			0.5,
		},
		{
			"missing attr in mul",
			"Strength * missing",
			&stub{stats: map[core.StatID]float64{"Strength": 0.7}},
			0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prog := mustParse(t, c.formula, expr.KindNum, ks, nil)
			got := prog.EvalNumber(c.ctx)
			if math.IsNaN(got) || math.IsInf(got, 0) {
				t.Errorf("EvalNumber must be finite, got %v", got)
			}
			if math.Abs(got-c.want) > 1e-12 {
				t.Errorf("EvalNumber = %v, want %v", got, c.want)
			}
		})
	}
}

// ── AC: No domain clamp by expr (#6) ─────────────────────────────────────────

func TestNoDomainClamp(t *testing.T) {
	ks := makeStats("Strength", "Agility")
	prog := mustParse(t, "Strength + Agility", expr.KindNum, ks, nil)
	ctx := &stub{stats: map[core.StatID]float64{"Strength": 1.0, "Agility": 0.8}}
	got := prog.EvalNumber(ctx)
	const want = 1.8
	if math.Abs(got-want) > 1e-12 {
		t.Errorf("EvalNumber = %v, want %v", got, want)
	}
	if got <= 1.0 {
		t.Error("expr must NOT clamp to [0,1]; result 1.8 should be returned unchanged")
	}
}

// ── AC: Reads / ReadsAttrs / ReadsPreds introspection (#2) ───────────────────

func TestIntrospection(t *testing.T) {
	ks := makeStats("Strength", "Agility")
	kp := expr.BasePreds()

	// Mixed formula: 2 stats, 2 attrs, 3 preds (incl. arity-0 isOwner)
	const formula = "has(key) | Strength > door.lockStrength | Agility > terrain.depth | paid(toll) | isOwner"
	prog := mustParse(t, formula, expr.KindBool, ks, kp)

	// Sorted stat IDs
	wantStats := []core.StatID{"Agility", "Strength"}
	gotStats := prog.Reads()
	if len(gotStats) != len(wantStats) {
		t.Fatalf("Reads() = %v, want %v", gotStats, wantStats)
	}
	for i := range wantStats {
		if gotStats[i] != wantStats[i] {
			t.Errorf("Reads()[%d] = %q, want %q", i, gotStats[i], wantStats[i])
		}
	}

	// Sorted attr names
	wantAttrs := []core.Tag{"door.lockStrength", "terrain.depth"}
	gotAttrs := prog.ReadsAttrs()
	if len(gotAttrs) != len(wantAttrs) {
		t.Fatalf("ReadsAttrs() = %v, want %v", gotAttrs, wantAttrs)
	}
	for i := range wantAttrs {
		if gotAttrs[i] != wantAttrs[i] {
			t.Errorf("ReadsAttrs()[%d] = %q, want %q", i, gotAttrs[i], wantAttrs[i])
		}
	}

	// Sorted pred names
	wantPreds := []string{"has", "isOwner", "paid"}
	gotPreds := prog.ReadsPreds()
	if len(gotPreds) != len(wantPreds) {
		t.Fatalf("ReadsPreds() = %v, want %v", gotPreds, wantPreds)
	}
	for i := range wantPreds {
		if gotPreds[i] != wantPreds[i] {
			t.Errorf("ReadsPreds()[%d] = %q, want %q", i, gotPreds[i], wantPreds[i])
		}
	}

	// Identical across repeated calls
	for range 3 {
		if s := prog.Reads(); len(s) != 2 || s[0] != "Agility" {
			t.Error("Reads() unstable across calls")
		}
	}

	// Returns copies; caller cannot corrupt internal cache
	r1 := prog.Reads()
	r1[0] = "MUTATED"
	r2 := prog.Reads()
	for _, id := range r2 {
		if string(id) == "MUTATED" {
			t.Error("Reads() must return a copy, not the internal slice")
		}
	}

	// De-duplication: same stat/attr referenced multiple times → appears once
	const dupFormula = "Strength * 0.5 + Strength * 0.3 + moisture + moisture"
	ks2 := makeStats("Strength")
	dup := mustParse(t, dupFormula, expr.KindNum, ks2, nil)
	if r := dup.Reads(); len(r) != 1 || r[0] != "Strength" {
		t.Errorf("de-dup Reads = %v, want [Strength]", r)
	}
	if a := dup.ReadsAttrs(); len(a) != 1 || a[0] != "moisture" {
		t.Errorf("de-dup ReadsAttrs = %v, want [moisture]", a)
	}

	// Second Parse of same text → identical introspection
	prog2 := mustParse(t, formula, expr.KindBool, ks, kp)
	if r := prog2.Reads(); len(r) != 2 || r[0] != "Agility" || r[1] != "Strength" {
		t.Errorf("second Parse Reads = %v", r)
	}
	if a := prog2.ReadsAttrs(); len(a) != 2 {
		t.Errorf("second Parse ReadsAttrs = %v", a)
	}
}

// ── AC: Determinism golden (D12) ─────────────────────────────────────────────

const goldenPath = "testdata/golden/determinism.json"

type goldenEntry struct {
	Formula string   `json:"formula"`
	Num     *float64 `json:"num,omitempty"`
	Bool    *bool    `json:"bool,omitempty"`
}

func TestGoldenDeterminism(t *testing.T) {
	ks := makeStats("Strength", "Agility")
	kp := expr.BasePreds()

	type tc struct {
		formula string
		want    expr.Kind
		ctx     *stub
	}
	cases := []tc{
		{
			"Strength * 0.5 + Agility * 0.3",
			expr.KindNum,
			&stub{stats: map[core.StatID]float64{"Strength": 0.8, "Agility": 0.6}},
		},
		{
			"(Strength * 0.5 + Agility * 0.3 > 0.5) | (Agility > terrain.depth)",
			expr.KindBool,
			&stub{
				stats: map[core.StatID]float64{"Strength": 0.8, "Agility": 0.6},
				attrs: map[core.Tag]float64{"terrain.depth": 0.5},
			},
		},
		{
			"(Strength * 0.5 + Agility * 0.3 > 0.5) | (Agility > terrain.depth)",
			expr.KindBool,
			&stub{
				stats: map[core.StatID]float64{"Strength": 0.2, "Agility": 0.1},
				attrs: map[core.Tag]float64{"terrain.depth": 0.5},
			},
		},
		{
			"Strength / missing",
			expr.KindNum,
			&stub{stats: map[core.StatID]float64{"Strength": 1.0}},
		},
		{
			"Strength + Agility",
			expr.KindNum,
			&stub{stats: map[core.StatID]float64{"Strength": 1.0, "Agility": 0.8}},
		},
		{
			"has(key) | Strength > door.lockStrength | paid(toll) | isOwner",
			expr.KindBool,
			&stub{
				stats: map[core.StatID]float64{"Strength": 1.0},
				attrs: map[core.Tag]float64{"door.lockStrength": 0.5},
				preds: map[string]bool{"has": false, "paid": false, "isOwner": false},
			},
		},
		{
			"!(Strength > 0.5) & (Agility >= 0.5)",
			expr.KindBool,
			&stub{stats: map[core.StatID]float64{"Strength": 0.3, "Agility": 0.6}},
		},
		{
			"Strength * 2 - Agility",
			expr.KindNum,
			&stub{stats: map[core.StatID]float64{"Strength": 0.5, "Agility": 0.2}},
		},
	}

	var entries []goldenEntry
	for _, c := range cases {
		prog := mustParse(t, c.formula, c.want, ks, kp)
		e := goldenEntry{Formula: c.formula}
		if c.want == expr.KindNum {
			v := prog.EvalNumber(c.ctx)
			e.Num = &v
		} else {
			v := prog.EvalBool(c.ctx)
			e.Bool = &v
		}
		entries = append(entries, e)
	}

	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("marshal golden: %v", err)
	}

	existing, readErr := os.ReadFile(goldenPath)
	if readErr != nil {
		// First run: seed the golden file and pass.
		if err2 := os.MkdirAll("testdata/golden", 0o755); err2 != nil {
			t.Fatalf("mkdir: %v", err2)
		}
		if err2 := os.WriteFile(goldenPath, raw, 0o644); err2 != nil {
			t.Fatalf("write golden: %v", err2)
		}
		t.Logf("seeded golden file %s (pinned for future runs)", goldenPath)
		return
	}

	// Byte-identical comparison — proves same Program+Context → same output every run.
	if string(existing) != string(raw) {
		t.Errorf("golden mismatch.\nGot:\n%s\nWant (on disk):\n%s", raw, existing)
	}

	// Second Parse of same text reproduces identical results (cross-process determinism).
	for i, c := range cases {
		prog2 := mustParse(t, c.formula, c.want, ks, kp)
		if c.want == expr.KindNum {
			v1, v2 := *entries[i].Num, prog2.EvalNumber(c.ctx)
			if v1 != v2 {
				t.Errorf("re-Parse[%d] num: first=%v second=%v", i, v1, v2)
			}
		} else {
			v1, v2 := *entries[i].Bool, prog2.EvalBool(c.ctx)
			if v1 != v2 {
				t.Errorf("re-Parse[%d] bool: first=%v second=%v", i, v1, v2)
			}
		}
	}
}

// ── AC: Read-only inputs ──────────────────────────────────────────────────────

func TestReadOnly(t *testing.T) {
	ks := makeStats("Strength")
	kp := expr.BasePreds()

	prog := mustParse(t, "Strength * 0.5 + moisture", expr.KindNum, ks, kp)

	origStatVal := 0.8
	origAttrVal := 0.5
	ctx := &stub{
		stats: map[core.StatID]float64{"Strength": origStatVal},
		attrs: map[core.Tag]float64{"moisture": origAttrVal},
	}
	_ = prog.EvalNumber(ctx)

	// Context maps must not be mutated.
	if ctx.stats[core.StatID("Strength")] != origStatVal {
		t.Error("Stat map mutated by EvalNumber")
	}
	if ctx.attrs[core.Tag("moisture")] != origAttrVal {
		t.Error("Attr map mutated by EvalNumber")
	}

	// Reads() returns a copy; mutating it cannot corrupt internal state.
	r := prog.Reads()
	if len(r) > 0 {
		r[0] = "MUTATED"
	}
	for _, id := range prog.Reads() {
		if string(id) == "MUTATED" {
			t.Error("Program internal Reads cache was mutated via the returned slice")
		}
	}

	// knownStats is not mutated by Parse.
	ks2 := makeStats("Strength")
	_, _ = expr.Parse("Strength > 0.5", expr.KindBool, ks2, kp)
	if !ks2.Has(core.StatID("Strength")) {
		t.Error("knownStats mutated by Parse")
	}
}

// ── AC: No forbidden imports (guard) ─────────────────────────────────────────

func TestNoForbiddenImports(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("os.ReadDir: %v", err)
	}
	forbidden := []string{
		`"os"`, `"os/`,
		`"net`,
		`"time"`,
		`math/rand`, `crypto/rand`,
		`"io/fs"`,
		`engine/kernel/rng`,
		`engine/mind/stats`,
		`engine/mind/gates`,
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		content := string(src)
		for _, f := range forbidden {
			if strings.Contains(content, f) {
				t.Errorf("%s: contains forbidden import/reference %q", e.Name(), f)
			}
		}
	}
}

// ── AC: Concurrent eval safe (plan phase) ─────────────────────────────────────

func TestConcurrentEval(t *testing.T) {
	ks := makeStats("Strength", "Agility")
	kp := expr.BasePreds()

	numProg := mustParse(t, "Strength * 0.5 + Agility * 0.3", expr.KindNum, ks, kp)
	boolProg := mustParse(t, "(Strength > 0.5) | (Agility > 0.3)", expr.KindBool, ks, kp)

	baseCtx := &stub{stats: map[core.StatID]float64{"Strength": 0.8, "Agility": 0.6}}
	wantNum := numProg.EvalNumber(baseCtx)
	wantBool := boolProg.EvalBool(baseCtx)

	const goroutines = 64
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			ctx := &stub{stats: map[core.StatID]float64{"Strength": 0.8, "Agility": 0.6}}
			got := numProg.EvalNumber(ctx)
			if got != wantNum {
				t.Errorf("concurrent EvalNumber: got %v want %v", got, wantNum)
			}
		}()
		go func() {
			defer wg.Done()
			ctx := &stub{stats: map[core.StatID]float64{"Strength": 0.8, "Agility": 0.6}}
			got := boolProg.EvalBool(ctx)
			if got != wantBool {
				t.Errorf("concurrent EvalBool: got %v want %v", got, wantBool)
			}
		}()
	}
	wg.Wait()
}
