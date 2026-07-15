package persist

import (
	"context"
	"encoding/json"
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
	animals   map[string]string    // key → JSON of AnimalView (WI-P4)
	flora     map[string]string    // key → JSON of []FloraView (WI-P4)
	climate   map[string]string    // key → JSON of ClimateView (WI-P4)
	terrain   map[string]string    // key → JSON of TerrainView (WI-P4)
	meta      map[string]string    // key → meta hash fields (stored as flat key:field)
	expired   bool                 // true after Expire called
}

func NewFakeRedis() *FakeRedis {
	return &FakeRedis{
		ticks:     make(map[string]core.Tick),
		snapshots: make(map[string][]byte),
		agents:    make(map[string]string),
		animals:   make(map[string]string),
		flora:     make(map[string]string),
		climate:   make(map[string]string),
		terrain:   make(map[string]string),
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

func (f *FakeRedis) WriteAnimal(_ context.Context, run core.RunID, v AnimalView) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	blob, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f.animals[Keyer{Run: run}.Animal(v.ID)] = string(blob)
	return nil
}

func (f *FakeRedis) WriteFlora(_ context.Context, run core.RunID, v FloraDoc) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v.Flora == nil {
		v.Flora = []FloraView{}
	}
	blob, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f.flora[Keyer{Run: run}.Flora()] = string(blob)
	return nil
}

func (f *FakeRedis) WriteClimate(_ context.Context, run core.RunID, v ClimateView) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	blob, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f.climate[Keyer{Run: run}.Climate()] = string(blob)
	return nil
}

func (f *FakeRedis) WriteTerrain(_ context.Context, run core.RunID, v TerrainView) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	blob, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f.terrain[Keyer{Run: run}.Terrain()] = string(blob)
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

// PublishWorldRevision mirrors RedisLiveStore: one write of the
// {world_revision, terrain} publication fields onto the meta hash; InitMeta
// never touches them.
func (f *FakeRedis) PublishWorldRevision(_ context.Context, run core.RunID, rev int64, terrainOn bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	keyer := Keyer{Run: run}
	terrain := "off"
	if terrainOn {
		terrain = "on"
	}
	f.meta[keyer.Meta()+":world_revision"] = fmt.Sprintf("%d", rev)
	f.meta[keyer.Meta()+":terrain"] = terrain
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
	delete(f.flora, keyer.Flora())
	delete(f.climate, keyer.Climate())
	delete(f.terrain, keyer.Terrain())
	// Remove agent/animal keys (per-id, matched by prefix).
	for k := range f.agents {
		prefix := fmt.Sprintf("sim:%s:agent:", run)
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(f.agents, k)
		}
	}
	for k := range f.animals {
		prefix := fmt.Sprintf("sim:%s:animal:", run)
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(f.animals, k)
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

// AnimalViewOf returns the stored animal view JSON for a given run+animal, or "" if absent.
func (f *FakeRedis) AnimalViewOf(run core.RunID, id core.ObjectID) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.animals[Keyer{Run: run}.Animal(id)]
}

// FloraOf returns the stored sim:{run}:flora JSON blob, or "" if absent.
func (f *FakeRedis) FloraOf(run core.RunID) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.flora[Keyer{Run: run}.Flora()]
}

// ClimateOf returns the stored sim:{run}:climate JSON blob, or "" if absent.
func (f *FakeRedis) ClimateOf(run core.RunID) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.climate[Keyer{Run: run}.Climate()]
}

// TerrainOf returns the stored sim:{run}:terrain JSON blob, or "" if absent.
func (f *FakeRedis) TerrainOf(run core.RunID) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.terrain[Keyer{Run: run}.Terrain()]
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
