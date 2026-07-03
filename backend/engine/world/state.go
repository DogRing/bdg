package world

import (
	"sort"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/mind/planner"
	"github.com/dogring/bdg/engine/mind/stats"
	"github.com/dogring/bdg/engine/mind/tom"
	"github.com/dogring/bdg/engine/space/spatial"
)

// ── WorldState: serializable snapshot for resume (testing.md §1) ────────────────

// WorldState is a public, copyable snapshot of the world's mutable state at one
// tick. Captures tick counter, root RNG state, every agent's full public state
// (incl. ToM[self] means), every object, per-agent known-object sets, and the
// P6 emerged-roles set.
// JSON field names are the snake_case data-contracts (§1) shape consumed by the
// read API / god-view; the engine's resume path uses the same struct in-memory.
type WorldState struct {
	Tick         core.Tick          `json:"tick"`
	RNGState     rng.RNGState       `json:"rng_state"`
	Agents       []agentDigest      `json:"agents"`
	Objects      []objectRecord     `json:"objects"`
	Known        []knownDigest      `json:"known"`         // sorted by AgentID (D12)
	EmergedRoles []emergedRoleEntry `json:"emerged_roles"` // sorted by Function (D12)
	// TomDigest is the cross-agent ToM projection for the god-view (D6/D8): per
	// observer, per subject, the believed per-stat distribution + reliance edges.
	// Capture-only — NOT consumed by RestoreState (the running sim rebuilds beliefs);
	// it exists so /api/god/* can expose real≠self≠others without a single scalar (D6).
	TomDigest map[core.AgentID]map[core.AgentID]tomDigestEntry `json:"tom_digest,omitempty"`

	// ── Env state (WI-P4, data-contracts §10) ──────────────────────────────────
	// Flora/Animals/Climate are the periodic-full sources for the env subsystems.
	// omitempty ⇒ ABSENT (not just empty) when env is OFF (InstallEnv/InstallFauna
	// not called), so a pre-WI-P4 env-off snapshot stays byte-identical (additive
	// change, no SchemaVersion bump — see persist/SPEC-world.md Open Questions).
	// Derived views (stage from length, active/dormant from active_until) are NOT
	// stored (D9); the scent grid is NOT stored (derived, rebuilt on restore from
	// emitter positions — see RestoreState).
	Flora   []floraDigest       `json:"flora,omitempty"`
	Animals []animalStateDigest `json:"animals,omitempty"`
	Climate *climateDigest      `json:"climate,omitempty"`
}

// emergedRoleEntry is a single (function → holder) record for serialization.
type emergedRoleEntry struct {
	Function core.Function `json:"function"`
	Holder   core.AgentID  `json:"holder"`
}

// tomDigestEntry is one observer's belief about one subject (god-view projection).
type tomDigestEntry struct {
	EstStats map[core.StatID]tom.StatDist `json:"est_stats,omitempty"`
	RelyOn   map[core.Function]float64    `json:"rely_on,omitempty"`
}

// agentDigest is one agent's full public state for capture/restore.
type agentDigest struct {
	ID              core.AgentID                 `json:"id"`
	Pos             core.Vec2                    `json:"pos"`
	RealStats       stats.Stats                  `json:"real_stats"`
	Stamina         float64                      `json:"stamina"`
	Mood            float64                      `json:"mood"`
	Adrenaline      float64                      `json:"adrenaline"`
	NeedIntensities map[core.Dimension]float64   `json:"need_intensities"`
	Inventory       map[core.Tag]int             `json:"inventory"`
	Goal            core.Dimension               `json:"goal"`
	PlanActions     []string                     `json:"plan_actions"` // ActionIDs in plan (serialized)
	PlanHorizon     int                          `json:"plan_horizon"`
	PlanIdx         int                          `json:"plan_idx"`
	Elapsed         core.GameMinutes             `json:"elapsed"`
	Coping          agent.CopingState            `json:"coping"`
	Latent          []agent.LatentGoal           `json:"latent"`
	SelfEstStats    map[core.StatID]tom.StatDist `json:"self_est_stats"`
	AgentCfg        agent.Config                 `json:"agent_cfg"`
}

// knownDigest carries one agent's known-object set.
type knownDigest struct {
	AgentID core.AgentID        `json:"agent_id"`
	Objects []agent.KnownObject `json:"objects"` // sorted by ObjectID (D12)
}

// ── State() — capture ──────────────────────────────────────────────────────────

// State returns a serializable snapshot of the world's current mutable state.
func (w *World) State() WorldState {
	ws := WorldState{
		Tick:     w.tick,
		RNGState: w.rootRNG.State(),
	}

	for _, agentID := range w.agentIDs {
		a := w.agents[agentID]
		d := agentDigest{
			ID:              a.ID,
			Pos:             a.Pos,
			RealStats:       a.RealStats.Clone(),
			Stamina:         a.Stamina,
			Mood:            a.Mood,
			Adrenaline:      a.Adrenaline,
			NeedIntensities: cloneDimMap(a.NeedIntensities),
			Inventory:       cloneTagMap(a.Inventory),
			Goal:            a.Goal,
			PlanActions:     actionIDsToStrings(a.Plan.Actions),
			PlanHorizon:     a.Plan.Horizon,
			PlanIdx:         a.PlanIdx,
			Elapsed:         a.Elapsed,
			Coping:          a.Coping,
			Latent:          cloneLatents(a.Latent),
			AgentCfg:        a.Cfg,
		}
		if selfBelief, ok := a.ToM.Self(a.ToM.SelfID()); ok {
			d.SelfEstStats = cloneSDists(selfBelief.EstStats)
		}
		ws.Agents = append(ws.Agents, d)
	}

	for _, objID := range w.objectIDs {
		ws.Objects = append(ws.Objects, w.objects[objID])
	}

	for _, agentID := range w.agentIDs {
		known := w.knownObjects[agentID]
		var kos []agent.KnownObject
		objIDs := make([]core.ObjectID, 0, len(known))
		for oid := range known {
			objIDs = append(objIDs, oid)
		}
		sort.Slice(objIDs, func(i, j int) bool { return string(objIDs[i]) < string(objIDs[j]) })
		for _, oid := range objIDs {
			kos = append(kos, known[oid])
		}
		ws.Known = append(ws.Known, knownDigest{AgentID: agentID, Objects: kos})
	}

	// Serialize emerged roles in sorted Function order (D12).
	for _, fn := range sortedFunctionKeys(w.emergedRoles) {
		ws.EmergedRoles = append(ws.EmergedRoles, emergedRoleEntry{
			Function: fn,
			Holder:   w.emergedRoles[fn],
		})
	}

	// Cross-agent ToM projection for the god-view (capture-only; see WorldState.TomDigest).
	// Iterate agents in fixed ID order (D12); each agent's ToM is its beliefs about every
	// known subject (incl. self). JSON map-key sort makes the encoded blob deterministic.
	td := make(map[core.AgentID]map[core.AgentID]tomDigestEntry, len(w.agentIDs))
	for _, observerID := range w.agentIDs {
		obs := w.agents[observerID]
		subjects := obs.ToM.Subjects()
		inner := make(map[core.AgentID]tomDigestEntry, len(subjects))
		for _, subj := range subjects {
			belief, ok := obs.ToM.Self(subj)
			if !ok {
				continue
			}
			inner[subj] = tomDigestEntry{
				EstStats: cloneSDists(belief.EstStats),
				RelyOn:   cloneRelyOn(belief.RelyOn),
			}
		}
		if len(inner) > 0 {
			td[observerID] = inner
		}
	}
	if len(td) > 0 {
		ws.TomDigest = td
	}

	// ── Env state capture (WI-P4; see state_env.go) ───────────────────────────
	w.captureEnvState(&ws)

	return ws
}

// ── RestoreState() — restore ───────────────────────────────────────────────────

// RestoreState overwrites the world's mutable state from a previously-captured
// WorldState. The spatial hash is rebuilt from agent/object positions.
func (w *World) RestoreState(ws WorldState) {
	w.tick = ws.Tick
	w.rootRNG.Restore(ws.RNGState)

	w.agents = make(map[core.AgentID]*agent.Agent, len(ws.Agents))
	w.agentIDs = make([]core.AgentID, 0, len(ws.Agents))
	for _, d := range ws.Agents {
		// Reconstruct ToM with the real stats registry so Observe/GossipUpdate work.
		restoredToM := tom.NewToM(d.ID, d.RealStats.Clone(), 0.5, rng.New(0), w.svc.Stats, d.AgentCfg.Rates)
		if d.SelfEstStats != nil {
			restoredToM.SetSelfStats(cloneSDists(d.SelfEstStats))
		}
		a := agent.New(d.ID, d.Pos, d.RealStats.Clone(), restoredToM, d.AgentCfg)
		a.Stamina = d.Stamina
		a.Mood = d.Mood
		a.Adrenaline = d.Adrenaline
		a.NeedIntensities = cloneDimMap(d.NeedIntensities)
		a.Inventory = cloneTagMap(d.Inventory)
		a.Goal = d.Goal
		a.Plan = planner.Plan{Actions: stringsToActionIDs(d.PlanActions), Horizon: d.PlanHorizon}
		a.PlanIdx = d.PlanIdx
		a.Elapsed = d.Elapsed
		a.Coping = d.Coping
		a.Latent = cloneLatents(d.Latent)

		w.agents[d.ID] = a
		w.agentIDs = append(w.agentIDs, d.ID)
	}

	w.objects = make(map[core.ObjectID]objectRecord, len(ws.Objects))
	w.objectIDs = make([]core.ObjectID, 0, len(ws.Objects))
	for _, obj := range ws.Objects {
		w.objects[obj.ID] = obj
		w.objectIDs = append(w.objectIDs, obj.ID)
	}

	// ── Env state restore (WI-P4) ──────────────────────────────────────────────
	// Must run BEFORE the spatial-hash rebuild below so restored animals are
	// included in it. The caller is expected to have already called InstallEnv/
	// InstallFauna with the SAME content-derived Rules/Config as the captured run
	// (Rules are not part of the blob — only dynamic state is, mirroring how
	// agents' RealStats registry (w.svc.Stats) is assumed pre-installed above).
	w.restoreFlora(ws.Flora)
	w.restoreAnimals(ws.Animals)
	w.restoreClimate(ws.Climate)

	rebuildSpatialHash(w.spatial, w.agentIDs, w.agents, w.objects, w.animalIDs, w.animals)

	w.knownObjects = make(map[core.AgentID]map[core.ObjectID]agent.KnownObject)
	for _, kd := range ws.Known {
		m := make(map[core.ObjectID]agent.KnownObject, len(kd.Objects))
		for _, ko := range kd.Objects {
			m[ko.ID] = ko
		}
		w.knownObjects[kd.AgentID] = m
	}

	// Restore emerged roles.
	w.emergedRoles = make(map[core.Function]core.AgentID, len(ws.EmergedRoles))
	for _, e := range ws.EmergedRoles {
		w.emergedRoles[e.Function] = e.Holder
	}

	// The scent grid is NOT serialized (derived, data-contracts §10) — rebuild it
	// from the just-restored emitter positions so the next tick's fauna reads
	// match the uninterrupted run (mirrors the spatial-hash rebuild above).
	w.rebuildScent(ws.Tick)

	w.currentSounds = nil
	w.currentSnap = nil
}

// ── Helpers ────────────────────────────────────────────────────────────────────

func rebuildSpatialHash(sh *spatial.SpatialHash, agentIDs []core.AgentID, agents map[core.AgentID]*agent.Agent, objects map[core.ObjectID]objectRecord, animalIDs []core.ObjectID, animals map[core.ObjectID]*fauna.Animal) {
	// Clear and rebuild: remove all, then re-insert.
	for _, id := range agentIDs {
		sh.Remove(core.ObjectID(id))
	}
	for _, obj := range objects {
		sh.Remove(obj.ID)
	}
	for _, id := range animalIDs {
		sh.Remove(id)
	}
	for _, id := range agentIDs {
		a := agents[id]
		sh.Insert(core.ObjectID(id), a.Pos)
	}
	for _, obj := range objects {
		sh.Insert(obj.ID, obj.Pos)
	}
	for _, id := range animalIDs {
		if a := animals[id]; a != nil {
			sh.Insert(id, a.Pos)
		}
	}
}

func cloneDimMap(m map[core.Dimension]float64) map[core.Dimension]float64 {
	if m == nil {
		return nil
	}
	out := make(map[core.Dimension]float64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneTagMap(m map[core.Tag]int) map[core.Tag]int {
	if m == nil {
		return nil
	}
	out := make(map[core.Tag]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneLatents(src []agent.LatentGoal) []agent.LatentGoal {
	if src == nil {
		return nil
	}
	out := make([]agent.LatentGoal, len(src))
	copy(out, src)
	return out
}

func cloneSDists(src map[core.StatID]tom.StatDist) map[core.StatID]tom.StatDist {
	if src == nil {
		return nil
	}
	out := make(map[core.StatID]tom.StatDist, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func cloneRelyOn(src map[core.Function]float64) map[core.Function]float64 {
	if len(src) == 0 {
		return nil
	}
	out := make(map[core.Function]float64, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func actionIDsToStrings(ids []actions.ActionID) []string {
	out := make([]string, len(ids))
	for i, a := range ids {
		out[i] = string(a)
	}
	return out
}

func stringsToActionIDs(strs []string) []actions.ActionID {
	out := make([]actions.ActionID, len(strs))
	for i, s := range strs {
		out[i] = actions.ActionID(s)
	}
	return out
}

// sortedFunctionKeys returns the keys of m sorted lexicographically (D12).
func sortedFunctionKeys(m map[core.Function]core.AgentID) []core.Function {
	keys := make([]core.Function, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return string(keys[i]) < string(keys[j])
	})
	return keys
}
