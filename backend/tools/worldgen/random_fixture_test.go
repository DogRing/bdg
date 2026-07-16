package worldgen

import (
	"math"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/space/navmap"
	"github.com/dogring/bdg/platform/config"
)

// genTestCfg is a 500×500 world @ CellSize 12 — the rabbit_meadow geometry (29×26 hexes).
func genTestCfg() navmap.Config {
	return navmap.Config{MinX: 0, MinY: 0, MaxX: 500, MaxY: 500, CellSize: 12}
}

var genTerrainIDs = map[core.Tag]bool{
	"soil": true, "sand": true, "river": true, "lake": true,
	"sea": true, "mountain": true, "bare_rock": true,
}

// ── GenerateTerrain (SPEC AC: seeded-deterministic WG1-a terrain stages) ─────────

func TestGenerateTerrainDeterministicAndValid(t *testing.T) {
	navCfg := genTestCfg()
	cols, rows := navmap.OffsetDimsOf(navCfg)

	c1, e1, m1 := GenerateTerrain(cols, rows, navCfg, rng.New(42))
	c2, e2, m2 := GenerateTerrain(cols, rows, navCfg, rng.New(42))
	if !reflect.DeepEqual(c1, c2) || !reflect.DeepEqual(e1, e2) || !reflect.DeepEqual(m1, m2) {
		t.Fatalf("same seed produced different terrain/elevation/moisture")
	}
	c3, _, _ := GenerateTerrain(cols, rows, navCfg, rng.New(43))
	if reflect.DeepEqual(c1, c3) {
		t.Fatalf("different seeds produced identical terrain (suspicious)")
	}

	if len(c1) != cols*rows || len(e1) != cols*rows || len(m1) != cols*rows {
		t.Fatalf("output lengths %d/%d/%d, want cols*rows=%d", len(c1), len(e1), len(m1), cols*rows)
	}
	for i, id := range c1 {
		if !genTerrainIDs[id] {
			t.Fatalf("cell %d has id %q outside the generator subset", i, id)
		}
	}
	for i, e := range e1 {
		if e < 0 || e > 1 {
			t.Fatalf("elevation[%d] = %v outside [0,1]", i, e)
		}
	}
	for i, m := range m1 {
		if m < 0 || m > 1 {
			t.Fatalf("moisture[%d] = %v outside [0,1]", i, m)
		}
	}
}

func TestGenerateTerrainShape(t *testing.T) {
	navCfg := genTestCfg()
	cols, rows := navmap.OffsetDimsOf(navCfg)

	for seed := int64(1); seed <= 5; seed++ {
		cells, elev, moist := GenerateTerrain(cols, rows, navCfg, rng.New(seed))

		counts := map[core.Tag]int{}
		for _, id := range cells {
			counts[id]++
		}
		water := counts["sea"] + counts["lake"] + counts["river"]
		if water == 0 {
			t.Errorf("seed %d: no water cells at all", seed)
		}
		if frac := float64(counts["soil"]) / float64(len(cells)); frac < 0.4 {
			t.Errorf("seed %d: soil fraction %.2f < 0.4 (pos-less placement needs soil): %v", seed, frac, counts)
		}

		// Relief sanity: sea sits below mountains.
		var seaSum, mtSum float64
		var seaN, mtN int
		// Moisture sanity: beside water wetter than the driest cell.
		minMoist, maxWaterAdjMoist := 1.0, 0.0
		for i, id := range cells {
			switch id {
			case "sea":
				seaSum += elev[i]
				seaN++
			case "mountain", "bare_rock":
				mtSum += elev[i]
				mtN++
			}
			if moist[i] < minMoist {
				minMoist = moist[i]
			}
			if hasWaterNeighbor(waterMask(cells), cols, rows, i%cols, i/cols) && moist[i] > maxWaterAdjMoist {
				maxWaterAdjMoist = moist[i]
			}
		}
		if seaN > 0 && mtN > 0 && seaSum/float64(seaN) >= mtSum/float64(mtN) {
			t.Errorf("seed %d: mean sea elevation %.3f >= mean mountain elevation %.3f", seed, seaSum/float64(seaN), mtSum/float64(mtN))
		}
		if maxWaterAdjMoist <= minMoist {
			t.Errorf("seed %d: water-adjacent moisture %.3f not above driest cell %.3f", seed, maxWaterAdjMoist, minMoist)
		}
	}
}

func waterMask(cells []core.Tag) []bool {
	w := make([]bool, len(cells))
	for i, id := range cells {
		w[i] = id == "sea" || id == "lake" || id == "river"
	}
	return w
}

// ── minimal_meadow: random terrain materialization + pos-less placement + respawn
//    override + moisture/elevation couplings over the real content. Uses the minimal
//    (4 grass + 1 rabbit, pos-less-on-soil) fixture because these assertions need a
//    known handful of entities; rabbit_meadow itself is now the full density ecosystem.
//    ──────────────

func TestMinimalMeadowRandomFixture(t *testing.T) {
	contentDir := findExisting(t, "../../../content", "../content", "content")
	fixturePath := findExisting(t, "testdata/minimal_meadow.fixture.yaml", "backend/tools/worldgen/testdata/minimal_meadow.fixture.yaml")
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
	if !fx.Terrain.Random || len(fx.Terrain.Cells) != 0 || len(fx.Terrain.Elevation) != 0 {
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

	// Terrain + relief materialized over the fixture bounds (500×500): the render view
	// carries the elevation array (3D height wire), and the far corner is sampleable.
	rv := w.RenderView()
	if rv.Terrain == nil {
		t.Fatalf("no terrain render view")
	}
	if len(rv.Terrain.Elevation) != rv.Terrain.Cols*rv.Terrain.Rows {
		t.Fatalf("render elevation len %d, want cols*rows=%d", len(rv.Terrain.Elevation), rv.Terrain.Cols*rv.Terrain.Rows)
	}
	if _, ok := w.ClimateCellAt(core.Vec2{X: 499, Y: 499}); !ok {
		t.Fatalf("climate/terrain not installed over the fixture bounds")
	}

	// Water-proximity moisture seeding (WG1-a stage 4): the climate field is non-uniform
	// at t=0 (a uniform seed would make every sampled cell identical).
	minM, maxM := 1.0, 0.0
	for x := 10.0; x < 500; x += 60 {
		for y := 10.0; y < 500; y += 60 {
			if cell, ok := w.ClimateCellAt(core.Vec2{X: x, Y: y}); ok {
				if cell.Moisture < minM {
					minM = cell.Moisture
				}
				if cell.Moisture > maxM {
					maxM = cell.Moisture
				}
			}
		}
	}
	if maxM <= minM {
		t.Errorf("climate initial moisture is uniform (%.3f) — InitMoistureAt coupling not applied", maxM)
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
	headings := []float64{}
	for _, a := range w.Animals() {
		if a.Species == "rabbit" {
			rabbits++
			headings = append(headings, a.Heading)
		}
	}
	if rabbits != 10 {
		t.Fatalf("rabbits after one respawn cadence = %d, want 10 (fixture override)", rabbits)
	}

	// Anti-east-march: initial + respawned rabbits must face random directions, not
	// all share the zero (due-east) heading that funnelled the whole cohort into the
	// east wall. Independent uniform draws ⇒ distinct values spanning a wide arc.
	minH, maxH := math.Inf(1), math.Inf(-1)
	distinct := map[float64]bool{}
	for _, h := range headings {
		distinct[h] = true
		minH, maxH = math.Min(minH, h), math.Max(maxH, h)
	}
	if len(distinct) < 8 {
		t.Fatalf("rabbit headings not varied (%d distinct of 10): %v", len(distinct), headings)
	}
	if maxH-minH < 1.0 {
		t.Fatalf("rabbit headings span only %.2f rad — clustered (east-march regression): %v", maxH-minH, headings)
	}
}

func TestMinimalMeadowSeedDeterminism(t *testing.T) {
	contentDir := findExisting(t, "../../../content", "../content", "content")
	fixturePath := findExisting(t, "testdata/minimal_meadow.fixture.yaml", "backend/tools/worldgen/testdata/minimal_meadow.fixture.yaml")
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
		m, moist, err := materialize(f, navCfg, cfg)
		if err != nil {
			t.Fatalf("materialize(seed %d): %v", seed, err)
		}
		wantCols, wantRows := navmap.OffsetDimsOf(navCfg)
		if m.Terrain.Cols != wantCols || m.Terrain.Rows != wantRows {
			t.Fatalf("materialized dims %dx%d, want %dx%d", m.Terrain.Cols, m.Terrain.Rows, wantCols, wantRows)
		}
		if len(m.Terrain.Elevation) != wantCols*wantRows {
			t.Fatalf("materialized elevation len %d, want %d", len(m.Terrain.Elevation), wantCols*wantRows)
		}
		if len(moist) != wantCols*wantRows {
			t.Fatalf("materialized moisture len %d, want %d", len(moist), wantCols*wantRows)
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

// A pos-less spawn must get a seeded, varied, in-range heading — not the zero
// value. Leaving Heading at 0 pointed every animal due-east so the dormant
// ballistic steer marched them all right into the east wall on a fresh world.
func TestMaterializeRandomizesAnimalHeading(t *testing.T) {
	navCfg := genTestCfg()
	mk := func(seed int64) []AnimalPlacement {
		fx := Fixture{
			SchemaVersion: 1,
			Seed:          seed,
			Terrain:       &TerrainLayout{Random: true},
			Animals: []AnimalPlacement{
				{ID: "a1", Species: "rabbit"}, {ID: "a2", Species: "rabbit"},
				{ID: "a3", Species: "rabbit"}, {ID: "a4", Species: "rabbit"},
				{ID: "a5", Species: "rabbit"}, {ID: "a6", Species: "rabbit"},
			},
		}
		m, _, err := materialize(fx, navCfg, nil)
		if err != nil {
			t.Fatalf("materialize(seed %d): %v", seed, err)
		}
		return m.Animals
	}

	a := mk(42)
	allZero := true
	distinct := map[float64]bool{}
	for _, ap := range a {
		if ap.Heading != 0 {
			allZero = false
		}
		if ap.Heading < 0 || ap.Heading >= 2*math.Pi {
			t.Fatalf("animal %s heading %v outside [0,2π)", ap.ID, ap.Heading)
		}
		distinct[ap.Heading] = true
	}
	if allZero {
		t.Fatalf("every spawned heading is 0 — not randomized (the east-march bug)")
	}
	if len(distinct) < 2 {
		t.Fatalf("spawned headings not varied: %+v", a)
	}

	// Determinism: same seed ⇒ same headings (D12).
	b := mk(42)
	for i := range a {
		if a[i].Heading != b[i].Heading {
			t.Fatalf("heading non-deterministic for %s: %v vs %v", a[i].ID, a[i].Heading, b[i].Heading)
		}
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
