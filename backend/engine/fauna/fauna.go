// Package fauna is the reduced-reactive animal controller: a pure, deterministic
// per-tick horizon-1 utility arbitration over the read-only world snapshot that
// produces one Intent per Animal. It scores every candidate atomic action with a
// §6 utility Program, picks the max (ties by sorted ActionID), and emits the
// chosen action + steered next position/heading + per-tick drive evolution.
//
// DORMANT animals skip full re-arbitration (hold CurrentAction + cheap steering)
// and wake the instant predator scent reaches their cell (F45). No planner, no
// ToM, no value stack: a single real-stat channel, drives, and a scent grid.
//
// D12 determinism: pure function of (snap, Rules, rng). Same inputs + fork ⇒
// byte-identical []Intent. No time.Now(), no global rand, no wall-clock; rng
// drawn ONLY in the steer step (wander jitter), once per ACTIVE animal, in sorted
// ObjectID order.
//
// Forbidden imports (one-way wiring): engine/world, engine/agent,
// engine/mind/planner, engine/space/navmap, engine/env/climate, engine/mind/needs,
// engine/mind/stats, engine/mind/tom, engine/mind/values, engine/mind/gates,
// engine/mind/perception.
package fauna

import (
	"sort"

	"github.com/dogring/bdg/engine/kernel/core"
	"github.com/dogring/bdg/engine/kernel/expr"
	"github.com/dogring/bdg/engine/kernel/rng"
	"github.com/dogring/bdg/engine/mind/actions"
	"github.com/dogring/bdg/engine/space/scent"
	"github.com/dogring/bdg/engine/space/spatial"
)

// ── Identity & state ──────────────────────────────────────────────────────────

// SpeciesID names a fauna species from content/objects.yaml object_kinds carrying a
// `fauna:` block (e.g. "deer", "wolf"). core.Tag underlying so the content catalog
// validates it; fauna never parses YAML (D10). flora.SpeciesID parity (F42).
type SpeciesID = core.Tag

// DriveID is an open drive key (F25/F29(ii)/D10 — a new drive is content data,
// StatID/Stats parity). Its value is ALSO its §6 Attr operand name (lowercase, F27
// — Attr("hunger") → Drives["hunger"]).
type DriveID = core.Tag

// Animal holds one live animal's fauna-owned dynamic state (F29). Pos is the
// continuous world coordinate (D11). Stats is the open base-attribute vector,
// READ-ONLY here (D7: stat training/aging is a cross-cutting stats/lifecycle
// concern). Drives is the open per-drive scalar vector ∈ [0,1] (F29(ii)).
// Heading is the continuous steering direction (radians) and the FOV reference
// axis (F44). CurrentAction backs the §6 is_current stickiness term (F30/F45),
// NOT an FSM state (D3). ActiveUntil is the tick THROUGH which a predator-scent
// wake keeps the animal ACTIVE (the F45 cooldown); 0 ⇒ no cooldown running.
type Animal struct {
	ID                  core.ObjectID
	Species             SpeciesID
	Pos                 core.Vec2               // continuous (D11)
	Stats               map[core.StatID]float64 // open base-attribute vector, READ-ONLY (D7)
	Drives              map[DriveID]float64     // open drive vector ∈ [0,1] (F29(ii))
	Stamina             float64
	Vital               float64          // single vital (F3); world owns death (§7)
	VitalCap            float64          // upper bound for Vital regen after combat scars; 0 means full cap
	Heading             float64          // steering direction (radians); FOV reference axis (F44)
	CurrentAction       actions.ActionID // last chosen action — §6 stickiness operand, NOT FSM (F30/D3)
	ActiveUntil         core.Tick        // F45 wake-cooldown horizon; 0 = none. world commits it.
	EngagedWith         core.ObjectID    // combat partner; empty means free
	NextExchangeTick    core.Tick        // next tick an engaged attack may propose damage
	EngageCooldownUntil core.Tick        // next tick an engage attempt may be made
	HiddenUntil         core.Tick        // hidden while >0 and >= current tick; SINGLE WRITER = engine/world (M3)
}

// EnvSample is the per-animal exogenous climate world samples injected each tick.
// fauna is a pure transform over VALUES — it does NOT import climate. In P1
// climate is OFF → all fields are neutral → apparent_temp neutral → thermal
// drive stays 0 + scent spread stays local (F10/F21/F33).
type EnvSample struct {
	Temperature float64    // §6 operand "temperature"; neutral in P1
	Moisture    float64    // §6 operand "moisture"; neutral in P1
	Wind        scent.Wind // {Dir radians, Mag} (operands "wind.dir"/"wind.mag"); zero in P1
}

// TerrainSampler is the read-only navmap view fauna declares and world adapts
// (dependency inversion). fauna must NOT import engine/space/navmap (F35/D11).
// Species affinity is applied controller-side via Rules.TerrainCost (W10b).
//   - FootprintBlocked: true ONLY for hard blockers (walls/building footprints), ALL species.
//   - TerrainAt: the terrain type id at p (D11 index read).
//   - BaseCost: the navmap base terrain cost at p (species-independent, ≥1).
//     Effective traversal cost = BaseCost × Rules.TerrainCost(species,terrain).mult.
type TerrainSampler interface {
	FootprintBlocked(p core.Vec2) bool
	TerrainAt(p core.Vec2) core.Tag
	BaseCost(p core.Vec2) float64
}

// Cadence carries the F45 adaptive per-animal cadence parameters (balance data,
// world-injected; NOT per-species). DormantPeriod is the N in the dormant
// re-arbitration gate (Tick+phase(ID))%N==0 (N≈100). WakeCooldown is how many
// ticks a predator-scent wake keeps an animal ACTIVE before it may revert to
// DORMANT. phase(ID) is FNV-1a of the ObjectID bytes mod DormantPeriod —
// deterministic + cross-process stable (D12) — spreading dormant re-evals.
type Cadence struct {
	DormantPeriod int       // ≥ 1; the dormant re-arbitration period N
	WakeCooldown  core.Tick // ≥ 0; ticks an animal stays ACTIVE after a predator-scent wake
}

// CombatParams carries balance-authored combat timing/range/regeneration values.
// Zero values are neutral until platform/config wires real content values.
type CombatParams struct {
	ExchangeMinTicks       int
	ExchangeMaxTicks       int
	EngageCooldownMinTicks int
	EngageCooldownMaxTicks int
	DisengageRangeFactor   float64 // multiplied by Snapshot.ScentCellSize
	StaminaDropThreshold   float64
	StaminaDrainPerTick    float64 // stamina spent per tick while engaged in combat (FC6 / scenario #8)
	StaminaRecoverPerTick  float64 // stamina regained per tick while not engaged
	FatiguePursuitPerTick  float64 // fatigue gained per tick during a high-effort chase/flight (M2 endurance)
	FatigueRecoverPerTick  float64 // fatigue shed per tick while resting/low-effort
	VitalRegenPerTick      float64
	VitalCapDamageFraction float64
	HiddenFlushFactor      float64 // × Snapshot.ScentCellSize = flush radius for hidden prey detection (M3)
	HideDurationTicks      int     // ticks a prey stays hidden after a successful world-side hide roll (M3)
	HideCoverFactor        float64 // × ScentCellSize = cover reach for world nearCoverFlora (M3)
	CoverRadiusFactor      float64 // × plant Width = cover-drag radius for world-side resistance (M4-b)
}

// Snapshot is the read-only world view the controller scores over (the read phase;
// parallel-safe — each animal's evaluation reads only immutable/snapshot state, D12
// plan phase). A missing live-animal entry in Env is a world-contract bug (panic,
// mirrors flora's missing SiteInput).
type Snapshot struct {
	Animals       []Animal
	Scent         *scent.Grid
	Spatial       *spatial.SpatialHash
	Terrain       TerrainSampler
	Env           map[core.ObjectID]EnvSample // per-animal; missing entry ⇒ panic
	Tick          core.Tick
	Cadence       Cadence
	Combat        CombatParams
	ScentCellSize float64
	DT            float64 // locomotion time-step magnitude for one tick (world/balance)
}

// Intent is the controller's per-animal output (ONE per animal, F1/F41). world
// applies it in the combined agent+animal sorted-ObjectID apply order (F41). The
// proposed Drives are the PASSIVE per-tick evolution (F25(c)); the action's own
// drive Effect is layered by world when it enacts the action.
type Intent struct {
	Animal               core.ObjectID
	Action               actions.ActionID    // max-utility action (ACTIVE/re-eval) or held CurrentAction (dormant)
	Target               core.ObjectID       // resolved target for targeted action; empty in P_fa1
	NextPos              core.Vec2           // steered next position (continuous, D11); == Pos if Rest/blocked
	NextHeading          float64             // steered next heading (radians)
	Drives               map[DriveID]float64 // passive per-tick drive evolution (F25(c)); world commits it
	Stamina              float64             // proposed next stamina
	Vital                float64             // proposed self vital regen, clamped by VitalCap
	VitalCap             float64             // proposed self vital cap
	ActiveUntil          core.Tick           // updated F45 wake-cooldown horizon; world commits it
	EngagedWith          core.ObjectID       // proposed combat partner; empty means disengaged/free
	NextExchangeTick     core.Tick           // proposed next exchange tick
	EngageCooldownUntil  core.Tick           // proposed next engage attempt tick
	Damage               float64             // damage world applies to Target; 0 means no exchange this tick
	TargetVitalCapDamage float64             // permanent VitalCap reduction world applies to Target
}

// ── AttrOperands ─────────────────────────────────────────────────────────────

// AttrOperands returns the fixed controller-resolved §6 Attr operand vocabulary
// (sorted, de-duplicated). platform/config cross-checks each compiled program's
// expr.ReadsAttrs() against this set ∪ the species' drive ids at load. Excludes
// the Stat channel (validated against the stats registry) and drive ids (from the
// species `fauna:` block, open content D10). Deterministic.
//
// Note: drive ids (hunger, fear, thermal, fatigue, repro_readiness, …) are ALSO
// Attr operands (F27) but are species-content-defined and thus not in this fixed
// set — the cross-check union includes them separately.
func AttrOperands() []core.Tag {
	// Fixed list, pre-sorted alphabetically (D12).
	fixed := []core.Tag{
		"apparent_temp",
		"dist.food",
		"dist.predator",
		"dist.prey",
		"is_current",
		"moisture",
		"scent.carrion",
		"scent.food",
		"scent.predator",
		"scent.prey",
		"sight.predator",
		"target.threat",
		"temperature",
		"wind.dir",
		"wind.mag",
	}
	out := make([]core.Tag, len(fixed))
	copy(out, fixed)
	return out
}

// ── compile-time interface check ──────────────────────────────────────────────

// Ensure animalContext satisfies expr.Context (defined in context.go).
var _ expr.Context = (*animalContext)(nil)

// Ensure Step is callable (signature check — body in step.go).
var _ = Step

// suppress unused-import errors for packages used only in type literals.
var (
	_ *rng.RNG             = nil
	_ *spatial.SpatialHash = nil
)

// sortAnimalsByID returns a sorted copy of animals (D12 — never drives logic by
// input order). Called at the top of Step so shuffling snap.Animals is neutral.
func sortAnimalsByID(animals []Animal) []Animal {
	sorted := make([]Animal, len(animals))
	copy(sorted, animals)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ID < sorted[j].ID
	})
	return sorted
}
