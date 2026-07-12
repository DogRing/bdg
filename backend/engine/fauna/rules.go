package fauna

import (
	"math"
	"sort"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/mind/actions"
)

const defaultTerrainCost = 1.0

var absentUtilityScore = math.Inf(-1)

// ── DriveRule / SpeciesRule (NewRules inputs) ─────────────────────────────────

// DriveRule is one drive's compiled params (F25(c)/F43). The drive id is also its
// §6 Attr operand (F27). Classification by fields:
//   - Rate > 0                         → ACCUMULATOR (hunger/fatigue/repro_readiness)
//   - WaryLevel > 0 || FleeLevel > 0   → FEAR SET-from-context; Decay cools it
//   - else                             → THERMAL derived from AppTemp (F40)
type DriveRule struct {
	ID        DriveID
	Rate      float64 // accumulator per-tick rise; 0 for set/derived drives
	Decay     float64 // per-tick decay when not raised/set (e.g. fear cooling)
	WaryLevel float64 // fear value SET on scent.predator (F43); 0 if unused
	FleeLevel float64 // fear value SET on sight.predator (F43); 0 if unused
}

// SpeciesRule is one species' compiled fauna spec (the NewRules input).
// All expr.Programs are compiled by platform/config; fauna never parses YAML (D10).
// TerrainCost maps terrain tag → cost multiplier (W10b). Impassable lists terrain
// types the species cannot enter (fish on land). SteerChannel maps each ActionID
// to a steer-behavior tag (content-derived from action Tags, D4/D10); the fauna
// package defines the recognized tag constants below. Absent entry → continue
// along current Heading.
type SpeciesRule struct {
	Utilities       map[actions.ActionID]*expr.Program // candidate set + per-action §6 utility (F26)
	Drives          []DriveRule                        // drive vocabulary + params (F25(c))
	AppTemp         *expr.Program                      // §6 apparent_temp (F40) — emits °C (render + operand)
	ComfortTemp     float64                            // °C midpoint of the thermal comfort band (FA5)
	ThermalBand     float64                            // °C half-width; thermal = clamp01(|apparent_temp−comfort|/band); ≤0 ⇒ thermal neutral (0)
	HazardAvoidance float64                            // per-species hazard-repulsion multiplier `e` (P_move1/FM5); ≤0 ⇒ no bend
	MoveDeadband    float64                            // per-species §6-speed hold threshold (FM14a); >0 overrides Snapshot.MoveDeadband, ≤0 ⇒ fall back to the global
	Speed           *expr.Program                      // §6 locomotion speed (F35)
	TurnRate        *expr.Program                      // §6 max turn rate radians/unit time (M6)
	AttackPower     *expr.Program                      // §6 damage magnitude composition (FC4)
	Hit             *expr.Program                      // §6 hit multiplier/probability composition (FC4)
	Feed            *expr.Program                      // §6 carcass feed value composition (FC8)
	Graze           *expr.Program                      // §6 herbivore graze hunger-recovery factor (parallels Feed)
	Drink           *expr.Program                      // §6 thirst-recovery factor at water (FM4; parallels Graze)
	HideChance      *expr.Program                      // §6 cover-hide probability (M3)
	CoverCost       float64                            // scalar cover-drag cost (M4-b)
	Diet            []core.Tag                         // diet/target tags (F7)
	Tags            []core.Tag                         // this kind's own content tags — what another animal's Diet matches against (D10)
	IsPredator      bool                               // carries `threat:predator` (F8)
	SmellRadius     float64                            // smell radius (F31/F44)
	SightRadius     float64                            // sight radius (F44)
	FovArc          float64                            // forward FOV half-angle (radians, F44)
	TerrainCost     map[core.Tag]float64               // per-species terrain affinity mult (W10b)
	Impassable      []core.Tag                         // terrain types this species cannot enter
	SteerChannel    map[actions.ActionID]core.Tag      // action → steer-behavior tag (D4/D10)
}

// Steer-behavior tag constants recognized by the steer step (D4 — derived from
// action Tags via SpeciesRule.SteerChannel). Content authors place these tags on
// actions in content/actions.yaml to declare the steer channel (D10).
const (
	TagSteerFood  core.Tag = "seek:food"     // steer toward food scent channel
	TagSteerWater core.Tag = "seek:water"    // steer toward the water-attraction field gradient (FM4 thirst)
	TagSteerPrey  core.Tag = "seek:prey"     // steer toward prey scent channel
	TagFleePred   core.Tag = "flee:predator" // steer away from predator (reversed dir)
	TagWaryPred   core.Tag = "wary:predator" // steer slowly away from predator
	TagNoLoco     core.Tag = "no:locomotion" // rest: NextPos == Pos
	TagAttack     core.Tag = "combat:attack" // engage/exchange against a diet target
	TagFeed       core.Tag = "feed:carrion"  // steer toward carrion scent / feed target
	TagSleep      core.Tag = "state:sleep"   // torpor sleep: NextPos == Pos (like no-loco) + high F45 wake threshold (SS3) + deep fatigue recovery (SS2)
)

// ── internal compiled species table ───────────────────────────────────────────

// speciesData is the immutable per-species compiled record stored in Rules.
type speciesData struct {
	candidates      []actions.ActionID // sorted (D12)
	utilities       map[actions.ActionID]*expr.Program
	drives          []DriveRule // in sorted DriveID order (D12)
	appTemp         *expr.Program
	comfortTemp     float64
	thermalBand     float64
	hazardAvoidance float64
	moveDeadband    float64
	speed           *expr.Program
	turnRate        *expr.Program
	attackPower     *expr.Program
	hit             *expr.Program
	feed            *expr.Program
	graze           *expr.Program
	drink           *expr.Program
	hideChance      *expr.Program
	coverCost       float64
	diet            []core.Tag
	tags            []core.Tag
	isPredator      bool
	smellRadius     float64
	sightRadius     float64
	fovArc          float64
	terrainCost     map[core.Tag]float64
	impassable      map[core.Tag]bool // O(1) lookup
	steerChannel    map[actions.ActionID]core.Tag
}

// ── Rules ─────────────────────────────────────────────────────────────────────

// Rules is the compiled, immutable per-species fauna table. Opaque; built by
// NewRules from SpeciesRule inputs. An empty map = fauna-off (Candidates returns
// empty for every species). Pure read-only during Step.
type Rules struct {
	species map[SpeciesID]speciesData
}

// NewRules builds the immutable per-species fauna table from COMPILED inputs.
// platform/config parses content/objects.yaml `fauna:`, compiles each §6 via
// expr.Parse, validates, then calls this. Pure. An empty map = fauna-off (the
// P_fa1 safety lever: no animals ⇒ no intents, legacy world goldens hold).
func NewRules(species map[SpeciesID]SpeciesRule) *Rules {
	r := &Rules{
		species: make(map[SpeciesID]speciesData, len(species)),
	}
	for sp, sr := range species {
		// Sorted candidates (D12).
		cands := make([]actions.ActionID, 0, len(sr.Utilities))
		for act := range sr.Utilities {
			cands = append(cands, act)
		}
		sort.Slice(cands, func(i, j int) bool { return cands[i] < cands[j] })

		// Sorted drives (D12).
		drives := make([]DriveRule, len(sr.Drives))
		copy(drives, sr.Drives)
		sort.Slice(drives, func(i, j int) bool { return drives[i].ID < drives[j].ID })

		// impassable set for O(1) lookup.
		imp := make(map[core.Tag]bool, len(sr.Impassable))
		for _, t := range sr.Impassable {
			imp[t] = true
		}

		// Deep-copy maps to ensure immutability.
		tc := make(map[core.Tag]float64, len(sr.TerrainCost))
		for k, v := range sr.TerrainCost {
			tc[k] = v
		}
		util := make(map[actions.ActionID]*expr.Program, len(sr.Utilities))
		for k, v := range sr.Utilities {
			util[k] = v
		}
		sc := make(map[actions.ActionID]core.Tag, len(sr.SteerChannel))
		for k, v := range sr.SteerChannel {
			sc[k] = v
		}

		r.species[sp] = speciesData{
			candidates:      cands,
			utilities:       util,
			drives:          drives,
			appTemp:         sr.AppTemp,
			comfortTemp:     sr.ComfortTemp,
			thermalBand:     sr.ThermalBand,
			hazardAvoidance: sr.HazardAvoidance,
			moveDeadband:    sr.MoveDeadband,
			speed:           sr.Speed,
			turnRate:        sr.TurnRate,
			attackPower:     sr.AttackPower,
			hit:             sr.Hit,
			feed:            sr.Feed,
			graze:           sr.Graze,
			drink:           sr.Drink,
			hideChance:      sr.HideChance,
			coverCost:       sr.CoverCost,
			diet:            cloneTags(sr.Diet),
			tags:            cloneTags(sr.Tags),
			isPredator:      sr.IsPredator,
			smellRadius:     sr.SmellRadius,
			sightRadius:     sr.SightRadius,
			fovArc:          sr.FovArc,
			terrainCost:     tc,
			impassable:      imp,
			steerChannel:    sc,
		}
	}
	return r
}

// ── Rules methods ─────────────────────────────────────────────────────────────

// Candidates returns the species' candidate ActionIDs in sorted order (D12).
// Empty for an unknown species (fauna-off neutrality).
func (r *Rules) Candidates(sp SpeciesID) []actions.ActionID {
	if r == nil {
		return nil
	}
	sd, ok := r.species[sp]
	if !ok {
		return nil
	}
	out := make([]actions.ActionID, len(sd.candidates))
	copy(out, sd.candidates)
	return out
}

// Utility evaluates the (species × action) §6 utility Program against ctx.
// Returns math.Inf(-1) for a species/action absent from the table (never chosen
// by max, ties impossible). Pure, no RNG.
func (r *Rules) Utility(sp SpeciesID, act actions.ActionID, ctx expr.Context) float64 {
	if r == nil {
		return absentUtilityScore
	}
	sd, ok := r.species[sp]
	if !ok {
		return absentUtilityScore
	}
	prog, ok := sd.utilities[act]
	if !ok || prog == nil {
		return absentUtilityScore
	}
	return prog.EvalNumber(ctx)
}

// DriveUpdate advances the whole drive vector ONE tick per F25(c) and returns the
// new vector (clamped [0,1]). Pure, no RNG, drive ids advanced in sorted order.
// Does NOT apply the action's drive Effect (world's apply). Fear SET-from-context:
// scent.predator → WaryLevel, sight.predator → FleeLevel (F43). Thermal SET from
// AppTemp (0 in P1 climate-OFF). Does NOT mutate cur.
func (r *Rules) DriveUpdate(sp SpeciesID, cur map[DriveID]float64, ctx expr.Context, dt float64) map[DriveID]float64 {
	if r == nil {
		return cloneDrives(cur)
	}
	sd, ok := r.species[sp]
	if !ok {
		return cloneDrives(cur)
	}

	// Pre-read context operands used by conditional drives (sorted access, not map-range).
	scentPred, _ := ctx.Attr(attrScentPredator)
	sightPred, _ := ctx.Attr(attrSightPredator)

	// AppTemp for thermal drive (if any thermal drives exist; 0 in P1).
	appTemp := r.AppTemp(sp, ctx)

	next := cloneDrives(cur)

	// Advance drives in sorted order (D12 — drives are stored sorted).
	for _, dr := range sd.drives {
		v := next[dr.ID]
		switch {
		case dr.Rate > scalarZero:
			// Accumulator: rise by rate * dt (D9: no future field).
			v += dr.Rate * dt
		case dr.WaryLevel > scalarZero || dr.FleeLevel > scalarZero:
			// Fear drive: SET-from-context (F43).
			if sightPred > scalarZero {
				v = dr.FleeLevel
			} else if scentPred > scalarZero {
				v = dr.WaryLevel
			} else {
				// Neither signal: decay toward 0.
				v -= dr.Decay * dt
			}
		default:
			// Thermal drive: symmetric comfort-band stress from apparent_temp
			// (F40/FA5). stress = |apparent_temp − comfort| / band; clamp01 below.
			// Cold AND heat deviations raise it; band ≤ 0 ⇒ 0 (neutral).
			v = thermalStress(appTemp, sd.comfortTemp, sd.thermalBand)
		}
		next[dr.ID] = clamp01(v)
	}
	return next
}

// cheapDriveAdvance advances ONLY the accumulator drives by rate + decays fear
// drives. Used on the dormant cheap path (no full sense, no SET from context).
// Drive ids advanced in sorted order (D12). Does NOT mutate cur.
func (r *Rules) cheapDriveAdvance(sp SpeciesID, cur map[DriveID]float64, dt float64) map[DriveID]float64 {
	if r == nil {
		return cloneDrives(cur)
	}
	sd, ok := r.species[sp]
	if !ok {
		return cloneDrives(cur)
	}
	next := cloneDrives(cur)
	for _, dr := range sd.drives {
		v := next[dr.ID]
		switch {
		case dr.Rate > scalarZero:
			// Accumulator: rise by rate.
			v += dr.Rate * dt
		case dr.WaryLevel > scalarZero || dr.FleeLevel > scalarZero:
			// Fear: decay only (no SET on cheap path).
			v -= dr.Decay * dt
		default:
			// Thermal: no change on cheap path.
		}
		next[dr.ID] = clamp01(v)
	}
	return next
}

// hazardAvoidance returns the species' hazard-repulsion multiplier `e` (P_move1/FM5).
// ≤ 0 ⇒ no blend. Zero for an unknown/nil species.
func (r *Rules) hazardAvoidance(sp SpeciesID) float64 {
	if r == nil {
		return scalarZero
	}
	if sd, ok := r.species[sp]; ok {
		return sd.hazardAvoidance
	}
	return scalarZero
}

// moveDeadband returns the effective locomotion deadband for the species (FM14a): the species' own
// `move_deadband` when it authored a POSITIVE one, else the world's `global` (`Snapshot.MoveDeadband`).
// A single global scalar cannot separate a fast idler (rabbit idle ≈ 0.34) from a slow forager whose
// graze-seek speed equals its idle speed (deer ≈ 0.21) — stopping the former would freeze the latter's
// foraging. Per-species overrides break that tie; the global stays the fallback so unauthored species and
// the FM9 off-lever (global 0) are byte-identical.
func (r *Rules) moveDeadband(sp SpeciesID, global float64) float64 {
	if r == nil {
		return global
	}
	if sd, ok := r.species[sp]; ok && sd.moveDeadband > scalarZero {
		return sd.moveDeadband
	}
	return global
}

// AppTemp evaluates the species' §6 apparent_temp Program over climate operands +
// the animal's own attrs. P1 climate-OFF: neutral inputs ⇒ ≈0. Pure, no RNG.
// Returns 0 if the species has no AppTemp program.
func (r *Rules) AppTemp(sp SpeciesID, ctx expr.Context) float64 {
	if r == nil {
		return scalarZero
	}
	sd, ok := r.species[sp]
	if !ok || sd.appTemp == nil {
		return scalarZero
	}
	return sd.appTemp.EvalNumber(ctx)
}

// Speed evaluates the species' §6 locomotion speed Program. Returns 0 if the
// species has no Speed program. Pure, no RNG.
func (r *Rules) Speed(sp SpeciesID, ctx expr.Context) float64 {
	if r == nil {
		return scalarZero
	}
	sd, ok := r.species[sp]
	if !ok || sd.speed == nil {
		return scalarZero
	}
	v := sd.speed.EvalNumber(ctx)
	if v < scalarZero {
		return scalarZero
	}
	return v
}

// TurnRate is the species' §6 max turn RATE (radians per unit time; ×DT =
// per-tick heading cap, M6). 0 (nil/absent/negative) = unlimited: caller does
// NOT clamp (OFF-neutral). Pure.
func (r *Rules) TurnRate(sp SpeciesID, ctx expr.Context) float64 {
	if r == nil {
		return scalarZero
	}
	sd, ok := r.species[sp]
	if !ok || sd.turnRate == nil {
		return scalarZero
	}
	v := sd.turnRate.EvalNumber(ctx)
	if v < scalarZero {
		return scalarZero
	}
	return v
}

// IsPredator reports whether the species carries the `threat:predator` tag (F8).
func (r *Rules) IsPredator(sp SpeciesID) bool {
	if r == nil {
		return false
	}
	sd, ok := r.species[sp]
	return ok && sd.isPredator
}

// Senses returns the species' sense radii: smellRadius, sightRadius, fovArc.
// Returns zeros for an unknown species.
func (r *Rules) Senses(sp SpeciesID) (smellRadius, sightRadius, fovArc float64) {
	if r == nil {
		return scalarZero, scalarZero, scalarZero
	}
	sd, ok := r.species[sp]
	if !ok {
		return scalarZero, scalarZero, scalarZero
	}
	return sd.smellRadius, sd.sightRadius, sd.fovArc
}

// TerrainCost returns the species' affinity for a terrain type (W10b): mult
// multiplies the navmap BaseCost for the effective traversal cost; absent terrain
// ⇒ 1.0. passable=false ⇒ the species cannot enter that terrain (fish on land).
func (r *Rules) TerrainCost(sp SpeciesID, terrain core.Tag) (mult float64, passable bool) {
	if r == nil {
		return defaultTerrainCost, true
	}
	sd, ok := r.species[sp]
	if !ok {
		return defaultTerrainCost, true
	}
	if sd.impassable[terrain] {
		return defaultTerrainCost, false
	}
	m, found := sd.terrainCost[terrain]
	if !found {
		m = defaultTerrainCost
	}
	return m, true
}

// steerChannelFor returns the steer-behavior tag for the chosen action, or ""
// if no steer channel is mapped (default: continue along current Heading).
func (r *Rules) steerChannelFor(sp SpeciesID, act actions.ActionID) core.Tag {
	if r == nil {
		return ""
	}
	sd, ok := r.species[sp]
	if !ok {
		return ""
	}
	return sd.steerChannel[act]
}

// ── helpers ───────────────────────────────────────────────────────────────────

func cloneDrives(m map[DriveID]float64) map[DriveID]float64 {
	out := make(map[DriveID]float64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneTags(in []core.Tag) []core.Tag {
	out := append([]core.Tag(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func clamp01(v float64) float64 {
	if v < scalarZero {
		return scalarZero
	}
	if v > scalarOne {
		return scalarOne
	}
	return v
}

// thermalStress maps an apparent temperature (°C) to a SYMMETRIC comfort-band
// stress (FA5/F40): |appTemp − comfort| / band, un-clamped (the DriveUpdate
// caller clamp01s it into the [0,1] thermal drive). Cold AND heat deviations
// both raise stress, so "cold night → thermal↑" and "hot noon → thermal↑" are
// both encodable; apparent_temp itself stays °C for render (CA3). A band ≤ 0
// means "no comfort band authored" ⇒ 0 (the thermal-neutral lever: a species
// with a thermal drive but no comfort_temp/thermal_band never feels stress).
func thermalStress(appTemp, comfort, band float64) float64 {
	if band <= scalarZero {
		return scalarZero
	}
	return math.Abs(appTemp-comfort) / band
}
