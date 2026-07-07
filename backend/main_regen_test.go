package main

import (
	"context"
	"testing"

	"github.com/dogring/bdg/engine/world"
	"github.com/dogring/bdg/platform/config"
	"github.com/dogring/bdg/tools/worldgen"
)

// ── runLoop regen (POST /api/regen → tick-goroutine new-seed world rebuild) ────

// buildRabbitMeadow returns main.go's fixture-mode rebuild closure over the
// rabbit_meadow fixture (terrain{random:true} — the seed actually changes the map).
func buildRabbitMeadow(t *testing.T) func(int64) (*world.World, error) {
	t.Helper()
	contentDir := findContentDir(t)
	if contentDir == "" {
		t.Skip("content directory not found — run from repo root or set -content flag")
	}
	cfg, err := config.Load(contentDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fx, err := worldgen.ParseFile("tools/worldgen/testdata/rabbit_meadow.fixture.yaml",
		contentDir+"/schema/fixture.schema.json")
	if err != nil {
		t.Fatalf("worldgen.ParseFile: %v", err)
	}
	return func(seed int64) (*world.World, error) {
		f := fx
		if seed != 0 {
			f.Seed = seed
		}
		return worldgen.Load(f, cfg)
	}
}

func terrainCells(t *testing.T, w *world.World) []string {
	t.Helper()
	rv := w.RenderView()
	if rv.Terrain == nil {
		t.Fatalf("world has no terrain render view")
	}
	return rv.Terrain.Terrain
}

// TestRunLoop_RegenRebuildsWithNewSeed verifies the regen signal path: a pending
// seed-carrying signal makes runLoop swap in a world rebuilt from THAT seed (new
// random terrain, tick 0, counter reset) and purge the outgoing world's live keys.
func TestRunLoop_RegenRebuildsWithNewSeed(t *testing.T) {
	build := buildRabbitMeadow(t)
	w, err := build(0)
	if err != nil {
		t.Fatalf("worldgen.Load: %v", err)
	}
	original := terrainCells(t, w)
	for range 10 {
		w.Tick()
	}

	var purged []*world.World
	regen := make(chan int64, 1)
	regen <- 12345
	got := runLoop(context.Background(), w, 2, "test-run", 0, nil, nil, nil, 0,
		loopControl{regen: regen, rebuild: build,
			purge: func(_ context.Context, old *world.World) { purged = append(purged, old) }})

	if got == w {
		t.Error("runLoop returned the pre-regen world; want a rebuilt one")
	}
	if tick := got.CurrentTick(); int64(tick) != 2 {
		t.Errorf("post-regen tick = %d, want 2 (fresh world + 2 loop ticks)", tick)
	}
	if len(purged) != 1 || purged[0] != w {
		t.Errorf("purge called %d times with %v; want once with the outgoing world", len(purged), purged)
	}

	regenerated := terrainCells(t, got)
	if equalCells(original, regenerated) {
		t.Error("regen with a new seed produced identical terrain; want a re-rolled map")
	}

	// Reproducibility: the same pinned seed rebuilds the same terrain.
	again, err := build(12345)
	if err != nil {
		t.Fatalf("rebuild(12345): %v", err)
	}
	if !equalCells(regenerated, terrainCells(t, again)) {
		t.Error("two rebuilds with the same pinned seed disagree (determinism, D12)")
	}
}

// TestRunLoop_RegenZeroSeedDrawsOne verifies the loop draws a random seed for a
// 0-seed signal (the plain POST /api/regen path) and still swaps in a fresh world.
func TestRunLoop_RegenZeroSeedDrawsOne(t *testing.T) {
	build := buildRabbitMeadow(t)
	w, err := build(0)
	if err != nil {
		t.Fatalf("worldgen.Load: %v", err)
	}

	regen := make(chan int64, 1)
	regen <- 0
	got := runLoop(context.Background(), w, 2, "test-run", 0, nil, nil, nil, 0,
		loopControl{regen: regen, rebuild: build})

	if got == w {
		t.Error("runLoop returned the pre-regen world; want a rebuilt one")
	}
	if tick := got.CurrentTick(); int64(tick) != 2 {
		t.Errorf("post-regen tick = %d, want 2", tick)
	}
}

func TestRandomSeedNeverZero(t *testing.T) {
	for range 100 {
		if s := randomSeed(); s <= 0 {
			t.Fatalf("randomSeed() = %d, want positive", s)
		}
	}
}

func equalCells(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
