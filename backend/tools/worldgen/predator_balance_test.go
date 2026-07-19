package worldgen

import (
	"path/filepath"
	"testing"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/platform/config"
)

// TestPredatorBalanceObservation is the tuning bed for cluster 10's "~15% hunt success" target
// (docs/plans/fauna.md 클러스터 10, 균형(공통)). Run it to READ the numbers:
//
//	go test ./tools/worldgen -run PredatorBalance -v -count=1
//
// -count=1 matters: `go test` does NOT invalidate its cache when only content YAML changes, so a
// re-measurement after a balance edit will silently replay the previous run's output without it.
//
// What it measures that the older starter-village harness could not:
//   - HUNT SUCCESS: engagement attempts (a predator entering Attack on a fresh target) vs kills,
//     which is the actual quantity cluster 10 named a target for. The old harness proxied it by
//     deaths/1000t and said so.
//   - WHETHER THE MECHANISMS FIRE: M3 cover-hiding episodes, and how much of prey life is spent
//     hidden. If these are ~0 the balance numbers say nothing about ambush/cover — they are just
//     measuring a footrace, which is what the meadow turned out to be (0.17% hidden).
//   - WHETHER PREDATORS FEED THEMSELVES: the fixture has NO respawn, so predator extinction is a
//     real outcome and time-to-extinction is a tuning signal, not a broken run.
//
// It asserts only the loosest sanity band, because its job is to report, not to freeze today's
// balance: some predation must occur, and prey must not be wiped out.
func TestPredatorBalanceObservation(t *testing.T) {
	contentDir := findExisting(t, "../../../content", "../content", "content")
	fixturePath := findExisting(t,
		"testdata/predator_balance.fixture.yaml",
		"backend/tools/worldgen/testdata/predator_balance.fixture.yaml")
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
	isPredator := map[fauna.SpeciesID]bool{"wolf": true, "bear": true}
	const ticks = 6000

	// Engagement attempts: count each tick a predator newly locks onto a target it was not already
	// engaged with. That is the closest thing to "a hunt was attempted" the engine exposes.
	prevEngaged := map[core.ObjectID]core.ObjectID{}
	prevHidden := map[core.ObjectID]bool{}
	attempts := map[fauna.SpeciesID]int{}
	hideEpisodes := map[fauna.SpeciesID]int{}
	hiddenTickSum, preyTickSum := 0, 0
	predatorExtinctAt := -1
	minPrey := 1 << 30

	for i := 1; i <= ticks; i++ {
		w.Tick()
		now := w.CurrentTick()
		preyNow, predNow, hiddenNow := 0, 0, 0
		for _, a := range w.Animals() {
			switch {
			case isPrey[a.Species]:
				preyNow++
				hidden := a.HiddenUntil > 0 && a.HiddenUntil >= now
				if hidden {
					hiddenNow++
					if !prevHidden[a.ID] {
						hideEpisodes[a.Species]++
					}
				}
				prevHidden[a.ID] = hidden
			case isPredator[a.Species]:
				predNow++
				if a.EngagedWith != "" && prevEngaged[a.ID] != a.EngagedWith {
					attempts[a.Species]++
				}
				prevEngaged[a.ID] = a.EngagedWith
			}
		}
		hiddenTickSum += hiddenNow
		preyTickSum += preyNow
		if preyNow < minPrey {
			minPrey = preyNow
		}
		if predNow == 0 && predatorExtinctAt < 0 {
			predatorExtinctAt = i
		}
	}

	kills, starved := map[string]int{}, map[string]int{}
	for _, e := range rec.events {
		if e.Type != "AnimalDied" {
			continue
		}
		payload, ok := e.Payload.(map[string]any)
		if !ok {
			continue
		}
		sp, _ := payload["species"].(string)
		cause, _ := payload["cause"].(string)
		if cause == "starvation" {
			starved[sp]++
		} else {
			kills[sp]++
		}
	}

	totalAttempts, totalKills := 0, 0
	for _, n := range attempts {
		totalAttempts += n
	}
	for sp, n := range kills {
		if isPrey[fauna.SpeciesID(sp)] {
			totalKills += n
		}
	}
	finalPrey, finalPred := 0, 0
	for _, a := range w.Animals() {
		switch {
		case isPrey[a.Species]:
			finalPrey++
		case isPredator[a.Species]:
			finalPred++
		}
	}
	successRate := 0.0
	if totalAttempts > 0 {
		successRate = 100 * float64(totalKills) / float64(totalAttempts)
	}

	t.Logf("── predator balance over %d ticks (seed=%d, NO respawn) ──", ticks, fx.Seed)
	t.Logf("hunt attempts:   %s (total %d)", fmtSpeciesCounts(attempts), totalAttempts)
	t.Logf("prey killed:     %s (total %d)", fmtCounts(kills), totalKills)
	// Hunt success is an OBSERVATION, not a target to hit. It is a function of the conditions —
	// prey density above all, plus prey condition, cover and terrain — so the same content reads
	// very differently across beds (this open world vs the forced-encounter arena vs the dilute
	// live meadow, where predators starve out entirely). Cluster 10's "~15%" is a reference point
	// for what a healthy chase economy looks like, not a number to drive a knob to: forcing it on
	// one fixture would just encode that fixture's stocking into the balance constants.
	t.Logf("hunt success:    %.1f%%  (an outcome of THIS bed's stocking — cluster 10's ~15%% is a"+
		" reference, not a target; more prey ⇒ more successful hunts)", successRate)
	t.Logf("starvation:      %s", fmtCounts(starved))
	t.Logf("cover hiding:    episodes %s ; prey-life spent hidden %.2f%%",
		fmtSpeciesCounts(hideEpisodes), 100*float64(hiddenTickSum)/float64(max(preyTickSum, 1)))
	if predatorExtinctAt >= 0 {
		t.Logf("PREDATORS DIED OUT at tick %d — they cannot feed themselves at this balance", predatorExtinctAt)
	}
	t.Logf("populations:     prey min=%d final=%d ; predators final=%d", minPrey, finalPrey, finalPred)

	// What this bed asserts is COEXISTENCE, not a rate. A predator-prey balance is healthy when
	// both sides persist without a thermostat propping them up (this fixture has no respawn) —
	// the rate that produces it is whatever the conditions produce.
	if totalKills == 0 {
		t.Errorf("no prey killed in %d ticks — predators cannot convert an encounter into a meal", ticks)
	}
	if minPrey == 0 {
		t.Errorf("prey went extinct — predation far too strong for this stocking")
	}
	if finalPred == 0 {
		t.Errorf("predators died out — they cannot feed themselves at this balance (no respawn here)")
	}
}
