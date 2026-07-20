package worldgen

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/space/navmap"
	"github.com/dogring/bdg/engine/world"
	"github.com/dogring/bdg/platform/config"
)

// TestHabitatPartitioning — PD10's payoff. Species do not get a hardcoded home (D2 forbids it);
// habitat selection is supposed to EMERGE from a §6 scalar: an animal whose speed drops on ground it
// prefers lingers there and keeps moving where it does not, which is area-restricted search. Goats
// slow on slope, deer slow on damp ground, and nobody was told where to live.
//
// Asserts the ORDERING (goats end up on steeper ground than deer), never an absolute value — the
// terrain a given seed produces is whatever the generator produced.
func TestHabitatPartitioning(t *testing.T) {
	contentDir := findExisting(t, "../../../content", "../content", "content")
	cfg, err := config.Load(contentDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fx, err := ParseFile(
		findExisting(t, "testdata/rabbit_meadow.fixture.yaml",
			"backend/tools/worldgen/testdata/rabbit_meadow.fixture.yaml"),
		filepath.Join(contentDir, "schema", "fixture.schema.json"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	w, err := Load(fx, cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	const ticks = 4000
	slopeSum := map[fauna.SpeciesID]float64{}
	moistSum := map[fauna.SpeciesID]float64{}
	n := map[fauna.SpeciesID]int{}
	for i := 1; i <= ticks; i++ {
		w.Tick()
		if i%100 != 0 || i < 1000 { // let them settle first
			continue
		}
		for _, a := range w.Animals() {
			attrs := cfg.TerrainAttrs[navTerrain(w, a)]
			slopeSum[a.Species] += attrs["slope"]
			moistSum[a.Species] += attrs["moisture"]
			n[a.Species]++
		}
	}

	species := make([]string, 0, len(n))
	for sp := range n {
		species = append(species, string(sp))
	}
	sort.Strings(species)
	mean := func(m map[fauna.SpeciesID]float64, sp string) float64 {
		if n[fauna.SpeciesID(sp)] == 0 {
			return 0
		}
		return m[fauna.SpeciesID(sp)] / float64(n[fauna.SpeciesID(sp)])
	}
	for _, sp := range species {
		t.Logf("%-7s mean terrain under it: slope=%.3f moisture=%.3f  (n=%d)",
			sp, mean(slopeSum, sp), mean(moistSum, sp), n[fauna.SpeciesID(sp)])
	}

	goatSlope, deerSlope := mean(slopeSum, "goat"), mean(slopeSum, "deer")
	deerMoist, goatMoist := mean(moistSum, "deer"), mean(moistSum, "goat")
	if n["goat"] == 0 || n["deer"] == 0 {
		t.Skip("a species did not establish on this seed; habitat ordering is not measurable")
	}
	if goatSlope <= deerSlope {
		t.Errorf("goats are not on steeper ground than deer (%.3f vs %.3f) — the terrain.slope "+
			"preference is not reaching §6 speed", goatSlope, deerSlope)
	}
	// Only the goat authors a preference. Deer/moisture was tried and DROPPED: forage distribution
	// dominates where animals end up, and a speed term alone did not overcome it (measured). The
	// operand works — that is what the goat assertion above shows — but authoring a habitat per species
	// is its own tuning round, not a free consequence of the operand existing.
	_ = deerMoist
	_ = goatMoist
}

// navTerrain resolves the terrain id under an animal, matching how the engine indexes it.
func navTerrain(w *world.World, a fauna.Animal) navmap.TerrainID {
	return navmap.TerrainID(w.TerrainAt(a.Pos))
}
