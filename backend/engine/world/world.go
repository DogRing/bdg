// Package world is the simulation orchestrator (L6). It owns all global mutable
// state — agents, objects, the spatial index, the Tick counter, the root RNG —
// and drives the deterministic tick loop in read→plan→collect→apply order (D12).
// It implements agent.WorldView so agents perceive and plan against a frozen
// per-tick snapshot. It emits observability events through an injected
// core.EventEmitter and never touches IO.
package world

import (
	"sort"

	"github.com/dogring/bdg/engine/actions"
	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/perception"
	"github.com/dogring/bdg/engine/rng"
	"github.com/dogring/bdg/engine/spatial"
	"github.com/dogring/bdg/engine/tom"
	"github.com/dogring/bdg/engine/worldtime"
)

// ── Config ─────────────────────────────────────────────────────────────────────

// Config bundles the world-level tunables, injected by the caller (read from
// content/balance.yaml world.* via platform/config). The world hardcodes NO
// numeric constant (D10).
type Config struct {
	SpatialHashCell       float64
	RelianceThreshold     float64
	OutcomeDifficultyBase float64
	BackupEveryTicks      int
	MoveSpeedPerTick      float64 // fraction of remaining distance covered per tick (0,1]
}

// DefaultConfig returns the canonical Config from content/balance.yaml world.*.
func DefaultConfig() Config {
	return Config{
		SpatialHashCell:       8.0,
		RelianceThreshold:     0.5,
		OutcomeDifficultyBase: 50.0,
		BackupEveryTicks:      60,
		MoveSpeedPerTick:      0.5,
	}
}

// ── Services ───────────────────────────────────────────────────────────────────

// Services are the read-only, shared services every agent borrows each Tick.
type Services = agent.Services

// ── Object record ──────────────────────────────────────────────────────────────

// objectRecord is one placed object in the world. Carries supply only (D9).
type objectRecord struct {
	ID     core.ObjectID
	Kind   core.Tag
	Pos    core.Vec2
	Supply map[core.Dimension]float64
}

// ── World ──────────────────────────────────────────────────────────────────────

// World is the global mutable simulation state and the tick driver.
type World struct {
	tick     core.Tick
	agents   map[core.AgentID]*agent.Agent
	agentIDs []core.AgentID // sorted lexicographically (D12)

	objects   map[core.ObjectID]objectRecord
	objectIDs []core.ObjectID // sorted lexicographically (D12)

	// Per-agent known objects (objects each agent has ever perceived).
	knownObjects map[core.AgentID]map[core.ObjectID]agent.KnownObject

	spatial *spatial.SpatialHash

	rootRNG *rng.RNG
	clock   worldtime.Clock
	cfg     Config
	svc     Services
	actReg  *actions.Registry
	emit    core.EventEmitter

	// Current tick state (built fresh each Tick).
	currentSounds []perception.SoundEvent
	currentSnap   *WorldSnapshot

	// P2 trade protocol: pending offer indexed by the intended receiver.
	pendingOffers map[core.AgentID]pendingOffer
}

// pendingOffer records an Offer signal awaiting response from the receiver.
type pendingOffer struct {
	Offeror      core.AgentID
	ClaimedValue float64
	Truth        float64
	Tick         core.Tick
}

// New builds an empty world.
func New(
	cfg Config,
	clock worldtime.Clock,
	root *rng.RNG,
	svc Services,
	actReg *actions.Registry,
	emit core.EventEmitter,
) *World {
	w := &World{
		tick:          0,
		agents:        make(map[core.AgentID]*agent.Agent),
		agentIDs:      nil,
		objects:       make(map[core.ObjectID]objectRecord),
		objectIDs:     nil,
		knownObjects:  make(map[core.AgentID]map[core.ObjectID]agent.KnownObject),
		spatial:       spatial.New(cfg.SpatialHashCell),
		rootRNG:       root,
		clock:         clock,
		cfg:           cfg,
		svc:           svc,
		actReg:        actReg,
		emit:          emit,
		pendingOffers: make(map[core.AgentID]pendingOffer),
	}
	return w
}

// ── Accessors ──────────────────────────────────────────────────────────────────

// CurrentTick returns the world's authoritative tick counter.
func (w *World) CurrentTick() core.Tick { return w.tick }

// AgentIDs returns every agent id in canonical sorted order (D12).
func (w *World) AgentIDs() []core.AgentID {
	out := make([]core.AgentID, len(w.agentIDs))
	copy(out, w.agentIDs)
	return out
}

// AgentOf returns the live agent for id and whether it exists.
func (w *World) AgentOf(id core.AgentID) (*agent.Agent, bool) {
	a, ok := w.agents[id]
	return a, ok
}

// Snapshot returns the read-only per-tick WorldView.
func (w *World) Snapshot() *WorldSnapshot { return w.currentSnap }

// ── Generation ─────────────────────────────────────────────────────────────────

// Spawn samples RealStats, builds ToM[self], constructs the agent, and registers it.
func (w *World) Spawn(id core.AgentID, pos core.Vec2, agentCfg agent.Config, rng *rng.RNG) *agent.Agent {
	// Sample RealStats in stats.IDs() order (D12).
	realStats := sampleRealStats(w.svc.Stats, rng)

	// Build ToM[self] with calibrated noise (D8).
	selfPerception := 0.5 // P1: mid-range
	selfToM := tom.NewToM(id, realStats, selfPerception, rng, w.svc.Stats, agentCfg.Rates)

	// Construct the agent.
	a := agent.New(id, pos, realStats, selfToM, agentCfg)

	// Register.
	w.agents[id] = a
	w.agentIDs = append(w.agentIDs, id)
	sort.Slice(w.agentIDs, func(i, j int) bool {
		return string(w.agentIDs[i]) < string(w.agentIDs[j])
	})

	// Insert into spatial index.
	w.spatial.Insert(core.ObjectID(id), pos)

	// Initialize known objects.
	w.knownObjects[id] = make(map[core.ObjectID]agent.KnownObject)
	for _, objID := range w.objectIDs {
		obj := w.objects[objID]
		w.knownObjects[id][objID] = agent.KnownObject{
			ID: obj.ID, Pos: obj.Pos, Kind: obj.Kind, Supply: obj.Supply,
		}
	}

	return a
}

// PlaceObject inserts an object. Idempotent on id.
func (w *World) PlaceObject(id core.ObjectID, kind core.Tag, pos core.Vec2, supply map[core.Dimension]float64) {
	obj := objectRecord{ID: id, Kind: kind, Pos: pos, Supply: supply}

	if _, exists := w.objects[id]; !exists {
		w.objectIDs = append(w.objectIDs, id)
		sort.Slice(w.objectIDs, func(i, j int) bool {
			return string(w.objectIDs[i]) < string(w.objectIDs[j])
		})
	}
	w.objects[id] = obj
	w.spatial.Insert(id, pos)

	ko := agent.KnownObject{ID: id, Pos: pos, Kind: kind, Supply: supply}
	for _, agentID := range w.agentIDs {
		if w.knownObjects[agentID] == nil {
			w.knownObjects[agentID] = make(map[core.ObjectID]agent.KnownObject)
		}
		w.knownObjects[agentID][id] = ko
	}
}

// RemoveObject deletes an object. No-op if absent.
func (w *World) RemoveObject(id core.ObjectID) {
	if _, exists := w.objects[id]; !exists {
		return
	}
	delete(w.objects, id)
	w.spatial.Remove(id)
	for i, oid := range w.objectIDs {
		if oid == id {
			w.objectIDs = append(w.objectIDs[:i], w.objectIDs[i+1:]...)
			break
		}
	}
	for _, agentID := range w.agentIDs {
		if known, ok := w.knownObjects[agentID]; ok {
			delete(known, id)
		}
	}
}
