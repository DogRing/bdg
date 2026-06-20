package world

import (
	"sort"

	"github.com/dogring/bdg/engine/agent"
	"github.com/dogring/bdg/engine/core"
	"github.com/dogring/bdg/engine/perception"
	"github.com/dogring/bdg/engine/tom"
)

// ── Compile-time interface check ───────────────────────────────────────────────

var _ agent.WorldView = (*WorldSnapshot)(nil)

// ── WorldSnapshot ──────────────────────────────────────────────────────────────

// WorldSnapshot is the world's implementation of agent.WorldView. It is a
// read-only view frozen at the start of phase 1, valid only for the current tick.
type WorldSnapshot struct {
	w    *World
	tick core.Tick
}

// newSnapshot creates a new snapshot bound to the current world state.
func newSnapshot(w *World) *WorldSnapshot {
	return &WorldSnapshot{w: w, tick: w.tick}
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

// resolveTags returns the tags for an entity (object or agent), sorted (D12).
func (s *WorldSnapshot) resolveTags(id core.ObjectID) []core.Tag {
	// Try as agent first.
	if agentID := core.AgentID(id); true {
		if a, ok := s.w.agents[agentID]; ok {
			_ = a
			// Agents have the "agent" tag.
			return []core.Tag{"agent"}
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

// ResentmentTriggers returns the agents who rejected or beat self in a resource
// conflict this tick (P3). AgentID-stable order. Stub for now — world conflict
// resolution not yet fully built; returns nil.
func (s *WorldSnapshot) ResentmentTriggers(self core.AgentID) []core.AgentID {
	// P3 stub: world conflict resolution tracks resentment triggers in a future pass.
	// For now, Latent agents will only accrue Resentment from explicit trigger events
	// reported by the world when conflict resolution is implemented.
	return nil
}
