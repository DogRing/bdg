package world

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/env/climate"
	"github.com/dogring/bdg/engine/env/flora"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/space/navmap"
)

// ── envInstalled gating ──────────────────────────────────────────────────────

func TestEnvInstalledGating(t *testing.T) {
	fx := newFixtureSeeded(t, 200)
	if fx.world.envInstalled() {
		t.Fatalf("envInstalled() = true on a fresh world, want false")
	}
	fx.world.InstallEnv(testEnvConfig(), testNavMap(), nil, nil, nil, nil, nil, nil)
	if !fx.world.envInstalled() {
		t.Fatalf("envInstalled() = false after InstallEnv(nav), want true")
	}

	fx2 := newFixtureSeeded(t, 201)
	fx2.world.InstallFauna(testEnvConfig(), testFaunaRules(t), testScentEmitters(), nil, nil)
	if !fx2.world.envInstalled() {
		t.Fatalf("envInstalled() = false after InstallFauna, want true")
	}
}

// ── WorldFrame emission gate (env-OFF ⇒ no WorldFrame at all) ───────────────

func TestWorldFrameNotEmittedWhenEnvOff(t *testing.T) {
	fx := newFixtureSeeded(t, 202)
	spawnTwoAgents(t, fx, 1)
	for range 3 {
		fx.world.Tick()
	}
	for _, e := range fx.emit.events {
		if e.Type == "WorldFrame" {
			t.Fatalf("WorldFrame emitted on an env-OFF world (tick %d)", e.Tick)
		}
	}
}

// ── WorldFrame shape + god-view exclusion ───────────────────────────────────

func newEnvFixture(t *testing.T, seed int64) *testFixture {
	t.Helper()
	fx := newFixtureSeeded(t, seed)
	cfg := testEnvConfig()
	nav := testNavMap()

	climCfg := climate.Config{
		GridCols: 2, GridRows: 2,
		WorldMin: cfg.Min, WorldMax: cfg.Max,
		InitMoisture: 0.4, InitTemperature: 12,
		RainHardCapHours: 1000, RainDurMinHours: 1, RainDurMaxHours: 1,
		EvapBaseRate: 0.1, AnnualMid: 12,
	}
	climState := climate.New(climCfg, func(core.Vec2) core.Tag { return "plain" })
	climRules := climate.NewRules(nil)

	plant := flora.Plant{ID: "rf_plant_1", Species: "grass", Pos: core.Vec2{X: 5, Y: 5}, Length: 1, Width: 1}
	flState := flora.New([]flora.Plant{plant})
	flRules := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{
		"grass": {
			Suitability: testNumProgram(t, "1"),
			LengthRate:  testNumProgram(t, "0"), WidthRate: testNumProgram(t, "0"),
			ShadeRadius: testNumProgram(t, "0"), ShadeOpacity: testNumProgram(t, "0"),
			Stages:         []float64{0, 2},
			PropRadius:     testNumProgram(t, "0"),
			PropChance:     testNumProgram(t, "0"),
			PropagateStage: 5,
		},
	})
	fx.world.PlaceObject(plant.ID, "grass", plant.Pos, nil)
	fx.world.InstallEnv(cfg, nav, climState, climRules, flState, flRules, nil, nil)
	fx.world.InstallFauna(cfg, testFaunaRules(t), testScentEmitters(), nil, []fauna.Animal{
		testAnimal("an:deer1", "deer", core.Vec2{X: 3, Y: 3}),
	})
	return fx
}

func TestWorldFrameEmittedShapeAndGodViewExclusion(t *testing.T) {
	fx := newEnvFixture(t, 203)
	fx.world.Tick()

	var frame *core.Event
	for i := range fx.emit.events {
		if fx.emit.events[i].Type == "WorldFrame" {
			frame = &fx.emit.events[i]
		}
	}
	if frame == nil {
		t.Fatalf("no WorldFrame event emitted with env installed")
	}
	payload, ok := frame.Payload.(map[string]any)
	if !ok {
		t.Fatalf("WorldFrame payload is not a map: %T", frame.Payload)
	}
	for _, key := range []string{
		"tick", "hour_of_day", "day_night", "temperature", "raining", "wind",
		"agents", "animals", "flora_delta", "terrain_delta",
	} {
		if _, ok := payload[key]; !ok {
			t.Errorf("WorldFrame payload missing key %q: %+v", key, payload)
		}
	}
	if _, ok := payload["apparent_temp"]; ok {
		t.Errorf("WorldFrame payload carries apparent_temp — expected omitted (optional, no representative value)")
	}

	// God-view guard: no real_stats/tom_digest/raw stats/drives/vital vocabulary
	// anywhere in the serialized payload (data-contracts §4).
	dump := fmt.Sprint(payload)
	for _, forbidden := range []string{"real_stats", "tom_digest", "\"stats\"", "\"drives\"", "\"vital\""} {
		if strings.Contains(dump, forbidden) {
			t.Errorf("WorldFrame payload leaks god-view field %q: %s", forbidden, dump)
		}
	}

	animals, ok := payload["animals"].([]map[string]any)
	if !ok || len(animals) == 0 {
		t.Fatalf("WorldFrame.animals missing/empty: %+v", payload["animals"])
	}
	a0 := animals[0]
	for _, key := range []string{"id", "pos", "species", "action", "heading", "stamina"} {
		if _, ok := a0[key]; !ok {
			t.Errorf("WorldFrame.animals[0] missing key %q: %+v", key, a0)
		}
	}
	for _, forbidden := range []string{"stats", "drives", "vital"} {
		if _, ok := a0[forbidden]; ok {
			t.Errorf("WorldFrame.animals[0] leaks god-view field %q", forbidden)
		}
	}
}

// TestRenderViewStructuralGodViewGuard asserts AnimalRenderView carries exactly
// the render-relevant field set — a reflection guard so a future edit cannot
// silently reintroduce Stats/Drives/Vital onto the render-visible type (mirrors
// persist.AgentView's structural god-view guard).
func TestRenderViewStructuralGodViewGuard(t *testing.T) {
	want := map[string]bool{"ID": true, "Species": true, "Pos": true, "Action": true, "Heading": true, "Stamina": true}
	typ := reflect.TypeOf(AnimalRenderView{})
	if typ.NumField() != len(want) {
		t.Fatalf("AnimalRenderView has %d fields, want %d (%v)", typ.NumField(), len(want), want)
	}
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if !want[name] {
			t.Errorf("AnimalRenderView has unexpected field %q (god-view leak risk)", name)
		}
	}
}

// ── RenderView() ─────────────────────────────────────────────────────────────

func TestRenderViewBuildsEnvProjection(t *testing.T) {
	fx := newEnvFixture(t, 204)
	fx.world.Tick()

	rv := fx.world.RenderView()
	if !rv.ClimateOn {
		t.Fatalf("RenderView.ClimateOn = false with climate installed")
	}
	if len(rv.Animals) != 1 || rv.Animals[0].ID != "an:deer1" {
		t.Fatalf("RenderView.Animals = %+v, want one an:deer1", rv.Animals)
	}
	if len(rv.Flora) != 1 || rv.Flora[0].ID != "rf_plant_1" {
		t.Fatalf("RenderView.Flora = %+v, want one rf_plant_1", rv.Flora)
	}
	if rv.Terrain == nil || rv.Terrain.W <= 0 || rv.Terrain.H <= 0 {
		t.Fatalf("RenderView.Terrain = %+v, want a populated grid", rv.Terrain)
	}
	if len(rv.Terrain.Terrain) != rv.Terrain.W*rv.Terrain.H {
		t.Fatalf("Terrain.Terrain len=%d, want %d (W*H)", len(rv.Terrain.Terrain), rv.Terrain.W*rv.Terrain.H)
	}
}

func TestRenderViewEnvOffIsZero(t *testing.T) {
	fx := newFixtureSeeded(t, 205)
	rv := fx.world.RenderView()
	if rv.ClimateOn || len(rv.Animals) != 0 || len(rv.Flora) != 0 || rv.Terrain != nil {
		t.Fatalf("RenderView on env-off world not neutral: %+v", rv)
	}
}

// ── Terrain grid ─────────────────────────────────────────────────────────────

func TestBuildTerrainGridDimensionsAndOverride(t *testing.T) {
	fx := newFixtureSeeded(t, 206)
	cfg := testEnvConfig() // Min={0,0} Max={20,20} NavmapCellSize=5 ⇒ 4x4 grid
	nav := testNavMap()
	fx.world.InstallEnv(cfg, nav, nil, nil, nil, nil, nil, nil)

	grid := fx.world.buildTerrainGrid()
	if grid == nil {
		t.Fatalf("buildTerrainGrid() = nil with navmap installed")
	}
	if grid.W != 4 || grid.H != 4 {
		t.Fatalf("grid = %dx%d, want 4x4", grid.W, grid.H)
	}
	if grid.CellSize != 5 {
		t.Fatalf("grid.CellSize = %v, want 5", grid.CellSize)
	}
	if len(grid.Terrain) != 16 || len(grid.Wear) != 16 {
		t.Fatalf("grid arrays len terrain=%d wear=%d, want 16/16", len(grid.Terrain), len(grid.Wear))
	}
	for i, terr := range grid.Terrain {
		if terr != "plain" {
			t.Fatalf("cell %d terrain = %q, want plain (base layout)", i, terr)
		}
	}

	// A SetTerrain override is reflected (TerrainAt is override-aware).
	nav.SetTerrain([]navmap.Cell{{X: 1, Y: 1}}, "wet")
	grid2 := fx.world.buildTerrainGrid()
	idx := 1*grid2.W + 1
	if grid2.Terrain[idx] != "wet" {
		t.Fatalf("cell (1,1) after SetTerrain = %q, want wet", grid2.Terrain[idx])
	}
}

func TestBuildTerrainGridNilWhenNavOff(t *testing.T) {
	fx := newFixtureSeeded(t, 207)
	if got := fx.world.buildTerrainGrid(); got != nil {
		t.Fatalf("buildTerrainGrid() = %+v, want nil with navmap OFF", got)
	}
}

// ── Small pure helpers ───────────────────────────────────────────────────────

func TestDayNightOf(t *testing.T) {
	cases := []struct {
		hour int
		want string
	}{
		{0, "night"}, {5, "night"}, {6, "day"}, {12, "day"}, {17, "day"}, {18, "night"}, {23, "night"},
	}
	for _, c := range cases {
		if got := dayNightOf(c.hour); got != c.want {
			t.Errorf("dayNightOf(%d) = %q, want %q", c.hour, got, c.want)
		}
	}
}

func TestAmbientClimateAverages(t *testing.T) {
	cfg := climate.Config{
		GridCols: 2, GridRows: 1,
		WorldMin: core.Vec2{}, WorldMax: core.Vec2{X: 10, Y: 10},
		InitMoisture: 0.5, InitTemperature: 10,
	}
	// Two cells with different starting terrain-derived state would require a
	// transition; simplest deterministic check: uniform init averages to itself.
	s := climate.New(cfg, func(core.Vec2) core.Tag { return "plain" })
	temp, moist := ambientClimate(s)
	if temp != 10 || moist != 0.5 {
		t.Fatalf("ambientClimate = (%.2f,%.2f), want (10,0.5)", temp, moist)
	}
}

// ── Flora lifecycle events + frame delta ────────────────────────────────────

// baseWeedRule is a minimal always-suitable species rule; individual tests
// override Suitability/PropChance/DeathThreshold as needed.
func baseWeedRule(t *testing.T) flora.SpeciesRule {
	t.Helper()
	return flora.SpeciesRule{
		Suitability: testNumProgram(t, "1"),
		LengthRate:  testNumProgram(t, "0"), WidthRate: testNumProgram(t, "0"),
		ShadeRadius: testNumProgram(t, "0"), ShadeOpacity: testNumProgram(t, "0"),
		Stages:     []float64{0, 1},
		PropRadius: testNumProgram(t, "0"),
		PropChance: testNumProgram(t, "0"),
	}
}

func TestPlantSpawnedEventAndFrameDelta(t *testing.T) {
	fx := newFixtureSeeded(t, 208)
	cfg := testEnvConfig()

	plant := flora.Plant{ID: "weed_1", Species: "weed", Pos: core.Vec2{X: 5, Y: 5}, Length: 1, Width: 1}
	flState := flora.New([]flora.Plant{plant})
	rule := baseWeedRule(t)
	rule.PropChance = testNumProgram(t, "1") // always propagate
	rule.PropagateStage = 0
	flRules := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{"weed": rule})
	fx.world.PlaceObject(plant.ID, "weed", plant.Pos, nil)
	fx.world.InstallEnv(cfg, nil, nil, nil, flState, flRules, nil, nil)

	fx.world.Tick()

	var spawned *core.Event
	for i := range fx.emit.events {
		if fx.emit.events[i].Type == "PlantSpawned" {
			spawned = &fx.emit.events[i]
		}
	}
	if spawned == nil {
		t.Fatalf("no PlantSpawned event; flora %+v", fx.world.floraState.Plants())
	}
	sp := spawned.Payload.(map[string]any)
	for _, key := range []string{"object_id", "species", "pos"} {
		if _, ok := sp[key]; !ok {
			t.Errorf("PlantSpawned payload missing %q: %+v", key, sp)
		}
	}
	if sp["species"] != "weed" {
		t.Errorf("PlantSpawned species = %v, want weed", sp["species"])
	}

	// The spawn is also reflected in the sparse WorldFrame flora_delta buffer.
	foundDelta := false
	for _, e := range fx.world.pendingFloraFrame {
		if e.ID == core.ObjectID(sp["object_id"].(string)) {
			foundDelta = true
		}
	}
	if !foundDelta {
		t.Errorf("spawned plant %v not present in pendingFloraFrame %+v", sp["object_id"], fx.world.pendingFloraFrame)
	}
}

func TestPlantDiedEventPayloadShape(t *testing.T) {
	fx := newFixtureSeeded(t, 2081)
	cfg := testEnvConfig()

	plant := flora.Plant{ID: "weed_2", Species: "weed", Pos: core.Vec2{X: 5, Y: 5}, Length: 1, Width: 1}
	flState := flora.New([]flora.Plant{plant})
	rule := baseWeedRule(t)
	rule.Suitability = testNumProgram(t, "0") // below DeathThreshold every step
	rule.DeathThreshold = 0.5
	rule.DeathHysteresis = 1 // one sub-threshold step ⇒ death
	flRules := flora.NewRules(map[flora.SpeciesID]flora.SpeciesRule{"weed": rule})
	fx.world.PlaceObject(plant.ID, "weed", plant.Pos, nil)
	fx.world.InstallEnv(cfg, nil, nil, nil, flState, flRules, nil, nil)

	fx.world.Tick()

	var died *core.Event
	for i := range fx.emit.events {
		if fx.emit.events[i].Type == "PlantDied" {
			died = &fx.emit.events[i]
		}
	}
	if died == nil {
		t.Fatalf("no PlantDied event; flora %+v", fx.world.floraState.Plants())
	}
	dp := died.Payload.(map[string]any)
	for _, key := range []string{"object_id", "species", "pos"} {
		if _, ok := dp[key]; !ok {
			t.Errorf("PlantDied payload missing %q: %+v", key, dp)
		}
	}
	if dp["object_id"] != "weed_2" || dp["species"] != "weed" {
		t.Errorf("PlantDied payload = %+v, want object_id=weed_2 species=weed", dp)
	}
}

// ── AnimalBorn / AnimalDied payload shapes ──────────────────────────────────

func TestAnimalBornEventPayloadShape(t *testing.T) {
	fx := newFixtureSeeded(t, 209)
	cfg := testEnvConfig()
	cfg.ScentCellSize = 5
	cfg.ScentSpread = 1
	cfg.FaunaDT = 1
	fx.world.InstallFauna(cfg, testFaunaRules(t), testScentEmitters(), nil, []fauna.Animal{
		testAnimal("an:deer1", "deer", core.Vec2{X: 10, Y: 10}),
	})
	tpl := testAnimal("", "deer", core.Vec2{})
	tpl.Vital = 1
	fx.world.InstallRespawn(
		map[core.Tag]fauna.Animal{"deer": tpl},
		map[core.Tag]int{"deer": 3},
		map[core.Tag]core.Vec2{"deer": {X: 10, Y: 10}},
		1, // cadence: every tick
	)

	fx.world.Tick()

	var born *core.Event
	for i := range fx.emit.events {
		if fx.emit.events[i].Type == "AnimalBorn" {
			born = &fx.emit.events[i]
		}
	}
	if born == nil {
		t.Fatalf("no AnimalBorn event; animals=%+v", fx.world.Animals())
	}
	p := born.Payload.(map[string]any)
	for _, key := range []string{"object_id", "species", "pos"} {
		if _, ok := p[key]; !ok {
			t.Errorf("AnimalBorn payload missing %q: %+v", key, p)
		}
	}
	if _, ok := p["id"]; ok {
		t.Errorf("AnimalBorn payload still carries legacy 'id' key: %+v", p)
	}
	if p["species"] != "deer" {
		t.Errorf("AnimalBorn species = %v, want deer", p["species"])
	}
}

func TestAnimalDiedEventPayloadShape(t *testing.T) {
	fx := newFixtureSeeded(t, 210)
	cfg := testEnvConfig()
	cfg.ScentCellSize = 5
	cfg.ScentSpread = 2
	cfg.FaunaDT = 1
	fx.world.InstallFauna(cfg, testFaunaRules(t), testScentEmitters(), nil, []fauna.Animal{
		testAnimal("an:1", "deer", core.Vec2{}),
	})

	a := fx.world.animals["an:1"]
	a.Vital = 0
	fx.world.applyAnimalIntent(fauna.Intent{Animal: "an:1", Action: "Forage", NextPos: a.Pos, Drives: a.Drives})

	var died *core.Event
	for i := range fx.emit.events {
		if fx.emit.events[i].Type == "AnimalDied" {
			died = &fx.emit.events[i]
		}
	}
	if died == nil {
		t.Fatalf("no AnimalDied event")
	}
	p := died.Payload.(map[string]any)
	if p["object_id"] != "an:1" || p["species"] != "deer" || p["cause"] != "predation" {
		t.Errorf("AnimalDied payload = %+v, want object_id=an:1 species=deer cause=predation", p)
	}
	if _, ok := p["id"]; ok {
		t.Errorf("AnimalDied payload still carries legacy 'id' key: %+v", p)
	}
}
