package persist

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/dogring/bdg/engine/kernel/core"
)

// FakeRedis is an in-memory stub implementing LiveStore for unit tests.
// All methods are safe for concurrent use but tests run sequentially.
type FakeRedis struct {
	mu        sync.Mutex
	ticks     map[string]core.Tick // key → tick value
	snapshots map[string][]byte    // key → blob
	agents    map[string]string    // key → JSON of AgentView
	meta      map[string]string    // key → meta hash fields (stored as flat key:field)
	expired   bool                 // true after Expire called
}

func NewFakeRedis() *FakeRedis {
	return &FakeRedis{
		ticks:     make(map[string]core.Tick),
		snapshots: make(map[string][]byte),
		agents:    make(map[string]string),
		meta:      make(map[string]string),
	}
}

func (f *FakeRedis) WriteTick(_ context.Context, run core.RunID, tick core.Tick) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	keyer := Keyer{Run: run}
	f.ticks[keyer.Tick()] = tick
	f.meta[keyer.Meta()+":tick"] = fmt.Sprintf("%d", tick)
	return nil
}

func (f *FakeRedis) WriteSnapshot(_ context.Context, run core.RunID, blob []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	keyer := Keyer{Run: run}
	f.snapshots[keyer.SnapshotKey()] = cloneBytes(blob)
	return nil
}

func (f *FakeRedis) WriteAgent(_ context.Context, run core.RunID, v AgentView) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	keyer := Keyer{Run: run}
	// Store as a simple string representation for testing.
	f.agents[keyer.Agent(v.ID)] = fmt.Sprintf(
		`{"id":%q,"pos":{"x":%.4f,"y":%.4f},"goal":%q,"action":%q,"mood":%.4f}`,
		string(v.ID), v.Pos.X, v.Pos.Y, v.Goal, v.Action, v.Mood,
	)
	return nil
}

func (f *FakeRedis) InitMeta(_ context.Context, run core.RunID, m RunMeta) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	keyer := Keyer{Run: run}
	f.meta[keyer.Meta()+":tick"] = fmt.Sprintf("%d", m.Tick)
	f.meta[keyer.Meta()+":schema_version"] = fmt.Sprintf("%d", m.SchemaVersion)
	f.meta[keyer.Meta()+":started_at"] = m.StartedAt
	f.meta[keyer.Meta()+":status"] = m.Status
	return nil
}

func (f *FakeRedis) ReadSnapshot(_ context.Context, run core.RunID) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	keyer := Keyer{Run: run}
	blob, ok := f.snapshots[keyer.SnapshotKey()]
	if !ok {
		return nil, fmt.Errorf("fake: no snapshot for run %s", run)
	}
	return cloneBytes(blob), nil
}

func (f *FakeRedis) Expire(_ context.Context, run core.RunID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expired = true
	// Clear everything for this run.
	keyer := Keyer{Run: run}
	delete(f.ticks, keyer.Tick())
	delete(f.snapshots, keyer.SnapshotKey())
	// Remove agent keys.
	for k := range f.agents {
		// Match prefix sim:{run}:agent:
		prefix := fmt.Sprintf("sim:%s:agent:", run)
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(f.agents, k)
		}
	}
	// Remove meta keys.
	for k := range f.meta {
		if len(k) >= len(keyer.Meta()) && k[:len(keyer.Meta())] == keyer.Meta() {
			delete(f.meta, k)
		}
	}
	return nil
}

// ── Test helpers ───────────────────────────────────────────────────────────────

// TickOf returns the stored tick for a run, or 0 if absent.
func (f *FakeRedis) TickOf(run core.RunID) core.Tick {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ticks[Keyer{Run: run}.Tick()]
}

// SnapshotOf returns the stored snapshot blob for a run, or nil if absent.
func (f *FakeRedis) SnapshotOf(run core.RunID) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloneBytes(f.snapshots[Keyer{Run: run}.SnapshotKey()])
}

// AgentViewOf returns the stored agent view JSON for a given run+agent, or "" if absent.
func (f *FakeRedis) AgentViewOf(run core.RunID, id core.AgentID) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.agents[Keyer{Run: run}.Agent(id)]
}

// Expired returns whether Expire was called.
func (f *FakeRedis) Expired() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.expired
}

// MetaField returns a single meta field value, or "" if absent.
func (f *FakeRedis) MetaField(run core.RunID, field string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.meta[Keyer{Run: run}.Meta()+":"+field]
}

// AgentIDs returns all agent IDs stored for a given run.
func (f *FakeRedis) AgentIDs(run core.RunID) []core.AgentID {
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := fmt.Sprintf("sim:%s:agent:", run)
	var ids []core.AgentID
	for k := range f.agents {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			idStr := k[len(prefix):]
			ids = append(ids, core.AgentID(idStr))
		}
	}
	sort.Slice(ids, func(i, j int) bool { return string(ids[i]) < string(ids[j]) })
	return ids
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
