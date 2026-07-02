package gates

import (
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/mind/stats"
)

// ── Helpers ────────────────────────────────────────────────────────────────

// minimalStatsYAML is a minimal stats document with the stats referenced by the
// shipped content/gates.yaml: Strength, Agility, Intelligence, Honesty, Aggression.
// It also includes a few extra stats for testing unknown stat rejection.
const minimalStatsYAML = `
schema_version: 1
stats:
  - id: Strength
    label: Strength
    kind: capability
    range: [0.0, 1.0]
    default: 0.5
    gen: { dist: normal, mean: 0.5, sd: 0.15 }
    inherit: 0.5
  - id: Agility
    label: Agility
    kind: capability
    range: [0.0, 1.0]
    default: 0.5
    gen: { dist: normal, mean: 0.5, sd: 0.15 }
    inherit: 0.5
  - id: Intelligence
    label: Intelligence
    kind: capability
    range: [0.0, 1.0]
    default: 0.5
    gen: { dist: normal, mean: 0.5, sd: 0.18 }
    inherit: 0.5
  - id: Honesty
    kind: disposition
    range: [0.0, 1.0]
    default: 0.6
    gen: { dist: normal, mean: 0.6, sd: 0.2 }
    inherit: 0.4
  - id: Aggression
    kind: disposition
    range: [0.0, 1.0]
    default: 0.3
    gen: { dist: normal, mean: 0.3, sd: 0.2 }
    inherit: 0.4
`

// mustLoadStats builds a stats.Registry from the YAML string or fails the test.
func mustLoadStats(t *testing.T, yamlContent string) *stats.Registry {
	t.Helper()
	reg, err := stats.Load(strings.NewReader(yamlContent))
	if err != nil {
		t.Fatalf("mustLoadStats: %v", err)
	}
	return reg
}

// mustLoadGates builds a gates.Registry from the YAML string or fails the test.
func mustLoadGates(t *testing.T, yamlContent string, statsReg *stats.Registry) *Registry {
	t.Helper()
	reg, err := Load(strings.NewReader(yamlContent), statsReg)
	if err != nil {
		t.Fatalf("mustLoadGates: %v", err)
	}
	return reg
}

// statSnapshot creates an AgentSnapshot with the given SelfStats values.
func statSnapshot(statsVals map[core.StatID]float64) AgentSnapshot {
	s := make(stats.Stats)
	for k, v := range statsVals {
		s[k] = v
	}
	return AgentSnapshot{SelfStats: s}
}

// ── AC: Load from injected io.Reader ───────────────────────────────────────

func TestLoad_Basic(t *testing.T) {
	sReg := mustLoadStats(t, minimalStatsYAML)

	const gatesYAML = `
schema_version: 2
gates:
  - id: test_gate
    tags: ["test:tag"]
    expr:
      stat: Strength
      op: ">="
      value: 0.5
`
	reg, err := Load(strings.NewReader(gatesYAML), sReg)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	ids := reg.IDs()
	if len(ids) != 1 {
		t.Fatalf("expected 1 gate, got %d", len(ids))
	}
	if ids[0] != "test_gate" {
		t.Errorf("expected id 'test_gate', got %q", ids[0])
	}
}

func TestLoad_ShippedContent(t *testing.T) {
	sReg := mustLoadStats(t, minimalStatsYAML)

	const shipped = `
schema_version: 2
gates:
  - id: capability_floor
    tags: ["uses:Strength", "uses:Agility", "uses:Intelligence"]
    expr:
      and:
        - or:
            - { not: { tag: "uses:Strength" } }
            - { stat: Strength, op: ">=", value: 0.15 }
        - or:
            - { not: { tag: "uses:Agility" } }
            - { stat: Agility, op: ">=", value: 0.15 }
        - or:
            - { not: { tag: "uses:Intelligence" } }
            - { stat: Intelligence, op: ">=", value: 0.15 }

  - id: knowledge
    tags: ["abstraction:low", "abstraction:med", "abstraction:high"]
    expr:
      or:
        - and: [{ tag: "abstraction:low" },  { stat: Intelligence, op: ">=", value: 0.20 }]
        - and: [{ tag: "abstraction:med" },  { stat: Intelligence, op: ">=", value: 0.45 }]
        - and: [{ tag: "abstraction:high" }, { stat: Intelligence, op: ">=", value: 0.70 }]

  - id: conscience
    tags: ["norm:transgressive"]
    expr:
      or:
        - { stat: Honesty,    op: "<",  value: 0.40 }
        - { stat: Aggression, op: ">=", value: 0.65 }
`
	reg, err := Load(strings.NewReader(shipped), sReg)
	if err != nil {
		t.Fatalf("Load shipped content failed: %v", err)
	}

	ids := reg.IDs()
	expectedIDs := []GateID{"capability_floor", "conscience", "knowledge"}
	if len(ids) != len(expectedIDs) {
		t.Fatalf("expected %d gates, got %d: %v", len(expectedIDs), len(ids), ids)
	}
	for i, want := range expectedIDs {
		if ids[i] != want {
			t.Errorf("ids[%d] = %q, want %q", i, ids[i], want)
		}
	}
}

// ── AC: Tag matching (D4) ──────────────────────────────────────────────────

func TestTagMatching(t *testing.T) {
	sReg := mustLoadStats(t, minimalStatsYAML)

	const gatesYAML = `
schema_version: 2
gates:
  - id: strength_gate
    tags: ["uses:Strength"]
    expr:
      stat: Strength
      op: ">="
      value: 0.5
  - id: transgression_gate
    tags: ["norm:transgressive"]
    expr:
      stat: Honesty
      op: "<"
      value: 0.5
  - id: catch_all_gate
    tags: []
    expr:
      stat: Strength
      op: ">="
      value: 0.0
`
	reg := mustLoadGates(t, gatesYAML, sReg)

	cases := []struct {
		name     string
		actTags  []core.Tag
		wantGate bool // whether strength_gate matches (used for introspection)
	}{
		{"uses:Strength present", []core.Tag{"uses:Strength"}, true},
		{"norm:transgressive present", []core.Tag{"norm:transgressive"}, false},
		{"social tag only", []core.Tag{"social"}, true}, // catch_all applies, strength_gate does not
		{"multiple tags", []core.Tag{"uses:Strength", "social"}, true},
		{"empty tags", []core.Tag{}, true}, // catches all -> catch_all matches, others don't
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			act := Action{Tags: tc.actTags}
			snap := statSnapshot(map[core.StatID]float64{core.StatID("Strength"): 0.3})

			// For the catch_all gate, Strength >= 0.0 is true with Strength=0.3, so visible.
			// For strength_gate, Strength >= 0.5 is false with Strength=0.3.
			// For transgression_gate, Honesty < 0.5: Honesty is 0 (absent), so 0 < 0.5 is true.
			// So if both catch_all and strength_gate match for "uses:Strength": false AND true = false.
			// If only catch_all matches (e.g. "social"): true.
			result := reg.Evaluate(act, snap)

			// For the "uses:Strength" case: strength_gate matches (false) AND catch_all matches (true) = false
			// For "social" case: only catch_all matches (true) = true
			// For "norm:transgressive": transgression_gate matches (Honesty=0 < 0.5 = true) AND catch_all matches (true) = true
			// For empty tags: only catch_all matches (true) = true

			if tc.name == "uses:Strength present" {
				if result.Visible {
					t.Errorf("expected invisible (strength_gate fails, Strength=0.3 < 0.5)")
				}
			} else if tc.name == "social tag only" || tc.name == "empty tags" {
				if !result.Visible {
					t.Errorf("expected visible (only catch_all matches and passes)")
				}
			} else if tc.name == "norm:transgressive present" {
				if !result.Visible {
					t.Errorf("expected visible (transgression_gate passes: Honesty=0 < 0.5, catch_all also passes)")
				}
			} else {
				t.Logf("result for %q: visible=%v trace=%v", tc.name, result.Visible, result.Trace)
			}
		})
	}
}

// ── AC: Visibility is AND of matching gates ────────────────────────────────

func TestVisibility_ANDofMatchingGates(t *testing.T) {
	sReg := mustLoadStats(t, minimalStatsYAML)

	const gatesYAML = `
schema_version: 2
gates:
  - id: gate_one
    tags: ["tag:a"]
    expr:
      stat: Strength
      op: ">="
      value: 0.5
  - id: gate_two
    tags: ["tag:a"]
    expr:
      stat: Agility
      op: ">="
      value: 0.5
  - id: other_gate
    tags: ["tag:b"]
    expr:
      stat: Strength
      op: ">="
      value: 0.0
`
	reg := mustLoadGates(t, gatesYAML, sReg)

	cases := []struct {
		name         string
		strength     float64
		agility      float64
		wantVisible  bool
		wantTraceLen int
	}{
		{"both gates pass", 0.7, 0.7, true, 2},
		{"gate_one fails", 0.3, 0.7, false, 2},
		{"gate_two fails", 0.7, 0.3, false, 2},
		{"both fail", 0.3, 0.3, false, 2},
		{"no matching gate", 0.3, 0.3, true, 0}, // no gate has "tag:c"
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			act := Action{Tags: []core.Tag{"tag:a"}}
			if tc.wantTraceLen == 0 {
				act = Action{Tags: []core.Tag{"tag:c"}}
			}
			snap := statSnapshot(map[core.StatID]float64{
				core.StatID("Strength"): tc.strength,
				core.StatID("Agility"):  tc.agility,
			})
			result := reg.Evaluate(act, snap)
			if result.Visible != tc.wantVisible {
				t.Errorf("Visible = %v, want %v", result.Visible, tc.wantVisible)
			}
			if len(result.Trace) != tc.wantTraceLen {
				t.Errorf("Trace length = %d, want %d", len(result.Trace), tc.wantTraceLen)
			}
		})
	}
}

// ── AC: Expr tree evaluation ───────────────────────────────────────────────

func TestExprEval_StatOps(t *testing.T) {
	sReg := mustLoadStats(t, minimalStatsYAML)

	// Test each operator as a single standalone gate.
	ops := []struct {
		opYAML string
		op     Op
	}{
		{">=", OpGE}, {">", OpGT}, {"<=", OpLE},
		{"<", OpLT}, {"==", OpEQ}, {"!=", OpNE},
	}

	for _, oc := range ops {
		t.Run(oc.opYAML, func(t *testing.T) {
			yaml := `
schema_version: 2
gates:
  - id: op_test
    tags: ["test"]
    expr:
      stat: Strength
      op: "` + oc.opYAML + `"
      value: 0.5
`
			reg := mustLoadGates(t, yaml, sReg)
			act := Action{Tags: []core.Tag{"test"}}

			// Test: value 0.5, threshold 0.5
			// OpGE: 0.5 >= 0.5 = true, OpGT: 0.5 > 0.5 = false
			// OpLE: 0.5 <= 0.5 = true, OpLT: 0.5 < 0.5 = false
			// OpEQ: 0.5 == 0.5 = true, OpNE: 0.5 != 0.5 = false
			snap := statSnapshot(map[core.StatID]float64{core.StatID("Strength"): 0.5})
			r := reg.Evaluate(act, snap)
			expectedPassed := cmpOp(0.5, oc.op, 0.5)
			if len(r.Trace) != 1 {
				t.Fatalf("expected 1 trace entry, got %d", len(r.Trace))
			}
			if r.Trace[0].Passed != expectedPassed {
				t.Errorf("Passed = %v, want %v for op %s (val=0.5, threshold=0.5)", r.Trace[0].Passed, expectedPassed, oc.opYAML)
			}
		})
	}
}

func TestExprEval_TagLeaf(t *testing.T) {
	sReg := mustLoadStats(t, minimalStatsYAML)

	// Use empty tags (matches all actions) to focus on the tag leaf evaluation.
	const yaml = `
schema_version: 2
gates:
  - id: tag_test
    tags: []
    expr:
      tag: "specific:tag"
`
	reg := mustLoadGates(t, yaml, sReg)

	cases := []struct {
		name     string
		actTags  []core.Tag
		wantPass bool
	}{
		{"tag present", []core.Tag{"specific:tag", "other"}, true},
		{"tag absent", []core.Tag{"other:tag"}, false},
		{"empty tags", []core.Tag{}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			act := Action{Tags: tc.actTags}
			snap := statSnapshot(nil)
			r := reg.Evaluate(act, snap)
			if len(r.Trace) != 1 {
				t.Fatalf("expected 1 trace entry, got %d", len(r.Trace))
			}
			if r.Trace[0].Passed != tc.wantPass {
				t.Errorf("Passed = %v, want %v", r.Trace[0].Passed, tc.wantPass)
			}
		})
	}
}

func TestExprEval_AndOrNot(t *testing.T) {
	sReg := mustLoadStats(t, minimalStatsYAML)

	// Empty tags so the gate always matches the action.
	const yaml = `
schema_version: 2
gates:
  - id: composite_test
    tags: []
    expr:
      or:
        - and:
            - { tag: "alpha" }
            - { tag: "beta" }
        - not:
            { tag: "gamma" }
`
	reg := mustLoadGates(t, yaml, sReg)

	cases := []struct {
		name     string
		actTags  []core.Tag
		wantPass bool
	}{
		{"and branch: alpha+beta passes", []core.Tag{"alpha", "beta"}, true},
		{"only alpha: and fails, not gamma is true", []core.Tag{"alpha"}, true},
		{"delta: and fails, not gamma is true", []core.Tag{"delta"}, true},
		{"gamma: and branch fails, not gamma is false", []core.Tag{"gamma"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			act := Action{Tags: tc.actTags}
			snap := statSnapshot(nil)
			r := reg.Evaluate(act, snap)
			if len(r.Trace) != 1 {
				t.Fatalf("expected 1 trace entry, got %d", len(r.Trace))
			}
			if r.Trace[0].Passed != tc.wantPass {
				t.Errorf("Passed = %v, want %v (tags=%v)", r.Trace[0].Passed, tc.wantPass, tc.actTags)
			}
		})
	}
}

// ── AC: Decisions read ToM[self] (D8) ──────────────────────────────────────

func TestDecisionsReadSelfStats(t *testing.T) {
	sReg := mustLoadStats(t, minimalStatsYAML)

	const yaml = `
schema_version: 2
gates:
  - id: strength_check
    tags: ["uses:Strength"]
    expr:
      stat: Strength
      op: ">="
      value: 0.5
`
	reg := mustLoadGates(t, yaml, sReg)
	act := Action{Tags: []core.Tag{"uses:Strength"}}

	// Agent with low self-belief: Strength=0.2 (< 0.5) → invisible
	// This is the self-sealing underestimation pattern (D8): even though
	// the agent might have real Strength=0.9, the gate reads SelfStats
	// (ToM[self]) and the action stays hidden. The AgentSnapshot only
	// exposes SelfStats, not Real Stats, so the gate cannot read the true value.
	lowBelief := statSnapshot(map[core.StatID]float64{core.StatID("Strength"): 0.2})
	r1 := reg.Evaluate(act, lowBelief)
	if r1.Visible {
		t.Errorf("expected invisible with SelfStats Strength=0.2 (below 0.5 floor)")
	}
	if !r1.Trace[0].Passed {
		t.Logf("gate correctly failed: Strength=0.2 < 0.5")
	}

	// Agent with high self-belief: Strength=0.9 (>= 0.5) → visible
	highBelief := statSnapshot(map[core.StatID]float64{core.StatID("Strength"): 0.9})
	r2 := reg.Evaluate(act, highBelief)
	if !r2.Visible {
		t.Errorf("expected visible with SelfStats Strength=0.9 (above 0.5 floor)")
	}
}

// ── AC: Unknown stat reference rejected at load ────────────────────────────

func TestLoad_UnknownStatRejected(t *testing.T) {
	sReg := mustLoadStats(t, minimalStatsYAML)

	const yaml = `
schema_version: 2
gates:
  - id: bad_gate
    tags: ["test"]
    expr:
      stat: NotAStat
      op: ">="
      value: 0.5
`
	_, err := Load(strings.NewReader(yaml), sReg)
	if err == nil {
		t.Fatal("expected error for unknown stat, got nil")
	}
	t.Logf("got expected error: %v", err)
}

func TestLoad_MultiShapeRejected(t *testing.T) {
	sReg := mustLoadStats(t, minimalStatsYAML)

	// A node with both 'stat' and 'and' should be rejected.
	const yaml = `
schema_version: 2
gates:
  - id: bad_gate
    tags: ["test"]
    expr:
      stat: Strength
      op: ">="
      value: 0.5
      and:
        - { stat: Strength, op: ">=", value: 0.3 }
`
	_, err := Load(strings.NewReader(yaml), sReg)
	if err == nil {
		t.Fatal("expected error for multi-shape node, got nil")
	}
	t.Logf("got expected error: %v", err)
}

// ── AC: Determinism (D12) ──────────────────────────────────────────────────

func TestDeterminism_IDsOrder(t *testing.T) {
	sReg := mustLoadStats(t, minimalStatsYAML)

	const yaml = `
schema_version: 2
gates:
  - id: zebra
    tags: []
    expr:
      stat: Strength
      op: ">="
      value: 0.0
  - id: alpha
    tags: []
    expr:
      stat: Agility
      op: ">="
      value: 0.0
  - id: mike
    tags: []
    expr:
      stat: Intelligence
      op: ">="
      value: 0.0
`
	reg := mustLoadGates(t, yaml, sReg)

	// IDs should be sorted lexicographically.
	want := []GateID{"alpha", "mike", "zebra"}
	ids := reg.IDs()
	if len(ids) != len(want) {
		t.Fatalf("IDs len = %d, want %d: %v", len(ids), len(want), ids)
	}
	for i, w := range want {
		if ids[i] != w {
			t.Errorf("IDs[%d] = %q, want %q", i, ids[i], w)
		}
	}

	// Calling twice returns same result.
	ids2 := reg.IDs()
	if len(ids2) != len(ids) {
		t.Fatal("second IDs() call returned different length")
	}
	for i := range ids {
		if ids[i] != ids2[i] {
			t.Errorf("IDs differs across calls at index %d: %q vs %q", i, ids[i], ids2[i])
		}
	}
}

func TestDeterminism_TraceOrder(t *testing.T) {
	sReg := mustLoadStats(t, minimalStatsYAML)

	const yaml = `
schema_version: 2
gates:
  - id: gate_b
    tags: ["all"]
    expr:
      stat: Strength
      op: ">="
      value: 0.0
  - id: gate_a
    tags: ["all"]
    expr:
      stat: Strength
      op: ">="
      value: 0.0
  - id: gate_c
    tags: ["all"]
    expr:
      stat: Strength
      op: ">="
      value: 0.0
`
	reg := mustLoadGates(t, yaml, sReg)
	act := Action{Tags: []core.Tag{"all"}}
	snap := statSnapshot(map[core.StatID]float64{core.StatID("Strength"): 0.5})

	// Trace should be in lexicographic gate ID order: gate_a, gate_b, gate_c
	result := reg.Evaluate(act, snap)
	want := []GateID{"gate_a", "gate_b", "gate_c"}
	if len(result.Trace) != len(want) {
		t.Fatalf("Trace len = %d, want %d", len(result.Trace), len(want))
	}
	for i, w := range want {
		if result.Trace[i].Gate != w {
			t.Errorf("Trace[%d].Gate = %q, want %q", i, result.Trace[i].Gate, w)
		}
	}
}

func TestDeterminism_PureFunction(t *testing.T) {
	sReg := mustLoadStats(t, minimalStatsYAML)

	const yaml = `
schema_version: 2
gates:
  - id: strength_gate
    tags: ["uses:Strength"]
    expr:
      stat: Strength
      op: ">="
      value: 0.5
`
	reg := mustLoadGates(t, yaml, sReg)
	act := Action{Tags: []core.Tag{"uses:Strength"}}
	snap := statSnapshot(map[core.StatID]float64{core.StatID("Strength"): 0.7})

	// Evaluate twice — must return identical results.
	r1 := reg.Evaluate(act, snap)
	r2 := reg.Evaluate(act, snap)

	if r1.Visible != r2.Visible {
		t.Errorf("Visible differs across calls: %v vs %v", r1.Visible, r2.Visible)
	}
	if len(r1.Trace) != len(r2.Trace) {
		t.Fatalf("Trace length differs: %d vs %d", len(r1.Trace), len(r2.Trace))
	}
	for i := range r1.Trace {
		if r1.Trace[i] != r2.Trace[i] {
			t.Errorf("Trace[%d] differs: %v vs %v", i, r1.Trace[i], r2.Trace[i])
		}
	}
}

// ── AC: Read-only inputs ───────────────────────────────────────────────────

func TestEvaluate_DoesNotMutateInputs(t *testing.T) {
	sReg := mustLoadStats(t, minimalStatsYAML)

	const yaml = `
schema_version: 2
gates:
  - id: strength_gate
    tags: ["uses:Strength"]
    expr:
      stat: Strength
      op: ">="
      value: 0.5
`
	reg := mustLoadGates(t, yaml, sReg)

	origTags := []core.Tag{"uses:Strength", "social"}
	origTarget := core.ObjectID("obj-1")
	act := Action{
		Tags:   append([]core.Tag{}, origTags...), // copy
		Target: origTarget,
	}

	snapStats := stats.Stats{core.StatID("Strength"): 0.7}
	origKnown := map[core.ObjectID]struct{}{"obj-2": {}}
	snap := AgentSnapshot{
		SelfStats: snapStats,
		Known:     origKnown,
	}

	// Capture before state.
	tagsBefore := make([]core.Tag, len(act.Tags))
	copy(tagsBefore, act.Tags)
	targetBefore := act.Target
	selfStatsBefore := snap.SelfStats.Clone()
	knownLenBefore := len(snap.Known)

	_ = reg.Evaluate(act, snap)

	// Verify unchanged.
	if act.Target != targetBefore {
		t.Errorf("Target changed: was %q, now %q", targetBefore, act.Target)
	}
	if len(act.Tags) != len(tagsBefore) {
		t.Errorf("Tags length changed: was %d, now %d", len(tagsBefore), len(act.Tags))
	}
	for i := range tagsBefore {
		if act.Tags[i] != tagsBefore[i] {
			t.Errorf("Tags[%d] changed: was %q, now %q", i, tagsBefore[i], act.Tags[i])
		}
	}
	for k, v := range selfStatsBefore {
		if snap.SelfStats[k] != v {
			t.Errorf("SelfStats[%q] changed: was %v, now %v", k, v, snap.SelfStats[k])
		}
	}
	if len(snap.Known) != knownLenBefore {
		t.Errorf("Known length changed: was %d, now %d", knownLenBefore, len(snap.Known))
	}
}

// ── AC: Reads() union ──────────────────────────────────────────────────────

func TestReads_Union(t *testing.T) {
	sReg := mustLoadStats(t, minimalStatsYAML)

	const yaml = `
schema_version: 2
gates:
  - id: gate_a
    tags: ["a"]
    expr:
      stat: Strength
      op: ">="
      value: 0.5
  - id: gate_b
    tags: ["b"]
    expr:
      or:
        - { stat: Honesty, op: "<", value: 0.4 }
        - { stat: Aggression, op: ">=", value: 0.65 }
  - id: gate_c
    tags: ["c"]
    expr:
      stat: Strength
      op: "<="
      value: 0.8
`
	reg := mustLoadGates(t, yaml, sReg)
	reads := reg.Reads()

	// Expected: Aggression, Honesty, Strength (sorted lexicographically by StatID)
	want := []core.StatID{"Aggression", "Honesty", "Strength"}
	if len(reads) != len(want) {
		t.Fatalf("Reads len = %d, want %d: got %v", len(reads), len(want), reads)
	}
	for i, w := range want {
		if reads[i] != w {
			t.Errorf("Reads[%d] = %q, want %q", i, reads[i], w)
		}
	}
}

func TestReads_Empty(t *testing.T) {
	sReg := mustLoadStats(t, minimalStatsYAML)

	// Gate with only a tag leaf — no stat references.
	const yaml = `
schema_version: 2
gates:
  - id: tag_only
    tags: ["test"]
    expr:
      tag: "some:tag"
`
	reg := mustLoadGates(t, yaml, sReg)
	reads := reg.Reads()
	if len(reads) != 0 {
		t.Errorf("expected empty reads for tag-only gates, got %v", reads)
	}
}

// ── AC: Absent stat treated as 0 ───────────────────────────────────────────

func TestAbsentStat_TreatedAsZero(t *testing.T) {
	sReg := mustLoadStats(t, minimalStatsYAML)

	const yaml = `
schema_version: 2
gates:
  - id: absent_test
    tags: ["test"]
    expr:
      or:
        - { stat: Strength, op: ">=", value: 0.01 }
        - { stat: Strength, op: "<", value: 0.01 }
`
	reg := mustLoadGates(t, yaml, sReg)
	act := Action{Tags: []core.Tag{"test"}}

	// Snapshot with no stats at all → Strength = 0 (absent).
	// Strength >= 0.01: false (0 < 0.01)
	// Strength < 0.01: true (0 < 0.01)
	snap := statSnapshot(nil)
	r := reg.Evaluate(act, snap)
	if !r.Visible {
		t.Errorf("expected visible: absent stat treated as 0, so Strength=0 < 0.01")
	}
}

// ── AC: Empty gate list rejected ───────────────────────────────────────────

func TestLoad_EmptyGatesList(t *testing.T) {
	sReg := mustLoadStats(t, minimalStatsYAML)

	const yaml = `
schema_version: 2
gates: []
`
	_, err := Load(strings.NewReader(yaml), sReg)
	if err == nil {
		t.Fatal("expected error for empty gates list, got nil")
	}
}

// ── AC: Shipped content evaluation (golden) ────────────────────────────────

func TestShippedContent_Evaluate(t *testing.T) {
	sReg := mustLoadStats(t, minimalStatsYAML)

	const shipped = `
schema_version: 2
gates:
  - id: capability_floor
    tags: ["uses:Strength", "uses:Agility", "uses:Intelligence"]
    expr:
      and:
        - or:
            - { not: { tag: "uses:Strength" } }
            - { stat: Strength, op: ">=", value: 0.15 }
        - or:
            - { not: { tag: "uses:Agility" } }
            - { stat: Agility, op: ">=", value: 0.15 }
        - or:
            - { not: { tag: "uses:Intelligence" } }
            - { stat: Intelligence, op: ">=", value: 0.15 }

  - id: knowledge
    tags: ["abstraction:low", "abstraction:med", "abstraction:high"]
    expr:
      or:
        - and:
            - { tag: "abstraction:low" }
            - { stat: Intelligence, op: ">=", value: 0.20 }
        - and:
            - { tag: "abstraction:med" }
            - { stat: Intelligence, op: ">=", value: 0.45 }
        - and:
            - { tag: "abstraction:high" }
            - { stat: Intelligence, op: ">=", value: 0.70 }

  - id: conscience
    tags: ["norm:transgressive"]
    expr:
      or:
        - { stat: Honesty, op: "<", value: 0.40 }
        - { stat: Aggression, op: ">=", value: 0.65 }
`

	reg := mustLoadGates(t, shipped, sReg)

	cases := []struct {
		name        string
		actTags     []core.Tag
		selfStats   map[core.StatID]float64
		wantVisible bool
		wantTrace   []GateContribution
	}{
		{
			name:        "capability floor: strong enough",
			actTags:     []core.Tag{"uses:Strength"},
			selfStats:   map[core.StatID]float64{core.StatID("Strength"): 0.5, core.StatID("Agility"): 0.5, core.StatID("Intelligence"): 0.5},
			wantVisible: true,
			wantTrace: []GateContribution{
				{Gate: "capability_floor", Passed: true},
			},
		},
		{
			name:        "capability floor: too weak",
			actTags:     []core.Tag{"uses:Strength"},
			selfStats:   map[core.StatID]float64{core.StatID("Strength"): 0.1, core.StatID("Agility"): 0.5, core.StatID("Intelligence"): 0.5},
			wantVisible: false,
			wantTrace: []GateContribution{
				{Gate: "capability_floor", Passed: false},
			},
		},
		{
			name:        "knowledge: low abstraction",
			actTags:     []core.Tag{"abstraction:low"},
			selfStats:   map[core.StatID]float64{core.StatID("Intelligence"): 0.3},
			wantVisible: true,
			wantTrace: []GateContribution{
				{Gate: "knowledge", Passed: true},
			},
		},
		{
			name:        "knowledge: low abstraction, too dumb",
			actTags:     []core.Tag{"abstraction:low"},
			selfStats:   map[core.StatID]float64{core.StatID("Intelligence"): 0.1},
			wantVisible: false, // only knowledge matches, and it fails
			wantTrace: []GateContribution{
				{Gate: "knowledge", Passed: false},
			},
		},
		{
			name:        "conscience: low honesty passes",
			actTags:     []core.Tag{"norm:transgressive"},
			selfStats:   map[core.StatID]float64{core.StatID("Honesty"): 0.2, core.StatID("Aggression"): 0.3},
			wantVisible: true,
			wantTrace: []GateContribution{
				{Gate: "conscience", Passed: true},
			},
		},
		{
			name:        "conscience: high honesty and low aggression blocks",
			actTags:     []core.Tag{"norm:transgressive"},
			selfStats:   map[core.StatID]float64{core.StatID("Honesty"): 0.8, core.StatID("Aggression"): 0.3},
			wantVisible: false,
			wantTrace: []GateContribution{
				{Gate: "conscience", Passed: false},
			},
		},
		{
			name:        "no matching gate",
			actTags:     []core.Tag{"social"},
			selfStats:   map[core.StatID]float64{},
			wantVisible: true,
			wantTrace:   []GateContribution{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			act := Action{Tags: tc.actTags}
			snap := statSnapshot(tc.selfStats)
			result := reg.Evaluate(act, snap)

			if result.Visible != tc.wantVisible {
				t.Errorf("Visible = %v, want %v", result.Visible, tc.wantVisible)
			}

			if len(result.Trace) != len(tc.wantTrace) {
				t.Fatalf("Trace length = %d, want %d\n  got:  %v\n  want: %v",
					len(result.Trace), len(tc.wantTrace), result.Trace, tc.wantTrace)
			}
			for i, w := range tc.wantTrace {
				if result.Trace[i].Gate != w.Gate {
					t.Errorf("Trace[%d].Gate = %q, want %q", i, result.Trace[i].Gate, w.Gate)
				}
				if result.Trace[i].Passed != w.Passed {
					t.Errorf("Trace[%d].Passed = %v, want %v (gate=%q)", i, result.Trace[i].Passed, w.Passed, w.Gate)
				}
			}
		})
	}
}

// ── AC: Empty tags matches all actions ──────────────────────────────────────

func TestEmptyTags_MatchesAll(t *testing.T) {
	sReg := mustLoadStats(t, minimalStatsYAML)

	const yaml = `
schema_version: 2
gates:
  - id: always
    tags: []
    expr:
      stat: Strength
      op: ">="
      value: 0.5
`
	reg := mustLoadGates(t, yaml, sReg)

	// Action with any tags should be matched by the empty-tags gate.
	act := Action{Tags: []core.Tag{"anything", "something"}}
	snap := statSnapshot(map[core.StatID]float64{core.StatID("Strength"): 0.6})
	r := reg.Evaluate(act, snap)
	if !r.Visible {
		t.Errorf("expected visible: Strength=0.6 >= 0.5")
	}

	// Action with no tags should also be matched.
	act2 := Action{Tags: []core.Tag{}}
	r2 := reg.Evaluate(act2, snap)
	if !r2.Visible {
		t.Errorf("expected visible with empty tags on action")
	}
}

// ── AC: Load from file YAML like the shipped content ───────────────────────

func TestLoadAndEvaluateShippedContent(t *testing.T) {
	// Load the actual shipped content/gates.yaml content.
	sReg := mustLoadStats(t, minimalStatsYAML)

	const shipped = `
schema_version: 2
gates:
  - id: capability_floor
    tags: ["uses:Strength", "uses:Agility", "uses:Intelligence"]
    expr:
      and:
        - or:
            - { not: { tag: "uses:Strength" } }
            - { stat: Strength, op: ">=", value: 0.15 }
        - or:
            - { not: { tag: "uses:Agility" } }
            - { stat: Agility, op: ">=", value: 0.15 }
        - or:
            - { not: { tag: "uses:Intelligence" } }
            - { stat: Intelligence, op: ">=", value: 0.15 }

  - id: knowledge
    tags: ["abstraction:low", "abstraction:med", "abstraction:high"]
    expr:
      or:
        - and:
            - { tag: "abstraction:low" }
            - { stat: Intelligence, op: ">=", value: 0.20 }
        - and:
            - { tag: "abstraction:med" }
            - { stat: Intelligence, op: ">=", value: 0.45 }
        - and:
            - { tag: "abstraction:high" }
            - { stat: Intelligence, op: ">=", value: 0.70 }

  - id: conscience
    tags: ["norm:transgressive"]
    expr:
      or:
        - { stat: Honesty, op: "<", value: 0.40 }
        - { stat: Aggression, op: ">=", value: 0.65 }
`
	reg, err := Load(strings.NewReader(shipped), sReg)
	if err != nil {
		t.Fatalf("Load shipped content: %v", err)
	}

	// Smoke test that reads matches the glossary spec.
	reads := reg.Reads()
	t.Logf("Reads: %v", reads)
	if len(reads) != 5 {
		t.Errorf("expected 5 stat reads, got %d: %v", len(reads), reads)
	}
	// Should be: Aggression, Agility, Honesty, Intelligence, Strength
	expectedReads := []core.StatID{"Aggression", "Agility", "Honesty", "Intelligence", "Strength"}
	for i, want := range expectedReads {
		if reads[i] != want {
			t.Errorf("Reads[%d] = %q, want %q", i, reads[i], want)
		}
	}

	// Verify IDs order.
	ids := reg.IDs()
	expectedIDs := []GateID{"capability_floor", "conscience", "knowledge"}
	if len(ids) != len(expectedIDs) {
		t.Fatalf("IDs: got %v, want %v", ids, expectedIDs)
	}
	for i, w := range expectedIDs {
		if ids[i] != w {
			t.Errorf("IDs[%d] = %q, want %q", i, ids[i], w)
		}
	}
}
