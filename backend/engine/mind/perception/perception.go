// Package perception models the three senses (Sight, Smell, Hearing) as pure,
// stateless, per-tick snapshot queries. Given an observer position and a read-only
// WorldSnapshot, it answers "what can this observer perceive right now?" It computes
// sense modeling only — falloff math and LoS occlusion — and does NOT decide what the
// observer does with a percept.
package perception

import (
	"fmt"
	"io"
	"math"
	"sort"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/space/spatial"
	"gopkg.in/yaml.v3"
)

// PerceptionConfig holds the three sense radii loaded from content/balance.yaml's
// `perception:` block. World units (free coordinates, D11). Immutable after Load.
type PerceptionConfig struct {
	SightRadius   float64 // line-of-sight range            (balance.yaml perception.sight_radius)
	SmellRadius   float64 // scent-gradient cutoff range     (balance.yaml perception.smell_radius)
	HearingRadius float64 // base hearing range              (balance.yaml perception.hearing_radius)
}

// PerceivedEntity is one entity an observer can see / is a candidate for sensing this tick.
// Distance is the Euclidean distance from the observer (already computed; saves callers a sqrt).
// Tags are a COPY of the entity's current tags (from the WorldSnapshot) — read-only.
type PerceivedEntity struct {
	ID       core.ObjectID
	Pos      core.Vec2
	Distance float64
	Tags     []core.Tag
}

// ScentSignal is one perceived scent: its source id and the gradient strength at the observer.
type ScentSignal struct {
	ID       core.ObjectID
	Strength float64
}

// SoundEvent is a sound emitted by an actor performing a `[loud]`-tagged action at its position
// for the current tick. SourceID is the acting agent/object; Pos is where the sound originated;
// ActionID names the action that emitted it. Distance is filled (observer->Pos) by Hearing on
// the returned (heard) events; it is ignored/zero on the input slice the world supplies.
type SoundEvent struct {
	SourceID core.ObjectID
	ActionID actions.ActionID
	Pos      core.Vec2
	Distance float64
}

// ShadeOccluder is one flora-shade caster the world exposes for line-of-sight attenuation.
type ShadeOccluder struct {
	ID      core.ObjectID
	Pos     core.Vec2
	Radius  float64
	Opacity float64
}

// WorldSnapshot is a read-only view over entity positions, tags, opacity, and flora shade for the
// CURRENT tick. The world (engine/world) implements it; perception only reads it.
type WorldSnapshot interface {
	EntitiesInRadius(center core.Vec2, radius float64) []PerceivedEntity
	Tags(id core.ObjectID) []core.Tag
	IsOpaque(id core.ObjectID) bool
	ShadeOccluders(center core.Vec2, radius float64) []ShadeOccluder
}

// Sensor evaluates the three senses against a WorldSnapshot. It is created once (per agent or
// shared) with the spatial index + config and reused across ticks; it holds NO per-tick state.
type Sensor struct {
	idx *spatial.SpatialHash
	cfg PerceptionConfig
}

// NewSensor builds a Sensor from the spatial index and config. The index is the canonical
// position source (D11) used to enumerate sound emitters / sight candidates that share the
// world's tick snapshot. Panics if idx is nil.
func NewSensor(idx *spatial.SpatialHash, cfg PerceptionConfig) *Sensor {
	if idx == nil {
		panic("perception: NewSensor called with nil spatial index")
	}
	return &Sensor{idx: idx, cfg: cfg}
}

// Sight returns every entity within SightRadius of observer that is NOT occluded by an opaque
// entity on the straight line observer->entity. Result is sorted by Distance ASCENDING; ties
// broken by ascending ObjectID (D12). The observer's own entity (Distance == 0 at observer) is
// excluded from the result.
func (s *Sensor) Sight(observer core.Vec2, world WorldSnapshot) []PerceivedEntity {
	candidates := world.EntitiesInRadius(observer, s.cfg.SightRadius)
	if len(candidates) == 0 {
		return nil
	}

	result := make([]PerceivedEntity, 0, len(candidates))
	for _, c := range candidates {
		d := observer.Distance(c.Pos)
		// Exclude observer's own entity (distance ~= 0).
		if d < 1e-12 {
			continue
		}
		// Check occlusion: is there an opaque entity strictly between observer and target?
		if !s.isOccluded(observer, c.Pos, c.ID, d, world) {
			result = append(result, PerceivedEntity{
				ID:       c.ID,
				Pos:      c.Pos,
				Distance: d,
				Tags:     world.Tags(c.ID),
			})
		}
	}

	// Sort by Distance ASC, ties by ObjectID ASC (D12).
	sort.Slice(result, func(i, j int) bool {
		if result[i].Distance != result[j].Distance {
			return result[i].Distance < result[j].Distance
		}
		return result[i].ID < result[j].ID
	})

	return result
}

// isOccluded reports whether the segment from observer to target is intersected by an opaque
// entity (not the target itself). It marches along the segment in steps (bounded by ~1 world
// unit), querying the spatial hash for nearby entities at each step.
func (s *Sensor) isOccluded(observer, target core.Vec2, targetID core.ObjectID, targetDist float64, world WorldSnapshot) bool {
	dir := target.Sub(observer)
	// If observer and target are essentially coincident, nothing occludes.
	if targetDist < 1e-12 {
		return false
	}

	// Normalize direction to unit vector.
	ux := dir.X / targetDist
	uy := dir.Y / targetDist

	// Step size: ~1 world unit, or as small as needed to not skip over opaque entities.
	// We use min(1.0, targetDist/2) so we always take at least 2 steps.
	step := 1.0
	if step > targetDist*0.5 {
		step = targetDist * 0.5
	}
	// Ensure at least 3 steps for short distances so we have intermediate points.
	if targetDist > 0 && step*3 > targetDist {
		step = targetDist / 3.0
	}
	if step < 0.1 {
		step = 0.1
	}

	// The spatial hash's cell size. Use it as the query radius for nearby checks at each step.
	// We use a small radius (half the step) to catch entities whose center is near the segment.
	queryRadius := step * 0.6
	if queryRadius < 0.5 {
		queryRadius = 0.5
	}

	steps := int(targetDist / step)
	// March from just beyond observer to just before target.
	for i := 1; i < steps; i++ {
		px := observer.X + ux*float64(i)*step
		py := observer.Y + uy*float64(i)*step
		pt := core.Vec2{X: px, Y: py}

		nearby := s.idx.NearbyEntities(pt, queryRadius)
		for _, n := range nearby {
			// Skip the target itself (target's opacity never occludes itself).
			if n.ID == targetID {
				continue
			}
			if world.IsOpaque(n.ID) {
				// Double-check: the entity is strictly between observer and target by projection.
				toOccluder := n.Pos.Sub(observer)
				proj := (toOccluder.X*dir.X + toOccluder.Y*dir.Y) / targetDist
				if proj > 0 && proj < targetDist {
					return true
				}
			}
		}
	}

	return false
}

// Smell returns a ScentSignal for every `[scented]`-tagged entity within SmellRadius of observer,
// with Strength from the gradient formula (no LoS — smell goes around obstacles). Result is
// sorted by Strength DESCENDING; ties broken by ascending ObjectID (D12).
func (s *Sensor) Smell(observer core.Vec2, world WorldSnapshot) []ScentSignal {
	candidates := world.EntitiesInRadius(observer, s.cfg.SmellRadius)
	if len(candidates) == 0 {
		return nil
	}

	smellTag := core.Tag("scented")
	var result []ScentSignal
	for _, c := range candidates {
		tags := world.Tags(c.ID)
		if !hasTag(tags, smellTag) {
			continue
		}
		dSq := observer.DistSq(c.Pos)
		strength := 1.0 / (1.0 + dSq) // base_strength = 1.0 per SPEC "baseStrength defaults to 1.0"
		result = append(result, ScentSignal{
			ID:       c.ID,
			Strength: strength,
		})
	}

	// Sort by Strength DESC, ties by ObjectID ASC (D12).
	sort.Slice(result, func(i, j int) bool {
		if result[i].Strength != result[j].Strength {
			return result[i].Strength > result[j].Strength // descending
		}
		return result[i].ID < result[j].ID
	})

	return result
}

// Hearing filters the tick-scoped events slice to those within HearingRadius of observer (no LoS),
// filling each kept event's Distance (observer->Pos). The input slice is a per-tick collection the
// world supplies (NO persistent subscription); it is not retained. Result is sorted by Distance
// ASCENDING; ties broken by ascending SourceID (D12). The input slice is never mutated.
func (s *Sensor) Hearing(observer core.Vec2, events []SoundEvent) []SoundEvent {
	if len(events) == 0 {
		return nil
	}

	radiusSq := s.cfg.HearingRadius * s.cfg.HearingRadius
	result := make([]SoundEvent, 0, len(events))
	for _, e := range events {
		dSq := observer.DistSq(e.Pos)
		if dSq <= radiusSq {
			e.Distance = math.Sqrt(dSq)
			result = append(result, e)
		}
	}

	// Sort by Distance ASC, ties by SourceID ASC (D12).
	sort.Slice(result, func(i, j int) bool {
		if result[i].Distance != result[j].Distance {
			return result[i].Distance < result[j].Distance
		}
		return result[i].SourceID < result[j].SourceID
	})

	return result
}

// ── Config loading ──────────────────────────────────────────────────────────────────────────

// balanceDoc is the intermediate YAML structure for parsing the perception block.
type balanceDoc struct {
	Perception *struct {
		SightRadius   *float64 `yaml:"sight_radius"`
		SmellRadius   *float64 `yaml:"smell_radius"`
		HearingRadius *float64 `yaml:"hearing_radius"`
	} `yaml:"perception"`
}

// LoadConfig parses ONLY the top-level `perception:` block from balanceDoc (the bytes of
// content/balance.yaml — the path is injected by platform/config, NEVER a file path here,
// keeping the engine IO-free, D10). It returns a descriptive error if the `perception:` block
// is absent or any radius is missing / <= 0.
func LoadConfig(r io.Reader) (PerceptionConfig, error) {
	var doc balanceDoc
	dec := yaml.NewDecoder(r)
	if err := dec.Decode(&doc); err != nil {
		return PerceptionConfig{}, fmt.Errorf("perception: failed to decode balance doc: %w", err)
	}

	if doc.Perception == nil {
		return PerceptionConfig{}, fmt.Errorf("perception: missing required top-level 'perception:' block in balance.yaml")
	}

	if doc.Perception.SightRadius == nil {
		return PerceptionConfig{}, fmt.Errorf("perception: missing required field 'sight_radius' in perception block")
	}
	if doc.Perception.SmellRadius == nil {
		return PerceptionConfig{}, fmt.Errorf("perception: missing required field 'smell_radius' in perception block")
	}
	if doc.Perception.HearingRadius == nil {
		return PerceptionConfig{}, fmt.Errorf("perception: missing required field 'hearing_radius' in perception block")
	}

	if *doc.Perception.SightRadius <= 0 {
		return PerceptionConfig{}, fmt.Errorf("perception: sight_radius must be > 0, got %v", *doc.Perception.SightRadius)
	}
	if *doc.Perception.SmellRadius <= 0 {
		return PerceptionConfig{}, fmt.Errorf("perception: smell_radius must be > 0, got %v", *doc.Perception.SmellRadius)
	}
	if *doc.Perception.HearingRadius <= 0 {
		return PerceptionConfig{}, fmt.Errorf("perception: hearing_radius must be > 0, got %v", *doc.Perception.HearingRadius)
	}

	return PerceptionConfig{
		SightRadius:   *doc.Perception.SightRadius,
		SmellRadius:   *doc.Perception.SmellRadius,
		HearingRadius: *doc.Perception.HearingRadius,
	}, nil
}

// ── Helpers ─────────────────────────────────────────────────────────────────────────────────

// hasTag reports whether the tags slice contains the given tag.
func hasTag(tags []core.Tag, tag core.Tag) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}
