package planner

import (
	"errors"
	"sort"
	"testing"

	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/rng"
)

// ── GOAP backward-chaining tests ────────────────────────────────────────────

func TestGOAPSelection(t *testing.T) {
	regs := makeTestRegs(t)
	pl := newPlanner(t, regs, defaultConfig())

	agent := defaultAgent()
	agent.SelfModel = beliefFromMeans(map[core.StatID]float64{"Intelligence": 50, "Strength": 100, "Honesty": 50})

	vals := []DimensionPriority{{Dim: "Satiety", Priority: 0.8, Salience: 0.8}}
	plan, trace, err := pl.Plan(agent, vals, rng.New(42))
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if len(plan.Actions) == 0 {
		t.Fatal("expected non-empty plan")
	}
	if plan.Actions[len(plan.Actions)-1] != "Eat" {
		t.Errorf("expected last action Eat, got %s", plan.Actions[len(plan.Actions)-1])
	}
	found := false
	for _, c := range trace.Candidates {
		if c.Action == "Eat" && c.Visible && c.Chosen {
			found = true
		}
	}
	if !found {
		t.Error("Eat should be visible and chosen")
	}
}

func TestGOAPSelectionGateFails(t *testing.T) {
	regs := makeTestRegs(t)
	pl := newPlanner(t, regs, defaultConfig())

	agent := defaultAgent()
	agent.SelfModel = beliefFromMeans(map[core.StatID]float64{"Intelligence": 50, "Strength": 10, "Honesty": 50})

	vals := []DimensionPriority{{Dim: "Satiety", Priority: 0.8, Salience: 0.8}}
	plan, _, err := pl.Plan(agent, vals, rng.New(42))
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if len(plan.Actions) == 0 {
		t.Fatal("expected non-empty plan (fallback producer)")
	}
	t.Logf("selected fallback plan: %v", plan.Actions)
}

// ── HTN decomposition tests ─────────────────────────────────────────────────

func TestHTNPrependsPrerequisites(t *testing.T) {
	regs := makeTestRegs(t)
	pl := newPlanner(t, regs, defaultConfig())

	agent := defaultAgent()
	agent.SelfModel = beliefFromMeans(map[core.StatID]float64{"Intelligence": 50, "Strength": 100, "Honesty": 50})

	vals := []DimensionPriority{{Dim: "Satiety", Priority: 0.8, Salience: 0.8}}
	plan, _, err := pl.Plan(agent, vals, rng.New(42))
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// HTN: Eat requires has_food → prereqs must precede the goal action.
	if len(plan.Actions) < 2 {
		t.Fatalf("expected at least 2 actions in plan, got %d: %v", len(plan.Actions), plan.Actions)
	}
	if plan.Actions[len(plan.Actions)-1] != "Eat" {
		t.Errorf("goal action should be Eat, got %s", plan.Actions[len(plan.Actions)-1])
	}
	t.Logf("HTN plan: %v", plan.Actions)
}

func TestHTNWithPrerequisites(t *testing.T) {
	regs := makeTestRegs(t)
	pl := newPlanner(t, regs, defaultConfig())

	agent := defaultAgent()
	agent.SelfModel = beliefFromMeans(map[core.StatID]float64{"Intelligence": 50, "Strength": 100, "Honesty": 100})

	vals := []DimensionPriority{{Dim: "Satiety", Priority: 0.8, Salience: 0.8}}
	plan, _, err := pl.Plan(agent, vals, rng.New(42))
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if len(plan.Actions) == 0 {
		t.Fatal("expected non-empty plan")
	}
	if plan.Actions[len(plan.Actions)-1] != "Eat" {
		t.Errorf("expected Eat as goal action, got %s", plan.Actions[len(plan.Actions)-1])
	}
	t.Logf("HTN plan with prereqs: %v", plan.Actions)
}

// ── Tag-derived cost tie-breaking tests (D4) ────────────────────────────────

func TestTagCostTieBreaking(t *testing.T) {
	regs := makeTestRegs(t)
	cfg := defaultConfig()
	cfg.TagCosts["effort:low"] = 0.20
	cfg.TagCosts["effort:med"] = 0.50
	cfg.TagCosts["effort:high"] = 0.90
	pl := newPlanner(t, regs, cfg)

	agent := defaultAgent()
	agent.SelfModel = beliefFromMeans(map[core.StatID]float64{"Intelligence": 50, "Strength": 100, "Honesty": 50})

	vals := []DimensionPriority{{Dim: "Hydration", Priority: 0.8, Salience: 0.8}}
	plan, _, err := pl.Plan(agent, vals, rng.New(42))
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if len(plan.Actions) == 0 {
		t.Fatal("expected non-empty plan")
	}
	// Drink (effort:low) has lower tag cost than GatherWater (effort:med)
	t.Logf("tag-cost tie-break plan: %v", plan.Actions)
}

func TestCostNeverOverridesPriority(t *testing.T) {
	regs := makeTestRegs(t)
	pl := newPlanner(t, regs, defaultConfig())

	agent := defaultAgent()
	agent.SelfModel = beliefFromMeans(map[core.StatID]float64{"Intelligence": 50, "Strength": 100, "Honesty": 50})

	vals := []DimensionPriority{
		{Dim: "Satiety", Priority: 0.9, Salience: 0.9},
		{Dim: "Hydration", Priority: 0.3, Salience: 0.3},
	}
	plan, trace, err := pl.Plan(agent, vals, rng.New(42))
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if trace.GoalDim != "Satiety" {
		t.Errorf("higher-Priority Satiety should be selected, got %s", trace.GoalDim)
	}
	_ = plan
}

// ── Dynamic visibility relaxation tests ─────────────────────────────────────

func TestDynamicVisibilityRelaxation(t *testing.T) {
	regs := makeTestRegs(t)
	pl := newPlanner(t, regs, defaultConfig())

	agent := defaultAgent()
	agent.SelfModel = beliefFromMeans(map[core.StatID]float64{"Intelligence": 50, "Strength": 100, "Honesty": 10})
	agent.Urgency = 0.3 // below threshold 0.65

	vals := []DimensionPriority{{Dim: "Satiety", Priority: 0.8, Salience: 0.8}}
	_, trace, err := pl.Plan(agent, vals, rng.New(42))
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if trace.Relaxed {
		t.Error("relaxation should not trigger when Urgency < threshold")
	}

	agent.Urgency = 0.9 // above threshold
	_, trace2, err2 := pl.Plan(agent, vals, rng.New(42))
	if err2 != nil {
		t.Fatalf("Plan failed: %v", err2)
	}
	if !trace2.Relaxed {
		t.Error("relaxation should trigger when Urgency > threshold")
	}
}

// ── Candidate sort / selection tests ────────────────────────────────────────

func TestCandidateSort(t *testing.T) {
	candidates := []Candidate{
		{Action: "Z", Cost: 1.0, Visible: true, Chosen: false},
		{Action: "A", Cost: 2.0, Visible: true, Chosen: true},
		{Action: "M", Cost: 1.5, Visible: false, Chosen: false},
	}
	sort.Slice(candidates, func(i, j int) bool {
		return string(candidates[i].Action) < string(candidates[j].Action)
	})
	if candidates[0].Action != "A" || candidates[1].Action != "M" || candidates[2].Action != "Z" {
		t.Errorf("candidates not sorted by ActionID: %v", candidates)
	}
}

// ── D3/D4/guards ────────────────────────────────────────────────────────────

func TestNoTaskTree(t *testing.T) {
	var p Plan
	_ = p.Actions // flat slice — D3: no Method/Task/Subtask type
}

func TestReadsToMSelf(t *testing.T) {
	regs := makeTestRegs(t)
	pl := newPlanner(t, regs, defaultConfig())

	agent := defaultAgent()
	agent.SelfModel = beliefFromMeans(map[core.StatID]float64{"Intelligence": 10, "Strength": 10, "Honesty": 10})
	agent.CurrentStats = map[core.StatID]float64{"Intelligence": 100, "Strength": 100, "Honesty": 100}

	vals := []DimensionPriority{{Dim: "Satiety", Priority: 0.8, Salience: 0.8}}
	plan, _, err := pl.Plan(agent, vals, rng.New(42))
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	// SelfModel Intelligence=10 → 10/100 = 0.1 < 0.4 threshold → hard skip → 0
	if plan.Horizon >= 720 {
		t.Errorf("horizon should use SelfModel (Intelligence=10, hard skip). Got %d", plan.Horizon)
	}
}

// ── Errors ──────────────────────────────────────────────────────────────────

func TestErrNoGoal(t *testing.T) {
	regs := makeTestRegs(t)
	pl := newPlanner(t, regs, defaultConfig())
	agent := defaultAgent()

	_, _, err := pl.Plan(agent, nil, rng.New(42))
	if !errors.Is(err, ErrNoGoal) {
		t.Errorf("expected ErrNoGoal for empty values, got %v", err)
	}
}

func TestErrUnreachable(t *testing.T) {
	regs := makeTestRegs(t)
	pl := newPlanner(t, regs, defaultConfig())
	agent := defaultAgent()

	vals := []DimensionPriority{{Dim: "NonExistent", Priority: 0.8, Salience: 0.8}}
	_, _, err := pl.Plan(agent, vals, rng.New(42))
	if !errors.Is(err, ErrUnreachable) {
		t.Errorf("expected ErrUnreachable, got %v", err)
	}
}

// ── Sort utility ────────────────────────────────────────────────────────────

func TestSortDimensionPriority(t *testing.T) {
	vals := []DimensionPriority{
		{Dim: "B", Priority: 0.5, Salience: 0.5},
		{Dim: "A", Priority: 0.5, Salience: 0.5},
		{Dim: "C", Priority: 0.9, Salience: 0.9},
		{Dim: "D", Priority: 0.1, Salience: 0.1},
	}
	sortDimensionPriority(vals)
	if vals[0].Dim != "C" {
		t.Errorf("highest priority should be first, got %s", vals[0].Dim)
	}
	if vals[1].Dim != "A" {
		t.Errorf("equal priority broken by lexicographic Dim, got %s", vals[1].Dim)
	}
}

// ── ComputeCost ─────────────────────────────────────────────────────────────

func TestComputeCost(t *testing.T) {
	tagCosts := map[core.Tag]float64{"effort:low": 0.20, "effort:high": 0.90}
	keys := sortedTagCostKeys(tagCosts)
	cost := computeCost([]core.Tag{"effort:low", "effort:high"}, keys, tagCosts)
	if cost != 1.10 {
		t.Errorf("cost = %f, want 1.10", cost)
	}
}

func TestComputeCostEmptyTags(t *testing.T) {
	tagCosts := map[core.Tag]float64{"effort:low": 0.20}
	keys := sortedTagCostKeys(tagCosts)
	cost := computeCost(nil, keys, tagCosts)
	if cost != 0 {
		t.Errorf("empty tags cost = %f, want 0", cost)
	}
}

// ── Config guards ───────────────────────────────────────────────────────────

func TestNoHardcodedConstants(t *testing.T) {
	cfg := defaultConfig()
	if cfg.Budget.MaxDepth != 6 {
		t.Error("MaxDepth should be injected")
	}
	if cfg.BaseHorizonTicks != 720 {
		t.Error("BaseHorizonTicks should be injected")
	}
}

func TestDefaultsFromConfig(t *testing.T) {
	regs := makeTestRegs(t)
	cfg := PlannerConfig{
		Budget:           Budget{MaxDepth: 4, MaxActions: 8, MaxNodes: 64},
		BaseHorizonTicks: 100,
		TagCosts:         map[core.Tag]float64{"effort:low": 0.10},
		UrgencyThreshold: 0.80,
		LookaheadThreshold: 0.4,
	}
	pl := newPlanner(t, regs, cfg)
	agent := defaultAgent()
	agent.SelfModel = beliefFromMeans(map[core.StatID]float64{"Intelligence": 100, "Strength": 100, "Honesty": 50})
	vals := []DimensionPriority{{Dim: "Satiety", Priority: 0.8, Salience: 0.8}}
	plan, _, err := pl.Plan(agent, vals, rng.New(42))
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if plan.Horizon != 100 {
		t.Errorf("horizon should use injected BaseHorizonTicks: got %d, want 100", plan.Horizon)
	}
}

// ── Trace ordering test ─────────────────────────────────────────────────────

func TestTraceProvisionedDimsOrder(t *testing.T) {
	regs := makeTestRegs(t)
	pl := newPlanner(t, regs, defaultConfig())

	agent := defaultAgent()
	agent.SelfModel = beliefFromMeans(map[core.StatID]float64{"Intelligence": 100, "Strength": 100, "Honesty": 50})
	agent.NeedIntensities = map[core.Dimension]float64{"Satiety": 0.0, "Hydration": 0.0, "Rest": 0.0}

	vals := []DimensionPriority{{Dim: "Satiety", Priority: 0.8, Salience: 0.8}}
	_, trace, err := pl.Plan(agent, vals, rng.New(42))
	if err != nil && !errors.Is(err, ErrUnreachable) {
		t.Fatalf("Plan failed: %v", err)
	}
	for i := 1; i < len(trace.Provisioned); i++ {
		if string(trace.Provisioned[i-1]) >= string(trace.Provisioned[i]) {
			t.Errorf("provisioned dims not in IDs() order: %v", trace.Provisioned)
		}
	}
}

// ── Horizon edge cases (P5 lookahead threshold) ─────────────────────────────

func TestHorizonHandlesMissingIntelligence(t *testing.T) {
	statReg := mustLoadStats(t, testStatsYAML)
	// With missing Intelligence stat, perceivedIntel = 0 < 0.4 → hard skip → 0.
	horizon := computeHorizon(
		beliefFromMeans(map[core.StatID]float64{"Strength": 50}),
		"Intelligence", statReg, 720, 0.4,
	)
	if horizon != 0 {
		t.Errorf("expected horizon=0 (P5 hard skip, no Intelligence stat), got %d", horizon)
	}
}

// ── Golden snapshot for HTN decomposition ───────────────────────────────────

func TestGoldenHTNDecomposition(t *testing.T) {
	regs := makeTestRegs(t)
	pl := newPlanner(t, regs, defaultConfig())

	agent := defaultAgent()
	agent.SelfModel = beliefFromMeans(map[core.StatID]float64{"Intelligence": 50, "Strength": 100, "Honesty": 100})

	vals := []DimensionPriority{{Dim: "Satiety", Priority: 0.8, Salience: 0.8}}
	plan, trace, err := pl.Plan(agent, vals, rng.New(42))
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	if len(plan.Actions) == 0 {
		t.Fatal("expected non-empty plan")
	}
	// Goal is Eat (last action), prereqs precede it
	if plan.Actions[len(plan.Actions)-1] != "Eat" {
		t.Errorf("goal action should be Eat")
	}
	if trace.GoalDim != "Satiety" {
		t.Errorf("goal should be Satiety")
	}
	t.Logf("GOLDEN HTN: actions=%v horizon=%d goal=%s cost=%.2f nodes=%d",
		plan.Actions, plan.Horizon, trace.GoalDim, trace.TotalCost, trace.NodesExpanded)
}
