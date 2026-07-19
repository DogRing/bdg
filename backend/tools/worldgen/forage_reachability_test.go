package worldgen

import (
	"path/filepath"
	"testing"

	"github.com/dogring/bdg/engine/world"
	"github.com/dogring/bdg/platform/config"
)

// TestForageReachability is the end-to-end tripwire for the herbivore feeding loop on the live meadow.
// The unit regressions (engine/world: TestGrazePassesOverExhaustedPlant, TestFloraScentTracksBiomass)
// pin the two mechanisms; this one pins the OUTCOME they exist for, because the defect they fixed was
// invisible at every level above the individual animal — populations looked fine, plants looked fine,
// and animals were picking a Graze action ~96% of ticks. What was wrong was WHICH plant they picked.
//
// The signal: for each hungry animal whose nearest food plant is exhausted, was there a plant WITH
// biomass sitting inside the same graze reach? That is food the animal could have eaten and did not.
// Before the fix it was ~82% (animals starving on top of food); after, ~5% (they eat what is in reach,
// then the whole neighbourhood is legitimately bare — which is grazing pressure, the PD2 intent).
//
// It also guards PD5b: fish must hold ABOVE the respawn floor, i.e. births — not top-ups — are what
// carries the school. Fish are the strictest probe of the feeding loop because they barely move, so
// they cannot walk away from a bad food choice the way a deer can.
func TestForageReachability(t *testing.T) {
	contentDir := findExisting(t, "../../../content", "../content", "content")
	fixturePath := findExisting(t,
		"testdata/rabbit_meadow.fixture.yaml",
		"backend/tools/worldgen/testdata/rabbit_meadow.fixture.yaml")
	cfg, err := config.Load(contentDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fx, err := ParseFile(fixturePath, filepath.Join(contentDir, "schema", "fixture.schema.json"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	w, err := Load(fx, cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	foodKind := map[string]bool{}
	for kind, tags := range cfg.ScentEmitters {
		for _, tg := range tags {
			if tg == "scent:food" {
				foodKind[string(kind)] = true
			}
		}
	}
	reach := cfg.WorldEnv.ScentCellSize

	const ticks = 3000
	exhaustedNearest, foodLeftInReach := 0, 0
	fishSamples, fishTotal := 0, 0

	for i := 1; i <= ticks; i++ {
		w.Tick()
		if i%250 != 0 || i < 1000 { // let the world settle before reading the steady state
			continue
		}
		var plants []world.FloraRenderView
		for _, p := range w.RenderView().Flora {
			if foodKind[p.Species] {
				plants = append(plants, p)
			}
		}
		fishNow := 0
		for _, a := range w.Animals() {
			if a.Species == "fish" {
				fishNow++
			}
			if _, hungry := a.Drives["hunger"]; !hungry {
				continue
			}
			nearest, nearestDist, fed := -1, reach, 0
			for j, p := range plants {
				d := a.Pos.Distance(p.Pos)
				if d > reach {
					continue
				}
				if d <= nearestDist {
					nearest, nearestDist = j, d
				}
				if p.Stage >= 1 {
					fed++
				}
			}
			if nearest < 0 || plants[nearest].Stage != 0 {
				continue
			}
			exhaustedNearest++
			if fed > 0 {
				foodLeftInReach++
			}
		}
		fishTotal += fishNow
		fishSamples++
	}

	wasted := 0.0
	if exhaustedNearest > 0 {
		wasted = 100 * float64(foodLeftInReach) / float64(exhaustedNearest)
	}
	meanFish := 0.0
	if fishSamples > 0 {
		meanFish = float64(fishTotal) / float64(fishSamples)
	}
	t.Logf("animals whose nearest food plant is exhausted: %d ; of those, edible food WAS in reach: %d (%.0f%%)",
		exhaustedNearest, foodLeftInReach, wasted)
	t.Logf("mean live fish: %.1f (respawn floor %d)", meanFish, 6)

	// 82% was the broken loop; ~5% is a working one. 40% is a wide band that only trips on a real
	// regression of the "an exhausted plant is not food" rule, not on ordinary balance drift.
	if wasted > 40 {
		t.Errorf("animals are ignoring food inside their own graze reach %.0f%% of the time — "+
			"the forage lookup is picking exhausted plants again", wasted)
	}
	// The fixture tops fish up TO 6, so anything above that is births carrying the school (PD5b).
	if meanFish <= 6 {
		t.Errorf("fish are being held up by the respawn floor (mean %.1f ≤ 6) — the aquatic feeding/"+
			"breeding loop has stopped producing offspring", meanFish)
	}
}
