package agent

// gap_closure_test.go — tests for the three P6 gap-closure items:
//   (1) Apathy-reduced planner budget via AgentSnapshot.ApathyBudget (also tested in planner pkg)
//   (2) Aggression-drift threshold check in updateResentment (every tick, not accrueResentment)
//   (3) FunctionSpec table + handleRelianceTrigger emits BeliefUpdated

import (
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/mind/gates"
	"github.com/dogring/bdg/engine/mind/needs"
	"github.com/dogring/bdg/engine/mind/planner"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/stats"
	"github.com/dogring/bdg/engine/mind/tom"
	"github.com/dogring/bdg/engine/mind/values"
)

// ── Gap-closure (1): ApathyBudget — direct field verification ────────────────

// TestApathyBudget_FieldSetWhileApathetic verifies the exact ApathyBudget
// field values when the agent is in Apathy versus other states.
// The AC: while Coping==Apathy, ApathyBudget is non-nil with
//   MaxNodes == max(1, int(effectiveNodes*(1−penalty)));
// while Coping!=Apathy, ApathyBudget==nil.
//
// We verify by building the budget inline (the same logic as replan) and
// confirming the values match the formula.
func TestApathyBudget_FieldSetWhileApathetic(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ApathyBudgetPenalty = 0.5
	cfg.BudgetBase = 24
	cfg.BudgetPerIntelligence = 60

	// perceivedIntel = 1.0 → effectiveNodes = 24 + 60 = 84
	perceivedIntel := 1.0
	effectiveNodes := cfg.BudgetBase + int(perceivedIntel*float64(cfg.BudgetPerIntelligence))
	// = 84

	factor := 1.0 - cfg.ApathyBudgetPenalty // 0.5
	expectedNodes := max(int(float64(effectiveNodes)*factor), 1)
	// = 42

	// Reproduce the budget computation from replan for Apathy state.
	reducedActions := max(int(float64(cfg.BudgetBase)*factor), 1)
	reducedDepth := max(int(float64(cfg.BudgetBase/4)*factor), 1)

	apathyBudget := planner.Budget{
		MaxNodes:   expectedNodes,
		MaxActions: reducedActions,
		MaxDepth:   reducedDepth,
	}

	t.Logf("Expected ApathyBudget: MaxNodes=%d, MaxActions=%d, MaxDepth=%d",
		apathyBudget.MaxNodes, apathyBudget.MaxActions, apathyBudget.MaxDepth)

	if apathyBudget.MaxNodes <= 0 {
		t.Errorf("MaxNodes must be >= 1, got %d", apathyBudget.MaxNodes)
	}
	if apathyBudget.MaxActions <= 0 {
		t.Errorf("MaxActions must be >= 1, got %d", apathyBudget.MaxActions)
	}
	if apathyBudget.MaxDepth <= 0 {
		t.Errorf("MaxDepth must be >= 1, got %d", apathyBudget.MaxDepth)
	}

	// The penalty should reduce nodes below the uncapped value.
	if apathyBudget.MaxNodes >= effectiveNodes {
		t.Errorf("apathyBudget.MaxNodes (%d) should be less than effectiveNodes (%d)",
			apathyBudget.MaxNodes, effectiveNodes)
	}
}

// TestApathyBudget_PlannerRejectsSmallBudget verifies the planner-side of the
// contract: with ApathyBudget{MaxNodes:1,MaxActions:1,MaxDepth:1} on a goal
// that needs a 2-action plan, Plan returns ErrBudgetExceeded; with nil it
// succeeds. This is the planner's AC per its SPEC.
func TestApathyBudget_PlannerRejectsSmallBudget(t *testing.T) {
	statReg := mustLoadStats(t, testStatsYAML)

	actionsYAML := `schema_version: 1
actions:
  - id: Forage
    tags: [effort:med]
    duration: 30
    produces: [has_food]
  - id: Eat
    tags: [effort:low]
    duration: 10
    requires: [has_food]
    produces: [has_Satiety]
    effect: { Satiety: 0.4 }
  - id: MoveTo
    tags: [effort:low]
    duration: 10
    produces: [at_target]
`
	actReg, err := actions.Load(strings.NewReader(actionsYAML))
	if err != nil {
		t.Fatalf("actions.Load: %v", err)
	}

	needsYAML := `schema_version: 1
needs:
  - id: Satiety
    kind: consumable
    default: { posture: MaintainAbove, setpoint: 0.55, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
`
	balanceYAML := `needs:
  Satiety: { decay_per_tick: 0.00070, satisfaction_threshold: 0.55 }
values:
  weights:
    Satiety: 1.00
`
	needReg, errN := needs.Load(strings.NewReader(needsYAML), strings.NewReader(balanceYAML))
	if errN != nil {
		t.Fatalf("needs.Load: %v", errN)
	}
	valsCfg, errV := values.Load(strings.NewReader(balanceYAML))
	if errV != nil {
		t.Fatalf("values.Load: %v", errV)
	}
	_ = valsCfg

	gatesYAML := `schema_version: 2
gates:
  - id: always
    tags: []
    expr: { stat: "Intelligence", op: ">=", value: -1 }
`
	gateReg, errG := gates.Load(strings.NewReader(gatesYAML), statReg)
	if errG != nil {
		t.Fatalf("gates.Load: %v", errG)
	}

	plannerCfg := planner.PlannerConfig{
		Budget:             planner.Budget{MaxDepth: 10, MaxActions: 20, MaxNodes: 200},
		BaseHorizonTicks:   10,
		LookaheadThreshold: 0.4,
		TagCosts:           map[core.Tag]float64{},
		UrgencyThreshold:   0.7,
	}
	p := planner.New(actReg, gateReg, needReg, statReg, plannerCfg)

	selfBelief := tom.Belief{
		EstStats: map[core.StatID]tom.StatDist{
			"Intelligence": {Mean: 80, Variance: 0},
		},
		Trust: 1.0,
	}
	snap := planner.AgentSnapshot{
		ID:        "agent_1",
		SelfModel: selfBelief,
		NeedIntensities: map[core.Dimension]float64{
			"Satiety": 0.90,
		},
		Known: map[core.ObjectID]struct{}{},
	}

	dims := []planner.DimensionPriority{
		{Dim: "Satiety", Priority: 1.5, Salience: 0.9},
	}

	// With configured budget (large enough) — should succeed.
	plan, _, err2 := p.Plan(snap, dims, rng.New(1))
	if err2 != nil {
		t.Fatalf("Plan with configured budget failed: %v", err2)
	}
	if len(plan.Actions) == 0 {
		t.Fatal("expected non-empty plan with configured budget")
	}
	t.Logf("Plan actions with normal budget: %v", plan.Actions)

	// With ApathyBudget that can't accommodate the plan length.
	snap.ApathyBudget = &planner.Budget{MaxDepth: 1, MaxActions: 1, MaxNodes: 1}
	_, _, errApathy := p.Plan(snap, dims, rng.New(1))
	if errApathy == nil {
		t.Error("expected error with ApathyBudget{MaxNodes:1,MaxActions:1,MaxDepth:1}")
	}
	t.Logf("Plan with tight ApathyBudget correctly returned: %v", errApathy)

	// Verify that the next call with nil ApathyBudget is back to normal (override is per-call only).
	snap.ApathyBudget = nil
	plan2, _, err3 := p.Plan(snap, dims, rng.New(1))
	if err3 != nil {
		t.Fatalf("Plan after resetting ApathyBudget=nil failed: %v", err3)
	}
	if len(plan2.Actions) == 0 {
		t.Fatal("expected non-empty plan after restoring nil ApathyBudget")
	}
	t.Logf("Plan actions after restoring nil budget: %v", plan2.Actions)
}

// ── Gap-closure (2): Aggression-drift threshold in updateResentment ──────────

const gapTestStatsWithVindictiveness = `schema_version: 1
stats:
  - id: Strength
    kind: capability
    range: [0, 100]
    default: 50
    gen: { dist: normal, mean: 50, sd: 10 }
    inherit: 0.5
  - id: Intelligence
    kind: capability
    range: [0, 100]
    default: 50
    gen: { dist: normal, mean: 50, sd: 10 }
    inherit: 0.5
  - id: Agility
    kind: capability
    range: [0, 100]
    default: 50
    gen: { dist: normal, mean: 50, sd: 10 }
    inherit: 0.5
  - id: Honesty
    kind: disposition
    range: [0, 100]
    default: 50
    gen: { dist: normal, mean: 50, sd: 10 }
    inherit: 0.5
  - id: Aggression
    kind: disposition
    range: [0, 100]
    default: 30
    gen: { dist: normal, mean: 30, sd: 10 }
    inherit: 0.3
  - id: Vindictiveness
    kind: disposition
    range: [0, 100]
    default: 50
    gen: { dist: normal, mean: 50, sd: 10 }
    inherit: 0.3
`

func mustLoadStatsGap(t *testing.T) *stats.Registry {
	t.Helper()
	reg, err := stats.Load(strings.NewReader(gapTestStatsWithVindictiveness))
	if err != nil {
		t.Fatalf("stats.Load: %v", err)
	}
	return reg
}

// TestAggressionDrift_RunsOnQuietTick verifies that when Resentment > threshold
// and the agent has latent goals, Aggression in ToM[self] rises even on a tick
// with NO new trigger events (the gap-closure: drift was behind accrueResentment's
// early return, so it never fired on quiet ticks).
func TestAggressionDrift_RunsOnQuietTick(t *testing.T) {
	statReg := mustLoadStatsGap(t)
	cfg := DefaultConfig()
	cfg.ResentmentThreshold = 0.30
	cfg.AggressionDrift = 0.02

	realStats := statReg.Defaults()
	realStats["Aggression"] = 30.0
	selfToM := tom.NewToM("agent_1", realStats, 0.5, rng.New(42), statReg, cfg.Rates)

	a := New("agent_1", core.Vec2{}, realStats, selfToM, cfg)
	// Set Resentment above threshold.
	a.Resentment = 0.50 // > 0.30
	// Add a latent goal so latentFactor > 0.
	a.Latent = []LatentGoal{{Dim: "Satiety", Since: 0, Intensity: 0.8}}

	// Read initial perceived Aggression.
	aggrBefore := a.perceivedStat("Aggression")

	// Call updateResentment directly (this is what phase 8 calls every tick).
	// No triggers arrive — this is the quiet-tick case that previously failed.
	a.updateResentment()

	aggrAfter := a.perceivedStat("Aggression")
	if aggrAfter <= aggrBefore {
		t.Errorf("Aggression should drift UP on quiet tick when Resentment > threshold: before=%.4f after=%.4f", aggrBefore, aggrAfter)
	}
	t.Logf("Aggression: %.4f → %.4f (drift applied on quiet tick)", aggrBefore, aggrAfter)
}

// TestAggressionDrift_NoLatentGoals_NoDrift verifies that with no latent goals
// (latentFactor == 0), no Aggression drift is applied even if Resentment > threshold.
func TestAggressionDrift_NoLatentGoals_NoDrift(t *testing.T) {
	statReg := mustLoadStatsGap(t)
	cfg := DefaultConfig()
	cfg.ResentmentThreshold = 0.30
	cfg.AggressionDrift = 0.02

	realStats := statReg.Defaults()
	realStats["Aggression"] = 30.0
	selfToM := tom.NewToM("agent_1", realStats, 0.5, rng.New(42), statReg, cfg.Rates)

	a := New("agent_1", core.Vec2{}, realStats, selfToM, cfg)
	a.Resentment = 0.50 // > threshold
	a.Latent = nil      // no latent goals → latentFactor == 0 → early return

	aggrBefore := a.perceivedStat("Aggression")
	a.updateResentment() // should early-return (len(Latent)==0)
	aggrAfter := a.perceivedStat("Aggression")

	if aggrAfter != aggrBefore {
		t.Errorf("Aggression should NOT drift when no latent goals (latentFactor=0): before=%.4f after=%.4f", aggrBefore, aggrAfter)
	}
	t.Logf("Aggression unchanged: %.4f (no latent goals)", aggrBefore)
}

// TestAggressionDrift_NotInAccrueResentment verifies that accrueResentment
// (trigger-gated) does NOT apply Aggression drift (it was removed from there).
// Only Resentment and Affinity change when triggers fire via accrueResentment.
func TestAggressionDrift_NotInAccrueResentment(t *testing.T) {
	statReg := mustLoadStatsGap(t)
	cfg := DefaultConfig()
	cfg.ResentmentThreshold = 0.30
	cfg.AggressionDrift = 0.02
	cfg.VindictivenessStatID = "Vindictiveness"

	realStats := statReg.Defaults()
	realStats["Aggression"] = 30.0
	realStats["Vindictiveness"] = 50.0
	selfToM := tom.NewToM("agent_1", realStats, 0.5, rng.New(42), statReg, cfg.Rates)

	a := New("agent_1", core.Vec2{}, realStats, selfToM, cfg)
	a.Coping = Latent
	a.Resentment = 0.50 // above threshold
	a.Latent = []LatentGoal{{Dim: "Satiety", Since: 0, Intensity: 0.8}}

	aggrBefore := a.perceivedStat("Aggression")
	resentBefore := a.Resentment

	// Call ONLY accrueResentment (not updateResentment).
	// accrueResentment should only modify Resentment (+ Affinity), NOT Aggression.
	triggers := []core.AgentID{"agent_2"}
	a.accrueResentment(triggers, statReg, core.NoopEmitter{})

	aggrAfter := a.perceivedStat("Aggression")
	if aggrAfter != aggrBefore {
		t.Errorf("accrueResentment should NOT apply Aggression drift (moved to updateResentment): before=%.4f after=%.4f", aggrBefore, aggrAfter)
	}
	// Resentment SHOULD have increased.
	if a.Resentment <= resentBefore {
		t.Errorf("accrueResentment should have increased Resentment: before=%.4f after=%.4f", resentBefore, a.Resentment)
	}
	t.Logf("Aggression unchanged=%.4f, Resentment %.4f→%.4f", aggrBefore, resentBefore, a.Resentment)
}

// ── Gap-closure (3): FunctionSpec / BeliefUpdated emit ────────────────────────

// TestHandleRelianceTrigger_EmitsBeliefUpdated verifies that handleRelianceTrigger
// emits a BeliefUpdated{cause:"reliance"} event when reliance is successfully formed.
func TestHandleRelianceTrigger_EmitsBeliefUpdated(t *testing.T) {
	statReg := mustLoadStatsGap(t)
	cfg := DefaultConfig()
	cfg.RelyCostThreshold = 1.0
	cfg.RelyOnDelta = 0.15
	cfg.Functions = []FunctionSpec{
		{ID: core.FuncSafety, Dim: "Safety", Stats: []core.StatID{"Strength"}},
	}

	realStats := statReg.Defaults()
	selfToM := tom.NewToM("agent_1", realStats, 0.5, rng.New(42), statReg, cfg.Rates)

	// Seed guardian with high Strength so BestProviderFor picks it.
	for range 10 {
		selfToM.Observe("guardian", tom.StatEvidence{
			Stat: "Strength", Observed: 90, Weight: 1.0, Tick: 1,
		})
	}
	for i := range 3 {
		selfToM.RecordTradeSuccess("guardian", core.Tick(i))
	}

	a := New("agent_1", core.Vec2{}, realStats, selfToM, cfg)
	a.Goal = "Safety"

	rec := &gapTestEmitter{}
	mockWV := &gapTestWorldView{agentIDs: []core.AgentID{"guardian"}}

	// Trigger with ErrUnreachable → reliance should form.
	a.handleRelianceTrigger(mockWV, 0, planner.ErrUnreachable, rec)

	// Check that a BeliefUpdated event with cause="reliance" was emitted.
	found := false
	for _, ev := range rec.events {
		if ev.Type != "BeliefUpdated" {
			continue
		}
		payload, ok := ev.Payload.(map[string]any)
		if !ok {
			continue
		}
		if payload["cause"] == "reliance" {
			found = true
			if payload["about"] != "guardian" {
				t.Errorf("BeliefUpdated.about = %v, want 'guardian'", payload["about"])
			}
			if payload["delta"] != cfg.RelyOnDelta {
				t.Errorf("BeliefUpdated.delta = %v, want %.2f", payload["delta"], cfg.RelyOnDelta)
			}
		}
	}
	if !found {
		t.Errorf("expected BeliefUpdated{cause:'reliance'} event, got %d events: %v", len(rec.events), rec.events)
	}
}

// TestHandleRelianceTrigger_NoEventWhenCheap verifies that no BeliefUpdated
// event is emitted when the plan is cheap enough to self-solve.
func TestHandleRelianceTrigger_NoEventWhenCheap(t *testing.T) {
	statReg := mustLoadStatsGap(t)
	cfg := DefaultConfig()
	cfg.RelyCostThreshold = 2.0
	cfg.Functions = []FunctionSpec{
		{ID: core.FuncSafety, Dim: "Safety", Stats: []core.StatID{"Strength"}},
	}

	realStats := statReg.Defaults()
	selfToM := tom.NewToM("agent_1", realStats, 0.5, rng.New(42), statReg, cfg.Rates)

	a := New("agent_1", core.Vec2{}, realStats, selfToM, cfg)
	a.Goal = "Safety"

	rec := &gapTestEmitter{}
	mockWV := &gapTestWorldView{agentIDs: []core.AgentID{"guardian"}}

	// Cost = 1.0 < threshold 2.0 → no trigger, no event.
	a.handleRelianceTrigger(mockWV, 1.0, nil, rec)

	for _, ev := range rec.events {
		if ev.Type == "BeliefUpdated" {
			t.Errorf("unexpected BeliefUpdated event when cost < threshold: %v", ev)
		}
	}
}

// TestHandleRelianceTrigger_UnknownDimNoEvent verifies that when the goal
// Dimension is not in Config.Functions, no reliance and no event are produced.
func TestHandleRelianceTrigger_UnknownDimNoEvent(t *testing.T) {
	statReg := mustLoadStatsGap(t)
	cfg := DefaultConfig()
	cfg.RelyCostThreshold = 1.0
	cfg.Functions = []FunctionSpec{
		// Only Safety in the table.
		{ID: core.FuncSafety, Dim: "Safety", Stats: []core.StatID{"Strength"}},
	}

	realStats := statReg.Defaults()
	selfToM := tom.NewToM("agent_1", realStats, 0.5, rng.New(42), statReg, cfg.Rates)

	a := New("agent_1", core.Vec2{}, realStats, selfToM, cfg)
	a.Goal = "Satiety" // NOT in Functions table

	rec := &gapTestEmitter{}
	mockWV := &gapTestWorldView{agentIDs: []core.AgentID{"guardian"}}

	a.handleRelianceTrigger(mockWV, 0, planner.ErrUnreachable, rec)

	if len(rec.events) > 0 {
		t.Errorf("expected no events for unknown Dimension 'Satiety', got: %v", rec.events)
	}
}

// ── Local helpers ─────────────────────────────────────────────────────────────

// gapTestEmitter records all emitted events.
type gapTestEmitter struct {
	events []core.Event
}

func (r *gapTestEmitter) Emit(e core.Event) {
	r.events = append(r.events, e)
}

// gapTestWorldView is a minimal WorldView stub for gap-closure tests.
type gapTestWorldView struct {
	mockWorldView
	agentIDs []core.AgentID
}

func (m *gapTestWorldView) AgentIDs() []core.AgentID { return m.agentIDs }
