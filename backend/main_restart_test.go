package main

import (
	"context"
	"testing"

	"github.com/dogring/bdg/engine/world"
	"github.com/dogring/bdg/platform/config"
	"github.com/dogring/bdg/tools/worldgen"
)

// ── runLoop restart (POST /api/restart → tick-goroutine world rebuild) ─────────

// TestRunLoop_RestartRebuildsWorld verifies the restart signal path: a pending
// signal makes runLoop swap in a freshly rebuilt world (tick 0, D12: rebuilt on
// the loop goroutine) and reset its tick-limit counter, instead of continuing
// the old run.
func TestRunLoop_RestartRebuildsWorld(t *testing.T) {
	contentDir := findContentDir(t)
	if contentDir == "" {
		t.Skip("content directory not found — run from repo root or set -content flag")
	}
	cfg, err := config.Load(contentDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fx, err := worldgen.ParseFile("tools/worldgen/testdata/starter_village.fixture.yaml",
		contentDir+"/schema/fixture.schema.json")
	if err != nil {
		t.Fatalf("worldgen.ParseFile: %v", err)
	}
	buildWorld := func(int64) (*world.World, error) { return worldgen.Load(fx, cfg) }

	// Age the initial world well past the post-restart horizon.
	w, err := buildWorld(0)
	if err != nil {
		t.Fatalf("worldgen.Load: %v", err)
	}
	for range 10 {
		w.Tick()
	}
	if got := w.CurrentTick(); int64(got) != 10 {
		t.Fatalf("pre-restart tick = %d, want 10", got)
	}

	// A pending restart signal + a 2-tick limit: the loop must consume the
	// signal first (fresh world, counter reset) and then tick twice.
	restart := make(chan struct{}, 1)
	restart <- struct{}{}
	got := runLoop(context.Background(), w, 2, "test-run", 0, nil, nil, nil, 0,
		loopControl{restart: restart, rebuild: buildWorld})

	if got == w {
		t.Error("runLoop returned the pre-restart world; want a rebuilt one")
	}
	if tick := got.CurrentTick(); int64(tick) != 2 {
		t.Errorf("post-restart tick = %d, want 2 (fresh world + 2 loop ticks)", tick)
	}
}

// TestRunLoop_RestartRebuildFailureKeepsWorld verifies a failing rebuild is
// non-fatal: the loop logs and keeps ticking the current world.
func TestRunLoop_RestartRebuildFailureKeepsWorld(t *testing.T) {
	contentDir := findContentDir(t)
	if contentDir == "" {
		t.Skip("content directory not found — run from repo root or set -content flag")
	}
	cfg, err := config.Load(contentDir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	fx, err := worldgen.ParseFile("tools/worldgen/testdata/starter_village.fixture.yaml",
		contentDir+"/schema/fixture.schema.json")
	if err != nil {
		t.Fatalf("worldgen.ParseFile: %v", err)
	}
	w, err := worldgen.Load(fx, cfg)
	if err != nil {
		t.Fatalf("worldgen.Load: %v", err)
	}
	for range 10 {
		w.Tick()
	}

	restart := make(chan struct{}, 1)
	restart <- struct{}{}
	failing := func(int64) (*world.World, error) { return nil, context.DeadlineExceeded }
	got := runLoop(context.Background(), w, 2, "test-run", 0, nil, nil, nil, 0,
		loopControl{restart: restart, rebuild: failing})

	if got != w {
		t.Error("runLoop swapped the world despite a failed rebuild")
	}
	if tick := got.CurrentTick(); int64(tick) != 12 {
		t.Errorf("tick = %d, want 12 (old world kept, +2 loop ticks)", tick)
	}
}
