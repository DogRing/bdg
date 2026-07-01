package world

// Scenario D — Romeo and Juliet (가치 충돌: 파벌 vs 개인 애착)
//
// A파 멤버 로미오는 B파 멤버 줄리엣에게 매우 높은 Affinity(0.8)를 가지고 있다.
// 줄리엣이 위기에 처하면 로미오의 "care for others" (appraiseOthers) 우선순위가
// 急등하여 combined urgency(= max(self, other) / MaxPossiblePriority)가 conscience
// 게이트 임계값(0.70)을 돌파한다. 같은 상황에서 줄리엣에 무관심한 머큐티오는
// 우선순위가 부스트되지 않아 conscience 게이트가 차단된 채로 유지된다.
//
// 기술 근거:
//   - appraiseOthers(): Affinity > MinCareThreshold(0.30)인 타자에 대해
//     bondMultiplier = 1 + Affinity × BondAffinityGain 만큼 우선순위 부스트
//   - DeriveReferentInput(Other, ...): highForesight 경로에서
//     suffering ≈ mean(1 − stat/100) — [0,100] 스케일 필수
//   - Romeo: Affinity(Juliet)=0.8 → bondMultiplier≈1.5 → otherPriority=2.1 → urgency=0.84
//   - Mercutio: Affinity(Juliet)=0.1 < 0.30(MinCareThreshold) → maxOther=0 → urgency=0.36
//
// ⚠️ 픽스처 설계 주의:
//   - 반드시 [0,100] 스케일 스탯을 사용해야 DeriveReferentInput.Other가 올바르게 동작.
//     (maxVal=100 하드코딩: [0,1] 스케일에서는 모든 에이전트가 최대 고통으로 오판)
//   - conscience 게이트 임계값도 [0,100] 스케일: Honesty<48 대신 Honesty<55 (stat 단위)
//
// 평판 낙하 (발각):
//   - A파 리더가 로미오의 월담을 발견 → AdjustAffinity(romeo, -delta)
//   - 로미오의 줄리엣에 대한 개인 Affinity는 불변 (자신의 ToM은 타인이 수정 불가)

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

// ── Romeo/Juliet fixture ──────────────────────────────────────────────────────
//
// [0,100] stats — required for DeriveReferentInput(Other) to distinguish distress levels.
// conscience gate with [0,100]-scale Honesty/Aggression thresholds.
// Actions: Take (transgressive), Eat, MoveTo, GiveItem.
// Needs: Safety (weight 1.40) + Satiety (weight 1.00).

const romeoStatsYAML = `schema_version: 1
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

const romeoActionsYAML = `schema_version: 1
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
  - id: GiveItem
    tags: [ "social", "cooperative", "effort:low" ]
    requires: [ near_other, holding ]
    produces: [ transferred ]
    duration: 2
`

// Conscience gate with [0,100]-scale stat thresholds.
// Body scalars (Urgency, Stamina, Mood, Adrenaline) are always [0,1].
//
// Gate logic:
//
//	Honesty < 40    → base barrier (very dishonest, no conscience)
//	Aggression ≥ 65 → base barrier (very aggressive)
//	Urgency > 0.70 AND (Honesty < 55 OR Aggression ≥ 50) → urgency-relief branch
const romeoGatesYAML = `schema_version: 3
gates:
  - id: conscience
    tags: [ "norm:transgressive" ]
    expr:
      or:
        - { stat: Honesty,    op: "<",  value: 40 }
        - { stat: Aggression, op: ">=", value: 65 }
        - and:
            - { body: Urgency, op: ">",  value: 0.70 }
            - or:
                - { stat: Honesty,    op: "<",  value: 55 }
                - { stat: Aggression, op: ">=", value: 50 }
`

const romeoNeedsYAML = `schema_version: 1
needs:
  - id: Safety
    kind: conditional
    default: { posture: PreventBelow, setpoint: 0.60, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
  - id: Satiety
    kind: consumable
    default: { posture: MaintainAbove, setpoint: 0.55, referent: Self }
    salience: { curve: deficit, gain: 1.0 }
`

const romeoBalanceYAML = `needs:
  Satiety: { decay_per_tick: 0.00070, satisfaction_threshold: 0.55 }
values:
  weights:
    Safety:  1.40
    Satiety: 1.00
`

// romeoAgentConfig returns an agent config tuned for the Romeo scenario.
//   - BondAffinityGain = 0.625 → bondMultiplier = 1.5 at Affinity 0.8
//   - MaxPossiblePriority = 2.5 (default; urgency = priority / 2.5)
//   - MinCareThreshold = 0.30 → Affinity must exceed this to trigger appraiseOthers
func romeoAgentConfig() agent.Config {
	cfg := agent.DefaultConfig()
	cfg.BondAffinityGain = 0.625
	cfg.MaxPossiblePriority = 2.5
	cfg.MinCareThreshold = 0.30
	return cfg
}

// newRomeoFixture creates a world with [0,100] stats, conscience gate, Safety+Satiety needs.
func newRomeoFixture(t *testing.T, seed int64) *testFixture {
	t.Helper()

	statReg, err := stats.Load(strings.NewReader(romeoStatsYAML))
	if err != nil {
		t.Fatalf("stats.Load: %v", err)
	}
	needReg, err := needs.Load(
		strings.NewReader(romeoNeedsYAML),
		strings.NewReader(romeoBalanceYAML),
	)
	if err != nil {
		t.Fatalf("needs.Load: %v", err)
	}
	valsCfg, err := values.Load(strings.NewReader(romeoBalanceYAML))
	if err != nil {
		t.Fatalf("values.Load: %v", err)
	}
	actReg, err := actions.Load(strings.NewReader(romeoActionsYAML))
	if err != nil {
		t.Fatalf("actions.Load: %v", err)
	}
	gateReg, err := gates.Load(strings.NewReader(romeoGatesYAML), statReg)
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

// seedJulietSuffering seeds low stats into `observer`'s ToM about Juliet, making
// Juliet appear to be in deep distress from `observer`'s perspective.
// Low stats (≈20/100) → suffering ≈ 0.80 via DeriveReferentInput(Other) high-foresight.
func seedJulietSuffering(observer *agent.Agent, julietID core.AgentID) {
	lowStats := map[core.StatID]float64{
		"Strength": 10, "Agility": 5, "Intelligence": 20, "Honesty": 80,
	}
	for range 50 {
		for statID, val := range lowStats {
			observer.ToM.Observe(julietID, tom.StatEvidence{
				Stat: statID, Observed: val, Weight: 1.0, Tick: 1,
			})
		}
	}
}

// ── Test 1: Romeo의 Affinity가 urgency를 부스트하여 conscience 게이트를 통과 ─────

// TestScenarioD_RomeoUrgencyBoostsConscienceBypass verifies that Romeo's high
// Affinity toward Juliet (who appears to be suffering) raises the combined urgency
// proxy above the conscience gate threshold (0.70), enabling Take at moderate
// self-need — whereas Mercutio (low Affinity) cannot plan Take at the same level.
//
// Urgency math (with BondAffinityGain=0.625, MaxPossiblePriority=2.5):
//
//	Romeo.appraiseOthers: Juliet suffering≈0.80, Safety weight=1.40, bondMult=1.5
//	  → priority = 1.0 × 1.40 × 1.5 = 2.10 → urgency = 2.10/2.5 = 0.84 > 0.70 ✓
//	Mercutio.appraiseOthers: Juliet filtered (Affinity=0.1 < MinCareThreshold=0.30)
//	  → maxOther = 0 → urgency = selfPriority/2.5 ≈ 0.25 < 0.70 → conscience blocks ✓
func TestScenarioD_RomeoUrgencyBoostsConscienceBypass(t *testing.T) {
	const seed = int64(4001)
	const ticks = 40

	run := func() (*World, *agent.Agent, *agent.Agent) {
		fx := newRomeoFixture(t, seed)
		cfg := romeoAgentConfig()

		// Monopolist: has food (owned_by_other when near others with inventory).
		monopolist := fx.world.Spawn("monopolist", core.Vec2{X: 1, Y: 0}, cfg, rng.New(11))
		monopolist.Inventory["berries"] = 5

		// Romeo: moderate Honesty (48 — between base barrier 40 and urgency branch 55).
		romeo := fx.world.Spawn("romeo", core.Vec2{X: 0, Y: 0}, cfg, rng.New(22))
		// Seed Romeo's ToM[self] Honesty ≈ 48 (not low enough for base barrier, but below urgency branch).
		selfID := romeo.ToM.SelfID()
		for range 60 {
			romeo.ToM.Observe(selfID, tom.StatEvidence{Stat: "Honesty", Observed: 48, Weight: 1.0, Tick: 1})
			romeo.ToM.Observe(selfID, tom.StatEvidence{Stat: "Aggression", Observed: 20, Weight: 1.0, Tick: 1})
			romeo.ToM.Observe(selfID, tom.StatEvidence{Stat: "Intelligence", Observed: 80, Weight: 1.0, Tick: 1})
		}

		// Juliet: spawned in world (nearby), appears deeply distressed to Romeo.
		juliet := fx.world.Spawn("juliet", core.Vec2{X: 2, Y: 0}, cfg, rng.New(33))
		seedJulietSuffering(romeo, juliet.ToM.SelfID())

		// Romeo cares deeply about Juliet.
		romeo.ToM.AdjustAffinity(juliet.ToM.SelfID(), 0.8) // > MinCareThreshold(0.30)

		// Romeo's OWN Satiety is moderate — urgency from SELF alone ≈ 0.25 < 0.70.
		romeo.NeedIntensities["Satiety"] = 0.35

		// Mercutio: same Honesty and self-need, but does NOT care about Juliet.
		mercutio := fx.world.Spawn("mercutio", core.Vec2{X: 0, Y: 1}, cfg, rng.New(44))
		for range 60 {
			mercutio.ToM.Observe(mercutio.ToM.SelfID(), tom.StatEvidence{Stat: "Honesty", Observed: 48, Weight: 1.0, Tick: 1})
			mercutio.ToM.Observe(mercutio.ToM.SelfID(), tom.StatEvidence{Stat: "Aggression", Observed: 20, Weight: 1.0, Tick: 1})
		}
		seedJulietSuffering(mercutio, juliet.ToM.SelfID())
		mercutio.ToM.AdjustAffinity(juliet.ToM.SelfID(), 0.1) // < MinCareThreshold(0.30)
		mercutio.NeedIntensities["Satiety"] = 0.35            // same self-need as Romeo

		for range ticks {
			fx.world.Tick()
		}
		return fx.world, romeo, mercutio
	}

	worldA, romeoA, mercutioA := run()
	worldB, _, _ := run()

	// Romeo: should have Take in plan (conscience bypassed via Juliet's suffering)
	// OR have already executed Take (satiety decreased from 0.35).
	romeoPlanHasTake := slices.Contains(romeoA.Plan.Actions, "Take")
	romeoTookAlready := romeoA.NeedIntensities["Satiety"] < 0.35
	romeoActed := romeoPlanHasTake || romeoTookAlready

	if !romeoActed {
		t.Errorf("Romeo (Affinity(Juliet)=0.8, urgency≈0.84>0.70) should plan/execute Take "+
			"(conscience gate bypassed): plan=%v satiety=%.4f (started 0.35)",
			romeoA.Plan.Actions, romeoA.NeedIntensities["Satiety"])
	} else {
		t.Logf("Romeo acted (conscience bypassed): plan=%v satiety=%.4f",
			romeoA.Plan.Actions, romeoA.NeedIntensities["Satiety"])
	}

	// Mercutio: should NOT have Take in plan (conscience blocks without caring about Juliet).
	if slices.Contains(mercutioA.Plan.Actions, "Take") {
		t.Errorf("Mercutio (Affinity(Juliet)=0.1 < MinCareThreshold=0.30) should NOT plan Take "+
			"— conscience gate should hold when not caring about Juliet: plan=%v",
			mercutioA.Plan.Actions)
	} else {
		t.Logf("Mercutio blocked: plan=%v satiety=%.4f", mercutioA.Plan.Actions, mercutioA.NeedIntensities["Satiety"])
	}

	// Determinism (D12).
	if dA, dB := worldDigest(worldA), worldDigest(worldB); dA != dB {
		t.Errorf("DETERMINISM FAILED (Romeo urgency bypass test)")
	}
}

// ── Test 2: 로미오의 파벌 내 평판 낙하 (발각) ─────────────────────────────────

// TestScenarioD_ReputationFallout verifies the "caught" consequence:
// when Faction A leader discovers Romeo's bond with Juliet (a Faction B member),
// the leader's Affinity toward Romeo drops (social punishment).
// Romeo's own Affinity toward Juliet remains unchanged (private belief).
func TestScenarioD_ReputationFallout(t *testing.T) {
	const seed = int64(4002)

	run := func() (leaderAffinityBefore, leaderAffinityAfter, romeoAffinityTowardJuliet float64) {
		fx := newRomeoFixture(t, seed)
		cfg := romeoAgentConfig()

		romeo := fx.world.Spawn("romeo", core.Vec2{X: 0, Y: 0}, cfg, rng.New(11))
		juliet := fx.world.Spawn("juliet", core.Vec2{X: 5, Y: 0}, cfg, rng.New(22))
		leader := fx.world.Spawn("leader_a", core.Vec2{X: -2, Y: 0}, cfg, rng.New(33))

		// Romeo secretly bonds with Juliet (high Affinity in Romeo's private ToM).
		romeo.ToM.AdjustAffinity(juliet.ToM.SelfID(), 0.8)

		// Leader: positive Affinity toward Romeo (faction bond).
		leader.ToM.Observe(romeo.ToM.SelfID(), tom.StatEvidence{Stat: "Honesty", Weight: 1e-9, Tick: 1})
		leader.ToM.AdjustAffinity(romeo.ToM.SelfID(), 0.6) // strong faction loyalty before discovery

		leaderBeliefBefore, _ := leader.ToM.Self(romeo.ToM.SelfID())
		leaderAffinityBefore = leaderBeliefBefore.Affinity

		for range 5 {
			fx.world.Tick()
		}

		// === Discovery event: leader discovers Romeo's bond with Juliet ===
		// Simulates: leader observes Romeo giving resources to Juliet (faction enemy).
		// Social punishment: leader drops Affinity toward Romeo.
		const betrayalPenalty = 0.50
		leader.ToM.AdjustAffinity(romeo.ToM.SelfID(), -betrayalPenalty)

		leaderBeliefAfter, _ := leader.ToM.Self(romeo.ToM.SelfID())
		leaderAffinityAfter = leaderBeliefAfter.Affinity

		// Romeo's own Affinity toward Juliet: unchanged (private belief).
		romeoBeliefOfJuliet, _ := romeo.ToM.Self(juliet.ToM.SelfID())
		romeoAffinityTowardJuliet = romeoBeliefOfJuliet.Affinity

		return
	}

	before, after, romeoToJuliet := run()
	before2, after2, romeoToJuliet2 := run()

	t.Logf("Leader's Affinity toward Romeo: before=%.4f after=%.4f (discovery penalty=−0.50)",
		before, after)
	t.Logf("Romeo's Affinity toward Juliet: %.4f (private, unaffected by leader's discovery)",
		romeoToJuliet)

	// 1. Leader's Affinity toward Romeo drops after discovery.
	if after >= before {
		t.Errorf("Leader's Affinity toward Romeo should DROP after discovery: before=%.4f after=%.4f",
			before, after)
	}
	const expectedDrop = 0.50
	actualDrop := before - after
	if actualDrop < expectedDrop*0.99 {
		t.Errorf("Leader's Affinity drop should be ≈%.2f, got %.4f", expectedDrop, actualDrop)
	}

	// 2. Romeo's Affinity toward Juliet is unchanged (private belief).
	const expectedRomeoAffinity = 0.80
	if romeoToJuliet < expectedRomeoAffinity*0.98 {
		t.Errorf("Romeo's private Affinity toward Juliet should stay ≈%.2f, got %.4f",
			expectedRomeoAffinity, romeoToJuliet)
	}

	// 3. Determinism (D12).
	if before != before2 || after != after2 || romeoToJuliet != romeoToJuliet2 {
		t.Errorf("DETERMINISM FAILED (reputation fallout test): before=%v/%v after=%v/%v romeo=%v/%v",
			before, before2, after, after2, romeoToJuliet, romeoToJuliet2)
	}
}

// ── Test 3: 반대 조건 — 무관심한 A파 멤버는 우선순위 부스트 없음 ──────────────

// TestScenarioD_LowAffinityMemberUnaffected verifies that a Faction A member with
// Affinity(Juliet) = 0.1 (below MinCareThreshold = 0.30) gets NO urgency boost
// from Juliet's suffering — confirming that care for others is gated by Affinity.
func TestScenarioD_LowAffinityMemberUnaffected(t *testing.T) {
	const seed = int64(4003)

	run := func() (*World, *agent.Agent) {
		fx := newRomeoFixture(t, seed)
		cfg := romeoAgentConfig()

		monopolist := fx.world.Spawn("monopolist", core.Vec2{X: 1, Y: 0}, cfg, rng.New(11))
		monopolist.Inventory["berries"] = 5

		juliet := fx.world.Spawn("juliet", core.Vec2{X: 2, Y: 0}, cfg, rng.New(33))

		// Faction A member with low Affinity toward Juliet (indifference / hostility).
		factionMember := fx.world.Spawn("faction_member", core.Vec2{X: 0, Y: 0}, cfg, rng.New(44))
		for range 60 {
			factionMember.ToM.Observe(factionMember.ToM.SelfID(), tom.StatEvidence{
				Stat: "Honesty", Observed: 48, Weight: 1.0, Tick: 1,
			})
			factionMember.ToM.Observe(factionMember.ToM.SelfID(), tom.StatEvidence{
				Stat: "Aggression", Observed: 20, Weight: 1.0, Tick: 1,
			})
		}
		seedJulietSuffering(factionMember, juliet.ToM.SelfID())
		factionMember.ToM.AdjustAffinity(juliet.ToM.SelfID(), 0.1) // < MinCareThreshold
		factionMember.NeedIntensities["Satiety"] = 0.35

		for range 40 {
			fx.world.Tick()
		}
		return fx.world, factionMember
	}

	worldA, memberA := run()
	worldB, _ := run()

	// Faction member must NOT plan Take (conscience blocks without Affinity boost).
	if slices.Contains(memberA.Plan.Actions, "Take") {
		t.Errorf("Faction A member (Affinity(Juliet)=0.1 < MinCareThreshold=0.30) "+
			"should NOT plan Take — urgency from Juliet's suffering should be zero: plan=%v",
			memberA.Plan.Actions)
	} else {
		t.Logf("Faction member (indifferent to Juliet): plan=%v satiety=%.4f (Take blocked)",
			memberA.Plan.Actions, memberA.NeedIntensities["Satiety"])
	}

	// Determinism (D12).
	if dA, dB := worldDigest(worldA), worldDigest(worldB); dA != dB {
		t.Errorf("DETERMINISM FAILED (low-affinity faction member test)")
	}
}

// ── Test 4: 전체 로미오 시나리오 결정론 ─────────────────────────────────────────

// TestScenarioD_FullScenarioDeterminism runs the complete Romeo/Juliet scenario
// (3 agents, 30 ticks, gossip + discovery) and verifies byte-identical state (D12).
func TestScenarioD_FullScenarioDeterminism(t *testing.T) {
	const seed = int64(4004)

	run := func() *World {
		fx := newRomeoFixture(t, seed)
		cfg := romeoAgentConfig()

		monopolist := fx.world.Spawn("monopolist", core.Vec2{X: 1, Y: 0}, cfg, rng.New(11))
		monopolist.Inventory["berries"] = 5

		romeo := fx.world.Spawn("romeo", core.Vec2{X: 0, Y: 0}, cfg, rng.New(22))
		juliet := fx.world.Spawn("juliet", core.Vec2{X: 2, Y: 0}, cfg, rng.New(33))
		leader := fx.world.Spawn("leader_a", core.Vec2{X: -2, Y: 0}, cfg, rng.New(44))

		selfID := romeo.ToM.SelfID()
		for range 60 {
			romeo.ToM.Observe(selfID, tom.StatEvidence{Stat: "Honesty", Observed: 48, Weight: 1.0, Tick: 1})
			romeo.ToM.Observe(selfID, tom.StatEvidence{Stat: "Aggression", Observed: 20, Weight: 1.0, Tick: 1})
			romeo.ToM.Observe(selfID, tom.StatEvidence{Stat: "Intelligence", Observed: 80, Weight: 1.0, Tick: 1})
		}

		seedJulietSuffering(romeo, juliet.ToM.SelfID())
		romeo.ToM.AdjustAffinity(juliet.ToM.SelfID(), 0.8)
		romeo.NeedIntensities["Satiety"] = 0.35

		leader.ToM.Observe(romeo.ToM.SelfID(), tom.StatEvidence{Stat: "Honesty", Weight: 1e-9, Tick: 1})
		leader.ToM.AdjustAffinity(romeo.ToM.SelfID(), 0.6)

		for range 30 {
			fx.world.Tick()
		}

		// Discovery consequence.
		leader.ToM.AdjustAffinity(romeo.ToM.SelfID(), -0.50)

		return fx.world
	}

	if dA, dB := worldDigest(run()), worldDigest(run()); dA != dB {
		t.Errorf("DETERMINISM FAILED (Romeo full scenario)")
	}
}
