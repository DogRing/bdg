package planner

import (
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/actions"
	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/gates"
	"github.com/dogring/bdg/engine/needs"
	"github.com/dogring/bdg/engine/stats"
	"github.com/dogring/bdg/engine/tom"
)

// ── Test helpers ──────────────────────────────────────────────────────────────

// testRegs holds minimal registries for testing.
type testRegs struct {
	actions *actions.Registry
	gates   *gates.Registry
	needs   *needs.Registry
	stats   *stats.Registry
}

// makeTestRegs creates minimal registries for testing.
func makeTestRegs(t *testing.T) testRegs {
	t.Helper()

	statReg := mustLoadStats(t, testStatsYAML)

	gatesYAML := `schema_version: 2
gates:
  - id: low_effort_honest
    tags: [effort:high]
    expr:
      and:
        - { stat: Strength, op: ">=", value: 30 }
  - id: violent_block
    tags: [violent:high]
    expr:
      and:
        - { stat: Honesty, op: ">=", value: 50 }
  - id: always_visible
    tags: []
    expr: { stat: "Intelligence", op: ">=", value: -1 }
`
	gateReg, err := gates.Load(strings.NewReader(gatesYAML), statReg)
	if err != nil {
		t.Fatalf("gates.Load: %v", err)
	}

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
  - id: Safety
    kind: conditional
    default: { posture: PreventBelow, setpoint: 0.60, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
`
	balanceYAML := `needs:
  Satiety: { decay_per_tick: 0.00070, satisfaction_threshold: 0.55 }
  Hydration: { decay_per_tick: 0.00110, satisfaction_threshold: 0.50 }
  Rest: { decay_per_tick: 0.00045, satisfaction_threshold: 0.45 }
`
	needReg, err := needs.Load(strings.NewReader(needsYAML), strings.NewReader(balanceYAML))
	if err != nil {
		t.Fatalf("needs.Load: %v", err)
	}

	actionsYAML := `schema_version: 1
actions:
  - id: Eat
    tags: [effort:low]
    duration: 10
    produces: [has_Satiety]
    effect: { Satiety: 0.4 }
  - id: Forage
    tags: [effort:med]
    duration: 30
    produces: [has_food]
    produces_item: berry
    effect: {}
  - id: Hunt
    tags: [effort:high, risk:med, violent:high]
    duration: 60
    requires: [has_weapon]
    produces: [has_food]
    produces_item: meat
    effect: {}
  - id: CraftWeapon
    tags: [effort:med]
    duration: 45
    produces: [has_weapon]
    effect: {}
  - id: Drink
    tags: [effort:low]
    duration: 5
    produces: [has_Hydration]
    effect: { Hydration: 0.5 }
  - id: RestAction
    tags: [effort:none]
    duration: 60
    produces: [has_Rest]
    effect_per_minute: { Rest: 0.01 }
  - id: GatherWater
    tags: [effort:med]
    duration: 20
    produces: [has_Hydration, has_water]
    effect: {}
  - id: Steal
    tags: [effort:low, violent:high]
    duration: 15
    produces: [has_Satiety]
    effect: { Satiety: 0.5 }
  - id: MoveTo
    tags: [effort:low]
    duration: 10
    produces: [at_target]
`
	actReg, err := actions.Load(strings.NewReader(actionsYAML))
	if err != nil {
		t.Fatalf("actions.Load: %v", err)
	}

	return testRegs{
		actions: actReg,
		gates:   gateReg,
		needs:   needReg,
		stats:   statReg,
	}
}

// defaultConfig returns a minimal PlannerConfig for testing.
func defaultConfig() PlannerConfig {
	return PlannerConfig{
		Budget: Budget{
			MaxDepth:   6,
			MaxActions: 16,
			MaxNodes:   256,
		},
		BaseHorizonTicks: 720,
		UrgencyThreshold: 0.65,
		LookaheadThreshold: 0.4,
		TagCosts: map[core.Tag]float64{
			"effort:low":   0.20,
			"effort:med":   0.50,
			"effort:high":  0.90,
			"risk:med":     0.40,
			"violent:high": 1.50,
		},
	}
}

func newPlanner(t *testing.T, regs testRegs, cfg PlannerConfig) *Planner {
	t.Helper()
	return New(regs.actions, regs.gates, regs.needs, regs.stats, cfg)
}

func defaultAgent() AgentSnapshot {
	return AgentSnapshot{
		ID:              "test_agent",
		SelfModel:       beliefFromMeans(map[core.StatID]float64{"Intelligence": 50, "Strength": 50, "Honesty": 50}),
		NeedIntensities: map[core.Dimension]float64{"Satiety": 0.3, "Hydration": 0.2, "Rest": 0.15},
		Known:           map[core.ObjectID]struct{}{"well": {}},
		Urgency:         0.3,
	}
}

// beliefFromMeans creates a tom.Belief from a map of stat means, for concise test setup.
func beliefFromMeans(means map[core.StatID]float64) tom.Belief {
	estStats := make(map[core.StatID]tom.StatDist, len(means))
	for sid, mean := range means {
		estStats[sid] = tom.StatDist{Mean: mean, Variance: 0}
	}
	return tom.Belief{
		EstStats: estStats,
		Trust:    1.0,
	}
}

// testStatsYAML is the canonical test stats fixture.
const testStatsYAML = `schema_version: 1
stats:
  - id: Strength
    label: Strength
    kind: capability
    range: [0, 100]
    default: 50
    gen: { dist: normal, mean: 50, sd: 10 }
    inherit: 0.5
  - id: Agility
    label: Agility
    kind: capability
    range: [0, 100]
    default: 50
    gen: { dist: normal, mean: 50, sd: 10 }
    inherit: 0.5
  - id: Intelligence
    label: Intelligence
    kind: capability
    range: [0, 100]
    default: 50
    gen: { dist: normal, mean: 50, sd: 10 }
    inherit: 0.5
  - id: Honesty
    label: Honesty
    kind: disposition
    range: [0, 100]
    default: 50
    gen: { dist: normal, mean: 50, sd: 10 }
    inherit: 0.5
`

// newEmptyGateRegistry creates a gates registry with a pass-through gate
// so the registry is non-empty (gates.Load rejects empty lists).
func newEmptyGateRegistry(t *testing.T, statReg *stats.Registry) *gates.Registry {
	t.Helper()
	yamlStr := `schema_version: 2
gates:
  - id: always_visible
    tags: []
    expr: { stat: "Intelligence", op: ">=", value: -1 }
`
	reg, err := gates.Load(strings.NewReader(yamlStr), statReg)
	if err != nil {
		t.Fatalf("gates.Load: %v", err)
	}
	return reg
}

func mustLoadStats(t *testing.T, yamlStr string) *stats.Registry {
	t.Helper()
	reg, err := stats.Load(strings.NewReader(yamlStr))
	if err != nil {
		t.Fatalf("stats.Load: %v", err)
	}
	return reg
}
