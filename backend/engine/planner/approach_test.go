package planner

import (
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/actions"
	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/needs"
	"github.com/dogring/bdg/engine/rng"
)

// TestApproach_BridgesNearOtherForSocialGoal proves the structural unlock added by
// the Approach action: a social goal (Comfort) whose action requires `near_other` is
// REACHABLE even when the agent is not near anyone, because Approach produces
// `near_other` and the planner prepends it the same way it prepends MoveTo for
// `at_target`. Before Approach, `near_other` had NO producer, so the entire social /
// emergence layer was unplannable unless agents happened to already be adjacent.
//
// It also asserts the converse: when the agent is ALREADY near_other, Approach is NOT
// prepended (it is only the bridge, never dead weight).
func TestApproach_BridgesNearOtherForSocialGoal(t *testing.T) {
	statReg := mustLoadStats(t, testStatsYAML)
	gateReg := newEmptyGateRegistry(t, statReg)

	needsYAML := `schema_version: 1
needs:
  - id: Comfort
    kind: consumable
    default: { posture: MaintainAbove, setpoint: 0.35, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
`
	balanceYAML := `needs:
  Comfort: { decay_per_tick: 0.0006, satisfaction_threshold: 0.35 }
`
	needReg, err := needs.Load(strings.NewReader(needsYAML), strings.NewReader(balanceYAML))
	if err != nil {
		t.Fatalf("needs.Load: %v", err)
	}

	// Comfort requires near_other (a social action); Approach is its only producer.
	actionsYAML := `schema_version: 1
actions:
  - id: Approach
    tags: [effort:low]
    duration: 1
    produces: [near_other]
  - id: Comfort
    tags: [effort:low]
    duration: 15
    requires: [near_other]
    produces: [comforted, has_Comfort]
    effect: { Comfort: 0.4 }
`
	actReg, err := actions.Load(strings.NewReader(actionsYAML))
	if err != nil {
		t.Fatalf("actions.Load: %v", err)
	}

	pl := New(actReg, gateReg, needReg, statReg, defaultConfig())

	lonely := AgentSnapshot{
		ID:              "lonely",
		SelfModel:       beliefFromMeans(map[core.StatID]float64{"Intelligence": 50, "Strength": 50, "Honesty": 50, "Agility": 50}),
		NeedIntensities: map[core.Dimension]float64{"Comfort": 1.5},
		Known:           map[core.ObjectID]struct{}{},
		Urgency:         0.9,
		// SatisfiedFacts intentionally EMPTY: the agent is not near anyone.
	}
	goal := []DimensionPriority{{Dim: "Comfort", Priority: 1.0}}

	plan, _, err := pl.Plan(lonely, goal, rng.New(1))
	if err != nil {
		t.Fatalf("Comfort goal must be reachable via Approach when alone, got error: %v", err)
	}
	if !containsAction(plan.Actions, "Approach") || !containsAction(plan.Actions, "Comfort") {
		t.Errorf("alone: plan must prepend Approach before Comfort, got %v", plan.Actions)
	}

	// When ALREADY near_other, the planner must NOT prepend Approach.
	near := lonely
	near.SatisfiedFacts = []core.Pred{"near_other"}
	plan2, _, err := pl.Plan(near, goal, rng.New(1))
	if err != nil {
		t.Fatalf("Comfort goal must be reachable when already near_other: %v", err)
	}
	if containsAction(plan2.Actions, "Approach") {
		t.Errorf("near_other already satisfied: Approach must not be prepended, got %v", plan2.Actions)
	}
	if !containsAction(plan2.Actions, "Comfort") {
		t.Errorf("near_other already satisfied: plan must still contain Comfort, got %v", plan2.Actions)
	}
}

func containsAction(seq []actions.ActionID, id actions.ActionID) bool {
	for _, a := range seq {
		if a == id {
			return true
		}
	}
	return false
}
