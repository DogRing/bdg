package persist

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/dogring/bdg/engine/core"
)

// FakePg is an in-memory stub implementing BackupStore for unit tests.
type FakePg struct {
	mu        sync.Mutex
	runs      map[string]RunRecord
	snapshots []pgSnapshotRow // ordered by insertion
	events    []pgEventRow    // ordered by insertion
}

type pgSnapshotRow struct {
	RunID core.RunID
	Tick  core.Tick
	Blob  []byte
}

type pgEventRow struct {
	RunID core.RunID
	Event core.Event
}

func NewFakePg() *FakePg {
	return &FakePg{
		runs: make(map[string]RunRecord),
	}
}

func (f *FakePg) UpsertRun(_ context.Context, r RunRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs[string(r.RunID)] = r
	return nil
}

func (f *FakePg) WriteSnapshot(_ context.Context, run core.RunID, tick core.Tick, blob []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshots = append(f.snapshots, pgSnapshotRow{
		RunID: run,
		Tick:  tick,
		Blob:  cloneBytes(blob),
	})
	return nil
}

func (f *FakePg) WriteEvents(_ context.Context, run core.RunID, evs []core.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range evs {
		f.events = append(f.events, pgEventRow{RunID: run, Event: e})
	}
	return nil
}

func (f *FakePg) LatestSnapshot(_ context.Context, run core.RunID) (core.Tick, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Find the highest tick for this run.
	var bestTick core.Tick = -1
	var bestBlob []byte
	for _, row := range f.snapshots {
		if row.RunID == run && row.Tick > bestTick {
			bestTick = row.Tick
			bestBlob = row.Blob
		}
	}
	if bestTick < 0 {
		return 0, nil, fmt.Errorf("fake: no snapshots for run %s", run)
	}
	return bestTick, cloneBytes(bestBlob), nil
}

// ── Test helpers ───────────────────────────────────────────────────────────────

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
