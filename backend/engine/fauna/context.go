package fauna

import (
	"hash/fnv"
	"math"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/space/scent"
)

// ── animalContext — expr.Context adapter ─────────────────────────────────────

// animalContext adapts one Animal's state + sense data + climate to the
// expr.Context interface for §6 utility/speed/appTemp evaluation.
//
// §6 operand channels (F27 — all lowercase/dotted, flora operand parity):
//
//	Stat channel  → base attributes (Strength, Dexterity, …); uppercase-initial (D10)
//	Attr channel  → drives (hunger, fear, …) + scent (scent.food, …) +
//	                distance (dist.food, …) + sight (sight.predator) +
//	                climate (temperature, moisture, wind.dir, wind.mag) +
//	                apparent_temp + is_current
//	Pred channel  → always false (fauna uses no predicates in P_fa1)
//
// isCurrent is the ONLY mutable field (set per-candidate before Utility eval).
// All other fields are set once and read-only during a scoring pass (D12).
//
// dist.food / dist.prey: smellRadius / (1 + intensity) — a coarse distance
// estimate in world units: high intensity → near 0, absent → smellRadius.
// dist.predator: Euclidean distance to nearest FOV predator (or sightRadius).
type animalContext struct {
	animal      *Animal
	drives      map[DriveID]float64 // may be pre- or post-update drives (caller sets)
	reading     scent.Reading       // scent reading at Pos (Step 1)
	sightPred   float64             // 1.0 if predator in FOV, else 0.0 (Step 1)
	distPred    float64             // distance to nearest FOV predator (or sightRadius)
	smellRadius float64             // from Rules.Senses; used for dist.{food,prey}
	sightRadius float64             // from Rules.Senses; used as sentinel for dist.predator
	env         EnvSample           // injected climate sample (world-provided)
	appTemp     float64             // pre-computed apparent_temp (to avoid circular eval)
	isCurrent   bool                // set per-candidate in scoring loop (is_current operand)
}

// Stat resolves a base-attribute stat id to its value from Animal.Stats.
// Returns 0 for an absent stat (load-time validation guarantees presence; D10).
func (c *animalContext) Stat(id core.StatID) float64 {
	return c.animal.Stats[id]
}

// Attr resolves a §6 attribute operand by name. Returns (0, false) for unknown
// names (expr maps absence to 0, fixed policy). Drive ids (hunger, fear, …) are
// resolved from the drives map. All operand names are lowercase/dotted (F27/D10).
func (c *animalContext) Attr(name core.Tag) (float64, bool) {
	// Scent channels (F32/F34) — intensity scalars.
	switch name {
	case "scent.food":
		return c.reading.Food.Intensity, true
	case "scent.prey":
		return c.reading.Prey.Intensity, true
	case "scent.predator":
		return c.reading.Predator.Intensity, true

	// Coarse distance estimates derived from scent intensity (D11 — no snap).
	// Formula: smellRadius / (1 + intensity).
	// High intensity ⇒ near 0; absent (intensity=0) ⇒ smellRadius.
	case "dist.food":
		return c.scentDist(c.reading.Food.Intensity, c.smellRadius), true
	case "dist.prey":
		return c.scentDist(c.reading.Prey.Intensity, c.smellRadius), true

	// Sight predator distance: Euclidean from spatial query, or sightRadius if absent.
	case "dist.predator":
		return c.distPred, true

	// Sight predator presence (1.0 / 0.0) from the FOV spatial query (F44).
	case "sight.predator":
		return c.sightPred, true

	// Apparent temperature (pre-computed from AppTemp program, F40).
	case "apparent_temp":
		return c.appTemp, true

	// Climate operands (injected from EnvSample, F33/F40).
	case "temperature":
		return c.env.Temperature, true
	case "moisture":
		return c.env.Moisture, true
	case "wind.dir":
		return c.env.Wind.Dir, true
	case "wind.mag":
		return c.env.Wind.Mag, true

	// is_current: 1.0 iff the candidate being evaluated == CurrentAction (F30/F45/R5).
	case "is_current":
		if c.isCurrent {
			return 1.0, true
		}
		return 0.0, true
	}

	// Drive ids (open content, F27 — a drive id IS its §6 Attr operand name).
	if v, ok := c.drives[DriveID(name)]; ok {
		return v, true
	}

	return 0, false
}

// Pred always returns false. fauna uses no boolean predicates in P_fa1.
func (c *animalContext) Pred(_ string, _ core.Tag) bool { return false }

// scentDist converts a scent intensity to a coarse distance estimate (world units).
// Formula: radius / (1 + intensity). When intensity=0: returns radius (out of range).
// When intensity→∞: approaches 0 (right at source).
func (c *animalContext) scentDist(intensity, radius float64) float64 {
	if radius <= 0 {
		return 0
	}
	return radius / (1 + intensity)
}

// ── phase — deterministic dormant-jitter hash ─────────────────────────────────

// phase returns a stable FNV-1a hash of the ObjectID bytes, modulo period.
// Same ID + same period ⇒ same value across runs and processes (D12). Used as
// the dormant re-arbitration gate: (Tick + phase(ID)) % DormantPeriod == 0
// spreads dormant re-evals across ticks (load levelling, F45/SPEC Cadence).
// Returns 0 if period ≤ 0.
func phase(id core.ObjectID, period int) core.Tick {
	if period <= 0 {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return core.Tick(h.Sum32() % uint32(period))
}

// ── angularDiff ───────────────────────────────────────────────────────────────

// angularDiff returns the signed angular difference a−b, wrapped to (−π, π].
func angularDiff(a, b float64) float64 {
	d := math.Mod(a-b, 2*math.Pi)
	if d > math.Pi {
		d -= 2 * math.Pi
	} else if d <= -math.Pi {
		d += 2 * math.Pi
	}
	return d
}
