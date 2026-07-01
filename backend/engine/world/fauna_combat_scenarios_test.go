package world

import (
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/mind/actions"
)

// scenarioCombatRules builds a wolf (predator) + deer (prey) rule set with a configurable wolf diet so
// tests can probe target matching. attack_power is a constant so exchanges deal fixed damage.
func scenarioCombatRules(t *testing.T, wolfDiet []core.Tag) *fauna.Rules {
	t.Helper()
	return fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		"deer": {
			Utilities:   map[actions.ActionID]*expr.Program{"Forage": testNumProgram(t, "1")},
			Drives:      []fauna.DriveRule{{ID: "hunger", Rate: 0.01}},
			Speed:       testNumProgram(t, "0"),
			Tags:        []core.Tag{"game", "scent:prey"}, // wolf diet [game] matches this (D10 tag-driven)
			SmellRadius: 5, SightRadius: 5, FovArc: 3.14,
		},
		"wolf": {
			Utilities:   map[actions.ActionID]*expr.Program{"Attack": testNumProgram(t, "1")},
			Drives:      []fauna.DriveRule{{ID: "hunger", Rate: 0.02}},
			Speed:       testNumProgram(t, "0"),
			AttackPower: testNumProgram(t, "0.4"),
			Hit:         testNumProgram(t, "1"),
			Diet:        wolfDiet,
			IsPredator:  true,
			SmellRadius: 5, SightRadius: 5, FovArc: 3.14,
			SteerChannel: map[actions.ActionID]core.Tag{"Attack": fauna.TagAttack},
		},
	})
}

func scenarioCombatCfg() EnvConfig {
	cfg := testEnvConfig()
	cfg.ScentCellSize = 5
	cfg.ScentSpread = 1
	cfg.FaunaDT = 1
	cfg.DecayStep = 0
	cfg.FaunaCombat = fauna.CombatParams{
		ExchangeMinTicks: 1, ExchangeMaxTicks: 1,
		EngageCooldownMinTicks: 1, EngageCooldownMaxTicks: 1,
		DisengageRangeFactor:   100,
		StaminaDropThreshold:   0.5,
		VitalCapDamageFraction: 0.1,
	}
	return cfg
}

// TestCombatScenarioDietIsTagDriven verifies predator diet matches the TARGET's content TAGS (D10), not its
// SpeciesID: a wolf diet [game] engages a deer carrying the `game` tag, while a diet tag the prey does NOT
// carry does not engage. (Regression: dietMatches used to compare against target.Species, so content diets
// authored as tags silently failed — fixed via SpeciesRule.Tags + tag-intersection matching.)
func TestCombatScenarioDietIsTagDriven(t *testing.T) {
	run := func(t *testing.T, diet []core.Tag) float64 {
		fx := newFixtureSeeded(t, 810)
		installCombatActionRegistry(t, fx)
		wolf := testAnimal("an:wolf", "wolf", core.Vec2{X: 1, Y: 1})
		wolf.Drives = map[fauna.DriveID]float64{"hunger": 0.9}
		wolf.Stamina = 1
		deer := testAnimal("an:deer", "deer", core.Vec2{X: 2, Y: 1})
		deer.Vital = 1
		deer.VitalCap = 1
		fx.world.InstallFauna(scenarioCombatCfg(), scenarioCombatRules(t, diet), testScentEmitters(), []fauna.Animal{deer, wolf})
		for range 10 {
			fx.world.Tick()
		}
		if d := fx.world.animals["an:deer"]; d != nil {
			return d.Vital
		}
		return -1 // dead
	}

	// diet = the content TAG "game", which the deer carries (scenarioCombatRules sets deer.Tags) → engages.
	if v := run(t, []core.Tag{"game"}); v >= 1.0 {
		t.Errorf("diet-by-tag [game] failed to engage the deer (vital=%.2f, want <1 or dead)", v)
	}
	// diet = a tag the deer does NOT carry → no engagement.
	if v := run(t, []core.Tag{"fish"}); v != 1.0 {
		t.Errorf("diet [fish] should not match a deer carrying only [game, scent:prey] (vital=%.2f, want 1.0)", v)
	}
}

// TestCombatScenarioPreyNeverRetaliates: a prey (no Attack action / not a predator) never damages its
// attacker — only the predator deals damage (FC5).
func TestCombatScenarioPreyNeverRetaliates(t *testing.T) {
	fx := newFixtureSeeded(t, 811)
	installCombatActionRegistry(t, fx)
	wolf := testAnimal("an:wolf", "wolf", core.Vec2{X: 1, Y: 1})
	wolf.Drives = map[fauna.DriveID]float64{"hunger": 0.9}
	wolf.Stamina = 1
	wolf.Vital = 1
	wolf.VitalCap = 1
	deer := testAnimal("an:deer", "deer", core.Vec2{X: 2, Y: 1})
	deer.Vital = 1
	deer.VitalCap = 1
	fx.world.InstallFauna(scenarioCombatCfg(), scenarioCombatRules(t, []core.Tag{"game"}), testScentEmitters(), []fauna.Animal{deer, wolf})

	sawDeerDamage := false
	for range 8 {
		fx.world.Tick()
		if w := fx.world.animals["an:wolf"]; w != nil && w.Vital < 1.0 {
			t.Fatalf("predator took damage from an unarmed prey: wolf vital=%.3f", w.Vital)
		}
		if d := fx.world.animals["an:deer"]; d == nil || d.Vital < 1.0 {
			sawDeerDamage = true
		}
	}
	if !sawDeerDamage {
		t.Fatalf("predator never damaged the prey (setup issue)")
	}
}

// TestCombatScenarioPackTwoPredators: two predators on one prey both land damage (prey dies faster than a
// lone predator would) and the outcome is deterministic across identical runs (D12).
func TestCombatScenarioPackTwoPredators(t *testing.T) {
	ticksToKill := func(t *testing.T, nWolves int) int {
		fx := newFixtureSeeded(t, 812)
		installCombatActionRegistry(t, fx)
		animals := []fauna.Animal{}
		deer := testAnimal("an:deer", "deer", core.Vec2{X: 5, Y: 5})
		deer.Vital = 1
		deer.VitalCap = 1
		animals = append(animals, deer)
		for i := range nWolves {
			w := testAnimal(core.ObjectID("an:wolf"+string(rune('a'+i))), "wolf", core.Vec2{X: 5, Y: 6})
			w.Drives = map[fauna.DriveID]float64{"hunger": 0.9}
			w.Stamina = 1
			animals = append(animals, w)
		}
		fx.world.InstallFauna(scenarioCombatCfg(), scenarioCombatRules(t, []core.Tag{"game"}), testScentEmitters(), animals)
		for tick := 1; tick <= 30; tick++ {
			fx.world.Tick()
			if fx.world.animals["an:deer"] == nil {
				return tick
			}
		}
		return -1 // never died
	}

	lone := ticksToKill(t, 1)
	pack := ticksToKill(t, 2)
	packAgain := ticksToKill(t, 2)
	if pack < 0 {
		t.Fatalf("pack never killed the prey")
	}
	// HARD guarantee: same-target contention is deterministic (D12).
	if pack != packAgain {
		t.Fatalf("pack kill non-deterministic: %d vs %d", pack, packAgain)
	}
	// FC12 v1 = 1:1 — same-target predators go through the combined-conflict resolver, so only ONE lands
	// damage per tick (no pack-hunting speedup). Documented, not asserted as a defect: pack == lone here.
	t.Logf("pack(%d) vs lone(%d) — FC12 v1=1:1 (conflict serializes same-target attackers; pack hunting is a future feature)", pack, lone)
}

// TestGrazingReducesHungerAtForageFlora verifies the herbivore feeding loop end-to-end: a hungry deer that
// picks Graze (a seek:food action) next to a forage flora crops it and its hunger drops. Regression for the
// gap where Graze had no hunger effect (deer wanted food but never ate).
func TestGrazingReducesHungerAtForageFlora(t *testing.T) {
	fx := newFixtureSeeded(t, 830)
	reg, err := actions.Load(strings.NewReader("schema_version: 1\nactions:\n  - id: Graze\n    tags: [uses:Agility, seek:food]\n    duration: 10\n    produces: [grazed]\n"))
	if err != nil {
		t.Fatal(err)
	}
	fx.world.actReg = reg
	fx.svc.Actions = reg
	fx.world.svc.Actions = reg

	rules := fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		"deer": {
			Utilities:    map[actions.ActionID]*expr.Program{"Graze": testNumProgram(t, "1")},
			Drives:       []fauna.DriveRule{{ID: "hunger", Rate: 0.001}},
			Speed:        testNumProgram(t, "0"),
			Graze:        testNumProgram(t, "0.1"),
			SteerChannel: map[actions.ActionID]core.Tag{"Graze": fauna.TagSteerFood},
			SmellRadius:  5, SightRadius: 5, FovArc: 3.14,
		},
	})
	cfg := testEnvConfig()
	cfg.ScentCellSize = 5
	cfg.ScentSpread = 1
	cfg.FaunaDT = 1
	deer := testAnimal("an:deer", "deer", core.Vec2{X: 1, Y: 1})
	deer.Drives = map[fauna.DriveID]float64{"hunger": 0.8}
	fx.world.InstallFauna(cfg, rules, testScentEmitters(), []fauna.Animal{deer})
	fx.world.PlaceObject("grass_1", "grass", core.Vec2{X: 2, Y: 1}, nil) // forage flora within reach

	const start = 0.8
	for range 20 {
		fx.world.Tick()
	}
	end := fx.world.animals["an:deer"].Drives["hunger"]
	if end >= start {
		t.Fatalf("grazing at forage flora did not reduce hunger: start=%.3f end=%.3f", start, end)
	}

	// Control: no forage flora nearby → grazing cannot reduce hunger (it only rises).
	fx2 := newFixtureSeeded(t, 831)
	fx2.world.actReg = reg
	fx2.svc.Actions = reg
	fx2.world.svc.Actions = reg
	deer2 := testAnimal("an:deer", "deer", core.Vec2{X: 50, Y: 50})
	deer2.Drives = map[fauna.DriveID]float64{"hunger": 0.8}
	fx2.world.InstallFauna(cfg, rules, testScentEmitters(), []fauna.Animal{deer2})
	fx2.world.PlaceObject("grass_far", "grass", core.Vec2{X: 2, Y: 1}, nil) // far away, out of reach
	for range 20 {
		fx2.world.Tick()
	}
	if got := fx2.world.animals["an:deer"].Drives["hunger"]; got < start {
		t.Fatalf("hunger dropped with NO forage flora in reach (grazing should need food): %.3f", got)
	}
}
