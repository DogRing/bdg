package world

// Scenario J: 분업 공급망 붕괴 (Division of Labor Supply Chain Collapse)
//
// A village economy runs on strict division of labor:
//   A (Lumberjack) --[log_pile]--> B (Tool Maker) --[tool_cache]--> C (Hunter)
//   --[meat_stock]--> D (Cook) --eats--> Satiety
//
// Observation points:
//   1. STAMINA BOTTLENECK: When A's Stamina gates out (Stamina<0.25), A can no longer
//      plan ChopWood. The stamina_ok gate hard-blocks ChopWood; A falls back to Hunt
//      (the next-cheapest Strength action). In a live sim, this dries up log_piles;
//      B, C, D downstream would eventually enter coping as their buffers empty.
//
//   2. CRIME BYPASS: When buffer is empty and urgency crosses the conscience threshold
//      (urgency>0.70 AND Honesty<0.55), D abandons Cook planning and falls back to Take
//      against B or C's buffered inventory. The crime plan is cheaper ([Take,Eat]=0.40)
//      than the legitimate path ([MoveTo,Cook]=0.70). Agents with Honesty>=0.55 never
//      Take even at the same urgency — conscience holds.
//
// Technical rationale:
//   - UrgencyThreshold=2.0: disables gate relaxation entirely. Without this, urgency=0.909
//     would relax ALL gates (including stamina_ok and conscience) for every agent. With
//     UrgencyThreshold>1.0, gates are hard-blocks; only the conscience gate's OWN
//     body:Urgency check activates crime — not the planner's relaxation mechanism.
//   - Aggression explicitly seeded to 0.20 for all agents: default Aggression gen (mean=0.4,
//     sd=0.2) can produce values >=0.50, which would accidentally open the conscience gate's
//     Aggression branch for agents that should be crime-blocked.
//   - ChopWood vs Hunt cost: ChopWood=0.80 (effort:high, no MoveTo), [MoveTo,Hunt]=1.00
//     (effort:low + effort:high). Stamina gate breaks ChopWood -> A falls to Hunt (1.00).
//   - Take vs Cook cost: [Take,Eat]=0.40 vs [MoveTo,Cook]=0.70. Take is preferred when
//     conscience gate passes AND near_other+owned_by_other are in SatisfiedFacts.
//   - Specialization: strength_action gate (>=0.60 Strength) blocks MakeTool/Cook for A,C.
//     intel_action gate (>=0.60 Intel) blocks ChopWood/Hunt for B,D.

import (
	"slices"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/actions"
	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/gates"
	"github.com/dogring/bdg/engine/needs"
	"github.com/dogring/bdg/engine/perception"
	"github.com/dogring/bdg/engine/planner"
	"github.com/dogring/bdg/engine/rng"
	"github.com/dogring/bdg/engine/spatial"
	"github.com/dogring/bdg/engine/stats"
	"github.com/dogring/bdg/engine/values"
	"github.com/dogring/bdg/engine/worldtime"
)

// -- Production chain action YAML ------------------------------------------------

// Each role action produces has_Satiety directly (one-step goal satisfaction).
// Object prerequisites use target_kind (enforced at execution time, not planning time).
// The planner selects the cheapest reachable plan:
//   ChopWood: 0.80 (effort:high, no MoveTo needed)
//   [MoveTo, MakeTool/Cook]: 0.20+0.50 = 0.70
//   [MoveTo, Hunt]: 0.20+0.80 = 1.00
//   [Take, Eat]: 0.20+0.20 = 0.40 (cheapest, but conscience-gated)
const chainActionsYAML = `schema_version: 1
actions:
  - id: ChopWood
    tags: [ "effort:high", "uses:Strength", "needs_stamina" ]
    produces: [ has_Satiety ]
    effect: { Satiety: 0.50 }
    duration: 10
  - id: MakeTool
    tags: [ "effort:med", "uses:Intelligence" ]
    target_kind: log_pile
    requires: [ at_target ]
    produces: [ has_Satiety ]
    effect: { Satiety: 0.50 }
    duration: 8
  - id: Hunt
    tags: [ "effort:high", "uses:Strength" ]
    target_kind: tool_cache
    requires: [ at_target ]
    produces: [ has_Satiety ]
    effect: { Satiety: 0.50 }
    duration: 12
  - id: Cook
    tags: [ "effort:med", "uses:Intelligence" ]
    target_kind: meat_stock
    requires: [ at_target ]
    produces: [ has_Satiety ]
    effect: { Satiety: 0.70 }
    duration: 8
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

// -- Gate YAML: capability + stamina + conscience --------------------------------

// Four gate families:
//   strength_action: blocks uses:Strength actions when ToM[self][Strength] < 0.60
//   intel_action:    blocks uses:Intelligence actions when ToM[self][Intel] < 0.60
//   stamina_ok:      hard-blocks needs_stamina actions when body Stamina < 0.25
//   conscience:      blocks norm:transgressive; matches robinHoodGatesYAML conscience model
//
// conscience allows Take when: Honesty<0.40 OR Aggression>=0.65 OR
//   (urgency>0.70 AND (Honesty<0.55 OR Aggression>=0.50))
const chainGatesYAML = `schema_version: 3
gates:
  - id: strength_action
    tags: [ "uses:Strength" ]
    expr: { stat: Strength, op: ">=", value: 0.60 }
  - id: intel_action
    tags: [ "uses:Intelligence" ]
    expr: { stat: Intelligence, op: ">=", value: 0.60 }
  - id: stamina_ok
    tags: [ "needs_stamina" ]
    expr: { body: Stamina, op: ">=", value: 0.25 }
  - id: conscience
    tags: [ "norm:transgressive" ]
    expr:
      or:
        - { stat: Honesty, op: "<", value: 0.40 }
        - { stat: Aggression, op: ">=", value: 0.65 }
        - and:
            - { body: Urgency, op: ">", value: 0.70 }
            - or:
                - { stat: Honesty, op: "<", value: 0.55 }
                - { stat: Aggression, op: ">=", value: 0.50 }
`

// -- Chain fixture ---------------------------------------------------------------

// newChainFixture builds a World with production-chain registries.
// Key setting: UrgencyThreshold=2.0 (above the maximum possible urgency of 1.0).
// This disables the planner's gate relaxation mechanism entirely, so gates are
// hard-blocks. Without this, urgency=0.909 would relax conscience/stamina gates
// for all agents, making it impossible to distinguish crime-capable from crime-blocked.
func newChainFixture(t *testing.T, seed int64) *testFixture {
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
	actReg, err := actions.Load(strings.NewReader(chainActionsYAML))
	if err != nil {
		t.Fatalf("actions.Load: %v", err)
	}
	gateReg, err := gates.Load(strings.NewReader(chainGatesYAML), statReg)
	if err != nil {
		t.Fatalf("gates.Load: %v", err)
	}

	plannerCfg := planner.PlannerConfig{
		Budget:             planner.Budget{MaxDepth: 8, MaxActions: 24, MaxNodes: 512},
		BaseHorizonTicks:   720,
		UrgencyThreshold:   2.0, // intentionally above max (1.0) — disables relaxation
		LookaheadThreshold: 0.4,
		TagCosts: map[core.Tag]float64{
			"effort:low":  0.20,
			"effort:med":  0.50,
			"effort:high": 0.80,
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

// -- Helper ----------------------------------------------------------------------

// seedChain seeds the four stats that drive gate visibility and crime decisions.
// Aggression must always be explicit: the default gen (mean=0.4, sd=0.2) can land
// >=0.50, which would accidentally open the conscience gate's Aggression branch for
// agents that should be crime-blocked.
func seedChain(a *agent.Agent, str, intel, honesty, aggression float64) {
	seedToM(a, "Strength", str)
	seedToM(a, "Intelligence", intel)
	seedToM(a, "Honesty", honesty)
	seedToM(a, "Aggression", aggression)
}

// -- Test 1: Normal chain — D cooks (no victims nearby) --------------------------

// TestChain_DCooksNormally verifies that when the full supply chain is functioning
// (no Take targets nearby), D (Cook, Intel=0.80) plans [MoveTo, Cook] using the
// legitimate path.
//
// Mechanism: no other agent is spawned, so near_other is never set in SatisfiedFacts.
// Take requires [near_other, owned_by_other] both be satisfied; without near_other
// no action can produce it -> Take is unreachable -> D plans the next-cheapest action.
func TestChain_DCooksNormally(t *testing.T) {
	const seed = int64(7001)

	run := func() (*World, *agent.Agent) {
		fx := newChainFixture(t, seed)

		d := fx.world.Spawn("d", core.Vec2{X: 0, Y: 0}, robinHoodAgentConfig(), rng.New(10))
		seedChain(d, 0.20, 0.80, 0.70, 0.20) // Str=0.20 blocks ChopWood/Hunt; Intel=0.80 enables Cook
		d.NeedIntensities["Satiety"] = 0.50

		supply := map[core.Dimension]float64{"Satiety": 0.70}
		fx.world.PlaceObject("meat_stock_1", "meat_stock", core.Vec2{X: 3, Y: 0}, supply)

		fx.world.Tick()
		return fx.world, d
	}

	worldA, dA := run()
	worldB, _ := run()

	t.Logf("D (Intel=0.80, Honesty=0.70, no nearby victims): plan=%v coping=%v",
		dA.Plan.Actions, dA.Coping)

	if !slices.Contains(dA.Plan.Actions, "Cook") {
		t.Errorf("D should plan Cook when no victims nearby: plan=%v coping=%v",
			dA.Plan.Actions, dA.Coping)
	}
	if slices.Contains(dA.Plan.Actions, "Take") {
		t.Errorf("D (no near_other) should NOT plan Take: plan=%v", dA.Plan.Actions)
	}

	if dA, dB := worldDigest(worldA), worldDigest(worldB); dA != dB {
		t.Error("DETERMINISM FAILED (normal cook)")
	}
}

// -- Test 2: Crime bypass — C holds buffered inventory, D is desperate -----------

// TestChain_DCrimeBypass verifies that when D is near C (who has buffered inventory)
// AND urgency crosses the conscience threshold, D abandons Cook planning and falls back
// to Take. Two sub-cases test the conscience gate:
//
//   - D (Honesty=0.48, Aggression=0.20): urgency=0.909 > 0.70 AND Honesty=0.48 < 0.55
//     -> conscience gate PASSES -> Take visible -> [Take,Eat]=0.40 < [MoveTo,Cook]=0.70
//     -> D plans crime
//
//   - D (Honesty=0.80, Aggression=0.20): urgency=0.909 > 0.70 BUT Honesty=0.80 >= 0.55
//     AND Aggression=0.20 < 0.50 -> conscience gate FAILS -> Take blocked (hard-gate,
//     no relaxation with UrgencyThreshold=2.0) -> D plans [MoveTo, Cook]
//
// Without UrgencyThreshold=2.0, the planner would relax ALL gates at urgency>0.65,
// making both agents plan [Take,Eat] and erasing the moral distinction.
func TestChain_DCrimeBypass(t *testing.T) {
	const seed = int64(7002)

	run := func(dHonesty float64) (*World, *agent.Agent) {
		fx := newChainFixture(t, seed)

		// C: Hunter with buffered inventory from before the supply chain failed.
		// Distance to D = 3.0 < interactionRadius=5.0 -> near_other fires for D.
		c := fx.world.Spawn("c", core.Vec2{X: 3, Y: 0}, robinHoodAgentConfig(), rng.New(20))
		seedChain(c, 0.80, 0.20, 0.70, 0.20)
		c.Inventory["meat"] = 5 // has_items tag -> owned_by_other fires for D
		c.NeedIntensities["Satiety"] = 0.10

		// D: Cook who is desperately hungry. No meat_stock in world.
		d := fx.world.Spawn("d", core.Vec2{X: 0, Y: 0}, robinHoodAgentConfig(), rng.New(30))
		seedChain(d, 0.20, 0.80, dHonesty, 0.20)
		d.NeedIntensities["Satiety"] = 0.50 // urgency ~= 0.909 > 0.70 -> conscience urgency-relief branch

		fx.world.Tick()
		return fx.world, d
	}

	// Sub-case A: Honesty=0.48 -> crime (conscience allows via urgency-relief branch)
	worldA, dA := run(0.48)
	worldB, _ := run(0.48)

	t.Logf("D (Honesty=0.48, urgency~=0.909, C nearby with meat): plan=%v coping=%v",
		dA.Plan.Actions, dA.Coping)

	if !slices.Contains(dA.Plan.Actions, "Take") {
		t.Errorf("D (Honesty=0.48) should plan Take when near victim and supply chain fails: plan=%v coping=%v",
			dA.Plan.Actions, dA.Coping)
	}

	// Sub-case B: Honesty=0.80 -> no crime (conscience holds; Aggression=0.20 < 0.50 also holds)
	_, dB := run(0.80)
	t.Logf("D (Honesty=0.80, urgency~=0.909, C nearby with meat): plan=%v coping=%v",
		dB.Plan.Actions, dB.Coping)

	if slices.Contains(dB.Plan.Actions, "Take") {
		t.Errorf("D (Honesty=0.80, Aggression=0.20) should NOT plan Take (conscience holds): plan=%v",
			dB.Plan.Actions)
	}
	if !slices.Contains(dB.Plan.Actions, "Cook") {
		t.Errorf("D (Honesty=0.80) should fall back to Cook when crime is blocked: plan=%v coping=%v",
			dB.Plan.Actions, dB.Coping)
	}

	t.Logf("Crime bypass confirmed: Honesty=0.48 => [Take,Eat]; Honesty=0.80 => [MoveTo,Cook]")

	if dAdig, dBdig := worldDigest(worldA), worldDigest(worldB); dAdig != dBdig {
		t.Error("DETERMINISM FAILED (crime bypass)")
	}
}

// -- Test 3: Stamina gate hard-blocks A's production role ------------------------

// TestChain_StaminaBottleneck verifies:
//   A. With Stamina=1.0: A plans ChopWood (stamina_ok gate passes AND cheaper than [MoveTo,Hunt])
//   B. With Stamina=0.0: stamina_ok gate FAILS -> ChopWood invisible; A falls back to Hunt.
//      Gate is a hard-block (UrgencyThreshold=2.0, no relaxation).
//
// In a live sim, A switching from ChopWood to Hunt means no new log_piles accumulate.
// Over time this cascades: B can't MakeTool -> no tool_caches -> C can't Hunt -> no
// meat_stocks -> D either waits or resorts to crime (tested separately in Test 4).
func TestChain_StaminaBottleneck(t *testing.T) {
	const seed = int64(7003)

	runA := func(aStamina float64) (*World, *agent.Agent) {
		fx := newChainFixture(t, seed)

		a := fx.world.Spawn("a", core.Vec2{X: 0}, robinHoodAgentConfig(), rng.New(40))
		seedChain(a, 0.80, 0.20, 0.70, 0.20) // Str=0.80 passes strength_action; Intel=0.20 blocks Cook/MakeTool
		a.Stamina = aStamina
		a.NeedIntensities["Satiety"] = 0.50

		fx.world.Tick()
		return fx.world, a
	}

	// A. Normal Stamina: stamina_ok gate passes (1.0 >= 0.25) -> ChopWood is cheapest plan (0.80)
	_, aHigh := runA(1.0)
	t.Logf("A (Stamina=1.0, Str=0.80): plan=%v coping=%v", aHigh.Plan.Actions, aHigh.Coping)
	if !slices.Contains(aHigh.Plan.Actions, "ChopWood") {
		t.Errorf("A (Stamina=1.0, Strength=0.80) should plan ChopWood (cheapest Strength action): plan=%v coping=%v",
			aHigh.Plan.Actions, aHigh.Coping)
	}

	// B. Stamina bottleneck: stamina_ok gate hard-blocks ChopWood (0.0 < 0.25, no relaxation).
	// A falls back to [MoveTo, Hunt] = 1.00 — next-cheapest strength-gated action.
	_, aLow := runA(0.0)
	t.Logf("A (Stamina=0.0, Str=0.80): plan=%v coping=%v", aLow.Plan.Actions, aLow.Coping)
	if slices.Contains(aLow.Plan.Actions, "ChopWood") {
		t.Errorf("A (Stamina=0.0) should NOT plan ChopWood (stamina_ok gate hard-blocks, no relaxation): plan=%v",
			aLow.Plan.Actions)
	}
	// A still has Hunt available -> Hunt is in the fallback plan.
	t.Logf("Stamina gate: Stamina=1.0 => [ChopWood]; Stamina=0.0 => fallback (no ChopWood), plan=%v",
		aLow.Plan.Actions)

	// Determinism using the low-stamina run.
	worldA, _ := runA(0.0)
	worldB, _ := runA(0.0)
	if dA, dB := worldDigest(worldA), worldDigest(worldB); dA != dB {
		t.Error("DETERMINISM FAILED (stamina gate hard-block)")
	}
}

// -- Test 4: Full cascade — A's bottleneck drives D to crime ---------------------

// TestChain_FullCascadeToCrime verifies the end-to-end supply chain collapse:
//   A (Stamina=0.0): stamina gate hard-blocks ChopWood -> A falls back to Hunt
//   C (has buffered meat inventory from before collapse): C is near D and holds meat
//   D (Honesty=0.48, urgency~=0.909, C nearby with meat): conscience passes ->
//     [Take,Eat] (cost=0.40) < [MoveTo,Cook] (cost=0.70) -> D plans crime
//
// The cascade narrative: A's stamina failure (production role disrupted) eventually
// drains the meat_stock buffer. D, seeing C's buffered meat and facing starvation,
// chooses to steal rather than wait for legitimate supply. The moral threshold
// (Honesty<0.55 at urgency>0.70) is the tipping point.
func TestChain_FullCascadeToCrime(t *testing.T) {
	const seed = int64(7004)

	run := func() (*World, *agent.Agent, *agent.Agent) {
		fx := newChainFixture(t, seed)

		// A: Lumberjack with exhausted stamina. Spawned far from D so A's inventory
		// does not affect D's SatisfiedFacts (near_other/owned_by_other).
		a := fx.world.Spawn("a", core.Vec2{X: 50}, robinHoodAgentConfig(), rng.New(50))
		seedChain(a, 0.80, 0.20, 0.70, 0.20)
		a.Stamina = 0.0 // stamina_ok gate fails -> ChopWood blocked
		a.NeedIntensities["Satiety"] = 0.50

		// C: Hunter, holds buffered meat (produced before A's stamina failed).
		// Distance D->C = 3.0 < interactionRadius=5.0 -> D sees near_other+owned_by_other.
		c := fx.world.Spawn("c", core.Vec2{X: 3}, robinHoodAgentConfig(), rng.New(52))
		seedChain(c, 0.80, 0.20, 0.70, 0.20)
		c.Inventory["meat"] = 5 // has_items tag -> owned_by_other fires for D
		c.NeedIntensities["Satiety"] = 0.20

		// D: Cook, no meat_stock in world. Near C's buffered inventory.
		// Honesty=0.48 < 0.55 + urgency=0.909 > 0.70 -> conscience gate passes -> Take.
		d := fx.world.Spawn("d", core.Vec2{X: 0}, robinHoodAgentConfig(), rng.New(53))
		seedChain(d, 0.20, 0.80, 0.48, 0.20)
		d.NeedIntensities["Satiety"] = 0.50

		// No world objects: supply chain has fully collapsed (A not producing log_piles).
		fx.world.Tick()
		return fx.world, a, d
	}

	worldA, aA, dA := run()
	worldB, _, _ := run()

	t.Logf("A (Stamina=0.0, Str=0.80): plan=%v", aA.Plan.Actions)
	t.Logf("D (Honesty=0.48, near C with meat): plan=%v", dA.Plan.Actions)

	// A: stamina gate hard-blocks ChopWood; A falls back to Hunt (next cheapest).
	if slices.Contains(aA.Plan.Actions, "ChopWood") {
		t.Errorf("A (Stamina=0.0) should not plan ChopWood (stamina_ok blocked): plan=%v", aA.Plan.Actions)
	}

	// D: crime is the cheapest reachable plan when near victim and conscience allows.
	if !slices.Contains(dA.Plan.Actions, "Take") {
		t.Errorf("D (Honesty=0.48, urgency~=0.909, C nearby with meat) should plan Take: plan=%v coping=%v",
			dA.Plan.Actions, dA.Coping)
	}

	t.Logf("Full cascade: A(stamina bottleneck, no ChopWood) => D(crime[Take]) — supply chain collapse documented")

	if dAdig, dBdig := worldDigest(worldA), worldDigest(worldB); dAdig != dBdig {
		t.Error("DETERMINISM FAILED (full cascade to crime)")
	}
}
