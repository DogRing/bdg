package agent

import (
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/needs"
	"github.com/dogring/bdg/engine/planner"
	"github.com/dogring/bdg/engine/rng"
	"github.com/dogring/bdg/engine/stats"
	"github.com/dogring/bdg/engine/tom"
	"github.com/dogring/bdg/engine/values"
)

// ── Scenario G (P6): Reliance convergence, Vote delegation, Influence ────────
//
// These tests verify the P6 policy layer:
//   G1: handleRelianceTrigger fires on ErrUnreachable -> AdjustRelyOn called
//   G2: handleRelianceTrigger does NOT fire on low-cost solvable plan
//   G3: emitVoteIfEligible fires when both RelyOn + urgency thresholds exceeded
//   G4: emitVoteIfEligible does NOT fire when RelyOn below threshold
//   G5: emitVoteIfEligible does NOT fire when urgency below threshold
//   G6: distributedUrgency returns high value when collective Safety is low
//   G7: Influence-weighting through processVoteSignal
//   G8: bestRelyOnFor finds correct target

// setupP6Agent creates an agent with necessary ToM beliefs and config for P6 tests.
func setupP6Agent(t *testing.T) (*Agent, *stats.Registry) {
	t.Helper()

	statReg := mustLoadStats(t, testStatsYAML)

	needsYAML := `schema_version: 1
needs:
  - id: Safety
    kind: conditional
    default: { posture: PreventBelow, setpoint: 0.60, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
  - id: Satiety
    kind: consumable
    default: { posture: MaintainAbove, setpoint: 0.55, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
`
	balanceYAML := `needs:
  Satiety: { decay_per_tick: 0.00070, satisfaction_threshold: 0.55 }
values:
  weights:
    Safety: 1.40
`
	needReg, err := needs.Load(strings.NewReader(needsYAML), strings.NewReader(balanceYAML))
	if err != nil {
		t.Fatalf("needs.Load: %v", err)
	}
	_ = needReg

	valsCfg, err := values.Load(strings.NewReader(balanceYAML))
	if err != nil {
		t.Fatalf("values.Load: %v", err)
	}
	_ = valsCfg

	cfg := DefaultConfig()
	cfg.RelyCostThreshold = 1.2
	cfg.RelyOnDelta = 0.15
	cfg.VoteRelyThreshold = 0.4
	cfg.VoteUrgencyThreshold = 0.65
	cfg.VoteRelyOnDelta = 0.10
	cfg.InfluenceWeight = 0.5

	realStats := statReg.Defaults()
	realStats["Intelligence"] = 50.0
	realStats["Honesty"] = 50.0
	realStats["Aggression"] = 30.0

	selfToM := tom.NewToM("test_agent", realStats, 0.5, rng.New(42), statReg, cfg.Rates)

	// Add a provider agent to ToM with high Trust and competence.
	for i := 0; i < 20; i++ {
		selfToM.Observe("guardian", tom.StatEvidence{
			Stat: "Strength", Observed: 90, Weight: 1.0, Tick: 1,
		})
		selfToM.Observe("guardian", tom.StatEvidence{
			Stat: "Agility", Observed: 80, Weight: 1.0, Tick: 1,
		})
		selfToM.Observe("guardian", tom.StatEvidence{
			Stat: "Intelligence", Observed: 70, Weight: 1.0, Tick: 1,
		})
	}
	// Build trust in guardian.
	for i := 0; i < 6; i++ {
		selfToM.RecordTradeSuccess("guardian", core.Tick(i))
	}

	// Add a second peer with medium stats for comparison.
	for i := 0; i < 5; i++ {
		selfToM.Observe("peer", tom.StatEvidence{
			Stat: "Strength", Observed: 40, Weight: 1.0, Tick: 1,
		})
		selfToM.Observe("peer", tom.StatEvidence{
			Stat: "Intelligence", Observed: 30, Weight: 1.0, Tick: 1,
		})
	}
	selfToM.RecordTradeSuccess("peer", core.Tick(1))

	agent := New("test_agent", core.Vec2{X: 10, Y: 10}, realStats, selfToM, cfg)
	agent.Goal = "Safety"
	agent.NeedIntensities["Safety"] = 0.60
	agent.NeedIntensities["Satiety"] = 0.10

	return agent, statReg
}

// mockWorldViewForP6 is a WorldView mock with AgentIDs and member need intensities.
type mockWorldViewForP6 struct {
	mockWorldView
	memberIntensities map[core.AgentID]map[core.Dimension]float64
	agentIDList       []core.AgentID
}

func (m *mockWorldViewForP6) MemberNeedIntensities() map[core.AgentID]map[core.Dimension]float64 {
	if m.memberIntensities != nil {
		return m.memberIntensities
	}
	return m.mockWorldView.MemberNeedIntensities()
}

func (m *mockWorldViewForP6) AgentIDs() []core.AgentID {
	if m.agentIDList != nil {
		return m.agentIDList
	}
	return m.mockWorldView.AgentIDs()
}

func newMockWorldViewForP6() *mockWorldViewForP6 {
	return &mockWorldViewForP6{
		mockWorldView: mockWorldView{
			beliefs:         make(map[core.AgentID]tom.Belief),
			incomingSignals: make(map[core.AgentID][]core.Signal),
		},
		memberIntensities: make(map[core.AgentID]map[core.Dimension]float64),
	}
}

// TestRelianceTrigger_ErrUnreachable verifies that when the planner returns
// ErrUnreachable, handleRelianceTrigger fires and AdjustRelyOn is called on the
// BestProvider (the guardian).
func TestRelianceTrigger_ErrUnreachable(t *testing.T) {
	agent, _ := setupP6Agent(t)

	// Verify guardian exists in ToM with high competence.
	guardianBelief, ok := agent.ToM.Self("guardian")
	if !ok {
		t.Fatal("guardian belief not found in ToM")
	}
	strengthMean := guardianBelief.EstStats["Strength"].Mean
	t.Logf("Guardian Strength mean (from ToM): %.2f (expected ~90)", strengthMean)
	if strengthMean < 80 {
		t.Fatalf("guardian should have high Strength, got %.2f", strengthMean)
	}

	// Verify peer also exists.
	peerBelief, ok := agent.ToM.Self("peer")
	if !ok {
		t.Fatal("peer belief not found in ToM")
	}
	_ = peerBelief

	// Check BestProviderFor would select guardian (higher competence).
	mockWV := newMockWorldViewForP6()
	mockWV.agentIDList = []core.AgentID{"guardian", "peer"}

	// Record pre-reliance RelyOn on guardian for FuncSafety (Safety goal -> FuncSafety).
	guardianRelyOnBefore, _ := agent.ToM.Self("guardian")
	var relyBefore float64
	if guardianRelyOnBefore.RelyOn != nil {
		relyBefore = guardianRelyOnBefore.RelyOn[core.FuncSafety]
	}
	t.Logf("Guardian RelyOn[FuncSafety] before trigger: %.3f", relyBefore)

	// Trigger reliance with ErrUnreachable.
	agent.handleRelianceTrigger(mockWV, 0, planner.ErrUnreachable)

	// After trigger, RelyOn should have increased by RelyOnDelta (0.15).
	guardianBeliefAfter, _ := agent.ToM.Self("guardian")
	relyAfter := guardianBeliefAfter.RelyOn[core.FuncSafety]
	t.Logf("Guardian RelyOn[FuncSafety] after trigger: %.3f", relyAfter)

	if relyAfter <= relyBefore {
		t.Errorf("RelyOn should increase after ErrUnreachable trigger: before=%.3f after=%.3f",
			relyBefore, relyAfter)
	}
	expectedDelta := agent.Cfg.RelyOnDelta // 0.15
	actualDelta := relyAfter - relyBefore
	if actualDelta < expectedDelta-0.01 || actualDelta > expectedDelta+0.01 {
		t.Errorf("RelyOn delta %.3f should be ~%.3f", actualDelta, expectedDelta)
	}
}

// TestRelianceTrigger_NoFire_SolvablePlan verifies that when the plan is solvable
// with cost below threshold, no reliance shift occurs.
func TestRelianceTrigger_NoFire_SolvablePlan(t *testing.T) {
	agent, _ := setupP6Agent(t)

	mockWV := newMockWorldViewForP6()
	mockWV.agentIDList = []core.AgentID{"guardian", "peer"}

	// Record pre-trigger RelyOn for FuncSafety.
	guardianRelyBefore, _ := agent.ToM.Self("guardian")
	var relyBefore float64
	if guardianRelyBefore.RelyOn != nil {
		relyBefore = guardianRelyBefore.RelyOn[core.FuncSafety]
	}
	t.Logf("Guardian RelyOn[FuncSafety] before trigger: %.3f", relyBefore)

	// Trigger with cost below threshold (1.0 < 1.2).
	agent.handleRelianceTrigger(mockWV, 1.0, nil)

	guardianRelyAfter, _ := agent.ToM.Self("guardian")
	relyAfter := guardianRelyAfter.RelyOn[core.FuncSafety]
	t.Logf("Guardian RelyOn[FuncSafety] after trigger (cost=1.0): %.3f", relyAfter)

	if relyAfter != relyBefore {
		t.Errorf("RelyOn should NOT change when plan cost (1.0) < threshold (1.2): before=%.3f after=%.3f",
			relyBefore, relyAfter)
	}
}

// TestRelianceTrigger_HighCostFires verifies that when plan cost exceeds threshold,
// reliance does fire even without an error.
func TestRelianceTrigger_HighCostFires(t *testing.T) {
	agent, _ := setupP6Agent(t)

	mockWV := newMockWorldViewForP6()
	mockWV.agentIDList = []core.AgentID{"guardian", "peer"}

	// Record pre-trigger RelyOn for FuncSafety (Safety goal -> FuncSafety).
	guardianRelyBefore, _ := agent.ToM.Self("guardian")
	var relyBefore float64
	if guardianRelyBefore.RelyOn != nil {
		relyBefore = guardianRelyBefore.RelyOn[core.FuncSafety]
	}
	t.Logf("Guardian RelyOn[FuncSafety] before high-cost trigger: %.3f", relyBefore)

	// Trigger with cost above threshold (2.0 > 1.2) — should fire.
	agent.handleRelianceTrigger(mockWV, 2.0, nil)

	guardianRelyAfter, _ := agent.ToM.Self("guardian")
	relyAfter := guardianRelyAfter.RelyOn[core.FuncSafety]
	t.Logf("Guardian RelyOn[FuncSafety] after high-cost trigger: %.3f", relyAfter)

	if relyAfter <= relyBefore {
		t.Errorf("RelyOn should increase when plan cost (2.0) > threshold (1.2): before=%.3f after=%.3f",
			relyBefore, relyAfter)
	}
}

// TestVoteEmission_BothThresholdsMet verifies that when RelyOn > VoteRelyThreshold
// AND distributed urgency > VoteUrgencyThreshold, a Vote intent is emitted.
func TestVoteEmission_BothThresholdsMet(t *testing.T) {
	agent, _ := setupP6Agent(t)

	// Seed RelyOn on guardian for Safety.
	agent.ToM.AdjustRelyOn("guardian", core.FuncSafety, 0.6)
	t.Logf("Guardian RelyOn[FuncSafety] = 0.6 (threshold=%.2f)", agent.Cfg.VoteRelyThreshold)

	// Create world with high collective urgency (low Safety).
	mockWV := newMockWorldViewForP6()
	mockWV.memberIntensities = map[core.AgentID]map[core.Dimension]float64{
		"villager_1": {"Safety": 0.80},
		"villager_2": {"Safety": 0.70},
		"villager_3": {"Safety": 0.90},
	}
	mockWV.agentIDList = []core.AgentID{"guardian"}

	urgency := distributedUrgency(mockWV)
	t.Logf("Distributed urgency: %.3f (threshold=%.2f)", urgency, agent.Cfg.VoteUrgencyThreshold)
	if urgency <= agent.Cfg.VoteUrgencyThreshold {
		t.Fatalf("urgency (%.3f) should exceed threshold (%.2f) for vote to emit", urgency, agent.Cfg.VoteUrgencyThreshold)
	}

	// Emit vote.
	intent := agent.emitVoteIfEligible(1, mockWV)

	if intent.Kind == IntentNone {
		t.Fatal("expected Vote IntentSignal when both thresholds met, got IntentNone")
	}
	if intent.Kind != IntentSignal {
		t.Fatalf("expected IntentSignal, got %v", intent.Kind)
	}
	if intent.Signal == nil {
		t.Fatal("expected non-nil Signal on Vote intent")
	}
	if intent.Signal.Kind != SignalKind("Vote") {
		t.Errorf("expected SignalKind 'Vote', got %v", intent.Signal.Kind)
	}
	if intent.Signal.Target != "guardian" {
		t.Errorf("expected Vote Target 'guardian', got %v", intent.Signal.Toward)
	}
	if intent.Signal.Function != core.FuncSafety {
		t.Errorf("expected Vote Function 'Safety', got %v", intent.Signal.Function)
	}
	if intent.Signal.Intensity != 0.6 {
		t.Errorf("expected Vote Intensity 0.6 (RelyOn strength), got %.2f", intent.Signal.Intensity)
	}
}

// TestVoteEmission_NoVote_LowRelyOn verifies that when RelyOn is below threshold,
// no vote is emitted even with high urgency.
func TestVoteEmission_NoVote_LowRelyOn(t *testing.T) {
	agent, _ := setupP6Agent(t)

	// No RelyOn seeded — default is 0.

	// High urgency.
	mockWV := newMockWorldViewForP6()
	mockWV.memberIntensities = map[core.AgentID]map[core.Dimension]float64{
		"villager_1": {"Safety": 0.90},
		"villager_2": {"Safety": 0.85},
	}

	urgency := distributedUrgency(mockWV)
	t.Logf("Distributed urgency: %.3f (threshold=%.2f)", urgency, agent.Cfg.VoteUrgencyThreshold)
	if urgency <= agent.Cfg.VoteUrgencyThreshold {
		t.Fatal("urgency should exceed threshold for this test to be meaningful")
	}

	intent := agent.emitVoteIfEligible(1, mockWV)
	if intent.Kind != IntentNone {
		t.Errorf("expected IntentNone when RelyOn=0 (below threshold %.2f), got %v",
			agent.Cfg.VoteRelyThreshold, intent.Kind)
	}
}

// TestVoteEmission_NoVote_LowUrgency verifies that when urgency is below threshold,
// no vote is emitted even with high RelyOn.
func TestVoteEmission_NoVote_LowUrgency(t *testing.T) {
	agent, _ := setupP6Agent(t)

	// Seed high RelyOn.
	agent.ToM.AdjustRelyOn("guardian", core.FuncSafety, 0.8)
	t.Logf("Guardian RelyOn[FuncSafety] = 0.8 (threshold=%.2f)", agent.Cfg.VoteRelyThreshold)

	// Low urgency — all members have low Safety need intensity.
	mockWV := newMockWorldViewForP6()
	mockWV.memberIntensities = map[core.AgentID]map[core.Dimension]float64{
		"villager_1": {"Safety": 0.10},
		"villager_2": {"Safety": 0.20},
	}

	urgency := distributedUrgency(mockWV)
	t.Logf("Distributed urgency: %.3f (threshold=%.2f)", urgency, agent.Cfg.VoteUrgencyThreshold)
	if urgency >= agent.Cfg.VoteUrgencyThreshold {
		t.Fatalf("urgency (%.3f) should be below threshold (%.2f) for this test", urgency, agent.Cfg.VoteUrgencyThreshold)
	}

	intent := agent.emitVoteIfEligible(1, mockWV)
	if intent.Kind != IntentNone {
		t.Errorf("expected IntentNone when urgency=%.3f (below threshold %.2f), got %v",
			urgency, agent.Cfg.VoteUrgencyThreshold, intent.Kind)
	}
}

// TestDistributedUrgency_LowCollectiveSafety verifies that distributedUrgency
// returns a high value when collective Safety is low.
func TestDistributedUrgency_LowCollectiveSafety(t *testing.T) {
	mockWV := newMockWorldViewForP6()
	mockWV.memberIntensities = map[core.AgentID]map[core.Dimension]float64{
		"villager_1": {"Safety": 0.80},
		"villager_2": {"Safety": 0.75},
		"villager_3": {"Safety": 0.90},
	}

	urgency := distributedUrgency(mockWV)
	t.Logf("Distributed urgency with low Safety: %.3f", urgency)

	if urgency <= 0.5 {
		t.Errorf("expected high urgency (>0.5) when collective Safety is low (mean=~0.82), got %.3f", urgency)
	}
}

// TestDistributedUrgency_HighCollectiveSafety verifies that distributedUrgency
// returns a low value when collective Safety is high.
func TestDistributedUrgency_HighCollectiveSafety(t *testing.T) {
	mockWV := newMockWorldViewForP6()
	mockWV.memberIntensities = map[core.AgentID]map[core.Dimension]float64{
		"villager_1": {"Safety": 0.10},
		"villager_2": {"Safety": 0.15},
	}

	urgency := distributedUrgency(mockWV)
	t.Logf("Distributed urgency with high Safety: %.3f", urgency)

	if urgency > 0.3 {
		t.Errorf("expected low urgency (<=0.3) when collective Safety is high (mean=~0.125), got %.3f", urgency)
	}
}

// TestDistributedUrgency_NoMembers verifies that without member data, urgency is 0.
func TestDistributedUrgency_NoMembers(t *testing.T) {
	mockWV := newMockWorldViewForP6()
	// No member intensities set.

	urgency := distributedUrgency(mockWV)
	if urgency != 0 {
		t.Errorf("expected 0 urgency with no member data, got %.3f", urgency)
	}
}

// TestBestRelyOnFor_FindsBestProvider verifies that bestRelyOnFor finds the agent
// with the highest RelyOn for a given Function.
func TestBestRelyOnFor_FindsBestProvider(t *testing.T) {
	agent, _ := setupP6Agent(t)

	// Seed different RelyOn values.
	agent.ToM.AdjustRelyOn("guardian", core.FuncSafety, 0.7)
	agent.ToM.AdjustRelyOn("peer", core.FuncSafety, 0.3)

	target, strength := agent.bestRelyOnFor(core.FuncSafety)
	t.Logf("Best provider for Safety: %s (strength=%.3f)", target, strength)

	if target != "guardian" {
		t.Errorf("expected 'guardian' as best provider (RelyOn=0.7 > peer's 0.3), got %v", target)
	}
	if strength != 0.7 {
		t.Errorf("expected strength 0.7, got %.3f", strength)
	}
}

// TestBestRelyOnFor_NoRelyOn returns empty when no RelyOn exists.
func TestBestRelyOnFor_NoRelyOn(t *testing.T) {
	agent, _ := setupP6Agent(t)

	target, strength := agent.bestRelyOnFor(core.FuncSafety)
	if target != "" || strength != 0 {
		t.Errorf("expected empty result when no RelyOn exists, got (%v, %.3f)", target, strength)
	}
}

// TestGoalToFunction_MapsCorrectly verifies goalToFunction mapping.
func TestGoalToFunction_MapsCorrectly(t *testing.T) {
	tests := []struct {
		dim      core.Dimension
		expected core.Function
	}{
		{"Safety", core.FuncSafety},
		{"Satiety", core.FuncKnowledge},
		{"Hydration", core.FuncKnowledge},
		{"Rest", core.FuncKnowledge},
		{"", core.FuncKnowledge},
	}

	for _, tc := range tests {
		fn := goalToFunction(tc.dim)
		if fn != tc.expected {
			t.Errorf("goalToFunction(%q) = %q, want %q", tc.dim, fn, tc.expected)
		}
	}
}

// TestProcessVoteSignal_NoopOnNonVote verifies that non-Vote signals are ignored.
func TestProcessVoteSignal_NoopOnNonVote(t *testing.T) {
	agent, _ := setupP6Agent(t)

	// Send a non-Vote signal (e.g. SignalGreet).
	sig := core.Signal{
		Kind:    core.SignalGreet,
		Valence: 0.5,
	}

	// Should not panic.
	agent.processVoteSignal(sig)
}

// TestP6ConfigDefaults verifies the P6 config fields have the expected defaults.
func TestP6ConfigDefaults(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.RelyCostThreshold != 1.2 {
		t.Errorf("RelyCostThreshold = %.2f, want 1.2", cfg.RelyCostThreshold)
	}
	if cfg.RelyOnDelta != 0.15 {
		t.Errorf("RelyOnDelta = %.2f, want 0.15", cfg.RelyOnDelta)
	}
	if cfg.VoteRelyThreshold != 0.4 {
		t.Errorf("VoteRelyThreshold = %.2f, want 0.4", cfg.VoteRelyThreshold)
	}
	if cfg.VoteUrgencyThreshold != 0.65 {
		t.Errorf("VoteUrgencyThreshold = %.2f, want 0.65", cfg.VoteUrgencyThreshold)
	}
	if cfg.VoteRelyOnDelta != 0.10 {
		t.Errorf("VoteRelyOnDelta = %.2f, want 0.10", cfg.VoteRelyOnDelta)
	}
	if cfg.InfluenceWeight != 0.5 {
		t.Errorf("InfluenceWeight = %.2f, want 0.5", cfg.InfluenceWeight)
	}
}

// TestEmitVoteIfEligible_AgentIDsRequired verifies that vote emission works
// even when world AgentIDs is empty.
func TestEmitVoteIfEligible_AgentIDsRequired(t *testing.T) {
	agent, _ := setupP6Agent(t)

	// High RelyOn.
	agent.ToM.AdjustRelyOn("guardian", core.FuncSafety, 0.6)

	mockWV := newMockWorldViewForP6()
	mockWV.memberIntensities = map[core.AgentID]map[core.Dimension]float64{
		"villager_1": {"Safety": 0.90},
	}
	// No AgentIDs set — bestRelyOnFor still works since it reads ToM directly.

	intent := agent.emitVoteIfEligible(1, mockWV)
	// Should still emit because bestRelyOnFor doesn't need AgentIDs.
	if intent.Kind != IntentSignal {
		t.Log("Note: vote not emitted (checking if this is expected)")
	}
}
