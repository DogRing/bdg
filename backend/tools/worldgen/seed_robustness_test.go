package worldgen

import (
	"path/filepath"
	"testing"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/platform/config"
)

// TestPredatorBalanceAcrossSeeds is the anti-overfit guard for the predation constants.
//
// TestPredatorBalanceObservation runs ONE seed, and single-seed counts on this bed swing wide
// (predation kills ranged 1–26 across seeds at a fixed balance). Tuning a knob until that one seed
// looks good is therefore the easiest way to ship a balance that only works on that seed — which is
// exactly what this replays the bed on several seeds to prevent.
//
// It asserts the same thing the single-seed bed does — COEXISTENCE, never a rate — but demands it
// hold on every seed: predators feed themselves without a respawn thermostat, and prey are not wiped
// out. The per-seed counts are logged so a balance change can be read as a distribution rather than
// a number.
func TestPredatorBalanceAcrossSeeds(t *testing.T) {
	contentDir := findExisting(t, "../../../content", "../content", "content")
	cfg, err := config.Load(contentDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	base, err := ParseFile(
		findExisting(t, "testdata/predator_balance.fixture.yaml",
			"backend/tools/worldgen/testdata/predator_balance.fixture.yaml"),
		filepath.Join(contentDir, "schema", "fixture.schema.json"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	isPrey := map[fauna.SpeciesID]bool{"deer": true, "rabbit": true, "goat": true}
	isPred := map[fauna.SpeciesID]bool{"wolf": true, "bear": true}
	const ticks = 6000

	for _, seed := range []int64{7, 1234, 99991} {
		fx := base
		fx.Seed = seed
		rec := &recordingEmitter{}
		w, err := Load(fx, cfg, WithEmitter(rec))
		if err != nil {
			t.Fatalf("Load(seed %d): %v", seed, err)
		}
		minPrey := 1 << 30
		for range ticks {
			w.Tick()
			n := 0
			for _, a := range w.Animals() {
				if isPrey[a.Species] {
					n++
				}
			}
			if n < minPrey {
				minPrey = n
			}
		}
		kills, preyStarv, predStarv := 0, 0, 0
		for _, e := range rec.events {
			if e.Type != "AnimalDied" {
				continue
			}
			p, ok := e.Payload.(map[string]any)
			if !ok {
				continue
			}
			sp, _ := p["species"].(string)
			// Three causes exist since PD11 (§7 aging): predation, starvation, senescence. Match each
			// EXACTLY — "not starvation" would fold old-age deaths into the kill count and let this
			// guard pass on a world where nothing hunts.
			starved := p["cause"] == "starvation"
			switch {
			case starved && isPrey[fauna.SpeciesID(sp)]:
				preyStarv++
			case starved && isPred[fauna.SpeciesID(sp)]:
				predStarv++
			case p["cause"] == "predation" && isPrey[fauna.SpeciesID(sp)]:
				kills++
			}
		}
		prey, pred := 0, 0
		for _, a := range w.Animals() {
			switch {
			case isPrey[a.Species]:
				prey++
			case isPred[a.Species]:
				pred++
			}
		}
		t.Logf("seed %-6d predation=%-3d preyStarved=%-4d predStarved=%-3d | prey min=%-3d final=%-3d ; predators final=%d",
			seed, kills, preyStarv, predStarv, minPrey, prey, pred)

		if pred == 0 {
			t.Errorf("seed %d: predators died out — they cannot feed themselves at this balance", seed)
		}
		if minPrey == 0 {
			t.Errorf("seed %d: prey went extinct", seed)
		}
		if kills == 0 {
			t.Errorf("seed %d: no predation at all in %d ticks", seed, ticks)
		}
	}
}
