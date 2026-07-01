package world

import (
	"testing"

	"github.com/dogring/bdg/engine/env/decay"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/space/scent"
)

// TestCombatPredationEndToEndThroughTick drives the REAL world tick loop with a hungry predator next to a
// prey and asserts the whole predation scenario fires through fauna.Step -> combined apply -> scent/decay:
// engage -> exchange damage -> death -> carcass (decay lot) -> carrion scent -> predator feeds (carcass
// consumed + hunger drops). This is the composition Phase 6 shipped OFF-neutral; it proves the modules line
// up end-to-end before Phase 7 activation.
func TestCombatPredationEndToEndThroughTick(t *testing.T) {
	fx := newFixtureSeeded(t, 700)
	installCombatActionRegistry(t, fx)

	rules := fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		"deer": {
			Utilities:   map[actions.ActionID]*expr.Program{"Forage": testNumProgram(t, "1")},
			Drives:      []fauna.DriveRule{{ID: "hunger", Rate: 0.01}},
			Speed:       testNumProgram(t, "0"),
			Tags:        []core.Tag{"game", "scent:prey"}, // wolf diet [game] matches (D10 tag-driven)
			SmellRadius: 5, SightRadius: 5, FovArc: 3.14,
		},
		"wolf": {
			Utilities: map[actions.ActionID]*expr.Program{
				"Attack": testNumProgram(t, "1"),
				"Feed":   testNumProgram(t, "scent.carrion * 100"), // dominates once carrion is smelled
			},
			// hunger is an ACCUMULATOR (Rate>0) so it persists — a Rate-0 drive is treated as thermal.
			Drives:      []fauna.DriveRule{{ID: "hunger", Rate: 0.02}},
			Speed:       testNumProgram(t, "0"),
			AttackPower: testNumProgram(t, "0.4"), // ~3 exchanges kill a full-vital prey
			Hit:         testNumProgram(t, "1"),
			Feed:        testNumProgram(t, "0.6"),
			Diet:        []core.Tag{"game"},
			IsPredator:  true,
			SmellRadius: 5, SightRadius: 5, FovArc: 3.14,
			// config maps these from the Attack/Feed action combat:attack/feed:carrion tags (D4/D10);
			// replicated here since this test builds fauna.Rules directly (no config.Load).
			SteerChannel: map[actions.ActionID]core.Tag{"Attack": fauna.TagAttack, "Feed": fauna.TagFeed},
		},
	})

	// Carcass decay rules give the carcass a Satiety supply so Feed actually consumes it.
	decayRules := decay.NewRules(map[decay.KindID]decay.KindRule{
		"carcass": {
			BaseRate: 0.01,
			Accel:    testNumProgram(t, "1"),
			States: []decay.StateRule{
				{Threshold: 0, Supply: map[core.Dimension]float64{"Satiety": 0.5}},
			},
		},
	})

	cfg := testEnvConfig()
	cfg.ScentCellSize = 5
	cfg.ScentSpread = 1
	cfg.FaunaDT = 1
	cfg.DecayStep = 0 // do NOT rot the carcass — the ONLY way it can vanish is a predator Feed
	cfg.FaunaCombat = fauna.CombatParams{
		ExchangeMinTicks: 1, ExchangeMaxTicks: 1,
		EngageCooldownMinTicks: 1, EngageCooldownMaxTicks: 1,
		DisengageRangeFactor:   100, // never disengage on range in this fixture
		StaminaDropThreshold:   0,
		VitalRegenPerTick:      0,
		VitalCapDamageFraction: 0.05,
	}

	// env (decay only) first, then fauna — the accepted install order.
	fx.world.InstallEnv(cfg, nil, nil, nil, nil, nil, decay.New(nil), decayRules)

	wolf := testAnimal("an:wolf", "wolf", core.Vec2{X: 1, Y: 1})
	wolf.Drives = map[fauna.DriveID]float64{"hunger": 0.9}
	wolf.Stamina = 1
	deer := testAnimal("an:deer", "deer", core.Vec2{X: 2, Y: 1})
	deer.Vital = 1
	deer.VitalCap = 1

	fx.world.InstallFauna(cfg, rules, testScentEmitters(), []fauna.Animal{deer, wolf})

	var sawDamage, sawDeath, sawCarcass, sawCarrion, sawCarcassConsumed bool
	for i := range 40 {
		fx.world.Tick()
		d := fx.world.animals["an:deer"]
		w := fx.world.animals["an:wolf"]

		carcass := false
		var carrion float64
		for _, id := range fx.world.objectIDs {
			if obj, ok := fx.world.objects[id]; ok && obj.Kind == "carcass" {
				carcass = true
				if fx.world.scent != nil {
					if v := fx.world.scent.IntensityAt(scent.ChanCarrion, obj.Pos); v > 0 {
						carrion, sawCarrion = v, true
					}
				}
			}
		}
		if carcass {
			sawCarcass = true
		} else if sawCarcass {
			sawCarcassConsumed = true // a carcass existed, then vanished — only Feed can do that (DecayStep=0)
		}

		dv := -1.0
		if d != nil {
			dv = d.Vital
			if d.Vital < 1.0 {
				sawDamage = true
			}
		} else {
			sawDeath = true
		}
		wa, wh := actions.ActionID(""), -1.0
		if w != nil {
			wa, wh = w.CurrentAction, w.Drives["hunger"]
		}
		t.Logf("tick %2d: deerVital=%.3f wolf[act=%s hunger=%.3f] carcass=%v carrion=%.3f", i+1, dv, wa, wh, carcass, carrion)
	}
	t.Logf("ARC: damage=%v death=%v carcass=%v carrion=%v carcassConsumed=%v",
		sawDamage, sawDeath, sawCarcass, sawCarrion, sawCarcassConsumed)

	if !sawDamage {
		t.Errorf("predator never damaged the prey")
	}
	if !sawDeath {
		t.Errorf("prey never died")
	}
	if !sawCarcass {
		t.Errorf("no carcass spawned on death")
	}
	if !sawCarrion {
		t.Errorf("carcass emitted no ChanCarrion scent")
	}
	if !sawCarcassConsumed {
		t.Errorf("predator never fed (carcass never consumed)")
	}
}
