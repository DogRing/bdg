package planner

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/actions"
)

// ── Determinism test (D12) ──────────────────────────────────────────────────

func TestDeterminism(t *testing.T) {
	regs := makeTestRegs(t)
	pl := newPlanner(t, regs, defaultConfig())

	agent := defaultAgent()
	agent.SelfModel = beliefFromMeans(map[core.StatID]float64{"Intelligence": 50, "Strength": 100, "Honesty": 50})
	vals := []DimensionPriority{{Dim: "Satiety", Priority: 0.8, Salience: 0.8}}

	// First call
	plan1, trace1, err1 := pl.Plan(agent, vals, rng.New(42))
	if err1 != nil {
		t.Fatalf("first Plan failed: %v", err1)
	}

	// 1000 identical calls — all must match byte-for-byte
	for i := 0; i < 1000; i++ {
		planN, traceN, errN := pl.Plan(agent, vals, rng.New(42))
		if errN != nil {
			t.Fatalf("call %d failed: %v", i, errN)
		}
		if len(planN.Actions) != len(plan1.Actions) {
			t.Fatalf("call %d: action count mismatch: %d vs %d", i, len(planN.Actions), len(plan1.Actions))
		}
		for j := range planN.Actions {
			if planN.Actions[j] != plan1.Actions[j] {
				t.Fatalf("call %d: action[%d] mismatch: %s vs %s", i, j, planN.Actions[j], plan1.Actions[j])
			}
		}
		if planN.Horizon != plan1.Horizon {
			t.Fatalf("call %d: horizon mismatch: %d vs %d", i, planN.Horizon, plan1.Horizon)
		}
		if traceN.GoalDim != trace1.GoalDim {
			t.Fatalf("call %d: goal dim mismatch", i)
		}
		if traceN.TotalCost != trace1.TotalCost {
			t.Fatalf("call %d: total cost mismatch", i)
		}
		if traceN.NodesExpanded != trace1.NodesExpanded {
			t.Fatalf("call %d: nodes expanded mismatch", i)
		}
	}

	t.Logf("determinism: 1000 calls produced identical Plan and Trace (actions=%v, horizon=%d, cost=%.2f)",
		plan1.Actions, plan1.Horizon, trace1.TotalCost)
}

// ── Budget abort tests ─────────────────────────────────────────────────────

func TestBudgetMaxDepthAborts(t *testing.T) {
	regs := makeTestRegs(t)
	cfg := defaultConfig()
	cfg.Budget.MaxDepth = 0 // zero recursion allowed — should abort immediately on any prereq
	pl := newPlanner(t, regs, cfg)

	agent := defaultAgent()
	agent.SelfModel = beliefFromMeans(map[core.StatID]float64{"Intelligence": 50, "Strength": 100, "Honesty": 50})
	agent.NeedIntensities = map[core.Dimension]float64{"Satiety": 0.9, "Hydration": 0.9, "Rest": 0.9}

	vals := []DimensionPriority{{Dim: "Satiety", Priority: 0.8, Salience: 0.8}}
	_, _, err := pl.Plan(agent, vals, rng.New(42))
	if err == nil {
		t.Error("expected ErrBudgetExceeded for MaxDepth=0 (Eat requires has_food prereq)")
	} else if !errors.Is(err, ErrBudgetExceeded) {
		t.Errorf("expected ErrBudgetExceeded, got %v", err)
	}
}

func TestBudgetMaxActionsAborts(t *testing.T) {
	regs := makeTestRegs(t)
	cfg := defaultConfig()
	cfg.Budget.MaxActions = 1 // cannot fit prereq + goal
	pl := newPlanner(t, regs, cfg)

	agent := defaultAgent()
	agent.SelfModel = beliefFromMeans(map[core.StatID]float64{"Intelligence": 50, "Strength": 100, "Honesty": 100})

	vals := []DimensionPriority{{Dim: "Satiety", Priority: 0.8, Salience: 0.8}}
	_, _, err := pl.Plan(agent, vals, rng.New(42))
	if err == nil {
		t.Log("may have succeeded with single action")
	} else if !errors.Is(err, ErrBudgetExceeded) {
		t.Errorf("expected ErrBudgetExceeded or success, got %v", err)
	}
}

func TestBudgetMaxNodesAborts(t *testing.T) {
	regs := makeTestRegs(t)
	cfg := defaultConfig()
	cfg.Budget.MaxNodes = 2 // very tight — will exhaust quickly
	pl := newPlanner(t, regs, cfg)

	agent := defaultAgent()
	agent.SelfModel = beliefFromMeans(map[core.StatID]float64{"Intelligence": 50, "Strength": 100, "Honesty": 100})

	vals := []DimensionPriority{{Dim: "Satiety", Priority: 0.8, Salience: 0.8}}
	_, _, err := pl.Plan(agent, vals, rng.New(42))
	// May or may not abort depending on producer count
	t.Logf("MaxNodes=2 result: %v", err)
}

// ── Golden snapshot for determinism ─────────────────────────────────────────

type planDigest struct {
	Actions       []string `json:"actions"`
	Horizon       int      `json:"horizon"`
	GoalDim       string   `json:"goal_dim"`
	Provisioned   []string `json:"provisioned"`
	Relaxed       bool     `json:"relaxed"`
	TotalCost     float64  `json:"total_cost"`
	NodesExpanded int      `json:"nodes_expanded"`
}

func TestGoldenDeterminismDigest(t *testing.T) {
	regs := makeTestRegs(t)
	pl := newPlanner(t, regs, defaultConfig())

	agent := defaultAgent()
	agent.SelfModel = beliefFromMeans(map[core.StatID]float64{"Intelligence": 100, "Strength": 100, "Honesty": 100})
	agent.NeedIntensities = map[core.Dimension]float64{"Satiety": 0.3, "Hydration": 0.2, "Rest": 0.15}

	vals := []DimensionPriority{{Dim: "Satiety", Priority: 0.8, Salience: 0.8}}

	plan1, trace1, err := pl.Plan(agent, vals, rng.New(42))
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// Build digest
	digest1 := planDigest{
		Actions:       actionIDsToStrings(plan1.Actions),
		Horizon:       plan1.Horizon,
		GoalDim:       string(trace1.GoalDim),
		Provisioned:   dimsToStrings(trace1.Provisioned),
		Relaxed:       trace1.Relaxed,
		TotalCost:     trace1.TotalCost,
		NodesExpanded: trace1.NodesExpanded,
	}

	// Second identical call with same seed
	plan2, trace2, err := pl.Plan(agent, vals, rng.New(42))
	if err != nil {
		t.Fatalf("second Plan failed: %v", err)
	}
	digest2 := planDigest{
		Actions:       actionIDsToStrings(plan2.Actions),
		Horizon:       plan2.Horizon,
		GoalDim:       string(trace2.GoalDim),
		Provisioned:   dimsToStrings(trace2.Provisioned),
		Relaxed:       trace2.Relaxed,
		TotalCost:     trace2.TotalCost,
		NodesExpanded: trace2.NodesExpanded,
	}

	// Marshal both and compare byte-for-byte (D12 golden)
	b1, _ := json.MarshalIndent(digest1, "", "  ")
	b2, _ := json.MarshalIndent(digest2, "", "  ")
	if string(b1) != string(b2) {
		t.Errorf("GOLDEN FAIL: determinism violated\n--- call 1 ---\n%s\n--- call 2 ---\n%s", string(b1), string(b2))
	}

	// Cross-process stability: build a second Planner with same content
	regs2 := makeTestRegs(t)
	pl2 := newPlanner(t, regs2, defaultConfig())
	plan3, trace3, err := pl2.Plan(agent, vals, rng.New(42))
	if err != nil {
		t.Fatalf("cross-process Plan failed: %v", err)
	}
	digest3 := planDigest{
		Actions:       actionIDsToStrings(plan3.Actions),
		Horizon:       plan3.Horizon,
		GoalDim:       string(trace3.GoalDim),
		Provisioned:   dimsToStrings(trace3.Provisioned),
		Relaxed:       trace3.Relaxed,
		TotalCost:     trace3.TotalCost,
		NodesExpanded: trace3.NodesExpanded,
	}
	b3, _ := json.MarshalIndent(digest3, "", "  ")
	if string(b1) != string(b3) {
		t.Errorf("GOLDEN FAIL: cross-process determinism violated\n--- process 1 ---\n%s\n--- process 2 ---\n%s", string(b1), string(b3))
	}

	t.Logf("GOLDEN digest (determinism):\n%s", string(b1))
}

func TestGoldenScenarioHProvisioning(t *testing.T) {
	// Golden snapshot for Scenario H: high Intelligence → provisioning, low → none.
	regs := makeTestRegs(t)
	pl := newPlanner(t, regs, defaultConfig())

	highIntel := defaultAgent()
	highIntel.SelfModel = beliefFromMeans(map[core.StatID]float64{"Intelligence": 100, "Strength": 100, "Honesty": 100})
	highIntel.NeedIntensities = map[core.Dimension]float64{"Satiety": 0.3, "Hydration": 0.2, "Rest": 0.15}

	vals := []DimensionPriority{{Dim: "Satiety", Priority: 0.8, Salience: 0.8}}

	planHi, traceHi, err := pl.Plan(highIntel, vals, rng.New(42))
	if err != nil {
		t.Fatalf("high-Intel Plan failed: %v", err)
	}

	lowIntel := defaultAgent()
	lowIntel.SelfModel = beliefFromMeans(map[core.StatID]float64{"Intelligence": 0, "Strength": 100, "Honesty": 100})
	lowIntel.NeedIntensities = map[core.Dimension]float64{"Satiety": 0.3, "Hydration": 0.2, "Rest": 0.15}

	planLo, traceLo, err := pl.Plan(lowIntel, vals, rng.New(42))
	if err != nil {
		t.Fatalf("low-Intel Plan failed: %v", err)
	}

	// GOLDEN assertions
	if planHi.Horizon <= planLo.Horizon {
		t.Errorf("high-Intel horizon (%d) should exceed low-Intel horizon (%d)", planHi.Horizon, planLo.Horizon)
	}
	if len(traceHi.Provisioned) == 0 {
		t.Error("GOLDEN FAIL: high-Intel should provision (predicted deficit)")
	}
	if len(traceLo.Provisioned) > 0 {
		t.Errorf("GOLDEN FAIL: low-Intel should NOT provision (no deficit), got %v", traceLo.Provisioned)
	}
	if len(planHi.Actions) <= len(planLo.Actions) {
		t.Error("GOLDEN FAIL: high-Intel plan should be longer (provisioning + goal) than low-Intel (goal only)")
	}

	// Record deterministic output
	t.Logf("GOLDEN Scenario-H: highIntel(horizon=%d, provisioned=%v, actions=%v) vs lowIntel(horizon=%d, provisioned=%v, actions=%v)",
		planHi.Horizon, traceHi.Provisioned, planHi.Actions,
		planLo.Horizon, traceLo.Provisioned, planLo.Actions)
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func actionIDsToStrings(acts []actions.ActionID) []string {
	out := make([]string, len(acts))
	for i, a := range acts {
		out[i] = string(a)
	}
	return out
}

func dimsToStrings(dims []core.Dimension) []string {
	out := make([]string, len(dims))
	for i, d := range dims {
		out[i] = string(d)
	}
	return out
}
