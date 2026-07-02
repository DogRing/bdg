// Package world is the simulation orchestrator (L6). It owns all global mutable
// state — agents, objects, the spatial index, the Tick counter, the root RNG —
// and drives the deterministic tick loop in read→plan→collect→apply order (D12).
// It implements agent.WorldView so agents perceive and plan against a frozen
// per-tick snapshot. It emits observability events through an injected
// core.EventEmitter and never touches IO.
package world

import (
	"sort"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/env/climate"
	"github.com/dogring/bdg/engine/env/decay"
	"github.com/dogring/bdg/engine/env/flora"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/kernel/worldtime"
	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/mind/perception"
	"github.com/dogring/bdg/engine/mind/tom"
	"github.com/dogring/bdg/engine/space/navmap"
	"github.com/dogring/bdg/engine/space/scent"
	"github.com/dogring/bdg/engine/space/spatial"
)

// ── Config ─────────────────────────────────────────────────────────────────────

// Config bundles the world-level tunables, injected by the caller (read from
// content/balance.yaml world.* via platform/config). The world hardcodes NO
// numeric constant (D10).
type Config struct {
	SpatialHashCell          float64
	RoleConvergenceThreshold float64 // balance.yaml politics.role_convergence_threshold; supersedes the retired world.reliance_threshold
	OutcomeDifficultyBase    float64
	BackupEveryTicks         int
	MoveSpeedPerTick         float64 // fraction of remaining distance covered per tick (0,1]
	ArrivalEpsilon           float64 // locomotion (MoveTo/Approach) completes when within this distance of Intent.Move
	PlanInterval             int     // plan_interval: agents per planning slice; 1 = all agents plan every tick. Higher spreads planner load across ticks.
	PruneThreshold           int     // prune_threshold: max ticks since LastSeen before ToM beliefs are pruned; 0 = never prune.
}

// DefaultConfig returns the canonical Config from content/balance.yaml world.*.
func DefaultConfig() Config {
	return Config{
		SpatialHashCell:          8.0,
		RoleConvergenceThreshold: 0.5,
		OutcomeDifficultyBase:    50.0,
		BackupEveryTicks:         60,
		MoveSpeedPerTick:         0.5,
		ArrivalEpsilon:           1.0,
		PlanInterval:             1,
		PruneThreshold:           0,
	}
}

// ── Services ───────────────────────────────────────────────────────────────────

// Services are the read-only, shared services every agent borrows each Tick.
type Services = agent.Services

// ── Object record ──────────────────────────────────────────────────────────────

// objectRecord is one placed object in the world. Carries supply only (D9).
// JSON keys are snake_case so the render snapshot matches the frontend / data-contracts §1.
type objectRecord struct {
	ID     core.ObjectID              `json:"id"`
	Kind   core.Tag                   `json:"kind"`
	Pos    core.Vec2                  `json:"pos"`
	Supply map[core.Dimension]float64 `json:"supply"`
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

	envCfg       EnvConfig
	nav          *navmap.NavMap
	climateState *climate.State
	climateRules *climate.Rules
	floraState   *flora.State
	floraRules   *flora.Rules
	decayState   *decay.State
	decayRules   *decay.Rules

	terrainAttrs     map[navmap.TerrainID]map[core.Tag]float64
	decayLotPos      map[core.ObjectID]core.Vec2
	decayStorageMult map[core.ObjectID]float64
	nextObjectSeq    int64

	animals       map[core.ObjectID]*fauna.Animal
	animalIDs     []core.ObjectID
	faunaRules    *fauna.Rules
	scent         *scent.Grid
	scentEmitters map[core.Tag][]core.Tag
	coverKinds    map[core.Tag]bool
	nextAnimalSeq int64

	// Respawn (F9 timer-respawn-to-target): optional; zero cadence ⇒ off. Templates + anchors are
	// per-run (fixture, injected by worldgen); targets are content; cadence is balance.
	respawnTemplates map[core.Tag]fauna.Animal
	respawnTargets   map[core.Tag]int
	respawnAnchors   map[core.Tag]core.Vec2
	respawnCadence   core.Tick

	// Current tick state (built fresh each Tick).
	currentSounds []perception.SoundEvent
	currentSnap   *WorldSnapshot

	// P2 trade protocol: pending offer indexed by the intended receiver.
	pendingOffers map[core.AgentID]pendingOffer

	// P3 resentment: conflict losers from the PREVIOUS tick's apply phase,
	// keyed by loser AgentID → the winner(s) who beat them over a shared
	// resource. Populated at the end of each Tick's apply phase and read by
	// agents in the next tick's plan phase (WorldView.ResentmentTriggers).
	// Transient per-tick buffer, like currentSounds.
	resentmentTriggers map[core.AgentID][]core.AgentID

	// P6 emerged roles: last-known holder per Function. Used by relianceScan
	// for rising-edge detection (emits RoleEmerged only when a (function,holder)
	// pair first crosses the threshold). Owned state, serialized with the world
	// (so resume stays byte-identical). DO NOT rename to Role — D2 forbids a
	// role type; this is a cluster statistic.
	emergedRoles map[core.Function]core.AgentID

	// P6: pending signals collected during the apply phase, keyed by the
	// intended receiver agent ID. Populated by applySignal for SignalVote and
	// read by the next tick's snapshot IncomingSignals(). Cleared at the start
	// of each tick.
	pendingSignals map[core.AgentID][]core.Signal
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
		tick:               0,
		agents:             make(map[core.AgentID]*agent.Agent),
		agentIDs:           nil,
		objects:            make(map[core.ObjectID]objectRecord),
		objectIDs:          nil,
		knownObjects:       make(map[core.AgentID]map[core.ObjectID]agent.KnownObject),
		spatial:            spatial.New(cfg.SpatialHashCell),
		rootRNG:            root,
		clock:              clock,
		cfg:                cfg,
		svc:                svc,
		actReg:             actReg,
		emit:               emit,
		terrainAttrs:       make(map[navmap.TerrainID]map[core.Tag]float64),
		decayLotPos:        make(map[core.ObjectID]core.Vec2),
		decayStorageMult:   make(map[core.ObjectID]float64),
		pendingOffers:      make(map[core.AgentID]pendingOffer),
		resentmentTriggers: make(map[core.AgentID][]core.AgentID),
		emergedRoles:       make(map[core.Function]core.AgentID),
		pendingSignals:     make(map[core.AgentID][]core.Signal),
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
