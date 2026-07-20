package worldgen

import (
	"math"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dogring/bdg/engine/env/flora"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/platform/config"
)

// densityTestFixture is a modest random world with by-species flora/fauna density (scaling.md
// SC4). Small enough to be fast; big enough that suitability/passability rejection is exercised.
func densityTestFixture(seed int64) Fixture {
	return Fixture{
		SchemaVersion: 1,
		Seed:          seed,
		Bounds:        &Bounds{Min: Vec2{0, 0}, Max: Vec2{400, 400}},
		Terrain:       &TerrainLayout{Random: true},
		FloraDensity: map[core.Tag]float64{
			"grass":       0.02,
			"tall_grass":  0.01,
			"berry_shrub": 0.005,
			"dry_shrub":   0.005,
		},
		AnimalDensity: map[core.Tag]float64{
			"rabbit": 0.002,
			"deer":   0.001,
			"fish":   0.002,
			"wolf":   0.0004,
		},
	}
}

// TestMaterializeDensityInvariants checks the SC4 placement guarantees: flora never lands where
// §6 carrying-capacity is 0 (sea/deep water), animals only land where the species may occupy the
// terrain (F18 allow-list — fish in water, land species off deep sea), and the abundant species
// actually get placed.
func TestMaterializeDensityInvariants(t *testing.T) {
	contentDir := findExisting(t, "../../../content", "../content", "content")
	cfg, err := config.Load(contentDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fx := densityTestFixture(7)
	_, navCfg, _ := fixtureConfigs(fx, cfg)

	m, moist, err := materialize(fx, navCfg, cfg)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	terrainAt := layoutSampler(*m.Terrain, navCfg)
	cols, rows := m.Terrain.Cols, m.Terrain.Rows
	moistureAt := densityMoistureSampler(moist, cols, rows, navCfg)

	// Flora: every placement must sit where the species' §6 carrying-capacity is > 0. This is
	// exactly the acceptance rule (accept ∝ K), so a placement on a K=0 site (sea/steep) would
	// be a bug. Also count per species to assert the abundant ones establish.
	floraBySpecies := map[core.Tag]int{}
	for _, fp := range m.Flora {
		floraBySpecies[fp.Species]++
		in := floraSiteAt(cfg, terrainAt, moistureAt, fp.Pos.Core())
		if k := cfg.FloraRules.CarryingCapacity(flora.SpeciesID(fp.Species), in); k <= 0 {
			t.Fatalf("flora %s placed at %v on terrain %q with carrying-capacity %.3f (≤0)",
				fp.ID, *fp.Pos, terrainAt(fp.Pos.Core()), k)
		}
	}
	if floraBySpecies["grass"] == 0 {
		t.Fatalf("no grass placed by density (expected the abundant ground cover)")
	}

	// Animals: every placement must satisfy the F18 allow-list (passable terrain ∧ ¬impassable).
	animalBySpecies := map[core.Tag]int{}
	for _, ap := range m.Animals {
		animalBySpecies[ap.Species]++
		terrain := terrainAt(ap.Pos.Core())
		if !animalCanOccupy(cfg, fauna.SpeciesID(ap.Species), terrain) {
			t.Fatalf("animal %s (%s) placed on terrain %q it cannot occupy", ap.ID, ap.Species, terrain)
		}
	}
	if animalBySpecies["rabbit"] == 0 {
		t.Fatalf("no rabbit placed by density (expected the abundant grazer)")
	}
	// Any fish that WAS placed must be on water — i.e., not on a fish-impassable land terrain.
	// (fish count may be low if the seed's world has little water; the invariant still holds.)
	for _, ap := range m.Animals {
		if ap.Species != "fish" {
			continue
		}
		terrain := terrainAt(ap.Pos.Core())
		if _, passable := cfg.FaunaRules.TerrainCost(fauna.SpeciesID("fish"), terrain); !passable {
			t.Fatalf("fish placed on land terrain %q (must be water-only)", terrain)
		}
	}
	t.Logf("placed flora=%v animals=%v", floraBySpecies, animalBySpecies)
}

// TestMaterializeDensityDeterministic guards D12: same (fixture, seed) ⇒ identical placement.
func TestMaterializeDensityDeterministic(t *testing.T) {
	contentDir := findExisting(t, "../../../content", "../content", "content")
	cfg, err := config.Load(contentDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fx := densityTestFixture(11)
	_, navCfg, _ := fixtureConfigs(fx, cfg)

	a, _, err := materialize(densityTestFixture(11), navCfg, cfg)
	if err != nil {
		t.Fatalf("materialize a: %v", err)
	}
	b, _, err := materialize(densityTestFixture(11), navCfg, cfg)
	if err != nil {
		t.Fatalf("materialize b: %v", err)
	}
	if !reflect.DeepEqual(a.Flora, b.Flora) {
		t.Fatalf("flora placement not deterministic: %d vs %d", len(a.Flora), len(b.Flora))
	}
	if !reflect.DeepEqual(a.Animals, b.Animals) {
		t.Fatalf("animal placement not deterministic: %d vs %d", len(a.Animals), len(b.Animals))
	}
	// A different seed must differ (guards a dead RNG).
	c, _, err := materialize(densityTestFixture(12), navCfg, cfg)
	if err != nil {
		t.Fatalf("materialize c: %v", err)
	}
	if reflect.DeepEqual(a.Flora, c.Flora) {
		t.Fatalf("different seeds produced identical flora placement (suspicious)")
	}
}

// TestDensityHeadingsVaried is the anti-east-march guard: every placed animal must get its OWN
// facing. Leaving Heading at its zero value points them all due-east, and the dormant ballistic
// steer then marches the entire cohort into the east wall on a fresh world.
//
// It lives here because density placement is how the live world is populated. It used to ride along
// in the minimal-meadow fixture test, which only saw variety because respawn topped that world up to
// ten rabbits and gave each a fresh heading — once respawn became a rescue floor (PD5) that world
// holds its single placed rabbit and can no longer demonstrate anything.
func TestDensityHeadingsVaried(t *testing.T) {
	contentDir := findExisting(t, "../../../content", "../content", "content")
	cfg, err := config.Load(contentDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fx, err := ParseFile(
		findExisting(t, "testdata/rabbit_meadow.fixture.yaml"),
		filepath.Join(contentDir, "schema", "fixture.schema.json"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	w, err := Load(fx, cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	placed := w.Animals()
	if len(placed) < 8 {
		t.Fatalf("too few animals placed to judge heading variety: %d", len(placed))
	}
	distinct := map[float64]bool{}
	minH, maxH := math.Inf(1), math.Inf(-1)
	for _, a := range placed {
		distinct[a.Heading] = true
		minH, maxH = math.Min(minH, a.Heading), math.Max(maxH, a.Heading)
	}
	if len(distinct) < len(placed) {
		t.Errorf("placed animals share headings (%d distinct of %d) — each needs its own draw",
			len(distinct), len(placed))
	}
	if maxH-minH < 1.0 {
		t.Errorf("headings span only %.2f rad — clustered (east-march regression)", maxH-minH)
	}
}
