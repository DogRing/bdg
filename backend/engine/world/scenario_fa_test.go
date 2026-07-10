package world

// FA scenario coverage map (docs/plans/scenarios-world.md, Phase 10):
//   FA1 graze loop        → TestGrazingReducesHungerAtForageFlora (fauna_combat_scenarios_test.go)
//   FA2 scent+FOV hunt    → TestFA2PredatorHuntChaseResolves (scenario_fa2_test.go)
//   FA3 wary↔flee band    → TestFA3WaryFleeBands (below)
//   FA4 downwind plume    → TestFA4DownwindScentPlume (below)
//   FA5 day/night thermal → TestFA5ClimateThermal (below) + TestThermalStressComfortBand
//                           (engine/fauna/thermal_test.go). RESOLVED: the thermal drive is a
//                           SYMMETRIC comfort-band stress = clamp01(|apparent_temp − comfort_temp|
//                           / thermal_band), so cold AND hot both raise it; apparent_temp stays °C
//                           for render (CA3). Decision: docs/decisions/fauna-gates.md (FA5); F40.
//   FA6 hidden respawn    → TestRespawnTopsUpToTarget (respawn_test.go)
//   FA7 herd/flocking     → DEFERRED (docs/plans/scenarios-world.md)
//   FA8 per-species terrain → TestWorldTerrainSamplerSemantics (fauna_test.go) + the ecosim
//                           containment assertions (engine/ecosim)

import (
	"math"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/env/climate"
	"github.com/dogring/bdg/engine/env/flora"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/space/navmap"
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

// ── FA5 — climate thermal (cold night AND hot noon → thermal ↑) ─────────────────

// fa5Climate builds a constant-temperature climate (no annual/daily swing, no
// rain/wind) so the deer's apparent_temp is a pure function of the injected
// midline °C — the test controls felt temperature directly and deterministically.
func fa5Climate(midC float64) (*climate.State, *climate.Rules) {
	cfg := climate.Config{
		GridCols: 2, GridRows: 2,
		WorldMin: core.Vec2{X: -200, Y: -200}, WorldMax: core.Vec2{X: 200, Y: 200},
		InitMoisture: 0, InitTemperature: midC,
		RainProbPerHour: 0, RainHardCapHours: 1000, RainDurMinHours: 1, RainDurMaxHours: 2,
		MoistureRainRate: 0, EvapBaseRate: 0, EvapTempScale: 0,
		AnnualMid: midC, AnnualAmp: 0, TempDayPeak: 0, TempNightLow: 0,
		WindPrevailingDir: 0, WindMagMean: 0, WindMagNoise: 0,
		WindDirDrift: 0, WindDirReversion: 1,
	}
	return climate.New(cfg, func(core.Vec2) core.Tag { return "plain" }), climate.NewRules(nil)
}

// TestFA5ClimateThermal is the FA5 production-path assertion (was NOT ENCODABLE
// before comfort_temp/thermal_band, docs/decisions/fauna-gates.md): a real deer
// under a constant climate feels thermal stress that RISES symmetrically as
// apparent_temp leaves its comfort band — ~0 at comfort, saturating toward 1
// under BOTH a cold night and a hot noon. apparent_temp stays °C (render); the
// comfort band is what normalizes it into the [0,1] thermal drive. This proves
// the climate → EnvSample.Temperature → apparent_temp → thermal wiring end-to-end.
func TestFA5ClimateThermal(t *testing.T) {
	const comfort, band = 12.0, 20.0
	deerThermal := func(midC float64) float64 {
		fx := newFixtureSeeded(t, 2500)
		installFA2ActionRegistry(t, fx)
		cfg := fa2Cfg() // DormantPeriod 1 → the deer fully re-arbitrates (sets thermal) every tick
		climState, climRules := fa5Climate(midC)
		fx.world.InstallEnv(cfg, testNavMap(), climState, climRules, flora.New(nil), flora.NewRules(nil), nil, nil)

		rules := fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
			"deer": {
				Utilities:   map[actions.ActionID]*expr.Program{"MoveTo": testNumProgram(t, "0.1")},
				Drives:      []fauna.DriveRule{{ID: "thermal"}},
				AppTemp:     testNumProgram(t, "temperature"), // apparent_temp = felt °C (isolate the band)
				ComfortTemp: comfort, ThermalBand: band,
				Speed:       testNumProgram(t, "0"),
				Tags:        []core.Tag{"game"},
				SmellRadius: 5, SightRadius: 5, FovArc: 1,
			},
		})
		deer := testAnimal("an:deer", "deer", core.Vec2{X: 0, Y: 0})
		deer.Vital, deer.VitalCap = 1, 1
		deer.CurrentAction = "MoveTo"
		fx.world.InstallFauna(cfg, rules, fa2ScentEmitters(), nil, []fauna.Animal{deer})

		for range 5 {
			fx.world.Tick()
		}
		return fx.world.animals["an:deer"].Drives["thermal"]
	}

	comfortT := deerThermal(comfort) // |12−12|/20 = 0
	coldT := deerThermal(-8)         // |−8−12|/20 = 1.0 (cold)
	hotT := deerThermal(32)          // |32−12|/20 = 1.0 (heat — symmetric)
	if comfortT > 1e-6 {
		t.Errorf("at comfort (%.0f°C): thermal %.4f, want ~0", comfort, comfortT)
	}
	if coldT < 0.9 {
		t.Errorf("cold climate (−8°C): thermal %.4f, want ≥0.9 (cold-night stress not encoded)", coldT)
	}
	if hotT < 0.9 {
		t.Errorf("hot climate (32°C): thermal %.4f, want ≥0.9 (symmetric heat stress not encoded)", hotT)
	}
	t.Logf("thermal: comfort=%.3f cold=%.3f hot=%.3f", comfortT, coldT, hotT)
}

// ── P_move1 — hazard-field avoidance (deer veers from a sea edge) ────────────────

// fa5SeaNavMap builds a navmap whose right side (x ≥ seaX) is IMPASSABLE sea and the
// rest is plain — a hard drowning hazard the world's danger field seeds from.
func fa5SeaNavMap(seaX float64) *navmap.NavMap {
	cfg := navmap.Config{
		CellSize: 5, MinX: -200, MinY: -200, MaxX: 200, MaxY: 200,
		WearCostMin: 0.5, WearMax: 1,
	}
	types := map[navmap.TerrainID]navmap.TerrainType{
		"plain": {BaseCost: 1, Passable: true},
		"sea":   {BaseCost: 1, Passable: false},
	}
	return navmap.New(cfg, func(p core.Vec2) navmap.TerrainID {
		if p.X >= seaX {
			return "sea"
		}
		return "plain"
	}, types)
}

// TestP_move1DeerVeersFromSea is the P_move1 production-path assertion (docs/plans/fauna.md §4): through
// real World.Tick(), the world builds a hazard field from the terrain and a deer heading toward a sea
// edge VEERS AWAY instead of pinning at it. Isolated by comparing a hazard-avoiding deer (e>0) against a
// byte-identical control with e=0 (same seed ⇒ same wander draws) — the only difference is the blend.
func TestP_move1DeerVeersFromSea(t *testing.T) {
	const seaX = 60.0
	// maxReach returns the deer's furthest-right X over N ticks (how close it got to the sea).
	maxReach := func(e float64) float64 {
		fx := newFixtureSeeded(t, 2600)
		installFA2ActionRegistry(t, fx)
		cfg := fa2Cfg()
		climState, climRules := fa5Climate(10)
		fx.world.InstallEnv(cfg, fa5SeaNavMap(seaX), climState, climRules, flora.New(nil), flora.NewRules(nil), nil, nil)

		rules := fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
			"deer": {
				Utilities:       map[actions.ActionID]*expr.Program{"MoveTo": testNumProgram(t, "0.1")},
				Drives:          []fauna.DriveRule{},
				Speed:           testNumProgram(t, "1.5"),
				HazardAvoidance: e,
				Tags:            []core.Tag{"game"},
				SmellRadius:     5, SightRadius: 5, FovArc: 1,
				SteerChannel: map[actions.ActionID]core.Tag{}, // MoveTo ⇒ continue heading
			},
		})
		deer := testAnimal("an:deer", "deer", core.Vec2{X: 45, Y: 0}) // 15 units left of the sea edge
		deer.Heading = 0                                              // pointed straight at the sea
		deer.Vital, deer.VitalCap = 1, 1
		deer.CurrentAction = "MoveTo"
		fx.world.InstallFauna(cfg, rules, fa2ScentEmitters(), nil, []fauna.Animal{deer})

		maxX := deer.Pos.X
		for range 40 {
			fx.world.Tick()
			if x := fx.world.animals["an:deer"].Pos.X; x > maxX {
				maxX = x
			}
		}
		return maxX
	}

	control := maxReach(0)   // no hazard blend: wanders toward the sea, pins at the edge
	avoider := maxReach(3.0) // strong hazard avoidance: veers away before the edge
	if !(avoider < control) {
		t.Errorf("hazard-avoiding deer should stay farther from the sea than the control: avoider maxX=%.2f control maxX=%.2f", avoider, control)
	}
	if avoider >= seaX {
		t.Errorf("hazard-avoiding deer reached the sea edge (x=%.2f ≥ %.1f) — it should veer away", avoider, seaX)
	}
	t.Logf("max reach toward sea (x=%.0f): control=%.2f avoider=%.2f", seaX, control, avoider)
}

// TestP_move1HazardOffNoDangerIsByteIdentical guards the typed-nil interface branch in
// buildFaunaSnapshot: with NO dangerous terrain the world builds NO hazard field, so
// Snapshot.HazardField must be a TRUE nil interface — not a non-nil interface wrapping a nil
// *field.Field (which would make `snap.HazardField != nil` true and call Repulsion on a nil field).
// A hazard-avoiding deer (e>0) on all-passable terrain must therefore move byte-identically to an
// e=0 control — no panic, no bend — through real World.Tick().
func TestP_move1HazardOffNoDangerIsByteIdentical(t *testing.T) {
	const noSeaX = 10000.0 // sea edge outside bounds ⇒ all-plain ⇒ zero danger ⇒ nil hazard field
	trajectory := func(e float64) []core.Vec2 {
		fx := newFixtureSeeded(t, 2600)
		installFA2ActionRegistry(t, fx)
		cfg := fa2Cfg()
		climState, climRules := fa5Climate(10)
		fx.world.InstallEnv(cfg, fa5SeaNavMap(noSeaX), climState, climRules, flora.New(nil), flora.NewRules(nil), nil, nil)

		rules := fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
			"deer": {
				Utilities:       map[actions.ActionID]*expr.Program{"MoveTo": testNumProgram(t, "0.1")},
				Drives:          []fauna.DriveRule{},
				Speed:           testNumProgram(t, "1.5"),
				HazardAvoidance: e,
				Tags:            []core.Tag{"game"},
				SmellRadius:     5, SightRadius: 5, FovArc: 1,
				SteerChannel: map[actions.ActionID]core.Tag{}, // MoveTo ⇒ continue heading
			},
		})
		deer := testAnimal("an:deer", "deer", core.Vec2{X: 0, Y: 0})
		deer.Heading = 0
		deer.Vital, deer.VitalCap = 1, 1
		deer.CurrentAction = "MoveTo"
		fx.world.InstallFauna(cfg, rules, fa2ScentEmitters(), nil, []fauna.Animal{deer})

		var path []core.Vec2
		for range 20 {
			fx.world.Tick()
			path = append(path, fx.world.animals["an:deer"].Pos)
		}
		return path
	}

	control := trajectory(0)   // no hazard blend
	avoider := trajectory(3.0) // e>0 but no dangerous terrain ⇒ nil field ⇒ must not diverge (would panic if typed-nil boxed)
	for i := range control {
		if control[i] != avoider[i] {
			t.Fatalf("hazard-off (no dangerous terrain) must be byte-identical for any e: tick %d control=%v avoider=%v", i, control[i], avoider[i])
		}
	}
}

// installSleepActionRegistry gives the world a Sleep action tagged state:sleep (torpor) + MoveTo, so
// the world's actionHasTag(Sleep, state:sleep) deep-fatigue-recovery path and the deer's candidate
// set resolve (P_sleep1).
func installSleepActionRegistry(t *testing.T, fx *testFixture) {
	t.Helper()
	reg, err := actions.Load(strings.NewReader(`schema_version: 1
actions:
  - id: Sleep
    tags: [effort:none, state:sleep]
    duration: 1
    produces: [safe]
  - id: MoveTo
    tags: [effort:low]
    duration: 1
    produces: [at_target]
`))
	if err != nil {
		t.Fatalf("actions.Load sleep registry: %v", err)
	}
	fx.world.actReg = reg
	fx.actReg = reg
	fx.svc.Actions = reg
	fx.world.svc.Actions = reg
}

// TestP_sleep1NightSleepRecoversFatigue is the P_sleep1 production-path assertion: through real
// World.Tick(), the world injects a clock-derived `daylight` into the fauna EnvSample; at a
// near-midnight tick (daylight≈0) a diurnal deer's Sleep utility (1-daylight) wins, so the deer
// SLEEPS (state:sleep) — stays put (torpor no-loco) AND recovers fatigue at the DEEP sleep rate
// (SleepFatigueRecoverPerTick), proving the whole chain: clock→daylight operand→§6 selection→
// torpor steer→world deep-recovery wiring.
func TestP_sleep1NightSleepRecoversFatigue(t *testing.T) {
	fx := newFixtureSeeded(t, 99)
	installSleepActionRegistry(t, fx)
	cfg := fa2Cfg()
	cfg.FaunaCombat.SleepFatigueRecoverPerTick = 0.02 // deep torpor recovery
	fx.world.InstallEnv(cfg, fa5SeaNavMap(10000), nil, nil, flora.New(nil), flora.NewRules(nil), nil, nil)

	rules := fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		"deer": {
			Utilities: map[actions.ActionID]*expr.Program{
				"Sleep":  testNumProgram(t, "1 - daylight"),
				"MoveTo": testNumProgram(t, "0.1"),
			},
			Drives:      []fauna.DriveRule{{ID: "fatigue", Rate: 0.001}}, // accumulator (rate>0 keeps it a real drive, not the thermal default branch)
			Speed:       testNumProgram(t, "1.5"),
			Tags:        []core.Tag{"game"},
			SmellRadius: 5, SightRadius: 5, FovArc: 1,
			SteerChannel: map[actions.ActionID]core.Tag{"Sleep": fauna.TagSleep},
		},
	})
	deer := testAnimal("an:deer", "deer", core.Vec2{X: 0, Y: 0})
	deer.Heading = 0
	deer.Vital, deer.VitalCap = 1, 1
	deer.CurrentAction = "MoveTo"
	deer.Drives = map[fauna.DriveID]float64{"fatigue": 0.5}
	fx.world.InstallFauna(cfg, rules, fa2ScentEmitters(), nil, []fauna.Animal{deer})

	fx.world.Tick() // tick → near midnight ⇒ daylight ≈ 0 (deep night)

	got := fx.world.animals["an:deer"]
	if got.CurrentAction != "Sleep" {
		t.Fatalf("night (daylight≈0): deer should Sleep, got CurrentAction=%q", got.CurrentAction)
	}
	if got.Pos != (core.Vec2{X: 0, Y: 0}) {
		t.Errorf("sleeping deer (torpor no-loco) must stay put, Pos=%v", got.Pos)
	}
	// Deep recovery: DriveUpdate accrues rate·dt (0.001·1), then world sleep-recovery subtracts
	// SleepFatigueRecoverPerTick (0.02): 0.5 + 0.001 − 0.02 = 0.481. The net DROP proves the deep
	// torpor rate applied (a mis-wire would leave 0.501 = accrual only, or hit 0 = no drive).
	if math.Abs(got.Drives["fatigue"]-0.481) > 1e-9 {
		t.Errorf("sleep must recover fatigue by the deep rate 0.02: fatigue=%.6f, want 0.481", got.Drives["fatigue"])
	}
}

// TestP_sleep1ZeroDeepRateFallsBackToRest locks the SS2 neutrality: with SleepFatigueRecoverPerTick=0
// the guarded torpor case is skipped, so a sleeper falls through to the ordinary effort:none rest rate
// (FatigueRecoverPerTick) — a sleeping animal never recovers LESS than a resting one.
func TestP_sleep1ZeroDeepRateFallsBackToRest(t *testing.T) {
	fx := newFixtureSeeded(t, 99)
	installSleepActionRegistry(t, fx)
	cfg := fa2Cfg()
	cfg.FaunaCombat.SleepFatigueRecoverPerTick = 0 // deep rate UNSET ⇒ fall through to ordinary rest
	cfg.FaunaCombat.FatigueRecoverPerTick = 0.01   // ordinary effort:none rest rate
	fx.world.InstallEnv(cfg, fa5SeaNavMap(10000), nil, nil, flora.New(nil), flora.NewRules(nil), nil, nil)

	rules := fauna.NewRules(map[fauna.SpeciesID]fauna.SpeciesRule{
		"deer": {
			Utilities: map[actions.ActionID]*expr.Program{
				"Sleep":  testNumProgram(t, "1 - daylight"),
				"MoveTo": testNumProgram(t, "0.1"),
			},
			Drives:      []fauna.DriveRule{{ID: "fatigue", Rate: 0.001}},
			Speed:       testNumProgram(t, "1.5"),
			Tags:        []core.Tag{"game"},
			SmellRadius: 5, SightRadius: 5, FovArc: 1,
			SteerChannel: map[actions.ActionID]core.Tag{"Sleep": fauna.TagSleep},
		},
	})
	deer := testAnimal("an:deer", "deer", core.Vec2{X: 0, Y: 0})
	deer.CurrentAction = "MoveTo"
	deer.Drives = map[fauna.DriveID]float64{"fatigue": 0.5}
	fx.world.InstallFauna(cfg, rules, fa2ScentEmitters(), nil, []fauna.Animal{deer})

	fx.world.Tick() // night: deer sleeps, but deep rate 0 ⇒ ordinary rest recovery applies

	got := fx.world.animals["an:deer"]
	if got.CurrentAction != "Sleep" {
		t.Fatalf("night: deer should still Sleep, got %q", got.CurrentAction)
	}
	// 0.5 + accrual 0.001 − ordinary FatigueRecoverPerTick 0.01 = 0.491 (NOT 0.501 = no recovery).
	if math.Abs(got.Drives["fatigue"]-0.491) > 1e-9 {
		t.Errorf("zero deep rate must fall back to ordinary rest recovery (0.01): fatigue=%.6f, want 0.491", got.Drives["fatigue"])
	}
}
