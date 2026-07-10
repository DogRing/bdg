# SPEC — `engine/fauna` · Combat & Predation (FC1–FC13)

> Status: `DRAFT`
> Sub-spec of: [`SPEC.md`](SPEC.md)  ·  Owner agent: `<filled by implementer>`
> Scope = **FC1–FC13** (phase 6b; gate RESOLVED 2026-07-01 — rationale: `docs/plans/fauna.md` 클러스터 9,
> deliberation: `docs/decisions/fauna-gates.md`).
> World-side counterpart (apply · death · carcass · feed enact): `backend/engine/world/SPEC-world-fauna.md`;
> content compile: `backend/platform/config/SPEC-world.md` §Fauna combat content.

## Purpose

Refines the F7 "Hunt→death" abstraction into an explicit **engage → exchange → kill → feed** loop.
This file is the **fauna-module interface delta only** over the core [`SPEC.md`](SPEC.md) contract
(behaviour/why → 클러스터 9): fauna PROPOSES the damage intent + engage state; **death (Vital≤0) +
carcass creation are the WORLD's** (`SPEC-world-fauna.md`; F3 "world owns death").

## Mechanism (delta over the core contract)

- **Actions (FC1):** two new SHARED `actions.yaml` entries scored by the SAME horizon-1 utility (D3, no
  FSM): `Attack` (engage + damage exchange, cooldown-gated) and `Feed` (durative carcass consumption →
  hunger↓). Approach stays the existing `TagSteerPrey` steer. Steer/utility tags parallel Graze/Flee/Wary.
- **Animal state (FC7/FC12):** add to `Animal` — `EngagedWith core.ObjectID` (combat partner; "" = free),
  `NextExchangeTick core.Tick`, `EngageCooldownUntil core.Tick`, `VitalCap float64` (≤1; each fight lowers
  it a little ⇒ Vital regens only up to `VitalCap` = "no full recovery", FC7). All serialized (F17).
- **Operands (FC2/FC10):** add to `AttrOperands()` — `target.threat` (candidate target's expected
  retaliation/danger ⇒ predator↔predator emerges ONLY when hunger overrides cost, D2/D4) and `scent.carrion`
  (new scent channel, scavenge homing). `platform/config` cross-checks these like every other operand.
- **§6 formulas (FC4, content, D4/D7):** per-species `attack_power` + `hit` (stat compositions, e.g.
  Strength / Agility-vs-Agility) — NO stored skill; recomputed from current base stats each exchange.
- **Engage/exchange/disengage (FC3/FC5/FC6/FC13):** utility picks `Attack` when a `diet` target is in range;
  a successful engage sets `EngagedWith` on BOTH animals; every `[10,20]`-tick exchange (seeded `envFork`)
  proposes `attack_power×hit` damage to the target (prey never retaliates; predator↔predator both attack).
  Engage-ATTEMPT cooldown `[50,100]` ticks. Disengage when predator stamina drops OR the target is beyond
  `disengage_range` (~2·cellSize). Stamina DRAINS `CombatParams.StaminaDrainPerTick` while engaged and
  recovers `StaminaRecoverPerTick` while free (balance), so a predator tires mid-hunt → disengages →
  recovers → re-engages (burst pursuit): this is where scenario #8 ("predator stops when its stamina drops
  first") emerges. While engaged, locomotion is suppressed (FC13). **Death (Vital≤0) + carcass creation are the
  WORLD's** (SPEC-world-fauna; F3 "world owns death") — fauna only PROPOSES the damage intent + engage state.
- **Feeding & diet (D10 tag-driven, F7).** A predator's `Diet` matches the TARGET's OWN content tags, NOT its
  SpeciesID: `SpeciesRule.Tags` carries each kind's tags (config-populated from `objects.yaml`), and
  `combatTarget` engages a candidate iff `diet ∩ target.Tags ≠ ∅` (e.g. wolf `diet:[game]` matches a deer
  carrying the `game` tag). The herbivore side mirrors Feed: a `seek:food` (Graze) action, when the animal
  has reached a `scent:food` flora, crops it — the world reduces hunger by the species' `Graze` §6
  (`Rules.Graze`, parallels `Rules.Feed`). Absent `Graze`/`Tags` ⇒ outcome-neutral.

## Interface delta (Go surface added to the core Public Interface)

```go
// Animal gains the combat state (FC7/FC12; all serialized, F17):
    VitalCap            float64       // upper bound for Vital regen after combat scars ("no full recovery", FC7); 0 = full cap
    EngagedWith         core.ObjectID // combat partner; empty = free
    NextExchangeTick    core.Tick     // next tick an engaged attack may propose damage
    EngageCooldownUntil core.Tick     // next tick an engage attempt may be made

// Snapshot gains the balance params + the scent-cell scale their range factors multiply:
    Combat        CombatParams
    ScentCellSize float64

// CombatParams carries balance-authored combat timing/range/regeneration values
// (content/world.yaml cadence → platform/config `EnvConfig.FaunaCombat`, D10). Zero values are
// neutral until platform/config wires real content values. Later deltas RIDE this struct too:
// the M3/M4-b/M5-b cover params (→ SPEC-cover.md), the M2 endurance rates, and the SS2
// sleep-recovery rate (core Sleep torpor).
type CombatParams struct {
    ExchangeMinTicks           int     // engaged exchange every [min,max] ticks (seeded envFork, FC5)
    ExchangeMaxTicks           int
    EngageCooldownMinTicks     int     // engage-ATTEMPT cooldown [min,max] ticks (FC5)
    EngageCooldownMaxTicks     int
    DisengageRangeFactor       float64 // × Snapshot.ScentCellSize = disengage_range (FC6)
    StaminaDropThreshold       float64 // predator disengages when its stamina drops below this (FC6)
    StaminaDrainPerTick        float64 // stamina spent per tick while engaged in combat (FC6 / scenario #8)
    StaminaRecoverPerTick      float64 // stamina regained per tick while not engaged
    FatiguePursuitPerTick      float64 // fatigue gained per tick during a high-effort chase/flight (M2 endurance)
    FatigueRecoverPerTick      float64 // fatigue shed per tick while resting/low-effort (M2)
    SleepFatigueRecoverPerTick float64 // fatigue shed per tick while SLEEPING (state:sleep) — deep-torpor rate,
                                       // > FatigueRecoverPerTick (SS2/FM11b); ≤0 ⇒ no deep bonus (falls back to
                                       // the ordinary FatigueRecoverPerTick, never LESS than resting)
    VitalRegenPerTick          float64 // free (un-engaged) Vital regen, clamped by VitalCap (FC7)
    VitalCapDamageFraction     float64 // fraction of exchange damage permanently lowering the target's VitalCap (FC7)
    HiddenFlushFactor          float64 // × ScentCellSize = flush radius for hidden prey (M3 → SPEC-cover.md)
    HideDurationTicks          int     // M3 hide duration (world-side entry → SPEC-cover.md)
    HideCoverFactor            float64 // × ScentCellSize = cover reach (world-side, M3 → SPEC-cover.md)
    CoverRadiusFactor          float64 // × plant Width = cover-drag radius (world-side, M4-b → SPEC-cover.md)
    ConcealFactor              float64 // × cover density = predator concealment (world-side, M5-b → SPEC-cover.md)
}

// Intent gains the combat proposal fields (world applies them; fauna never mutates):
    Vital                float64       // proposed self Vital regen, clamped by VitalCap
    VitalCap             float64       // proposed self VitalCap
    EngagedWith          core.ObjectID // proposed combat partner; empty = disengaged/free
    NextExchangeTick     core.Tick     // proposed next exchange tick
    EngageCooldownUntil  core.Tick     // proposed next engage-attempt tick
    Damage               float64       // damage world applies to Target; 0 = no exchange this tick
    TargetVitalCapDamage float64       // permanent VitalCap reduction world applies to Target (FC7)

// SpeciesRule gains the combat/feeding content (compiled §6 by platform/config, D4/D7/D10):
    AttackPower *expr.Program // §6 damage magnitude composition (FC4)
    Hit         *expr.Program // §6 hit multiplier/probability composition (FC4)
    Feed        *expr.Program // §6 carcass feed value composition (FC8)
    Graze       *expr.Program // §6 herbivore graze hunger-recovery factor (parallels Feed)
    Tags        []core.Tag    // this kind's OWN content tags — what another animal's Diet matches against (D10)

// Rules accessors (evaluated from CURRENT base stats each exchange — no stored skill, D7). Pure.
func (r *Rules) AttackPower(sp SpeciesID, ctx expr.Context) float64 // 0 when absent (species cannot attack)
func (r *Rules) Hit(sp SpeciesID, ctx expr.Context) float64         // 1 when absent (AttackPower may stand alone)
func (r *Rules) Feed(sp SpeciesID, ctx expr.Context) float64        // 0 when absent
func (r *Rules) Graze(sp SpeciesID, ctx expr.Context) float64       // 0 when absent
func (r *Rules) Diet(sp SpeciesID) []core.Tag                       // the F7 diet tags (world feed/carcass wiring reads it)
```

> **Provenance note (SPEC-first honesty):** the mechanism above is fully decision-backed (`docs/plans/
> fauna.md` 클러스터 9 FC1–FC13 · `SPEC-world-fauna.md` §Combat, death & carcass apply · `SPEC-world.md`
> §Fauna combat content · the `content/world.yaml cadence` keys, which the `CombatParams` field names map
> 1:1). Three **name-level** details were never spelled out in a prior SPEC/plan and are documented here
> FROM the shipped surface: (1) the seven `Intent` field NAMES (the proposal carriers of FC7/FC12 — the
> mechanism "fauna PROPOSES damage + engage state, world applies to both animals" is decided; the names
> mirror the FC12 `Animal` fields), (2) the `Rules.Diet` accessor NAME (the F7 `Diet` field is decided;
> the accessor is its read surface), (3) `Hit`'s **1-when-absent** default (multiplicative identity, so
> `AttackPower` may stand alone). Renaming/changing any of these is a SPEC change first (AGENTS.md #3).

- **Steer tags:** `TagAttack = "combat:attack"` (engage/exchange against a diet target) and
  `TagFeed = "feed:carrion"` (steer toward carrion scent / feed target) join the recognized
  steer-channel tag set (`SpeciesRule.SteerChannel`; content places them on the `Attack`/`Feed`
  actions in `content/actions.yaml`, D10).
- **Operands:** `AttrOperands()` gains `target.threat` + `scent.carrion` (FC2/FC10). `scent.carrion`
  reads the scent grid's carrion channel (`backend/engine/space/scent/SPEC.md`, `ChanCarrion`).

## Neutrality (the FC OFF lever)

No species content ⇒ the delta is outcome-neutral: absent `Attack`/`Feed` utilities and absent
`attack_power`/`hit`/`feed`/`graze` §6 programs mean no engage, no exchange, no feeding; absent
`Graze`/`Tags` ⇒ outcome-neutral (per the Feeding & diet bullet); `CombatParams` zero values are
neutral until `platform/config` wires real content values. Predator↔predator is NEVER a special-case
rule — it emerges only via the `target.threat` utility cost (D2, gate).
