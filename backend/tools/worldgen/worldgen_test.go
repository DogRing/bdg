package worldgen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/space/scent"
	"github.com/dogring/bdg/engine/world"
	"github.com/dogring/bdg/platform/config"
)

type recordingEmitter struct {
	events []core.Event
}

func (r *recordingEmitter) Emit(e core.Event) {
	switch e.Type {
	case "AnimalDied", "Decayed":
	default:
		return
	}
	r.events = append(r.events, e)
}

func TestStarterFixtureLiveSmoke(t *testing.T) {
	digestA := runStarterFixtureSmoke(t)
	digestB := runStarterFixtureSmoke(t)
	if digestA != digestB {
		t.Fatalf("starter fixture run was not deterministic\nA:\n%s\nB:\n%s", digestA, digestB)
	}
}

func runStarterFixtureSmoke(t *testing.T) string {
	t.Helper()
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

	initialAnimals := animalsByID(w.Animals())
	initialTemp, ok := w.ClimateCellAt(core.Vec2{X: 80, Y: 60})
	if !ok {
		t.Fatalf("climate not installed")
	}
	initialHunger := make(map[core.ObjectID]float64)
	for id, a := range initialAnimals {
		if a.Species == "fish" {
			continue
		}
		if h, ok := a.Drives["hunger"]; ok {
			initialHunger[id] = h
		}
	}
	minHunger := make(map[core.ObjectID]float64, len(initialHunger))
	for id, h := range initialHunger {
		minHunger[id] = h
	}
	var carcassPos core.Vec2
	sawCarcass := false
	sawCarrion := false

	for i := 0; i < 600; i++ {
		w.Tick()
		for _, a := range w.Animals() {
			if _, ok := minHunger[a.ID]; !ok {
				continue
			}
			if h := a.Drives["hunger"]; h < minHunger[a.ID] {
				minHunger[a.ID] = h
			}
		}
		if pos, ok := firstCarcassPos(w.State()); ok {
			carcassPos = pos
			sawCarcass = true
			if w.ScentIntensityAt(scent.ChanCarrion, pos) > 0 {
				sawCarrion = true
			}
		}
	}

	finalAnimals := animalsByID(w.Animals())
	if !anyAnimalMoved(initialAnimals, finalAnimals) {
		t.Fatalf("no animal moved after activation smoke")
	}
	if !anyHungerDropped(initialHunger, minHunger) {
		t.Fatalf("no herbivore hunger dropped; grazing did not fire")
	}
	if !hasAnimalDeath(rec.events) {
		t.Fatalf("no AnimalDied event; predation did not reach death")
	}
	if !sawCarcass {
		t.Fatalf("predation death did not create a carcass object")
	}
	if !sawCarrion {
		t.Fatalf("carcass did not emit committed carrion scent")
	}
	finalTemp, ok := w.ClimateCellAt(core.Vec2{X: 80, Y: 60})
	if !ok || finalTemp.Temperature == initialTemp.Temperature {
		t.Fatalf("climate temperature did not vary: initial %.3f final %.3f", initialTemp.Temperature, finalTemp.Temperature)
	}

	return smokeDigest(w.Animals(), rec.events, finalTemp.Temperature, carcassPos)
}

func animalsByID(animals []fauna.Animal) map[core.ObjectID]fauna.Animal {
	out := make(map[core.ObjectID]fauna.Animal, len(animals))
	for _, a := range animals {
		out[a.ID] = a
	}
	return out
}

func anyAnimalMoved(before, after map[core.ObjectID]fauna.Animal) bool {
	for id, a := range before {
		b, ok := after[id]
		if !ok {
			continue
		}
		if a.Pos.Distance(b.Pos) > 1e-6 {
			return true
		}
	}
	return false
}

func anyHungerDropped(initial, minObserved map[core.ObjectID]float64) bool {
	for id, start := range initial {
		low, ok := minObserved[id]
		if !ok {
			continue
		}
		if low < start {
			return true
		}
	}
	return false
}

func hasAnimalDeath(events []core.Event) bool {
	for _, e := range events {
		if e.Type == "AnimalDied" {
			return true
		}
	}
	return false
}

func firstCarcassPos(state world.WorldState) (core.Vec2, bool) {
	for _, obj := range state.Objects {
		if obj.Kind == "carcass" {
			return obj.Pos, true
		}
	}
	return core.Vec2{}, false
}

func smokeDigest(animals []fauna.Animal, events []core.Event, temp float64, carcass core.Vec2) string {
	var b strings.Builder
	fmt.Fprintf(&b, "temp=%.4f carcass=%.4f,%.4f\n", temp, carcass.X, carcass.Y)
	for _, a := range animals {
		fmt.Fprintf(&b, "%s|%s|%.4f,%.4f|v=%.4f|h=%.4f\n", a.ID, a.Species, a.Pos.X, a.Pos.Y, a.Vital, a.Drives["hunger"])
	}
	counts := make(map[string]int)
	for _, e := range events {
		counts[e.Type]++
	}
	types := make([]string, 0, len(counts))
	for typ := range counts {
		types = append(types, typ)
	}
	sort.Strings(types)
	for _, typ := range types {
		fmt.Fprintf(&b, "event.%s=%d\n", typ, counts[typ])
	}
	return b.String()
}

func findExisting(t *testing.T, candidates ...string) string {
	t.Helper()
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	t.Fatalf("none of the candidate paths exist: %v", candidates)
	return ""
}
