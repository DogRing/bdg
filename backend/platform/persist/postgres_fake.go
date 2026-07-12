package persist

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/dogring/bdg/engine/kernel/core"
)

// FakePg is an in-memory stub implementing BackupStore for unit tests. It
// mirrors the production failure semantics: WriteBackup and ResetRunData are
// all-or-nothing (an injected failure stores/changes NOTHING — the transaction
// rollback contract).
type FakePg struct {
	mu        sync.Mutex
	runs      map[string]RunRecord
	snapshots []pgSnapshotRow // ordered by insertion (insertion index ≙ row id)
	events    []pgEventRow    // ordered by insertion

	// Clock supplies created_at for WriteBackup snapshot rows (test hook for
	// the PruneSnapshots retention policy). nil ⇒ time.Now.
	Clock func() time.Time

	// Failure injection (mirrors a failed transaction: nothing persisted).
	FailWriteBackup    error // non-nil ⇒ WriteBackup returns it, stores nothing
	FailResetRunData   error // non-nil ⇒ ResetRunData returns it, changes nothing
	FailPruneSnapshots error // non-nil ⇒ PruneSnapshots returns it, deletes nothing

	pruneCalls int // how many PruneSnapshots maintenance runs passed the 6h gate
	lastPrune  map[core.RunID]time.Time
}

type pgSnapshotRow struct {
	RunID        core.RunID
	Tick         core.Tick
	Blob         []byte
	CreatedAt    time.Time
	LastEventSeq *int64 // nil ⇒ NULL (no why-trace event in that flush)
}

type pgEventRow struct {
	RunID     core.RunID
	Event     core.Event
	CreatedAt time.Time
}

func NewFakePg() *FakePg {
	return &FakePg{
		runs:      make(map[string]RunRecord),
		lastPrune: make(map[core.RunID]time.Time),
	}
}

func (f *FakePg) now() time.Time {
	if f.Clock != nil {
		return f.Clock()
	}
	return time.Now()
}

func (f *FakePg) UpsertRun(_ context.Context, r RunRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs[string(r.RunID)] = r
	return nil
}

// WriteBackup mirrors PgBackupStore: event rows + the snapshot row (stamped
// with the batch's max seq; nil when evs is empty) land together, or — on an
// injected failure — not at all (transaction rollback semantics).
func (f *FakePg) WriteBackup(_ context.Context, run core.RunID, tick core.Tick, blob []byte, evs []core.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailWriteBackup != nil {
		return f.FailWriteBackup
	}
	createdAt := f.now()
	var lastSeq *int64
	for _, e := range evs {
		f.events = append(f.events, pgEventRow{RunID: run, Event: e, CreatedAt: createdAt})
		if lastSeq == nil || e.Seq > *lastSeq {
			s := e.Seq
			lastSeq = &s
		}
	}
	f.snapshots = append(f.snapshots, pgSnapshotRow{
		RunID:        run,
		Tick:         tick,
		Blob:         cloneBytes(blob),
		CreatedAt:    createdAt,
		LastEventSeq: lastSeq,
	})
	return nil
}

func (f *FakePg) LatestSnapshot(_ context.Context, run core.RunID) (core.Tick, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Storage recency (created_at, then insertion order ≙ row id), NOT highest
	// tick — mirrors PgBackupStore (a restart rewind writes lower ticks later).
	best := -1
	for i, row := range f.snapshots {
		if row.RunID != run {
			continue
		}
		if best < 0 || !row.CreatedAt.Before(f.snapshots[best].CreatedAt) {
			best = i
		}
	}
	if best < 0 {
		return 0, nil, fmt.Errorf("fake: no snapshots for run %s", run)
	}
	return f.snapshots[best].Tick, cloneBytes(f.snapshots[best].Blob), nil
}

// PruneSnapshots mirrors PgBackupStore's retention/downsample policy (§3): this
// run's snapshots and events older than pruneMaxAge are deleted, then surviving
// snapshots are bucketed (10-minute buckets up to pruneMidWindow old, 1-day
// buckets beyond) with only the newest row per bucket retained.
func (f *FakePg) PruneSnapshots(_ context.Context, run core.RunID, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if last, ok := f.lastPrune[run]; ok && now.Before(last.Add(pruneInterval)) {
		return nil
	}
	f.lastPrune[run] = now
	f.pruneCalls++
	if f.FailPruneSnapshots != nil {
		return f.FailPruneSnapshots
	}
	recentCutoff := now.Add(-pruneKeepAllWindow)
	coarseCutoff := now.Add(-pruneMidWindow)
	retentionCutoff := now.Add(-pruneMaxAge)

	keptEvents := f.events[:0]
	for _, row := range f.events {
		if row.RunID == run && row.CreatedAt.Before(retentionCutoff) {
			continue
		}
		keptEvents = append(keptEvents, row)
	}
	f.events = keptEvents

	type bucketKey struct{ band, bucket int64 }
	keep := make(map[bucketKey]int) // bucket → index of the current survivor
	prune := make(map[int]bool)
	for i, row := range f.snapshots {
		if row.RunID == run && row.CreatedAt.Before(retentionCutoff) {
			prune[i] = true
			continue
		}
		if row.RunID != run || !row.CreatedAt.Before(recentCutoff) {
			continue // other run, or inside the keep-all window
		}
		key := bucketKey{band: 0, bucket: row.CreatedAt.Unix() / int64(pruneMidBucket/time.Second)}
		if row.CreatedAt.Before(coarseCutoff) {
			key = bucketKey{band: 1, bucket: row.CreatedAt.Unix() / int64(pruneCoarseBucket/time.Second)}
		}
		j, ok := keep[key]
		if !ok {
			keep[key] = i
			continue
		}
		best := f.snapshots[j]
		// Newest CreatedAt wins; equal timestamps break by Tick, then the later
		// insertion (higher row id) wins — same ordering as pruneBandSQL.
		if row.CreatedAt.After(best.CreatedAt) ||
			(row.CreatedAt.Equal(best.CreatedAt) && row.Tick >= best.Tick) {
			prune[j] = true
			keep[key] = i
		} else {
			prune[i] = true
		}
	}
	if len(prune) == 0 {
		return nil
	}
	out := f.snapshots[:0]
	for i, row := range f.snapshots {
		if !prune[i] {
			out = append(out, row)
		}
	}
	f.snapshots = out
	return nil
}

// ResetRunData mirrors PgBackupStore: the run's snapshots + events rows are
// deleted AND the runs record is replaced with r, all-or-nothing — an injected
// failure changes NOTHING (the transaction rollback contract, including the
// runs-row metadata). The regen "new map" mandatory step.
func (f *FakePg) ResetRunData(_ context.Context, r RunRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailResetRunData != nil {
		return f.FailResetRunData
	}
	snaps := f.snapshots[:0]
	for _, row := range f.snapshots {
		if row.RunID != r.RunID {
			snaps = append(snaps, row)
		}
	}
	f.snapshots = snaps
	evs := f.events[:0]
	for _, row := range f.events {
		if row.RunID != r.RunID {
			evs = append(evs, row)
		}
	}
	f.events = evs
	f.runs[string(r.RunID)] = r
	return nil
}

// ── Test helpers ───────────────────────────────────────────────────────────────

// PruneCallCount returns how many times PruneSnapshots was invoked.
func (f *FakePg) PruneCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pruneCalls
}

// SeedEvents inserts event rows directly (test seeding; production events land
// only through WriteBackup).
func (f *FakePg) SeedEvents(run core.RunID, evs []core.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range evs {
		f.events = append(f.events, pgEventRow{RunID: run, Event: e})
	}
}

// SnapshotRecord is the test-visible view of one stored snapshots row.
type SnapshotRecord struct {
	RunID        core.RunID
	Tick         core.Tick
	CreatedAt    time.Time
	LastEventSeq *int64
}

// SnapshotRecords returns the stored snapshot rows for a run in insertion order
// (blob omitted — tests assert on tick/created_at/last_event_seq).
func (f *FakePg) SnapshotRecords(run core.RunID) []SnapshotRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []SnapshotRecord
	for _, row := range f.snapshots {
		if row.RunID == run {
			out = append(out, SnapshotRecord{
				RunID: row.RunID, Tick: row.Tick,
				CreatedAt: row.CreatedAt, LastEventSeq: row.LastEventSeq,
			})
		}
	}
	return out
}

// RunOf returns the stored RunRecord for a runID, or zero value if absent.
func (f *FakePg) RunOf(runID core.RunID) (RunRecord, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.runs[string(runID)]
	return r, ok
}

// SnapshotCount returns how many snapshot rows exist for a given run.
func (f *FakePg) SnapshotCount(run core.RunID) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var count int
	for _, row := range f.snapshots {
		if row.RunID == run {
			count++
		}
	}
	return count
}

// EventCount returns how many event rows exist for a given run.
func (f *FakePg) EventCount(run core.RunID) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var count int
	for _, row := range f.events {
		if row.RunID == run {
			count++
		}
	}
	return count
}

// EventsOf returns the stored events for a run in insertion order.
func (f *FakePg) EventsOf(run core.RunID) []core.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []core.Event
	for _, row := range f.events {
		if row.RunID == run {
			out = append(out, row.Event)
		}
	}
	return out
}

// EventTypes returns the sorted event types for a given run.
func (f *FakePg) EventTypes(run core.RunID) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	typesSet := make(map[string]bool)
	for _, row := range f.events {
		if row.RunID == run {
			typesSet[row.Event.Type] = true
		}
	}
	types := make([]string, 0, len(typesSet))
	for t := range typesSet {
		types = append(types, t)
	}
	sort.Strings(types)
	return types
}
