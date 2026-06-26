package agent

import (
	"math"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/mind/needs"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/stats"
	"github.com/dogring/bdg/engine/mind/tom"
	"github.com/dogring/bdg/engine/mind/values"
)

// Scenario G: Collective referent evaluation — village safety (P5)
//
// This test verifies the collective referent wiring that underpins Scenario G
// (village chief emergence). When collective Safety drops, the agent should
// compute a non-zero priority through the Collective referent path.

// setupScenarioG creates an agent with a Collective value for village Safety.
func setupScenarioG(t *testing.T) (*Agent, *stats.Registry, *values.Config, *needs.Registry) {
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
    Satiety: 1.00
  collective_aggregation_mode: "min"
`
	needReg, err := needs.Load(strings.NewReader(needsYAML), strings.NewReader(balanceYAML))
	if err != nil {
		t.Fatalf("needs.Load: %v", err)
	}

	valsCfg, err := values.Load(strings.NewReader(balanceYAML))
	if err != nil {
		t.Fatalf("values.Load: %v", err)
	}

	if valsCfg.CollectiveAggregationMode != "min" {
		t.Fatalf("expected collective_aggregation_mode 'min', got %q", valsCfg.CollectiveAggregationMode)
	}

	cfg := DefaultConfig()

	realStats := statReg.Defaults()
	realStats["Intelligence"] = 50.0
	realStats["Honesty"] = 50.0
	realStats["Aggression"] = 30.0

	selfToM := tom.NewToM("agent_a", realStats, 0.5, rng.New(42), statReg, cfg.Rates)

	agent := New("agent_a", core.Vec2{X: 10, Y: 10}, realStats, selfToM, cfg)

	// Add a Collective value for village Safety.
	agent.Values = []core.Value{
		{
			Dimension: "Safety",
			Ref:       core.Referent{Kind: core.Collective, ID: "village"},
			Posture:   core.PreventBelow,
			Setpoint:  0.60,
		},
	}
	agent.NeedIntensities["Safety"] = 0.30
	agent.NeedIntensities["Satiety"] = 0.10

	return agent, statReg, valsCfg, needReg
}

// mockWorldViewWithMembers extends mockWorldView with member need intensity data.
type mockWorldViewWithMembers struct {
	mockWorldView
	memberIntensities map[core.AgentID]map[core.Dimension]float64
}

func (m *mockWorldViewWithMembers) MemberNeedIntensities() map[core.AgentID]map[core.Dimension]float64 {
	return m.memberIntensities
}

func newMockWorldViewWithMembers() *mockWorldViewWithMembers {
	return &mockWorldViewWithMembers{
		mockWorldView: mockWorldView{
			beliefs:         make(map[core.AgentID]tom.Belief),
			incomingSignals: make(map[core.AgentID][]core.Signal),
		},
		memberIntensities: make(map[core.AgentID]map[core.Dimension]float64),
	}
}

// TestScenarioG_CollectiveAppraisal_WithMembers verifies that appraiseCollective
// returns a non-zero priority when collective values are held and member data exists.
func TestScenarioG_CollectiveAppraisal_WithMembers(t *testing.T) {
	agent, statReg, valsCfg, needReg := setupScenarioG(t)
	_ = statReg

	// Build member intensities: multiple agents with varying Safety levels.
	mockWV := newMockWorldViewWithMembers()
	mockWV.memberIntensities = map[core.AgentID]map[core.Dimension]float64{
		"villager_1": {"Safety": 0.80, "Satiety": 0.30},
		"villager_2": {"Safety": 0.90, "Satiety": 0.25},
		"villager_3": {"Safety": 0.20, "Satiety": 0.60}, // poor safety (high need intensity)
	}

	// Compute collective safety priority.
	maxCollective := agent.appraiseCollective(mockWV, needReg, valsCfg)
	t.Logf("Scenario G: maxCollective = %.6f", maxCollective)

	if maxCollective <= 0 {
		t.Errorf("GOLDEN FAIL: appraiseCollective returned %.6f, expected > 0 when Collective value held and member data present",
			maxCollective)
	}

	// Verify the collective priority feeds into the urgency proxy.
	priorities := agent.appraise(needReg, valsCfg)
	selfMax := float64(priorities[0].Priority)
	combined := math.Max(selfMax, maxCollective)
	t.Logf("Scenario G: selfMax=%.6f, maxCollective=%.6f, combined=%.6f",
		selfMax, maxCollective, combined)

	if combined <= 0 {
		t.Error("combined urgency proxy should be > 0")
	}
}

// TestScenarioG_CollectiveAppraisal_NoMembers verifies that without member data,
// appraiseCollective returns 0.
func TestScenarioG_CollectiveAppraisal_NoMembers(t *testing.T) {
	agent, statReg, valsCfg, needReg := setupScenarioG(t)
	_ = statReg

	// Default mockWorldView has no member data (MemberNeedIntensities returns nil).
	mockWV := newMockWorldView()

	maxCollective := agent.appraiseCollective(mockWV, needReg, valsCfg)
	if maxCollective != 0 {
		t.Errorf("expected 0 when no member data, got %.6f", maxCollective)
	}
}

// TestScenarioG_CollectiveAppraisal_NoValues verifies that without any values,
// appraiseCollective returns 0.
func TestScenarioG_CollectiveAppraisal_NoValues(t *testing.T) {
	agent, statReg, valsCfg, needReg := setupScenarioG(t)
	_ = statReg

	// Clear values so no Collective values exist.
	agent.Values = nil

	mockWV := newMockWorldViewWithMembers()
	mockWV.memberIntensities = map[core.AgentID]map[core.Dimension]float64{
		"villager_1": {"Safety": 0.80},
	}

	maxCollective := agent.appraiseCollective(mockWV, needReg, valsCfg)
	if maxCollective != 0 {
		t.Errorf("expected 0 when no values, got %.6f", maxCollective)
	}
}

// TestScenarioG_CollectiveAppraisal_MeanMode verifies that when the aggregation
// mode is "mean", the collective current intensity is the mean across members.
func TestScenarioG_CollectiveAppraisal_MeanMode(t *testing.T) {
	// Setup with mean mode.
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
  collective_aggregation_mode: "mean"
`
	needReg, err := needs.Load(strings.NewReader(needsYAML), strings.NewReader(balanceYAML))
	if err != nil {
		t.Fatalf("needs.Load: %v", err)
	}

	valsCfg, err := values.Load(strings.NewReader(balanceYAML))
	if err != nil {
		t.Fatalf("values.Load: %v", err)
	}

	if valsCfg.CollectiveAggregationMode != "mean" {
		t.Fatalf("expected collective_aggregation_mode 'mean', got %q", valsCfg.CollectiveAggregationMode)
	}

	cfg := DefaultConfig()
	realStats := statReg.Defaults()
	realStats["Intelligence"] = 50.0
	realStats["Honesty"] = 50.0
	realStats["Aggression"] = 30.0

	selfToM := tom.NewToM("agent_b", realStats, 0.5, rng.New(42), statReg, cfg.Rates)
	agent := New("agent_b", core.Vec2{X: 10, Y: 10}, realStats, selfToM, cfg)
	agent.Values = []core.Value{
		{
			Dimension: "Safety",
			Ref:       core.Referent{Kind: core.Collective, ID: "village"},
			Posture:   core.PreventBelow,
			Setpoint:  0.60,
		},
	}
	agent.NeedIntensities["Safety"] = 0.30

	mockWV := newMockWorldViewWithMembers()
	mockWV.memberIntensities = map[core.AgentID]map[core.Dimension]float64{
		"villager_1": {"Safety": 0.10},
		"villager_2": {"Safety": 0.90},
	}
	// mean = (0.10 + 0.90) / 2 = 0.50
	// standing = 1 - 0.50/0.60 = 1 - 0.833... = 0.166...
	// salience = 1 - 0.166... = 0.833...
	// priority = 0.833... * 1.40 = 1.166...
	maxCollective := agent.appraiseCollective(mockWV, needReg, valsCfg)
	t.Logf("Scenario G mean mode: maxCollective = %.6f", maxCollective)

	if maxCollective <= 0 {
		t.Errorf("mean mode: expected > 0, got %.6f", maxCollective)
	}

	// Also verify we can reach the specific DeriveReferentInput with mean mode by
	// computing the priority manually.
	safetyDef, ok := needReg.Def("Safety")
	if !ok {
		t.Fatal("Safety not in registry")
	}
	ri := values.DeriveReferentInput(
		core.Referent{Kind: core.Collective, ID: "village"},
		"Safety", 0, safetyDef, tom.Belief{},
		0,
		[]values.ReferentInput{
			{CurrentIntensity: 0.10, MaxIntensity: safetyDef.Threshold},
			{CurrentIntensity: 0.90, MaxIntensity: safetyDef.Threshold},
		},
		0, "", valsCfg,
	)
	expectedCurrent := (0.10 + 0.90) / 2.0 // mean = 0.50
	if math.Abs(ri.CurrentIntensity-expectedCurrent) > 1e-12 {
		t.Errorf("DeriveReferentInput with mean mode: CurrentIntensity = %v, want %v",
			ri.CurrentIntensity, expectedCurrent)
	}
}

// TestScenarioG_CollectiveAppraisal_MinMode verifies that when the aggregation
// mode is "min", the collective current intensity is the min across members.
func TestScenarioG_CollectiveAppraisal_MinMode(t *testing.T) {
	agent, statReg, valsCfg, needReg := setupScenarioG(t)
	_ = statReg

	mockWV := newMockWorldViewWithMembers()
	mockWV.memberIntensities = map[core.AgentID]map[core.Dimension]float64{
		"villager_1": {"Safety": 0.10},
		"villager_2": {"Safety": 0.90},
	}
	// min = 0.10
	// standing = 1 - 0.10/0.60 = 1 - 0.166... = 0.833...
	// salience = 1 - 0.833... = 0.166...
	// priority = 0.166... * 1.40 = 0.233...
	maxCollective := agent.appraiseCollective(mockWV, needReg, valsCfg)
	t.Logf("Scenario G min mode: maxCollective = %.6f", maxCollective)

	if maxCollective <= 0 {
		t.Errorf("min mode: expected > 0, got %.6f", maxCollective)
	}

	// Also compute the exact priority through DeriveReferentInput to verify.
	safetyDef, ok := needReg.Def("Safety")
	if !ok {
		t.Fatal("Safety not in registry")
	}
	ri := values.DeriveReferentInput(
		core.Referent{Kind: core.Collective, ID: "village"},
		"Safety", 0, safetyDef, tom.Belief{},
		0,
		[]values.ReferentInput{
			{CurrentIntensity: 0.10, MaxIntensity: safetyDef.Threshold},
			{CurrentIntensity: 0.90, MaxIntensity: safetyDef.Threshold},
		},
		0, "", valsCfg,
	)
	// min = 0.10
	if ri.CurrentIntensity != 0.10 {
		t.Errorf("DeriveReferentInput with min mode: CurrentIntensity = %v, want 0.10",
			ri.CurrentIntensity)
	}
}

