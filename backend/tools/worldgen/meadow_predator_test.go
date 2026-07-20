package worldgen

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/platform/config"
)

// TestMeadowSustainsPredators guards the SHIPPING world, which is a different beast from the
// hand-authored predator bed: rabbit_meadow is 500×500 with procedurally placed animals, and for a
// long time it quietly contained no predation at all — zero kills in 6000 ticks, predators starving
// on a loop and being topped back up by respawn. Every fauna bed passed throughout, because they all
// run on small hand-stocked fixtures where prey are close by construction.
//
// The failure was arithmetic, not behaviour. A predator that cannot eat dies in roughly one game day
// (1440 ticks) and covers ~0.2 units/tick, so its whole lifetime search budget is a few hundred units
// — while on this map prey sat 100–200 units away, clumped, with a scent field that carries ~20u.
// Predators were within 30u of prey 2% of ticks and simply never met them. Raising predator smell
// radius did nothing (2.01% → 2.38%): the scent field does not extend that far either.
//
// So this test asserts the two things the small beds cannot: predators on the real map FIND prey, and
// predation actually happens there.
func TestMeadowSustainsPredators(t *testing.T) {
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
	isPrey := map[fauna.SpeciesID]bool{"deer": true, "rabbit": true, "goat": true}
	isPred := map[fauna.SpeciesID]bool{"wolf": true, "bear": true}
	predFloor := fx.RespawnTargets["wolf"] + fx.RespawnTargets["bear"]

	const ticks = 6000
	predSum, samples, within30, predObs := 0, 0, 0, 0
	for i := 1; i <= ticks; i++ {
		w.Tick()
		animals := w.Animals()
		var preyPos []core.Vec2
		pred := 0
		for _, a := range animals {
			if isPrey[a.Species] {
				preyPos = append(preyPos, a.Pos)
			} else if isPred[a.Species] {
				pred++
			}
		}
		for _, a := range animals {
			if !isPred[a.Species] {
				continue
			}
			best := math.Inf(1)
			for _, p := range preyPos {
				if d := a.Pos.Distance(p); d < best {
					best = d
				}
			}
			predObs++
			if best <= 30 {
				within30++
			}
		}
		predSum += pred
		samples++
	}
	kills, predStarv := 0, 0
	for _, e := range rec.events {
		if e.Type != "AnimalDied" {
			continue
		}
		p, ok := e.Payload.(map[string]any)
		if !ok {
			continue
		}
		sp, _ := p["species"].(string)
		switch {
		case p["cause"] == "starvation" && isPred[fauna.SpeciesID(sp)]:
			predStarv++
		case p["cause"] != "starvation" && isPrey[fauna.SpeciesID(sp)]:
			kills++
		}
	}
	meanPred := float64(predSum) / float64(max(samples, 1))
	proximity := 100 * float64(within30) / float64(max(predObs, 1))
	t.Logf("live meadow over %d ticks: mean predators=%.2f (respawn floor %d) | predation kills=%d "+
		"predator starvations=%d | predator within 30u of prey %.1f%% of ticks",
		ticks, meanPred, predFloor, kills, predStarv, proximity)

	// Not a rate target — the floor is "the mechanism happens at all on the real map".
	if kills == 0 {
		t.Errorf("no predation at all on the live map in %d ticks: predators exist but never eat", ticks)
	}
	// 2% was the broken world; ~40–75% is a stocked one. 15% is well clear of both.
	if proximity < 15 {
		t.Errorf("predators are within 30u of prey only %.1f%% of ticks — the map is too sparse for "+
			"them to find the herds within a lifetime's search budget", proximity)
	}
	// NOT asserted (yet): that births rather than respawn top-ups carry the predators. On this map
	// they do not — mean predator count sits AT the respawn target, and dropping the targets back to
	// their pre-stocking values takes the whole ecosystem to zero predation. Making the live world
	// self-sustaining is P_fa4c-3's job (PD5, respawn → rescue floor); until it lands, this world
	// leans on the thermostat and pretending otherwise here would just encode a wish.
	// Logged above so the ratio is visible when that phase starts.
	_ = meanPred
}
