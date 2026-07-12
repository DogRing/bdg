package main

import (
	"context"
	"sync"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/world"
	"github.com/dogring/bdg/platform/events"
	"github.com/dogring/bdg/platform/persist"
)

// ── multiEmitter seq stamping (one shared numbering across all sinks) ──────────

type recordingSink struct {
	mu  sync.Mutex
	evs []core.Event
}

func (r *recordingSink) Emit(e core.Event) {
	r.mu.Lock()
	r.evs = append(r.evs, e)
	r.mu.Unlock()
}

func TestMultiEmitterStampsSharedSeq(t *testing.T) {
	a, b := &recordingSink{}, &recordingSink{}
	m := &multiEmitter{sinks: []core.EventEmitter{a, b}}

	m.Emit(core.Event{Type: events.TypeGoalSelected})
	m.Emit(core.Event{Type: events.TypeAgentFrame}) // high-frequency: still consumes a seq
	m.Emit(core.Event{Type: events.TypePlanBuilt})

	for name, sink := range map[string]*recordingSink{"a": a, "b": b} {
		if len(sink.evs) != 3 {
			t.Fatalf("sink %s: %d events, want 3", name, len(sink.evs))
		}
		for i, ev := range sink.evs {
			if ev.Seq != int64(i) {
				t.Errorf("sink %s event %d: Seq = %d, want %d", name, i, ev.Seq, i)
			}
		}
	}
}

// ── eventBuffer: filter, seq-sorted drain, restore ─────────────────────────────

func TestEventBufferFiltersHighFrequency(t *testing.T) {
	buf := &eventBuffer{}
	for _, typ := range []string{
		events.TypeTickDone, events.TypeAgentFrame, events.TypeWorldFrame, events.TypeSnapshotReady,
	} {
		buf.Emit(core.Event{Type: typ, Seq: 1})
	}
	buf.Emit(core.Event{Type: events.TypeGoalSelected, Seq: 5})

	evs := buf.drain()
	if len(evs) != 1 || evs[0].Type != events.TypeGoalSelected {
		t.Fatalf("drained %v; want only the why-trace event", evs)
	}
}

func TestEventBufferPreservesOrderAndRestorePrepends(t *testing.T) {
	buf := &eventBuffer{}
	// Single-writer contract: emissions arrive in seq order (the fan-out stamps
	// seq immediately before Emit) and drain returns them verbatim (FIFO).
	buf.Emit(core.Event{Type: events.TypeGoalSelected, Seq: 3})
	buf.Emit(core.Event{Type: events.TypePlanBuilt, Seq: 5})
	buf.Emit(core.Event{Type: events.TypeActionStarted, Seq: 7})

	evs := buf.drain()
	if len(evs) != 3 || evs[0].Seq != 3 || evs[1].Seq != 5 || evs[2].Seq != 7 {
		t.Fatalf("drain order = %v; want emission order 3,5,7", evs)
	}

	// A failed flush/regen restores the drained batch AHEAD of newer events, so
	// the buffer stays seq-ascending by construction.
	buf.Emit(core.Event{Type: events.TypeGoalSelected, Seq: 9})
	buf.restore(evs)
	evs = buf.drain()
	if len(evs) != 4 || evs[0].Seq != 3 || evs[3].Seq != 9 {
		t.Fatalf("after restore, drain = %v; want 3,5,7,9", evs)
	}
	if buf.drain() != nil {
		t.Error("second drain not empty")
	}
}

func TestEventBufferBoundsMemoryByDroppingOldestTrace(t *testing.T) {
	buf := &eventBuffer{}
	for i := 0; i <= eventBufferMaxEvents; i++ {
		buf.Emit(core.Event{Type: events.TypeGoalSelected, Seq: int64(i)})
	}

	evs := buf.drain()
	wantDropped := uint64(eventBufferTrimEvents + 1)
	if got := buf.takeDropped(); got != wantDropped {
		t.Fatalf("dropped = %d, want %d", got, wantDropped)
	}
	if len(evs) != eventBufferMaxEvents-eventBufferTrimEvents {
		t.Fatalf("buffer length = %d, want %d", len(evs), eventBufferMaxEvents-eventBufferTrimEvents)
	}
	if evs[0].Seq != int64(wantDropped) || evs[len(evs)-1].Seq != eventBufferMaxEvents {
		t.Errorf("surviving seq range = %d..%d, want %d..%d",
			evs[0].Seq, evs[len(evs)-1].Seq, wantDropped, eventBufferMaxEvents)
	}
	if got := buf.takeDropped(); got != 0 {
		t.Errorf("second dropped report = %d, want 0", got)
	}
}

// ── flushSnapshot: last_event_seq semantics against FakePg ─────────────────────

func TestFlushSnapshotLastEventSeq(t *testing.T) {
	build := buildRabbitMeadow(t)
	w, err := build(0)
	if err != nil {
		t.Fatalf("worldgen.Load: %v", err)
	}
	fake := persist.NewFakePg()
	buf := &eventBuffer{}
	ctx := context.Background()

	// Flush 1: two why-trace events buffered → snapshot stamps their max seq.
	buf.Emit(core.Event{Type: events.TypeGoalSelected, Seq: 3})
	buf.Emit(core.Event{Type: events.TypePlanBuilt, Seq: 7})
	flushSnapshot(ctx, w, "flush-run", nil, nil, fake, buf)

	// Flush 2: buffer empty → NULL boundary.
	flushSnapshot(ctx, w, "flush-run", nil, nil, fake, buf)

	rows := fake.SnapshotRecords("flush-run")
	if len(rows) != 2 {
		t.Fatalf("snapshot rows = %d, want 2", len(rows))
	}
	if rows[0].LastEventSeq == nil || *rows[0].LastEventSeq != 7 {
		t.Errorf("flush 1 last_event_seq = %v, want 7 (max seq of the drained batch)", rows[0].LastEventSeq)
	}
	if rows[1].LastEventSeq != nil {
		t.Errorf("flush 2 last_event_seq = %v, want nil (no events drained)", *rows[1].LastEventSeq)
	}
	if got := fake.EventCount("flush-run"); got != 2 {
		t.Errorf("event rows = %d, want 2", got)
	}
}

// A failed WriteBackup persists nothing; the drained batch goes back into the
// buffer and the NEXT flush writes it intact (no lost why-trace, no partial
// batch, correct last_event_seq on the retry).
func TestFlushSnapshotRetriesFailedBackup(t *testing.T) {
	build := buildRabbitMeadow(t)
	w, err := build(0)
	if err != nil {
		t.Fatalf("worldgen.Load: %v", err)
	}
	fake := persist.NewFakePg()
	buf := &eventBuffer{}
	ctx := context.Background()

	buf.Emit(core.Event{Type: events.TypeGoalSelected, Seq: 3})
	fake.FailWriteBackup = context.DeadlineExceeded
	flushSnapshot(ctx, w, "retry-run", nil, nil, fake, buf)

	if fake.SnapshotCount("retry-run") != 0 || fake.EventCount("retry-run") != 0 {
		t.Fatal("failed flush persisted rows; want nothing (transaction rollback)")
	}
	if got := fake.PruneCallCount(); got != 0 {
		t.Errorf("prune calls after failed backup = %d, want 0 (prune only after a commit)", got)
	}

	// Next cadence: outage over; a newer event joined the buffer meanwhile.
	fake.FailWriteBackup = nil
	buf.Emit(core.Event{Type: events.TypePlanBuilt, Seq: 5})
	flushSnapshot(ctx, w, "retry-run", nil, nil, fake, buf)

	if got := fake.EventCount("retry-run"); got != 2 {
		t.Errorf("event rows after retry = %d, want 2 (restored + new)", got)
	}
	rows := fake.SnapshotRecords("retry-run")
	if len(rows) != 1 || rows[0].LastEventSeq == nil || *rows[0].LastEventSeq != 5 {
		t.Errorf("retry snapshot rows = %+v, want one row with last_event_seq 5", rows)
	}
	if got := fake.PruneCallCount(); got != 1 {
		t.Errorf("prune calls after successful backup = %d, want 1", got)
	}
}

// A prune failure after a COMMITTED backup neither invalidates the backup nor
// restores the drained events — downsampling just waits for the next flush.
func TestFlushSnapshotPruneFailureKeepsBackup(t *testing.T) {
	build := buildRabbitMeadow(t)
	w, err := build(0)
	if err != nil {
		t.Fatalf("worldgen.Load: %v", err)
	}
	fake := persist.NewFakePg()
	fake.FailPruneSnapshots = context.DeadlineExceeded
	buf := &eventBuffer{}
	ctx := context.Background()

	buf.Emit(core.Event{Type: events.TypeGoalSelected, Seq: 3})
	flushSnapshot(ctx, w, "prune-fail-run", nil, nil, fake, buf)

	if fake.SnapshotCount("prune-fail-run") != 1 || fake.EventCount("prune-fail-run") != 1 {
		t.Error("committed backup lost after prune failure")
	}
	if evs := buf.drain(); len(evs) != 0 {
		t.Errorf("buffer = %v after successful backup; want empty (no restoration on prune failure)", evs)
	}
	if got := fake.PruneCallCount(); got != 1 {
		t.Errorf("prune calls = %d, want 1 (attempted once, after the commit)", got)
	}
}

// ── runLoop cleanup routing: regen → reset, restart → purge ────────────────────

func TestRunLoopRegenUsesResetRestartUsesPurge(t *testing.T) {
	build := buildRabbitMeadow(t)
	w, err := build(0)
	if err != nil {
		t.Fatalf("worldgen.Load: %v", err)
	}

	var purged, resets int
	var resetSeed int64
	ctl := loopControl{
		rebuild: build,
		purge:   func(context.Context, *world.World) { purged++ },
		reset: func(_ context.Context, _ *world.World, seed int64) error {
			resets++
			resetSeed = seed
			return nil
		},
	}

	regen := make(chan int64, 1)
	regen <- 777
	ctl.regen = regen
	w = runLoop(context.Background(), w, 1, "test-run", 0, nil, nil, nil, 0, ctl)
	if resets != 1 || purged != 0 {
		t.Errorf("regen: reset=%d purge=%d; want reset once (destructive new-map cleanup), no purge", resets, purged)
	}
	if resetSeed != 777 {
		t.Errorf("reset seed = %d, want the regen seed 777", resetSeed)
	}

	restart := make(chan struct{}, 1)
	restart <- struct{}{}
	ctl.regen = nil
	ctl.restart = restart
	_ = runLoop(context.Background(), w, 1, "test-run", 0, nil, nil, nil, 0, ctl)
	if resets != 1 || purged != 1 {
		t.Errorf("restart: reset=%d purge=%d; want purge once (debugging rewind), no extra reset", resets, purged)
	}
}

// assertBufferSeqs drains buf and asserts it holds exactly the events with the
// given seqs, in that order (no candidate leakage, no duplication, no reorder).
func assertBufferSeqs(t *testing.T, buf *eventBuffer, want []int64) {
	t.Helper()
	evs := buf.drain()
	if len(evs) != len(want) {
		t.Fatalf("buffer holds %d events (%+v), want seqs %v", len(evs), evs, want)
	}
	for i, seq := range want {
		if evs[i].Seq != seq {
			t.Fatalf("buffer event %d has seq %d, want %d (order/content: %+v)", i, evs[i].Seq, seq, evs)
		}
	}
}

// A failing reset (mandatory Postgres cleanup) ABORTS the regen: the rebuilt
// world is discarded (current world keeps running and ticking), its candidate
// construction events are dropped, and the old map's buffered why-trace is
// restored exactly once for the next flush.
func TestRunLoopRegenAbortsOnResetFailure(t *testing.T) {
	build := buildRabbitMeadow(t)
	w, err := build(0)
	if err != nil {
		t.Fatalf("worldgen.Load: %v", err)
	}
	for range 5 {
		w.Tick()
	}

	buf := &eventBuffer{}
	buf.Emit(core.Event{Type: events.TypeGoalSelected, Seq: 11, AgentID: "old-agent"})

	regen := make(chan int64, 1)
	regen <- 42
	got := runLoop(context.Background(), w, 2, "test-run", 0, nil, nil, buf, 0, loopControl{
		regen: regen,
		rebuild: func(seed int64) (*world.World, error) {
			// Candidate construction events reach the shared buffer during the
			// rebuild (worldgen emits through the same fan-out in production).
			buf.Emit(core.Event{Type: events.TypeActionStarted, Seq: 12, AgentID: "candidate"})
			return build(seed)
		},
		reset: func(context.Context, *world.World, int64) error {
			return context.DeadlineExceeded
		},
	})

	if got != w {
		t.Error("regen swapped the world despite a failed cleanup; want current world kept")
	}
	if tick := got.CurrentTick(); int64(tick) != 7 {
		t.Errorf("tick = %d, want 7 (old world kept, +2 loop ticks)", tick)
	}
	assertBufferSeqs(t, buf, []int64{11}) // old batch once; candidate seq 12 dropped
}

// A failing REBUILD must not leak the candidate world's partial construction
// events into the current world's why-trace: the candidate batch is discarded
// and the old batch restored exactly once in its original seq order — for both
// the regen and the restart signal.
func TestRunLoopRebuildFailureDiscardsCandidateEvents(t *testing.T) {
	build := buildRabbitMeadow(t)
	w, err := build(0)
	if err != nil {
		t.Fatalf("worldgen.Load: %v", err)
	}

	for _, tc := range []struct {
		name string
		arm  func(ctl *loopControl)
	}{
		{"regen", func(ctl *loopControl) {
			ch := make(chan int64, 1)
			ch <- 42
			ctl.regen = ch
			ctl.reset = func(context.Context, *world.World, int64) error {
				t.Error("reset called despite a failed rebuild")
				return nil
			}
		}},
		{"restart", func(ctl *loopControl) {
			ch := make(chan struct{}, 1)
			ch <- struct{}{}
			ctl.restart = ch
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buf := &eventBuffer{}
			buf.Emit(core.Event{Type: events.TypeGoalSelected, Seq: 3, AgentID: "old-a"})
			buf.Emit(core.Event{Type: events.TypePlanBuilt, Seq: 5, AgentID: "old-b"})
			ctl := loopControl{
				rebuild: func(int64) (*world.World, error) {
					buf.Emit(core.Event{Type: events.TypeActionStarted, Seq: 9, AgentID: "candidate"})
					return nil, context.DeadlineExceeded
				},
			}
			tc.arm(&ctl)
			got := runLoop(context.Background(), w, 1, "test-run", 0, nil, nil, buf, 0, ctl)
			if got != w {
				t.Error("world swapped despite a failed rebuild; want current world kept")
			}
			assertBufferSeqs(t, buf, []int64{3, 5})
		})
	}
}

// A successful REGEN starts a fresh history: the old world's buffered batch is
// dropped (its Postgres rows were just deleted via reset) and only the
// candidate world's construction events remain buffered for the first flush.
func TestRunLoopRegenSuccessDropsOldKeepsCandidateEvents(t *testing.T) {
	build := buildRabbitMeadow(t)
	w, err := build(0)
	if err != nil {
		t.Fatalf("worldgen.Load: %v", err)
	}

	buf := &eventBuffer{}
	buf.Emit(core.Event{Type: events.TypeGoalSelected, Seq: 3, AgentID: "old"})
	regen := make(chan int64, 1)
	regen <- 42
	got := runLoop(context.Background(), w, 1, "test-run", 0, nil, nil, buf, 0, loopControl{
		regen: regen,
		rebuild: func(seed int64) (*world.World, error) {
			buf.Emit(core.Event{Type: events.TypeActionStarted, Seq: 7, AgentID: "candidate"})
			return build(seed)
		},
		reset: func(context.Context, *world.World, int64) error { return nil },
	})
	if got == w {
		t.Error("regen did not swap in the rebuilt world")
	}
	assertBufferSeqs(t, buf, []int64{7}) // candidate only; old seq 3 dropped
}

// A successful RESTART appends to the run's history: the old batch is re-queued
// AHEAD of the candidate world's construction events, seq order preserved.
func TestRunLoopRestartSuccessKeepsOldThenCandidateEvents(t *testing.T) {
	build := buildRabbitMeadow(t)
	w, err := build(0)
	if err != nil {
		t.Fatalf("worldgen.Load: %v", err)
	}

	buf := &eventBuffer{}
	buf.Emit(core.Event{Type: events.TypeGoalSelected, Seq: 3, AgentID: "old"})
	restart := make(chan struct{}, 1)
	restart <- struct{}{}
	got := runLoop(context.Background(), w, 1, "test-run", 0, nil, nil, buf, 0, loopControl{
		restart: restart,
		rebuild: func(seed int64) (*world.World, error) {
			buf.Emit(core.Event{Type: events.TypeActionStarted, Seq: 7, AgentID: "candidate"})
			return build(seed)
		},
	})
	if got == w {
		t.Error("restart did not swap in the rebuilt world")
	}
	assertBufferSeqs(t, buf, []int64{3, 7}) // old first, then candidate — seq order
}
