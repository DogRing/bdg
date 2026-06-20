package gates

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/stats"
)

// ── Golden Snapshot Test ───────────────────────────────────────────────────
//
// This test loads the shipped content/gates.yaml shape (embedded), evaluates a
// fixed set of (action, snapshot) pairs, and compares the serialized result to
// a golden file. On intentional changes, run:
//
//	go test ./engine/gates/... -run TestGolden -update
//
// and commit the updated golden file after human review.

const goldenShippedYAML = `
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

// goldenTestCase is one evaluate call in the golden snapshot.
type goldenTestCase struct {
	ActionTags     []string          `json:"action_tags"`
	SelfStats      map[string]float64 `json:"self_stats"`
	Visible        bool              `json:"visible"`
	TraceLength    int               `json:"trace_length"`
	TraceSummaries []gateSummary     `json:"trace,omitempty"`
}

type gateSummary struct {
	Gate   string `json:"gate"`
	Passed bool   `json:"passed"`
}

// goldenDocument is the top-level golden snapshot shape.
type goldenDocument struct {
	GateIDs  []string          `json:"gate_ids"`
	Reads    []string          `json:"reads"`
	Results  []goldenTestCase  `json:"results"`
}

func TestGolden(t *testing.T) {
	sReg := mustLoadStats(t, minimalStatsYAML)
	reg := mustLoadGates(t, goldenShippedYAML, sReg)

	// Golden file path.
	goldenPath := filepath.Join("testdata", "golden", "shipped.json")

	// Collect introspection data.
	ids := make([]string, len(reg.IDs()))
	for i, id := range reg.IDs() {
		ids[i] = string(id)
	}
	reads := make([]string, len(reg.Reads()))
	for i, r := range reg.Reads() {
		reads[i] = string(r)
	}

	// Evaluate a fixed set of test cases.
	testCases := []struct {
		name      string
		actTags   []core.Tag
		selfStats map[core.StatID]float64
	}{
		{
			name:      "capability_floor: strong enough",
			actTags:   []core.Tag{"uses:Strength"},
			selfStats: map[core.StatID]float64{core.StatID("Strength"): 0.8, core.StatID("Agility"): 0.5, core.StatID("Intelligence"): 0.5},
		},
		{
			name:      "capability_floor: too weak",
			actTags:   []core.Tag{"uses:Strength"},
			selfStats: map[core.StatID]float64{core.StatID("Strength"): 0.1, core.StatID("Agility"): 0.5, core.StatID("Intelligence"): 0.5},
		},
		{
			name:      "knowledge: low abstraction passes",
			actTags:   []core.Tag{"abstraction:low"},
			selfStats: map[core.StatID]float64{core.StatID("Intelligence"): 0.3},
		},
		{
			name:      "knowledge: low abstraction fails",
			actTags:   []core.Tag{"abstraction:low"},
			selfStats: map[core.StatID]float64{core.StatID("Intelligence"): 0.1},
		},
		{
			name:      "conscience: low honesty passes",
			actTags:   []core.Tag{"norm:transgressive"},
			selfStats: map[core.StatID]float64{core.StatID("Honesty"): 0.2, core.StatID("Aggression"): 0.3},
		},
		{
			name:      "conscience: high honesty blocks",
			actTags:   []core.Tag{"norm:transgressive"},
			selfStats: map[core.StatID]float64{core.StatID("Honesty"): 0.8, core.StatID("Aggression"): 0.3},
		},
		{
			name:      "no matching gate",
			actTags:   []core.Tag{"social"},
			selfStats: map[core.StatID]float64{},
		},
	}

	results := make([]goldenTestCase, 0, len(testCases))
	for _, tc := range testCases {
		act := Action{Tags: tc.actTags}
		snap := statSnapshot(tc.selfStats)
		r := reg.Evaluate(act, snap)

		gtc := goldenTestCase{
			ActionTags:  tagsToStrings(tc.actTags),
			SelfStats:   statsToStrings(tc.selfStats),
			Visible:     r.Visible,
			TraceLength: len(r.Trace),
		}
		for _, t := range r.Trace {
			gtc.TraceSummaries = append(gtc.TraceSummaries, gateSummary{
				Gate:   string(t.Gate),
				Passed: t.Passed,
			})
		}
		results = append(results, gtc)
	}

	doc := goldenDocument{
		GateIDs: ids,
		Reads:   reads,
		Results: results,
	}

	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal golden: %v", err)
	}

	// Check for -update flag.
	if os.Getenv("TEST_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0755); err != nil {
			t.Fatalf("mkdir golden: %v", err)
		}
		if err := os.WriteFile(goldenPath, raw, 0644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("golden updated: %s", goldenPath)
		return
	}

	// Compare against golden.
	wantRaw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v (run with TEST_UPDATE_GOLDEN=1 to create)", err)
	}

	if string(raw) != string(wantRaw) {
		t.Errorf("golden mismatch for shipped content.\n--- got:\n%s\n--- want:\n%s", string(raw), string(wantRaw))
	}
}

func tagsToStrings(tags []core.Tag) []string {
	out := make([]string, len(tags))
	for i, t := range tags {
		out[i] = string(t)
	}
	return out
}

func statsToStrings(s map[core.StatID]float64) map[string]float64 {
	out := make(map[string]float64, len(s))
	for k, v := range s {
		out[string(k)] = v
	}
	return out
}

// ── Extra tests used by helpers_test.go compatible pattern ─────────────────

func mustLoadStatsFromYAML(t *testing.T, yamlContent string) *stats.Registry {
	t.Helper()
	reg, err := stats.Load(strings.NewReader(yamlContent))
	if err != nil {
		t.Fatalf("mustLoadStatsFromYAML: %v", err)
	}
	return reg
}
