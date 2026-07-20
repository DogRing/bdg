// Package ecosim is the G5 ecosystem integration test harness.
// It wires fauna/scent/climate/navmap/spatial into a deterministic tick loop
// implementing the SPEC-world-fauna apply contract, with the G5 activation
// (deer×6, rabbit×8, goat×2, wolf×1, bear×1, fish×8).
//
// D12: no global rand, no time.Now; per-tick RNG forked from base seed + tick.
// D11: continuous positions; NavAdapter wraps navmap for TerrainSampler.
package ecosim

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"

	"github.com/dogring/bdg/engine/env/climate"
	"github.com/dogring/bdg/engine/fauna"
	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/space/navmap"
	"github.com/dogring/bdg/engine/space/scent"
	"github.com/dogring/bdg/engine/space/spatial"
)

// ── World geometry constants ──────────────────────────────────────────────────
const (
	WorldMin    = 0.0   // world x/y lower bound
	WorldMax    = 100.0 // world x/y upper bound
	ScentCell   = 2.0   // scent grid cell edge (world units)
	NavCell     = 2.0   // navmap cell edge (world units)
	ScentSpread = 3     // prey/food scent bulk-pass cadence (every N ticks)
	ClimCadence = 60    // climate step cadence (every N ticks = 1 game-hour)
	FaunaDT     = 1.0   // locomotion DT magnitude (world units / tick)
	MagPred     = 1.0   // predator scent deposit magnitude
	MagPrey     = 0.5   // prey scent deposit magnitude
	MagFood     = 0.8   // food scent deposit magnitude

	initialStamina = 1.0
	initialVital   = 1.0
)

// Terrain layout thresholds (D11: continuous coordinates).
const (
	RiverXMin = 40.0 // river strip x=[40,50]
	RiverXMax = 50.0
	MtnXMin   = 65.0 // mountain region x≥65, y≥65
	MtnYMin   = 65.0
	SeaYMin   = 85.0 // sea strip y≥85
)

const (
	faunaDormantPeriod = 20
	faunaWakeCooldown  = 10

	spatialCellSize = 8.0

	soilBaseCost     = 1.0
	riverBaseCost    = 2.5
	mountainBaseCost = 2.0
	seaBaseCost      = 5.0

	navNoWear       = 0.0
	navWearMax      = 1.0
	navWearCostMin  = 0.5
	navCostFallback = 1.0

	climateGridCols     = 4
	climateGridRows     = 4
	initMoisture        = 0.3
	initTemperature     = 15.0
	rainProbPerHour     = 0.05
	rainHardCapHours    = 720
	rainDurMinHours     = 2
	rainDurMaxHours     = 12
	moistureRainRate    = 0.05
	evapBaseRate        = 0.01
	evapTempScale       = 0.001
	annualMidTemp       = 12.5
	annualTempAmplitude = 17.5
	annualPhaseOffset   = -math.Pi / 2
	tempDayPeak         = 5.0
	tempNightLow        = -3.0
	tempRainDrop        = 2.0
	windPrevailingDir   = math.Pi / 4
	windDirDrift        = 0.1
	windDirReversion    = 0.1
	windMagMean         = 0.5
	windMagNoise        = 0.05

	hungerDrainPerTick = 0.00005
	feedingRelief      = 0.05
	restFatigueRelief  = 0.03
	minDriveValue      = 0.0
	minVitalValue      = 0.0

	hoursPerDay  = 24
	hoursPerYear = 720

	faunaSeedMultiplier   int64 = 10007
	climateSeedMultiplier int64 = 20011
	tickSeedMultiplier    int64 = 1000003
	climateForkOffset     int64 = 1
)

var faunaCadence = fauna.Cadence{DormantPeriod: faunaDormantPeriod, WakeCooldown: faunaWakeCooldown}

// WorldState holds the complete mutable simulation state.
// world is the SOLE mutator (D12): all mutations happen inside TickOnce.
type WorldState struct {
	Tick      core.Tick
	Animals   []fauna.Animal // sorted by ObjectID (D12)
	ScentGrid *scent.Grid
	SpatHash  *spatial.SpatialHash
	Nav       *navmap.NavMap
	ClimState *climate.State
	ClimRules *climate.Rules
	Rules     *fauna.Rules
	FoodPts   []core.Vec2 // static food emitter positions
	baseSeed  int64
}

// ── TerrainSampler adapter (navmap → fauna.TerrainSampler, D11 / SPEC-world-fauna) ──

// NavAdapter adapts *navmap.NavMap to fauna.TerrainSampler (world-side adapter).
// World-boundary positions are treated as footprint-blocked (hard blocker for this
// harness — production world stamps actual wall footprints at the map border).
type NavAdapter struct{ Nav *navmap.NavMap }

func (a NavAdapter) FootprintBlocked(p core.Vec2) bool {
	if p.X < WorldMin || p.X >= WorldMax || p.Y < WorldMin || p.Y >= WorldMax {
		return true // world boundary ≡ hard blocker in this harness
	}
	return a.Nav.FootprintBlocked(a.Nav.CellOf(p))
}

func (a NavAdapter) TerrainAt(p core.Vec2) core.Tag {
	return a.Nav.TerrainAt(a.Nav.CellOf(p))
}

func (a NavAdapter) BaseCost(p core.Vec2) float64 {
	c := a.Nav.BaseCost(a.Nav.CellOf(p))
	if c <= 0 {
		return navCostFallback
	}
	return c
}

// Attrs: ecosim carries no §5 terrain attr table, so every `terrain.<attr>` §6 operand reads 0 here
// (PD10 off-lever — the harness is a movement/cost bed, not a habitat one).
func (a NavAdapter) Attrs(core.Vec2) map[core.Tag]float64 { return nil }

// TerrainAtPos maps a continuous position to its terrain tag (exported for test assertions).
func TerrainAtPos(p core.Vec2) core.Tag { return terrainAt(p) }

// terrainAt is the harness layout sampler (river strip / mountain corner / sea strip).
func terrainAt(p core.Vec2) core.Tag {
	if p.X >= RiverXMin && p.X <= RiverXMax {
		return "river"
	}
	if p.X >= MtnXMin && p.Y >= MtnYMin {
		return "mountain"
	}
	if p.Y >= SeaYMin {
		return "sea"
	}
	return "soil"
}

// ── Construction ─────────────────────────────────────────────────────────────

// NewWorldState builds the G5 ecosystem: deer×6, rabbit×8, goat×2, wolf×1, bear×1, fish×8.
// Terrain: soil plain + river strip [40,50] + mountain corner [65+,65+] + sea [y≥85].
// Food emitters placed in soil plain near herbivore positions.
func NewWorldState(seed int64) *WorldState {
	w := &WorldState{baseSeed: seed}

	// ── Navmap ────────────────────────────────────────────────────────────────
	types := map[navmap.TerrainID]navmap.TerrainType{
		"soil":     {BaseCost: soilBaseCost, Passable: true},
		"river":    {BaseCost: riverBaseCost, Passable: true},
		"mountain": {BaseCost: mountainBaseCost, Passable: true},
		"sea":      {BaseCost: seaBaseCost, Passable: true},
	}
	w.Nav = navmap.New(navmap.Config{
		CellSize: NavCell, MinX: WorldMin, MinY: WorldMin, MaxX: WorldMax, MaxY: WorldMax,
		WearOnUse: navNoWear, WearOnPave: navNoWear, WearDecay: navNoWear, WearMax: navWearMax, WearCostMin: navWearCostMin,
	}, terrainAt, types)

	// ── Climate ───────────────────────────────────────────────────────────────
	// Coarse grid; real wind+temperature so scent spreads downwind + thermal affects speed.
	w.ClimState = climate.New(climate.Config{
		GridCols: climateGridCols, GridRows: climateGridRows,
		WorldMin:     core.Vec2{X: WorldMin, Y: WorldMin},
		WorldMax:     core.Vec2{X: WorldMax, Y: WorldMax},
		InitMoisture: initMoisture, InitTemperature: initTemperature,
		RainProbPerHour: rainProbPerHour, RainHardCapHours: rainHardCapHours,
		RainDurMinHours: rainDurMinHours, RainDurMaxHours: rainDurMaxHours, MoistureRainRate: moistureRainRate,
		EvapBaseRate: evapBaseRate, EvapTempScale: evapTempScale,
		AnnualMid: annualMidTemp, AnnualAmp: annualTempAmplitude, AnnualPhase: annualPhaseOffset,
		TempDayPeak: tempDayPeak, TempNightLow: tempNightLow, TempRainDrop: tempRainDrop,
		WindPrevailingDir: windPrevailingDir,
		WindDirDrift:      windDirDrift, WindDirReversion: windDirReversion,
		WindMagMean: windMagMean, WindMagNoise: windMagNoise,
	}, terrainAt)
	w.ClimRules = climate.NewRules(nil) // no terrain transitions in this harness

	// ── Scent grid & spatial hash ─────────────────────────────────────────────
	w.ScentGrid = scent.New(ScentCell)
	w.SpatHash = spatial.New(spatialCellSize)

	// ── Fauna rules ───────────────────────────────────────────────────────────
	w.Rules = buildRules()

	// ── Food emitters (static, in soil plain) ─────────────────────────────────
	w.FoodPts = []core.Vec2{
		{X: 6, Y: 8}, {X: 15, Y: 22}, {X: 22, Y: 12}, {X: 26, Y: 38},
		{X: 16, Y: 60}, {X: 30, Y: 25}, {X: 20, Y: 50}, {X: 28, Y: 68},
	}

	// ── Animal placement (sorted by ID, D12) ──────────────────────────────────
	stats := map[core.StatID]float64{"Agility": 1.0}
	hd := func(h float64) map[fauna.DriveID]float64 { // herbivore drives
		return map[fauna.DriveID]float64{"hunger": h, "fatigue": 0, "fear": 0, "thermal": 0}
	}
	pd := func(h float64) map[fauna.DriveID]float64 { // predator drives
		return map[fauna.DriveID]float64{"hunger": h, "fatigue": 0, "thermal": 0}
	}
	fd := map[fauna.DriveID]float64{"fatigue": 0, "fear": 0, "thermal": 0} // fish drives

	mk := func(id core.ObjectID, sp fauna.SpeciesID, pos core.Vec2,
		drives map[fauna.DriveID]float64, act actions.ActionID) fauna.Animal {
		return fauna.Animal{
			ID: id, Species: sp, Pos: pos, Stats: stats, Drives: drives,
			Stamina: initialStamina, Vital: initialVital, CurrentAction: act,
		}
	}

	w.Animals = []fauna.Animal{
		// bear — apex omnivore near mountain
		mk("an:b1", "bear", core.Vec2{X: 72, Y: 72}, pd(0.2), "Hunt"),
		// deer — wary herbivores in soil plain
		mk("an:d1", "deer", core.Vec2{X: 5, Y: 10}, hd(0.1), "Graze"),
		mk("an:d2", "deer", core.Vec2{X: 10, Y: 25}, hd(0.1), "Graze"),
		mk("an:d3", "deer", core.Vec2{X: 20, Y: 15}, hd(0.1), "Graze"),
		mk("an:d4", "deer", core.Vec2{X: 25, Y: 40}, hd(0.1), "Graze"),
		mk("an:d5", "deer", core.Vec2{X: 20, Y: 65}, hd(0.1), "Graze"),
		mk("an:d6", "deer", core.Vec2{X: 15, Y: 70}, hd(0.1), "Graze"),
		// fish — aquatic; confined to river (impassable on soil/mountain/etc.)
		mk("an:f1", "fish", core.Vec2{X: 43, Y: 15}, fd, "MoveTo"),
		mk("an:f2", "fish", core.Vec2{X: 44, Y: 25}, fd, "MoveTo"),
		mk("an:f3", "fish", core.Vec2{X: 45, Y: 35}, fd, "MoveTo"),
		mk("an:f4", "fish", core.Vec2{X: 46, Y: 45}, fd, "MoveTo"),
		mk("an:f5", "fish", core.Vec2{X: 44, Y: 55}, fd, "MoveTo"),
		mk("an:f6", "fish", core.Vec2{X: 43, Y: 65}, fd, "MoveTo"),
		mk("an:f7", "fish", core.Vec2{X: 45, Y: 75}, fd, "MoveTo"),
		mk("an:f8", "fish", core.Vec2{X: 44, Y: 82}, fd, "MoveTo"),
		// goat — climbers near mountain
		mk("an:g1", "goat", core.Vec2{X: 70, Y: 70}, hd(0.1), "Graze"),
		mk("an:g2", "goat", core.Vec2{X: 75, Y: 78}, hd(0.1), "Graze"),
		// rabbit — twitchy herbivores in soil plain
		mk("an:r1", "rabbit", core.Vec2{X: 8, Y: 12}, hd(0.1), "Graze"),
		mk("an:r2", "rabbit", core.Vec2{X: 12, Y: 20}, hd(0.1), "Graze"),
		mk("an:r3", "rabbit", core.Vec2{X: 22, Y: 18}, hd(0.1), "Graze"),
		mk("an:r4", "rabbit", core.Vec2{X: 28, Y: 35}, hd(0.1), "Graze"),
		mk("an:r5", "rabbit", core.Vec2{X: 18, Y: 45}, hd(0.1), "Graze"),
		mk("an:r6", "rabbit", core.Vec2{X: 10, Y: 55}, hd(0.1), "Graze"),
		mk("an:r7", "rabbit", core.Vec2{X: 25, Y: 62}, hd(0.1), "Graze"),
		mk("an:r8", "rabbit", core.Vec2{X: 35, Y: 22}, hd(0.1), "Graze"),
		// wolf — apex predator in soil plain
		mk("an:w1", "wolf", core.Vec2{X: 15, Y: 35}, pd(0.2), "Hunt"),
	}
	// Ensure sorted by ID (D12). IDs are already lexicographic above; sort as guard.
	sort.Slice(w.Animals, func(i, j int) bool { return w.Animals[i].ID < w.Animals[j].ID })

	// Populate spatial hash with initial positions.
	for _, a := range w.Animals {
		w.SpatHash.Insert(a.ID, a.Pos)
	}

	return w
}

// ── Tick loop (plan → apply → scent; SPEC-world-fauna apply contract) ─────────

// TickOnce advances the simulation one tick (the full plan→apply→scent loop).
// D12: fauna.Step called with a deterministic per-tick RNG fork; apply in sorted ID order.
func (w *WorldState) TickOnce() {
	// Phase 2: PLAN — fauna.Step is a pure read-only transform over the frozen snapshot.
	snap := w.buildSnapshot()
	intents := fauna.Step(snap, w.Rules, faunaFork(w.baseSeed, w.Tick))

	// Phase 4: APPLY — move animals, commit state, layer action effects, vital check.
	w.applyIntents(intents)

	// Phase 4-ENV step 4: SCENT deposit/spread/commit (after apply = post-move positions).
	w.doScent()

	w.Tick++

	// Climate step on cadence (every ClimCadence ticks ≈ 1 game-hour).
	if int(w.Tick)%ClimCadence == 0 {
		w.stepClimate()
	}
}

// buildSnapshot assembles the fauna.Snapshot from frozen world state (plan phase).
func (w *WorldState) buildSnapshot() *fauna.Snapshot {
	// Pass a copy of Animals so fauna.Step cannot mutate world state (D12).
	animals := make([]fauna.Animal, len(w.Animals))
	copy(animals, w.Animals)
	return &fauna.Snapshot{
		Animals:       animals,
		Scent:         w.ScentGrid,
		Spatial:       w.SpatHash,
		Terrain:       NavAdapter{Nav: w.Nav},
		Env:           w.buildEnvSamples(),
		Tick:          w.Tick,
		Cadence:       faunaCadence,
		ScentCellSize: ScentCell,
		DT:            FaunaDT,
	}
}

// buildEnvSamples maps each animal's position to its climate sample (SPEC-world-fauna §EnvSample).
func (w *WorldState) buildEnvSamples() map[core.ObjectID]fauna.EnvSample {
	env := make(map[core.ObjectID]fauna.EnvSample, len(w.Animals))
	cw := w.ClimState.Wind()
	sw := scent.Wind{Dir: cw.Dir, Mag: cw.Mag} // climate.Wind → scent.Wind (same fields, different pkgs)
	for _, a := range w.Animals {
		cs := w.ClimState.CellAt(a.Pos) // CA3 read: continuous Pos → coarse climate cell
		env[a.ID] = fauna.EnvSample{
			Temperature: cs.Temperature,
			Moisture:    cs.Moisture,
			Wind:        sw,
		}
	}
	return env
}

// applyIntents executes the SPEC-world-fauna apply contract (step 2–5) in sorted ObjectID order.
// intents and w.Animals are both sorted by ID (D12), so they are co-indexed.
func (w *WorldState) applyIntents(intents []fauna.Intent) {
	for i, intent := range intents {
		a := &w.Animals[i] // co-indexed: intents[i].Animal == w.Animals[i].ID (D12)

		// Step 2: Move (spatial hash + position).
		w.SpatHash.Move(a.ID, intent.NextPos)
		a.Pos = intent.NextPos
		a.Heading = intent.NextHeading

		// Step 3: Commit fauna state (passive drive evolution from fauna.Step).
		a.Drives = intent.Drives
		a.Stamina = intent.Stamina
		a.ActiveUntil = intent.ActiveUntil
		a.CurrentAction = intent.Action

		// Step 4: Layer the enacted action's own drive Effect (world applies, fauna only returns passive).
		a.Drives = layerActionEffect(intent.Action, a.Drives, w.ScentGrid, a.Pos)

		// Step 5: Vital — slow hunger-driven drain (F3; animals survive well past 2000 ticks).
		if h, ok := a.Drives["hunger"]; ok {
			v := a.Vital - h*hungerDrainPerTick
			if v < minVitalValue {
				v = minVitalValue
			}
			a.Vital = v
		}
	}
}

// layerActionEffect applies the enacted action's own drive effect on top of passive drives.
// Source: SPEC-world-fauna apply contract step 4 (Eat→hunger↓, Rest→fatigue↓).
// Magnitudes are harness placeholders; production derives them from content effect blocks.
func layerActionEffect(act actions.ActionID, drives map[fauna.DriveID]float64,
	sg *scent.Grid, pos core.Vec2) map[fauna.DriveID]float64 {
	next := cloneDrives(drives)
	switch act {
	case "Graze", "Hunt":
		// Enacting a feeding action near a food/prey scent source reduces hunger.
		// Check whether there is meaningful scent at the current cell before applying.
		ch := scent.ChanFood
		if act == "Hunt" {
			ch = scent.ChanPrey
		}
		if sg.IntensityAt(ch, pos) > 0 {
			if h, ok := next["hunger"]; ok {
				v := h - feedingRelief
				if v < minDriveValue {
					v = minDriveValue
				}
				next["hunger"] = v
			}
		}
	case "Rest":
		// Resting reduces fatigue.
		if f, ok := next["fatigue"]; ok {
			v := f - restFatigueRelief
			if v < minDriveValue {
				v = minDriveValue
			}
			next["fatigue"] = v
		}
	}
	return next
}

// doScent drives the scent field: deposit (post-apply positions) → spread (on cadence) → commit.
// Implements SPEC-world-fauna §Scent driving (Phase 4-ENV step 4).
func (w *WorldState) doScent() {
	// Deposit predator scent EVERY tick (kept fresh for F45 wake, post-move positions).
	for _, a := range w.Animals {
		if w.Rules.IsPredator(a.Species) {
			w.ScentGrid.Deposit(scent.ChanPredator, a.Pos, MagPred)
		}
	}

	// Deposit prey + food scent on bulk cadence (tick % ScentSpread).
	if int(w.Tick)%ScentSpread == 0 {
		for _, a := range w.Animals {
			if !w.Rules.IsPredator(a.Species) {
				w.ScentGrid.Deposit(scent.ChanPrey, a.Pos, MagPrey)
			}
		}
		for _, fp := range w.FoodPts {
			w.ScentGrid.Deposit(scent.ChanFood, fp, MagFood)
		}
		// Wind-driven diffusion (isotropic when Wind.Mag==0; downwind when>0).
		cw := w.ClimState.Wind()
		w.ScentGrid.Spread(scent.Wind{Dir: cw.Dir, Mag: cw.Mag})
	}

	// Commit every tick → 1-tick latency (deposits at T visible to fauna.Step at T+1, F33).
	w.ScentGrid.Commit()
}

// stepClimate advances the climate state one game-hour.
func (w *WorldState) stepClimate() {
	absHour := int64(w.Tick) / ClimCadence
	f := climate.Forcing{
		AbsHour:      absHour,
		HourOfDay:    int(absHour % hoursPerDay),
		YearFraction: float64(absHour%hoursPerYear) / float64(hoursPerYear),
	}
	next, _ := climate.Step(w.ClimState, f, w.ClimRules, climateFork(w.baseSeed, w.Tick))
	w.ClimState = next
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// faunaFork returns a deterministic per-tick RNG for fauna.Step (D12: no global rand).
// Uses a distinct prime multiplier from climateFork to guarantee disjoint streams.
func faunaFork(seed int64, tick core.Tick) *rng.RNG {
	return rng.New(seed*faunaSeedMultiplier + int64(tick)*tickSeedMultiplier)
}

// climateFork returns a deterministic per-tick RNG for climate.Step (D12).
func climateFork(seed int64, tick core.Tick) *rng.RNG {
	return rng.New(seed*climateSeedMultiplier + int64(tick)*tickSeedMultiplier + climateForkOffset)
}

// cloneDrives returns a shallow copy of a drive map (no mutation of the original).
func cloneDrives(m map[fauna.DriveID]float64) map[fauna.DriveID]float64 {
	out := make(map[fauna.DriveID]float64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// DigestState returns a deterministic SHA-256 digest of the per-tick animal state
// (pos / vital / action / drives) for the determinism test (D12).
// Animals must be in sorted ID order; drive keys are sorted for determinism.
func DigestState(animals []fauna.Animal) string {
	h := sha256.New()
	for _, a := range animals {
		fmt.Fprintf(h, "%s|%.8f,%.8f|%.8f|%s", a.ID, a.Pos.X, a.Pos.Y, a.Vital, a.CurrentAction)
		// Sort drive keys for deterministic iteration (D12: never raw map-range for logic).
		keys := make([]string, 0, len(a.Drives))
		for k := range a.Drives {
			keys = append(keys, string(k))
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(h, "|%s=%.8f", k, a.Drives[fauna.DriveID(k)])
		}
		fmt.Fprint(h, "\n")
	}
	return hex.EncodeToString(h.Sum(nil))
}
