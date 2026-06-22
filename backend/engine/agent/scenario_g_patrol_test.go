package agent

import (
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/actions"
	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/gates"
	"github.com/dogring/bdg/engine/needs"
	"github.com/dogring/bdg/engine/planner"
	"github.com/dogring/bdg/engine/rng"
	"github.com/dogring/bdg/engine/tom"
	"github.com/dogring/bdg/engine/values"
)

// Scenario G — defensive goal selects Patrol (P5 BLOCKER-2)
//
// When the village's mean collective Safety satisfaction (1 − mean member Safety
// intensity) falls below threats.safety_threat_threshold, an agent holding a Collective
// Safety MaintainAbove value adopts Safety as a defensive goal (defensiveCollectiveGoal),
// and the planner routes that goal to Patrol — Patrol produces `has_Safety` and is reached
// via MoveTo→at_target at the village_center.
func TestScenarioG_DefensiveGoalSelectsPatrol(t *testing.T) {
	statReg := mustLoadStats(t, testStatsYAML)

	needsYAML := `schema_version: 1
needs:
  - id: Safety
    kind: conditional
    default: { posture: MaintainAbove, setpoint: 0.60, referent: Self }
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
`
	needReg, err := needs.Load(strings.NewReader(needsYAML), strings.NewReader(balanceYAML))
	if err != nil {
		t.Fatalf("needs.Load: %v", err)
	}
	valsCfg, err := values.Load(strings.NewReader(balanceYAML))
	if err != nil {
		t.Fatalf("values.Load: %v", err)
	}

	// Patrol produces has_Safety (the Safety-dimension producer predicate); its at_target
	// precondition is satisfied by MoveTo — mirrors content/actions.yaml.
	actionsYAML := `schema_version: 1
actions:
  - id: MoveTo
    tags: [effort:low, uses:Agility]
    duration: 1
    produces: [at_target]
  - id: Patrol
    tags: [effort:med, uses:Agility]
    target_kind: village_center
    requires: [at_target]
    produces: [public_safety, has_Safety]
    effect: { Safety: 0.15 }
    duration: 12
`
	actReg, err := actions.Load(strings.NewReader(actionsYAML))
	if err != nil {
		t.Fatalf("actions.Load: %v", err)
	}

	gatesYAML := `schema_version: 2
gates:
  - id: capability_floor
    tags: [uses:Agility]
    expr:
      or:
        - { not: { tag: "uses:Agility" } }
        - { stat: Agility, op: ">=", value: 0.15 }
`
	gateReg, err := gates.Load(strings.NewReader(gatesYAML), statReg)
	if err != nil {
		t.Fatalf("gates.Load: %v", err)
	}

	plannerCfg := planner.PlannerConfig{
		Budget:           planner.Budget{MaxDepth: 6, MaxActions: 16, MaxNodes: 256},
		BaseHorizonTicks: 720,
		UrgencyThreshold: 0.65,
		TagCosts:         map[core.Tag]float64{"effort:low": 0.20, "effort:med": 0.50},
	}
	pl := planner.New(actReg, gateReg, needReg, statReg, plannerCfg)

	cfg := DefaultConfig() // SafetyThreatThreshold = 0.30
	realStats := statReg.Defaults()
	realStats["Agility"] = 0.7
	realStats["Intelligence"] = 0.5
	selfToM := tom.NewToM("guard", realStats, 0.5, rng.New(7), statReg, cfg.Rates)
	a := New("guard", core.Vec2{X: 0, Y: 0}, realStats, selfToM, cfg)
	a.Values = []core.Value{{
		Dimension: "Safety",
		Ref:       core.Referent{Kind: core.Collective, ID: "village"},
		Posture:   core.MaintainAbove,
		Setpoint:  0.60,
	}}

	// Village members with LOW collective safety: mean Safety intensity 0.80 → mean
	// satisfaction 0.20 < SafetyThreatThreshold 0.30.
	mockWV := newMockWorldViewWithMembers()
	mockWV.memberIntensities = map[core.AgentID]map[core.Dimension]float64{
		"villager_1": {"Safety": 0.80},
		"villager_2": {"Safety": 0.85},
		"guard":      {"Safety": 0.75},
	}

	// 1. The defensive override fires and selects the Safety dimension.
	defDim, fire := a.defensiveCollectiveGoal(mockWV, needReg)
	if !fire || defDim != "Safety" {
		t.Fatalf("defensiveCollectiveGoal = (%q, %v), want (Safety, true)", defDim, fire)
	}

	// 2. With Safety promoted as the goal, the planner selects Patrol.
	a.Goal = defDim
	priorities := a.appraise(needReg, valsCfg)
	goalPriorities := promoteGoal(a.Goal, priorities)

	selfBelief, _ := a.ToM.Self(a.ToM.SelfID())
	snapshot := planner.AgentSnapshot{
		ID:              core.ObjectID(a.ID),
		Pos:             a.Pos,
		CurrentStats:    a.RealStats,
		SelfModel:       selfBelief,
		NeedIntensities: a.NeedIntensities,
		Known:           map[core.ObjectID]struct{}{"village_center_0": {}},
		Stamina:         a.Stamina,
	}
	plan, _, err := pl.Plan(snapshot, goalPriorities, rng.New(7))
	if err != nil {
		t.Fatalf("planner.Plan: %v", err)
	}

	found := false
	for _, act := range plan.Actions {
		if act == "Patrol" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("plan %v does not contain Patrol", plan.Actions)
	}
	t.Logf("BLOCKER-2 PASSED: defensive Safety goal → plan=%v", plan.Actions)

	// 3. Negative: with adequate collective safety, no defensive override fires.
	mockWV.memberIntensities = map[core.AgentID]map[core.Dimension]float64{
		"villager_1": {"Safety": 0.10},
		"guard":      {"Safety": 0.05},
	}
	if _, fire := a.defensiveCollectiveGoal(mockWV, needReg); fire {
		t.Fatalf("defensiveCollectiveGoal fired despite healthy collective safety")
	}
}
