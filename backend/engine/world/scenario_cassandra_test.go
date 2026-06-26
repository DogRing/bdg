package world

// Scenario B — Cassandra's Tragedy (예측 지능 격차)
//
// 고지능 에이전트(Cassandra)는 lookahead forward-sim(P5)으로 미래 Satiety 부족을
// 예측하고 선제적으로 Forage를 계획한다. 저지능 에이전트(Peasant)는 lookahead가
// 없으므로 현재 요구가 임계점에 달하기 전까지 식량을 비축하지 않는다.
//
// 기근이 닥치면(Satiety 임계 도달), Peasant의 urgency가 0.70을 초과하고 Cassandra의
// 비축 창고(Inventory > 0 → owned_by_other)에서 Take를 계획한다.
//
// 관찰 포인트:
//  1. 지능 격차 → 계획 지평선(Horizon) 격차 (P5, LookaheadThreshold=0.40)
//  2. 기근 후 저지능 Peasant urgency 급등 → conscience 완화 → Take 계획
//  3. 결정론 검증 (D12)
//
// 기술 근거:
//   - Cassandra: Intelligence=0.80 >= 0.40 → Horizon = floor(720×0.80) = 576 > 0
//   - Peasant:   Intelligence=0.30 < 0.40  → Horizon = 0 (P5 hard skip)
//   - 기근 후: urgency = (0.50/0.55)/1.0 ≈ 0.909 > 0.70, Honesty=0.48 < 0.55 → Take 허용

import (
	"slices"
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

// ── Cassandra fixture YAML ────────────────────────────────────────────────────
// Same stats/gates/needs as Robin Hood, plus Forage action for provisioning.

const cassandraActionsYAML = `schema_version: 1
actions:
  - id: Take
    tags: [ "norm:transgressive", "effort:low" ]
    requires: [ near_other, owned_by_other ]
    produces: [ holding, has_food ]
    duration: 4
  - id: Eat
    tags: [ "effort:low" ]
    requires: [ has_food ]
    produces: [ has_Satiety ]
    effect: { Satiety: 0.50 }
    duration: 6
  - id: MoveTo
    tags: [ "effort:low", "uses:Agility" ]
    produces: [ at_target ]
    duration: 5
  - id: Forage
    tags: [ "effort:med", "uses:Agility" ]
    target_kind: berry_bush
    requires: [ at_target ]
    produces: [ has_food ]
    duration: 12
`

// newCassandraFixture builds a World with the Cassandra scenario registries.
// Identical to newRobinHoodFixture except actions include Forage for provisioning.
func newCassandraFixture(t *testing.T, seed int64) *testFixture {
	t.Helper()

	statReg, err := stats.Load(strings.NewReader(robinHoodStatsYAML))
	if err != nil {
		t.Fatalf("stats.Load: %v", err)
	}
	needReg, err := needs.Load(
		strings.NewReader(robinHoodNeedsYAML),
		strings.NewReader(robinHoodBalanceYAML),
	)
	if err != nil {
		t.Fatalf("needs.Load: %v", err)
	}
	valsCfg, err := values.Load(strings.NewReader(robinHoodBalanceYAML))
	if err != nil {
		t.Fatalf("values.Load: %v", err)
	}
	actReg, err := actions.Load(strings.NewReader(cassandraActionsYAML))
	if err != nil {
		t.Fatalf("actions.Load: %v", err)
	}
	gateReg, err := gates.Load(strings.NewReader(robinHoodGatesYAML), statReg)
	if err != nil {
		t.Fatalf("gates.Load: %v", err)
	}

	plannerCfg := planner.PlannerConfig{
		Budget:             planner.Budget{MaxDepth: 6, MaxActions: 16, MaxNodes: 256},
		BaseHorizonTicks:   720,
		UrgencyThreshold:   0.65,
		LookaheadThreshold: 0.4,
		TagCosts: map[core.Tag]float64{
			"effort:low":  0.20,
			"effort:med":  0.50,
			"effort:none": 0.0,
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

	rootRNG := rng.New(seed)
	clock, _ := worldtime.NewClock(worldtime.DefaultConfig())
	emit := &recordingEmitter{}
	w := New(DefaultConfig(), clock, rootRNG, svc, actReg, emit)

	return &testFixture{
		world:   w,
		regs:    testRegs{stats: statReg, needs: needReg, values: valsCfg, actions: actReg, gates: gateReg},
		rootRNG: rootRNG,
		clock:   clock,
		svc:     svc,
		actReg:  actReg,
		emit:    emit,
	}
}

// ── Test 1: Intelligence gap → Horizon gap ────────────────────────────────────

// TestCassandra_HighIntelProvisions_LowIntelDoesnt verifies the P5 intelligence-gated
// lookahead: Cassandra (Intelligence=0.80) gets Horizon>0 and forward-sims provisioning
// subgoals; Peasant (Intelligence=0.30) gets Horizon=0 (P5 hard skip) and cannot see
// the coming famine.
func TestCassandra_HighIntelProvisions_LowIntelDoesnt(t *testing.T) {
	const seed = int64(5050)

	run := func() (*World, *agent.Agent, *agent.Agent) {
		fx := newCassandraFixture(t, seed)
		cfg := robinHoodAgentConfig() // MaxPossiblePriority = 1.0

		// Cassandra: high intelligence → provisioning lookahead fires.
		cassandra := fx.world.Spawn("cassandra", core.Vec2{X: 0, Y: 0}, cfg, rng.New(10))
		seedToM(cassandra, "Intelligence", 0.80) // >= 0.40 → Horizon = floor(720×0.80)=576

		// Peasant: low intelligence → no lookahead.
		peasant := fx.world.Spawn("peasant", core.Vec2{X: 5, Y: 0}, cfg, rng.New(20))
		seedToM(peasant, "Intelligence", 0.30) // < 0.40 → Horizon = 0 (P5 hard skip)

		// Both start at moderate Satiety (intensity=0.30, threshold=0.55, slack=0.25).
		// Forward-sim: demand = 0.00070 × 576 = 0.403 > slack → provisioning fires for Cassandra.
		cassandra.NeedIntensities["Satiety"] = 0.30
		peasant.NeedIntensities["Satiety"] = 0.30

		// Berry bush in sight range so Forage is a reachable action.
		fx.world.PlaceObject("berry_bush_1", "berry_bush",
			core.Vec2{X: 3, Y: 0},
			map[core.Dimension]float64{"Satiety": 0.50})

		// One tick to trigger planning.
		fx.world.Tick()

		return fx.world, cassandra, peasant
	}

	worldA, cassandraA, peasantA := run()
	worldB, _, _ := run()

	// Cassandra: Horizon > 0 (lookahead active, provisioning subgoal generated).
	if cassandraA.Plan.Horizon <= 0 {
		t.Errorf("Cassandra (Intelligence=0.80) should have Horizon > 0 (lookahead active), got %d",
			cassandraA.Plan.Horizon)
	} else {
		t.Logf("Cassandra Plan.Horizon=%d plan=%v (provisions)", cassandraA.Plan.Horizon, cassandraA.Plan.Actions)
	}

	// Peasant: Horizon = 0 (P5 hard skip, no lookahead).
	if peasantA.Plan.Horizon != 0 {
		t.Errorf("Peasant (Intelligence=0.30) should have Horizon=0 (P5 hard skip), got %d",
			peasantA.Plan.Horizon)
	} else {
		t.Logf("Peasant Plan.Horizon=%d plan=%v (no lookahead)", peasantA.Plan.Horizon, peasantA.Plan.Actions)
	}

	// Determinism (D12).
	if dA, dB := worldDigest(worldA), worldDigest(worldB); dA != dB {
		t.Errorf("DETERMINISM FAILED (Cassandra intelligence gap test)")
	}
}

// ── Test 2: Famine pushes Peasant to theft ────────────────────────────────────

// TestCassandra_FaminePushesToTheft verifies that after the famine (Satiety intensity
// at critical level), a Peasant whose Honesty is in the urgency-relief range (0.40 < 0.48 < 0.55)
// will plan Take against Cassandra's stockpile.
//
// This reproduces the end-state of the Cassandra narrative:
//   - Cassandra survived by provisioning (she has food = has_items tag)
//   - Peasant didn't provision, now at critical urgency
//   - Conscience gate urgency-relief branch activates → Take is visible
func TestCassandra_FaminePushesToTheft(t *testing.T) {
	const seed = int64(6161)

	run := func() (*World, *agent.Agent) {
		fx := newCassandraFixture(t, seed)
		cfg := robinHoodAgentConfig()

		// Cassandra: near Peasant, stockpile in inventory (has_items → owned_by_other).
		cassandra := fx.world.Spawn("cassandra", core.Vec2{X: 1, Y: 0}, cfg, rng.New(10))
		cassandra.Inventory["berries"] = 8 // triggers has_items tag in snapshot
		cassandra.NeedIntensities["Satiety"] = 0.10 // well-fed (provisioned)

		// Peasant: critical Satiety → urgency ≈ 0.909 > 0.70; Honesty in urgency-relief zone.
		peasant := fx.world.Spawn("peasant", core.Vec2{X: 0, Y: 0}, cfg, rng.New(20))
		seedToM(peasant, "Honesty", 0.48)    // 0.40 < 0.48 < 0.55 → base blocked, urgency-relief passes
		seedToM(peasant, "Aggression", 0.20) // below 0.65 (base) and 0.50 (urgency branch)
		peasant.NeedIntensities["Satiety"] = 0.50 // critical: urgency = (0.50/0.55)/1.0 ≈ 0.909

		for range 40 {
			fx.world.Tick()
		}
		return fx.world, peasant
	}

	worldA, peasantA := run()
	worldB, _ := run()

	// Peasant should plan or execute Take (famine → urgency-relief → conscience passed).
	took := peasantA.NeedIntensities["Satiety"] < 0.50
	tookOrPlanned := took || slices.Contains(peasantA.Plan.Actions, "Take")
	if !tookOrPlanned {
		t.Errorf("Famine Peasant (Honesty=0.48, urgency≈0.909) should plan/execute Take "+
			"against Cassandra's stockpile: final_intensity=%.4f plan=%v coping=%v",
			peasantA.NeedIntensities["Satiety"], peasantA.Plan.Actions, peasantA.Coping)
	} else if took {
		t.Logf("Peasant executed Take+Eat during famine: Satiety intensity %.4f (< 0.50 initial)",
			peasantA.NeedIntensities["Satiety"])
	} else {
		t.Logf("Peasant has Take in plan (about to execute): %v", peasantA.Plan.Actions)
	}

	// Determinism (D12).
	if dA, dB := worldDigest(worldA), worldDigest(worldB); dA != dB {
		t.Errorf("DETERMINISM FAILED (Cassandra famine theft test)")
	}
}

// ── Test 3: Honest Peasant endures famine without stealing ───────────────────

// TestCassandra_HonestPeasant_StarvesDignified verifies that a Peasant with Honesty=0.80
// cannot plan Take against Cassandra's stockpile even at critical urgency. The urgency-
// relief branch requires Honesty < 0.55; at 0.80 neither conscience-relief condition is met.
func TestCassandra_HonestPeasant_StarvesDignified(t *testing.T) {
	const seed = int64(7272)

	run := func() (*World, *agent.Agent) {
		fx := newCassandraFixture(t, seed)
		cfg := robinHoodAgentConfig()

		cassandra := fx.world.Spawn("cassandra", core.Vec2{X: 1, Y: 0}, cfg, rng.New(10))
		cassandra.Inventory["berries"] = 8

		// Very honest Peasant: both urgency-relief conditions blocked.
		peasant := fx.world.Spawn("peasant", core.Vec2{X: 0, Y: 0}, cfg, rng.New(20))
		seedToM(peasant, "Honesty", 0.80)    // > 0.55 → urgency-relief Honesty branch BLOCKED
		seedToM(peasant, "Aggression", 0.20) // < 0.50 → urgency-relief Aggression branch BLOCKED
		peasant.NeedIntensities["Satiety"] = 0.50 // critical urgency ≈ 0.909

		for range 40 {
			fx.world.Tick()
		}
		return fx.world, peasant
	}

	worldA, peasantA := run()
	worldB, _ := run()

	// Honest Peasant must NOT plan Take (conscience holds even at max urgency).
	if slices.Contains(peasantA.Plan.Actions, "Take") {
		t.Errorf("Honest Peasant (Honesty=0.80) planned Take — conscience should hold at all urgencies: plan=%v",
			peasantA.Plan.Actions)
	}
	t.Logf("Honest Peasant: plan=%v satiety=%.4f (endures famine)", peasantA.Plan.Actions, peasantA.NeedIntensities["Satiety"])

	// Determinism (D12).
	if dA, dB := worldDigest(worldA), worldDigest(worldB); dA != dB {
		t.Errorf("DETERMINISM FAILED (Cassandra honest Peasant test)")
	}
}

// ── Test 4: Cassandra determinism across full run ─────────────────────────────

// TestCassandra_Determinism verifies that the full Cassandra scenario (two agents with
// intelligence gap + berry bush placement) is byte-identical across two runs (D12).
func TestCassandra_Determinism(t *testing.T) {
	const seed = int64(8383)

	run := func() *World {
		fx := newCassandraFixture(t, seed)
		cfg := robinHoodAgentConfig()

		cassandra := fx.world.Spawn("cassandra", core.Vec2{X: 0, Y: 0}, cfg, rng.New(10))
		seedToM(cassandra, "Intelligence", 0.80)
		cassandra.NeedIntensities["Satiety"] = 0.30

		peasant := fx.world.Spawn("peasant", core.Vec2{X: 5, Y: 0}, cfg, rng.New(20))
		seedToM(peasant, "Intelligence", 0.30)
		seedToM(peasant, "Honesty", 0.48)
		peasant.NeedIntensities["Satiety"] = 0.30

		fx.world.PlaceObject("berry_bush_1", "berry_bush",
			core.Vec2{X: 3, Y: 0},
			map[core.Dimension]float64{"Satiety": 0.50})

		for range 30 {
			fx.world.Tick()
		}
		return fx.world
	}

	if dA, dB := worldDigest(run()), worldDigest(run()); dA != dB {
		t.Errorf("DETERMINISM FAILED (Cassandra full scenario)")
	}
}
