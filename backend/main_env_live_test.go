package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/world"
	"github.com/dogring/bdg/platform/persist"
)

// floraFailStore is a LiveStore whose flora write always fails, to prove a
// FloraOn WriteFlora failure gates baseline publication (fix #4).
type floraFailStore struct{ *persist.FakeRedis }

func (floraFailStore) WriteFlora(context.Context, core.RunID, persist.FloraDoc) error {
	return errors.New("flora write boom")
}

// ── writeEnvLive (WI-P4 env render keys) ───────────────────────────────────────

// TestWriteEnvLive_WritesAllEnvKeys verifies an env-ON RenderView lands in every
// WI-P4 live key (animal/flora/climate/terrain, data-contracts §2) with the
// render-visible fields intact.
func TestWriteEnvLive_WritesAllEnvKeys(t *testing.T) {
	live := persist.NewFakeRedis()
	run := core.RunID("test-run")
	rv := world.RenderView{
		Tick: 7, HourOfDay: 10, DayNight: "day", YearFraction: 0.25,
		ClimateOn: true, Temperature: 18.5, Moisture: 0.4,
		Raining: true, WindDir: 1.5, WindMag: 0.6,
		Animals: []world.AnimalRenderView{
			{ID: "an:d1", Species: "deer", Pos: core.Vec2{X: 1, Y: 2}, Action: "Graze", Heading: 0.5, Stamina: 0.9},
		},
		FloraOn: true,
		Flora: []world.FloraRenderView{
			{ID: "pl:oak1", Species: "oak", Pos: core.Vec2{X: 3, Y: 4}, Stage: 2, Width: 1.5},
		},
		Terrain: &world.TerrainRenderView{
			CellSize: 2, Orientation: "flat", Cols: 2, Rows: 1, Terrain: []string{"grass", "water"}, Wear: []float64{0, 0.3},
		},
	}

	if !writeEnvLive(context.Background(), rv, run, 3, live) {
		t.Fatal("writeEnvLive returned false on a healthy store")
	}

	if got := live.AnimalViewOf(run, "an:d1"); !strings.Contains(got, `"species":"deer"`) {
		t.Fatalf("animal key missing/wrong: %s", got)
	}
	if got := live.FloraOf(run); !strings.Contains(got, `"object_id":"pl:oak1"`) || !strings.Contains(got, `"stage":2`) {
		t.Fatalf("flora key missing/wrong: %s", got)
	}
	// The flora baseline is wrapped in a FloraDoc tagged with the publishing
	// revision (data-contracts §2) — the frontend verifies it against the snapshot.
	if got := live.FloraOf(run); !strings.Contains(got, `"world_revision":3`) || !strings.Contains(got, `"flora":[`) {
		t.Fatalf("flora blob missing world_revision/flora wrapper: %s", got)
	}
	if got := live.ClimateOf(run); !strings.Contains(got, `"temperature":18.5`) || !strings.Contains(got, `"day_night":"day"`) {
		t.Fatalf("climate key missing/wrong: %s", got)
	}
	if got := live.TerrainOf(run); !strings.Contains(got, `"cell_size":2`) || !strings.Contains(got, `"terrain":["grass","water"]`) {
		t.Fatalf("terrain key missing/wrong: %s", got)
	}
	// The terrain blob is tagged with the publishing revision (data-contracts §2).
	if got := live.TerrainOf(run); !strings.Contains(got, `"world_revision":3`) {
		t.Fatalf("terrain blob missing world_revision tag: %s", got)
	}
}

// TestWriteEnvLive_FloraWriteFailureGatesPublication verifies fix #4: when flora
// is installed (FloraOn) but its baseline write fails, writeEnvLive returns
// false so the run-driver holds the revision — a published revision must always
// serve /api/flora. (Animal/climate failures still self-heal; only terrain+flora
// gate.)
func TestWriteEnvLive_FloraWriteFailureGatesPublication(t *testing.T) {
	live := floraFailStore{persist.NewFakeRedis()}
	rv := world.RenderView{
		Tick: 1, FloraOn: true,
		Flora: []world.FloraRenderView{{ID: "p1", Species: "oak", Pos: core.Vec2{X: 1, Y: 2}, Stage: 1, Width: 1}},
	}
	if writeEnvLive(context.Background(), rv, "r", 5, live) {
		t.Fatal("writeEnvLive must return false when a FloraOn WriteFlora fails (gates publication)")
	}
}

// TestWriteEnvLive_EnvOffWritesNothing verifies env-off neutrality: an empty
// RenderView (no env subsystem installed) writes NO env key — absence is the
// env-off signal (data-contracts §2).
func TestWriteEnvLive_EnvOffWritesNothing(t *testing.T) {
	live := persist.NewFakeRedis()
	run := core.RunID("test-run")

	if !writeEnvLive(context.Background(), world.RenderView{Tick: 7}, run, 1, live) {
		t.Fatal("writeEnvLive returned false with no terrain to write")
	}

	if got := live.FloraOf(run); got != "" {
		t.Fatalf("flora key written on env-off: %s", got)
	}
	if got := live.ClimateOf(run); got != "" {
		t.Fatalf("climate key written on env-off: %s", got)
	}
	if got := live.TerrainOf(run); got != "" {
		t.Fatalf("terrain key written on env-off: %s", got)
	}
}
