# SPEC — `engine/fauna`

> Status: `DRAFT`
> Leaf level: `L4` (flat, beside `agent`)  ·  Owner agent: `<filled by implementer>`
> Scope: **P_fa1 core** (`docs/plans/fauna.md §2`) — later fauna-side phase deltas live in the
> sub-specs below. Module sub-SPEC: `backend/engine/space/scent/SPEC.md` (the scent grid).

## Sub-specs (phase-delta decomposition)

| File | Scope |
|------|-------|
| [`SPEC-combat.md`](SPEC-combat.md) | **FC1–FC13** Combat & Predation (phase 6b, 클러스터 9): `Attack`/`Feed` actions · engage→exchange→kill→feed loop · combat `Animal`/`Snapshot`/`Intent` fields · `CombatParams` · `target.threat`/`scent.carrion` operands · Feed/Graze feeding + diet-tag matching |
| [`SPEC-cover.md`](SPEC-cover.md) | **M3 / M4-b / M5-b** cover mechanics (fauna-realism, 클러스터 10): cover-hiding (`HiddenUntil`, flush/crouch/bolt) · cover speed resistance (`CoverCost`) · ambush concealment (`Concealment` sight reduction) |
| [`SPEC-steering.md`](SPEC-steering.md) | **M6 / M7** steering deltas (fauna-realism, 클러스터 10): turn-rate inertia (`TurnRate` heading clamp) · multi-predator aggregated flee direction |

This file is the **entry point** — the **P_fa1 core contract** (types, the `Step` pipeline, the
core `Rules` accessors, invariants, ACs). The sub-specs are **interface deltas over this core**,
each with its own OFF-neutral lever; where a core struct carries later-phase fields, a pointer
comment below names the owning sub-spec. Implementing/reviewing the P_fa1 core needs only this
file; combat/cover/steering work reads this file **plus the owning sub-spec** (world-side apply
counterparts: `backend/engine/world/SPEC-world-fauna.md`).

## Purpose
The **reduced-reactive animal controller**: a pure, deterministic per-tick **horizon-1 utility
arbitration** over the *read-only* world snapshot that produces **one intent per `Animal`** — for each
ACTIVE animal it scores every candidate atomic action (from the shared `engine/mind/actions` registry)
with that (species × action)'s §6 utility `Program`, picks the max (ties by sorted `ActionID`), and
emits the chosen action + targeting + the steered next position/heading + the per-tick drive evolution.
DORMANT animals skip the expensive re-arbitration (hold `CurrentAction` + cheap steering) and wake the
instant predator scent reaches their cell (F45, adaptive per-animal cadence). It owns *how an animal
decides and moves and smells* — **no planner, no ToM, no value stack** (F1/F2/F3): a single real-stat
channel, drives, and a scent grid. It does **not** own the combined agent+animal **apply order** or the
scent bulk-pass cadence (those are `engine/world`'s, F41), the navmap/climate state it reads (injected
as values / via a declared sampler), or the object/animal mutation itself (`engine/world` is the sole
mutator, D12 apply phase). No IO, no wall-clock, no global rand: every output is a function of
`(snapshot, Rules, rng)` (D12). Mirrors `engine/env/flora`'s and `engine/env/decay`'s "pure read →
return delta/intent → world applies" shape exactly. Concept & rationale: `docs/core/design.md §5`
(continuous coords / dynamic terrain) + `§6` (the shared §6 evaluator) + `§7` (object-mortality) +
`docs/plans/fauna.md` (§0 locked, §1 F1–F46 ALL RESOLVED — binding; do NOT re-decide).

## Public Interface
```go
package fauna

import (
    "github.com/dogring/bdg/engine/kernel/core"
    "github.com/dogring/bdg/engine/kernel/expr"
    "github.com/dogring/bdg/engine/kernel/rng"
    "github.com/dogring/bdg/engine/mind/actions"
    "github.com/dogring/bdg/engine/space/spatial"
    "github.com/dogring/bdg/engine/space/scent"
)

// ── Identity & state ──────────────────────────────────────────────────────────────

// SpeciesID names a fauna species from content/objects.yaml object_kinds carrying a `fauna:` block
// (e.g. "deer", "wolf"). core.Tag underlying so the content catalog validates it; fauna never parses
// YAML (D10). flora.SpeciesID parity (F42).
type SpeciesID = core.Tag

// DriveID is an OPEN drive key (F25/F29(ii)/D10 — a new drive is content data, StatID/Stats parity).
// Base vocabulary: hunger / fear / thermal / fatigue / repro_readiness (F19/F25); a drive id is ALSO
// its §6 Attr operand name (lowercase, F27 — Attr("hunger") → Drives["hunger"]).
type DriveID = core.Tag

// Animal is one live animal's fauna-owned dynamic state (F29). Pos is the continuous world coordinate
// (D11 — never snapped to a scent cell). Stats is the open base-attribute vector
// (map[core.StatID]float64, the same shape as engine/mind/stats.Stats); fauna does NOT import
// engine/mind/stats (architecture import set: core/expr/rng/actions/spatial) — it is the raw open map,
// READ-ONLY here (D7: stat training/aging is a cross-cutting stats/lifecycle concern; §6 only reads it).
// Drives is the open per-drive scalar vector ∈ [0,1] (F29(ii)). Vital is the single mortality scalar
// (F3). Heading is the continuous steering direction (radians) and the FOV reference axis (F44).
// CurrentAction is the last-chosen action — it backs the §6 `is_current` stickiness term (F30/F45), NOT
// an FSM state (D3). ActiveUntil is the tick THROUGH which a recent predator-scent wake keeps the animal
// ACTIVE (the F45 wake cooldown); 0 ⇒ no cooldown running. world commits it from the Intent.
type Animal struct {
    ID            core.ObjectID
    Species       SpeciesID
    Pos           core.Vec2                // continuous (D11)
    Stats         map[core.StatID]float64  // open base-attribute vector, READ-ONLY (D7); §6 Stat channel
    Drives        map[DriveID]float64      // open drive vector ∈ [0,1] (F29(ii))
    Stamina       float64
    Vital         float64                  // single vital (F3); world owns death (object-mortality, §7)
    Heading       float64                  // steering direction (radians); FOV reference axis (F44)
    CurrentAction actions.ActionID         // last chosen action — §6 stickiness operand, NOT an FSM (F30/D3)
    ActiveUntil   core.Tick                // F45 wake cooldown horizon; 0 = none. world commits it.

    // Later-phase state deltas — NOT part of the P_fa1 core contract:
    //   + VitalCap, EngagedWith, NextExchangeTick, EngageCooldownUntil (combat, FC7/FC12) → SPEC-combat.md
    //   + HiddenUntil, Concealment (cover state; world is the SINGLE writer, M3/M5-b)     → SPEC-cover.md
    //   + Age (Σ DT since birth; world advances it each tick — drives the §6 `maturity` operand),
    //     MateCooldownUntil (post-mating refractory tick) — reproduction (PD4/P_fa4c). §7 aging is a later hook.
}

// EnvSample is the per-animal exogenous climate world samples at Animal.Pos and injects each tick.
// fauna is a pure transform over VALUES — it does NOT import climate (flora's SiteInput rule). When
// climate is OFF all fields are the neutral value world injects → apparent_temp reads that neutral °C;
// the thermal-NEUTRAL lever is now ThermalBand ≤ 0 (no comfort band ⇒ thermal 0), NOT the env alone —
// with a band set, thermal = clamp01(|apparent_temp−ComfortTemp|/ThermalBand) even under a neutral env
// (FA5). Scent spread stays local under OFF (F10/F21/F33). Operand names match the climate/flora/decay
// Context vocabulary exactly (temperature, moisture, wind.dir, wind.mag).
type EnvSample struct {
    Temperature float64    // operand `temperature`; neutral in P1
    Moisture    float64    // operand `moisture`; neutral in P1
    Wind        scent.Wind // {Dir radians, Mag} (operands `wind.dir`/`wind.mag`); zero in P1
    Daylight    float64    // operand `daylight` ∈[0,1]: 1=solar-noon, 0=midnight (FM11); world injects it from the clock. Drives the Sleep action ((1−daylight) diurnal, daylight nocturnal — D2/D10 sign). 0 ⇒ perpetual-night neutral
}

// TerrainSampler is the read-only navmap view fauna DECLARES and world ADAPTS (dependency inversion —
// expr.Context / perception.WorldSnapshot parity); steering samples at the dynamically computed next
// position, so it cannot be pre-injected like flora's fixed-Pos SiteInput. fauna must NOT import
// engine/space/navmap (F35, D11 — sample only, no pathfind). It exposes the SPECIES-INDEPENDENT terrain
// facts; the SPECIES affinity is applied by the controller via Rules.TerrainCost (per-species cost map,
// W10b RESOLVED 2026-06-28 — "수영 잘하는 동물 / 등산 잘하는 동물").
//   FootprintBlocked — true ONLY for hard blockers (walls / building footprints), ALL species; steering must not enter.
//   TerrainAt        — the terrain type id at p (D11 index read); the controller looks up the SPECIES
//                      affinity Rules.TerrainCost(species, terrain) for the effective cost/passability.
//   BaseCost         — the navmap base terrain cost at p (species-independent, ≥1). Effective traversal
//                      cost = BaseCost × Rules.TerrainCost(species,terrain).mult; the §6 speed terrain
//                      term reads it. (No global pathfinding — local sample only, F35/D11. Swim
//                      stamina-drain + drowning risk DEFERRED to §7/lifecycle.)
type TerrainSampler interface {
    FootprintBlocked(p core.Vec2) bool
    TerrainAt(p core.Vec2) core.Tag
    BaseCost(p core.Vec2) float64
}

// Cadence carries the F45 adaptive per-animal cadence parameters (balance data, world-injected; NOT
// per-species). DormantPeriod is the N in the dormant re-arbitration gate `(tick + phase(ID)) % N == 0`
// (N≈100). WakeCooldown is how many ticks a predator-scent wake keeps an animal ACTIVE before it may
// revert to DORMANT. phase(ID) is a FIXED stable hash of the ObjectID (e.g. FNV-1a) mod DormantPeriod —
// deterministic + cross-process stable (D12) — spreading dormant re-evals across ticks (load + jitter).
type Cadence struct {
    DormantPeriod int       // ≥ 1; the dormant re-arbitration period N
    WakeCooldown  core.Tick // ≥ 0; ticks an animal stays ACTIVE after a predator-scent wake
    // SleepWakeScentThreshold (SS3/FM12): torpor wake gate. While an animal is SLEEPING (its
    // CurrentAction's steer channel is TagSleep) the F45 predator-scent wake fires only when the
    // predator intensity at its cell is ≥ this — a deep sleeper ignores a faint/distant predator.
    // 0 ⇒ any scent wakes (identical to a non-sleeping animal).
    SleepWakeScentThreshold float64
}

// Snapshot is the read-only world view the controller scores over (the read phase; parallel-safe — each
// animal's evaluation is independent and reads only immutable/snapshot state, D12 plan phase).
//   Animals — all live animals in sorted ObjectID order (D12); never mutated by Step.
//   Scent   — the scent field; Step calls only its READ side (committed snapshot buffer, next-tick
//             latency, F33): IntensityAt (O(1) own-cell predator wake check, F45) + Read (full sense).
//   Spatial — the shared proximity index (combined agent/object/animal ObjectID space) world keeps
//             populated; READ-ONLY, for the F44 sight predator query.
//   Terrain — the passability/cost sampler (above).
//   Env     — per-animal EnvSample keyed by Animal.ID; a missing live-animal entry is a world-contract
//             bug (panic, mirrors flora's missing SiteInput).
//   Tick    — the current tick (drives the F45 dormant gate + wake cooldown; no wall-clock, D12).
//   Cadence — the F45 cadence parameters (balance).
//   DT      — locomotion time-step magnitude for one tick (world/balance), used by steering (F35).
//   HazardField — the SHARED STATIC hazard field as the fauna-declared HazardSampler interface (world
//             adapts *engine/space/field.Field — dependency inversion, like TerrainSampler, so fauna
//             imports no field/navmap; docs/plans/fauna.md §4 P_move1, FM2). world builds it ONCE
//             (source cells = dangerous terrain by max(depth,slope), weight = danger) and injects it;
//             steering adds e·Repulsion (e = species HazardAvoidance) to the chosen direction (below).
//             Per-species differentiation is via `e`, so one shared field suffices in P_move1. nil ⇒
//             NO hazard blend (the P_move1-off lever — byte-identical to pre-P_move1). Read-only.
type Snapshot struct {
    Animals     []Animal
    Scent       *scent.Grid
    Spatial     *spatial.SpatialHash
    Terrain     TerrainSampler
    Env         map[core.ObjectID]EnvSample
    Tick        core.Tick
    Cadence     Cadence
    DT          float64
    HazardField HazardSampler // shared static hazard field (P_move1); nil ⇒ off

    // Later-phase delta fields — NOT part of the P_fa1 core contract:
    //   + Combat CombatParams, ScentCellSize float64 (combat/cover/sleep balance params + the
    //     scent-cell scale their range factors multiply, FC) → SPEC-combat.md
    //   + MoveDeadband float64 (FM9 locomotion deadband: §6 speed below this ⇒ hold position;
    //     ≤ 0 ⇒ off/byte-identical; docs/plans/fauna.md §4.3) → SPEC-steering.md
    //   + WaterField WaterSampler (FM4 water-ATTRACTION field: Gradient toward drinkable water; the
    //     seek:water steer channel follows it; nil ⇒ off/byte-identical; docs/plans/fauna.md §4.1) → SPEC-steering.md
}

// HazardSampler is the read-only per-species static hazard POTENTIAL field fauna declares and world
// adapts (dependency inversion, like TerrainSampler — fauna imports no engine/space/field or navmap).
// Satisfied by *engine/space/field.Field. Repulsion(p) = the away-from-danger vector at p: UNIT away
// direction × LOCAL DANGER INTENSITY (severity × proximity — deep water/cliff push harder than a
// shallow bank); zero where safe/flat. A continuous cost, NOT a block: a strong drive/flight overcomes it.
type HazardSampler interface {
    Repulsion(p core.Vec2) core.Vec2
}

// Intent is the controller's per-animal output (ONE per animal, F1/F41). world APPLIES it in the
// combined agent+animal sorted-ObjectID apply order (F41 — Out of Scope) and is the sole mutator of
// Animal state: it enacts Action (conflict resolution by the relevant stat, ties by ObjectID), moves the
// animal to NextPos (spatial.Move), sets Heading, and commits Drives/Stamina/ActiveUntil. The proposed
// Drives are the PASSIVE per-tick evolution (F25(c)); the action's own drive Effect (Eat→hunger↓,
// Rest→fatigue↓) is layered by world when it enacts the action (world the sole mutator).
type Intent struct {
    Animal      core.ObjectID
    Action      actions.ActionID    // max-utility action (ACTIVE/re-eval) or held CurrentAction (dormant)
    Target      core.ObjectID       // resolved target for a targeted action (Hunt→prey, Eat→food); empty otherwise
    NextPos     core.Vec2           // steered next position (continuous, D11); == Pos if Rest/blocked
    NextHeading float64             // steered next heading (radians)
    Drives      map[DriveID]float64 // passive per-tick drive evolution (F25(c)); world commits it
    Stamina     float64             // proposed next stamina
    ActiveUntil core.Tick           // updated F45 wake-cooldown horizon; world commits it

    // Later-phase delta fields — NOT part of the P_fa1 core contract:
    //   + Vital, VitalCap, EngagedWith, NextExchangeTick, EngageCooldownUntil, Damage,
    //     TargetVitalCapDamage (the combat proposals, FC) → SPEC-combat.md
}

// ── The pure transform world calls (mirrors flora.Step / decay.Step) ────────────────

// Step scores ALL animals and returns ONE Intent each, in sorted Animal-ObjectID order (D12). PURE
// function of (snap, rules, rng): it does NOT mutate snap (incl. snap.Animals, the scent grid, the
// spatial index) and returns intents world applies. For each animal, in sorted ObjectID order:
//   0. CADENCE / WAKE (F45): every tick do an O(1) snap.Scent.IntensityAt(ChanPredator, Pos) read. The
//      predator BIT is intensity > wakeThreshold, where wakeThreshold = Cadence.SleepWakeScentThreshold
//      iff the animal is SLEEPING (its CurrentAction's steer channel is TagSleep — torpor, SS3/FM12),
//      else 0 (any scent, as before). The animal is ACTIVE iff Rules.IsPredator(species) (predators are
//      always ACTIVE) OR the predator bit is set (wake — also set ActiveUntil = Tick + Cadence.WakeCooldown)
//      OR Tick ≤ ActiveUntil (cooldown still running). Otherwise DORMANT. A DORMANT animal additionally
//      RE-ARBITRATES this tick iff (Tick + phase(ID)) % Cadence.DormantPeriod == 0. (A deep sleeper thus
//      ignores a faint/distant predator but still re-arbitrates periodically — light stirring, not coma.)
//   • ACTIVE, or DORMANT on a re-arbitration tick → run the FULL pipeline (1–4).
//   • DORMANT off-boundary → CHEAP path: Action = CurrentAction; advance only the accumulator drives by
//     rate + decay fear (no full sense, no sight query, no utility scoring); cheap steering = continue
//     along Heading at the held speed (still TerrainSampler-clamped). It is NOT frozen.
//   1. SENSE (F44 two-channel; scent-only-omni after F34↔F44): Scent.Read at Pos (omni neighbor/upwind,
//      smell radius) → scent.{food,prey,predator} + dist.{food,prey} + per-channel coarse direction;
//      spatial query within sight radius for predator ANIMALS whose relative bearing is within
//      Heading ± fov_arc → sight.predator (1/0) + dist.predator (nearest). (≥2 predators simultaneously
//      in FOV additionally accumulate an aggregated flee direction — M7, SPEC-steering.md; ≤1 visible
//      is byte-identical to the single-target path, and dist.predator stays the nearest.)
//   2. DRIVES (F25(c)): advance the drive vector — accumulators (hunger/fatigue/repro_readiness) by
//      Rules rate constants (D9); fear SET-from-context from the predator scent/sight channels (the
//      needs UpdateConditionalNeeds shape, replicated — fauna does not import needs); thermal SET from
//      Rules.AppTemp (OFF/0 in P1). → Intent.Drives.
//   3. SCORE (F1/F26): build the §6 Context once (Stat→base stats; Attr→drives/scent/dist/sight/
//      apparent_temp/wind), then per candidate ActionID in Rules.Candidates(species) set `is_current`
//      (1 iff == CurrentAction, the §6 stickiness term, F30/F45) and evaluate Rules.Utility. Pick MAX;
//      ties by sorted ActionID (D12).
//   4. STEER (F35): speed = Rules.Speed (§6 base-stat speed + fear/fatigue/thermal modulation + terrain Cost,
//      where terrain Cost = TerrainSampler.BaseCost × Rules.TerrainCost(species,terrain).mult — the
//      per-species cost map, W10b: a swimmer is fast in water, a climber on mountains);
//      direction = the resolved channel direction the chosen action seeks/avoids (Graze/Hunt→toward
//      food/prey, Flee→away from predator [away from the single nearest; ≥2 visible aggregate per M7 —
//      SPEC-steering.md], Wary→slowly away, Rest/Sleep→none [TagNoLoco/
//      TagSleep ⇒ NextPos == Pos; Sleep is the diurnal torpor, chosen when its (1−daylight) §6 utility
//      wins at night, P_sleep1/FM11]), then a THIN
//      always-on HAZARD-REPULSION blend (docs/plans/fauna.md §4 keystone "a-backbone + bounded blend",
//      FM5, P_move1): dir += HazardAvoidance · HazardField.Repulsion(Pos) — ONE shared danger field,
//      per-species differentiation via the scalar HazardAvoidance `e` (per-species fields deferred, §4 FM2).
//      Repulsion already
//      scales with local danger SEVERITY + proximity (deep water/cliff push harder & farther than a
//      shallow bank) — a CONTINUOUS cost, not a block: a strong flee/drive out-pulls it and the animal
//      crosses anyway (FM5). Only the blended ANGLE is used (atan2); HazardAvoidance (the per-species
//      multiplier `e`) ≤ 0 or a nil field ⇒ no bend (byte-identical to pre-P_move1). NextPos = Pos + dir·speed·DT
//      clamped by TerrainSampler (blocked iff FootprintBlocked OR !TerrainCost(species,terrain).passable —
//      so water is traversable for a swimmer, impassable for a fish-on-land; D11). On a BLOCKED step the
//      animal HOLDS position (NextPos == Pos) but STILL commits the turned NextHeading (wander + blend),
//      never freezing its old heading against the obstacle (FM13, SPEC-steering.md); otherwise NextHeading
//      turns toward dir. (World then reflects NextHeading off any world-bound wall it overshot — FM13.)
// rng is the injected per-tick fork world supplies (per-step fork(tick), F41). Arbitration, drive
// advance, scent read are deterministic and draw nothing; rng is drawn ONLY by a stochastic steering
// term (a species §6 wander/jitter operand) — same seed ⇒ identical steering.
func Step(snap *Snapshot, rules *Rules, rng *rng.RNG) []Intent

// AttrOperands returns the FIXED controller-resolved §6 Attr operand vocabulary (sorted, de-duplicated):
// scent.{food,prey,predator}, dist.{food,prey,predator}, sight.predator, apparent_temp, daylight,
// temperature, moisture, wind.dir, wind.mag, is_current. platform/config cross-checks each compiled program's
// expr.ReadsAttrs() against this set ∪ the species' drive ids at load (flora operand-cross-check parity)
// so a typo Attr (silently 0, expr policy) is a LOAD failure. Deterministic; excludes the Stat channel
// (validated against the stats registry) and drive ids (from the species `fauna:` block).
// (agent.disposition is P_fa3, F46 — NOT in the P_fa1 set; see Out of Scope. Combat adds
// `target.threat` + `scent.carrion` to the fixed set — FC2/FC10, SPEC-combat.md. Reproduction adds
// `maturity` — PD4-ii/P_fa4c: a controller-derived operand = clamp01(Age/SpeciesRule.MaturityAge), 1 when
// mature or when MaturityAge ≤ 0 (unauthored ⇒ gate-neutral). The Mate §6 utility reads it.)
func AttrOperands() []core.Tag

// ── Rules (the data-defined fauna table; flora.Rules parity, F26/F31) ────────────────

// Rules is the compiled, immutable per-species fauna table from content/objects.yaml `fauna:` blocks:
// per species — the candidate action set + each (species × ActionID) §6 utility Program (F26(a)), the
// drive rate constants + fear set-from-context params (F25(c)), the §6 apparent_temp Program (F40), the
// §6 speed Program (F35), the predator flag (`threat:predator`, F8), the diet/target tags (F7), the
// sense radii (smell/sight radius, fov_arc — F31/F44). Built ONCE by platform/config (parse each §6 via
// engine/kernel/expr, validate StatIDs/operands/action-ids/tags); engine/fauna evaluates it READ-ONLY
// and never parses YAML (D10). Mirrors flora.Rules / decay.Rules.
type Rules struct{ /* opaque; per-SpeciesID compiled §6 programs + drive params + sense radii + tags. */ }

// NewRules builds the immutable per-species fauna table from COMPILED inputs — the config-facing
// constructor (WI-P0). platform/config parses content/objects.yaml `fauna:`, compiles each §6 via
// expr.Parse, validates each program's ReadsAttrs ⊆ AttrOperands() ∪ that species' drive ids +
// Reads ⊆ stats registry + candidate ids ⊆ actions.Registry, then calls this; fauna never parses YAML
// (D10). Pure. (An empty map = fauna-off: Candidates returns empty for every species, the P_fa1 lever.)
func NewRules(species map[SpeciesID]SpeciesRule) *Rules

// SpeciesRule is one species' compiled fauna spec (the NewRules input, mirroring the Rules accessors).
type SpeciesRule struct {
    Utilities  map[actions.ActionID]*expr.Program // candidate set + per-action §6 utility (F26) → Candidates/Utility
    Drives     []DriveRule                        // drive vocabulary + params (F25(c)) → DriveUpdate
    AppTemp    *expr.Program                      // §6 apparent_temp (F40) — emits °C (render + operand), NOT the drive
    ComfortTemp float64                           // °C midpoint of the thermal comfort band (FA5)
    ThermalBand float64                           // °C half-width; thermal = clamp01(|apparent_temp−ComfortTemp|/ThermalBand); ≤0 ⇒ thermal neutral
    HazardAvoidance float64                       // per-species hazard-repulsion multiplier `e` (P_move1/FM5); ≤0 ⇒ no bend
    Speed      *expr.Program                      // §6 locomotion speed (F35) → Speed
    Diet       []core.Tag                         // diet/target tags (F7)
    IsPredator bool                               // carries `threat:predator` (F8) → IsPredator
    SmellRadius, SightRadius, FovArc float64      // senses (F31/F44) → Senses
    TerrainCost map[core.Tag]float64              // per-species terrain affinity mult (W10b); absent terrain ⇒ 1.0 → TerrainCost
    Impassable  []core.Tag                        // terrain types this species CANNOT enter (e.g. fish→land) → TerrainCost passable=false
    SteerChannel map[actions.ActionID]core.Tag    // action → steer-behavior tag (content-derived from the action's Tags, D4/D10); absent entry ⇒ continue along current Heading

    // Later-phase delta fields — NOT part of the P_fa1 core contract:
    //   + AttackPower, Hit, Feed, Graze (§6), Tags (diet-tag matching) — combat/feeding (FC4/FC8) → SPEC-combat.md
    //   + HideChance (§6), CoverCost — cover (M3/M4-b)                                            → SPEC-cover.md
    //   + TurnRate (§6 max turn rate) — steering inertia (M6)                                     → SPEC-steering.md
    //   + ScentAcuity (§6 keenness gain) — scent-tracking confidence (PD1/P_fa4a)                 → SPEC-steering.md
    //   + MaturityAge (Age→`maturity` operand scale) — reproduction gate (PD4-ii/P_fa4c); ≤0 ⇒ maturity≡1
}

// Steer-behavior tags recognized by the STEER step (the SpeciesRule.SteerChannel values; content
// places them on actions in content/actions.yaml, D10 — the steer channel is derived from action
// Tags, never a per-action Go function, D4): TagSteerFood "seek:food" · TagSteerPrey "seek:prey" ·
// TagFleePred "flee:predator" · TagWaryPred "wary:predator" · TagNoLoco "no:locomotion" ·
// TagSleep "state:sleep" (torpor: NextPos == Pos + the SS3 wake threshold + SS2 deep fatigue
// recovery). (+ TagAttack "combat:attack" / TagFeed "feed:carrion" — FC → SPEC-combat.md.)

// DriveRule is one drive's compiled params (F25(c)/F43; the drive id is also its §6 Attr operand, F27).
// An ACCUMULATOR drive uses Rate; a SET-from-context fear drive uses WaryLevel (scent.predator) /
// FleeLevel (sight.predator) + Decay; a DERIVED thermal drive uses none of these DriveRule fields —
// it is SET each full tick from the species' SYMMETRIC comfort band (ComfortTemp/ThermalBand, FA5).
//
// VitalDrain/VitalDrainAbove are the PD3 grace+bleed coupling (docs/plans/fauna.md §5, P_fa4b): when
// this drive is AT/ABOVE VitalDrainAbove (θ) the animal's Vital bleeds VitalDrain (r) per tick — a
// drive-saturation→mortality seam authored PER DRIVE as content data (D4/D10), not a bespoke starvation
// function. hunger authors it (starvation); thermal REUSES the SAME two fields later (freeze die-off,
// only the drive differs). The "grace" is emergent — no counter field: hunger is [0,1]-clamped so the
// time it takes the accumulator to reach θ is the grace, and the Vital pool draining at r is a second
// buffer. OFF-lever: VitalDrain ≤ 0 ⇒ no coupling (every pre-P_fa4b drive has 0 ⇒ byte-identical).
type DriveRule struct {
    ID        DriveID
    Rate      float64 // accumulator per-tick rise (hunger/fatigue/repro_readiness); 0 for set/derived
    Decay     float64 // per-tick decay when not raised/set (e.g. fear cooling)
    WaryLevel float64 // fear value SET on scent.predator (F43); 0 if unused
    FleeLevel float64 // fear value SET on sight.predator (F43); 0 if unused

    VitalDrain      float64 // PD3: Vital bleed per tick while this drive ≥ VitalDrainAbove; ≤0 ⇒ no vital coupling (off-lever)
    VitalDrainAbove float64 // PD3: the drive threshold θ at/above which VitalDrain applies (grace = accumulator rise time to θ)
}

// Candidates returns the species' candidate ActionIDs (= the utility-map keys, F26(a)/F28) in sorted
// order (D12). Each is a SHARED engine/mind/actions id (Graze/Flee/Wary/Hunt/MoveTo/Rest). Empty for an
// unknown species (fauna-off neutrality).
func (r *Rules) Candidates(sp SpeciesID) []actions.ActionID

// Utility evaluates the (species × action) §6 utility Program against ctx → a plain score (F26(a)). PURE
// numeric §6 (design.md §6) — NO RNG, NO cross-action reference, NO sequencing (a flat score set, not a
// tree, D3). Returns the formula's natural "never" value for a species/action absent from the table.
func (r *Rules) Utility(sp SpeciesID, act actions.ActionID, ctx expr.Context) float64

// DriveUpdate advances the whole drive vector ONE tick per F25(c) and returns the new vector (clamped
// [0,1]): accumulators rise by per-species rate constants (D9, no future field); fear is SET-from-context
// from the resolved predator channels via ctx (scent.predator → wary level, sight.predator → flee level
// — the needs conditional-set shape, rate+level data, F25(c)/F43); thermal SET from the symmetric
// comfort band clamp01(|apparent_temp−ComfortTemp|/ThermalBand) — cold AND heat raise it, ThermalBand≤0
// ⇒ 0 (FA5). Pure, no RNG, drive ids advanced in sorted order. It does NOT apply the action's drive Effect
// (world's apply). (The dormant cheap path advances only the accumulators + fear decay.)
func (r *Rules) DriveUpdate(sp SpeciesID, cur map[DriveID]float64, ctx expr.Context, dt float64) map[DriveID]float64

// AppTemp evaluates the species' §6 apparent_temp Program over climate operands (temperature/moisture/
// wind.mag) + the animal's own attrs (size/base stats), F40 — per-entity §6, emitting felt °C (CA3).
// This °C value is BOTH the render value (persist ClimateView.apparent_temp) AND the `apparent_temp`
// operand; the `thermal` DRIVE is a SEPARATE derivation — DriveUpdate maps this °C through the species'
// symmetric comfort band clamp01(|apparent_temp−ComfortTemp|/ThermalBand), so "winter/cold night" AND
// "hot noon" both raise thermal, no season enum (FA5). Pure, no RNG.
func (r *Rules) AppTemp(sp SpeciesID, ctx expr.Context) float64

// Speed evaluates the species' §6 locomotion speed Program (base = §6(base stats); modulated by
// drive terms — fear/fatigue AND **thermal** (body-temperature stress → slower locomotion; the
// `thermal` drive is derived from `apparent_temp` via the comfort band, F40/FA5, so weather/wind alters
// movement ability; ThermalBand ≤ 0 ⇒ thermal 0 ⇒ neutral) — plus terrain Cost, F35(a)+(c)+R2) → world
// units per DT. Which drives a species' speed §6 reads is open content (D4/D10 — a §6 over the base
// stats + any drive operand), never a fixed Go whitelist; the fear/fatigue/thermal set is the authored
// content convention, not an engine constraint. Pure, no RNG.
func (r *Rules) Speed(sp SpeciesID, ctx expr.Context) float64

// IsPredator reports whether the species carries the `threat:predator` tag (F8) — used to classify
// nearby spatial entities in the F44 sight query AND to keep predators always-ACTIVE (F45). Pure.
func (r *Rules) IsPredator(sp SpeciesID) bool

// Senses returns the species' sense radii: smell radius (the scent-grid read radius; the grid's cellSize
// ∝ this, F32), sight radius (the spatial predator-query radius), fov_arc (the half-angle, radians, of
// the Heading-relative forward sight cone, F44). From the `fauna:` senses block (D10).
func (r *Rules) Senses(sp SpeciesID) (smellRadius, sightRadius, fovArc float64)

// TerrainCost is the species' affinity for a terrain type (W10b — per-species cost map, "수영/등산"):
// `mult` multiplies the navmap BaseCost for the effective traversal cost (steer/§6 speed); a value <1 =
// the species is GOOD at it (a swimmer's river/sea mult is low), >1 = poor (a deer's mountain mult high);
// absent terrain ⇒ 1.0. `passable=false` ⇒ the species CANNOT enter that terrain at all (fish on land),
// even where FootprintBlocked is false. From the `fauna:` `terrain_cost`/`impassable` block (D4/D10). Pure.
// The controller's effective sample: blocked = TerrainSampler.FootprintBlocked(p) OR !passable;
// cost = TerrainSampler.BaseCost(p) × mult — this is the species-specific "cost map" over the SHARED terrain.
func (r *Rules) TerrainCost(sp SpeciesID, terrain core.Tag) (mult float64, passable bool)

// ── Later-phase Rules accessors (interface deltas; see the owning sub-spec) ──────────
//   AttackPower / Hit / Feed / Graze / Diet — combat & feeding (FC4/FC8) → SPEC-combat.md
//   HideChance / CoverCost                  — cover (M3/M4-b)            → SPEC-cover.md
//   TurnRate                                — turn-rate inertia (M6)     → SPEC-steering.md
```

## Dependencies
- `engine/kernel/core` — `Vec2`, `Tag`, `ObjectID`, `StatID`, `Tick`. No IO.
- `engine/kernel/expr` — the shared §6 evaluator (L0). fauna OWNS the compiled per-species `Program`s in
  `Rules` (utility / apparent_temp / speed) and evaluates them via an `expr.Context` adapter (`Stat`→base
  stats; `Attr`→drives/scent/dist/sight/climate/`is_current`; `Pred`→unused, always false). fauna does
  NOT parse the DSL (`platform/config`'s job) and never imports `engine/mind/gates`. expr L0 is UNCHANGED
  (F27 — all fauna operands ride the lowercase/dotted `Attr` channel, flora `moisture` parity).
- `engine/kernel/rng` — `*RNG` (injected per-tick fork; only a stochastic steering term draws, D12).
- `engine/mind/actions` — `ActionID`, the shared `Registry` (candidate actions are SHARED ids; reads
  action defs for the Duration→`EffectPerMinute` denominator, F30). NO gates/cost use (F1 — no planner):
  an action's `Tags` feed utility tag-matching / steer channel only, never a gate or cost.
- `engine/space/spatial` — `*SpatialHash` (`NearbyEntities`, ObjectID-sorted) for the F44 sight predator
  query; READ-ONLY in the score phase. world keeps it populated (combined ObjectID space).
- `engine/space/scent` — the **scent grid** sub-module (`backend/engine/space/scent/SPEC.md`): `Grid`,
  `Channel`, `Wind`, `Reading`. The grid is a **world-owned shared index** (F36 promotion, `engine/space`);
  the controller calls only its READ side (`IntensityAt` wake check + `Read` full sense).
- *(NOT `engine/world` / `engine/agent` / `engine/mind/planner`)* — the L4 cut: fauna emits intents and
  world applies them (combined agent+animal ObjectID order, F41); no cycle (dependency-inversion like
  climate/flora/decay). Import-guarded.
- *(NOT `engine/space/navmap` / `engine/env/climate` / `engine/mind/needs` / `engine/mind/stats` /
  `engine/mind/tom` / `engine/mind/values` / `engine/mind/gates` / `engine/mind/perception`)* — navmap via
  the declared `TerrainSampler` (world adapts); climate via injected `EnvSample`; the fear conditional-set
  replicates the needs shape without importing needs; base stats are the raw open map; no ToM/value/gate/
  perception coupling (F2/F3).

## Owned Data
- The **`Animal` entity type** + the live animal set is held by `engine/world` (one per run, snapshot-
  serialized — `docs/core/data-contracts.md §6`, P_fa5); `fauna.Step` reads a `Snapshot` and never mutates it.
- The **scent field** (`engine/space/scent.Grid`) is a **shared world index** (promoted out of fauna, ①;
  `engine/space`, spatial/navmap kin) — **world-owned**, fed by world from `scent:<channel>`-tagged emitters
  (flora/fauna/decay), `world` DRIVES deposit/spread/commit (P_fa2); **fauna only READS it** + defines the
  read→behaviour. **Scalar intensity** (F21 revised). See `backend/engine/space/scent/SPEC.md`.
- The **`Rules`** table is owned by `platform/config` (built from `content/objects.yaml` `fauna:` via
  `engine/kernel/expr`), injected read-only; never parsed or mutated here.
- This module owns **no** object instances, **no** spatial-hash entries, **no** navmap/climate state — it
  only emits `[]Intent` (and, via the scent sub-module, the scent field).
- **§6 Attr operand vocabulary** (the F27 bridge): `AttrOperands()` (the fixed controller-resolved set,
  incl. `is_current`, F45/R5) ∪ the species' open drive ids. The Stat channel (UPPERCASE) is the
  base-attribute vector, validated against the stats registry by config.

## Invariants
- **D12 determinism** — `Step` is a pure function of `(snap, Rules, rng)`. Same snapshot (incl. `Tick`) +
  same RNG fork ⇒ byte-identical `[]Intent`. No `time.Now()`, no global rand, no wall-clock; the only
  randomness is the injected `*rng.RNG` (steering jitter only).
- **F45 cadence determinism** — active/dormant is a pure function of `(IsPredator, IntensityAt(predator),
  Tick, ActiveUntil)`; the dormant re-eval gate is `(Tick + phase(ID)) % DormantPeriod`; `phase(ID)` is a
  FIXED stable ObjectID hash, identical across runs/processes. The wake read is the pure committed-buffer
  `IntensityAt` (next-tick latency, F33). No wall-clock, no map-iteration; animals processed in sorted ID
  order. Same `(snap, Rules, rng)` ⇒ identical dormant/active partition + intents.
- **D12 no map-iteration for logic** — animals scored in sorted `ObjectID` order; intents returned sorted
  by `Animal`; candidate actions via `Rules.Candidates` (sorted), ties by sorted `ActionID`; drive ids
  advanced in sorted order; `snap.Env` read by sorted animal id; `spatial.NearbyEntities` ObjectID-sorted.
- **D11 / continuous space** — every `Pos`/`NextPos`/`Heading` is continuous; the scent grid is an
  auxiliary INDEX (read own cell + neighbors, never snapped); steering moves along a continuous vector and
  only SAMPLES `Terrain` (no tile iteration, no pathfind, F35). **Terrain cost is PER-SPECIES** (W10b):
  effective cost = `BaseCost × Rules.TerrainCost(species,terrain).mult`; a cell is blocked iff
  `FootprintBlocked` OR `!TerrainCost(species,terrain).passable`. So water is fast for a swimmer / slow for
  a deer / impassable for a fish-on-land — the SHARED terrain, a SPECIES cost map over it. No global pathfind.
- **D3 no FSM / no hand-drawn tree** — action choice is a flat horizon-1 `max` over §6 utility *scores*
  (F1/F26). **No** behaviour tree, **no** explicit wary→flee transition, **no** commit+interrupt machine:
  Wary vs Flee is the continuous `fear` value crossing §6 utility bands (F43); stickiness is the §6
  `is_current` term, not a stored mode; ACTIVE/DORMANT is a cadence gate (re-arbitration FREQUENCY), NOT a
  behaviour state (F45 — a dormant animal's chosen action is still its last §6 max). A utility `Program`
  may not reference another action's score or encode sequencing (D3 guard).
- **D4 / D10 data-defined** — utility, drive rates + fear levels, apparent_temp, speed, sense radii,
  predator/diet tags are ALL `content/objects.yaml` `fauna:` §6/data via `engine/kernel/expr`; **no**
  bespoke per-species Go decision/steer/drive function. An unknown species/operand/action-id is caught at
  load (`platform/config`), never in `Step`. (Cadence N / cooldown / `phase` are engine-fixed balance.)
- **D2 emergence** — no hardcoded meta-institution: husbandry/taming, herds/territory, food-chains,
  predator/hunter-roles all EMERGE (drives + utility + tags + agent-disposition §6, F46). **No** `tame`
  flag, **no** herd/role type, **no** authored food-chain table (F7/F13). Predator↔prey is a tag relation.
- **D5 / D1 — drive ≠ Value** — the drive set is a SEPARATE motivation machine from the agent `Value`
  stack (reduced loop, §0-1); fauna has no `Value`/`Standing`/`Salience` and never imports values. Utility
  stays a per-(species×action) §6 score (F26(b)/(c) dot-products rejected, D4).
- **D6 / D8 — no ToM** — animals carry no belief distribution / self-perception; F2 collapses the
  attempt/judge channel to the single real-stat channel. Predator→agent Safety flows only through the
  perception hostile-tag channel world wires (F8). The F46 agent-disposition read is REAL agent stats
  (not ToM), P_fa3.
- **D7 read-only stats** — `Animal.Stats` is read by §6 and never written here; stat training/aging is a
  cross-cutting concern (flora `Dexterity` parity). No per-action skill stored (D7).
- **D9 no future field** — a drive accumulator rises by a RATE; **no** "future need" / "time-until-hungry"
  / provisioning field on `Animal` or any drive.
- **Read-only inputs** — `Step` never mutates `snap` (incl. `snap.Animals`, the grid, the spatial index),
  `Rules`, or `rng` beyond drawing. The `expr.Context` adapter is pure for a given animal.
- **Outcome-neutral until activated (the P_fa1 safety lever)** — with NO species placed there are zero
  `Animal`s ⇒ `Step` returns ZERO intents ⇒ no behaviour, no scent deposits, no animal state change; the
  legacy `prey` timer-respawn object stays untouched; existing world goldens hold. Activation is the
  deliberate P_fa3 re-baseline (climate/flora/decay parity).

## Acceptance Criteria (testable)
- [ ] **Utility max + ID tie-break determinism (F1/F26/D12)** — over a fixture species scored against a
  stub Context, `Step` picks the strictly-highest `Rules.Utility`; equal scores → the lexicographically-
  smaller `ActionID` wins; identical across repeated calls + a second `Rules` from the same content.
- [ ] **One intent per animal, sorted (D12)** — exactly one `Intent` per snapshot animal, ascending
  `Animal` order; shuffling `snap.Animals`/`snap.Env` order yields byte-identical intents.
- [ ] **F45 adaptive cadence: dormant ID-phase determinism + predator-scent wake** — a non-predator
  herbivore with NO predator bit in its cell and `Tick ≤ ActiveUntil` false re-arbitrates ONLY on ticks
  where `(Tick + phase(ID)) % DormantPeriod == 0` (off-boundary it holds `CurrentAction`, still steers +
  advances accumulator drives — not frozen); the SAME animal whose own-cell predator bit is set goes
  ACTIVE that tick (full pipeline) and its `Intent.ActiveUntil == Tick + WakeCooldown`; it stays ACTIVE
  while `Tick ≤ ActiveUntil` then reverts to dormant; a predator species (`IsPredator`) is ACTIVE every
  tick regardless. `phase(ID)` is identical across a second process. Table-driven over ticks/animals.
- [ ] **Drive integration per F25(c)** — over N ACTIVE ticks an accumulator (hunger) rises by `rate·N`
  clamped [0,1]; `scent.predator` present → `fear` SET to the wary level; `sight.predator` present → SET
  to the (higher) flee level; neither → fear decays; `thermal` stays 0 while climate is OFF. A dormant
  off-boundary tick advances ONLY accumulators + fear decay (no full sense). Table-driven.
- [ ] **Seeded steering reproducible + PER-SPECIES terrain cost (F35/R2/W10b/D12)** — for a species whose
  steering has a stochastic jitter term, two `Step` runs with the same seed produce byte-identical
  `NextPos`/`NextHeading`; a different seed differs; steering never enters a `FootprintBlocked` cell nor a
  terrain where `Rules.TerrainCost(species,terrain).passable==false` (stays/slides, never teleports);
  **two species over the SAME terrain differ**: a swimmer (low river `mult`) crosses water fast while a
  high-`mult` species crawls, and a fish-species (`river` passable, `soil` impassable) is blocked on land —
  cost = `BaseCost × mult`; Rest ⇒ `NextPos == Pos`. Table-driven over species × terrain.
- [ ] **Scent deposit/spread/read determinism + neutrality (F20–F24/F32–F34)** — delegated to the scent
  sub-SPEC ACs. Controller-side: a `Step` whose grid has a `predator` on-cell within smell radius sets the
  animal's `scent.predator` operand to 1 + a coarse `dist.predator` direction away-from-source for a
  Flee/Wary steer, and (own-cell bit) wakes a dormant animal (F45).
- [ ] **Sight FOV predator query (F44(c-ii)/D11/D12)** — a predator ANIMAL within sight radius AND within
  `Heading ± fov_arc` sets `sight.predator`=1 + `dist.predator`=its distance; the same predator OUTSIDE the
  cone (behind, |bearing| > fov_arc) sets `sight.predator`=0 (rear blind spot); a non-predator nearby
  entity is ignored; the nearest qualifying predator wins; the bearing test iterates the ObjectID-sorted
  `NearbyEntities` set. (Agent entities are NOT classified in P_fa1 — F46/Out of Scope.) Table-driven.
- [ ] **Shared-registry scoring (F28)** — `Rules.Candidates(species)` ⊆ the shared `actions.Registry.IDs()`
  (Graze/Flee/Wary/Hunt/MoveTo/Rest); the controller scores exactly those and reads each action's def from
  the shared registry — it builds NO fauna-private action set.
- [ ] **Context bridge = lowercase/dotted Attr; stats = Stat; is_current (F27/F45/R5)** — the adapter
  resolves `Strength`/`Agility`/… via `Stat` and `hunger`/`fear`/`scent.predator`/`dist.food`/
  `sight.predator`/`apparent_temp`/`wind.dir`/`is_current` via `Attr`; `is_current` is 1.0 ONLY for the
  candidate equal to `CurrentAction`, else 0.0; an unknown Attr returns `ok=false` (→ expr 0); the sorted
  `AttrOperands()` matches the operands the fixture programs reference. Table-driven.
- [ ] **fauna-OFF neutrality (the P_fa1 lever)** — with empty `Rules` and/or zero animals, `Step` returns
  no intents, draws nothing from rng, performs no scent deposit, and leaves all input state unchanged;
  existing world goldens hold.
- [ ] **Read-only inputs** — a property test confirms `snap.Animals`, the scent grid, the spatial index,
  and `Rules` are unchanged after `Step`.
- [ ] **Missing EnvSample panics** — `Step` with a live animal absent from `snap.Env` panics (world-
  contract guard, mirrors flora/decay), never silently skips an animal.
- [ ] **Determinism golden (D12)** — a fixed `(snapshot+Tick sequence, seed, Rules)` over N ticks yields a
  byte-identical digest of the per-tick `[]Intent` (incl. the dormant/active partition); a second run from
  a registry built from the same `content/objects.yaml` reproduces it. First fauna-OFF (neutral), then
  re-baselined at activation (P_fa3).
- [ ] **No wall-clock / no global rand / no forbidden import (D12 guard)** — grep/import guard: no `time`
  import for logic, no global `rand`; import set EXACTLY `core`, `expr`, `rng`, `actions`, `spatial`,
  `space/scent` (+ stdlib `sort`/`math`/`hash`); NO `engine/world`, `engine/agent`,
  `engine/mind/planner`, `engine/space/navmap`, `engine/env/climate`, `engine/mind/needs`,
  `engine/mind/stats`, `engine/mind/tom`, `engine/mind/values`, `engine/mind/gates`,
  `engine/mind/perception` import.
- [ ] **No hardcoded constant / id (D10 guard)** — grep guard: no species-name / action-name / drive-name
  / operand / rate / threshold / sense-radius literal in logic; every constant/formula flows from `Rules`
  (config-injected), the actions registry, or `Cadence` (balance). `AttrOperands()` is the one declared
  operand list (the F27 bridge), not scattered literals.

## Out of Scope
- **Combined agent+animal apply ORDER + per-tick `fork(tick)` RNG + scent bulk-pass cadence wiring**
  (world drives `Grid.Deposit` in apply + `Grid.Spread` on `tick % Ns` + `Grid.Commit` next-tick swap,
  F33/F41) → `engine/world` (P_fa2, `backend/engine/world/SPEC-tick.md`). **Apply contract documented
  here, implemented there:** world collects fauna `[]Intent` alongside agent intents, applies them in ONE
  sorted-ObjectID sequence (D12; conflicts resolve by the relevant stat, ties by ObjectID), moves each
  animal (`spatial.Move`), commits Drives/Stamina/Heading/ActiveUntil, layers the enacted action's drive
  Effect, owns Vital/death (object-mortality, §7). world still calls `Step` EVERY tick (F45 active/dormant
  is fauna-internal). fauna only emits intents + defines the scent behaviour.
- **Agent-sight + `agent.disposition` response (F46, P_fa3).** When a herbivore's SIGHT detects an AGENT
  it reads the agent's disposition = a signed §6 over the agent's REAL base stats (recommend `Sociability
  − Aggression − Vindictiveness`, coefficients = balance/content, D4), exposed as the signed operand
  `agent.disposition`: positive → stay (fear 0), negative → flee (fear↑) via the F43 fear channel. NOT ToM
  (reads real agent stats, F3); "hunter" emerges from stats (D2); the F8 predator→agent-Safety direction
  is unchanged. **P_fa1 sight = predator ANIMALS only**; the exact §6 + operand + the agent-entity
  classification (world injects agent stats into the sight query) finalize at P_fa3 → `engine/world` +
  `content` + this SPEC's P_fa3 revision.
- **Swim stamina-drain + drowning risk** (the W1 animal-analog of high-`Cost` water traversal) → §7 /
  lifecycle (P_fa4+). P1 water is cost-only-traversable (R2).
- **`carcass` object + `Butcher` extract + decay-lot mapping** → world/actions/`engine/env/decay`
  (P_fa3, F11/F12/F37/F38).
- **`content/objects.yaml` `fauna:` parse + §6 compile into `Rules` + cross-check** (`ReadsAttrs()` ⊆
  `AttrOperands()` ∪ species drive ids; `Reads()` ⊆ stats registry) → `platform/config`.
- **`Graze`/`Flee`/`Wary` `content/actions.yaml` entries** (+ the `IDs()`/`Producers` golden re-baseline)
  → `content` + `engine/mind/actions` (P_fa3, F28/F43). This SPEC consumes them via the shared registry.
- **Climate operand SOURCE + `apparent_temp` activation** (climate-OFF in P1 → neutral `EnvSample`;
  wind-driven long-range scent + upwind homing) → `engine/env/climate` + `engine/world` (P_fa4,
  `docs/plans/climate.md §1c` CA1–CA3; operand names/units MUST match across docs).
- **Population maintenance (NOT breeding) — RESOLVED 2026-06-28 (W11, `docs/plans/world-integration.md`)**: there is
  **no parent→child birth**. The world keeps a per-species population target and, when below it, respawns ONE
  animal on a seeded cadence at a **HIDDEN wild location** — outside every agent's `sight_radius` AND in
  undeveloped terrain (no building footprint / settlement, passable wild). No parent, no inheritance (stats =
  species `GenSpec`). Generalizes the legacy `balance.regen.prey_respawn`. → `engine/world` respawn placement
  (small wiring). The `repro_readiness` drive + a drive-gated birth mechanism (P_fa4) are **dropped**; the drive
  id may remain a latent/unused content option.
- **Serialization / SSE of `animals[]`** → `platform/persist` + `docs/core/data-contracts.md §6` (P_fa5).
- **`docs/core/glossary.md` sync** of the coined terms → a separate glossary step.
- The shared §6 `expr` evaluator **implementation** → `engine/kernel/expr` (L0); fauna only USES it.
- **Later fauna-side phase deltas** — combat & predation (FC), cover mechanics (M3/M4-b/M5-b), steering
  refinements (M6/M7) → the sub-specs in the table at the top of this file.

## Open Questions
> `docs/plans/fauna.md` §1 (F1–F46) is **ALL RESOLVED** (human-confirmed 2026-06-26 / 2026-06-27). This SPEC
> writes from those resolutions and **invents no mechanism**. The four plumbing/naming seams the prior
> draft surfaced are now RESOLVED and folded in (2026-06-27): **`is_current` kept** (R5/F45) →
> `AttrOperands`; **`TerrainSampler` = {FootprintBlocked, TerrainAt, BaseCost} + per-species `Rules.TerrainCost`** (W10b 2026-06-28 — 수영/등산 cost map, fish-on-land impassable; R2/F35); **stats =
> inline `map[core.StatID]float64`** (R3/F29, no `stats` import); **agents-as-sight = `agent.disposition`
> §6, P_fa3** (R4/F46). The **F45 adaptive per-animal cadence** (R1) is integrated above. **None remaining
> — no new mechanism seam.** (The later FC/M gates are likewise all RESOLVED — see each sub-spec's header.)

## Notes
- `Step` deliberately mirrors `flora.Step`/`decay.Step`: pure read → return per-entity delta/intent →
  `world` applies. Keeps `world` the single mutator (D12) and adds no cycle (F5/F41 — dependency-inverted
  like the env drivers).
- **F45 cadence is a frequency gate, not a behaviour state (D3).** DORMANT ≠ a mode the §6 sees; it only
  means "skip full re-arbitration this tick and hold the last §6-chosen action + cheap steer." The wake
  (own-cell predator scent → ACTIVE) gives "the herbivore goes real-time exactly when predator scent
  reaches it" with zero O(N²) scan. Determinism rests on the ID-`phase` hash + the pure committed-buffer
  `IntensityAt` read; never introduce a per-animal Go state machine.
- **Two motivation machines, kept apart (D5).** The drive set is the animal's whole motivation; keep
  utility a per-(species×action) §6 score (F26(a)) — never an EffValue dot-product nor an engine-coded
  drive↔action map (D4 risk).
- **Wary vs Flee is one continuous fear value (F43/D3).** No wary→flee transition. `scent.predator` raises
  fear into the wary band (Wary wins), `sight.predator` into the flee band (Flee wins) — an emergent
  consequence of the value crossing data utility bands. The F46 `agent.disposition` rides the SAME fear
  channel (negative disposition → fear↑ → flee).
- **Sensing is two clean channels (F44/F34):** smell = the omni scent grid (early-warning → Wary + the
  F45 wake), sight = the spatial-hash forward-FOV predator query (proximity → Flee). The F34 omni rule is
  scent-only; sight is the Heading-relative continuous-bearing cone (D11).
- The scent grid is split into its own sub-SPEC (CLAUDE.md split rule): see
  **`backend/engine/space/scent/SPEC.md`** for the cell/bitset shape, deposit/spread/read/commit +
  `IntensityAt` contract, `cellSize ∝ smell radius`, and the determinism + neutrality ACs.
- Tuning + behaviour live in `content/objects.yaml` `fauna:` §6 (D10); cadence N/cooldown in
  `content/balance.yaml`. Adding a species, drive, predator/prey relation, or steer/utility is a content +
  §6 change, never code (D2/D3 — herds/husbandry/food-chains must emerge).
- Reference paths: `docs/plans/fauna.md` (binding F1–F46), `docs/core/design.md §5/§6/§7`,
  `backend/engine/env/flora/SPEC.md` + `backend/engine/env/decay/SPEC.md` (the pure-Step template),
  `backend/engine/kernel/expr/SPEC.md` (§6 evaluator + `Attr`/`Stat` classification), `backend/engine/
  mind/actions/SPEC.md` (shared candidate registry), `backend/engine/space/spatial/SPEC.md`
  (`NearbyEntities` sight query), `docs/core/architecture.md §2/§4/§5` (fauna = L4, stage 6, import set),
  `docs/plans/scenarios-world.md` (W1 — the water/swim hazard analog), `docs/core/glossary.md` (the coined terms).
