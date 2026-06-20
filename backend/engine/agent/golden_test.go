package agent

import (
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/actions"
	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/gates"
	"github.com/dogring/bdg/engine/needs"
	"github.com/dogring/bdg/engine/perception"
	"github.com/dogring/bdg/engine/planner"
	"github.com/dogring/bdg/engine/rng"
	"github.com/dogring/bdg/engine/spatial"
	"github.com/dogring/bdg/engine/stats"
	"github.com/dogring/bdg/engine/tom"
	"github.com/dogring/bdg/engine/values"
)

// ── Scenario B: starving pauper — theft becomes visible when urgency accumulates ──

// setupScenarioB builds registries and an agent for testing urgency-gated theft.
// The setup includes:
//   - Actions: Forage (honest food), StealFood (transgressive, produces has_food)
//   - Eat (consumes has_food)
//   - Conscience gate: blocks norm:transgressive unless Honesty<0.4 or Aggression>=0.65
//   - Agent with Honesty=0.6, Aggression=0.3 → StealFood is normally invisible
func setupScenarioB(t *testing.T) (testRegs, *Agent, *planner.Planner, Services) {
	t.Helper()

	statReg := mustLoadStats(t, testStatsYAML)

	// Needs: only Satiety (consumable) to keep the goal focused.
	needsYAML := `schema_version: 1
needs:
  - id: Satiety
    kind: consumable
    default: { posture: MaintainAbove, setpoint: 0.55, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
  - id: Hydration
    kind: consumable
    default: { posture: MaintainAbove, setpoint: 0.50, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
  - id: Rest
    kind: consumable
    default: { posture: MaintainAbove, setpoint: 0.45, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
`
	balanceYAML := `needs:
  Satiety: { decay_per_tick: 0.00070, satisfaction_threshold: 0.55 }
  Hydration: { decay_per_tick: 0.00110, satisfaction_threshold: 0.50 }
  Rest: { decay_per_tick: 0.00045, satisfaction_threshold: 0.45 }
values:
  weights:
    Satiety: 1.00
    Hydration: 1.05
    Rest: 0.85
`
	needReg, err := needs.Load(strings.NewReader(needsYAML), strings.NewReader(balanceYAML))
	if err != nil {
		t.Fatalf("needs.Load: %v", err)
	}

	valsCfg, err := values.Load(strings.NewReader(balanceYAML))
	if err != nil {
		t.Fatalf("values.Load: %v", err)
	}

	// Actions: Forage (honest), StealFood (transgressive), Eat (consumes food).
	actionsYAML := `schema_version: 1
actions:
  - id: Eat
    tags: [effort:low]
    duration: 6
    requires: [has_food]
    produces: [has_Satiety]
    effect: { Satiety: 0.5 }
  - id: Forage
    tags: [effort:med, uses:Agility]
    duration: 12
    produces: [has_food]
  - id: StealFood
    tags: [effort:low, norm:transgressive]
    duration: 8
    produces: [has_food]
`
	actReg, err := actions.Load(strings.NewReader(actionsYAML))
	if err != nil {
		t.Fatalf("actions.Load: %v", err)
	}

	// Gates: capability_floor and conscience.
	// conscience blocks norm:transgressive unless Honesty<0.4 or Aggression>=0.65.
	gatesYAML := `schema_version: 2
gates:
  - id: capability_floor
    tags: [uses:Agility, uses:Strength, uses:Intelligence]
    expr:
      and:
        - or: [{ not: { tag: "uses:Agility" } }, { stat: Agility, op: ">=", value: 0.15 }]
        - or: [{ not: { tag: "uses:Strength" } }, { stat: Strength, op: ">=", value: 0.15 }]
        - or: [{ not: { tag: "uses:Intelligence" } }, { stat: Intelligence, op: ">=", value: 0.15 }]
  - id: conscience
    tags: [norm:transgressive]
    expr:
      or:
        - { stat: Honesty, op: "<", value: 0.40 }
        - { stat: Aggression, op: ">=", value: 0.65 }
`
	gateReg, err := gates.Load(strings.NewReader(gatesYAML), statReg)
	if err != nil {
		t.Fatalf("gates.Load: %v", err)
	}

	regs := testRegs{
		actions: actReg,
		gates:   gateReg,
		needs:   needReg,
		stats:   statReg,
		values:  valsCfg,
	}

	// Planner with UrgencyThreshold = 0.65.
	plannerCfg := planner.PlannerConfig{
		Budget: planner.Budget{
			MaxDepth:   6,
			MaxActions: 16,
			MaxNodes:   256,
		},
		BaseHorizonTicks: 720,
		UrgencyThreshold: 0.65,
		TagCosts: map[core.Tag]float64{
			"effort:low":  0.20,
			"effort:med":  0.50,
			"effort:high": 0.90,
		},
	}
	pl := planner.New(actReg, gateReg, needReg, statReg, plannerCfg)

	// Agent with moderate Honesty (0.6) and low Aggression (0.3).
	// Conscience gate blocks: Honesty 0.6 >= 0.40 AND Aggression 0.3 < 0.65 → NOT visible.
	cfg := DefaultConfig()
	cfg.UrgencyFromDeficit = 1.4 // amplifies salience to urgency
	cfg.AdrTriggerUrgency = 0.65 // same as planner threshold, per balance.yaml

	// Create real stats with moderate Honesty and low Aggression.
	realStats := statReg.Defaults()
	realStats["Honesty"] = 0.6    // too honest for conscience gate
	realStats["Aggression"] = 0.3 // not aggressive enough
	realStats["Agility"] = 0.7    // enough for capability_floor
	realStats["Intelligence"] = 0.5

	selfToM := tom.NewToM("pauper", realStats, 0.5, rng.New(42), statReg, cfg.Rates)
	agent := New("pauper", core.Vec2{X: 10, Y: 10}, realStats, selfToM, cfg)

	// Set the agent to be STARVING: Satiety intensity very high.
	agent.NeedIntensities["Satiety"] = 0.80 // well above threshold 0.55

	// Services.
	sp := spatial.New(8.0)
	sensor := perception.NewSensor(sp, perception.PerceptionConfig{
		SightRadius: 18.0, SmellRadius: 10.0, HearingRadius: 14.0,
	})

	svc := Services{
		Sensor:  sensor,
		Planner: pl,
		Values:  valsCfg,
		Needs:   needReg,
		Stats:   statReg,
		Actions: actReg,
	}

	return regs, agent, pl, svc
}

// TestScenarioB_UrgencyRelaxesTheftGate is the golden snapshot for Scenario B.
// It verifies that when the agent is starving (high Satiety intensity → high Urgency),
// the planner's dynamic visibility relaxation makes a normally-gated transgressive
// action (StealFood) visible.
func TestScenarioB_UrgencyRelaxesTheftGate(t *testing.T) {
	_, agent, pl, svc := setupScenarioB(t)

	// ── Sanity check: agent has high Satiety need ─────────────────────────
	if agent.NeedIntensities["Satiety"] < 0.55 {
		t.Fatalf("expected starving agent (Satiety > 0.55), got %v", agent.NeedIntensities["Satiety"])
	}

	// ── Compute priorities (phase 3) ──────────────────────────────────────
	priorities := agent.appraise(svc.Needs, svc.Values)
	if len(priorities) == 0 {
		t.Fatal("expected non-empty priorities")
	}

	// ── Compute Urgency (as replan would) ─────────────────────────────────
	maxSalience := 0.0
	for _, p := range priorities {
		if float64(p.Salience) > maxSalience {
			maxSalience = float64(p.Salience)
		}
	}
	urgency := clamp01(maxSalience * agent.Cfg.UrgencyFromDeficit)

	// ── GOLDEN: Satiety intensity 0.80 vs threshold 0.55 ──────────────────
	// Standing = 1 - 0.80/0.55 = 1 - 1.4545... = clamped to 0
	// Salience = 1 - Standing = 1 - 0 = 1.0
	// Priority = 1.0 × 1.00 = 1.0
	// Urgency = 1.0 × 1.4 = 1.4, clamped to 1.0
	if urgency <= 0.65 {
		t.Errorf("GOLDEN: expected urgency > 0.65 (threshold), got %v", urgency)
	}
	t.Logf("GOLDEN Urgency: %v (exceeds 0.65 threshold)", urgency)

	// Expected salience for Satiety at intensity 0.80.
	satietyDef, _ := svc.Needs.Def("Satiety")
	standing := values.ComputeStanding(satietyDef, 0.80)
	salience := values.ComputeSalience(standing)
	t.Logf("GOLDEN Standing=%v Salience=%v for Satiety at intensity 0.80", standing, salience)

	// ── Build AgentSnapshot and call the planner ──────────────────────────
	selfBelief, _ := agent.ToM.Self(agent.ToM.SelfID())
	knownSet := map[core.ObjectID]struct{}{"food_stall": {}}
	snapshot := planner.AgentSnapshot{
		ID:              "pauper",
		Pos:             agent.Pos,
		CurrentStats:    agent.RealStats,
		SelfModel:       selfBelief,
		NeedIntensities: agent.NeedIntensities,
		Known:           knownSet,
		Urgency:         urgency,
	}

	plan, trace, err := pl.Plan(snapshot, priorities, rng.New(0))
	if err != nil {
		t.Fatalf("planner.Plan failed: %v", err)
	}

	// ── GOLDEN: Trace.Relaxed must be true ────────────────────────────────
	if !trace.Relaxed {
		t.Error("GOLDEN FAIL: expected Trace.Relaxed=true — urgency relaxation should make StealFood visible")
	}
	t.Logf("GOLDEN Trace.Relaxed: %v", trace.Relaxed)
	t.Logf("GOLDEN Plan.Actions: %v", plan.Actions)
	t.Logf("GOLDEN Trace.TotalCost: %v", trace.TotalCost)
	t.Logf("GOLDEN Trace.GoalDim: %v", trace.GoalDim)

	// ── GOLDEN: Plan must include StealFood (the cheaper transgressive option) ──
	foundSteal := false
	for _, a := range plan.Actions {
		if a == "StealFood" {
			foundSteal = true
			break
		}
	}
	if !foundSteal {
		t.Error("GOLDEN FAIL: expected plan to include StealFood (cheapest producer when relaxed)")
	}

	// ── GOLDEN: Verify the chosen candidate is recorded ───────────────────
	var chosenCandidate string
	for _, c := range trace.Candidates {
		if c.Chosen {
			chosenCandidate = string(c.Action)
			t.Logf("GOLDEN Candidate chosen: %s (cost=%v, visible=%v)", c.Action, c.Cost, c.Visible)
		}
	}
	if chosenCandidate == "" {
		t.Error("GOLDEN FAIL: no chosen candidate in trace")
	}
}

// TestScenarioB_LowUrgencyKeepsTheftGated verifies that when urgency is below the
// threshold, the transgressive action stays invisible.
func TestScenarioB_LowUrgencyKeepsTheftGated(t *testing.T) {
	_, agent, pl, svc := setupScenarioB(t)

	// Set Satiety to a comfortable level → low urgency.
	agent.NeedIntensities["Satiety"] = 0.20 // well below threshold 0.55

	priorities := agent.appraise(svc.Needs, svc.Values)
	maxSalience := 0.0
	for _, p := range priorities {
		if float64(p.Salience) > maxSalience {
			maxSalience = float64(p.Salience)
		}
	}
	urgency := clamp01(maxSalience * agent.Cfg.UrgencyFromDeficit)

	// GOLDEN: Urgency should be below threshold.
	if urgency > 0.65 {
		t.Errorf("GOLDEN: expected urgency <= 0.65 (threshold), got %v", urgency)
	}
	t.Logf("GOLDEN Low Urgency: %v (below 0.65 threshold)", urgency)

	selfBelief, _ := agent.ToM.Self(agent.ToM.SelfID())
	knownSet := map[core.ObjectID]struct{}{"food_stall": {}}
	snapshot := planner.AgentSnapshot{
		ID:              "pauper",
		Pos:             agent.Pos,
		CurrentStats:    agent.RealStats,
		SelfModel:       selfBelief,
		NeedIntensities: agent.NeedIntensities,
		Known:           knownSet,
		Urgency:         urgency,
	}

	plan, trace, err := pl.Plan(snapshot, priorities, rng.New(0))
	if err != nil {
		t.Fatalf("planner.Plan failed: %v", err)
	}

	// GOLDEN: Trace.Relaxed must be false.
	if trace.Relaxed {
		t.Error("GOLDEN FAIL: expected Trace.Relaxed=false — no relaxation at low urgency")
	}
	t.Logf("GOLDEN Trace.Relaxed: %v", trace.Relaxed)
	t.Logf("GOLDEN Plan.Actions: %v", plan.Actions)

	// GOLDEN: Plan must NOT include StealFood.
	for _, a := range plan.Actions {
		if a == "StealFood" {
			t.Error("GOLDEN FAIL: plan should NOT include StealFood at low urgency")
		}
	}
}

// ── Scenario F: dead-end goal — Intelligence gates rebinding vs fixation ───────

// setupScenarioF builds an agent with controllable perceived Intelligence for
// testing the coping cascade's Intelligence gate. The setup has an unreachable
// goal so the planner returns ErrUnreachable.
func setupScenarioF(t *testing.T, perceivedIntel float64) (*Agent, *stats.Registry, []planner.DimensionPriority) {
	t.Helper()

	statReg := mustLoadStats(t, testStatsYAML)

	// Create an agent whose perceived Intelligence is controlled.
	cfg := DefaultConfig()
	cfg.BudgetBase = 24
	cfg.BudgetPerIntelligence = 60

	realStats := statReg.Defaults()
	realStats["Intelligence"] = perceivedIntel // the REAL Intelligence (god view)
	realStats["Honesty"] = 0.5
	realStats["Aggression"] = 0.3

	// Use NewToM with a seed that produces negative noise for Intelligence
	// (the 4th stat alphabetically: Agility, Aggression, Honesty, Intelligence, Strength).
	// Seed=-198 → Intelligence NormFloat64() = -1.477... → clamped estMean = 0.
	// This ensures perceivedIntelligence = 0 for the low-intel tests.
	var rngSeed int64
	if perceivedIntel <= 0 {
		rngSeed = -198 // negative noise → clamped to 0
	} else {
		rngSeed = 100 // positive noise → clearly above 0
	}
	selfToM := tom.NewToM("test_agent", realStats, 0.5, rng.New(rngSeed), statReg, cfg.Rates)

	agent := New("test_agent", core.Vec2{}, realStats, selfToM, cfg)
	agent.Goal = "Craft"       // unreachable goal (no tool_bench known)
	agent.Coping = Idle

	// Priorities with Craft as the (dead-end) top goal.
	priorities := []planner.DimensionPriority{
		{Dim: "Craft", Priority: 0.70, Salience: 0.70},
		{Dim: "Satiety", Priority: 0.50, Salience: 0.50},
		{Dim: "Hydration", Priority: 0.40, Salience: 0.40},
		{Dim: "Rest", Priority: 0.30, Salience: 0.30},
	}

	return agent, statReg, priorities
}

// TestScenarioF_HighIntelligenceRebinds verifies that a high-Intelligence agent
// enters the Rebinding stage when facing a dead-end goal, and successfully
// substitutes to the next-highest-priority dimension.
func TestScenarioF_HighIntelligenceRebinds(t *testing.T) {
	// perceivedIntel = 50 (high) → canRebind = true.
	agent, statReg, priorities := setupScenarioF(t, 50)

	// ── Verify perceived Intelligence from ToM[self] (D8) ─────────────────
	perceivedIntel := agent.perceivedIntelligence(statReg)
	t.Logf("GOLDEN perceived Intelligence (from ToM[self]): %v", perceivedIntel)

	// ── Verify canRebind ──────────────────────────────────────────────────
	if !agent.canRebind(statReg) {
		t.Error("GOLDEN FAIL: high-Intelligence agent should be able to rebind")
	}

	// ── Enter coping cascade (failed planner call) ────────────────────────
	emit := core.NoopEmitter{}
	agent.enterCopingCascade(planner.ErrUnreachable, 1, priorities, statReg, emit)

	// ── GOLDEN: Agent should be in Idle after successful rebind ────────────
	t.Logf("GOLDEN Coping state after rebind: %v", agent.Coping)
	if agent.Coping != Idle {
		t.Errorf("GOLDEN FAIL: expected Coping=Idle after successful rebind, got %v", agent.Coping)
	}

	// ── GOLDEN: Goal should have been substituted to the second priority ───
	t.Logf("GOLDEN Goal after rebind: %v (was Craft, should be Satiety)", agent.Goal)
	if agent.Goal == "Craft" {
		t.Error("GOLDEN FAIL: goal should have been substituted away from Craft")
	}
	if agent.Goal != "Satiety" {
		t.Errorf("GOLDEN FAIL: expected substituted goal Satiety, got %v", agent.Goal)
	}

	// ── GOLDEN: Plan should be reset ──────────────────────────────────────
	if len(agent.Plan.Actions) != 0 {
		t.Error("GOLDEN FAIL: plan should be reset after rebind")
	}
}

// TestScenarioF_LowIntelligenceFixates verifies that a low-Intelligence agent
// skips Rebinding AND Longing (object-fixation, D8; P3 shortcut) and falls
// directly to Latent when facing a dead-end goal.
func TestScenarioF_LowIntelligenceFixates(t *testing.T) {
	// perceivedIntel = 0 (very low) → canRebind = false.
	agent, statReg, priorities := setupScenarioF(t, 0)

	// ── Verify perceived Intelligence from ToM[self] (D8) ─────────────────
	perceivedIntel := agent.perceivedIntelligence(statReg)
	t.Logf("GOLDEN perceived Intelligence (from ToM[self]): %v", perceivedIntel)

	// ── GOLDEN: canRebind must be false for zero-Intelligence agent ────────
	if agent.canRebind(statReg) {
		t.Error("GOLDEN FAIL: zero-Intelligence agent should NOT be able to rebind")
	}

	// Record pre-cascade latent count.
	latentBefore := len(agent.Latent)

	// ── Enter coping cascade (failed planner call) ────────────────────────
	emit := core.NoopEmitter{}
	agent.enterCopingCascade(planner.ErrUnreachable, 1, priorities, statReg, emit)

	// ── GOLDEN: P3: low-Intelligence goes directly to Latent (skips Rebinding+Longing) ──
	t.Logf("GOLDEN Coping state after cascade (low intel): %v", agent.Coping)
	if agent.Coping != Latent {
		t.Errorf("GOLDEN FAIL: expected Coping=Latent (P3 low-intel shortcut skips Rebinding+Longing), got %v", agent.Coping)
	}

	// ── GOLDEN: Goal should be cleared ────────────────────────────────────
	if agent.Goal != "" {
		t.Errorf("GOLDEN FAIL: goal should be cleared in Longing, got %v", agent.Goal)
	}

	// ── GOLDEN: A latent goal should have been created ────────────────────
	if len(agent.Latent) <= latentBefore {
		t.Error("GOLDEN FAIL: expected a new latent goal to be created")
	}
	for _, lg := range agent.Latent {
		t.Logf("GOLDEN LatentGoal: dim=%v since=%v intensity=%v", lg.Dim, lg.Since, lg.Intensity)
	}
}

// TestScenarioF_FullCascadeToApathy verifies that repeated failures advance the
// low-Intelligence agent through the full cascade: Longing → Latent → Apathy.
func TestScenarioF_FullCascadeToApathy(t *testing.T) {
	agent, statReg, priorities := setupScenarioF(t, 0)

	emit := core.NoopEmitter{}

	// Step 1: Idle → Latent directly (P3 low-intel shortcut skips Rebinding+Longing).
	agent.enterCopingCascade(planner.ErrUnreachable, 1, priorities, statReg, emit)
	if agent.Coping != Latent {
		t.Fatalf("expected Latent (P3 low-intel shortcut), got %v", agent.Coping)
	}
	t.Logf("Tick 1: Coping=%v", agent.Coping)

	// Step 2: Latent with FailStreak=1 (<3) → stays Latent.
	agent.Goal = "Craft"
	agent.enterCopingCascade(planner.ErrUnreachable, 2, priorities, statReg, emit)
	if agent.Coping != Latent {
		t.Fatalf("expected Latent (FailStreak=%d < threshold %d), got %v", agent.FailStreak, agent.Cfg.ApathyFailStreak, agent.Coping)
	}
	t.Logf("Tick 2: Coping=%v (Latent, FailStreak=%d)", agent.Coping, agent.FailStreak)

	// Step 3: Latent with FailStreak=2+1=3 >= 3 → Apathy.
	agent.Goal = "Craft"
	agent.enterCopingCascade(planner.ErrUnreachable, 3, priorities, statReg, emit)
	if agent.Coping != Apathy {
		t.Fatalf("expected Apathy (FailStreak=%d >= threshold %d), got %v", agent.FailStreak, agent.Cfg.ApathyFailStreak, agent.Coping)
	}
	t.Logf("Tick 3: Coping=%v (Apathy, FailStreak=%d)", agent.Coping, agent.FailStreak)

	// ── GOLDEN: Apathy should crash Adrenaline and clear latent goals ─────
	if agent.Adrenaline != 0 {
		t.Errorf("GOLDEN FAIL: Apathy should crash Adrenaline to 0, got %v", agent.Adrenaline)
	}
	if len(agent.Latent) != 0 {
		t.Errorf("GOLDEN FAIL: Apathy should clear latent goals, got %d", len(agent.Latent))
	}
	if agent.Goal != "" {
		t.Errorf("GOLDEN FAIL: Apathy should clear the goal, got %v", agent.Goal)
	}
}

// TestScenarioF_HighIntelligenceFallsToLongingOnSubstitutionFailure verifies
// that when rebinding succeeds in entering the rebind state but no substitute
// goal is available, the agent still falls through to Longing.
func TestScenarioF_HighIntelFallbackToLonging(t *testing.T) {
	agent, statReg, _ := setupScenarioF(t, 80)

	// Only one priority → rebind substitute fails (no second option).
	priorities := []planner.DimensionPriority{
		{Dim: "Craft", Priority: 0.70, Salience: 0.70},
	}

	emit := core.NoopEmitter{}
	agent.enterCopingCascade(planner.ErrUnreachable, 1, priorities, statReg, emit)

	// GOLDEN: Even though canRebind=true, no substitute available → falls to Longing.
	t.Logf("GOLDEN Coping after rebind with single priority: %v", agent.Coping)
	if agent.Coping != Longing {
		t.Errorf("GOLDEN FAIL: expected Longing (rebind failed, no substitute), got %v", agent.Coping)
	}
}
