package worldgen

import (
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/platform/config"
)

// TestFaunaBalanceObservation runs the starter fixture over a long horizon and REPORTS the observable
// predator-prey balance signals (M3 cover-hiding + M4-a river refuge). It is an OBSERVATION harness: run
// `go test ./tools/worldgen -run FaunaBalance -v` to read the numbers. It also guards the healthy band
// (some predation happens AND prey does not go extinct) so it doubles as a regression tripwire.
//
// Hunt-attempts are not instrumented, so predation "success rate" is proxied by deaths/1000t; hiding
// activity is measured directly from Animal.HiddenUntil (M3), which is exactly what we tuned.
func TestFaunaBalanceObservation(t *testing.T) {
	contentDir := findExisting(t, "../../../content", "../content", "content")
	fixturePath := findExisting(t, "testdata/starter_village.fixture.yaml", "backend/tools/worldgen/testdata/starter_village.fixture.yaml")
	schemaPath := filepath.Join(contentDir, "schema", "fixture.schema.json")

	cfg, err := config.Load(contentDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fx, err := ParseFile(fixturePath, schemaPath)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	rec := &recordingEmitter{}
	w, err := Load(fx, cfg, WithEmitter(rec))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	isPrey := map[fauna.SpeciesID]bool{"deer": true, "rabbit": true, "goat": true}
	const ticks = 3000

	prevHidden := map[core.ObjectID]bool{}
	hideEpisodes := map[fauna.SpeciesID]int{}
	hiddenTickSum := 0
	hiddenPeak := 0
	minPrey := 1 << 30

	for range ticks {
		w.Tick()
		now := w.CurrentTick()
		hiddenNow, preyNow := 0, 0
		for _, a := range w.Animals() {
			if isPrey[a.Species] {
				preyNow++
			}
			hidden := a.HiddenUntil > 0 && a.HiddenUntil >= now
			if hidden {
				hiddenNow++
				if !prevHidden[a.ID] {
					hideEpisodes[a.Species]++
				}
			}
			prevHidden[a.ID] = hidden
		}
		hiddenTickSum += hiddenNow
		if hiddenNow > hiddenPeak {
			hiddenPeak = hiddenNow
		}
		if preyNow < minPrey {
			minPrey = preyNow
		}
	}

	deaths := map[string]int{}
	totalDeaths := 0
	for _, e := range rec.events {
		if e.Type != "AnimalDied" {
			continue
		}
		sp := ""
		if payload, ok := e.Payload.(map[string]any); ok {
			sp, _ = payload["species"].(string)
		}
		deaths[sp]++
		totalDeaths++
	}
	finalPrey, finalPred := 0, 0
	for _, a := range w.Animals() {
		switch {
		case isPrey[a.Species]:
			finalPrey++
		case a.Species != "fish":
			finalPred++
		}
	}

	t.Logf("── fauna balance over %d ticks (seed=%d) ──", ticks, fx.Seed)
	t.Logf("predation deaths: %s  (total %d, ~%.2f/1000t)", fmtCounts(deaths), totalDeaths, float64(totalDeaths)*1000/ticks)
	t.Logf("hide episodes:    %s", fmtSpeciesCounts(hideEpisodes))
	t.Logf("hidden prey:      avg=%.2f peak=%d", float64(hiddenTickSum)/ticks, hiddenPeak)
	t.Logf("prey population:  min=%d final=%d ; predators final=%d", minPrey, finalPrey, finalPred)

	if totalDeaths == 0 {
		t.Errorf("no predation in %d ticks — predators cannot catch prey (too easy to escape)", ticks)
	}
	if minPrey == 0 {
		t.Errorf("prey went extinct — predation too strong or respawn too slow")
	}
}

func fmtCounts(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := ""
	for _, k := range keys {
		out += fmt.Sprintf("%s=%d ", k, m[k])
	}
	if out == "" {
		return "(none)"
	}
	return out
}

func fmtSpeciesCounts(m map[fauna.SpeciesID]int) string {
	conv := make(map[string]int, len(m))
	for k, v := range m {
		conv[string(k)] = v
	}
	return fmtCounts(conv)
}
