package worldgen

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/space/navmap"
	"github.com/dogring/bdg/platform/config"
)

// ── SimpleTerrain (SPEC AC: seeded-deterministic dev terrain) ────────────────────

func TestSimpleTerrainDeterministicAndValid(t *testing.T) {
	const cols, rows = 18, 16
	a := SimpleTerrain(cols, rows, rng.New(42))
	b := SimpleTerrain(cols, rows, rng.New(42))
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("same seed produced different terrain")
	}
	c := SimpleTerrain(cols, rows, rng.New(43))
	if reflect.DeepEqual(a, c) {
		t.Fatalf("different seeds produced identical terrain (suspicious)")
	}

	valid := map[core.Tag]bool{"soil": true, "river": true, "sand": true, "mountain": true}
	for i, id := range a {
		if !valid[id] {
			t.Fatalf("cell %d has id %q outside the dev subset", i, id)
		}
	}
	for row := 0; row < rows; row++ {
		found := false
		for col := 0; col < cols; col++ {
			if a[row*cols+col] == "river" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("row %d has no river cell", row)
		}
	}
}

// ── rabbit_meadow: random terrain materialization + pos-less placement + respawn
//    override (SPEC AC ×3) over the real content ─────────────────────────────────

func TestRabbitMeadowRandomFixture(t *testing.T) {
	contentDir := findExisting(t, "../../../content", "../content", "content")
	fixturePath := findExisting(t, "testdata/rabbit_meadow.fixture.yaml", "backend/tools/worldgen/testdata/rabbit_meadow.fixture.yaml")
	schemaPath := filepath.Join(contentDir, "schema", "fixture.schema.json")

	cfg, err := config.Load(contentDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	contentRabbit := cfg.RespawnTargets["rabbit"]

	fx, err := ParseFile(fixturePath, schemaPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if fx.Terrain == nil || !fx.Terrain.Random {
		t.Fatalf("fixture terrain.random not parsed: %+v", fx.Terrain)
	}
	if got := fx.RespawnTargets["rabbit"]; got != 10 {
		t.Fatalf("fixture respawn_targets[rabbit] = %d, want 10", got)
	}

	w, err := Load(fx, cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// The caller's fixture and the shared config must be untouched (regen re-rolls).
	if !fx.Terrain.Random || len(fx.Terrain.Cells) != 0 {
		t.Fatalf("Load mutated the caller's fixture terrain: %+v", fx.Terrain)
	}
	for _, fp := range fx.Flora {
		if fp.Pos != nil {
			t.Fatalf("Load mutated the caller's flora placement %s", fp.ID)
		}
	}
	if cfg.RespawnTargets["rabbit"] != contentRabbit {
		t.Fatalf("Load mutated config.RespawnTargets: %d", cfg.RespawnTargets["rabbit"])
	}

	// Initial population: exactly 1 rabbit, nothing else.
	animals := w.Animals()
	if len(animals) != 1 || animals[0].Species != "rabbit" {
		t.Fatalf("initial animals = %+v, want exactly one rabbit", animals)
	}

	// Terrain materialized to the navmap hex offset dims: sampling any point works and
	// the loader accepted the dims cross-check (Load would have failed otherwise). The
	// world spans 300×300 (fixture bounds), so the far corner must be sampleable.
	if _, ok := w.ClimateCellAt(core.Vec2{X: 299, Y: 299}); !ok {
		t.Fatalf("climate/terrain not installed over the fixture bounds")
	}

	// Respawn override: the first cadence tick tops rabbit up to 10 (not the content
	// value). world.yaml respawn_cadence gates when; run just past one cadence.
	cadence := int(cfg.WorldEnv.RespawnCadence)
	if cadence <= 0 {
		t.Fatalf("content respawn_cadence not positive: %d", cadence)
	}
	for i := 0; i < cadence+1; i++ {
		w.Tick()
	}
	rabbits := 0
	for _, a := range w.Animals() {
		if a.Species == "rabbit" {
			rabbits++
		}
	}
	if rabbits != 10 {
		t.Fatalf("rabbits after one respawn cadence = %d, want 10 (fixture override)", rabbits)
	}
}

func TestRabbitMeadowSeedDeterminism(t *testing.T) {
	contentDir := findExisting(t, "../../../content", "../content", "content")
	fixturePath := findExisting(t, "testdata/rabbit_meadow.fixture.yaml", "backend/tools/worldgen/testdata/rabbit_meadow.fixture.yaml")
	schemaPath := filepath.Join(contentDir, "schema", "fixture.schema.json")

	cfg, err := config.Load(contentDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fx, err := ParseFile(fixturePath, schemaPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	terrainOf := func(seed int64) []core.Tag {
		f := fx
		f.Seed = seed
		navCfg := *cfg.NavCfg
		navCfg.MinX, navCfg.MinY = f.Bounds.Min[0], f.Bounds.Min[1]
		navCfg.MaxX, navCfg.MaxY = f.Bounds.Max[0], f.Bounds.Max[1]
		m, err := materialize(f, navCfg)
		if err != nil {
			t.Fatalf("materialize(seed %d): %v", seed, err)
		}
		wantCols, wantRows := navmap.OffsetDimsOf(navCfg)
		if m.Terrain.Cols != wantCols || m.Terrain.Rows != wantRows {
			t.Fatalf("materialized dims %dx%d, want %dx%d", m.Terrain.Cols, m.Terrain.Rows, wantCols, wantRows)
		}
		// Every pos-less placement landed on a soil hex inside bounds.
		sample := layoutSampler(*m.Terrain, navCfg)
		for _, fp := range m.Flora {
			if fp.Pos == nil {
				t.Fatalf("flora %s still pos-less after materialize", fp.ID)
			}
			if got := sample(fp.Pos.Core()); got != "soil" {
				t.Fatalf("flora %s placed on %q, want soil", fp.ID, got)
			}
		}
		for _, ap := range m.Animals {
			if ap.Pos == nil {
				t.Fatalf("animal %s still pos-less after materialize", ap.ID)
			}
			if got := sample(ap.Pos.Core()); got != "soil" {
				t.Fatalf("animal %s placed on %q, want soil", ap.ID, got)
			}
		}
		return m.Terrain.Cells
	}

	a := terrainOf(1)
	b := terrainOf(1)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("same fixture seed materialized different terrain")
	}
	c := terrainOf(2)
	if reflect.DeepEqual(a, c) {
		t.Fatalf("different fixture seeds materialized identical terrain (suspicious)")
	}
}

func TestPoslessPlacementWithoutTerrainFails(t *testing.T) {
	contentDir := findExisting(t, "../../../content", "../content", "content")
	cfg, err := config.Load(contentDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fx := Fixture{
		SchemaVersion: 1,
		Seed:          7,
		Flora:         []FloraPlacement{{ID: "grass_1", Species: "grass"}},
	}
	if _, err := Load(fx, cfg); err == nil {
		t.Fatalf("Load accepted a pos-less placement without a terrain layout")
	}
}

func TestRespawnTargetsUnknownSpeciesFails(t *testing.T) {
	contentDir := findExisting(t, "../../../content", "../content", "content")
	cfg, err := config.Load(contentDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fx := Fixture{
		SchemaVersion:  1,
		Seed:           7,
		RespawnTargets: map[core.Tag]int{"dragon": 3},
	}
	if _, err := Load(fx, cfg); err == nil {
		t.Fatalf("Load accepted an unknown respawn_targets species")
	}
}
