package fauna

import (
	"math"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/space/scent"
)

// defaultWanderAngle is the standard deviation (radians) of the heading
// perturbation drawn once per ACTIVE animal per tick (stochastic steering jitter).
// Named balance-tunable constant (like scent.spreadFraction); not a drive/rate/
// threshold/radius literal (D10 guard). Same seed ⇒ identical steering (D12).
const defaultWanderAngle = 0.15

const (
	minEffectiveTerrainCost = 1.0
)

// Step scores all animals and returns ONE Intent each, in sorted Animal-ObjectID
// order (D12). Pure function of (snap, rules, rng): never mutates snap (incl.
// snap.Animals, the scent grid, the spatial index) or Rules. Panics if snap.Env
// is missing a live animal entry (world-contract guard, mirrors flora/decay).
//
// For each animal in sorted ID order:
//  0. CADENCE/WAKE (F45): O(1) IntensityAt(ChanPredator, Pos); determine ACTIVE
//     or DORMANT; DORMANT may re-arbitrate on phase-gate tick.
//     1–4. Full pipeline: SENSE → DRIVES → SCORE → STEER.
//     Cheap path (DORMANT, off-boundary): hold Action, advance accumulators + decay
//     fear, cheap steer along Heading — NOT frozen.
//
// RNG is drawn ONLY in the STEER step, once per ACTIVE (or re-arb DORMANT) animal,
// in sorted ID order. DORMANT off-boundary animals make no draw (D12 byte-identity).
func Step(snap *Snapshot, rules *Rules, r *rng.RNG) []Intent {
	if len(snap.Animals) == 0 {
		return nil
	}

	sorted := sortAnimalsByID(snap.Animals)
	intents := make([]Intent, 0, len(sorted))

	for i := range sorted {
		a := sorted[i]

		// ── Step 0: CADENCE / WAKE (F45) ─────────────────────────────────────
		// O(1) predator intensity probe on COMMITTED buffer (next-tick latency, F33).
		// Torpor wake gate (SS3/FM12): a SLEEPING animal (CurrentAction is a state:sleep
		// action) wakes only to a predator scent ≥ SleepWakeScentThreshold — a deep sleeper
		// ignores a faint/distant predator. Non-sleeping ⇒ threshold 0 ⇒ any scent wakes (as before).
		predIntensity := snap.Scent.IntensityAt(scent.ChanPredator, a.Pos)
		wakeThreshold := scalarZero
		if rules.steerChannelFor(a.Species, a.CurrentAction) == TagSleep {
			wakeThreshold = snap.Cadence.SleepWakeScentThreshold
		}
		predBit := predIntensity > wakeThreshold
		isPred := rules.IsPredator(a.Species)

		newActiveUntil := a.ActiveUntil
		if predBit {
			// Wake: extend/set cooldown horizon (SPEC: "set ActiveUntil = Tick + WakeCooldown").
			candidate := snap.Tick + snap.Cadence.WakeCooldown
			if candidate > newActiveUntil {
				newActiveUntil = candidate
			}
		}

		isActive := isPred || predBit || a.EngagedWith != "" || (snap.Tick <= a.ActiveUntil)

		// DORMANT: check re-arbitration gate.
		isReArb := false
		if !isActive && snap.Cadence.DormantPeriod > 0 {
			ph := phase(a.ID, snap.Cadence.DormantPeriod)
			isReArb = (snap.Tick+ph)%core.Tick(snap.Cadence.DormantPeriod) == 0
		}

		// Lookup EnvSample — panic on missing entry (world-contract guard).
		env, ok := snap.Env[a.ID]
		if !ok {
			panic("fauna.Step: missing EnvSample for animal " + string(a.ID))
		}

		var intent Intent
		if isActive || isReArb {
			intent = fullPipeline(a, env, snap, rules, r, newActiveUntil)
		} else {
			intent = cheapPath(a, snap, rules, newActiveUntil)
		}
		intents = append(intents, intent)
	}

	return intents
}

// ── Full pipeline (ACTIVE or re-arbitrating DORMANT) ─────────────────────────

func fullPipeline(
	a Animal, env EnvSample, snap *Snapshot, rules *Rules, r *rng.RNG,
	newActiveUntil core.Tick,
) Intent {
	smellRadius, sightRadius, fovArc := rules.Senses(a.Species)

	// ── Step 1: SENSE ─────────────────────────────────────────────────────────
	// Scent: omni neighbor/upwind read (F34, scent-only, no FOV here).
	reading := snap.Scent.Read(a.Pos, smellRadius, env.Wind)

	// Sight: spatial forward-FOV predator query (F44(c-ii) / D11 / D12).
	// NearbyEntities is ObjectID-sorted (D12). Agents NOT classified in P_fa1 (F46).
	sightPred, distPred, nearPredPos, fleePredDir := sightQuery(a, snap, rules, sightRadius, fovArc)
	target := combatTarget(a, snap, rules, sightRadius)

	// ── Step 2: DRIVES (F25(c)) ───────────────────────────────────────────────
	// Pre-compute AppTemp once (uses stats + climate only; no circular dep on drives).
	// The animalContext for AppTemp uses pre-update drives + climate.
	appTemp := computeAppTemp(a, env, rules)

	// Build context for DriveUpdate with PRE-update drives.
	dCtx := &animalContext{
		animal:       &a,
		drives:       a.Drives,
		reading:      reading,
		sightPred:    sightPred,
		distPred:     distPred,
		smellRadius:  smellRadius,
		sightRadius:  sightRadius,
		env:          env,
		appTemp:      appTemp,
		targetThreat: target.threat,
	}
	newDrives := rules.DriveUpdate(a.Species, a.Drives, dCtx, snap.DT)

	// ── Step 3: SCORE (F1/F26) ────────────────────────────────────────────────
	// Build §6 context with POST-update drives (SPEC: "build the §6 Context once").
	sCtx := &animalContext{
		animal:       &a,
		drives:       newDrives,
		reading:      reading,
		sightPred:    sightPred,
		distPred:     distPred,
		smellRadius:  smellRadius,
		sightRadius:  sightRadius,
		env:          env,
		appTemp:      appTemp,
		targetThreat: target.threat,
	}

	bestAction, _ := scoreAction(a, rules, sCtx)

	combat := resolveCombat(a, target, bestAction, snap, rules, sCtx, r)

	// ── Step 4: STEER (F35) ───────────────────────────────────────────────────
	wander := scalarZero
	if combat.engagedWith == "" {
		// Stochastic wander draw (once per ACTIVE/re-arb animal that locomotes, in sorted ID order, D12).
		wander = r.NormFloat64() * defaultWanderAngle
	}

	nextPos, nextHeading := steerFull(a, env, bestAction, reading, nearPredPos, fleePredDir, sightPred,
		snap, rules, sCtx, wander, smellRadius, sightRadius)
	if combat.engagedWith != "" {
		nextPos = a.Pos
		nextHeading = a.Heading
	}
	flushRadius := snap.Combat.HiddenFlushFactor * snap.ScentCellSize
	flushed := sightPred > scalarZero && distPred <= flushRadius
	if a.HiddenUntil > 0 && a.HiddenUntil >= snap.Tick && !flushed {
		nextPos = a.Pos
		nextHeading = a.Heading
	}

	return Intent{
		Animal:               a.ID,
		Action:               bestAction,
		Target:               combat.target,
		NextPos:              nextPos,
		NextHeading:          nextHeading,
		Drives:               newDrives,
		Stamina:              nextStamina(a, combat.engagedWith != "", snap.DT, snap.Combat),
		Vital:                regenVital(a, snap.DT, snap.Combat),
		VitalCap:             effectiveVitalCap(a),
		ActiveUntil:          newActiveUntil,
		EngagedWith:          combat.engagedWith,
		NextExchangeTick:     combat.nextExchangeTick,
		EngageCooldownUntil:  combat.engageCooldownUntil,
		Damage:               combat.damage,
		TargetVitalCapDamage: combat.targetVitalCapDamage,
	}
}

// sightQuery performs the F44 forward-FOV spatial predator query.
// Returns (sightPred, distPred, nearestPredPos, fleeDir):
//   - sightPred = 1.0 if a predator animal is within FOV, else 0.0
//   - distPred  = distance to nearest qualifying predator, or sightRadius if none
//   - nearPredPos: pointer to the nearest qualifying predator position (nil if none)
//   - fleeDir: aggregated unit flee direction over ALL visible predators (M7), non-nil
//     ONLY when ≥2 predators are in FOV and their repulsion sum is non-degenerate; nil
//     for ≤1 (the single-target away-from-nearest path is used, byte-identical to pre-M7).
//
// Iterates NearbyEntities (ObjectID-sorted, D12). Only checks entities that are
// also in snap.Animals (P_fa1 — agents NOT classified, F46). Bearing test uses
// continuous angle (D11); rear blind spot: |bearing − Heading| > fovArc.
func sightQuery(a Animal, snap *Snapshot, rules *Rules, sightRadius, fovArc float64) (float64, float64, *core.Vec2, *core.Vec2) {
	if sightRadius <= scalarZero {
		return scalarZero, sightRadius, nil, nil
	}

	// Build a temporary ObjectID → index map for snap.Animals (key lookup, not iteration).
	// This is built per-animal on the full pipeline; O(N) per-animal is acceptable for P_fa1.
	// We iterate snap.Animals once to build the map, then do O(1) key lookups.
	animalIdx := make(map[core.ObjectID]int, len(snap.Animals))
	for i := range snap.Animals {
		animalIdx[snap.Animals[i].ID] = i
	}

	nearby := snap.Spatial.NearbyEntities(a.Pos, sightRadius)
	// NearbyEntities is already ObjectID-sorted (spatial.NearbyEntities guarantee, D12).

	bestDist := sightRadius
	var bestID core.ObjectID
	var bestPos *core.Vec2
	found := false

	// M7: accumulate a distance-weighted repulsion sum over EVERY visible predator so that
	// when ≥2 are in FOV the prey flees the resultant threat field (sideways out of a pincer)
	// rather than straight away from the single nearest. Summed in nearby's ObjectID order
	// (deterministic float accumulation, D12).
	var sumX, sumY float64
	predCount := 0

	for _, ent := range nearby {
		if ent.ID == a.ID {
			continue // skip self
		}
		idx, isAnimal := animalIdx[ent.ID]
		if !isAnimal {
			continue // skip non-animals (objects, agents — P_fa1 scope)
		}
		other := snap.Animals[idx]
		if !rules.IsPredator(other.Species) {
			continue
		}

		// Bearing from a to other (D11 — continuous angle).
		dx := ent.Pos.X - a.Pos.X
		dy := ent.Pos.Y - a.Pos.Y
		bearing := math.Atan2(dy, dx)
		diff := math.Abs(angularDiff(bearing, a.Heading))
		if diff > fovArc {
			continue // rear blind spot
		}

		dist := math.Sqrt(dx*dx + dy*dy)
		if dist > sightRadius/(scalarOne+other.Concealment) {
			continue // M5-b: concealed predators are seen only at reduced range.
		}

		// M7 repulsion contribution: (Pos − predᵢ)/distᵢ² — a unit away-vector scaled by
		// 1/dist (p=2 in Σ(Pos−pred)/dist^p; nearer predators dominate, a 2nd nearby one
		// bends the escape sideways). Only consumed when predCount ≥ 2.
		predCount++
		if dist > scalarZero {
			inv := scalarOne / (dist * dist)
			sumX += -dx * inv // −dx = a.Pos.X − ent.Pos.X (away from predator)
			sumY += -dy * inv
		}

		if !found || dist < bestDist || (dist == bestDist && ent.ID < bestID) {
			bestDist = dist
			bestID = ent.ID
			pos := ent.Pos
			bestPos = &pos
			found = true
		}
	}

	// M7: aggregated flee direction, only when ≥2 predators are visible and their weighted
	// repulsion does not cancel (a symmetric pincer sums to ~0 → nil, falls back to nearest).
	var fleeDir *core.Vec2
	if predCount >= 2 {
		if mag := math.Sqrt(sumX*sumX + sumY*sumY); mag > scalarZero {
			fleeDir = &core.Vec2{X: sumX / mag, Y: sumY / mag}
		}
	}

	if found {
		return scalarOne, bestDist, bestPos, fleeDir
	}
	return scalarZero, sightRadius, nil, nil
}

// computeAppTemp evaluates the AppTemp program using a climate-only context.
// Uses pre-update drives + climate (no circular dependency on scent/sight/drives).
func computeAppTemp(a Animal, env EnvSample, rules *Rules) float64 {
	ctx := &animalContext{
		animal:  &a,
		drives:  a.Drives,
		reading: scent.Reading{}, // zero: no scent for AppTemp computation
		env:     env,
	}
	return rules.AppTemp(a.Species, ctx)
}

// scoreAction evaluates all candidate actions and returns the best (highest
// utility) action and its score. Ties broken by lexicographically-smaller
// ActionID (D12). Returns (CurrentAction, -∞) if no candidates.
func scoreAction(a Animal, rules *Rules, ctx *animalContext) (actions.ActionID, float64) {
	candidates := rules.Candidates(a.Species)
	if len(candidates) == 0 {
		return a.CurrentAction, math.Inf(-1)
	}

	var bestAction actions.ActionID
	bestScore := math.Inf(-1)
	first := true

	// Candidates are in sorted order (D12 — NewRules guarantees this).
	for _, act := range candidates {
		ctx.isCurrent = (act == a.CurrentAction)
		score := rules.Utility(a.Species, act, ctx)
		if first || score > bestScore || (score == bestScore && act < bestAction) {
			bestScore = score
			bestAction = act
			first = false
		}
	}
	return bestAction, bestScore
}

// steerFull computes NextPos and NextHeading for the full pipeline (ACTIVE/re-arb).
// Direction is determined by the steer-channel tag of the chosen action (D4):
//   - TagSteerFood   → toward food scent Dir
//   - TagSteerPrey   → toward prey scent Dir
//   - TagFleePred    → away from predator (aggregated fleePredDir if ≥2 visible, else away from nearPredPos)
//   - TagWaryPred    → slowly away from predator
//   - TagNoLoco      → no movement (NextPos == Pos, speed = 0 regardless of §6)
//   - ""             → continue along current Heading (random walk)
//
// A stochastic wander angle is added to the heading (D12: drawn once per animal,
// same seed ⇒ identical). NextPos is TerrainSampler-clamped: blocked iff
// FootprintBlocked OR !TerrainCost(species,terrain).passable.
func steerFull(
	a Animal, env EnvSample, act actions.ActionID,
	reading scent.Reading, nearPredPos, fleePredDir *core.Vec2,
	sightPred float64,
	snap *Snapshot, rules *Rules, ctx *animalContext,
	wander, smellRadius, sightRadius float64,
) (nextPos core.Vec2, nextHeading float64) {
	// Check steer channel from Rules (content-defined, D4/D10).
	tag := rules.steerChannelFor(a.Species, act)

	// TagNoLoco/TagSleep or zero-speed actions: Rest/Sleep → NextPos == Pos.
	if tag == TagNoLoco || tag == TagSleep {
		return a.Pos, a.Heading
	}

	// Evaluate §6 speed (F35).
	speed := rules.Speed(a.Species, ctx)
	if speed <= scalarZero {
		// Speed program returned 0 (e.g., authored Rest-equivalent).
		return a.Pos, a.Heading
	}

	// FM9 locomotion deadband (P_move-realism, docs/plans/fauna.md §4.3): below the deadband the animal
	// HOLDS position rather than crawling — §6 speed already encodes drive (fear/hunger ↑ ⇒ speed ↑), so a
	// sub-deadband speed means no salient drive ("mostly doesn't move", energy conservation). The threshold
	// is the species' own `move_deadband` when authored, else the global Snapshot.MoveDeadband (FM14a); ≤ 0
	// ⇒ OFF (byte-identical). Burst-rest UNDER drive is the fatigue axis (M2), not here.
	if db := rules.moveDeadband(a.Species, snap.MoveDeadband); db > scalarZero && speed < db {
		return a.Pos, a.Heading
	}

	// Resolve base direction from steer channel. For the thirst-seek channel the direction is the
	// water-attraction field Gradient at Pos (toward the nearest drinkable water, FM4); queried ONLY for
	// that channel so non-drinking species never touch the field (byte-identical to pre-FM4).
	var waterDir *core.Vec2
	if tag == TagSteerWater && snap.WaterField != nil {
		if g := snap.WaterField.Gradient(a.Pos); g.X != scalarZero || g.Y != scalarZero {
			waterDir = &g
		}
	}
	dir := baseSteerDir(a, tag, reading, nearPredPos, fleePredDir, sightPred, waterDir)

	// Thin always-on HAZARD-REPULSION blend (P_move1/FM5, "a-backbone + bounded blend"):
	// add e·Repulsion to bend the chosen direction away from dangerous terrain. Repulsion already
	// scales with local danger SEVERITY + proximity (deep water/cliff push harder than a shallow
	// bank); it is a continuous COST, not a block — a strong flee/drive out-pulls it. nil field or
	// e ≤ 0 ⇒ no bend (byte-identical to pre-P_move1). Only the resulting ANGLE is used below.
	if e := rules.hazardAvoidance(a.Species); e > scalarZero && snap.HazardField != nil {
		rep := snap.HazardField.Repulsion(a.Pos)
		dir = core.Vec2{X: dir.X + rep.X*e, Y: dir.Y + rep.Y*e}
	}

	// Apply angular jitter to heading (stochastic wander, D12), then cap turn
	// rate when authored (M6). turn_rate <= 0 means unlimited/off-neutral.
	desired := math.Atan2(dir.Y, dir.X) + wander
	if tr := rules.TurnRate(a.Species, ctx); tr > scalarZero {
		if maxTurn := tr * snap.DT; math.Abs(angularDiff(desired, a.Heading)) > maxTurn {
			desired = a.Heading + math.Copysign(maxTurn, angularDiff(desired, a.Heading))
		}
	}
	heading := desired
	dir = core.Vec2{X: math.Cos(heading), Y: math.Sin(heading)}

	// Evaluate terrain at tentative next position.
	tentative := a.Pos.Add(dir.Scale(speed * snap.DT))
	terrain := snap.Terrain.TerrainAt(tentative)
	mult, passable := rules.TerrainCost(a.Species, terrain)
	blocked := snap.Terrain.FootprintBlocked(tentative) || !passable

	if blocked {
		// Cannot enter this tick: HOLD position but COMMIT the turned heading (FM13, docs/plans/fauna.md
		// §4.4). Freezing the heading here made an animal that steered into impassable terrain (deep sea /
		// cliff) re-propose the SAME blocked step every tick — pinned against the obstacle. Returning the
		// already-turned `heading` (wander + hazard-repulsion blend + turn-rate applied above) lets it keep
		// rotating off the obstacle so the next tick re-evaluates from a new heading. Position unchanged.
		return a.Pos, heading
	}

	// Effective cost: BaseCost × species mult (W10b).
	baseCost := snap.Terrain.BaseCost(tentative)
	effectiveCost := baseCost * mult
	if effectiveCost < minEffectiveTerrainCost {
		effectiveCost = minEffectiveTerrainCost
	}

	// Actual speed modulated by terrain cost (higher cost = slower effective movement).
	// speed is in world units / DT (already from §6); terrain cost scales it inversely.
	effectiveSpeed := speed / effectiveCost
	nextPos = a.Pos.Add(dir.Scale(effectiveSpeed * snap.DT))
	nextHeading = heading

	return nextPos, nextHeading
}

// baseSteerDir returns the base steering direction vector for the chosen action's
// steer-channel tag. All directions are derived from sense data (D11 — continuous).
// Zero vector fallback → continue along heading (caller applies wander to heading).
func baseSteerDir(
	a Animal, tag core.Tag,
	reading scent.Reading, nearPredPos, fleePredDir *core.Vec2, sightPred float64,
	waterDir *core.Vec2,
) core.Vec2 {
	switch tag {
	case TagSteerFood:
		if reading.Food.Intensity > scalarZero {
			return reading.Food.Dir
		}
	case TagSteerWater:
		// Thirst-seek: toward the water-attraction field Gradient (FM4). nil (no field / flat) ⇒
		// fall through to continue-heading (no spurious pull when no water is reachable).
		if waterDir != nil {
			return *waterDir
		}
	case TagSteerPrey:
		if reading.Prey.Intensity > scalarZero {
			return reading.Prey.Dir
		}
	case TagFeed:
		if reading.Carrion.Intensity > scalarZero {
			return reading.Carrion.Dir
		}
	case TagFleePred, TagWaryPred:
		// Flee / wary: away from predator. Sight has priority over scent.
		if sightPred > scalarZero {
			// M7: with ≥2 predators visible, sightQuery supplies the aggregated repulsion
			// direction (flee the threat field — sideways out of a pincer). With ≤1 it is
			// nil and we fall back to away-from-nearest (byte-identical to pre-M7).
			if fleePredDir != nil {
				return *fleePredDir
			}
			if nearPredPos != nil {
				// Away from the single nearest visible predator (continuous, D11).
				dx := a.Pos.X - nearPredPos.X
				dy := a.Pos.Y - nearPredPos.Y
				mag := math.Sqrt(dx*dx + dy*dy)
				if mag > scalarZero {
					return core.Vec2{X: dx / mag, Y: dy / mag}
				}
			}
		}
		if reading.Predator.Intensity > scalarZero {
			// Scent Dir points TOWARD source; reverse for flee.
			return core.Vec2{X: -reading.Predator.Dir.X, Y: -reading.Predator.Dir.Y}
		}
	}
	// Default: continue along current Heading.
	return core.Vec2{X: math.Cos(a.Heading), Y: math.Sin(a.Heading)}
}
