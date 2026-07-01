package world

// Scenario K: 내부고발자의 딜레마 (The Whistleblower's Dilemma)
//
// Core conflict: ToM reputation-propagation power vs. survival instinct.
//
// Setup:
//   - Tyrant (high Strength=0.90, Honesty=0.05): privately commits crimes
//   - Witness: ordinary agent who observed the crime; holds incriminating ToM data
//   - Villagers: initially trust the Tyrant for Safety (RelyOn[Tyrant][Safety]=0.85)
//   - Rebel: emerges as legitimate Safety provider after Tyrant's reputation collapses
//
// Observation points:
//   1. ToM COLLAPSE: After witnessing crime, Witness's ToM[Tyrant][Honesty] drops
//      from ~0.80 (trusted public image) to ~0.02 (credibility destroyed).
//   2. SILENCE vs GOSSIP: The planner chooses between Gossip and StayQuiet based
//      on a courage gate (Honesty×Strength). Fearful agents stay quiet; honest-brave
//      agents spread the truth; very-honest agents speak despite physical weakness.
//   3. GOSSIP PROPAGATION: 1-hop (Witness→V1) and 2-hop (V1→V2) propagation via
//      GossipUpdate. Agents who never received gossip keep their prior belief (V3).
//   4. REBELLION: When 3 of 4 villagers shift RelyOn to the rebel leader after
//      gossip reaches them, relianceScan emits RoleEmerged(rebel, Safety).
//      One loyalist (V4) never received gossip and remains loyal to Tyrant.
//
// Technical rationale:
//   - courage gate: STAT-based (not body:Urgency). Fear is physical vulnerability:
//       Honesty>=0.80 (moral imperative overrides fear — speaks despite weakness)
//       OR (Honesty>=0.60 AND Strength>=0.30) (brave-enough + moderately honest)
//     Fearful Witness: Honesty=0.65, Str=0.10 → neither branch → StayQuiet
//     Brave Witness: Honesty=0.65, Str=0.50 → second branch → Gossip
//     Very Honest Witness: Honesty=0.85, Str=0.10 → first branch → Gossip
//   - Gossip is preferred (effort:none = 0 cost) over StayQuiet (effort:med = 0.50)
//     when the courage gate passes AND near_other is in SatisfiedFacts (someone nearby).
//   - GossipUpdate uses Bayesian shifting: V1's belief drifts toward Witness's
//     evidence (Honesty≈0.02), weighted by trust. 2-hop further dilutes the signal.
//   - Rebellion detection: relianceScan scans per-agent strongest RelyOn per Function.
//     When rebel gets ≥ majority share → RoleEmerged(rebel, Safety).

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
	"github.com/dogring/bdg/engine/mind/values"
	"github.com/dogring/bdg/engine/space/spatial"
)

// -- YAML definitions ------------------------------------------------------------

// whistleblowerActionsYAML: two actions both produce has_TruthObligation.
// Gossip (effort:none = cost 0.0) is preferred over StayQuiet (effort:med = 0.50)
// when the courage gate passes AND near_other is satisfied.
const whistleblowerActionsYAML = `schema_version: 1
actions:
  - id: Gossip
    tags: [ "requires_courage", "effort:none" ]
    requires: [ near_other ]
    produces: [ has_TruthObligation ]
    effect: { TruthObligation: 0.80 }
    duration: 3
  - id: StayQuiet
    tags: [ "effort:med" ]
    produces: [ has_TruthObligation ]
    effect: { TruthObligation: 0.30 }
    duration: 2
`

// whistleblowerGatesYAML: courage gate on the "requires_courage" action tag.
// Two branches model the conflict between moral honesty and physical vulnerability:
//   - Honesty>=0.80: very honest agent; moral imperative overrides physical fear
//   - Honesty>=0.60 AND Strength>=0.30: moderately honest AND physically brave
//
// Note: this is a STAT gate (no body:Urgency). Fear = physical vulnerability (low
// Strength), not urgency level. High urgency to share truth doesn't overcome a
// weak body's fear of retaliation — only exceptional Honesty does.
const whistleblowerGatesYAML = `schema_version: 3
gates:
  - id: courage
    tags: [ "requires_courage" ]
    expr:
      or:
        - { stat: Honesty, op: ">=", value: 0.80 }
        - and:
            - { stat: Honesty, op: ">=", value: 0.60 }
            - { stat: Strength, op: ">=", value: 0.30 }
`

// whistleblowerNeedsYAML: single consumable need — TruthObligation.
// When intensity < setpoint (0.55), the agent feels compelled to share truth.
const whistleblowerNeedsYAML = `schema_version: 1
needs:
  - id: TruthObligation
    kind: consumable
    default: { posture: MaintainAbove, setpoint: 0.55, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
`

// whistleblowerBalanceYAML: maps TruthObligation to a weight and a decay rate.
const whistleblowerBalanceYAML = `needs:
  TruthObligation: { decay_per_tick: 0.0001, satisfaction_threshold: 0.55 }
values:
  weights:
    TruthObligation: 1.00
`

// -- Fixture ---------------------------------------------------------------------

// newWhistleblowerFixture builds a World for the planner-based test (Test 2 only).
// UrgencyThreshold=2.0 disables gate relaxation so the courage gate is a hard-block.
func newWhistleblowerFixture(t *testing.T, seed int64) *testFixture {
	t.Helper()

	statReg, err := stats.Load(strings.NewReader(robinHoodStatsYAML))
	if err != nil {
		t.Fatalf("stats.Load: %v", err)
	}
	needReg, err := needs.Load(
		strings.NewReader(whistleblowerNeedsYAML),
		strings.NewReader(whistleblowerBalanceYAML),
	)
	if err != nil {
		t.Fatalf("needs.Load: %v", err)
	}
	valsCfg, err := values.Load(strings.NewReader(whistleblowerBalanceYAML))
	if err != nil {
		t.Fatalf("values.Load: %v", err)
	}
	actReg, err := actions.Load(strings.NewReader(whistleblowerActionsYAML))
	if err != nil {
		t.Fatalf("actions.Load: %v", err)
	}
	gateReg, err := gates.Load(strings.NewReader(whistleblowerGatesYAML), statReg)
	if err != nil {
		t.Fatalf("gates.Load: %v", err)
	}

	plannerCfg := planner.PlannerConfig{
		Budget:             planner.Budget{MaxDepth: 4, MaxActions: 8, MaxNodes: 128},
		BaseHorizonTicks:   720,
		UrgencyThreshold:   2.0, // disables gate relaxation — courage is a hard-block
		LookaheadThreshold: 0.4,
		TagCosts: map[core.Tag]float64{
			"effort:none": 0.0,
			"effort:low":  0.20,
			"effort:med":  0.50,
			"effort:high": 0.80,
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

// -- Test 1: ToM Collapse After Crime --------------------------------------------

// TestWhistleblower_ToMCollapseAfterCrime verifies that witnessing the Tyrant's crime
// causes the Witness's ToM[Tyrant][Honesty] to collapse from ~0.80 (public image)
// to ~0.02 (credibility destroyed).
//
// Mechanism: seedOtherToM uses 60 Observe calls with Bayesian update (beta≈0.08).
// Starting from any prior, 60 observations at value V converge the mean to ≈V.
// First seeding (0.80) establishes public trust. Second seeding (0.02) models
// witnessing the crime — repeated evidence that Tyrant is dishonest.
//
// After the collapse: Witness also removes reliance on Tyrant for Safety
// (no longer trusts the Tyrant to protect them).
func TestWhistleblower_ToMCollapseAfterCrime(t *testing.T) {
	fx := newFixtureSeeded(t, 8001)
	cfg := agent.DefaultConfig()

	tyrantID := core.AgentID("tyrant")
	witnessID := core.AgentID("witness")

	fx.world.Spawn(tyrantID, core.Vec2{X: 20}, cfg, rng.New(10))
	witness := fx.world.Spawn(witnessID, core.Vec2{X: 0}, cfg, rng.New(11))

	// Establish Tyrant's public image: Witness initially trusts Tyrant.
	seedOtherToM(witness, tyrantID, "Honesty", 0.80)
	before, _ := witness.ToM.Self(tyrantID)
	beforeHonesty := before.EstStats["Honesty"].Mean

	t.Logf("Tyrant's ToM[Honesty] BEFORE crime: %.4f (public image: trusted)", beforeHonesty)

	if beforeHonesty < 0.60 {
		t.Errorf("initial trust should be high (≥0.60), got %.4f", beforeHonesty)
	}

	// Witness observes crime: 60 observations of actual Honesty=0.02.
	// Fully converges belief toward 0.02 regardless of prior.
	seedOtherToM(witness, tyrantID, "Honesty", 0.02)
	after, _ := witness.ToM.Self(tyrantID)
	afterHonesty := after.EstStats["Honesty"].Mean

	t.Logf("Tyrant's ToM[Honesty] AFTER crime: %.4f (reputation destroyed)", afterHonesty)
	t.Logf("Collapse magnitude: %.4f → %.4f (drop of %.4f)", beforeHonesty, afterHonesty, beforeHonesty-afterHonesty)

	if afterHonesty >= 0.20 {
		t.Errorf("Tyrant's reputation should collapse after crime (expect <0.20), got %.4f", afterHonesty)
	}

	// Witness also removes Safety reliance on Tyrant (can't be trusted to protect).
	witness.ToM.AdjustRelyOn(tyrantID, core.FuncSafety, -1.0)

	relyAfter := witness.ToM
	_ = relyAfter // reliance cleared; verified by subsequent rebellion test
	t.Logf("Witness removed Safety reliance on Tyrant — no longer counts as protector")
}

// -- Test 2: Silence vs Gossip (Planner Decision) --------------------------------

// TestWhistleblower_SilenceVsGossip verifies three Witness personas:
//
//	A. FEARFUL (Honesty=0.65, Strength=0.10): courage gate fails on both branches
//	   → Gossip invisible (hard-block, UrgencyThreshold=2.0) → plans StayQuiet
//
//	B. BRAVE (Honesty=0.65, Strength=0.50): second branch passes (0.65≥0.60 AND 0.50≥0.30)
//	   → Gossip visible (cost=0.0) < StayQuiet (cost=0.50) → plans Gossip
//
//	C. VERY HONEST (Honesty=0.85, Strength=0.10): first branch passes (0.85≥0.80)
//	   → Gossip visible despite physical weakness → plans Gossip
//
// A Villager is spawned near the Witness in all sub-cases so that near_other is
// in SatisfiedFacts (someone to gossip to). Without near_other, Gossip is
// unplannable regardless of courage (requires: [near_other]).
func TestWhistleblower_SilenceVsGossip(t *testing.T) {
	const seed = int64(8002)

	// spawns a Witness with the given stats and a nearby Villager, then ticks.
	// Returns (world, witness) for assertion.
	run := func(honesty, strength float64) (*World, *agent.Agent) {
		fx := newWhistleblowerFixture(t, seed)

		// Villager nearby (dist=3 < interactionRadius=5) → near_other fires for Witness.
		villager := fx.world.Spawn("villager", core.Vec2{X: 3}, robinHoodAgentConfig(), rng.New(20))
		villager.NeedIntensities["TruthObligation"] = 0.10 // low need; just here to provide near_other

		witness := fx.world.Spawn("witness", core.Vec2{X: 0}, robinHoodAgentConfig(), rng.New(21))
		seedChain(witness, strength, 0.50, honesty, 0.20) // Aggression seeded low (no crime intent)
		witness.NeedIntensities["TruthObligation"] = 0.50 // below setpoint 0.55 → goal active

		fx.world.Tick()
		return fx.world, witness
	}

	// A. FEARFUL: Honesty=0.65 (≥0.60 OK), Strength=0.10 (< 0.30 FAIL) → neither branch → StayQuiet
	worldA, fearful := run(0.65, 0.10)
	worldA2, _ := run(0.65, 0.10)

	t.Logf("Fearful Witness (Honesty=0.65, Str=0.10): plan=%v coping=%v",
		fearful.Plan.Actions, fearful.Coping)

	if slices.Contains(fearful.Plan.Actions, "Gossip") {
		t.Errorf("FEARFUL Witness should NOT Gossip (courage gate fails: Str=0.10<0.30 AND Honesty=0.65<0.80): plan=%v",
			fearful.Plan.Actions)
	}
	if !slices.Contains(fearful.Plan.Actions, "StayQuiet") {
		t.Errorf("FEARFUL Witness should plan StayQuiet as fallback: plan=%v coping=%v",
			fearful.Plan.Actions, fearful.Coping)
	}

	// B. BRAVE: Honesty=0.65 (≥0.60), Strength=0.50 (≥0.30) → second branch → Gossip
	_, brave := run(0.65, 0.50)
	t.Logf("Brave Witness (Honesty=0.65, Str=0.50): plan=%v coping=%v",
		brave.Plan.Actions, brave.Coping)

	if !slices.Contains(brave.Plan.Actions, "Gossip") {
		t.Errorf("BRAVE Witness (Honesty=0.65≥0.60 AND Str=0.50≥0.30) should plan Gossip: plan=%v coping=%v",
			brave.Plan.Actions, brave.Coping)
	}

	// C. VERY HONEST: Honesty=0.85 (≥0.80) → first branch overrides physical weakness → Gossip
	_, veryHonest := run(0.85, 0.10)
	t.Logf("Very Honest Witness (Honesty=0.85, Str=0.10): plan=%v coping=%v",
		veryHonest.Plan.Actions, veryHonest.Coping)

	if !slices.Contains(veryHonest.Plan.Actions, "Gossip") {
		t.Errorf("VERY HONEST Witness (Honesty=0.85≥0.80) should plan Gossip despite Str=0.10: plan=%v coping=%v",
			veryHonest.Plan.Actions, veryHonest.Coping)
	}

	t.Logf("Courage bifurcation: Honesty=0.65+Str=0.10 → StayQuiet; " +
		"Honesty=0.65+Str=0.50 → Gossip; Honesty=0.85+Str=0.10 → Gossip (conscience override)")

	if dA, dB := worldDigest(worldA), worldDigest(worldA2); dA != dB {
		t.Error("DETERMINISM FAILED (silence vs gossip)")
	}
}

// -- Test 3: Gossip Propagation (1-hop and 2-hop) --------------------------------

// TestWhistleblower_GossipPropagation verifies that incriminating evidence spreads
// through the social graph via GossipUpdate.
//
// Assertion strategy: record each villager's actual belief BEFORE gossip (after
// seeding — the exact value depends on the stats prior and Bayesian update rate),
// then assert that gossip-recipients' beliefs go DOWN relative to their own prior,
// while V3 (isolated) stays exactly at its pre-gossip value.
//
//	Witness → V1 (1-hop): V1's belief drops toward Witness's incriminating evidence
//	V1 → V2 (2-hop): V2's belief also drops, shifted toward V1's already-lower belief
//	V3 (no gossip): belief is identical to pre-gossip snapshot (no mutation)
//	V3 > V1_after > V2_after... OR V3 > V2_after, V3 > V1_after (order depends on
//	  whether V1's shift was stronger than V2's via attenuation)
//
// GossipUpdate: shifts listener's belief toward source.Mean weighted by trustWeight=0.70.
// Because Witness's belief is LOWER than villagers' prior, gossip always reduces beliefs.
func TestWhistleblower_GossipPropagation(t *testing.T) {
	fx := newFixtureSeeded(t, 8003)
	cfg := agent.DefaultConfig()

	tyrantID := core.AgentID("tyrant")
	witnessID := core.AgentID("witness")

	fx.world.Spawn(tyrantID, core.Vec2{X: 50}, cfg, rng.New(30))
	witness := fx.world.Spawn(witnessID, core.Vec2{X: 0}, cfg, rng.New(31))

	// All villagers seeded toward Honesty=0.80 (high trust in Tyrant's public image).
	// Actual converged value depends on stats-registry prior; record it after seeding.
	v1 := fx.world.Spawn("v1", core.Vec2{X: 5}, cfg, rng.New(32))
	v2 := fx.world.Spawn("v2", core.Vec2{X: 10}, cfg, rng.New(33))
	v3 := fx.world.Spawn("v3", core.Vec2{X: 15}, cfg, rng.New(34)) // isolated: never receives gossip

	for _, v := range []*agent.Agent{v1, v2, v3} {
		seedOtherToM(v, tyrantID, "Honesty", 0.80)
	}

	// Record actual pre-gossip beliefs (identical for all three since same seeding).
	v1RefBelief, _ := v1.ToM.Self(tyrantID)
	v1Before := v1RefBelief.EstStats["Honesty"].Mean
	v2RefBelief, _ := v2.ToM.Self(tyrantID)
	v2Before := v2RefBelief.EstStats["Honesty"].Mean
	v3RefBelief, _ := v3.ToM.Self(tyrantID)
	v3Ref := v3RefBelief.EstStats["Honesty"].Mean

	// Witness observes crime: 60 obs at 0.02 from default prior → Bayesian mean drops.
	// The final value depends on the stats-registry default, but it must be LOWER
	// than villagers' prior (otherwise gossip can't shift them downward).
	seedOtherToM(witness, tyrantID, "Honesty", 0.02)
	witnessBelief, _ := witness.ToM.Self(tyrantID)
	witnessHonesty := witnessBelief.EstStats["Honesty"].Mean
	t.Logf("Witness ToM[Tyrant][Honesty]: %.4f (crime evidence, ref=%.4f)", witnessHonesty, v3Ref)

	if witnessHonesty >= v1Before {
		t.Fatalf("precondition: Witness belief (%.4f) must be below V1 prior (%.4f) for gossip to reduce belief",
			witnessHonesty, v1Before)
	}

	// 1-hop: Witness gossips to V1 (trustWeight=0.70).
	v1.ToM.GossipUpdate(tyrantID, witnessBelief, 0.70)
	v1AfterBelief, _ := v1.ToM.Self(tyrantID)
	v1After := v1AfterBelief.EstStats["Honesty"].Mean

	t.Logf("V1 (1-hop): ToM[Tyrant][Honesty] %.4f → %.4f", v1Before, v1After)
	if v1After >= v1Before {
		t.Errorf("1-hop gossip should reduce V1's belief: before=%.4f after=%.4f", v1Before, v1After)
	}

	// 2-hop: V1 passes its updated belief to V2 (trustWeight=0.70).
	v2.ToM.GossipUpdate(tyrantID, v1AfterBelief, 0.70)
	v2AfterBelief, _ := v2.ToM.Self(tyrantID)
	v2After := v2AfterBelief.EstStats["Honesty"].Mean

	t.Logf("V2 (2-hop): ToM[Tyrant][Honesty] %.4f → %.4f", v2Before, v2After)
	if v2After >= v2Before {
		t.Errorf("2-hop gossip should reduce V2's belief: before=%.4f after=%.4f", v2Before, v2After)
	}

	// V3 (no gossip): exact snapshot — no operations done after recording v3Ref.
	v3FinalBelief, _ := v3.ToM.Self(tyrantID)
	v3Final := v3FinalBelief.EstStats["Honesty"].Mean

	t.Logf("V3 (no gossip): ToM[Tyrant][Honesty] = %.4f (unchanged)", v3Final)
	if v3Final != v3Ref {
		t.Errorf("V3 (no gossip) should not change: ref=%.4f final=%.4f", v3Ref, v3Final)
	}

	// Divergence: both gossip recipients end up below the isolated V3.
	if v3Final <= v1After {
		t.Errorf("V3(%.4f) should be higher than V1 post-gossip(%.4f): info isolation should preserve belief",
			v3Final, v1After)
	}
	if v3Final <= v2After {
		t.Errorf("V3(%.4f) should be higher than V2 post-gossip(%.4f): info isolation should preserve belief",
			v3Final, v2After)
	}

	t.Logf("Gossip cascade: Witness(%.4f) → V1(%.4f→%.4f) → V2(%.4f→%.4f) | V3(%.4f unchanged)",
		witnessHonesty, v1Before, v1After, v2Before, v2After, v3Final)
}

// -- Test 4: Rebellion Emergence -------------------------------------------------

// TestWhistleblower_RebellionEmergence verifies the full power-shift cascade:
//
// Phase 1 (Tyrant's rule): 4 villagers all rely on Tyrant for Safety →
//
//	relianceScan emits RoleEmerged(tyrant, Safety) [tyrant holds the role]
//
// Phase 2 (Gossip reaches 3 of 4):
//   - V1, V2, V3: receive 1-hop gossip → Tyrant's credibility collapses in their ToM
//   - V1, V2, V3: AdjustRelyOn(tyrant, Safety, -1.0) AND AdjustRelyOn(rebel, Safety, 0.85)
//   - V4: never received gossip → stays loyal to Tyrant (loyalist)
//
// Phase 3 (Rebellion):
//   - Rebel holds 3/5 = 60% share → relianceScan emits RoleEmerged(rebel, Safety)
//   - Tyrant holds 1/5 = 20% share (only V4 loyal) → no longer majority holder
//   - Rebellion has succeeded: power transferred to the rebel leader
//
// This models how reputation collapse + social network diffusion creates
// the structural condition for political upheaval — even without direct violence.
func TestWhistleblower_RebellionEmergence(t *testing.T) {
	fx := newFixtureSeeded(t, 8004)
	cfg := agent.DefaultConfig()

	tyrantID := core.AgentID("tyrant")
	rebelID := core.AgentID("rebel")
	witnessID := core.AgentID("witness")
	villagerIDs := []core.AgentID{"v1", "v2", "v3", "v4"}

	tyrant := fx.world.Spawn(tyrantID, core.Vec2{X: 50}, cfg, rng.New(40))
	rebel := fx.world.Spawn(rebelID, core.Vec2{X: 60}, cfg, rng.New(41))
	witness := fx.world.Spawn(witnessID, core.Vec2{X: 0}, cfg, rng.New(42))

	_ = tyrant
	_ = rebel

	villagers := make(map[core.AgentID]*agent.Agent)
	for i, id := range villagerIDs {
		v := fx.world.Spawn(id, core.Vec2{X: float64(10 + i*5)}, cfg, rng.New(int64(43+i)))
		villagers[id] = v
	}

	// ── Phase 1: Tyrant's rule ──────────────────────────────────────────────────
	// All 4 villagers strongly rely on Tyrant for Safety.
	// Tyrant and rebel have no reliance on each other (they hold power, not seek it).
	for _, vid := range villagerIDs {
		v := villagers[vid]
		v.ToM.AdjustRelyOn(tyrantID, core.FuncSafety, 0.85)
		v.ToM.AdjustRelyOn(rebelID, core.FuncSafety, 0.05)
		v.ToM.AdjustRelyOn(vid, core.FuncSafety, 0.05)
	}
	// Power figures don't rely on themselves or each other for their own protection.
	tyrant.ToM.AdjustRelyOn(tyrantID, core.FuncSafety, 0.0)
	tyrant.ToM.AdjustRelyOn(rebelID, core.FuncSafety, 0.0)
	rebel.ToM.AdjustRelyOn(tyrantID, core.FuncSafety, 0.0)
	rebel.ToM.AdjustRelyOn(rebelID, core.FuncSafety, 0.0)
	witness.ToM.AdjustRelyOn(tyrantID, core.FuncSafety, 0.80)
	witness.ToM.AdjustRelyOn(rebelID, core.FuncSafety, 0.05)

	fx.emit.events = nil
	fx.world.relianceScan()

	phase1Safety := roleEventsForFunction(fx.emit.events, "Safety")
	if len(phase1Safety) != 1 {
		t.Fatalf("phase 1: expected exactly 1 RoleEmerged(Safety), got %d events", len(phase1Safety))
	}
	phase1Holder := extractString(phase1Safety[0].Payload, "holder")
	if phase1Holder != "tyrant" {
		t.Errorf("phase 1: expected Safety holder=tyrant, got %s", phase1Holder)
	}
	t.Logf("Phase 1 — Tyrant's rule: Safety role → %s (share=%.2f)",
		phase1Holder, phase1Safety[0].Payload.(map[string]any)["reliance_share"])

	// ── Phase 2: Gossip reaches V1, V2, V3 (not V4) ────────────────────────────
	// Witness has crime evidence (Honesty≈0.02 for Tyrant).
	seedOtherToM(witness, tyrantID, "Honesty", 0.02)
	witnessBelief, _ := witness.ToM.Self(tyrantID)

	// V1, V2, V3 receive gossip → Tyrant's credibility collapses.
	gossipRecipients := []core.AgentID{"v1", "v2", "v3"}
	for _, vid := range gossipRecipients {
		v := villagers[vid]
		v.ToM.GossipUpdate(tyrantID, witnessBelief, 0.70)
	}

	// V1, V2, V3 shift their Safety reliance from Tyrant to Rebel.
	for _, vid := range gossipRecipients {
		v := villagers[vid]
		v.ToM.AdjustRelyOn(tyrantID, core.FuncSafety, -1.0) // remove Tyrant reliance
		v.ToM.AdjustRelyOn(rebelID, core.FuncSafety, 0.85)  // transfer to rebel
	}
	// Witness also shifts (direct evidence is most damning).
	witness.ToM.AdjustRelyOn(tyrantID, core.FuncSafety, -1.0)
	witness.ToM.AdjustRelyOn(rebelID, core.FuncSafety, 0.85)

	// V4: LOYAL — never received gossip; still trusts Tyrant.
	// V4's reliance unchanged: AdjustRelyOn(tyrant, Safety, 0.85) from Phase 1.

	// ── Phase 3: Rebellion ───────────────────────────────────────────────────────
	// rebel: v1+v2+v3+witness = 4 agents → share = 4/6 = 0.667 > threshold
	// tyrant: only v4 → share = 1/6 = 0.167 < threshold → loses role
	fx.emit.events = nil
	fx.world.relianceScan()

	phase3Safety := roleEventsForFunction(fx.emit.events, "Safety")
	if len(phase3Safety) != 1 {
		t.Fatalf("phase 3 (rebellion): expected 1 RoleEmerged(Safety) for rebel, got %d", len(phase3Safety))
	}
	rebelHolder := extractString(phase3Safety[0].Payload, "holder")
	if rebelHolder != "rebel" {
		t.Errorf("rebellion: expected Safety role → rebel, got %s (tyrant should have lost majority)",
			rebelHolder)
	}

	share := phase3Safety[0].Payload.(map[string]any)["reliance_share"].(float64)
	t.Logf("Phase 3 — REBELLION: Safety role transferred to %s (share=%.3f)", rebelHolder, share)
	t.Logf("V4 remains loyal to Tyrant (loyalist) — information isolation preserved their prior belief")
	t.Logf("Rebellion succeeded: gossip-driven reputation collapse → power transfer without violence")
}
