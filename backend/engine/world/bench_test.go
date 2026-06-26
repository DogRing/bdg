package world

import (
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/mind/gates"
	"github.com/dogring/bdg/engine/mind/needs"
	"github.com/dogring/bdg/engine/mind/perception"
	"github.com/dogring/bdg/engine/mind/planner"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/space/spatial"
	"github.com/dogring/bdg/engine/mind/stats"
	"github.com/dogring/bdg/engine/mind/values"
	"github.com/dogring/bdg/engine/kernel/worldtime"
)

// BenchmarkWorld_200Agents_1440Ticks measures wall-clock time and allocations
// for running a world with 200 agents for 1440 ticks (1 game-day at 1 tick/min).
// Use -benchtime=1x for a single iteration (each iteration IS 1440 ticks).
// Use -benchtime=Nx for N iterations.
// Skip with -short.
func BenchmarkWorld_200Agents_1440Ticks(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping benchmark in short mode")
	}

	// Load test content (same as world_test.go but extended for 200 agents).
	statReg := mustLoadBenchStats(b)

	needsYAML := `schema_version: 1
needs:
  - id: Satiety
    kind: consumable
    default: { posture: MaintainAbove, setpoint: 0.55, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
  - id: Rest
    kind: consumable
    default: { posture: MaintainAbove, setpoint: 0.45, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
  - id: Safety
    kind: conditional
    default: { posture: PreventBelow, setpoint: 0.50, referent: Self }
    salience: { curve: deficit, gain: 1.5 }
  - id: Social
    kind: consumable
    default: { posture: MaintainAbove, setpoint: 0.30, referent: Self }
    salience: { curve: deficit, gain: 0.8 }
`
	balanceYAML := `needs:
  Satiety: { decay_per_tick: 0.00070, satisfaction_threshold: 0.55 }
  Rest: { decay_per_tick: 0.00045, satisfaction_threshold: 0.45 }
  Social: { decay_per_tick: 0.00020, satisfaction_threshold: 0.30 }
values:
  weights:
    Satiety: 1.00
    Rest: 0.85
    Safety: 1.40
    Social: 0.80
  collective_aggregation_mode: "mean"
`
	needReg, err := needs.Load(strings.NewReader(needsYAML), strings.NewReader(balanceYAML))
	if err != nil {
		b.Fatalf("needs.Load: %v", err)
	}
	valsCfg, err := values.Load(strings.NewReader(balanceYAML))
	if err != nil {
		b.Fatalf("values.Load: %v", err)
	}

	actionsYAML := `schema_version: 1
actions:
  - id: RestAction
    tags: [effort:none]
    duration: 10
    produces: [has_Rest]
    effect_per_minute: { Rest: 0.01 }
  - id: Forage
    tags: [effort:med, uses:Agility]
    duration: 12
    produces: [has_food]
  - id: Eat
    tags: [effort:low]
    duration: 6
    requires: [has_food]
    produces: [has_Satiety]
    effect: { Satiety: 0.5 }
  - id: MoveTo
    tags: [effort:low, uses:Agility]
    duration: 5
    produces: [at_target]
  - id: Socialize
    tags: [effort:low, social]
    duration: 8
    produces: [has_Social]
    effect_per_minute: { Social: 0.005 }
`
	actReg, err := actions.Load(strings.NewReader(actionsYAML))
	if err != nil {
		b.Fatalf("actions.Load: %v", err)
	}

	gatesYAML := `schema_version: 2
gates:
  - id: always_visible
    tags: []
    expr: { stat: "Intelligence", op: ">=", value: -1 }
`
	gateReg, err := gates.Load(strings.NewReader(gatesYAML), statReg)
	if err != nil {
		b.Fatalf("gates.Load: %v", err)
	}

	plannerCfg := planner.PlannerConfig{
		Budget:             planner.Budget{MaxDepth: 6, MaxActions: 16, MaxNodes: 256},
		BaseHorizonTicks:   720,
		UrgencyThreshold:   0.65,
		LookaheadThreshold: 0.4,
		TagCosts: map[core.Tag]float64{
			"effort:low": 0.20, "effort:med": 0.50, "effort:none": 0.0,
		},
	}
	pl := planner.New(actReg, gateReg, needReg, statReg, plannerCfg)

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

	// Reset timer before setup completes, setup time is not counted.
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Create fresh world each iteration.
		rootRNG := rng.New(42)
		clock, _ := worldtime.NewClock(worldtime.DefaultConfig())
		w := New(DefaultConfig(), clock, rootRNG, svc, actReg, core.NoopEmitter{})

		// Spawn 200 agents.
		agentCfg := agent.DefaultConfig()
		for j := 0; j < 200; j++ {
			id := core.AgentID(benchAgentID(j))
			pos := core.Vec2{X: float64(j % 20) * 5.0, Y: float64(j / 20) * 5.0}
			spawnRNG := rng.New(int64(j + 1000))
			w.Spawn(id, pos, agentCfg, spawnRNG)
		}

		// Place some objects.
		supply := map[core.Dimension]float64{"Satiety": 0.5}
		for j := 0; j < 10; j++ {
			objID := core.ObjectID(benchObjectID(j))
			w.PlaceObject(objID, "berry_bush", core.Vec2{X: float64(j * 15), Y: float64(j * 15)}, supply)
		}

		b.StartTimer()

		// Run 1440 ticks.
		for tick := 0; tick < 1440; tick++ {
			w.Tick()
		}
	}
}

func benchAgentID(i int) string {
	return "agent_" + strings.Repeat("0", 4-len(itoa(i))) + itoa(i)
}

func benchObjectID(i int) string {
	return "object_" + strings.Repeat("0", 4-len(itoa(i))) + itoa(i)
}

// itoa is a simple int-to-string for benchmark seed IDs (avoid fmt import).
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [10]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// mustLoadBenchStats loads stats from a YAML string for benchmark use.
func mustLoadBenchStats(b *testing.B) *stats.Registry {
	b.Helper()
	yamlStr := `schema_version: 1
stats:
  - id: Strength
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
  - id: Intelligence
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
`
	reg, err := stats.Load(strings.NewReader(yamlStr))
	if err != nil {
		b.Fatalf("stats.Load: %v", err)
	}
	return reg
}
