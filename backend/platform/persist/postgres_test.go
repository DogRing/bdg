package persist

import (
	"context"
	"testing"

	"github.com/dogring/bdg/engine/kernel/core"
)

// ── FakePg as BackupStore ─────────────────────────────────────────────────────

func TestFakePgUpsertRun(t *testing.T) {
	fake := NewFakePg()
	ctx := context.Background()

	rec := RunRecord{
		RunID:         "run-1",
		Seed:          42,
		SchemaVersion: SchemaVersion,
		StartedAt:     "2026-06-21T00:00:00Z",
		EndedAt:       "",
		Status:        "running",
		ConfigHash:    "abc123",
	}
	if err := fake.UpsertRun(ctx, rec); err != nil {
		t.Fatalf("UpsertRun: %v", err)
	}

	stored, ok := fake.RunOf("run-1")
	if !ok {
		t.Fatal("RunOf: run not found")
	}
	if stored.Seed != 42 {
		t.Errorf("Seed = %d, want 42", stored.Seed)
	}
	if stored.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", stored.SchemaVersion, SchemaVersion)
	}
	if stored.Status != "running" {
		t.Errorf("Status = %q, want running", stored.Status)
	}
	if stored.ConfigHash != "abc123" {
		t.Errorf("ConfigHash = %q, want abc123", stored.ConfigHash)
	}

	// Upsert again (update).
	rec.Status = "completed"
	rec.EndedAt = "2026-06-21T12:00:00Z"
	if err := fake.UpsertRun(ctx, rec); err != nil {
		t.Fatalf("UpsertRun (update): %v", err)
	}
	stored, _ = fake.RunOf("run-1")
	if stored.Status != "completed" {
		t.Errorf("after update, Status = %q, want completed", stored.Status)
	}
}

func TestFakePgWriteSnapshot(t *testing.T) {
	fake := NewFakePg()
	ctx := context.Background()

	if err := fake.WriteSnapshot(ctx, "run-1", 10, []byte("blob1")); err != nil {
		t.Fatal(err)
	}
	if err := fake.WriteSnapshot(ctx, "run-1", 20, []byte("blob2")); err != nil {
		t.Fatal(err)
	}

	if cnt := fake.SnapshotCount("run-1"); cnt != 2 {
		t.Errorf("SnapshotCount = %d, want 2", cnt)
	}
}

func TestFakePgLatestSnapshot(t *testing.T) {
	fake := NewFakePg()
	ctx := context.Background()

	_ = fake.WriteSnapshot(ctx, "run-1", 10, []byte("blob-tick-10"))
	_ = fake.WriteSnapshot(ctx, "run-1", 30, []byte("blob-tick-30"))
	_ = fake.WriteSnapshot(ctx, "run-1", 20, []byte("blob-tick-20"))

	tick, blob, err := fake.LatestSnapshot(ctx, "run-1")
	if err != nil {
		t.Fatalf("LatestSnapshot: %v", err)
	}
	if tick != 30 {
		t.Errorf("LatestSnapshot tick = %d, want 30", tick)
	}
	if string(blob) != "blob-tick-30" {
		t.Errorf("LatestSnapshot blob = %q, want blob-tick-30", string(blob))
	}

	_, _, err = fake.LatestSnapshot(ctx, "no-such-run")
	if err == nil {
		t.Fatal("expected error for non-existent run")
	}
}

func TestFakePgWriteEvents(t *testing.T) {
	fake := NewFakePg()
	ctx := context.Background()

	evs := []core.Event{
		{Tick: 1, Seq: 0, AgentID: "a1", Type: "GoalSelected", Payload: map[string]any{"goal": "Satiety"}},
		{Tick: 1, Seq: 1, AgentID: "a1", Type: "ActionStarted", Payload: map[string]any{"action": "Eat"}},
		{Tick: 2, Seq: 2, AgentID: "a2", Type: "GoalSelected", Payload: nil},
	}

	if err := fake.WriteEvents(ctx, "run-1", evs); err != nil {
		t.Fatalf("WriteEvents: %v", err)
	}

	if cnt := fake.EventCount("run-1"); cnt != 3 {
		t.Errorf("EventCount = %d, want 3", cnt)
	}

	if err := fake.WriteEvents(ctx, "run-1", nil); err != nil {
		t.Fatal(err)
	}
	if cnt := fake.EventCount("run-1"); cnt != 3 {
		t.Errorf("after empty WriteEvents, count = %d, want 3", cnt)
	}

	types := fake.EventTypes("run-1")
	if len(types) != 2 {
		t.Errorf("EventTypes count = %d, want 2", len(types))
	}
}

// ── PgBackupStore via fakePgxClient ───────────────────────────────────────────

func TestPgBackupStoreUpsertRun(t *testing.T) {
	client := newFakePgxClient()
	store := NewPgBackupStore(client)
	ctx := context.Background()

	rec := RunRecord{
		RunID:         "pg-run",
		Seed:          42,
		SchemaVersion: SchemaVersion,
		StartedAt:     "now",
		EndedAt:       "",
		Status:        "running",
		ConfigHash:    "hash",
	}
	if err := store.UpsertRun(ctx, rec); err != nil {
		t.Fatal(err)
	}

	// Verify by checking the reference FakePg.
	fake := NewFakePg()
	_ = fake.UpsertRun(ctx, rec)
	stored, ok := fake.RunOf("pg-run")
	if !ok {
		t.Fatal("run record not found")
	}
	if stored.Seed != 42 {
		t.Errorf("seed = %d, want 42", stored.Seed)
	}
	if stored.Status != "running" {
		t.Errorf("status = %q, want running", stored.Status)
	}
}

func TestPgBackupStoreWriteSnapshot(t *testing.T) {
	client := newFakePgxClient()
	store := NewPgBackupStore(client)
	ctx := context.Background()
	run := core.RunID("pg-snap")

	if err := store.WriteSnapshot(ctx, run, 15, []byte(`{"test":true}`)); err != nil {
		t.Fatal(err)
	}
}

func TestPgBackupStoreWriteEvents(t *testing.T) {
	client := newFakePgxClient()
	store := NewPgBackupStore(client)
	ctx := context.Background()
	run := core.RunID("pg-events")

	evs := []core.Event{
		{Tick: 15, Seq: 0, AgentID: "a1", Type: "TickDone", Payload: nil},
	}
	if err := store.WriteEvents(ctx, run, evs); err != nil {
		t.Fatal(err)
	}
}

func TestPgBackupStoreLatestSnapshot(t *testing.T) {
	// For LatestSnapshot, we need to set up the fake to return a specific row.
	client := newFakePgxClient()
	// Configure the fake to return a row for the snapshot query.
	// The real SQL starts with "SELECT tick, blob FROM snapshots" followed by WHERE/ORDER BY/LIMIT.
	client.rows["SELECT tick, blob FROM snapshots"] = &fakePgxRow{
		values: []any{int64(30), []byte("blob-tick-30")},
	}
	store := NewPgBackupStore(client)
	ctx := context.Background()
	run := core.RunID("pg-latest")

	tick, blob, err := store.LatestSnapshot(ctx, run)
	if err != nil {
		t.Fatalf("LatestSnapshot: %v", err)
	}
	if tick != 30 {
		t.Errorf("tick = %d, want 30", tick)
	}
	if string(blob) != "blob-tick-30" {
		t.Errorf("blob = %q, want blob-tick-30", string(blob))
	}
}

func TestPgBackupStoreWithEmptyEvents(t *testing.T) {
	client := newFakePgxClient()
	store := NewPgBackupStore(client)
	ctx := context.Background()

	// WriteEvents with nil should be a no-op.
	if err := store.WriteEvents(ctx, "run", nil); err != nil {
		t.Fatal(err)
	}
}

// ── BackupStore via FakePg (end-to-end interface test) ────────────────────────

func TestBackupStoreViaFakePg(t *testing.T) {
	var store BackupStore = NewFakePg()
	ctx := context.Background()
	run := core.RunID("e2e-pg")

	rec := RunRecord{
		RunID:         run,
		Seed:          1,
		SchemaVersion: SchemaVersion,
		StartedAt:     "now",
		EndedAt:       "",
		Status:        "running",
		ConfigHash:    "hash",
	}
	if err := store.UpsertRun(ctx, rec); err != nil {
		t.Fatal(err)
	}

	blob := []byte(`{"test":true}`)
	if err := store.WriteSnapshot(ctx, run, 15, blob); err != nil {
		t.Fatal(err)
	}

	tick, got, err := store.LatestSnapshot(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	if tick != 15 {
		t.Errorf("tick = %d, want 15", tick)
	}
	if string(got) != string(blob) {
		t.Errorf("blob = %q, want %q", string(got), string(blob))
	}

	evs := []core.Event{{Tick: 15, Seq: 0, AgentID: "a1", Type: "TickDone", Payload: nil}}
	if err := store.WriteEvents(ctx, run, evs); err != nil {
		t.Fatal(err)
	}

	fake := store.(*FakePg)
	if fake.EventCount(run) != 1 {
		t.Errorf("event count = %d, want 1", fake.EventCount(run))
	}
}
