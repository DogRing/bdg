package world

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/env/climate"
	"github.com/dogring/bdg/engine/env/flora"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/space/scent"
)

// ── WorldState env-field capture (env-OFF neutrality) ───────────────────────

func TestWorldStateEnvFieldsAbsentWhenOff(t *testing.T) {
	fx := newFixtureSeeded(t, 300)
	spawnTwoAgents(t, fx, 1)
	fx.world.Tick()

	ws := fx.world.State()
	if ws.Flora != nil {
		t.Errorf("WorldState.Flora = %+v, want nil (env OFF)", ws.Flora)
	}
	if ws.Animals != nil {
		t.Errorf("WorldState.Animals = %+v, want nil (env OFF)", ws.Animals)
	}
	if ws.Climate != nil {
		t.Errorf("WorldState.Climate = %+v, want nil (env OFF)", ws.Climate)
	}
}

// ── envPieces: a fresh, self-contained flora+fauna+climate install ─────────
//
// buildResumeEnvPieces returns brand-new instances every call (navmap.New/
// climate.New/flora.New/fauna.NewRules are pure constructors) so it can be
// called independently for two or three fixtures that must behave identically
// (D12 — mirrors the existing two-fixture-same-seed pattern already used
// throughout this package, e.g. TestEnvForksAreDeterministicAndDisjointByChannel).
// Climate Rules are intentionally EMPTY (no transitions): navmap terrain-
// override/wear serialization is a pre-existing, separately-tracked gap
// (data-contracts §6 "finalized when engine/space/navmap serialization
// lands") — exercising climate transitions here would depend on that gap,
// not on this change, so the resume test below sidesteps it deliberately.
type envPieces struct {
	cfg           EnvConfig
	climState     *climate.State
	climRules     *climate.Rules
	floraState    *flora.State
	floraRules    *flora.Rules
	faunaRules    *fauna.Rules
	scentEmitters map[core.Tag][]core.Tag
	animals       []fauna.Animal
}

func buildResumeEnvPieces(t *testing.T) envPieces {
	t.Helper()
	cfg := testEnvConfig()
	cfg.ScentSpread = 2 // exercise both the spread and non-spread cadence branches

	climCfg := climate.Config{
		GridCols: 2, GridRows: 2,
		WorldMin: cfg.Min, WorldMax: cfg.Max,
		InitMoisture: 0.3, InitTemperature: 10,
		RainProbPerHour: 0.05, RainHardCapHours: 500, RainDurMinHours: 2, RainDurMaxHours: 4,
		MoistureRainRate: 0.05,
		EvapBaseRate:     0.01, EvapTempScale: 0.001,
		AnnualMid: 10, AnnualAmp: 5, TempDayPeak: 2, TempNightLow: -2,
		WindPrevailingDir: 1, WindMagMean: 0.3, WindMagNoise: 0.05,
		WindDirDrift: 0.05, WindDirReversion: 0.1,
		IceType: "ice",
	}
	climState := climate.New(climCfg, func(core.Vec2) core.Tag { return "plain" })
	// Include an already-frozen cell in every snapshot/resume fixture. The climate digest must
	// preserve its exact water origin even though the ordinary fixture has transitions disabled.
	climCells := climState.Cells()
	climCells[0].State.Terrain = "ice"
	climCells[0].State.FrozenFrom = "lake"
	climState = climate.Restore(climState, climCells, climState.Rain(), climState.Wind(), climState.SnowCover())
	climRules := climate.NewRules(nil)

	plant := flora.Plant{ID: "resume_plant", Species: "moss", Pos: core.Vec2{X: 5, Y: 5}, Length: 0.1, Width: 0.1}
	floraState := flora.New([]flora.Plant{plant})
	floraRules := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{
		"moss": {
			Suitability: testNumProgram(t, "1"),
			LengthRate:  testNumProgram(t, "moisture"), WidthRate: testNumProgram(t, "0.01"),
			ShadeRadius: testNumProgram(t, "0"), ShadeOpacity: testNumProgram(t, "0"),
			Stages:     []float64{0, 1, 2},
			PropRadius: testNumProgram(t, "0"), PropChance: testNumProgram(t, "0"),
		},
	})

	animals := []fauna.Animal{
		testAnimal("an:deer1", "deer", core.Vec2{X: 3, Y: 3}),
		testAnimal("an:wolf1", "wolf", core.Vec2{X: 12, Y: 12}),
	}

	return envPieces{
		cfg: cfg, climState: climState, climRules: climRules,
		floraState: floraState, floraRules: floraRules,
		faunaRules: testFaunaRules(t), scentEmitters: testScentEmitters(), animals: animals,
	}
}

func installEnvPieces(fx *testFixture, p envPieces) {
	for _, plant := range p.floraState.Plants() {
		fx.world.PlaceObject(plant.ID, core.Tag(plant.Species), plant.Pos, nil)
	}
	fx.world.InstallEnv(p.cfg, testNavMap(), p.climState, p.climRules, p.floraState, p.floraRules, nil, nil)
	fx.world.InstallFauna(p.cfg, p.faunaRules, p.scentEmitters, nil, p.animals)
}

// envDigest is a byte-stable text digest of the world's env-relevant state
// (flora/animals/climate/scent/rng) — the determinism-golden comparison basis
// for the resume test below. Sorted throughout (D12); agents are intentionally
// excluded (this fixture spawns none).
func envDigest(w *World) string {
	ws := w.State()
	var b strings.Builder
	fmt.Fprintf(&b, "tick=%d\n", int64(ws.Tick))

	for _, p := range ws.Flora {
		fmt.Fprintf(&b, "flora %s pos=%.6f,%.6f len=%.6f wid=%.6f death=%d\n",
			p.ObjectID, p.Pos.X, p.Pos.Y, p.Length, p.Width, p.DeathStreak)
	}

	for _, a := range ws.Animals {
		fmt.Fprintf(&b, "animal %s pos=%.6f,%.6f stamina=%.6f vital=%.6f vitalcap=%.6f heading=%.6f action=%s active=%d engaged=%s nextex=%d cooldown=%d hidden=%d conceal=%.6f\n",
			a.ObjectID, a.Pos.X, a.Pos.Y, a.Stamina, a.Vital, a.VitalCap, a.Heading, a.CurrentAction,
			int64(a.ActiveUntil), a.EngagedWith, int64(a.NextExchangeTick), int64(a.EngageCooldownUntil),
			int64(a.HiddenUntil), a.Concealment)
		keys := make([]string, 0, len(a.Drives))
		for k := range a.Drives {
			keys = append(keys, string(k))
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "  drive %s=%.6f\n", k, a.Drives[fauna.DriveID(k)])
		}
	}

	if ws.Climate != nil {
		for _, c := range ws.Climate.Cells {
			fmt.Fprintf(&b, "cell %d,%d moist=%.6f temp=%.6f terrain=%s frozen_from=%s\n",
				c.Cell.X, c.Cell.Y, c.Moisture, c.Temperature, c.Terrain, c.FrozenFrom)
		}
		fmt.Fprintf(&b, "rain=%v ends=%d prain=%.6f sincerain=%d\n",
			ws.Climate.Rain.Raining, ws.Climate.Rain.RainEndsAtHour, ws.Climate.Rain.PRain, ws.Climate.Rain.HoursSinceRain)
		fmt.Fprintf(&b, "wind dir=%.6f mag=%.6f\n", ws.Climate.Wind.Dir, ws.Climate.Wind.Mag)
	}

	// Scent is derived (not serialized) — comparing it here exercises rebuildScent
	// indirectly, at whatever the animals' post-tick positions ended up being.
	for _, a := range ws.Animals {
		ch := scent.ChanPrey
		if a.Species == "wolf" {
			ch = scent.ChanPredator
		}
		fmt.Fprintf(&b, "scent chan=%d @%s=%.6f\n", ch, a.ObjectID, w.ScentIntensityAt(ch, a.Pos))
	}
	b.WriteString("rng=" + ws.RNGState.Data + "\n")
	return b.String()
}

// ── AC: Flora/animals/climate round-trip ────────────────────────────────────

func TestWorldStateEnvRoundTrip(t *testing.T) {
	fx := newFixtureSeeded(t, 301)
	installEnvPieces(fx, buildResumeEnvPieces(t))
	for range 5 {
		fx.world.Tick()
	}
	ws := fx.world.State()
	if len(ws.Flora) == 0 || len(ws.Animals) == 0 || ws.Climate == nil {
		t.Fatalf("captured WorldState missing env blocks: flora=%d animals=%d climate=%v",
			len(ws.Flora), len(ws.Animals), ws.Climate)
	}

	fx2 := newFixtureSeeded(t, 301)
	installEnvPieces(fx2, buildResumeEnvPieces(t))
	fx2.world.RestoreState(ws)

	got := fx2.world.State()
	if len(got.Flora) != len(ws.Flora) || got.Flora[0] != ws.Flora[0] {
		t.Errorf("Flora round-trip mismatch:\ngot:  %+v\nwant: %+v", got.Flora, ws.Flora)
	}
	if len(got.Animals) != len(ws.Animals) {
		t.Fatalf("Animals round-trip length mismatch: got %d want %d", len(got.Animals), len(ws.Animals))
	}
	for i := range ws.Animals {
		// Full-digest DeepEqual: covers Stats/Drives maps AND the Phase-6
		// combat/hiding fields (vital_cap, engaged_with, next_exchange_tick,
		// engage_cooldown_until, hidden_until, concealment) the resume invariant
		// requires to survive intact (data-contracts §10).
		if !reflect.DeepEqual(got.Animals[i], ws.Animals[i]) {
			t.Errorf("Animal[%d] round-trip mismatch:\ngot:  %+v\nwant: %+v", i, got.Animals[i], ws.Animals[i])
		}
	}
	if got.Climate == nil || len(got.Climate.Cells) != len(ws.Climate.Cells) {
		t.Fatalf("Climate round-trip missing/short: %+v", got.Climate)
	}
	for i := range ws.Climate.Cells {
		if got.Climate.Cells[i] != ws.Climate.Cells[i] {
			t.Errorf("Climate cell[%d] round-trip mismatch: got %+v want %+v", i, got.Climate.Cells[i], ws.Climate.Cells[i])
		}
	}
	if got.Climate.Rain != ws.Climate.Rain || got.Climate.Wind != ws.Climate.Wind {
		t.Errorf("Climate rain/wind round-trip mismatch: got %+v/%+v want %+v/%+v",
			got.Climate.Rain, got.Climate.Wind, ws.Climate.Rain, ws.Climate.Wind)
	}

	// The restored frozen cell must still resolve __origin__ to lake, never to the sentinel.
	thawRules := climate.NewRules([]climate.TransitionRule{
		{From: "ice", When: testBoolProgram(t, "temperature > 2"), To: climate.OriginTerrain},
	})
	thawed, transitions := climate.Step(
		fx2.world.climateState,
		climate.Forcing{HourOfDay: 14},
		thawRules,
		rng.New(999),
	)
	if len(transitions) != 1 || transitions[0].From != "ice" || transitions[0].To != "lake" {
		t.Fatalf("restored frozen cell thaw transitions = %+v, want ice->lake", transitions)
	}
	if cell := thawed.Cells()[0].State; cell.Terrain != "lake" || cell.FrozenFrom != "" {
		t.Fatalf("restored frozen cell after thaw = %+v, want lake with empty FrozenFrom", cell)
	}
}

// ── AC: Scent rebuilt, not serialized ────────────────────────────────────────

func TestWorldStateScentNotSerialized(t *testing.T) {
	fx := newFixtureSeeded(t, 302)
	installEnvPieces(fx, buildResumeEnvPieces(t))
	for range 3 {
		fx.world.Tick()
	}
	blob := fmt.Sprint(fx.world.State())
	if strings.Contains(strings.ToLower(blob), "scent") {
		t.Fatalf("WorldState contains a 'scent' field — the scent grid must not be serialized")
	}
}

// ── AC: Determinism golden — resume with env installed ──────────────────────
//
// Captures at tick T=5, restores into a fresh world, runs K=5 more ticks, and
// compares the result to an uninterrupted 0→10 run of an independent (same-
// seed) world. Exercises the flora/animal/climate restore AND the scent-grid
// rebuild (rebuildScent) together — if either were wrong, fauna steering
// (which reads scent + climate) would diverge within a few ticks.
func TestResumeInvariantEnvInstalled(t *testing.T) {
	const T, K = 5, 5

	// Uninterrupted 0 → T+K.
	uninterrupted := newFixtureSeeded(t, 303)
	installEnvPieces(uninterrupted, buildResumeEnvPieces(t))
	for range T + K {
		uninterrupted.world.Tick()
	}
	want := envDigest(uninterrupted.world)

	// Capture at T from an independent (same-seed) world.
	source := newFixtureSeeded(t, 303)
	installEnvPieces(source, buildResumeEnvPieces(t))
	for range T {
		source.world.Tick()
	}
	ws := source.world.State()

	// Restore into a FRESH world (fresh env pieces, same config/content) and
	// run the remaining K ticks.
	resumed := newFixtureSeeded(t, 303)
	installEnvPieces(resumed, buildResumeEnvPieces(t))
	resumed.world.RestoreState(ws)
	for range K {
		resumed.world.Tick()
	}
	got := envDigest(resumed.world)

	if got != want {
		t.Fatalf("resume invariant broken:\nresumed (T=%d+K=%d):\n%s\nuninterrupted (0→%d):\n%s", T, K, got, T+K, want)
	}
}
