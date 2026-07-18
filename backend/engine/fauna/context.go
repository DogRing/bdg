package fauna

import (
	"hash/fnv"
	"math"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/space/scent"
)

const (
	scalarZero = 0.0
	scalarOne  = 1.0

	fullCircleRadians = 2 * math.Pi

	attrScentFood     core.Tag = "scent.food"
	attrScentPrey     core.Tag = "scent.prey"
	attrScentPredator core.Tag = "scent.predator"
	attrScentCarrion  core.Tag = "scent.carrion"
	attrDistFood      core.Tag = "dist.food"
	attrDistPrey      core.Tag = "dist.prey"
	attrDistPredator  core.Tag = "dist.predator"
	attrSightPredator core.Tag = "sight.predator"
	attrTargetThreat  core.Tag = "target.threat"
	attrApparentTemp  core.Tag = "apparent_temp"
	attrTemperature   core.Tag = "temperature"
	attrMoisture      core.Tag = "moisture"
	attrWindDir       core.Tag = "wind.dir"
	attrWindMag       core.Tag = "wind.mag"
	attrDaylight      core.Tag = "daylight"
	attrIsCurrent     core.Tag = "is_current"
	attrMaturity      core.Tag = "maturity" // PD4-ii/P_fa4c: clamp01(age/maturity_age); 1 when mature (or maturity_age unauthored)
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
	animal       *Animal
	drives       map[DriveID]float64 // may be pre- or post-update drives (caller sets)
	reading      scent.Reading       // scent reading at Pos (Step 1)
	sightPred    float64             // 1.0 if predator in FOV, else 0.0 (Step 1)
	distPred     float64             // distance to nearest FOV predator (or sightRadius)
	smellRadius  float64             // from Rules.Senses; used for dist.{food,prey}
	sightRadius  float64             // from Rules.Senses; used as sentinel for dist.predator
	env          EnvSample           // injected climate sample (world-provided)
	appTemp      float64             // pre-computed apparent_temp (to avoid circular eval)
	maturity     float64             // pre-computed clamp01(age/maturity_age) ∈[0,1]; §6 `maturity` operand (PD4-ii/P_fa4c)
	targetThreat float64             // candidate target danger for combat utility (FC2)
	isCurrent    bool                // set per-candidate in scoring loop (is_current operand)
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
	case attrScentFood:
		return c.reading.Food.Intensity, true
	case attrScentPrey:
		return c.reading.Prey.Intensity, true
	case attrScentPredator:
		return c.reading.Predator.Intensity, true
	case attrScentCarrion:
		return c.reading.Carrion.Intensity, true

	// Coarse distance estimates derived from scent intensity (D11 — no snap).
	// Formula: smellRadius / (1 + intensity).
	// High intensity ⇒ near 0; absent (intensity=0) ⇒ smellRadius.
	case attrDistFood:
		return c.scentDist(c.reading.Food.Intensity, c.smellRadius), true
	case attrDistPrey:
		return c.scentDist(c.reading.Prey.Intensity, c.smellRadius), true

	// Sight predator distance: Euclidean from spatial query, or sightRadius if absent.
	case attrDistPredator:
		return c.distPred, true

	// Sight predator presence (1.0 / 0.0) from the FOV spatial query (F44).
	case attrSightPredator:
		return c.sightPred, true

	// Combat candidate target danger (FC2).
	case attrTargetThreat:
		return c.targetThreat, true

	// Apparent temperature (pre-computed from AppTemp program, F40).
	case attrApparentTemp:
		return c.appTemp, true

	// Maturity (pre-computed clamp01(age/maturity_age), PD4-ii/P_fa4c): 1 when mature (or when the
	// species authors no maturity_age). Gates the Mate §6 utility + partner eligibility.
	case attrMaturity:
		return c.maturity, true

	// Climate operands (injected from EnvSample, F33/F40).
	case attrTemperature:
		return c.env.Temperature, true
	case attrMoisture:
		return c.env.Moisture, true
	case attrWindDir:
		return c.env.Wind.Dir, true
	case attrWindMag:
		return c.env.Wind.Mag, true

	// Daylight (diurnal cue, FM11): 1 at solar-noon, 0 at midnight, smooth. World
	// injects it from the worldtime clock (EnvSample.Daylight). Diurnal vs nocturnal
	// species emerge from the §6 sign ((1−daylight) vs daylight), not a hardcoded flag.
	case attrDaylight:
		return c.env.Daylight, true

	// is_current: 1.0 iff the candidate being evaluated == CurrentAction (F30/F45/R5).
	case attrIsCurrent:
		if c.isCurrent {
			return scalarOne, true
		}
		return scalarZero, true
	}

	// Drive ids (open content, F27 — a drive id IS its §6 Attr operand name).
	if v, ok := c.drives[DriveID(name)]; ok {
		return v, true
	}

	return scalarZero, false
}

// Pred always returns false. fauna uses no boolean predicates in P_fa1.
func (c *animalContext) Pred(_ string, _ core.Tag) bool { return false }

// scentDist converts a scent intensity to a coarse distance estimate (world units).
// Formula: radius / (1 + intensity). When intensity=0: returns radius (out of range).
// When intensity→∞: approaches 0 (right at source).
func (c *animalContext) scentDist(intensity, radius float64) float64 {
	if radius <= 0 {
		return scalarZero
	}
	return radius / (scalarOne + intensity)
}

// ── phase — deterministic dormant-jitter hash ─────────────────────────────────

// phase returns a stable FNV-1a hash of the ObjectID bytes, modulo period.
// Same ID + same period ⇒ same value across runs and processes (D12). Used as
// the dormant re-arbitration gate: (Tick + phase(ID)) % DormantPeriod == 0
// spreads dormant re-evals across ticks (load levelling, F45/SPEC Cadence).
// Returns 0 if period ≤ 0.
func phase(id core.ObjectID, period int) core.Tick {
	if period <= 0 {
		return core.Tick(0)
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return core.Tick(h.Sum32() % uint32(period))
}

// ── angularDiff ───────────────────────────────────────────────────────────────

// angularDiff returns the signed angular difference a−b, wrapped to (−π, π].
func angularDiff(a, b float64) float64 {
	d := math.Mod(a-b, fullCircleRadians)
	if d > math.Pi {
		d -= fullCircleRadians
	} else if d <= -math.Pi {
		d += fullCircleRadians
	}
	return d
}
