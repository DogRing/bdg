package worldgen

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/platform/config"
)

// TestVillageEcosystemLoads loads the LARGE 3000² by-type ecosystem fixture (scaling.md P1),
// reports the per-species placement census + terrain-gen / load / tick timing + heap, and
// asserts every by-type species establishes. Skipped in -short; run explicitly:
//
//	go test ./tools/worldgen -run VillageEcosystemLoads -v
//
// This is also the first scaling data point (P0 seed): a single 3000² sample of the numbers the
// parametric sweep will chart.
func TestVillageEcosystemLoads(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large 3000² ecosystem load in short mode")
	}
	contentDir := findExisting(t, "../../../content", "../content", "content")
	cfg, err := config.Load(contentDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	schemaPath := filepath.Join(contentDir, "schema", "fixture.schema.json")
	fx, err := ParseFile("testdata/village_ecosystem.fixture.yaml", schemaPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	// Placement census: materialize the same fx to read the by-species breakdown + gen time.
	_, navCfg, _ := fixtureConfigs(fx, cfg)
	genStart := time.Now()
	m, _, err := materialize(fx, navCfg, cfg)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	genDur := time.Since(genStart)

	floraBy := map[core.Tag]int{}
	for _, fp := range m.Flora {
		floraBy[fp.Species]++
	}
	animalBy := map[core.Tag]int{}
	for _, ap := range m.Animals {
		animalBy[ap.Species]++
	}
	t.Logf("terrain %dx%d cells | materialize %v", m.Terrain.Cols, m.Terrain.Rows, genDur)
	t.Logf("flora   placed (%d total): %v", len(m.Flora), floraBy)
	t.Logf("animals placed (%d total): %v", len(m.Animals), animalBy)

	for _, sp := range []core.Tag{"grass", "tall_grass", "wildflower", "berry_shrub", "dry_shrub", "oak"} {
		if floraBy[sp] == 0 {
			t.Errorf("flora species %s not placed (P1 requires all by-type species)", sp)
		}
	}
	for _, sp := range []core.Tag{"rabbit", "deer", "goat", "fish", "wolf", "bear"} {
		if animalBy[sp] == 0 {
			t.Errorf("fauna species %s not placed (P1 requires all by-type species)", sp)
		}
	}

	// Build + tick to prove the ecosystem runs at 3000² (SC1 whole-map premise).
	loadStart := time.Now()
	w, err := Load(fx, cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	loadDur := time.Since(loadStart)

	const ticks = 200
	tickStart := time.Now()
	for i := 0; i < ticks; i++ {
		w.Tick()
	}
	tickDur := time.Since(tickStart)

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	t.Logf("Load %v | %d ticks in %v (%.3f ms/tick) | heap %d MB, sys %d MB",
		loadDur, ticks, tickDur, float64(tickDur.Microseconds())/ticks/1000.0,
		ms.HeapAlloc/1024/1024, ms.Sys/1024/1024)
}
