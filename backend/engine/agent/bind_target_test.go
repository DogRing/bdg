package agent

import (
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/mind/planner"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/tom"
)

// targetActionsYAML defines an object-targeted action (Forage→berry_bush) and a
// no-target action (RestAction), so execute() can be exercised for both shapes.
const targetActionsYAML = `schema_version: 1
actions:
  - id: Forage
    tags: [effort:med]
    target_kind: berry_bush
    requires: [at_target]
    produces: [has_food]
    duration: 30
  - id: RestAction
    tags: [effort:none]
    duration: 60
    produces: [has_Rest]
    effect_per_minute: { Rest: 0.01 }
`

func mustTargetActions(t *testing.T) *actions.Registry {
	t.Helper()
	reg, err := actions.Load(strings.NewReader(targetActionsYAML))
	if err != nil {
		t.Fatalf("actions.Load: %v", err)
	}
	return reg
}

func newBindAgent(t *testing.T, pos core.Vec2) *Agent {
	t.Helper()
	cfg := DefaultConfig()
	statReg := mustLoadStats(t, testStatsYAML)
	realStats := statReg.Defaults()
	selfToM := tom.NewToM("agent_1", realStats, 0.5, rng.New(1), statReg, cfg.Rates)
	return New("agent_1", pos, realStats, selfToM, cfg)
}

// execute() must bind the NEAREST known object of the action's target_kind into
// Intent.Target — the binding the world uses for resource-conflict resolution.
func TestExecute_BindsNearestObjectTarget(t *testing.T) {
	actReg := mustTargetActions(t)
	a := newBindAgent(t, core.Vec2{X: 0, Y: 0})
	a.Plan = planner.Plan{Actions: []actions.ActionID{"Forage"}}
	a.PlanIdx = 0

	// Known objects in ObjectID-sorted order (as WorldView.KnownObjects yields).
	mwv := &mockWorldView{knownObjs: []KnownObject{
		{ID: "berry_bush_far", Pos: core.Vec2{X: 10, Y: 0}, Kind: "berry_bush"},
		{ID: "berry_bush_near", Pos: core.Vec2{X: 2, Y: 0}, Kind: "berry_bush"},
		{ID: "rock_1", Pos: core.Vec2{X: 1, Y: 0}, Kind: "rock"}, // wrong kind, closer
	}}

	got := a.execute(0, actReg, mwv)
	if got.Kind != IntentStart {
		t.Fatalf("expected IntentStart, got %v", got.Kind)
	}
	if got.Target != "berry_bush_near" {
		t.Fatalf("Target = %q, want nearest matching object %q", got.Target, "berry_bush_near")
	}
}

// On a tie in distance, the lower ObjectID wins (D12; KnownObjects is ObjectID-sorted).
func TestExecute_TargetTieBreaksByObjectID(t *testing.T) {
	actReg := mustTargetActions(t)
	a := newBindAgent(t, core.Vec2{X: 0, Y: 0})
	a.Plan = planner.Plan{Actions: []actions.ActionID{"Forage"}}

	mwv := &mockWorldView{knownObjs: []KnownObject{
		{ID: "berry_bush_a", Pos: core.Vec2{X: 3, Y: 0}, Kind: "berry_bush"},
		{ID: "berry_bush_b", Pos: core.Vec2{X: 3, Y: 0}, Kind: "berry_bush"}, // equidistant
	}}

	if got := a.execute(0, actReg, mwv).Target; got != "berry_bush_a" {
		t.Fatalf("tie should bind lowest ObjectID, got %q", got)
	}
}

// A no-object-target action binds the empty Target (so it never contends).
func TestExecute_NoTargetForNonObjectAction(t *testing.T) {
	actReg := mustTargetActions(t)
	a := newBindAgent(t, core.Vec2{X: 0, Y: 0})
	a.Plan = planner.Plan{Actions: []actions.ActionID{"RestAction"}}

	mwv := &mockWorldView{knownObjs: []KnownObject{
		{ID: "berry_bush_near", Pos: core.Vec2{X: 1, Y: 0}, Kind: "berry_bush"},
	}}

	if got := a.execute(0, actReg, mwv).Target; got != "" {
		t.Fatalf("non-object action should bind no Target, got %q", got)
	}
}

// With no known object of the action's kind, Target stays empty.
func TestExecute_NoMatchingObjectLeavesTargetEmpty(t *testing.T) {
	actReg := mustTargetActions(t)
	a := newBindAgent(t, core.Vec2{X: 0, Y: 0})
	a.Plan = planner.Plan{Actions: []actions.ActionID{"Forage"}}

	mwv := &mockWorldView{knownObjs: []KnownObject{
		{ID: "rock_1", Pos: core.Vec2{X: 1, Y: 0}, Kind: "rock"},
	}}

	if got := a.execute(0, actReg, mwv).Target; got != "" {
		t.Fatalf("no matching object should leave Target empty, got %q", got)
	}
}

// The Target binding persists across the durative action's Continue ticks.
func TestExecute_TargetBoundOnContinue(t *testing.T) {
	actReg := mustTargetActions(t)
	a := newBindAgent(t, core.Vec2{X: 0, Y: 0})
	a.Plan = planner.Plan{Actions: []actions.ActionID{"Forage"}}
	a.Elapsed = 5 // mid-action → IntentContinue

	mwv := &mockWorldView{knownObjs: []KnownObject{
		{ID: "berry_bush_0", Pos: core.Vec2{X: 4, Y: 0}, Kind: "berry_bush"},
	}}

	got := a.execute(0, actReg, mwv)
	if got.Kind != IntentContinue {
		t.Fatalf("expected IntentContinue, got %v", got.Kind)
	}
	if got.Target != "berry_bush_0" {
		t.Fatalf("Continue Target = %q, want %q", got.Target, "berry_bush_0")
	}
}
