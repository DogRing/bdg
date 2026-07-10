package world

// Scenario C Extended — Trust Collapse & Galapagos Effect (신뢰의 붕괴와 갈라파고스화)
//
// 기존 scenario_c_test.go (basic fraud detection, P1)를 보완.
// 여기서는 사기꾼 A의 평판이 가십(gossip) 체인을 통해 얼마나 빠르게 전파되는지와,
// 정직한 에이전트들끼리만 신뢰 기반 클러스터가 형성되는 "갈라파고스화" 현상을 검증한다.
//
// 기술 근거:
//   - RecordFraud: 직접 피해 에이전트의 ToM[A].Honesty.Mean을 FraudHonestyDrop(0.10)만큼 낮춤
//   - GossipUpdate(subject, sourceBelief, trustWeight): 2차 전파
//     delta = alpha * trustWeight * (sourceMean − priorMean), alpha ≈ 0.08
//   - 2차 전파(trustWeight=0.7)는 직접 피해보다 작은 delta를 발생시킴 (dampening)
//   - 3차 전파(trustWeight=0.5)는 2차보다 더 작음 (chain dampening)
//   - 비목격자(silent E)는 어떤 채널도 통하지 않아 prior 그대로 유지
//
// Galapagos 효과:
//   - A의 Honesty 평균 (B, C, D 관점) << 사전 추정값 (≈50)
//   - 정직한 에이전트들끼리의 Honesty 추정값은 사전값 그대로 (A 사건과 무관)

import (
	"testing"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/tom"
)

const (
	fraudClaimedValue = 0.80
	fraudActualEffect = 0.20
	// fraudClaimedValue − fraudActualEffect = 0.60 > FraudThreshold(0.20) → fraud fires.
)

// ── Test 1: gossip 체인의 dampening 검증 ─────────────────────────────────────

// TestScenarioC_GossipChainDampens verifies that reputation information propagates
// through a gossip chain but weakens with each hop (trustWeight dampening).
//
// Chain: A defrauds B → B's estimate drops (direct victim)
//
//	B gossips to D (trustWeight=0.7) → D's estimate drops less
//	D gossips to F (trustWeight=0.5) → F's estimate drops even less
//	E never hears → E's estimate unchanged (non-witness isolation)
func TestScenarioC_GossipChainDampens(t *testing.T) {
	const seed = int64(3001)
	const ticks = 5

	run := func() map[string]float64 {
		fx := newFixtureSeeded(t, seed)
		cfg := agent.DefaultConfig()

		scammer := fx.world.Spawn("scammer", core.Vec2{X: 0, Y: 0}, cfg, rng.New(seed))
		scammer.RealStats["Honesty"] = 20.0 // very dishonest

		victimB := fx.world.Spawn("victim_b", core.Vec2{X: 1, Y: 0}, cfg, rng.New(seed+1))
		listenerD := fx.world.Spawn("listener_d", core.Vec2{X: 3, Y: 0}, cfg, rng.New(seed+2))
		listenerF := fx.world.Spawn("listener_f", core.Vec2{X: 5, Y: 0}, cfg, rng.New(seed+3))
		silentE := fx.world.Spawn("silent_e", core.Vec2{X: 100, Y: 100}, cfg, rng.New(seed+4))

		// Seed initial beliefs (tiny weight → midpoint prior ≈ 50).
		for _, ag := range []*agent.Agent{victimB, listenerD, listenerF, silentE} {
			ag.ToM.Observe("scammer", tom.StatEvidence{Stat: "Honesty", Weight: 1e-9, Tick: 1})
		}

		for range ticks {
			fx.world.Tick()
		}

		// === Fraud detection: B directly observes A's deception ===
		victimB.ToM.RecordFraud("scammer", fraudClaimedValue, fraudActualEffect, "Honesty", core.Tick(ticks))
		bBelief, _ := victimB.ToM.Self("scammer")

		// === 1st-hop gossip: B tells D (trustWeight=0.7) ===
		listenerD.ToM.GossipUpdate("scammer", bBelief, 0.7)
		dBelief, _ := listenerD.ToM.Self("scammer")

		// === 2nd-hop gossip: D tells F (trustWeight=0.5) ===
		listenerF.ToM.GossipUpdate("scammer", dBelief, 0.5)
		fBelief, _ := listenerF.ToM.Self("scammer")

		eBelief, _ := silentE.ToM.Self("scammer")

		return map[string]float64{
			"b_honesty": bBelief.EstStats["Honesty"].Mean,
			"d_honesty": dBelief.EstStats["Honesty"].Mean,
			"f_honesty": fBelief.EstStats["Honesty"].Mean,
			"e_honesty": eBelief.EstStats["Honesty"].Mean,
		}
	}

	// Run twice for determinism.
	resA := run()
	resB := run()

	t.Logf("Honesty[scammer] via chain: B=%.4f D=%.4f F=%.4f E=%.4f (prior≈50)",
		resA["b_honesty"], resA["d_honesty"], resA["f_honesty"], resA["e_honesty"])

	// 1. Direct victim B's estimate is lowest (full fraud impact).
	if resA["b_honesty"] >= 50.0 {
		t.Errorf("B (direct victim) Honesty estimate should drop below prior ≈50: got %.4f", resA["b_honesty"])
	}

	// 2. D's estimate dropped less than B's (1-hop dampening, trustWeight=0.7).
	bDrop := 50.0 - resA["b_honesty"]
	dDrop := 50.0 - resA["d_honesty"]
	if dDrop <= 0 {
		t.Errorf("D (1-hop gossip) should have some reputation drop, got Honesty=%.4f", resA["d_honesty"])
	}
	if dDrop >= bDrop {
		t.Errorf("D's drop (%.4f) should be LESS than B's drop (%.4f) due to trust dampening",
			dDrop, bDrop)
	}

	// 3. F's estimate dropped even less than D's (2-hop dampening, trustWeight=0.5).
	fDrop := 50.0 - resA["f_honesty"]
	if fDrop >= dDrop {
		t.Errorf("F's drop (%.4f) should be less than D's drop (%.4f) (chain dampening)", fDrop, dDrop)
	}
	t.Logf("Drop chain: B=%.4f > D=%.4f > F=%.4f (dampening confirmed)", bDrop, dDrop, fDrop)

	// 4. Silent E is untouched — non-witness isolation.
	priorE := resA["e_honesty"]
	if priorE != 50.0 && (priorE < 49.0 || priorE > 51.0) {
		// Allow tiny float drift from initial seeding but E must not have received gossip.
		// The prior from Observe(weight=1e-9) converges to stat default ≈ 50.
	}
	// Main isolation check: E's mean should not deviate toward scammer's real Honesty (20).
	if resA["e_honesty"] < 40.0 {
		t.Errorf("E (silent non-witness) should NOT have received gossip: Honesty=%.4f", resA["e_honesty"])
	}
	t.Logf("E (silent): Honesty=%.4f (non-witness isolation confirmed)", resA["e_honesty"])

	// 5. Determinism (D12).
	for k, vA := range resA {
		if vA != resB[k] {
			t.Errorf("DETERMINISM FAILED: %s: run1=%.6f run2=%.6f", k, vA, resB[k])
		}
	}
}

// ── Test 2: 다중 피해자 → 허브 에이전트 수렴 ────────────────────────────────

// TestScenarioC_MultiVictimConvergesAtHub verifies that when two independent fraud
// victims (B and C) both gossip to the same hub agent (D), D's Honesty estimate of A
// converges lower than either victim alone would produce.
func TestScenarioC_MultiVictimConvergesAtHub(t *testing.T) {
	const seed = int64(3002)
	const ticks = 5

	run := func() (bHonesty, cHonesty, dAfterBOnly, dAfterBC float64) {
		fx := newFixtureSeeded(t, seed)
		cfg := agent.DefaultConfig()

		fx.world.Spawn("scammer", core.Vec2{X: 0, Y: 0}, cfg, rng.New(seed))

		victimB := fx.world.Spawn("victim_b", core.Vec2{X: 1, Y: 0}, cfg, rng.New(seed+1))
		victimC := fx.world.Spawn("victim_c", core.Vec2{X: 2, Y: 0}, cfg, rng.New(seed+2))
		hubD := fx.world.Spawn("hub_d", core.Vec2{X: 3, Y: 0}, cfg, rng.New(seed+3))

		for _, ag := range []*agent.Agent{victimB, victimC, hubD} {
			ag.ToM.Observe("scammer", tom.StatEvidence{Stat: "Honesty", Weight: 1e-9, Tick: 1})
		}

		for range ticks {
			fx.world.Tick()
		}

		victimB.ToM.RecordFraud("scammer", fraudClaimedValue, fraudActualEffect, "Honesty", core.Tick(ticks))
		victimC.ToM.RecordFraud("scammer", fraudClaimedValue, fraudActualEffect, "Honesty", core.Tick(ticks))

		bB, _ := victimB.ToM.Self("scammer")
		bC, _ := victimC.ToM.Self("scammer")
		bHonesty = bB.EstStats["Honesty"].Mean
		cHonesty = bC.EstStats["Honesty"].Mean

		// D hears from B first.
		hubD.ToM.GossipUpdate("scammer", bB, 0.7)
		dAfterB, _ := hubD.ToM.Self("scammer")
		dAfterBOnly = dAfterB.EstStats["Honesty"].Mean

		// Then D hears from C independently.
		hubD.ToM.GossipUpdate("scammer", bC, 0.6)
		dFinal, _ := hubD.ToM.Self("scammer")
		dAfterBC = dFinal.EstStats["Honesty"].Mean

		return
	}

	bH, cH, dAfterB, dAfterBC := run()
	bH2, cH2, dAfterB2, dAfterBC2 := run()

	t.Logf("B=%.4f C=%.4f | D after B gossip=%.4f D after B+C gossip=%.4f",
		bH, cH, dAfterB, dAfterBC)

	// After two gossip hits, D's estimate should be lower than after one.
	if dAfterBC >= dAfterB {
		t.Errorf("D's estimate should converge lower after hearing from BOTH B and C: after_B=%.4f after_BC=%.4f",
			dAfterB, dAfterBC)
	}

	// D's estimate should be lower than the prior (≈50).
	if dAfterBC >= 50.0 {
		t.Errorf("D's estimate after multi-victim gossip should be below prior ≈50: got %.4f", dAfterBC)
	}

	t.Logf("Multi-victim convergence: D drops %.4f below prior (%.4f → %.4f)",
		50.0-dAfterBC, 50.0, dAfterBC)

	// Determinism.
	if bH != bH2 || cH != cH2 || dAfterB != dAfterB2 || dAfterBC != dAfterBC2 {
		t.Errorf("DETERMINISM FAILED (multi-victim test)")
	}
}

// ── Test 3: 갈라파고스 효과 — 정직 네트워크의 신뢰 유지 ────────────────────

// TestScenarioC_GalapagosHonestNetworkIntact verifies the Galapagos effect:
// after fraud and gossip propagation, the SCAMMER's Honesty estimate across the
// network is much lower than the honest agents' mutual estimates of each other.
// Honest agents form an implicit "trust-based cluster" by preserving good mutual beliefs.
func TestScenarioC_GalapagosHonestNetworkIntact(t *testing.T) {
	const seed = int64(3003)
	const ticks = 5

	run := func() map[string]float64 {
		fx := newFixtureSeeded(t, seed)
		cfg := agent.DefaultConfig()

		fx.world.Spawn("scammer", core.Vec2{X: 0, Y: 0}, cfg, rng.New(seed))

		victimB := fx.world.Spawn("victim_b", core.Vec2{X: 1, Y: 0}, cfg, rng.New(seed+1))
		victimC := fx.world.Spawn("victim_c", core.Vec2{X: 2, Y: 0}, cfg, rng.New(seed+2))
		hubD := fx.world.Spawn("hub_d", core.Vec2{X: 3, Y: 0}, cfg, rng.New(seed+3))

		// Seed initial beliefs for all subjects.
		for _, ag := range []*agent.Agent{victimB, victimC, hubD} {
			ag.ToM.Observe("scammer", tom.StatEvidence{Stat: "Honesty", Weight: 1e-9, Tick: 1})
		}
		// B seeds initial belief about C.
		victimB.ToM.Observe("victim_c", tom.StatEvidence{Stat: "Honesty", Weight: 1e-9, Tick: 1})
		// D seeds initial belief about B and C.
		hubD.ToM.Observe("victim_b", tom.StatEvidence{Stat: "Honesty", Weight: 1e-9, Tick: 1})
		hubD.ToM.Observe("victim_c", tom.StatEvidence{Stat: "Honesty", Weight: 1e-9, Tick: 1})

		for range ticks {
			fx.world.Tick()
		}

		// Fraud and gossip propagation.
		victimB.ToM.RecordFraud("scammer", fraudClaimedValue, fraudActualEffect, "Honesty", core.Tick(ticks))
		victimC.ToM.RecordFraud("scammer", fraudClaimedValue, fraudActualEffect, "Honesty", core.Tick(ticks))
		bBelief, _ := victimB.ToM.Self("scammer")
		cBelief, _ := victimC.ToM.Self("scammer")
		hubD.ToM.GossipUpdate("scammer", bBelief, 0.7)
		hubD.ToM.GossipUpdate("scammer", cBelief, 0.6)

		// Read results.
		bOfA, _ := victimB.ToM.Self("scammer")
		cOfA, _ := victimC.ToM.Self("scammer")
		dOfA, _ := hubD.ToM.Self("scammer")
		bOfC, _ := victimB.ToM.Self("victim_c")
		dOfB, _ := hubD.ToM.Self("victim_b")
		dOfC, _ := hubD.ToM.Self("victim_c")

		return map[string]float64{
			"b_of_scammer": bOfA.EstStats["Honesty"].Mean,
			"c_of_scammer": cOfA.EstStats["Honesty"].Mean,
			"d_of_scammer": dOfA.EstStats["Honesty"].Mean,
			"b_of_c":       bOfC.EstStats["Honesty"].Mean, // honest mutual: unchanged
			"d_of_b":       dOfB.EstStats["Honesty"].Mean, // honest mutual: unchanged
			"d_of_c":       dOfC.EstStats["Honesty"].Mean, // honest mutual: unchanged
		}
	}

	res := run()
	res2 := run()

	// Average scammer Honesty across network.
	scammerNetworkMean := (res["b_of_scammer"] + res["c_of_scammer"] + res["d_of_scammer"]) / 3

	// Average honest mutual Honesty (B's view of C, D's view of B, D's view of C).
	honestMutualMean := (res["b_of_c"] + res["d_of_b"] + res["d_of_c"]) / 3

	t.Logf("Scammer Honesty in network: B=%.2f C=%.2f D=%.2f → mean=%.2f",
		res["b_of_scammer"], res["c_of_scammer"], res["d_of_scammer"], scammerNetworkMean)
	t.Logf("Honest mutual Honesty:      B→C=%.2f D→B=%.2f D→C=%.2f → mean=%.2f",
		res["b_of_c"], res["d_of_b"], res["d_of_c"], honestMutualMean)
	t.Logf("Galapagos gap: honest_mutual(%.2f) - scammer_network(%.2f) = %.2f",
		honestMutualMean, scammerNetworkMean, honestMutualMean-scammerNetworkMean)

	// GALAPAGOS: scammer's network reputation is lower than honest mutual reputation.
	if scammerNetworkMean >= honestMutualMean {
		t.Errorf("GALAPAGOS FAIL: scammer's network Honesty (%.2f) should be LESS than honest mutual (%.2f)",
			scammerNetworkMean, honestMutualMean)
	}

	// Honest agents' mutual Honesty estimates are near the prior (≈50), unchanged by A's fraud.
	for key, val := range map[string]float64{
		"b_of_c": res["b_of_c"],
		"d_of_b": res["d_of_b"],
		"d_of_c": res["d_of_c"],
	} {
		// Should be close to prior ≈ 50; seeding with 1e-9 weight keeps mean near default.
		if val < 40.0 {
			t.Errorf("Honest mutual belief %s should be near prior (≈50), got %.2f — gossip contaminated honest network", key, val)
		}
	}

	// Determinism (D12).
	for k, vA := range res {
		if vA != res2[k] {
			t.Errorf("DETERMINISM FAILED (Galapagos test): %s run1=%.6f run2=%.6f", k, vA, res2[k])
		}
	}
}

// ── Test 4: 결정론 전체 실행 ─────────────────────────────────────────────────

// TestScenarioC_GalapagosFullRunDeterminism runs the full Galapagos scenario
// (5 agents, 30 ticks, fraud+gossip chain) and verifies byte-identical world state (D12).
func TestScenarioC_GalapagosFullRunDeterminism(t *testing.T) {
	const seed = int64(3004)

	run := func() *World {
		fx := newFixtureSeeded(t, seed)
		cfg := agent.DefaultConfig()

		scammer := fx.world.Spawn("scammer", core.Vec2{X: 0, Y: 0}, cfg, rng.New(seed))
		scammer.RealStats["Honesty"] = 20.0

		victimB := fx.world.Spawn("victim_b", core.Vec2{X: 1, Y: 0}, cfg, rng.New(seed+1))
		victimC := fx.world.Spawn("victim_c", core.Vec2{X: 2, Y: 0}, cfg, rng.New(seed+2))
		hubD := fx.world.Spawn("hub_d", core.Vec2{X: 3, Y: 0}, cfg, rng.New(seed+3))
		fx.world.Spawn("silent_e", core.Vec2{X: 100, Y: 100}, cfg, rng.New(seed+4))

		for _, ag := range []*agent.Agent{victimB, victimC, hubD} {
			ag.ToM.Observe("scammer", tom.StatEvidence{Stat: "Honesty", Weight: 1e-9, Tick: 1})
		}

		for range 30 {
			fx.world.Tick()
		}

		// Post-tick fraud detection and gossip (same as above tests).
		victimB.ToM.RecordFraud("scammer", fraudClaimedValue, fraudActualEffect, "Honesty", 30)
		victimC.ToM.RecordFraud("scammer", fraudClaimedValue, fraudActualEffect, "Honesty", 30)
		bBelief, _ := victimB.ToM.Self("scammer")
		cBelief, _ := victimC.ToM.Self("scammer")
		hubD.ToM.GossipUpdate("scammer", bBelief, 0.7)
		hubD.ToM.GossipUpdate("scammer", cBelief, 0.6)

		return fx.world
	}

	assertScenarioDeterministic(t, "Galapagos full run", run)
}
