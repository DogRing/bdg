package world

// Scenario G: 허세꾼의 몰락 (The Bluffer's Fall)
//
// An agent whose actual combat ability is poor (Strength=15) but who has
// high self-perceived strength (ToM[self][Strength]=65) and has seeded
// witnesses' beliefs with an even higher estimate (≈85) through boasting.
//
// Observation points:
//  1. When a real threat activates Safety need, can the bluffer plan Patrol?
//     Gate requires Strength ≥ 80. ToM[self]=65 < 80 → gate fails → Flee only.
//     A genuine hero (ToM[self][Strength]=85 ≥ 80) plans Patrol (cheaper than Flee).
//  2. When witnesses see the actual performance (Strength=15), their ToM[bluffer]
//     collapses from ~85 to ~15: the reputation bubble bursts in one reveal.
//  3. Information asymmetry: bluffer's self-estimate (65) lies between their
//     actual ability (15) and witnesses' prior beliefs (85).
//  4. Determinism (D12).
//
// Technical rationale:
//   - dimensionToProducerPredicate("Safety") → "has_Safety"; both Patrol and Flee
//     produce has_Safety. Patrol is effort:low (cost=0.20); Flee is effort:med
//     (cost=0.50). When Patrol gate passes, Patrol wins on cost. When gate fails,
//     Flee is the only option.
//   - Reputation math: beta=0.08; 60 Observe(15) updates from prior≈85 converge
//     to 85 × (0.92)^60 + 15 × (1−0.92^60) ≈ 0.59 + 14.9 ≈ 15.5.
//   - SafetyDim="" in blufferAgentConfig disables driveConditionalSafety so
//     consumable Safety decays normally without a threat-entity scan.

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
	"github.com/dogring/bdg/engine/tom"
	"github.com/dogring/bdg/engine/values"
	"github.com/dogring/bdg/engine/worldtime"
)

// ── Bluffer fixture YAML ──────────────────────────────────────────────────────

// Patrol: low-cost fighting action, gated on Strength (uses:Strength → strength_gate).
// Flee:   medium-cost escape action, no gate — always available.
// Patrol is cheaper than Flee so heroes prefer Patrol; bluffers can only Flee.
const blufferActionsYAML = `schema_version: 1
actions:
  - id: Patrol
    tags: [ "effort:low", "uses:Strength" ]
    produces: [ has_Safety ]
    effect: { Safety: 0.60 }
    duration: 10
  - id: Flee
    tags: [ "effort:med" ]
    produces: [ has_Safety ]
    effect: { Safety: 0.40 }
    duration: 8
  - id: MoveTo
    tags: [ "effort:low", "uses:Agility" ]
    produces: [ at_target ]
    duration: 5
`

// Safety is consumable so it can drive planning without threat entities.
// SafetyDim="" in the agent config prevents driveConditionalSafety from overwriting.
const blufferNeedsYAML = `schema_version: 1
needs:
  - id: Safety
    kind: consumable
    default: { posture: MaintainAbove, setpoint: 0.60, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
  - id: Satiety
    kind: consumable
    default: { posture: MaintainAbove, setpoint: 0.55, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
`

const blufferBalanceYAML = `needs:
  Safety:  { decay_per_tick: 0.00050, satisfaction_threshold: 0.60 }
  Satiety: { decay_per_tick: 0.00070, satisfaction_threshold: 0.55 }
values:
  weights:
    Safety:  1.40
    Satiety: 1.00
`

// Patrol requires Strength ≥ 80 (a real fighter). Bluffer's ToM[self][Strength]=65
// fails this gate, forcing Flee. Hero's ToM[self][Strength]=85 passes.
const blufferGatesYAML = `schema_version: 3
gates:
  - id: fighter_strength
    tags: [ "uses:Strength" ]
    expr: { stat: Strength, op: ">=", value: 80 }
`

// ── Fixture constructor ───────────────────────────────────────────────────────

func newBlufferFixture(t *testing.T, seed int64) *testFixture {
	t.Helper()

	statReg, err := stats.Load(strings.NewReader(testStatsYAML))
	if err != nil {
		t.Fatalf("stats.Load: %v", err)
	}
	needReg, err := needs.Load(
		strings.NewReader(blufferNeedsYAML),
		strings.NewReader(blufferBalanceYAML),
	)
	if err != nil {
		t.Fatalf("needs.Load: %v", err)
	}
	valsCfg, err := values.Load(strings.NewReader(blufferBalanceYAML))
	if err != nil {
		t.Fatalf("values.Load: %v", err)
	}
	actReg, err := actions.Load(strings.NewReader(blufferActionsYAML))
	if err != nil {
		t.Fatalf("actions.Load: %v", err)
	}
	gateReg, err := gates.Load(strings.NewReader(blufferGatesYAML), statReg)
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

// blufferAgentConfig returns DefaultConfig with SafetyDim="" so driveConditionalSafety
// does not fire (Safety is consumable in this fixture, not threat-driven).
func blufferAgentConfig() agent.Config {
	cfg := agent.DefaultConfig()
	cfg.SafetyDim = "" // disable conditional override; Safety decays as consumable
	return cfg
}

// seedOtherToM seeds an observer's ToM estimate of another agent's stat
// via 60 Observe iterations (beta≈0.08), converging from prior (50) toward targetVal.
// Used to simulate "everyone thinks the bluffer is strong" (reputational prior).
func seedOtherToM(observer *agent.Agent, targetID core.AgentID, statID core.StatID, targetVal float64) {
	for range 60 {
		observer.ToM.Observe(targetID, tom.StatEvidence{
			Stat:     statID,
			Observed: targetVal,
			Weight:   1.0,
			Tick:     1,
		})
	}
}

// ── Test 1: Bluffer chooses Flee when Patrol gate fails ──────────────────────

// TestBluffer_ChoosesFlee verifies that an agent whose ToM[self][Strength]=65 (below
// the Patrol gate threshold of 80) cannot plan Patrol and falls back to Flee.
//
// This models the "bluffer" who overestimates their strength (actual=15, self=65)
// but is still caught short when real fighting demands Strength ≥ 80.
// Flee (cost=0.50) is the only Safety producer available when Patrol is gated out.
func TestBluffer_ChoosesFlee(t *testing.T) {
	const seed = int64(4001)

	run := func() (*World, *agent.Agent) {
		fx := newBlufferFixture(t, seed)
		cfg := blufferAgentConfig()

		// Bluffer: actual Strength=15, self-estimates Strength=65 (overconfident).
		bluffer := fx.world.Spawn("bluffer", core.Vec2{X: 0, Y: 0}, cfg, rng.New(10))
		seedToM(bluffer, "Strength", 65) // ToM[self][Strength]=65 < gate threshold 80

		// Safety need is high → Safety goal dominates (priority=0.50×1.40=0.70 > Satiety).
		bluffer.NeedIntensities["Safety"] = 0.50  // critical Safety need
		bluffer.NeedIntensities["Satiety"] = 0.05 // non-urgent

		for range 5 {
			fx.world.Tick()
		}
		return fx.world, bluffer
	}

	worldA, blufferA := run()
	worldB, _ := run()

	// Bluffer must use Flee, not Patrol (gate blocked at 65 < 80).
	hasFlee := slices.Contains(blufferA.Plan.Actions, "Flee") ||
		blufferA.NeedIntensities["Safety"] < 0.50
	hasPatrol := slices.Contains(blufferA.Plan.Actions, "Patrol")

	if hasPatrol {
		t.Errorf("Bluffer (ToM[self][Strength]=65) should NOT plan Patrol (gate requires 80): plan=%v",
			blufferA.Plan.Actions)
	}
	if !hasFlee {
		t.Errorf("Bluffer with critical Safety need should plan or execute Flee: plan=%v safety=%.4f",
			blufferA.Plan.Actions, blufferA.NeedIntensities["Safety"])
	}
	t.Logf("Bluffer (self-Strength=65): plan=%v safety=%.4f → cowardly escape (Patrol gated out)",
		blufferA.Plan.Actions, blufferA.NeedIntensities["Safety"])

	// D12.
	if dA, dB := worldDigest(worldA), worldDigest(worldB); dA != dB {
		t.Error("DETERMINISM FAILED (bluffer chooses flee)")
	}
}

// ── Test 2: Hero's strength passes gate → Patrol chosen ──────────────────────

// TestBluffer_HeroPatrols verifies that an agent with ToM[self][Strength]=85
// (above gate threshold 80) plans Patrol — the more efficient Safety producer
// (cost=0.20 vs Flee's 0.50). This is the contrast case for the bluffer.
func TestBluffer_HeroPatrols(t *testing.T) {
	const seed = int64(4002)

	run := func() (*World, *agent.Agent) {
		fx := newBlufferFixture(t, seed)
		cfg := blufferAgentConfig()

		// Hero: self-estimates Strength=85 ≥ gate threshold 80.
		hero := fx.world.Spawn("hero", core.Vec2{X: 0, Y: 0}, cfg, rng.New(20))
		seedToM(hero, "Strength", 85) // ToM[self][Strength]=85 ≥ 80 → gate passes

		hero.NeedIntensities["Safety"] = 0.50
		hero.NeedIntensities["Satiety"] = 0.05

		for range 5 {
			fx.world.Tick()
		}
		return fx.world, hero
	}

	worldA, heroA := run()
	worldB, _ := run()

	// Hero plans Patrol (cheaper, gate passes).
	heroPatrols := slices.Contains(heroA.Plan.Actions, "Patrol") ||
		heroA.NeedIntensities["Safety"] < 0.50 // executed and succeeded

	if !heroPatrols {
		t.Errorf("Hero (ToM[self][Strength]=85) should plan Patrol (gate requires 80): plan=%v",
			heroA.Plan.Actions)
	}
	if slices.Contains(heroA.Plan.Actions, "Flee") {
		t.Errorf("Hero should prefer cheaper Patrol over Flee: plan=%v", heroA.Plan.Actions)
	}
	t.Logf("Hero (self-Strength=85): plan=%v safety=%.4f → Patrol (strength gate passes)",
		heroA.Plan.Actions, heroA.NeedIntensities["Safety"])

	// D12.
	if dA, dB := worldDigest(worldA), worldDigest(worldB); dA != dB {
		t.Error("DETERMINISM FAILED (hero patrols)")
	}
}

// ── Test 3: Witness reveal bursts the reputation bubble ──────────────────────

// TestBluffer_WitnessRevealBurstsReputation verifies that observers who previously
// believed the bluffer was strong (ToM[bluffer][Strength]≈85) dramatically revise
// their estimate downward when they observe the actual performance (Strength=15).
//
// Reputation math: with beta=0.08 and 60 Observe calls at Observed=15, mean
// converges from 85 to ≈15.5 (drop of ~70 points, > 80% collapse).
func TestBluffer_WitnessRevealBurstsReputation(t *testing.T) {
	fx := newBlufferFixture(t, 4003)
	cfg := blufferAgentConfig()

	blufferID := core.AgentID("bluffer")
	witnessID := core.AgentID("witness")

	fx.world.Spawn(blufferID, core.Vec2{X: 0}, cfg, rng.New(30))
	witness := fx.world.Spawn(witnessID, core.Vec2{X: 5}, cfg, rng.New(31))

	// Prior belief: witness thinks bluffer is strong (the bluffer's boasting worked).
	// seedOtherToM converges witness.ToM[bluffer][Strength] to ≈85.
	seedOtherToM(witness, blufferID, "Strength", 85)

	priorBelief, _ := witness.ToM.Self(blufferID)
	priorMean := priorBelief.EstStats["Strength"].Mean
	t.Logf("Prior witness belief: ToM[bluffer][Strength].Mean=%.2f", priorMean)

	// Bluffer flees instead of fighting — witnesses observe actual Strength=15.
	// 60 Observe calls to represent the clarity of the revelation (everyone saw it).
	seedOtherToM(witness, blufferID, "Strength", 15)

	postBelief, _ := witness.ToM.Self(blufferID)
	postMean := postBelief.EstStats["Strength"].Mean
	t.Logf("Post-reveal witness belief: ToM[bluffer][Strength].Mean=%.2f", postMean)

	// Reputation bubble burst: post-reveal mean should be less than half the prior.
	if postMean >= 0.5*priorMean {
		t.Errorf("Reputation should collapse (>50%% drop): prior=%.2f, post=%.2f",
			priorMean, postMean)
	}

	// Prior should have been high (>= 70) since we seeded with 85.
	if priorMean < 70 {
		t.Errorf("Prior witness belief should be high (bluffer's boasting), got %.2f", priorMean)
	}

	// Post should be near actual Strength=15 (within ±10).
	if postMean > 25 {
		t.Errorf("Post-reveal belief should approach actual Strength=15, got %.2f", postMean)
	}

	t.Logf("Reputation bubble burst: %.2f → %.2f (drop of %.2f points, %.1f%% collapse)",
		priorMean, postMean, priorMean-postMean, (priorMean-postMean)/priorMean*100)
}

// ── Test 4: Determinism ───────────────────────────────────────────────────────

// TestBluffer_Determinism verifies that the full bluffer scenario (flee decision +
// reputation reveal) produces byte-identical results across two runs (D12).
func TestBluffer_Determinism(t *testing.T) {
	const seed = int64(4004)

	run := func() *World {
		fx := newBlufferFixture(t, seed)
		cfg := blufferAgentConfig()

		bluffer := fx.world.Spawn("bluffer", core.Vec2{X: 0}, cfg, rng.New(40))
		seedToM(bluffer, "Strength", 65) // self-estimate: moderately strong but below gate
		bluffer.NeedIntensities["Safety"] = 0.50
		bluffer.NeedIntensities["Satiety"] = 0.05

		hero := fx.world.Spawn("hero", core.Vec2{X: 5}, cfg, rng.New(41))
		seedToM(hero, "Strength", 85) // hero: above gate threshold
		hero.NeedIntensities["Safety"] = 0.50
		hero.NeedIntensities["Satiety"] = 0.05

		// Witnesses with prior high belief about bluffer.
		witness := fx.world.Spawn("witness", core.Vec2{X: 10}, cfg, rng.New(42))
		seedOtherToM(witness, core.AgentID("bluffer"), "Strength", 85)

		for range 30 {
			fx.world.Tick()
		}

		// Post-run reveal.
		seedOtherToM(witness, core.AgentID("bluffer"), "Strength", 15)

		return fx.world
	}

	if dA, dB := worldDigest(run()), worldDigest(run()); dA != dB {
		t.Error("DETERMINISM FAILED (bluffer scenario)")
	} else {
		t.Log("DETERMINISM PASSED: bluffer flee + reputation reveal byte-identical")
	}
}
