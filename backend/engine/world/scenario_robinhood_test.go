package world

// Scenario A — Robin Hood Emergence (자원 독점과 도덕적 딜레마)
//
// 한 에이전트(독점자)가 식량을 선점하고 있다. 평소 높은 도덕성(Honesty=0.48 — 기본 양심
// 임계치 0.40 초과)을 가진 Robin Hood는 배고픔이 한계에 달하기 전까지 절도(Take)를
// 고려하지 않는다. 하지만 Urgency > 0.70에 도달하면 conscience 게이트의 긴급 완화
// 조건이 활성화되어 Take가 가시화된다.
//
// 관찰 포인트:
//  1. 낮은 절박감(intensity=0.20): conscience 게이트가 Take를 차단
//  2. 높은 절박감(intensity=0.50): urgency > 0.70 → conscience 완화 → Take 계획
//  3. 매우 높은 도덕성(Honesty=0.80): 아무리 절박해도 절도 불가
//  4. 결정론 검증 (D12)
//
// 기술 근거:
//   - Urgency = Salience / MaxPossiblePriority = (intensity/threshold) / 1.0
//   - intensity=0.50, threshold=0.55 → urgency ≈ 0.909 > 0.70  (게이트 통과)
//   - intensity=0.20, threshold=0.55 → urgency ≈ 0.364 < 0.70  (게이트 차단)
//   - conscience 게이트: Honesty<0.40 OR Aggression>=0.65 OR
//     (Urgency>0.70 AND (Honesty<0.55 OR Aggression>=0.50))

import (
	"slices"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/kernel/worldtime"
	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/mind/gates"
	"github.com/dogring/bdg/engine/mind/needs"
	"github.com/dogring/bdg/engine/mind/perception"
	"github.com/dogring/bdg/engine/mind/planner"
	"github.com/dogring/bdg/engine/mind/stats"
	"github.com/dogring/bdg/engine/mind/tom"
	"github.com/dogring/bdg/engine/mind/values"
	"github.com/dogring/bdg/engine/space/spatial"
)

// ── Robin Hood fixture YAML ───────────────────────────────────────────────────────

const robinHoodStatsYAML = `schema_version: 1
stats:
  - id: Strength
    kind: capability
    range: [0.0, 1.0]
    default: 0.5
    gen: { dist: normal, mean: 0.5, sd: 0.15 }
    inherit: 0.5
  - id: Agility
    kind: capability
    range: [0.0, 1.0]
    default: 0.5
    gen: { dist: normal, mean: 0.5, sd: 0.15 }
    inherit: 0.5
  - id: Intelligence
    kind: capability
    range: [0.0, 1.0]
    default: 0.5
    gen: { dist: normal, mean: 0.5, sd: 0.15 }
    inherit: 0.5
  - id: Honesty
    kind: disposition
    range: [0.0, 1.0]
    default: 0.6
    gen: { dist: normal, mean: 0.6, sd: 0.2 }
    inherit: 0.4
  - id: Aggression
    kind: disposition
    range: [0.0, 1.0]
    default: 0.4
    gen: { dist: normal, mean: 0.4, sd: 0.2 }
    inherit: 0.4
`

const robinHoodActionsYAML = `schema_version: 1
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
`

// conscience gate: norm:transgressive is gated by Honesty/Aggression baseline
// PLUS the urgency-relief branch (P3 § conscience_urgency_threshold = 0.70).
const robinHoodGatesYAML = `schema_version: 3
gates:
  - id: conscience
    tags: [ "norm:transgressive" ]
    expr:
      or:
        - { stat: Honesty,    op: "<",  value: 0.40 }
        - { stat: Aggression, op: ">=", value: 0.65 }
        - and:
            - { body: Urgency, op: ">",  value: 0.70 }
            - or:
                - { stat: Honesty,    op: "<",  value: 0.55 }
                - { stat: Aggression, op: ">=", value: 0.50 }
`

const robinHoodNeedsYAML = `schema_version: 1
needs:
  - id: Satiety
    kind: consumable
    default: { posture: MaintainAbove, setpoint: 0.55, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
`

const robinHoodBalanceYAML = `needs:
  Satiety: { decay_per_tick: 0.00070, satisfaction_threshold: 0.55 }
values:
  weights:
    Satiety: 1.00
`

// newRobinHoodFixture creates a World with the ethics-scenario registries:
// [0,1]-range stats, Take+Eat+MoveTo actions, conscience gate, Satiety need.
// The agent config uses MaxPossiblePriority=1.0 so urgency ≈ Salience directly.
func newRobinHoodFixture(t *testing.T, seed int64) *testFixture {
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

	actReg, err := actions.Load(strings.NewReader(robinHoodActionsYAML))
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

// seedToM sets the ToM[self] belief for a given stat toward targetVal by
// repeated Observe calls. After 60 updates with beta≈0.08, the mean converges
// to within ~1% of targetVal regardless of the initial spawn estimate.
func seedToM(a *agent.Agent, statID core.StatID, targetVal float64) {
	selfID := a.ToM.SelfID()
	for range 60 {
		a.ToM.Observe(selfID, tom.StatEvidence{
			Stat:     statID,
			Observed: targetVal,
			Weight:   1.0,
			Tick:     1,
		})
	}
}

// robinHoodAgentConfig returns DefaultConfig with MaxPossiblePriority=1.0
// so urgency = Salience directly (no social dampening in single-agent tests).
func robinHoodAgentConfig() agent.Config {
	cfg := agent.DefaultConfig()
	cfg.MaxPossiblePriority = 1.0
	return cfg
}

// ── Test 1: conscience gate BLOCKS Take at low urgency ────────────────────────

// TestRobinHood_ConscienBlocksAtLowNeed verifies that a moral agent (Honesty=0.48,
// above the base barrier 0.40) does NOT plan Take when need intensity is low
// (urgency = 0.364 < 0.70 → urgency-relief branch inactive).
func TestRobinHood_ConscienceBlocksAtLowNeed(t *testing.T) {
	const seed = int64(1337)
	run := func() (*World, *agent.Agent) {
		fx := newRobinHoodFixture(t, seed)
		cfg := robinHoodAgentConfig()

		// Monopolist: nearby (distance=1.0 < interactionRadius=5.0), has inventory.
		monopolist := fx.world.Spawn("monopolist", core.Vec2{X: 1, Y: 0}, cfg, rng.New(11))
		monopolist.Inventory["berries"] = 5 // → has_items tag → owned_by_other fact

		// Robin Hood: midrange Honesty, non-aggressive, low need intensity.
		robinHood := fx.world.Spawn("robin_hood", core.Vec2{X: 0, Y: 0}, cfg, rng.New(22))
		seedToM(robinHood, "Honesty", 0.48)    // above 0.40 (base barrier) but below 0.55 (urgency threshold)
		seedToM(robinHood, "Aggression", 0.20) // below 0.65 (base) and 0.50 (urgency branch)

		// Low need intensity → urgency = (0.20/0.55) / 1.0 = 0.364 < 0.70.
		robinHood.NeedIntensities["Satiety"] = 0.20

		for range 8 {
			fx.world.Tick()
		}
		return fx.world, robinHood
	}

	worldA, robinHoodA := run()
	worldB, _ := run()

	// 1. Take must NOT appear in Robin Hood's plan (conscience blocks it at low urgency).
	if slices.Contains(robinHoodA.Plan.Actions, "Take") {
		t.Errorf("Robin Hood planned Take at low urgency (intensity=0.20) — conscience gate should block it")
	}
	t.Logf("plan at low urgency=%v (intensity=0.20, urgency≈0.364)", robinHoodA.Plan.Actions)

	// 2. Determinism (D12).
	assertWorldDigestsEqual(t, "Robin Hood blocked test", worldA, worldB)
}

// ── Test 2: conscience gate PASSES Take at critical need ─────────────────────

// TestRobinHood_ConsciencePassesAtCriticalNeed verifies that the same moral agent
// DOES plan Take when need intensity is critical (urgency = 0.91 > 0.70 → the
// urgency-relief OR branch activates because Honesty=0.48 < 0.55).
func TestRobinHood_ConsciencePassesAtCriticalNeed(t *testing.T) {
	const seed = int64(2024)
	const ticks = 40

	run := func() (*World, *agent.Agent) {
		fx := newRobinHoodFixture(t, seed)
		cfg := robinHoodAgentConfig()

		// Monopolist: nearby with food.
		monopolist := fx.world.Spawn("monopolist", core.Vec2{X: 1, Y: 0}, cfg, rng.New(11))
		monopolist.Inventory["berries"] = 5

		// Robin Hood: midrange Honesty, desperate need.
		robinHood := fx.world.Spawn("robin_hood", core.Vec2{X: 0, Y: 0}, cfg, rng.New(22))
		seedToM(robinHood, "Honesty", 0.48)
		seedToM(robinHood, "Aggression", 0.20)

		// High need intensity → urgency = (0.50/0.55) / 1.0 ≈ 0.909 > 0.70.
		robinHood.NeedIntensities["Satiety"] = 0.50

		for range ticks {
			fx.world.Tick()
		}
		return fx.world, robinHood
	}

	worldA, robinHoodA := run()
	worldB, _ := run()

	// Check that Robin Hood executed Take at some point by examining final state.
	// Proxy: if Take was executed, Satiety intensity should be lower than start (0.50),
	// OR Robin Hood should have "holding" or need intensity decreased.
	// More robust: check that Take was ever in the plan (plan tracking).
	// Since we can't easily replay, we check the plan at end + need intensity.
	t.Logf("final plan=%v coping=%v satiety_intensity=%.4f",
		robinHoodA.Plan.Actions, robinHoodA.Coping, robinHoodA.NeedIntensities["Satiety"])

	// Plan should contain Take (or have contained it — plan resets on completion).
	// Direction: if Take was executed and Eat followed, Satiety intensity < initial 0.50.
	const initial = 0.50
	final := robinHoodA.NeedIntensities["Satiety"]
	if final >= initial {
		// The intensity didn't decrease. Check if Take is currently in plan (about to execute).
		if !slices.Contains(robinHoodA.Plan.Actions, "Take") {
			t.Errorf("Robin Hood neither executed Take nor has it in plan at critical urgency "+
				"(intensity=0.50 → urgency≈0.909 > 0.70, Honesty=0.48 < 0.55): "+
				"final_intensity=%.4f plan=%v coping=%v",
				final, robinHoodA.Plan.Actions, robinHoodA.Coping)
		}
	} else {
		t.Logf("Robin Hood executed Take+Eat: intensity %.4f → %.4f (Δ=%.4f)",
			initial, final, initial-final)
	}

	// Determinism (D12).
	assertWorldDigestsEqual(t, "Robin Hood unblocked test", worldA, worldB)
}

// ── Test 3: very honest agent never steals ────────────────────────────────────

// TestRobinHood_HighHonesty_NeverSteals verifies that an agent with Honesty=0.80
// (above BOTH 0.40 and 0.55) cannot Take even at maximum urgency. The urgency-relief
// branch requires Honesty < 0.55 OR Aggression >= 0.50; neither is satisfied.
func TestRobinHood_HighHonesty_NeverSteals(t *testing.T) {
	const seed = int64(9999)

	run := func() (*World, *agent.Agent) {
		fx := newRobinHoodFixture(t, seed)
		cfg := robinHoodAgentConfig()

		monopolist := fx.world.Spawn("monopolist", core.Vec2{X: 1, Y: 0}, cfg, rng.New(11))
		monopolist.Inventory["berries"] = 5

		// Very honest agent: Honesty=0.80 blocks both base barrier AND urgency branch.
		saintly := fx.world.Spawn("saintly", core.Vec2{X: 0, Y: 0}, cfg, rng.New(33))
		seedToM(saintly, "Honesty", 0.80)    // > 0.55: urgency-relief branch stays blocked
		seedToM(saintly, "Aggression", 0.20) // < 0.50: aggression arm also inactive

		// Critical need intensity (urgency ≈ 0.91 > 0.70 — urgency-relief fires but
		// both Honesty ≥ 0.55 and Aggression < 0.50 keep the inner OR false).
		saintly.NeedIntensities["Satiety"] = 0.50

		for range 40 {
			fx.world.Tick()
		}
		return fx.world, saintly
	}

	worldA, saintlyA := run()
	worldB, _ := run()

	// Take must NEVER appear in the plan — even at critical urgency.
	if slices.Contains(saintlyA.Plan.Actions, "Take") {
		t.Errorf("Saintly agent (Honesty=0.80) planned Take — conscience should hold at all urgencies")
	}
	t.Logf("saintly plan=%v satiety=%.4f (Take absent: conscience held)", saintlyA.Plan.Actions, saintlyA.NeedIntensities["Satiety"])

	assertWorldDigestsEqual(t, "high-honesty test", worldA, worldB)
}

// ── Test 4: dishonest agent steals without needing urgency ───────────────────

// TestRobinHood_LowHonesty_StealsImmediately verifies that a dishonest agent
// (Honesty=0.30 < 0.40 base barrier) plans Take immediately even at low urgency.
func TestRobinHood_LowHonesty_StealsImmediately(t *testing.T) {
	const seed = int64(777)

	run := func() (*World, *agent.Agent) {
		fx := newRobinHoodFixture(t, seed)
		cfg := robinHoodAgentConfig()

		monopolist := fx.world.Spawn("monopolist", core.Vec2{X: 1, Y: 0}, cfg, rng.New(11))
		monopolist.Inventory["berries"] = 5

		thief := fx.world.Spawn("thief", core.Vec2{X: 0, Y: 0}, cfg, rng.New(44))
		seedToM(thief, "Honesty", 0.30)    // < 0.40 base barrier → Take visible without urgency
		seedToM(thief, "Aggression", 0.20) // below aggressive branch

		// Moderate need (urgency ≈ 0.364 < 0.70), BUT Honesty < 0.40 → base barrier alone
		// makes Take visible.
		thief.NeedIntensities["Satiety"] = 0.20

		for range 15 {
			fx.world.Tick()
		}
		return fx.world, thief
	}

	worldA, thiefA := run()
	worldB, _ := run()

	// Dishonest agent should plan or execute Take at low urgency (base barrier alone activates).
	if !slices.Contains(thiefA.Plan.Actions, "Take") && thiefA.NeedIntensities["Satiety"] >= 0.20 {
		t.Errorf("Dishonest agent (Honesty=0.30 < 0.40) should be able to plan Take even at low urgency: "+
			"plan=%v intensity=%.4f", thiefA.Plan.Actions, thiefA.NeedIntensities["Satiety"])
	}
	t.Logf("dishonest thief: plan=%v satiety=%.4f", thiefA.Plan.Actions, thiefA.NeedIntensities["Satiety"])

	assertWorldDigestsEqual(t, "low-honesty test", worldA, worldB)
}
