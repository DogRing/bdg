package gates

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/stats"
)

// ── P3 gates (schema_version 3) — body-scalar leaves + cost rule ────────────
//
// Covers the P3 additions the original shipped.json golden (3 v2 gates) omits:
//   - stamina   : effort:high invisible while Stamina < 0.20 (body-scalar leaf)
//   - apathy    : abstraction:med|high invisible while Mood <= -0.60 (body-scalar leaf)
//   - adrenaline: cost-rule gate — 0.5× discount on effort:high/risk:high/violent
//                 when Adrenaline >= 0.70; NEVER hides an action
//   - conscience: urgency-relief branch (body: Urgency leaf)
//
// The thresholds mirror content/balance.yaml `gates:` block and the shipped
// content/gates.yaml expr trees verbatim.

const p3GatesYAML = `
schema_version: 3
gates:
  - id: adrenaline
    tags: ["effort:high", "risk:high", "violent:low", "violent:med", "violent:high"]
    cost_rule: { mult: 0.50 }
    expr:
      and:
        - { body: Adrenaline, op: ">=", value: 0.70 }
        - or:
            - { tag: "effort:high" }
            - { tag: "risk:high" }
            - { tag: "violent:low" }
            - { tag: "violent:med" }
            - { tag: "violent:high" }
  - id: apathy
    tags: ["abstraction:med", "abstraction:high"]
    expr:
      or:
        - { body: Mood, op: ">", value: -0.60 }
        - and:
            - { not: { tag: "abstraction:med" } }
            - { not: { tag: "abstraction:high" } }
  - id: conscience
    tags: ["norm:transgressive"]
    expr:
      or:
        - { stat: Honesty, op: "<", value: 0.40 }
        - { stat: Aggression, op: ">=", value: 0.65 }
        - and:
            - { body: Urgency, op: ">", value: 0.70 }
            - or:
                - { stat: Honesty, op: "<", value: 0.55 }
                - { stat: Aggression, op: ">=", value: 0.50 }
  - id: stamina
    tags: ["effort:high"]
    expr:
      or:
        - { not: { tag: "effort:high" } }
        - { body: Stamina, op: ">=", value: 0.20 }
`

// bodySnapshot builds an AgentSnapshot with both ToM[self] stats and live Body scalars.
func bodySnapshot(selfStats map[core.StatID]float64, stamina, mood, adrenaline, urgency float64) AgentSnapshot {
	s := make(stats.Stats)
	for k, v := range selfStats {
		s[k] = v
	}
	return AgentSnapshot{
		SelfStats:  s,
		Stamina:    stamina,
		Mood:       mood,
		Adrenaline: adrenaline,
		Urgency:    urgency,
	}
}

// ── Golden: P3 gates over a fixed (action, body) case set ───────────────────

type p3GateSummary struct {
	Gate   string  `json:"gate"`
	Passed bool    `json:"passed"`
	Mult   float64 `json:"mult"`
}

type p3Case struct {
	Name           string             `json:"name"`
	ActionTags     []string           `json:"action_tags"`
	SelfStats      map[string]float64 `json:"self_stats,omitempty"`
	Stamina        float64            `json:"stamina"`
	Mood           float64            `json:"mood"`
	Adrenaline     float64            `json:"adrenaline"`
	Urgency        float64            `json:"urgency"`
	Visible        bool               `json:"visible"`
	CostMultiplier float64            `json:"cost_multiplier"`
	Trace          []p3GateSummary    `json:"trace,omitempty"`
}

type p3Document struct {
	GateIDs   []string `json:"gate_ids"`
	Reads     []string `json:"reads"`
	ReadsBody []string `json:"reads_body"`
	Results   []p3Case `json:"results"`
}

func TestP3Gates_Golden(t *testing.T) {
	sReg := mustLoadStats(t, minimalStatsYAML)
	reg := mustLoadGates(t, p3GatesYAML, sReg)

	goldenPath := filepath.Join("testdata", "golden", "p3_gates.json")

	cases := []struct {
		name       string
		actTags    []core.Tag
		selfStats  map[core.StatID]float64
		stamina    float64
		mood       float64
		adrenaline float64
		urgency    float64
	}{
		// stamina gate — Stamina=0 drained agent hides high-effort work.
		{name: "stamina: drained agent → effort:high invisible", actTags: []core.Tag{"effort:high"}, stamina: 0.0},
		{name: "stamina: rested agent → effort:high visible", actTags: []core.Tag{"effort:high"}, stamina: 0.5},
		{name: "stamina: at threshold 0.20 → visible (>=)", actTags: []core.Tag{"effort:high"}, stamina: 0.20},
		{name: "stamina: effort:low unaffected (gate not matched)", actTags: []core.Tag{"effort:low"}, stamina: 0.0},
		// apathy gate — low Mood narrows abstract methods out of view.
		{name: "apathy: depressed → abstraction:med invisible", actTags: []core.Tag{"abstraction:med"}, mood: -0.8},
		{name: "apathy: depressed → abstraction:high invisible", actTags: []core.Tag{"abstraction:high"}, mood: -0.8},
		{name: "apathy: normal mood → abstraction:med visible", actTags: []core.Tag{"abstraction:med"}, mood: 0.0},
		{name: "apathy: depressed but abstraction:low unaffected", actTags: []core.Tag{"abstraction:low"}, mood: -0.8},
		// adrenaline cost rule — discount but never hides.
		{name: "adrenaline: high arousal → violent:high cost ×0.5", actTags: []core.Tag{"violent:high"}, adrenaline: 0.9},
		{name: "adrenaline: low arousal → no discount", actTags: []core.Tag{"violent:high"}, adrenaline: 0.1},
		{name: "adrenaline: high arousal + effort:high (rested) → visible & discounted", actTags: []core.Tag{"effort:high"}, stamina: 0.8, adrenaline: 0.9},
		// conscience urgency-relief branch (body: Urgency leaf).
		{name: "conscience: high urgency lowers barrier → visible", actTags: []core.Tag{"norm:transgressive"}, selfStats: map[core.StatID]float64{"Honesty": 0.5, "Aggression": 0.3}, urgency: 0.8},
		{name: "conscience: low urgency keeps barrier → invisible", actTags: []core.Tag{"norm:transgressive"}, selfStats: map[core.StatID]float64{"Honesty": 0.5, "Aggression": 0.3}, urgency: 0.0},
	}

	results := make([]p3Case, 0, len(cases))
	for _, tc := range cases {
		act := Action{Tags: tc.actTags}
		snap := bodySnapshot(tc.selfStats, tc.stamina, tc.mood, tc.adrenaline, tc.urgency)
		r := reg.Evaluate(act, snap)

		pc := p3Case{
			Name:           tc.name,
			ActionTags:     tagsToStrings(tc.actTags),
			SelfStats:      statsToStrings(tc.selfStats),
			Stamina:        tc.stamina,
			Mood:           tc.mood,
			Adrenaline:     tc.adrenaline,
			Urgency:        tc.urgency,
			Visible:        r.Visible,
			CostMultiplier: r.CostMultiplier,
		}
		for _, g := range r.Trace {
			pc.Trace = append(pc.Trace, p3GateSummary{Gate: string(g.Gate), Passed: g.Passed, Mult: g.Mult})
		}
		results = append(results, pc)
	}

	gateIDs := make([]string, len(reg.IDs()))
	for i, id := range reg.IDs() {
		gateIDs[i] = string(id)
	}
	reads := make([]string, len(reg.Reads()))
	for i, r := range reg.Reads() {
		reads[i] = string(r)
	}
	readsBody := make([]string, len(reg.ReadsBody()))
	for i, b := range reg.ReadsBody() {
		readsBody[i] = string(b)
	}

	doc := p3Document{GateIDs: gateIDs, Reads: reads, ReadsBody: readsBody, Results: results}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal golden: %v", err)
	}

	if os.Getenv("TEST_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("mkdir golden: %v", err)
		}
		if err := os.WriteFile(goldenPath, raw, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("golden updated: %s", goldenPath)
		return
	}

	wantRaw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v (run with TEST_UPDATE_GOLDEN=1 to create)", err)
	}
	if string(raw) != string(wantRaw) {
		t.Errorf("p3 gates golden mismatch.\n--- got:\n%s\n--- want:\n%s", string(raw), string(wantRaw))
	}
}

// ── Stamina gate: a Stamina=0 agent cannot SEE effort:high actions ──────────

func TestStaminaGate_DrainedHidesHighEffort(t *testing.T) {
	sReg := mustLoadStats(t, minimalStatsYAML)
	reg := mustLoadGates(t, p3GatesYAML, sReg)

	tests := []struct {
		name        string
		tags        []core.Tag
		stamina     float64
		wantVisible bool
	}{
		{"drained, effort:high", []core.Tag{"effort:high"}, 0.0, false},
		{"just below threshold", []core.Tag{"effort:high"}, 0.19, false},
		{"at threshold", []core.Tag{"effort:high"}, 0.20, true},
		{"rested", []core.Tag{"effort:high"}, 0.9, true},
		{"drained but low-effort (gate not matched)", []core.Tag{"effort:low"}, 0.0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := reg.Evaluate(Action{Tags: tt.tags}, bodySnapshot(nil, tt.stamina, 0, 0, 0))
			if r.Visible != tt.wantVisible {
				t.Errorf("Visible = %v, want %v (Stamina=%.2f tags=%v)", r.Visible, tt.wantVisible, tt.stamina, tt.tags)
			}
		})
	}
}

// ── Apathy gate: low Mood narrows abstract methods out of view ──────────────
//
// The gate's visibility-narrowing is the gates-level mechanism behind the
// agent's effective deliberation narrowing under apathy: abstraction:med|high
// methods stop being considered once Mood <= -0.60.

func TestApathyGate_LowMoodNarrowsAbstraction(t *testing.T) {
	sReg := mustLoadStats(t, minimalStatsYAML)
	reg := mustLoadGates(t, p3GatesYAML, sReg)

	tests := []struct {
		name        string
		tags        []core.Tag
		mood        float64
		wantVisible bool
	}{
		{"depressed hides abstraction:med", []core.Tag{"abstraction:med"}, -0.8, false},
		{"depressed hides abstraction:high", []core.Tag{"abstraction:high"}, -0.8, false},
		{"at threshold -0.60 hides (<=)", []core.Tag{"abstraction:med"}, -0.60, false},
		{"just above threshold shows", []core.Tag{"abstraction:med"}, -0.59, true},
		{"neutral mood shows abstraction:high", []core.Tag{"abstraction:high"}, 0.0, true},
		{"depressed leaves abstraction:low alone", []core.Tag{"abstraction:low"}, -0.9, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := reg.Evaluate(Action{Tags: tt.tags}, bodySnapshot(nil, 1.0, tt.mood, 0, 0))
			if r.Visible != tt.wantVisible {
				t.Errorf("Visible = %v, want %v (Mood=%.2f tags=%v)", r.Visible, tt.wantVisible, tt.mood, tt.tags)
			}
		})
	}

	// Count how many abstract methods remain visible — the "narrowing" effect.
	abstractActs := [][]core.Tag{{"abstraction:med"}, {"abstraction:high"}, {"abstraction:low"}}
	countVisible := func(mood float64) int {
		n := 0
		for _, tags := range abstractActs {
			if reg.Evaluate(Action{Tags: tags}, bodySnapshot(nil, 1.0, mood, 0, 0)).Visible {
				n++
			}
		}
		return n
	}
	if hi, lo := countVisible(0.0), countVisible(-0.8); lo >= hi {
		t.Errorf("apathy should narrow the abstract option set: visible(neutral)=%d visible(depressed)=%d", hi, lo)
	}
}

// ── Adrenaline gate: cost-multiplier direction (discount, never hides) ──────
//
// High arousal (the post-urgency adrenaline surge) discounts the cost of
// effort:high / risk:high / violent:* actions and must never change visibility.

func TestAdrenalineGate_CostMultiplierDirection(t *testing.T) {
	sReg := mustLoadStats(t, minimalStatsYAML)
	reg := mustLoadGates(t, p3GatesYAML, sReg)

	tests := []struct {
		name       string
		tags       []core.Tag
		adrenaline float64
		stamina    float64
		wantMult   float64
	}{
		{"high arousal discounts violent:high", []core.Tag{"violent:high"}, 0.9, 1.0, 0.50},
		{"high arousal discounts risk:high", []core.Tag{"risk:high"}, 0.9, 1.0, 0.50},
		{"high arousal discounts effort:high", []core.Tag{"effort:high"}, 0.9, 1.0, 0.50},
		{"at threshold 0.70 discounts", []core.Tag{"violent:high"}, 0.70, 1.0, 0.50},
		{"low arousal → no discount", []core.Tag{"violent:high"}, 0.1, 1.0, 1.0},
		{"just below threshold → no discount", []core.Tag{"violent:high"}, 0.69, 1.0, 1.0},
		{"non-qualifying tag → no discount even at high arousal", []core.Tag{"social"}, 0.9, 1.0, 1.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := reg.Evaluate(Action{Tags: tt.tags}, bodySnapshot(nil, tt.stamina, 0, tt.adrenaline, 0))
			if r.CostMultiplier != tt.wantMult {
				t.Errorf("CostMultiplier = %v, want %v (Adrenaline=%.2f tags=%v)", r.CostMultiplier, tt.wantMult, tt.adrenaline, tt.tags)
			}
			// Direction: the multiplier is a discount (≤ 1) and never a penalty.
			if r.CostMultiplier > 1.0 {
				t.Errorf("adrenaline must only discount, got penalty multiplier %v", r.CostMultiplier)
			}
			// The adrenaline gate never hides an action (cost-rule gate).
			if !r.Visible {
				t.Errorf("adrenaline cost-rule gate must not hide the action (Visible=false)")
			}
		})
	}

	// Monotonic direction: arousal can only lower (never raise) the cost factor.
	hot := reg.Evaluate(Action{Tags: []core.Tag{"violent:high"}}, bodySnapshot(nil, 1.0, 0, 0.9, 0)).CostMultiplier
	cold := reg.Evaluate(Action{Tags: []core.Tag{"violent:high"}}, bodySnapshot(nil, 1.0, 0, 0.0, 0)).CostMultiplier
	if hot >= cold {
		t.Errorf("higher adrenaline should lower cost: mult(hot)=%v mult(cold)=%v", hot, cold)
	}
}
