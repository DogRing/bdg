package worldgen

import (
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/platform/config"
)

// TestAgingHappensOnTheLiveWorld is the end-to-end tripwire for §7 aging (PD11) on the shipping meadow.
//
// The unit regressions (engine/fauna: senescence_test.go) pin the operand and the bleed. This one pins
// the thing they cannot see: that a population which now HAS a lifespan actually turns over — animals
// grow old, die of old age, and are replaced by births rather than by the whole founding cohort simply
// persisting forever. Before PD11 every animal in this world was biologically immortal: the only death
// causes were predation and starvation, so an animal that avoided both never died.
//
// It deliberately asserts STRUCTURE, not rates (the standing rule for this subsystem — a rate is an
// outcome of the bed's stocking, never a target). Three things must be true at once:
//   - old-age deaths happen at all,
//   - the population survives them (aging is not a die-off), and
//   - the age distribution is SPREAD, not a single cohort marching to the grave together.
func TestAgingHappensOnTheLiveWorld(t *testing.T) {
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
	rec := &recordingEmitter{}
	w, err := Load(fx, cfg, WithEmitter(rec))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// PD11-iv, asserted at t=0 where it is unambiguous: the FOUNDING population must already have an age
	// structure. Everything worldgen placed used to start at Age 0, and once lifespans existed that one
	// cohort died together — measured on this very world, rabbits collapsed at t≈2400, goats at t≈5000,
	// deer at t≈6000, each exactly at its authored lifespan, and the predators starved after them.
	// Checking the end-of-run spread does NOT catch this (the cohort run still showed a 5401-tick spread,
	// because births spread it out); checking the founders does.
	founders := map[fauna.SpeciesID][]float64{}
	for _, a := range w.Animals() {
		founders[a.Species] = append(founders[a.Species], a.Age)
	}
	for _, sp := range sortedSpecies(founders) {
		ages := founders[sp]
		if len(ages) < 4 {
			continue // too few placed to say anything about a distribution
		}
		lo, hi := ages[0], ages[0]
		for _, v := range ages {
			lo, hi = min(lo, v), max(hi, v)
		}
		lifespan := cfg.FaunaRules.Lifespan(sp)
		if lifespan <= 0 {
			continue // species does not age — nothing to spread
		}
		if hi-lo < 0.25*lifespan {
			t.Errorf("%s founders span only %.0f ticks of a %.0f-tick lifespan (%d placed) — the founding "+
				"population is one cohort and will age out together", sp, hi-lo, lifespan, len(ages))
		}
	}

	const ticks = 6000
	popSum, samples := 0, 0
	minPop := 1 << 30
	for i := 1; i <= ticks; i++ {
		w.Tick()
		n := len(w.Animals())
		popSum += n
		samples++
		if n < minPop {
			minPop = n
		}
		if i%1000 == 0 {
			// The trajectory, not just the mean: aging is a new mortality channel, and the question it
			// raises is whether the population finds a new level or keeps falling. A mean cannot tell
			// those apart — a world that halves steadily and one that settles look identical in it.
			perSpecies := map[fauna.SpeciesID]int{}
			for _, a := range w.Animals() {
				perSpecies[a.Species]++
			}
			keys := make([]string, 0, len(perSpecies))
			for sp := range perSpecies {
				keys = append(keys, string(sp))
			}
			sort.Strings(keys) // sorted: no map-order logic (D12)
			line := ""
			for _, k := range keys {
				line += fmt.Sprintf("%s=%d ", k, perSpecies[fauna.SpeciesID(k)])
			}
			t.Logf("t=%5d total=%3d | %s", i, n, line)
		}
	}

	byCause := map[string]int{}
	agedBySpecies := map[string]int{}
	for _, e := range rec.events {
		if e.Type != "AnimalDied" {
			continue
		}
		p, ok := e.Payload.(map[string]any)
		if !ok {
			continue
		}
		cause, _ := p["cause"].(string)
		byCause[cause]++
		if cause == "senescence" {
			sp, _ := p["species"].(string)
			agedBySpecies[sp]++
		}
	}

	// Age structure at the end of the run: how spread is the population across its species' lifespans?
	// A world where everything was placed at age 0 and nothing has bred yet is one cohort — every animal
	// the same age — and it will die all at once.
	var ages []float64
	byAgeSpecies := map[fauna.SpeciesID][]float64{}
	for _, a := range w.Animals() {
		ages = append(ages, a.Age)
		byAgeSpecies[a.Species] = append(byAgeSpecies[a.Species], a.Age)
	}
	sort.Float64s(ages)
	spread := 0.0
	if len(ages) > 1 {
		spread = ages[len(ages)-1] - ages[0]
	}

	causes := make([]string, 0, len(byCause))
	for c := range byCause {
		causes = append(causes, c)
	}
	sort.Strings(causes) // sorted: no map-order logic (D12), and stable log output
	for _, c := range causes {
		t.Logf("deaths by cause: %-11s %d", c, byCause[c])
	}
	species := make([]string, 0, len(agedBySpecies))
	for s := range agedBySpecies {
		species = append(species, s)
	}
	sort.Strings(species)
	for _, s := range species {
		t.Logf("old-age deaths: %-7s %d", s, agedBySpecies[s])
	}
	t.Logf("population over %d ticks: mean %.1f min %d final %d | end-of-run age spread %.0f ticks (youngest %.0f, oldest %.0f)",
		ticks, float64(popSum)/float64(max(samples, 1)), minPop, len(ages), spread,
		firstOr(ages, 0), lastOr(ages, 0))

	if byCause["senescence"] == 0 {
		t.Errorf("not one animal died of old age in %d ticks — either no species authors a lifespan, or "+
			"nothing lives long enough to reach it (before PD11 every animal here was immortal)", ticks)
	}
	// Aging must not be a die-off. The population is allowed to move — it is an ecosystem — but a world
	// that empties out is aging killing faster than breeding replaces, which is a balance failure, not a
	// feature. The floor is deliberately loose: this asserts survival, not a level.
	if minPop == 0 || len(ages) == 0 {
		t.Fatalf("the world emptied out (min population %d, final %d) — aging is outrunning reproduction",
			minPop, len(ages))
	}
	// The founding population is placed at Age 0, so without births the survivors would all share one
	// age and this spread would be ~0. A spread comparable to a lifespan means generations overlap.
	if spread < 500 {
		t.Errorf("end-of-run age spread is only %.0f ticks — the population is a single cohort that will "+
			"age and die together, not a breeding population with overlapping generations", spread)
	}
}

// sortedSpecies returns the map's keys in a fixed order — no map-iteration for logic (D12).
func sortedSpecies(m map[fauna.SpeciesID][]float64) []fauna.SpeciesID {
	out := make([]fauna.SpeciesID, 0, len(m))
	for sp := range m {
		out = append(out, sp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func firstOr(xs []float64, def float64) float64 {
	if len(xs) == 0 {
		return def
	}
	return xs[0]
}

func lastOr(xs []float64, def float64) float64 {
	if len(xs) == 0 {
		return def
	}
	return xs[len(xs)-1]
}
