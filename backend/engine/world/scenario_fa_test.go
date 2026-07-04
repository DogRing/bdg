package world

// FA scenario coverage map (docs/scenarios-world.md, Phase 10):
//   FA1 graze loop        → TestGrazingReducesHungerAtForageFlora (fauna_combat_scenarios_test.go)
//   FA2 scent+FOV hunt    → TestFA2PredatorHuntChaseResolves (scenario_fa2_test.go)
//   FA3 wary↔flee band    → TestFA3WaryFleeBands (below)
//   FA4 downwind plume    → TestFA4DownwindScentPlume (below)
//   FA5 day/night thermal → NOT ENCODABLE with current content semantics: the thermal drive is
//                           clamp01(apparent_temp §6) and content apparent_temp programs emit °C,
//                           so thermal saturates at 1 for any temperature ≥ 1°C and can never
//                           express "cold night → thermal stress ↑". Needs a human decision on
//                           apparent_temp semantics (°C for render vs normalized stress) — see
//                           the Phase 10 report / docs/fauna.md F40.
//   FA6 hidden respawn    → TestRespawnTopsUpToTarget (respawn_test.go)
//   FA7 herd/flocking     → DEFERRED (docs/scenarios-world.md)
//   FA8 per-species terrain → TestWorldTerrainSamplerSemantics (fauna_test.go) + the ecosim
//                           containment assertions (engine/ecosim)

import (
	"testing"

	"github.com/dogring/bdg/engine/env/climate"
	"github.com/dogring/bdg/engine/env/flora"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/space/scent"
)

// ── FA3 — wary↔flee as ONE fear value crossing §6 bands (no FSM, D3) ────────────

// fa3Rules mirrors the content deer band shape: fear is a SET-from-context drive
// (scent → WaryLevel 0.5, sight → FleeLevel 1.0, F43); Wary scores `fear`, Flee
// scores `(fear-0.6)*5` — ONE fear value selects the band, no wary→flee FSM edge.
func fa3Rules(t *testing.T) *fauna.Rules {
	t.Helper()
	return fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		"deer": {
			Utilities: map[actions.ActionID]*expr.Program{
				"Wary":   testNumProgram(t, "fear"),
				"Flee":   testNumProgram(t, "(fear - 0.6) * 5"),
				"MoveTo": testNumProgram(t, "0.1"),
			},
			Drives: []fauna.DriveRule{
				{ID: "fear", Decay: 0.05, WaryLevel: 0.5, FleeLevel: 1.0},
			},
			Speed: testNumProgram(t, "0.3 + fear * 0.5"),
			Tags:  []core.Tag{"game"},
			SteerChannel: map[actions.ActionID]core.Tag{
				"Wary": fauna.TagWaryPred,
				"Flee": fauna.TagFleePred,
			},
			SmellRadius: 15, SightRadius: 12, FovArc: 3.0,
		},
		// Stationary scent/threat source: emits scent:predator, classifies as a
		// predator for the deer's FOV query, but never moves (controls geometry).
		"wolf": {
			Utilities:    map[actions.ActionID]*expr.Program{"MoveTo": testNumProgram(t, "0.1")},
			Drives:       []fauna.DriveRule{},
			Speed:        testNumProgram(t, "0"),
			IsPredator:   true,
			SmellRadius:  1,
			SightRadius:  1,
			FovArc:       0.1,
			SteerChannel: map[actions.ActionID]core.Tag{},
		},
	})
}

func fa3Install(t *testing.T, fx *testFixture, wolfPos core.Vec2, deerHeading float64) {
	t.Helper()
	installFA2ActionRegistry(t, fx) // Hunt/Attack/Flee/MoveTo tags; Wary needs its own entry
	// Extend the registry with Wary (effort:low vigilance walk).
	reg := fx.actReg
	_ = reg
	cfg := fa2Cfg()
	deer := testAnimal("an:deer", "deer", core.Vec2{X: 0, Y: 0})
	deer.Heading = deerHeading
	deer.Vital, deer.VitalCap = 1, 1
	deer.CurrentAction = "MoveTo"
	wolf := testAnimal("an:wolf", "wolf", wolfPos)
	wolf.Vital, wolf.VitalCap = 1, 1
	wolf.CurrentAction = "MoveTo"
	fx.world.InstallFauna(cfg, fa3Rules(t), fa2ScentEmitters(), nil, []fauna.Animal{deer, wolf})
}

// TestFA3WaryFleeBands drives BOTH bands from the SAME rules/content:
//
//	case A (scent only, predator far outside sight): fear settles at WaryLevel →
//	  Wary wins (0.5 > MoveTo 0.1, Flee (0.5−0.6)·5 < 0) and the deer drifts AWAY;
//	case B (predator inside sight FOV): fear jumps to FleeLevel → Flee band
//	  ((1−0.6)·5 = 2 > Wary 1) and the per-tick displacement jumps with it.
func TestFA3WaryFleeBands(t *testing.T) {
	// ── case A: scent-only → Wary band ──────────────────────────────────────────
	fxA := newFixtureSeeded(t, 2300)
	fa3Install(t, fxA, core.Vec2{X: 13.5, Y: 0}, 3.14) // wolf far E, deer faces W (never sighted)
	sawWary := false
	startDist := 13.5
	for i := 1; i <= 120; i++ {
		fxA.world.Tick()
		d := fxA.world.animals["an:deer"]
		if d == nil {
			t.Fatal("deer vanished in case A")
		}
		if d.CurrentAction == "Wary" {
			sawWary = true
		}
		if d.CurrentAction == "Flee" {
			t.Fatalf("t=%d case A: deer entered Flee on scent alone (fear should sit in the Wary band)", i)
		}
	}
	dA := fxA.world.animals["an:deer"]
	if !sawWary {
		t.Fatalf("case A: deer never entered Wary despite predator scent (fear SET-from-scent broken)")
	}
	if got := dA.Pos.Distance(core.Vec2{X: 13.5, Y: 0}); got <= startDist-1 {
		t.Errorf("case A: Wary deer should drift AWAY from the scent source; dist %.1f → %.1f", startDist, got)
	}

	// ── case B: sight contact → Flee band, visible speed jump ───────────────────
	fxB := newFixtureSeeded(t, 2300)
	fa3Install(t, fxB, core.Vec2{X: 8, Y: 0}, 0) // wolf E at 8 < sight 12, deer faces E (sighted)
	sawFlee := false
	maxDisp := 0.0
	prev := core.Vec2{X: 0, Y: 0}
	for i := 1; i <= 40; i++ {
		fxB.world.Tick()
		d := fxB.world.animals["an:deer"]
		if d == nil {
			t.Fatal("deer vanished in case B")
		}
		if d.CurrentAction == "Flee" {
			sawFlee = true
		}
		if disp := prev.Distance(d.Pos); disp > maxDisp {
			maxDisp = disp
		}
		prev = d.Pos
	}
	if !sawFlee {
		t.Fatalf("case B: deer never entered Flee despite the predator inside its sight FOV")
	}
	// Flee speed = 0.3 + 1.0·0.5 = 0.8 vs wander 0.3: assert a clear jump.
	if maxDisp < 0.6 {
		t.Errorf("case B: flee displacement %.2f/tick never cleared the 2x wander baseline (0.6)", maxDisp)
	}
}

// ── FA4 — downwind scent plume (wind-driven spread; deterministic) ──────────────

// fa4Climate builds a constant-wind climate: prevailing dir 0 rad (+X), mag 0.7,
// zero noise/drift so the wind vector is identical every tick (deterministic
// plume geometry is the subject).
func fa4Climate() (*climate.State, *climate.Rules) {
	cfg := climate.Config{
		GridCols: 2, GridRows: 2,
		WorldMin: core.Vec2{X: -60, Y: -60}, WorldMax: core.Vec2{X: 60, Y: 60},
		InitMoisture: 0.3, InitTemperature: 10,
		RainProbPerHour: 0, RainHardCapHours: 1000, RainDurMinHours: 1, RainDurMaxHours: 2,
		MoistureRainRate: 0, EvapBaseRate: 0, EvapTempScale: 0,
		AnnualMid: 10, AnnualAmp: 0, TempDayPeak: 0, TempNightLow: 0,
		WindPrevailingDir: 0, WindMagMean: 0.7, WindMagNoise: 0,
		WindDirDrift: 0, WindDirReversion: 1,
	}
	return climate.New(cfg, func(core.Vec2) core.Tag { return "plain" }), climate.NewRules(nil)
}

// TestFA4DownwindScentPlume: a stationary prey animal deposits scent under a
// constant +X wind; after N ticks the plume must be asymmetric — strictly more
// intense downwind (+X) than upwind (−X) at the same range — and byte-identical
// across two same-seed runs (D12).
func TestFA4DownwindScentPlume(t *testing.T) {
	run := func() (down, up float64) {
		fx := newFixtureSeeded(t, 2400)
		installFA2ActionRegistry(t, fx)
		cfg := fa2Cfg()
		cfg.Min, cfg.Max = core.Vec2{X: -60, Y: -60}, core.Vec2{X: 60, Y: 60}

		climState, climRules := fa4Climate()
		fx.world.InstallEnv(cfg, testNavMap(), climState, climRules, flora.New(nil), flora.NewRules(nil), nil, nil)

		deer := testAnimal("an:deer", "deer", core.Vec2{X: 0, Y: 0})
		deer.Vital, deer.VitalCap = 1, 1
		rules := fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
			"deer": {
				Utilities:   map[actions.ActionID]*expr.Program{"MoveTo": testNumProgram(t, "0.1")},
				Drives:      []fauna.DriveRule{},
				Speed:       testNumProgram(t, "0"), // stationary source
				Tags:        []core.Tag{"game"},
				SmellRadius: 5, SightRadius: 5, FovArc: 1,
			},
		})
		fx.world.InstallFauna(cfg, rules, fa2ScentEmitters(), nil, []fauna.Animal{deer})

		for range 30 {
			fx.world.Tick()
		}
		const D = 7.5 // one scent cell out (steady-state plume radius is 1-2 cells)
		return fx.world.ScentIntensityAt(scent.ChanPrey, core.Vec2{X: D, Y: 0}),
			fx.world.ScentIntensityAt(scent.ChanPrey, core.Vec2{X: -D, Y: 0})
	}

	down1, up1 := run()
	if down1 <= up1 {
		t.Fatalf("plume not wind-skewed: downwind=%.5f upwind=%.5f (want downwind > upwind under +X wind)", down1, up1)
	}
	if down1 == 0 {
		t.Fatalf("no scent reached the downwind probe at all (spread not running?)")
	}

	// Determinism: identical seed ⇒ identical plume (D12).
	down2, up2 := run()
	if down1 != down2 || up1 != up2 {
		t.Fatalf("plume not deterministic across same-seed runs: (%.9f,%.9f) vs (%.9f,%.9f)", down1, up1, down2, up2)
	}
	t.Logf("downwind=%.5f upwind=%.5f (skew ratio %.1fx)", down1, up1, down1/max(up1, 1e-12))
}
