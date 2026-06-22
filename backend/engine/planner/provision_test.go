package planner

import (
	"testing"

	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/rng"
)

// ── Scenario H: Forward-sim provisioning (D9) ───────────────────────────────
//
// An agent with high perceived Intelligence has a long horizon and predicts
// a setpoint breach for consumable needs → provisioning subgoal inserted.
// The same agent with low perceived Intelligence has a short horizon →
// no breach predicted → no provisioning inserted.

func TestForwardSimProvisionHighIntelligence(t *testing.T) {
	regs := makeTestRegs(t)
	pl := newPlanner(t, regs, defaultConfig())

	// High perceived Intelligence → long horizon (near BaseHorizonTicks=720)
	agent := defaultAgent()
	agent.SelfModel = beliefFromMeans(map[core.StatID]float64{"Intelligence": 100, "Strength": 100, "Honesty": 50})
	// Needs are partially depleted → at current rate, will breach within horizon
	agent.NeedIntensities = map[core.Dimension]float64{"Satiety": 0.3, "Hydration": 0.2, "Rest": 0.15}

	vals := []DimensionPriority{{Dim: "Satiety", Priority: 0.8, Salience: 0.8}}
	plan, trace, err := pl.Plan(agent, vals, rng.New(42))
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// High Intelligence (100) → max=100 → horizon = floor(720 * 1.0) = 720
	if plan.Horizon < 600 {
		t.Errorf("expected long horizon for high Intelligence, got %d", plan.Horizon)
	}

	// With horizon=720, Satiety demand = 0.00070*720 = 0.504
	// slack = 0.55 - 0.3 = 0.25 → deficit = 0.504 - 0.25 = 0.254 > 0
	// → provisioning should be inserted
	if len(trace.Provisioned) == 0 {
		t.Error("GOLDEN FAIL: high Intelligence should trigger provisioning (predicted deficit > 0)")
	}
	provisionedSatiety := false
	for _, d := range trace.Provisioned {
		if d == "Satiety" {
			provisionedSatiety = true
		}
	}
	if !provisionedSatiety {
		t.Errorf("expected Satiety to be provisioned, got %v", trace.Provisioned)
	}

	// Plan should have provisioning actions + goal action
	if len(plan.Actions) < 2 {
		t.Errorf("expected provisioning actions + goal, got %d actions: %v", len(plan.Actions), plan.Actions)
	}

	// Golden snapshot of the full plan
	t.Logf("GOLDEN Scenario-H (high Intel): horizon=%d provisioned=%v actions=%v cost=%.2f",
		plan.Horizon, trace.Provisioned, plan.Actions, trace.TotalCost)
}

func TestForwardSimNoProvisionLowIntelligence(t *testing.T) {
	regs := makeTestRegs(t)
	pl := newPlanner(t, regs, defaultConfig())

	// Low perceived Intelligence → 0/100 = 0.0 < 0.4 → hard skip → horizon = 0
	agent := defaultAgent()
	agent.SelfModel = beliefFromMeans(map[core.StatID]float64{"Intelligence": 0, "Strength": 100, "Honesty": 50})
	// Same need intensities as the high-Intelligence test
	agent.NeedIntensities = map[core.Dimension]float64{"Satiety": 0.3, "Hydration": 0.2, "Rest": 0.15}

	vals := []DimensionPriority{{Dim: "Satiety", Priority: 0.8, Salience: 0.8}}
	plan, trace, err := pl.Plan(agent, vals, rng.New(42))
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// Zero Intelligence → P5 hard skip → horizon = 0
	if plan.Horizon != 0 {
		t.Errorf("expected horizon=0 for Intelligence=0 (P5 hard skip), got %d", plan.Horizon)
	}

	// horizon=0 → forward-sim loop not entered → no provisioning
	if len(trace.Provisioned) > 0 {
		t.Errorf("GOLDEN FAIL: low Intelligence (hard skip) should NOT provision, got %v", trace.Provisioned)
	}

	t.Logf("GOLDEN Scenario-H (low Intel, P5 hard skip): horizon=%d provisioned=%v actions=%v",
		plan.Horizon, trace.Provisioned, plan.Actions)
}

func TestForwardSimMidIntelligence(t *testing.T) {
	regs := makeTestRegs(t)
	pl := newPlanner(t, regs, defaultConfig())

	// Half Intelligence → mid horizon
	agent := defaultAgent()
	agent.SelfModel = beliefFromMeans(map[core.StatID]float64{"Intelligence": 50, "Strength": 100, "Honesty": 50})
	agent.NeedIntensities = map[core.Dimension]float64{"Satiety": 0.5, "Hydration": 0.2, "Rest": 0.15}

	vals := []DimensionPriority{{Dim: "Satiety", Priority: 0.8, Salience: 0.8}}
	plan, trace, err := pl.Plan(agent, vals, rng.New(42))
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// Intelligence=50, max=100 → horizon = floor(720 * 0.5) = 360
	if plan.Horizon != 360 {
		t.Errorf("expected horizon=360 for half Intelligence, got %d", plan.Horizon)
	}

	// Satiety demand = 0.00070 * 360 = 0.252
	// slack = 0.55 - 0.5 = 0.05 → deficit = 0.252 - 0.05 = 0.202 > 0 → provisioned
	// Hydration demand = 0.00110 * 360 = 0.396
	// slack = 0.50 - 0.2 = 0.30 → deficit = 0.396 - 0.30 = 0.096 > 0 → provisioned
	// Rest demand = 0.00045 * 360 = 0.162
	// slack = 0.45 - 0.15 = 0.30 → deficit = 0.162 - 0.30 = -0.138 < 0 → NOT provisioned

	hasSatiety := false
	hasHydration := false
	hasRest := false
	for _, d := range trace.Provisioned {
		switch d {
		case "Satiety":
			hasSatiety = true
		case "Hydration":
			hasHydration = true
		case "Rest":
			hasRest = true
		}
	}

	if !hasSatiety {
		t.Error("Satiety should be provisioned (deficit > 0)")
	}
	if !hasHydration {
		t.Error("Hydration should be provisioned (deficit > 0)")
	}
	if hasRest {
		t.Error("Rest should NOT be provisioned (deficit < 0)")
	}

	t.Logf("GOLDEN Scenario-H (mid Intel): horizon=%d provisioned=%v actions=%v",
		plan.Horizon, trace.Provisioned, plan.Actions)
}

// ── Horizon formula tests ───────────────────────────────────────────────────

func TestIntelligenceHorizonZero(t *testing.T) {
	statReg := mustLoadStats(t, testStatsYAML)
	// 0/100 = 0.0 < 0.4 threshold → P5 hard skip → 0.
	horizon := computeHorizon(
		beliefFromMeans(map[core.StatID]float64{"Intelligence": 0}),
		"Intelligence", statReg, 720, 0.4,
	)
	if horizon != 0 {
		t.Errorf("expected horizon=0 (P5 hard skip, 0 < 0.4), got %d", horizon)
	}
}

func TestIntelligenceHorizonMax(t *testing.T) {
	statReg := mustLoadStats(t, testStatsYAML)
	// 100/100 = 1.0 >= 0.4 → gradual formula.
	horizon := computeHorizon(
		beliefFromMeans(map[core.StatID]float64{"Intelligence": 100}),
		"Intelligence", statReg, 720, 0.4,
	)
	if horizon != 720 {
		t.Errorf("expected horizon=720 for Intelligence=Max, got %d", horizon)
	}
}

func TestIntelligenceHorizonHalf(t *testing.T) {
	statReg := mustLoadStats(t, testStatsYAML)
	// 50/100 = 0.5 >= 0.4 → gradual formula.
	horizon := computeHorizon(
		beliefFromMeans(map[core.StatID]float64{"Intelligence": 50}),
		"Intelligence", statReg, 720, 0.4,
	)
	if horizon != 360 {
		t.Errorf("expected horizon=360 for half Intelligence, got %d", horizon)
	}
}

// ── P5 Lookahead threshold tests ────────────────────────────────────────────

func TestIntelligenceHorizonAtExactThreshold(t *testing.T) {
	statReg := mustLoadStats(t, testStatsYAML)
	// 40/100 = 0.4 >= 0.4 → gradual branch active, minimum 1.
	horizon := computeHorizon(
		beliefFromMeans(map[core.StatID]float64{"Intelligence": 40}),
		"Intelligence", statReg, 720, 0.4,
	)
	if horizon < 1 {
		t.Errorf("expected horizon >= 1 at exact threshold (0.4 >= 0.4), got %d", horizon)
	}
}

func TestIntelligenceHorizonJustBelowThreshold(t *testing.T) {
	statReg := mustLoadStats(t, testStatsYAML)
	// 39/100 = 0.39 < 0.4 → hard skip → 0.
	horizon := computeHorizon(
		beliefFromMeans(map[core.StatID]float64{"Intelligence": 39}),
		"Intelligence", statReg, 720, 0.4,
	)
	if horizon != 0 {
		t.Errorf("expected horizon=0 (P5 hard skip, 0.39 < 0.4), got %d", horizon)
	}
}

func TestIntelligenceHorizonWithCustomThreshold(t *testing.T) {
	statReg := mustLoadStats(t, testStatsYAML)
	// 50/100 = 0.5 < 0.6 → hard skip.
	horizon := computeHorizon(
		beliefFromMeans(map[core.StatID]float64{"Intelligence": 50}),
		"Intelligence", statReg, 720, 0.6,
	)
	if horizon != 0 {
		t.Errorf("expected horizon=0 (0.5 < 0.6 threshold), got %d", horizon)
	}
}

// ── Forward-sim math tests ──────────────────────────────────────────────────

func TestForwardSimProvisionMath(t *testing.T) {
	// Test the pure math: predicted_deficit = Rate * horizon - (threshold - intensity)
	// Satiety: Rate=0.00070, threshold=0.55, intensity=0.3, horizon=720
	// current_slack = 0.55 - 0.3 = 0.25
	// predicted_deficit = 0.00070 * 720 - 0.25 = 0.504 - 0.25 = 0.254 > 0 → provision
	regs := makeTestRegs(t)
	pl := newPlanner(t, regs, defaultConfig())

	agent := defaultAgent()
	agent.SelfModel = beliefFromMeans(map[core.StatID]float64{"Intelligence": 100, "Strength": 100, "Honesty": 50})
	agent.NeedIntensities = map[core.Dimension]float64{"Satiety": 0.3}

	vals := []DimensionPriority{{Dim: "Satiety", Priority: 0.8, Salience: 0.8}}
	_, trace, err := pl.Plan(agent, vals, rng.New(42))
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	found := false
	for _, d := range trace.Provisioned {
		if d == "Satiety" {
			found = true
		}
	}
	if !found {
		t.Errorf("Satiety should be provisioned: deficit=0.254 > 0, got %v", trace.Provisioned)
	}
}

// ── P5: Scenario H integration ──────────────────────────────────────────────

func TestScenarioH_LowIntelSkipsProvisioning(t *testing.T) {
	// Low-Intel agent (perceivedIntelligence = 0.35 < 0.40):
	// Should get NO provisioning subgoal inserted.
	regs := makeTestRegs(t)
	pl := newPlanner(t, regs, defaultConfig())

	agent := defaultAgent()
	agent.SelfModel = beliefFromMeans(map[core.StatID]float64{"Intelligence": 35, "Strength": 100, "Honesty": 50})
	agent.NeedIntensities = map[core.Dimension]float64{"Satiety": 0.3, "Hydration": 0.2, "Rest": 0.15}

	vals := []DimensionPriority{{Dim: "Satiety", Priority: 0.8, Salience: 0.8}}
	plan, trace, err := pl.Plan(agent, vals, rng.New(42))
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// 35/100 = 0.35 < 0.4 → hard skip → horizon = 0, no provisioning
	if plan.Horizon != 0 {
		t.Errorf("expected horizon=0 (0.35 < 0.4), got %d", plan.Horizon)
	}
	if len(trace.Provisioned) > 0 {
		t.Errorf("P5 Scenario H: low Intel should NOT provision, got %v", trace.Provisioned)
	}
	t.Logf("P5 H (low): horizon=%d provisioned=%v actions=%v", plan.Horizon, trace.Provisioned, plan.Actions)
}

func TestScenarioH_HighIntelInsertsProvisioning(t *testing.T) {
	// High-Intel agent (perceivedIntelligence = 0.75 >= 0.40):
	// Should get provisioning subgoal inserted.
	regs := makeTestRegs(t)
	pl := newPlanner(t, regs, defaultConfig())

	agent := defaultAgent()
	agent.SelfModel = beliefFromMeans(map[core.StatID]float64{"Intelligence": 75, "Strength": 100, "Honesty": 50})
	agent.NeedIntensities = map[core.Dimension]float64{"Satiety": 0.3, "Hydration": 0.2, "Rest": 0.15}

	vals := []DimensionPriority{{Dim: "Satiety", Priority: 0.8, Salience: 0.8}}
	plan, trace, err := pl.Plan(agent, vals, rng.New(42))
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// 75/100 = 0.75 >= 0.4 → gradual formula active.
	if plan.Horizon < 500 {
		t.Errorf("expected long horizon for high Intel, got %d", plan.Horizon)
	}
	// With sufficient horizon, Satiety should be provisioned.
	hasSatiety := false
	for _, d := range trace.Provisioned {
		if d == "Satiety" {
			hasSatiety = true
		}
	}
	if !hasSatiety {
		t.Errorf("P5 Scenario H: high Intel should provision Satiety, got %v", trace.Provisioned)
	}
	t.Logf("P5 H (high): horizon=%d provisioned=%v actions=%v", plan.Horizon, trace.Provisioned, plan.Actions)
}
