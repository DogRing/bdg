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

	// PD3 grace+bleed (docs/plans/fauna.md §5, P_fa4b): while this drive is ≥ VitalDrainAbove (θ) the
	// animal's Vital bleeds VitalDrain (r) per tick — a per-drive drive-saturation→mortality coupling
	// authored as content data (D4/D10), not a bespoke starvation function. hunger authors it (starvation);
	// thermal reuses the SAME two fields later (freeze die-off). VitalDrain ≤ 0 ⇒ no coupling (off-lever).
	VitalDrain      float64
	VitalDrainAbove float64
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
	MaturityAge     float64                            // PD4-ii/P_fa4c: Age at which `maturity` operand reaches 1 (clamp01(age/MaturityAge)); ≤0 ⇒ maturity≡1 (always mature, gate-neutral)

	// §7 aging (PD11, human-RESOLVED 2026-07-20) — the falling limb of the Age axis. `senescence` is a
	// DERIVED operand, not stat decay: fauna offspring stats are the parent mix (PD4-iv), so decaying an
	// aging parent's stored Stats would breed weaker children (Lamarckian). See SPEC.md §Senescence.
	PrimeAge                  float64                       // Age below which `senescence` is 0 — youth + prime, no decline
	Lifespan                  float64                       // Age at which `senescence` reaches 1; ≤0 ⇒ senescence≡0 (whole feature off, byte-identical)
	SenescenceVitalDrain      float64                       // r: per-tick Vital bleed once senescent (the aging death channel); ≤0 ⇒ no coupling — same shape as DriveRule.VitalDrain
	SenescenceVitalDrainAbove float64                       // θ: `senescence` level at which the bleed starts
	MateCooldown              core.Tick                     // PD4-vi(b)/P_fa4c-2: post-conception refractory ticks (species trait — breeding tempo); ≤0 ⇒ no cooldown
	Speed                     *expr.Program                 // §6 locomotion speed (F35)
	TurnRate                  *expr.Program                 // §6 max turn rate radians/unit time (M6)
	ScentAcuity               *expr.Program                 // §6 scent-tracking keenness gain g (PD1/P_fa4a); nil/≤0 ⇒ exact scent Dir (neutral)
	AttackPower               *expr.Program                 // §6 damage magnitude composition (FC4)
	Hit                       *expr.Program                 // §6 hit multiplier/probability composition (FC4)
	Feed                      *expr.Program                 // §6 carcass feed value composition (FC8)
	Graze                     *expr.Program                 // §6 herbivore graze hunger-recovery factor (parallels Feed)
	Drink                     *expr.Program                 // §6 thirst-recovery factor at water (FM4; parallels Graze)
	HideChance                *expr.Program                 // §6 cover-hide probability (M3)
	CoverCost                 float64                       // scalar cover-drag cost (M4-b)
	Diet                      []core.Tag                    // diet/target tags (F7)
	Tags                      []core.Tag                    // this kind's own content tags — what another animal's Diet matches against (D10)
	IsPredator                bool                          // carries `threat:predator` (F8)
	SmellRadius               float64                       // smell radius (F31/F44)
	SightRadius               float64                       // sight radius (F44)
	FovArc                    float64                       // forward FOV half-angle (radians, F44)
	TerrainCost               map[core.Tag]float64          // per-species terrain affinity mult (W10b)
	Impassable                []core.Tag                    // terrain types this species cannot enter
	SteerChannel              map[actions.ActionID]core.Tag // action → steer-behavior tag (D4/D10)
}

// Steer-behavior tag constants recognized by the steer step (D4 — derived from
// action Tags via SpeciesRule.SteerChannel). Content authors place these tags on
// actions in content/actions.yaml to declare the steer channel (D10).
const (
	TagSteerFood  core.Tag = "seek:food"     // steer toward food scent channel
	TagSteerWater core.Tag = "seek:water"    // steer toward the water-attraction field gradient (FM4 thirst)
	TagSteerPrey  core.Tag = "seek:prey"     // steer toward prey scent channel
	TagSteerMate  core.Tag = "seek:mate"     // steer toward the nearest eligible conspecific partner (PD4-i/P_fa4c-2)
	TagSteerCover core.Tag = "seek:cover"    // steer toward the nearest cover plant to hide in (M3)
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
	readsKinCount   bool               // any utility reads `kin_count` (PD4-v): skip the crowding query when false
	utilities       map[actions.ActionID]*expr.Program
	drives          []DriveRule // in sorted DriveID order (D12)
	appTemp         *expr.Program
	comfortTemp     float64
	thermalBand     float64
	hazardAvoidance float64
	moveDeadband    float64
	maturityAge     float64
	mateCooldown    core.Tick

	primeAge                  float64 // PD11 §7 aging — see SpeciesRule
	lifespan                  float64
	senescenceVitalDrain      float64
	senescenceVitalDrainAbove float64

	speed        *expr.Program
	turnRate     *expr.Program
	scentAcuity  *expr.Program
	attackPower  *expr.Program
	hit          *expr.Program
	feed         *expr.Program
	graze        *expr.Program
	drink        *expr.Program
	hideChance   *expr.Program
	coverCost    float64
	diet         []core.Tag
	tags         []core.Tag
	isPredator   bool
	smellRadius  float64
	sightRadius  float64
	fovArc       float64
	terrainCost  map[core.Tag]float64
	impassable   map[core.Tag]bool // O(1) lookup
	steerChannel map[actions.ActionID]core.Tag
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

		// Does any utility read `kin_count`? Computing local crowding costs a spatial query per
		// scoring animal, so it is answered once at load and skipped entirely for species that
		// never mention it (PD4-v) — the same off-lever discipline as scent_acuity.
		readsKin := false
		for _, id := range cands {
			if prog := sr.Utilities[id]; prog != nil && containsAttr(prog.ReadsAttrs(), attrKinCount) {
				readsKin = true
				break
			}
		}

		r.species[sp] = speciesData{
			candidates:      cands,
			readsKinCount:   readsKin,
			utilities:       util,
			drives:          drives,
			appTemp:         sr.AppTemp,
			comfortTemp:     sr.ComfortTemp,
			thermalBand:     sr.ThermalBand,
			hazardAvoidance: sr.HazardAvoidance,
			moveDeadband:    sr.MoveDeadband,
			maturityAge:     sr.MaturityAge,
			mateCooldown:    sr.MateCooldown,

			primeAge:                  sr.PrimeAge,
			lifespan:                  sr.Lifespan,
			senescenceVitalDrain:      sr.SenescenceVitalDrain,
			senescenceVitalDrainAbove: sr.SenescenceVitalDrainAbove,

			speed:        sr.Speed,
			turnRate:     sr.TurnRate,
			scentAcuity:  sr.ScentAcuity,
			attackPower:  sr.AttackPower,
			hit:          sr.Hit,
			feed:         sr.Feed,
			graze:        sr.Graze,
			drink:        sr.Drink,
			hideChance:   sr.HideChance,
			coverCost:    sr.CoverCost,
			diet:         cloneTags(sr.Diet),
			tags:         cloneTags(sr.Tags),
			isPredator:   sr.IsPredator,
			smellRadius:  sr.SmellRadius,
			sightRadius:  sr.SightRadius,
			fovArc:       sr.FovArc,
			terrainCost:  tc,
			impassable:   imp,
			steerChannel: sc,
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

// starveDrain sums the per-drive Vital bleed for this species (PD3 grace+bleed, docs/plans/fauna.md §5):
// for each DriveRule with VitalDrain > 0 whose CURRENT value (in `drives`, start-of-tick) is ≥
// VitalDrainAbove, add VitalDrain·dt. Iterates the species' drives slice in sorted-DriveID order (D12);
// reads `drives` by single key only (no map-iteration). Returns 0 for an unknown/nil species, or when no
// coupled drive is saturated — so with no drive authoring VitalDrain the result is 0 (off-lever). Pure.
func (r *Rules) starveDrain(sp SpeciesID, drives map[DriveID]float64, dt float64) float64 {
	if r == nil {
		return scalarZero
	}
	sd, ok := r.species[sp]
	if !ok {
		return scalarZero
	}
	total := scalarZero
	for _, dr := range sd.drives {
		if dr.VitalDrain <= scalarZero {
			continue
		}
		if drives[dr.ID] >= dr.VitalDrainAbove {
			total += dr.VitalDrain * dt
		}
	}
	return total
}

// IsCourting reports whether this (species, action) pair is a courtship action — i.e. its steer
// channel is seek:mate. world uses it to read a partner's CONSENT off its committed CurrentAction
// (P_fa4c-2): mate seeking is resolved in the full pipeline, but most herbivores spend most ticks
// on the F45 dormant cheap path and re-arbitrate on ID-staggered phases, so "both animals computed
// a partner in the SAME tick" would almost never coincide. Courtship is a state an animal is in,
// not a single-tick coincidence.
func (r *Rules) IsCourting(sp SpeciesID, act actions.ActionID) bool {
	return r.steerChannelFor(sp, act) == TagSteerMate
}

// MateCooldown returns the species' post-conception refractory window in ticks (PD4-vi ⓑ /
// P_fa4c-2). Breeding tempo is a species trait — a rabbit recovers far faster than a bear — so it
// is per-species content, not a global constant. 0 (unauthored) ⇒ no refractory period.
func (r *Rules) MateCooldown(sp SpeciesID) core.Tick {
	if r == nil {
		return 0
	}
	sd, ok := r.species[sp]
	if !ok || sd.mateCooldown < 0 {
		return 0
	}
	return sd.mateCooldown
}

// maturity returns the §6 `maturity` operand for an animal of the given species+age (PD4-ii/P_fa4c):
// clamp01(age / MaturityAge). MaturityAge ≤ 0 (unauthored) ⇒ 1 (always mature) — so a species with no
// maturity_age is gate-neutral (the operand is a constant 1, reproduction is unrestricted by age). Pure.
func (r *Rules) maturity(sp SpeciesID, age float64) float64 {
	if r == nil {
		return scalarOne
	}
	sd, ok := r.species[sp]
	if !ok || sd.maturityAge <= scalarZero {
		return scalarOne
	}
	return clamp01(age / sd.maturityAge)
}

// senescence returns the §6 `senescence` operand (PD11 / §7 aging): clamp01((age − PrimeAge) /
// (Lifespan − PrimeAge)) — 0 through youth and prime, 1 at Lifespan. The exact mirror of maturity: one
// is the rising limb of a life, the other the falling one. Lifespan ≤ 0 (unauthored) ⇒ 0, so a species
// that never opted in is byte-identical to pre-PD11. Pure.
//
// It is DERIVED, never stored, and it is deliberately NOT stat decay: offspring stats are the parent mix
// (PD4-iv), so decaying an aging parent's stored Stats would breed weaker children — inheritance of an
// acquired condition. Age belongs to the phenotype, not the genotype (SPEC.md §Senescence).
func (r *Rules) senescence(sp SpeciesID, age float64) float64 {
	if r == nil {
		return scalarZero
	}
	sd, ok := r.species[sp]
	if !ok || sd.lifespan <= scalarZero {
		return scalarZero
	}
	span := sd.lifespan - sd.primeAge
	if span <= scalarZero {
		// Degenerate authoring (prime ≥ lifespan): collapse to a step at lifespan rather than divide by
		// zero. platform/config rejects this at load, so this branch only protects a hand-built Rules.
		if age >= sd.lifespan {
			return scalarOne
		}
		return scalarZero
	}
	return clamp01((age - sd.primeAge) / span)
}

// Lifespan is the Age at which this species' `senescence` operand reaches 1 (PD11 / §7 aging), or 0 when
// the species does not age. Exported for the WORLD-BUILDING side: a founding population placed uniformly
// at Age 0 is a single cohort that then dies all at once at its lifespan, so worldgen and the rescue
// floor spread initial ages across this range (docs/plans/fauna.md PD11-iv). Pure.
func (r *Rules) Lifespan(sp SpeciesID) float64 {
	if r == nil {
		return scalarZero
	}
	sd, ok := r.species[sp]
	if !ok {
		return scalarZero
	}
	return sd.lifespan
}

// senescenceDrain is the per-tick Vital bleed of old age (PD11-ii): SenescenceVitalDrain·dt once
// `senescence` reaches SenescenceVitalDrainAbove. Deliberately the SAME (θ, r) shape as the PD3
// starvation coupling — aging needed no new mortality machinery, only a different trigger. The trigger
// is the derived operand rather than a stored drive because age is not a drive and must not become one
// (a drive would accumulate independently of Age, duplicating the state). Returns 0 when unauthored. Pure.
func (r *Rules) senescenceDrain(sp SpeciesID, age, dt float64) float64 {
	if r == nil {
		return scalarZero
	}
	sd, ok := r.species[sp]
	if !ok || sd.senescenceVitalDrain <= scalarZero {
		return scalarZero
	}
	if r.senescence(sp, age) < sd.senescenceVitalDrainAbove {
		return scalarZero
	}
	return sd.senescenceVitalDrain * dt
}

// VitalDrains splits this tick's non-combat Vital bleed into its two channels — (acute drive saturation,
// senescence) — so the WORLD can label a death without re-deriving either formula (death is world-owned,
// F3). With two non-combat channels the cause is no longer a constant: the world labels by the larger
// magnitude, which ATTRIBUTES the death rather than ranking the channels by precedence, so introducing
// aging cannot show up in telemetry as a famine. Pure, no RNG.
func (r *Rules) VitalDrains(a Animal, dt float64) (drive, senescence float64) {
	return r.starveDrain(a.Species, a.Drives, dt), r.senescenceDrain(a.Species, a.Age, dt)
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

// ScentAcuity is the species' §6 scent-tracking keenness gain `g` (PD1/P_fa4a, docs/plans/fauna.md §5).
// Higher = keener nose: the confidence a homing animal has in the scent gradient is g·I/(1+g·I) (I = channel
// intensity), so a keener species localizes a fainter scent. 0 (nil program / negative / nil Rules) ⇒ the
// caller SKIPS the confidence blend and uses the exact scent Dir — the byte-identical neutral off-lever.
// Pure, no RNG.
func (r *Rules) ScentAcuity(sp SpeciesID, ctx expr.Context) float64 {
	if r == nil {
		return scalarZero
	}
	sd, ok := r.species[sp]
	if !ok || sd.scentAcuity == nil {
		return scalarZero
	}
	v := sd.scentAcuity.EvalNumber(ctx)
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
