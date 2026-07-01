package world

import (
	"sort"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/mind/perception"
	"github.com/dogring/bdg/engine/mind/tom"
)

// ── Compile-time interface check ───────────────────────────────────────────────

var _ agent.WorldView = (*WorldSnapshot)(nil)

// ── WorldSnapshot ──────────────────────────────────────────────────────────────

// WorldSnapshot is the world's implementation of agent.WorldView. It is a
// read-only view frozen at the start of phase 1, valid only for the current tick.
type WorldSnapshot struct {
	w    *World
	tick core.Tick

	// frozenActionTags is a per-agent snapshot of the current action's tags,
	// captured at snapshot creation time. This is necessary for thread-safe
	// parallel planning (Phase 2): agent goroutines call resolveTags concurrently
	// and a.Tick writes to a.Plan/Actions, so reading those fields from the
	// shared snapshot would be a data race. Frozen once, read-only thereafter.
	frozenActionTags map[core.AgentID][]core.Tag
}

// newSnapshot creates a new snapshot bound to the current world state.
// It freezes per-agent action tags for thread-safe concurrent access.
func newSnapshot(w *World) *WorldSnapshot {
	s := &WorldSnapshot{w: w, tick: w.tick}

	// Freeze each agent's current action tags.
	s.frozenActionTags = make(map[core.AgentID][]core.Tag, len(w.agents))
	for _, agentID := range w.agentIDs {
		a := w.agents[agentID]
		if a == nil {
			continue
		}
		var tags []core.Tag
		if len(a.Plan.Actions) > 0 && a.PlanIdx < len(a.Plan.Actions) {
			if actDef, ok := w.actReg.Get(a.Plan.Actions[a.PlanIdx]); ok {
				tags = make([]core.Tag, len(actDef.Tags))
				copy(tags, actDef.Tags)
			}
		}
		s.frozenActionTags[agentID] = tags
	}

	return s
}

// ── perception.WorldSnapshot methods ───────────────────────────────────────────

// EntitiesInRadius delegates to the spatial hash, returning entities sorted by
// ascending ObjectID (spatial-hash contract, D12).
func (s *WorldSnapshot) EntitiesInRadius(center core.Vec2, radius float64) []perception.PerceivedEntity {
	entities := s.w.spatial.NearbyEntities(center, radius)
	result := make([]perception.PerceivedEntity, 0, len(entities))
	for _, e := range entities {
		d := center.Distance(e.Pos)
		tags := s.resolveTags(e.ID)
		result = append(result, perception.PerceivedEntity{
			ID:       e.ID,
			Pos:      e.Pos,
			Distance: d,
			Tags:     tags,
		})
	}
	// Already sorted by ObjectID from NearbyEntities (D12).
	return result
}

// Tags returns the tags for an entity. Agents have ["agent"]; objects have their
// kind tag plus any derived tags (like [opaque]).
func (s *WorldSnapshot) Tags(id core.ObjectID) []core.Tag {
	return s.resolveTags(id)
}

// IsOpaque reports whether the entity with the given id blocks line-of-sight.
func (s *WorldSnapshot) IsOpaque(id core.ObjectID) bool {
	tags := s.resolveTags(id)
	for _, t := range tags {
		if t == "opaque" {
			return true
		}
	}
	return false
}

// ShadeOccluders projects flora shade parameters into the perception-facing type.
func (s *WorldSnapshot) ShadeOccluders(center core.Vec2, radius float64) []perception.ShadeOccluder {
	if s.w.floraState == nil {
		return nil
	}
	entities := s.w.spatial.NearbyEntities(center, radius)
	out := make([]perception.ShadeOccluder, 0, len(entities))
	for _, e := range entities {
		shade, ok := s.w.floraState.ShadeOf(e.ID)
		if !ok || shade.Radius <= 0 || shade.Opacity <= 0 {
			continue
		}
		out = append(out, perception.ShadeOccluder{
			ID:      shade.ID,
			Pos:     shade.Pos,
			Radius:  shade.Radius,
			Opacity: shade.Opacity,
		})
	}
	// NearbyEntities is already sorted by ObjectID; retain that order.
	return out
}

// resolveTags returns the tags for an entity (object or agent), sorted (D12).
// For agents, the returned set always includes "agent"; it also includes:
//   - "has_items" when the agent's Inventory is non-empty (lets replan inject owned_by_other)
//   - the current action's tags from the frozen snapshot (safe for concurrent access)
func (s *WorldSnapshot) resolveTags(id core.ObjectID) []core.Tag {
	// Try as agent first.
	if agentID := core.AgentID(id); true {
		if a, ok := s.w.agents[agentID]; ok {
			tags := []core.Tag{"agent"}
			// Expose inventory presence so replan can inject owned_by_other.
			if len(a.Inventory) > 0 {
				tags = append(tags, "has_items")
			}
			// Include the current action's tags from the frozen snapshot.
			if frozenTags, ok := s.frozenActionTags[agentID]; ok {
				tags = append(tags, frozenTags...)
			}
			sort.Slice(tags, func(i, j int) bool {
				return string(tags[i]) < string(tags[j])
			})
			return tags
		}
	}
	// Try as object.
	if obj, ok := s.w.objects[id]; ok {
		tags := []core.Tag{obj.Kind}
		// Sort tags for determinism (D12).
		sort.Slice(tags, func(i, j int) bool {
			return string(tags[i]) < string(tags[j])
		})
		return tags
	}
	return nil
}

// ── agent.WorldView extension methods ──────────────────────────────────────────

// SoundEvents returns the tick-scoped sound events.
func (s *WorldSnapshot) SoundEvents() []perception.SoundEvent {
	// Convert agent.SoundEvent to perception.SoundEvent.
	out := make([]perception.SoundEvent, len(s.w.currentSounds))
	for i, se := range s.w.currentSounds {
		out[i] = perception.SoundEvent{
			SourceID: se.SourceID,
			ActionID: se.ActionID,
			Pos:      se.Pos,
			Distance: se.Distance,
		}
	}
	return out
}

// KnownObjects returns objects this agent has ever perceived, in ObjectID order (D12).
func (s *WorldSnapshot) KnownObjects(self core.AgentID) []agent.KnownObject {
	known, ok := s.w.knownObjects[self]
	if !ok {
		return nil
	}
	// Sort by ObjectID (D12).
	ids := make([]core.ObjectID, 0, len(known))
	for id := range known {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return string(ids[i]) < string(ids[j])
	})
	result := make([]agent.KnownObject, 0, len(ids))
	for _, id := range ids {
		result = append(result, known[id])
	}
	return result
}

// BeliefOf returns another agent's belief for gossip folding.
func (s *WorldSnapshot) BeliefOf(self, subject core.AgentID) (tom.Belief, bool) {
	subjectAgent, ok := s.w.agents[subject]
	if !ok {
		return tom.Belief{}, false
	}
	return subjectAgent.ToM.Self(subject)
}

// HasPendingOffer reports whether there is an unresolved Offer directed at receiver.
func (s *WorldSnapshot) HasPendingOffer(receiver core.AgentID) bool {
	_, ok := s.w.pendingOffers[receiver]
	return ok
}

// ResentmentTriggers returns the agents who beat self in a resource conflict in
// the previous tick's apply phase (P3), in AgentID-stable order (D12). The
// world records a trigger for every conflict loser; only Latent agents act on
// it (agent.accrueResentment). Returns nil when self lost no conflict.
func (s *WorldSnapshot) ResentmentTriggers(self core.AgentID) []core.AgentID {
	triggers, ok := s.w.resentmentTriggers[self]
	if !ok || len(triggers) == 0 {
		return nil
	}
	out := make([]core.AgentID, len(triggers))
	copy(out, triggers)
	sort.Slice(out, func(i, j int) bool { return string(out[i]) < string(out[j]) })
	return out
}

// PlaceQuality returns the quality of a place, in [0,1]. 1 = pristine (no obstruction),
// 0 = fully blocked. Place quality is derived from the configuration and
// spatial state of the world (e.g., whether an opaque entity sits between
// the place and a designated view direction). For now, a stub returning 1.0
// always; the Scenario E test simulates view-blocking by overriding this
// via mockWorldView.
func (s *WorldSnapshot) PlaceQuality(placeID core.ObjectID) float64 {
	_ = placeID
	return 1.0
}

// MemberNeedIntensities returns the need intensities for all agents in the village.
// Caller MUST NOT mutate the returned map. Returns nil if no agents exist.
// This is used for Collective referent appraisal (P5 Scenario G).
func (s *WorldSnapshot) MemberNeedIntensities() map[core.AgentID]map[core.Dimension]float64 {
	if len(s.w.agents) == 0 {
		return nil
	}
	result := make(map[core.AgentID]map[core.Dimension]float64, len(s.w.agents))
	for _, agentID := range sortedAgentIDsMap(s.w.agents) {
		a := s.w.agents[agentID]
		if a == nil {
			continue
		}
		// Make a copy of the need intensities to prevent caller mutation.
		ni := make(map[core.Dimension]float64, len(a.NeedIntensities))
		for _, dim := range sortedDimKeys(a.NeedIntensities) {
			ni[dim] = a.NeedIntensities[dim]
		}
		result[agentID] = ni
	}
	return result
}

// AgentIDs returns all agent IDs in the village, sorted (D12).
func (s *WorldSnapshot) AgentIDs() []core.AgentID {
	return sortedAgentIDsMap(s.w.agents)
}

// IncomingSignals returns signals addressed to self this tick.
// Populated from the world's pendingSignals buffer, which is filled during the
// PREVIOUS tick's apply phase (by applySignal for SignalVote broadcasts).
func (s *WorldSnapshot) IncomingSignals(self core.AgentID) []core.Signal {
	return s.w.pendingSignals[self]
}

// sortedAgentIDsMap returns the sorted AgentID keys of a map[*Agent] (D12).
func sortedAgentIDsMap(m map[core.AgentID]*agent.Agent) []core.AgentID {
	keys := make([]core.AgentID, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return string(keys[i]) < string(keys[j])
	})
	return keys
}

// sortedDimKeys returns the sorted Dimension keys of a map (D12).
func sortedDimKeys(m map[core.Dimension]float64) []core.Dimension {
	keys := make([]core.Dimension, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return string(keys[i]) < string(keys[j])
	})
	return keys
}
