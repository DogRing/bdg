package world

import (
	"testing"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/mind/actions"
)

// Emergent 2-parent reproduction (PD4 / P_fa4c-2, docs/plans/fauna.md §5).

// matingRules builds a species whose only action is Mate (steer channel seek:mate), so scoring is
// unambiguous and every test below isolates the conception gates rather than action arbitration.
func matingRules(t *testing.T, maturityAge float64, cooldown core.Tick) *fauna.Rules {
	t.Helper()
	return fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		"rabbit": {
			Utilities:    map[actions.ActionID]*expr.Program{"Mate": testNumProgram(t, "1")},
			SteerChannel: map[actions.ActionID]core.Tag{"Mate": fauna.TagSteerMate},
			Drives:       []fauna.DriveRule{{ID: "hunger"}},
			Speed:        testNumProgram(t, "0"), // hold position: distance is set by the test, not by steering
			MaturityAge:  maturityAge,
			MateCooldown: cooldown,
			SmellRadius:  5,
			SightRadius:  40,
			FovArc:       3.14,
		},
	})
}

func matingAnimal(id core.ObjectID, pos core.Vec2, age float64, stats map[core.StatID]float64) fauna.Animal {
	return fauna.Animal{
		ID: id, Species: "rabbit", Pos: pos, Age: age,
		Stats: stats, Drives: map[fauna.DriveID]float64{"hunger": 0},
		Stamina: 1, Vital: 1, CurrentAction: "Mate",
	}
}

// countSpecies returns how many live animals of a species the world holds.
func countSpecies(w *World, sp fauna.SpeciesID) int {
	n := 0
	for _, id := range w.animalIDs {
		if a := w.animals[id]; a != nil && a.Species == sp {
			n++
		}
	}
	return n
}

// The core case: two mature adults, each other's nearest eligible partner, within conception reach
// ⇒ exactly ONE offspring, and both parents go on cooldown.
func TestMutualCourtshipProducesOneOffspring(t *testing.T) {
	fx := newFixtureSeeded(t, 4242)
	cfg := testEnvConfig()
	// Re-arbitrate every tick: isolates the conception gates from the F45 dormant cadence, whose
	// ID-staggered phases are a separate concern (and the reason consent is read off CurrentAction).
	cfg.FaunaCadence = fauna.Cadence{DormantPeriod: 1, WakeCooldown: 1000}
	// Conception reach reuses the engagement proximity (PD4-vi ⓐ); the shared test config leaves
	// FaunaCombat zeroed, which would make the reach 0 and no pairing could ever be close enough.
	cfg.FaunaCombat.DisengageRangeFactor = 2 // reach = 2 × ScentCellSize(5) = 10 world units
	const cooldown = core.Tick(50)
	a := matingAnimal("an:r1", core.Vec2{X: 10, Y: 10}, 500, map[core.StatID]float64{"Agility": 40})
	b := matingAnimal("an:r2", core.Vec2{X: 12, Y: 10}, 500, map[core.StatID]float64{"Agility": 80})
	fx.world.InstallFauna(cfg, matingRules(t, 100, cooldown), testScentEmitters(), nil, []fauna.Animal{a, b})

	conceivedAt := fx.world.tick
	fx.world.Tick()

	if got := countSpecies(fx.world, "rabbit"); got != 3 {
		t.Fatalf("live rabbits after one mating tick = %d, want 3 (two parents + one newborn)", got)
	}
	for _, id := range []core.ObjectID{"an:r1", "an:r2"} {
		if got := fx.world.animals[id].MateCooldownUntil; got != conceivedAt+cooldown {
			t.Errorf("%s cooldown = %d, want %d (both parents go refractory)", id, got, conceivedAt+cooldown)
		}
	}

	// Exactly one pairing ⇒ exactly one birth, even though BOTH animals courted: only the
	// lower-ID parent conceives, which is what keeps the result independent of apply order.
	var child *fauna.Animal
	for _, id := range fx.world.animalIDs {
		if id != "an:r1" && id != "an:r2" {
			child = fx.world.animals[id]
		}
	}
	if child == nil {
		t.Fatal("no newborn found")
	}
	if child.Age != 0 {
		t.Errorf("newborn age = %v, want 0", child.Age)
	}
	if child.Species != "rabbit" {
		t.Errorf("newborn species = %q, want rabbit", child.Species)
	}
	if child.MateCooldownUntil != 0 {
		t.Errorf("newborn should not be born refractory, got cooldown %d", child.MateCooldownUntil)
	}
	// The stat blend is bracketed by the parents (40 and 80) — it can never leave that range.
	if got := child.Stats["Agility"]; got < 40 || got > 80 {
		t.Errorf("newborn Agility = %v, want within the parental bracket [40, 80]", got)
	}
}

// Mutual consent (PD4-vi ⓒ): one-sided interest must NOT conceive. Without this, a §6 utility that
// says "too hungry / too frightened to breed" could be bypassed by an eager partner.
func TestOneSidedCourtshipDoesNotConceive(t *testing.T) {
	fx := newFixtureSeeded(t, 77)
	cfg := testEnvConfig()
	// Re-arbitrate every tick: isolates the conception gates from the F45 dormant cadence, whose
	// ID-staggered phases are a separate concern (and the reason consent is read off CurrentAction).
	cfg.FaunaCadence = fauna.Cadence{DormantPeriod: 1, WakeCooldown: 1000}
	// Conception reach reuses the engagement proximity (PD4-vi ⓐ); the shared test config leaves
	// FaunaCombat zeroed, which would make the reach 0 and no pairing could ever be close enough.
	cfg.FaunaCombat.DisengageRangeFactor = 2 // reach = 2 × ScentCellSize(5) = 10 world units
	// Two candidate actions, arbitrated by hunger: a fed rabbit courts, a starving one forages.
	// This is the property mutual consent exists to protect — a species' §6 utility, not the mating
	// code, decides whether that species breeds under famine.
	rules := fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		"rabbit": {
			Utilities: map[actions.ActionID]*expr.Program{
				"Mate":  testNumProgram(t, "1 - hunger * 2"),
				"Graze": testNumProgram(t, "hunger"),
			},
			SteerChannel: map[actions.ActionID]core.Tag{
				"Mate":  fauna.TagSteerMate,
				"Graze": fauna.TagSteerFood,
			},
			// An ACCUMULATOR hunger (Rate > 0) so the drive persists tick to tick; a rate-less drive
			// is the thermal-derived kind and would be recomputed to 0, erasing the famine premise.
			Drives:      []fauna.DriveRule{{ID: "hunger", Rate: 0.001}},
			Speed:       testNumProgram(t, "0"),
			MaturityAge: 100,
			SmellRadius: 5, SightRadius: 40, FovArc: 3.14,
		},
	})
	a := matingAnimal("an:r1", core.Vec2{X: 10, Y: 10}, 500, map[core.StatID]float64{"Agility": 40})
	b := matingAnimal("an:r2", core.Vec2{X: 12, Y: 10}, 500, map[core.StatID]float64{"Agility": 80})
	b.Drives = map[fauna.DriveID]float64{"hunger": 1} // starving: Graze out-bids Mate
	b.CurrentAction = "Graze"
	fx.world.InstallFauna(cfg, rules, testScentEmitters(), nil, []fauna.Animal{a, b})

	fx.world.Tick()

	if got := fx.world.animals["an:r2"].CurrentAction; got != "Graze" {
		t.Fatalf("the starving rabbit chose %q, want Graze — the test premise no longer holds", got)
	}
	if got := countSpecies(fx.world, "rabbit"); got != 2 {
		t.Errorf("live rabbits = %d, want 2 — a willing rabbit must not conceive with a starving one", got)
	}
}

// Maturity gate (PD4-ii): a juvenile is not an eligible partner, so no conception happens even
// when the adult is willing and adjacent.
func TestJuvenileDoesNotConceive(t *testing.T) {
	fx := newFixtureSeeded(t, 31)
	cfg := testEnvConfig()
	// Re-arbitrate every tick: isolates the conception gates from the F45 dormant cadence, whose
	// ID-staggered phases are a separate concern (and the reason consent is read off CurrentAction).
	cfg.FaunaCadence = fauna.Cadence{DormantPeriod: 1, WakeCooldown: 1000}
	// Conception reach reuses the engagement proximity (PD4-vi ⓐ); the shared test config leaves
	// FaunaCombat zeroed, which would make the reach 0 and no pairing could ever be close enough.
	cfg.FaunaCombat.DisengageRangeFactor = 2 // reach = 2 × ScentCellSize(5) = 10 world units
	adult := matingAnimal("an:r1", core.Vec2{X: 10, Y: 10}, 500, map[core.StatID]float64{"Agility": 40})
	juvenile := matingAnimal("an:r2", core.Vec2{X: 11, Y: 10}, 5, map[core.StatID]float64{"Agility": 80})
	fx.world.InstallFauna(cfg, matingRules(t, 100, 50), testScentEmitters(), nil, []fauna.Animal{adult, juvenile})

	fx.world.Tick()

	if got := countSpecies(fx.world, "rabbit"); got != 2 {
		t.Errorf("live rabbits = %d, want 2 — a juvenile (age 5 < maturity 100) must not breed", got)
	}
}

// Refractory window (PD4-vi ⓑ): having just conceived, a pair cannot immediately conceive again —
// this, not a birth-rate constant, is what paces breeding.
func TestCooldownBlocksImmediateSecondConception(t *testing.T) {
	fx := newFixtureSeeded(t, 5150)
	cfg := testEnvConfig()
	// Re-arbitrate every tick: isolates the conception gates from the F45 dormant cadence, whose
	// ID-staggered phases are a separate concern (and the reason consent is read off CurrentAction).
	cfg.FaunaCadence = fauna.Cadence{DormantPeriod: 1, WakeCooldown: 1000}
	// Conception reach reuses the engagement proximity (PD4-vi ⓐ); the shared test config leaves
	// FaunaCombat zeroed, which would make the reach 0 and no pairing could ever be close enough.
	cfg.FaunaCombat.DisengageRangeFactor = 2 // reach = 2 × ScentCellSize(5) = 10 world units
	a := matingAnimal("an:r1", core.Vec2{X: 10, Y: 10}, 500, map[core.StatID]float64{"Agility": 40})
	b := matingAnimal("an:r2", core.Vec2{X: 12, Y: 10}, 500, map[core.StatID]float64{"Agility": 80})
	fx.world.InstallFauna(cfg, matingRules(t, 100, 1000), testScentEmitters(), nil, []fauna.Animal{a, b})

	fx.world.Tick()
	afterFirst := countSpecies(fx.world, "rabbit")
	if afterFirst != 3 {
		t.Fatalf("first tick produced %d rabbits, want 3", afterFirst)
	}
	for range 20 {
		fx.world.Tick()
	}
	// The newborn is far below maturity and both parents are refractory, so the population must
	// hold — a pair that could re-conceive every tick would explode.
	if got := countSpecies(fx.world, "rabbit"); got != afterFirst {
		t.Errorf("rabbits after the refractory window = %d, want %d (no second conception)", got, afterFirst)
	}
}

// D12: identical seed + identical parents ⇒ byte-identical newborn stats.
func TestOffspringStatsAreDeterministic(t *testing.T) {
	run := func() map[core.StatID]float64 {
		fx := newFixtureSeeded(t, 8080)
		cfg := testEnvConfig()
		// Re-arbitrate every tick: isolates the conception gates from the F45 dormant cadence, whose
		// ID-staggered phases are a separate concern (and the reason consent is read off CurrentAction).
		cfg.FaunaCadence = fauna.Cadence{DormantPeriod: 1, WakeCooldown: 1000}
		// Conception reach reuses the engagement proximity (PD4-vi ⓐ); the shared test config leaves
		// FaunaCombat zeroed, which would make the reach 0 and no pairing could ever be close enough.
		cfg.FaunaCombat.DisengageRangeFactor = 2 // reach = 2 × ScentCellSize(5) = 10 world units
		a := matingAnimal("an:r1", core.Vec2{X: 10, Y: 10}, 500, map[core.StatID]float64{"Agility": 40, "Strength": 10})
		b := matingAnimal("an:r2", core.Vec2{X: 12, Y: 10}, 500, map[core.StatID]float64{"Agility": 80, "Strength": 90})
		fx.world.InstallFauna(cfg, matingRules(t, 100, 50), testScentEmitters(), nil, []fauna.Animal{a, b})
		fx.world.Tick()
		for _, id := range fx.world.animalIDs {
			if id != "an:r1" && id != "an:r2" {
				return fx.world.animals[id].Stats
			}
		}
		return nil
	}
	first, second := run(), run()
	if first == nil || second == nil {
		t.Fatal("no newborn produced")
	}
	for id, v := range first {
		if second[id] != v {
			t.Errorf("stat %q differs between identical runs: %v vs %v", id, v, second[id])
		}
	}
	// Both stats must sit inside their own parental bracket — the blend never extrapolates.
	if got := first["Strength"]; got < 10 || got > 90 {
		t.Errorf("newborn Strength = %v, outside the parental bracket [10, 90]", got)
	}
}

// Off-lever: a species that authors no Mate action never courts, so worlds predating P_fa4c-2
// behave exactly as before (no births, no extra RNG draws taken on their behalf).
func TestSpeciesWithoutMateActionNeverBreeds(t *testing.T) {
	fx := newFixtureSeeded(t, 606)
	cfg := testEnvConfig()
	// Re-arbitrate every tick: isolates the conception gates from the F45 dormant cadence, whose
	// ID-staggered phases are a separate concern (and the reason consent is read off CurrentAction).
	cfg.FaunaCadence = fauna.Cadence{DormantPeriod: 1, WakeCooldown: 1000}
	// Conception reach reuses the engagement proximity (PD4-vi ⓐ); the shared test config leaves
	// FaunaCombat zeroed, which would make the reach 0 and no pairing could ever be close enough.
	cfg.FaunaCombat.DisengageRangeFactor = 2 // reach = 2 × ScentCellSize(5) = 10 world units
	a := testAnimal("an:deer1", "deer", core.Vec2{X: 10, Y: 10})
	b := testAnimal("an:deer2", "deer", core.Vec2{X: 11, Y: 10})
	a.Age, b.Age = 9999, 9999
	fx.world.InstallFauna(cfg, testFaunaRules(t), testScentEmitters(), nil, []fauna.Animal{a, b})

	for range 10 {
		fx.world.Tick()
	}
	if got := countSpecies(fx.world, "deer"); got != 2 {
		t.Errorf("live deer = %d, want 2 — a species with no Mate action must not reproduce", got)
	}
}
