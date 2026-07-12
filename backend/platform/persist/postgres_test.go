package persist

import (
	"context"
	"strings"
	"testing"
	"time"

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

// TestFakePgWriteBackup: one flush = event batch + snapshot row together; the
// snapshot's last_event_seq is the batch's max seq, NULL for an empty batch.
func TestFakePgWriteBackup(t *testing.T) {
	fake := NewFakePg()
	ctx := context.Background()

	if err := fake.WriteBackup(ctx, "run-1", 10, []byte("blob1"), nil); err != nil {
		t.Fatal(err)
	}
	evs := []core.Event{
		{Tick: 18, Seq: 39, AgentID: "a1", Type: "GoalSelected", Payload: map[string]any{"goal": "Satiety"}},
		{Tick: 19, Seq: 41, AgentID: "a1", Type: "PlanBuilt", Payload: nil},
	}
	if err := fake.WriteBackup(ctx, "run-1", 20, []byte("blob2"), evs); err != nil {
		t.Fatal(err)
	}

	if cnt := fake.SnapshotCount("run-1"); cnt != 2 {
		t.Errorf("SnapshotCount = %d, want 2", cnt)
	}
	if cnt := fake.EventCount("run-1"); cnt != 2 {
		t.Errorf("EventCount = %d, want 2", cnt)
	}
	rows := fake.SnapshotRecords("run-1")
	if len(rows) != 2 {
		t.Fatalf("SnapshotRecords = %d rows, want 2", len(rows))
	}
	if rows[0].LastEventSeq != nil {
		t.Errorf("row 0 LastEventSeq = %v, want nil (NULL — no events in that flush)", *rows[0].LastEventSeq)
	}
	if rows[1].LastEventSeq == nil || *rows[1].LastEventSeq != 41 {
		t.Errorf("row 1 LastEventSeq = %v, want 41 (max seq of the batch)", rows[1].LastEventSeq)
	}
}

// TestFakePgWriteBackupFailureIsAtomic: an injected failure persists NOTHING —
// no partial event batch, no snapshot row (the transaction rollback contract).
func TestFakePgWriteBackupFailureIsAtomic(t *testing.T) {
	fake := NewFakePg()
	ctx := context.Background()
	fake.FailWriteBackup = context.DeadlineExceeded

	evs := []core.Event{{Tick: 1, Seq: 0, AgentID: "a1", Type: "GoalSelected"}}
	if err := fake.WriteBackup(ctx, "run-1", 10, []byte("blob"), evs); err == nil {
		t.Fatal("want injected error")
	}
	if fake.SnapshotCount("run-1") != 0 || fake.EventCount("run-1") != 0 {
		t.Errorf("failed WriteBackup persisted rows (snapshots=%d events=%d); want nothing",
			fake.SnapshotCount("run-1"), fake.EventCount("run-1"))
	}

	// After the failure clears, the SAME batch persists intact (re-buffer + retry).
	fake.FailWriteBackup = nil
	if err := fake.WriteBackup(ctx, "run-1", 10, []byte("blob"), evs); err != nil {
		t.Fatal(err)
	}
	if fake.SnapshotCount("run-1") != 1 || fake.EventCount("run-1") != 1 {
		t.Errorf("retry did not persist the batch")
	}
}

// TestFakePgLatestSnapshot: "latest" is storage recency (created_at, id), NOT
// the highest tick — a /api/restart debugging rewind resets tick to 0 while
// preserving history, so the post-restart low-tick row must win over the
// pre-restart high-tick row.
func TestFakePgLatestSnapshot(t *testing.T) {
	fake := NewFakePg()
	ctx := context.Background()
	base := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	at := func(offset time.Duration) { fake.Clock = func() time.Time { return base.Add(offset) } }

	at(0)
	_ = fake.WriteBackup(ctx, "run-1", 10, []byte("blob-tick-10"), nil)
	at(1 * time.Minute)
	_ = fake.WriteBackup(ctx, "run-1", 30, []byte("blob-tick-30"), nil) // pre-restart high tick
	at(2 * time.Minute)
	_ = fake.WriteBackup(ctx, "run-1", 2, []byte("blob-tick-2"), nil) // post-restart rewind

	tick, blob, err := fake.LatestSnapshot(ctx, "run-1")
	if err != nil {
		t.Fatalf("LatestSnapshot: %v", err)
	}
	if tick != 2 {
		t.Errorf("LatestSnapshot tick = %d, want 2 (newest persisted row, not highest tick)", tick)
	}
	if string(blob) != "blob-tick-2" {
		t.Errorf("LatestSnapshot blob = %q, want blob-tick-2", string(blob))
	}

	// Equal created_at (same flush timestamp): the later insertion (higher row
	// id) wins — mirrors ORDER BY created_at DESC, id DESC.
	at(2 * time.Minute)
	_ = fake.WriteBackup(ctx, "run-1", 3, []byte("blob-tick-3"), nil)
	if tick, _, _ := fake.LatestSnapshot(ctx, "run-1"); tick != 3 {
		t.Errorf("equal created_at: tick = %d, want 3 (higher row id wins)", tick)
	}

	_, _, err = fake.LatestSnapshot(ctx, "no-such-run")
	if err == nil {
		t.Fatal("expected error for non-existent run")
	}
}

func TestFakePgEventHelpers(t *testing.T) {
	fake := NewFakePg()

	fake.SeedEvents("run-1", []core.Event{
		{Tick: 1, Seq: 0, AgentID: "a1", Type: "GoalSelected", Payload: map[string]any{"goal": "Satiety"}},
		{Tick: 1, Seq: 1, AgentID: "a1", Type: "ActionStarted", Payload: map[string]any{"action": "Eat"}},
		{Tick: 2, Seq: 2, AgentID: "a2", Type: "GoalSelected", Payload: nil},
	})

	if cnt := fake.EventCount("run-1"); cnt != 3 {
		t.Errorf("EventCount = %d, want 3", cnt)
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

// TestPgBackupStoreWriteBackup: the flush runs inside ONE transaction — event
// inserts then the snapshot insert (stamped with the batch's max seq) between
// BEGIN and COMMIT; an empty batch inserts only the snapshot with NULL.
func TestPgBackupStoreWriteBackup(t *testing.T) {
	client := newFakePgxClient()
	store := NewPgBackupStore(client)
	ctx := context.Background()
	run := core.RunID("pg-backup")

	evs := []core.Event{
		{Tick: 15, Seq: 3, AgentID: "a1", Type: "GoalSelected", Payload: map[string]any{"goal": "Satiety"}},
		{Tick: 15, Seq: 7, AgentID: "a2", Type: "PlanBuilt", Payload: nil},
	}
	if err := store.WriteBackup(ctx, run, 15, []byte(`{"test":true}`), evs); err != nil {
		t.Fatal(err)
	}

	// BEGIN, 2× event INSERT, snapshot INSERT, COMMIT.
	calls := client.execCalls
	if len(calls) != 5 {
		t.Fatalf("exec calls = %d, want 5 (BEGIN + 2 events + snapshot + COMMIT)", len(calls))
	}
	if calls[0].SQL != "BEGIN" || calls[4].SQL != "COMMIT" {
		t.Errorf("flush not wrapped in a transaction: first=%q last=%q", calls[0].SQL, calls[4].SQL)
	}
	for i := 1; i <= 2; i++ {
		if !strings.Contains(calls[i].SQL, "INSERT INTO events") {
			t.Errorf("call %d: %q, want event insert", i, calls[i].SQL)
		}
	}
	if !strings.Contains(calls[3].SQL, "INSERT INTO snapshots") {
		t.Errorf("call 3: %q, want snapshot insert", calls[3].SQL)
	}
	if got, ok := calls[3].Args[3].(*int64); !ok || got == nil || *got != 7 {
		t.Errorf("snapshot last_event_seq arg = %v, want *7 (max seq of the batch)", calls[3].Args[3])
	}

	// Empty batch: snapshot only, last_event_seq NULL.
	client2 := newFakePgxClient()
	store2 := NewPgBackupStore(client2)
	if err := store2.WriteBackup(ctx, run, 30, []byte(`{"test":2}`), nil); err != nil {
		t.Fatal(err)
	}
	calls2 := client2.execCalls
	if len(calls2) != 3 {
		t.Fatalf("exec calls = %d, want 3 (BEGIN + snapshot + COMMIT)", len(calls2))
	}
	if got := calls2[1].Args[3]; got != (*int64)(nil) {
		t.Errorf("empty-batch last_event_seq arg = %v, want nil (NULL)", got)
	}
}

// assertRolledBack asserts the recorded call sequence ends in ROLLBACK, never
// COMMITted, and never reached the snapshot INSERT (the all-or-nothing flush).
func assertRolledBack(t *testing.T, calls []execCall) {
	t.Helper()
	if len(calls) == 0 || calls[len(calls)-1].SQL != "ROLLBACK" {
		t.Fatalf("last exec = %q, want ROLLBACK", calls[len(calls)-1].SQL)
	}
	for _, c := range calls {
		if c.SQL == "COMMIT" {
			t.Fatal("COMMIT issued despite a failed statement")
		}
		if strings.Contains(c.SQL, "INSERT INTO snapshots") {
			t.Fatal("snapshot row inserted despite the failed event batch")
		}
	}
}

// A failing event INSERT inside WriteBackup rolls the whole flush back: no
// COMMIT, no snapshot row (the boundary can never reference unpersisted rows).
func TestPgBackupStoreWriteBackupRollsBackOnEventInsertFailure(t *testing.T) {
	client := newFakePgxClient()
	client.failOn = "INSERT INTO events"
	client.failErr = context.DeadlineExceeded
	store := NewPgBackupStore(client)

	evs := []core.Event{{Tick: 1, Seq: 0, AgentID: "a1", Type: "GoalSelected"}}
	if err := store.WriteBackup(context.Background(), "run", 10, []byte("b"), evs); err == nil {
		t.Fatal("want error when an event insert fails inside the transaction")
	}
	assertRolledBack(t, client.execCalls)
}

// A payload that cannot be marshaled aborts the flush mid-batch: the already
// inserted event rows roll back with the transaction — no COMMIT, no snapshot.
func TestPgBackupStoreWriteBackupRollsBackOnMarshalFailure(t *testing.T) {
	client := newFakePgxClient()
	store := NewPgBackupStore(client)

	evs := []core.Event{
		{Tick: 1, Seq: 0, AgentID: "a1", Type: "GoalSelected", Payload: map[string]any{"ok": true}},
		{Tick: 1, Seq: 1, AgentID: "a2", Type: "PlanBuilt", Payload: map[string]any{"bad": make(chan int)}},
	}
	if err := store.WriteBackup(context.Background(), "run", 10, []byte("b"), evs); err == nil {
		t.Fatal("want error when a payload cannot be marshaled")
	}
	inserted := 0
	for _, c := range client.execCalls {
		if strings.Contains(c.SQL, "INSERT INTO events") {
			inserted++
		}
	}
	if inserted != 1 {
		t.Errorf("event inserts before the marshal failure = %d, want 1 (rolled back)", inserted)
	}
	assertRolledBack(t, client.execCalls)
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

	// The query must select by storage recency, never by simulation tick
	// (restart rewinds tick to 0 while preserving history).
	if len(client.queryCalls) != 1 {
		t.Fatalf("query calls = %d, want 1", len(client.queryCalls))
	}
	sql := client.queryCalls[0].SQL
	if !strings.Contains(sql, "ORDER BY created_at DESC, id DESC") {
		t.Errorf("LatestSnapshot SQL orders by %q; want created_at DESC, id DESC", sql)
	}
	if strings.Contains(sql, "ORDER BY tick") {
		t.Errorf("LatestSnapshot SQL must not order by tick: %q", sql)
	}
}

// ── Retention / downsample (PruneSnapshots) ───────────────────────────────────

// TestFakePgPruneSnapshots exercises the §3 retention policy on the in-memory
// reference implementation: keep-all inside 1h, one-per-10-minute bucket up to
// 24h, one-per-day through 3d, then delete; the newest row of a bucket survives.
func TestFakePgPruneSnapshots(t *testing.T) {
	fake := NewFakePg()
	ctx := context.Background()
	run := core.RunID("prune-run")
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

	at := func(offset time.Duration) func() time.Time {
		return func() time.Time { return now.Add(offset) }
	}
	write := func(offset time.Duration, tick core.Tick) {
		fake.Clock = at(offset)
		if err := fake.WriteBackup(ctx, run, tick, []byte("b"), nil); err != nil {
			t.Fatal(err)
		}
	}

	// Recent band (< 1h old): all three survive.
	write(-10*time.Minute, 100)
	write(-20*time.Minute, 99)
	write(-50*time.Minute, 98)
	// Mid band (1h–24h old, 10-min buckets). 12:00-2h = 10:00 is a bucket start.
	write(-2*time.Hour, 50)                // bucket [10:00,10:10) — older of the pair
	write(-2*time.Hour+3*time.Minute, 51)  // same bucket, newer → survives
	write(-2*time.Hour+15*time.Minute, 52) // bucket [10:10,10:20) — survives
	// Mid-band tie-break: identical created_at, higher tick survives.
	write(-3*time.Hour, 40)
	write(-3*time.Hour, 42)
	// Coarse band (24h–3d old, 1-day buckets); rows strictly older than 3d expire.
	write(-72*time.Hour-3*time.Hour, 10) // expired
	write(-72*time.Hour+6*time.Hour, 12) // Jul 7 18:00 — survives
	write(-96*time.Hour, 5)              // expired
	// Another run's old rows are untouched.
	fake.Clock = at(-72 * time.Hour)
	_ = fake.WriteBackup(ctx, "other-run", 1, []byte("b"), nil)
	_ = fake.WriteBackup(ctx, "other-run", 2, []byte("b"), nil)

	if err := fake.PruneSnapshots(ctx, run, now); err != nil {
		t.Fatalf("PruneSnapshots: %v", err)
	}

	var ticks []core.Tick
	for _, row := range fake.SnapshotRecords(run) {
		ticks = append(ticks, row.Tick)
	}
	want := []core.Tick{100, 99, 98, 51, 52, 42, 12}
	if len(ticks) != len(want) {
		t.Fatalf("surviving ticks = %v, want %v", ticks, want)
	}
	for i := range want {
		if ticks[i] != want[i] {
			t.Fatalf("surviving ticks = %v, want %v", ticks, want)
		}
	}
	if cnt := fake.SnapshotCount("other-run"); cnt != 2 {
		t.Errorf("other-run rows = %d, want 2 (prune is per-run)", cnt)
	}
}

// TestFakePgPruneSnapshotsBoundaries pins the band edges: exactly now-1h is
// still keep-all (both fake and SQL use created_at < now-1h to enter the mid
// band); exactly now-24h is the mid band; exactly now-3d is retained while an
// older row is deleted.
func TestFakePgPruneSnapshotsBoundaries(t *testing.T) {
	fake := NewFakePg()
	ctx := context.Background()
	run := core.RunID("edge-run")
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	write := func(ts time.Time, tick core.Tick) {
		fake.Clock = func() time.Time { return ts }
		if err := fake.WriteBackup(ctx, run, tick, []byte("b"), nil); err != nil {
			t.Fatal(err)
		}
	}

	// Exactly at now-1h: keep-all band — BOTH rows in the same 10-min bucket survive.
	write(now.Add(-time.Hour), 200)
	write(now.Add(-time.Hour), 201)
	// Exactly at now-24h: MID band (10-min bucket), not coarse — the same-bucket
	// pair collapses to the newer row (tick tie-break at equal created_at).
	write(now.Add(-24*time.Hour), 100)
	write(now.Add(-24*time.Hour), 101)
	write(now.Add(-3*24*time.Hour), 50)                // exact retention boundary: keep
	write(now.Add(-3*24*time.Hour-time.Nanosecond), 1) // strictly older: delete

	if err := fake.PruneSnapshots(ctx, run, now); err != nil {
		t.Fatalf("PruneSnapshots: %v", err)
	}

	var ticks []core.Tick
	for _, row := range fake.SnapshotRecords(run) {
		ticks = append(ticks, row.Tick)
	}
	want := []core.Tick{200, 201, 101, 50}
	if len(ticks) != len(want) {
		t.Fatalf("surviving ticks = %v, want %v", ticks, want)
	}
	for i := range want {
		if ticks[i] != want[i] {
			t.Fatalf("surviving ticks = %v, want %v", ticks, want)
		}
	}
}

func TestFakePgPruneSnapshotsDeletesExpiredEvents(t *testing.T) {
	fake := NewFakePg()
	ctx := context.Background()
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	write := func(run core.RunID, ts time.Time, seq int64) {
		fake.Clock = func() time.Time { return ts }
		if err := fake.WriteBackup(ctx, run, core.Tick(seq), []byte("b"), []core.Event{{Seq: seq, Type: "trace"}}); err != nil {
			t.Fatal(err)
		}
	}

	write("run-a", now.Add(-3*24*time.Hour-time.Nanosecond), 1)
	write("run-a", now.Add(-3*24*time.Hour), 2)
	write("run-a", now.Add(-time.Hour), 3)
	write("run-b", now.Add(-4*24*time.Hour), 4)

	if err := fake.PruneSnapshots(ctx, "run-a", now); err != nil {
		t.Fatal(err)
	}
	if got := fake.EventsOf("run-a"); len(got) != 2 || got[0].Seq != 2 || got[1].Seq != 3 {
		t.Errorf("run-a events = %+v, want seqs [2 3]", got)
	}
	if got := fake.EventCount("run-b"); got != 1 {
		t.Errorf("run-b events = %d, want 1", got)
	}
}

// TestFakePgPruneSnapshotsIdempotent: a second prune with the same now deletes
// nothing further.
func TestFakePgPruneSnapshotsIdempotent(t *testing.T) {
	fake := NewFakePg()
	ctx := context.Background()
	run := core.RunID("prune-run")
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	for i := range 12 {
		fake.Clock = func() time.Time { return now.Add(-2*time.Hour - time.Duration(i)*time.Minute) }
		_ = fake.WriteBackup(ctx, run, core.Tick(i), []byte("b"), nil)
	}
	if err := fake.PruneSnapshots(ctx, run, now); err != nil {
		t.Fatal(err)
	}
	after := fake.SnapshotCount(run)
	if err := fake.PruneSnapshots(ctx, run, now); err != nil {
		t.Fatal(err)
	}
	if got := fake.SnapshotCount(run); got != after {
		t.Errorf("second prune changed row count %d → %d; want idempotent", after, got)
	}
}

// TestPgBackupStorePruneSnapshots checks the SQL plumbing: expired events and
// snapshots are deleted before the two windowed downsample bands.
func TestPgBackupStorePruneSnapshots(t *testing.T) {
	client := newFakePgxClient()
	store := NewPgBackupStore(client)
	ctx := context.Background()
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

	if err := store.PruneSnapshots(ctx, "pg-prune", now); err != nil {
		t.Fatalf("PruneSnapshots: %v", err)
	}
	if len(client.execCalls) != 4 {
		t.Fatalf("exec calls = %d, want 4 (expired events/snapshots + mid/coarse bands)", len(client.execCalls))
	}
	expiredEvents, expiredSnapshots := client.execCalls[0], client.execCalls[1]
	mid, coarse := client.execCalls[2], client.execCalls[3]
	retentionCutoff := now.Add(-3 * 24 * time.Hour)
	if !strings.Contains(expiredEvents.SQL, "DELETE FROM events") || expiredEvents.Args[0] != "pg-prune" ||
		!expiredEvents.Args[1].(time.Time).Equal(retentionCutoff) {
		t.Errorf("expired events call = %+v", expiredEvents)
	}
	if !strings.Contains(expiredSnapshots.SQL, "DELETE FROM snapshots") || expiredSnapshots.Args[0] != "pg-prune" ||
		!expiredSnapshots.Args[1].(time.Time).Equal(retentionCutoff) {
		t.Errorf("expired snapshots call = %+v", expiredSnapshots)
	}
	for i, call := range client.execCalls[2:] {
		if !strings.Contains(call.SQL, "row_number() OVER") {
			t.Errorf("band call %d: SQL missing windowed downsample: %s", i, call.SQL)
		}
		if call.Args[0] != "pg-prune" {
			t.Errorf("call %d: run arg = %v", i, call.Args[0])
		}
	}
	if got := mid.Args[1].(time.Time); !got.Equal(now.Add(-24 * time.Hour)) {
		t.Errorf("mid band lower = %v, want now-24h", got)
	}
	if got := mid.Args[2].(time.Time); !got.Equal(now.Add(-time.Hour)) {
		t.Errorf("mid band upper = %v, want now-1h", got)
	}
	if got := mid.Args[3].(int64); got != 600 {
		t.Errorf("mid band bucket = %d, want 600s", got)
	}
	if got := coarse.Args[2].(time.Time); !got.Equal(now.Add(-24 * time.Hour)) {
		t.Errorf("coarse band upper = %v, want now-24h", got)
	}
	if got := coarse.Args[1].(time.Time); !got.Equal(retentionCutoff) {
		t.Errorf("coarse band lower = %v, want now-3d", got)
	}
	if got := coarse.Args[3].(int64); got != 86400 {
		t.Errorf("coarse band bucket = %d, want 86400s", got)
	}

	if err := store.PruneSnapshots(ctx, "pg-prune", now.Add(5*time.Hour+59*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := len(client.execCalls); got != 4 {
		t.Errorf("SQL calls before 6h = %d, want 4", got)
	}
	if err := store.PruneSnapshots(ctx, "pg-prune", now.Add(6*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := len(client.execCalls); got != 8 {
		t.Errorf("SQL calls at 6h boundary = %d, want 8", got)
	}
}

// ── Regen reset (ResetRunData) ────────────────────────────────────────────────

func TestFakePgResetRunData(t *testing.T) {
	fake := NewFakePg()
	ctx := context.Background()

	_ = fake.UpsertRun(ctx, RunRecord{RunID: "run-a", Seed: 1, Status: "running", ConfigHash: "old"})
	_ = fake.WriteBackup(ctx, "run-a", 10, []byte("blob"), []core.Event{{Tick: 1, Seq: 0, Type: "GoalSelected"}})
	_ = fake.WriteBackup(ctx, "run-b", 5, []byte("blob"), []core.Event{{Tick: 1, Seq: 0, Type: "GoalSelected"}})

	newRec := RunRecord{RunID: "run-a", Seed: 99, SchemaVersion: SchemaVersion,
		StartedAt: "2026-07-11T00:00:00Z", Status: "running", ConfigHash: "new"}

	// An injected failure changes NOTHING — rows AND the runs metadata survive
	// (the transaction rollback contract).
	fake.FailResetRunData = context.DeadlineExceeded
	if err := fake.ResetRunData(ctx, newRec); err == nil {
		t.Fatal("want injected error")
	}
	if fake.SnapshotCount("run-a") != 1 || fake.EventCount("run-a") != 1 {
		t.Error("failed ResetRunData removed rows; want nothing deleted")
	}
	if rec, _ := fake.RunOf("run-a"); rec.Seed != 1 || rec.ConfigHash != "old" {
		t.Errorf("failed ResetRunData changed runs row to %+v; want old metadata intact", rec)
	}
	fake.FailResetRunData = nil

	if err := fake.ResetRunData(ctx, newRec); err != nil {
		t.Fatalf("ResetRunData: %v", err)
	}

	if cnt := fake.SnapshotCount("run-a"); cnt != 0 {
		t.Errorf("run-a snapshots = %d, want 0", cnt)
	}
	if cnt := fake.EventCount("run-a"); cnt != 0 {
		t.Errorf("run-a events = %d, want 0", cnt)
	}
	rec, ok := fake.RunOf("run-a")
	if !ok || rec.Seed != 99 || rec.ConfigHash != "new" {
		t.Errorf("runs row = %+v, want refreshed to the new record (seed 99)", rec)
	}
	if fake.SnapshotCount("run-b") != 1 || fake.EventCount("run-b") != 1 {
		t.Error("run-b rows touched; ResetRunData must be per-run")
	}
}

// TestPgBackupStoreResetRunData: one transaction — delete events, delete
// snapshots, upsert the runs row — between BEGIN and COMMIT.
func TestPgBackupStoreResetRunData(t *testing.T) {
	client := newFakePgxClient()
	store := NewPgBackupStore(client)
	ctx := context.Background()

	rec := RunRecord{RunID: "pg-reset", Seed: 7, SchemaVersion: SchemaVersion,
		StartedAt: "now", Status: "running", ConfigHash: "h"}
	if err := store.ResetRunData(ctx, rec); err != nil {
		t.Fatalf("ResetRunData: %v", err)
	}
	calls := client.execCalls
	if len(calls) != 5 {
		t.Fatalf("exec calls = %d, want 5 (BEGIN + 2 deletes + upsert + COMMIT)", len(calls))
	}
	if calls[0].SQL != "BEGIN" || calls[4].SQL != "COMMIT" {
		t.Errorf("reset not wrapped in a transaction: first=%q last=%q", calls[0].SQL, calls[4].SQL)
	}
	if !strings.Contains(calls[1].SQL, "DELETE FROM events") {
		t.Errorf("call 1 = %q, want events delete", calls[1].SQL)
	}
	if !strings.Contains(calls[2].SQL, "DELETE FROM snapshots") {
		t.Errorf("call 2 = %q, want snapshots delete", calls[2].SQL)
	}
	if !strings.Contains(calls[3].SQL, "INSERT INTO runs") {
		t.Errorf("call 3 = %q, want runs upsert", calls[3].SQL)
	}
	for i := 1; i <= 3; i++ {
		if calls[i].Args[0] != "pg-reset" {
			t.Errorf("call %d run arg = %v, want pg-reset", i, calls[i].Args[0])
		}
	}
}

// TestPgBackupStoreResetRunDataRollsBack: the runs-row upsert failing AFTER the
// deletes rolls the whole transaction back — no COMMIT is issued, so the old
// history and metadata survive (the exact hole ResetRunData exists to close).
func TestPgBackupStoreResetRunDataRollsBack(t *testing.T) {
	client := newFakePgxClient()
	client.failOn = "INSERT INTO runs"
	client.failErr = context.DeadlineExceeded
	store := NewPgBackupStore(client)
	ctx := context.Background()

	rec := RunRecord{RunID: "pg-reset", Seed: 7, Status: "running"}
	if err := store.ResetRunData(ctx, rec); err == nil {
		t.Fatal("want error when the runs upsert fails inside the transaction")
	}
	calls := client.execCalls
	if len(calls) == 0 || calls[len(calls)-1].SQL != "ROLLBACK" {
		t.Fatalf("last exec = %q, want ROLLBACK (deletes must not commit)", calls[len(calls)-1].SQL)
	}
	for _, c := range calls {
		if c.SQL == "COMMIT" {
			t.Fatal("COMMIT issued despite a failed statement")
		}
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
	evs := []core.Event{{Tick: 15, Seq: 0, AgentID: "a1", Type: "GoalSelected", Payload: nil}}
	if err := store.WriteBackup(ctx, run, 15, blob, evs); err != nil {
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

	fake := store.(*FakePg)
	if fake.EventCount(run) != 1 {
		t.Errorf("event count = %d, want 1", fake.EventCount(run))
	}
}
