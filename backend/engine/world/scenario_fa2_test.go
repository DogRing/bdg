package world

import (
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/mind/actions"
)

// ── FA2 — predator hunt via scent + FOV; the chase reads as drama, not a stalemate ──
//
// docs/scenarios-world.md FA2 + docs/fauna.md 클러스터10 (M1/M2/M6). Regression coverage for the
// Phase 10 fix: the wolf `Attack` §6 utility now carries an `is_current` stickiness term (F30/R5)
// so that once a predator wins the Attack/Hunt tie at melee range it does not flicker back to Hunt
// every other tick (which starved damage exchanges and produced the reported "perpetual parallel
// walk" — 200+ ticks of Hunt with distance never closing, no kill, no drama). The Hunt/Attack utility
// shapes below mirror content/objects.yaml's wolf block (§6 coefficients), including the fix.
//
// installFA2ActionRegistry declares the real content tags that matter here: Hunt is effort:high (so
// applyAnimalFatigue accrues M2 endurance while chasing) and Attack is combat:attack.
func installFA2ActionRegistry(t *testing.T, fx *testFixture) {
	t.Helper()
	reg, err := actions.Load(strings.NewReader(`schema_version: 1
actions:
  - id: Hunt
    tags: [effort:high, seek:prey]
    duration: 1
    produces: [near_other]
  - id: Attack
    tags: [effort:high, combat:attack]
    duration: 1
    produces: [near_other]
  - id: Flee
    tags: [effort:high, flee:predator]
    duration: 1
    produces: [safe]
  - id: MoveTo
    tags: [effort:low]
    duration: 1
    produces: [at_target]
`))
	if err != nil {
		t.Fatalf("actions.Load FA2 registry: %v", err)
	}
	fx.world.actReg = reg
	fx.actReg = reg
	fx.svc.Actions = reg
	fx.world.svc.Actions = reg
}

// fa2Rules builds a wolf+deer pair whose §6 shapes mirror content/objects.yaml (Hunt/Attack utility,
// speed-boost-on-sight, is_current stickiness). Wide deer FOV/high smell keep the test about the
// CHASE mechanics (approach → engage → resolve), not FOV/scent-geometry precision (separately covered
// by TestSightFOVQuery / TestSpreadDiffusion). DisengageRangeFactor is generous so that any disengage
// observed is attributable to stamina (M2/FC6), never to the target merely outrunning the freeze.
func fa2Rules(t *testing.T) *fauna.Rules {
	t.Helper()
	return fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		"deer": {
			Utilities: map[actions.ActionID]*expr.Program{
				"Flee":   testNumProgram(t, "sight.predator * 10"),
				"MoveTo": testNumProgram(t, "0.1"),
			},
			Drives: []fauna.DriveRule{},
			Speed:  testNumProgram(t, "0.3 + sight.predator * 0.6"), // graze-wander 0.3 vs flee 0.9 (3x, FA2 "clear factor")
			Tags:   []core.Tag{"game"},
			SteerChannel: map[actions.ActionID]core.Tag{
				"Flee": fauna.TagFleePred,
			},
			SmellRadius: 1, SightRadius: 12, FovArc: 3.0, // wide FOV, generous range: isolates chase mechanics from bearing geometry
		},
		"wolf": {
			Utilities: map[actions.ActionID]*expr.Program{
				// Scent-GATED Hunt (content mirror, Phase 10): at zero scent a hungry
				// wolf scores 0.9*0.09=0.081 < MoveTo 0.1 → WANDERS (heading
				// re-randomizes → plume re-acquisition) instead of blind-running its
				// last heading out of the world — the second half of the "perpetual
				// parallel walk" pathology (the diag trace showed the wolf overshooting
				// east forever after losing the deer, dist 8.6 → 45).
				"Hunt": testNumProgram(t, "hunger * (0.09 + scent.prey * 1.2)"),
				// Mirrors content/objects.yaml's wolf Attack (hunger/scent.prey/dist.prey/target.threat
				// shape) + the Phase 10 is_current fix. The dist.prey coefficient (0.06, re-scaled for
				// this test's compact geometry) is what keeps Attack BELOW Hunt while still approaching
				// (so Hunt's TagSteerPrey scent-homing keeps driving real pursuit, not a coincidental
				// straight line) and only lets Attack overtake Hunt once genuinely close.
				// Content shape with the dist.prey coefficient re-derived for THIS geometry.
				// dist.prey is the COARSE scent-derived estimate smellRadius/(1+scent) — with
				// SmellRadius 30 and the observed near-source intensity ~0.6 it bottoms out at
				// ~18.75, so the coefficient must satisfy BOTH:
				//   zero scent  (dist.prey=30):  0.72 − c·30  < MoveTo 0.1  → c > 0.021 (no blind Attack)
				//   melee s=0.6 (dist.prey=18.75): 1.26 − c·18.75 > Hunt 0.729 → c < 0.028 (Attack takes over)
				// c=0.025 sits in that band. Stickiness stays scent-scaled (never flat) so a
				// target-less wolf is not locked in Attack at zero scent.
				"Attack": testNumProgram(t, "hunger * (0.8 + scent.prey) - dist.prey * 0.025 - target.threat * 0.7 + is_current * (0.1 + scent.prey * 2)"),
				"MoveTo": testNumProgram(t, "0.1"),
			},
			// Rate must be > 0 for DriveUpdate to treat these as accumulators (F25(c)) — a Rate==0
			// drive falls through to the thermal SET-from-appTemp branch and gets zeroed every tick.
			// Both rates are negligible over the test's tick budget; fatigue's real dynamics come
			// from the world's applyAnimalFatigue (effort:high while Hunting, M2).
			Drives: []fauna.DriveRule{{ID: "hunger", Rate: 0.00001}, {ID: "fatigue", Rate: 0.00001}},
			// M1/M2: the wolf out-sprints a fleeing deer (0.9) only in a fresh burst; fatigue (accrued
			// while Hunt is effort:high) drags it back to parity within ~40 ticks — "predator tires
			// first" (docs/fauna.md 클러스터10), not a raw-speed advantage.
			Speed:       testNumProgram(t, "1.3 - fatigue * 1.0"),
			AttackPower: testNumProgram(t, "0.35"),
			Hit:         testNumProgram(t, "1"),
			Diet:        []core.Tag{"game"},
			IsPredator:  true,
			SteerChannel: map[actions.ActionID]core.Tag{
				"Hunt":   fauna.TagSteerPrey,
				"Attack": fauna.TagAttack,
			},
			// SightRadius here doubles as the combatTarget search range (engine behavior) — kept
			// tight so engagement only locks at genuinely close range, not a long-range "snipe".
			// SmellRadius 30 > the 25-unit start gap: the wolf picks up the deer's plume within a
			// few spread ticks and the approach is genuine scent-gradient homing (FA2's subject),
			// not initial-heading luck.
			SmellRadius: 30, SightRadius: 6, FovArc: 3.14,
		},
	})
}

func fa2Cfg() EnvConfig {
	cfg := testEnvConfig()
	cfg.Min, cfg.Max = core.Vec2{X: -200, Y: -200}, core.Vec2{X: 200, Y: 200}
	cfg.ScentCellSize = 5
	cfg.ScentSpread = 1
	cfg.FaunaDT = 1
	cfg.FaunaCadence = fauna.Cadence{DormantPeriod: 1, WakeCooldown: 1000} // every animal fully re-arbitrates every tick (isolates chase mechanics from F45 dormant cadence)
	cfg.FaunaCombat = fauna.CombatParams{
		ExchangeMinTicks: 5, ExchangeMaxTicks: 10,
		EngageCooldownMinTicks: 1, EngageCooldownMaxTicks: 1,
		DisengageRangeFactor:  20, // huge: a disengage in this test can only be stamina-driven (FC6), never distance
		StaminaDropThreshold:  0.05,
		StaminaDrainPerTick:   0.02,
		StaminaRecoverPerTick: 0.01,
		FatiguePursuitPerTick: 0.01,
		FatigueRecoverPerTick: 0.01,
		VitalRegenPerTick:     0,
	}
	return cfg
}

func fa2ScentEmitters() map[core.Tag][]core.Tag {
	return map[core.Tag][]core.Tag{
		"deer": {"scent:prey"},
		"wolf": {"scent:predator"},
	}
}

// TestFA2PredatorHuntChaseResolves is the FA2 production-path scenario: through real World.Tick(), a
// hungry wolf homes on a deer's prey scent (approach phase — distance trends down), the deer visibly
// flees once the wolf enters its sight FOV (speed jumps well above its graze-wander baseline), and the
// encounter RESOLVES within a bounded tick budget — either a kill or a stamina disengage — instead of
// stalling in the "perpetual parallel walk" the fix addresses.
func TestFA2PredatorHuntChaseResolves(t *testing.T) {
	fx := newFixtureSeeded(t, 2100)
	installFA2ActionRegistry(t, fx)
	rules := fa2Rules(t)

	wolf := testAnimal("an:wolf", "wolf", core.Vec2{X: -25, Y: 0})
	wolf.Drives = map[fauna.DriveID]float64{"hunger": 0.9, "fatigue": 0}
	wolf.Stamina = 1
	wolf.Vital = 1
	wolf.VitalCap = 1
	wolf.CurrentAction = "Hunt"

	deer := testAnimal("an:deer", "deer", core.Vec2{X: 0, Y: 0})
	deer.Drives = map[fauna.DriveID]float64{}
	deer.Vital = 1
	deer.VitalCap = 1
	deer.Heading = 0 // faces +X (away from the wolf's approach from the west)
	deer.CurrentAction = "MoveTo"

	fx.world.InstallFauna(fa2Cfg(), rules, fa2ScentEmitters(), nil, []fauna.Animal{deer, wolf})

	const ticks = 400
	dist := func() (float64, bool) {
		w := fx.world.animals["an:wolf"]
		d := fx.world.animals["an:deer"]
		if w == nil || d == nil {
			return 0, false
		}
		return w.Pos.Distance(d.Pos), true
	}

	// ── Phase A: approach — sample distance every 5 ticks until first engage or sight contact,
	// assert the trend is DOWN overall (allow noise from wander/steering jitter). ─────────────────
	type sample struct {
		tick core.Tick
		d    float64
	}
	var approach []sample
	engagedTick := core.Tick(-1)
	sawFleeSpeedup := false
	var lastDeerPos core.Vec2
	if d := fx.world.animals["an:deer"]; d != nil {
		lastDeerPos = d.Pos
	}
	resolvedTick := core.Tick(-1)
	resolvedByKill := false
	resolvedByStaminaDisengage := false
	wasEngaged := false
	minStaminaWhileEngaged := 1.0

	for i := 1; i <= ticks; i++ {

		fx.world.Tick()
		tick := core.Tick(i)

		w := fx.world.animals["an:wolf"]
		d := fx.world.animals["an:deer"]

		if d == nil && resolvedTick < 0 {
			resolvedTick = tick
			resolvedByKill = true
			break
		}
		if w == nil {
			t.Fatalf("t=%d: wolf unexpectedly removed (setup issue)", tick)
		}

		if engagedTick < 0 && w.EngagedWith != "" {
			engagedTick = tick
		}
		if w.EngagedWith != "" {
			wasEngaged = true
			if w.Stamina < minStaminaWhileEngaged {
				minStaminaWhileEngaged = w.Stamina
			}
		}
		if wasEngaged && w.EngagedWith == "" && resolvedTick < 0 {
			resolvedTick = tick
			resolvedByStaminaDisengage = true
			break
		}

		if engagedTick < 0 && i%5 == 0 {
			dd, ok := dist()
			if ok {
				approach = append(approach, sample{tick, dd})
			}
		}

		// Flee-speedup check: once the deer sees the wolf (sight.predator via its own FOV), its
		// per-tick displacement should clear the graze-wander baseline (0.3) by a wide margin.
		if !sawFleeSpeedup && d != nil {
			disp := lastDeerPos.Distance(d.Pos)
			if disp > 0.6 { // > 2x the 0.3 wander baseline; flee speed is 1.5
				sawFleeSpeedup = true
			}
		}
		if d != nil {
			lastDeerPos = d.Pos
		}
	}

	if len(approach) < 3 {
		t.Fatalf("not enough pre-engage distance samples (%d) — engaged too early or setup issue", len(approach))
	}
	if engagedTick < 0 {
		t.Fatalf("wolf never engaged the deer within %d ticks (chase never closed)", ticks)
	}
	t.Logf("approach samples: %+v (engaged at t=%d)", approach, engagedTick)
	// Monotonic-with-noise: the LAST pre-engage sample must be well below the FIRST (overall downward
	// trend), and no sample may exceed the running minimum-so-far by more than a small slack (rules
	// out "alternates with retreat" — the reported bug had samples oscillating around ~13.7 forever).
	if approach[len(approach)-1].d >= approach[0].d-1.0 {
		t.Fatalf("distance did not trend down during the Hunt approach: first=%.2f last=%.2f",
			approach[0].d, approach[len(approach)-1].d)
	}
	runningMin := approach[0].d
	for _, s := range approach[1:] {
		if s.d > runningMin+2.0 { // small slack for wander jitter; the bug pattern held flat near a fixed distance
			t.Fatalf("distance retreated well past its running minimum at t=%d: d=%.2f min-so-far=%.2f (perpetual parallel walk regression)",
				s.tick, s.d, runningMin)
		}
		if s.d < runningMin {
			runningMin = s.d
		}
	}

	if !sawFleeSpeedup {
		t.Errorf("deer never showed a flee-speed jump above its graze-wander baseline once the wolf closed in")
	}

	if resolvedTick < 0 {
		t.Fatalf("chase never resolved within %d ticks after engaging at t=%d (neither a kill nor a stamina disengage)", ticks, engagedTick)
	}
	span := resolvedTick - engagedTick
	const boundedTicks = 150
	if span > boundedTicks {
		t.Fatalf("chase took %d ticks to resolve after engaging (want <= %d): kill=%v staminaDisengage=%v",
			span, boundedTicks, resolvedByKill, resolvedByStaminaDisengage)
	}
	if !resolvedByKill && !resolvedByStaminaDisengage {
		t.Fatalf("chase resolved by neither a kill nor a stamina disengage (unexpected exit path)")
	}
	if resolvedByStaminaDisengage && minStaminaWhileEngaged > fa2Cfg().FaunaCombat.StaminaDropThreshold+1e-9 {
		t.Fatalf("disengage fired without stamina actually dropping to the threshold (min stamina while engaged=%.3f) — would indicate a distance-driven disengage, not M2 stamina",
			minStaminaWhileEngaged)
	}
	t.Logf("engaged at t=%d, resolved at t=%d (span=%d): kill=%v staminaDisengage=%v minStaminaWhileEngaged=%.3f",
		engagedTick, resolvedTick, span, resolvedByKill, resolvedByStaminaDisengage, minStaminaWhileEngaged)
}
