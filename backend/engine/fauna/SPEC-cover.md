# SPEC — `engine/fauna` · Cover mechanics (M3 hiding · M4-b speed resistance · M5-b ambush concealment)

> Status: `DRAFT`
> Sub-spec of: [`SPEC.md`](SPEC.md)  ·  Owner agent: `<filled by implementer>`
> Scope = **M3 / M4-b / M5-b** (fauna-realism; gates RESOLVED 2026-07-02 — rationale:
> `docs/plans/fauna.md` 클러스터 10, deliberation: `docs/decisions/fauna-gates.md`).
> World-side counterpart (hide entry roll · scent suppression · cover drag · concealment computation):
> `backend/engine/world/SPEC-world-fauna.md` §Cover-hiding apply / §Cover speed resistance /
> §Ambush concealment.

## Purpose

`cover`-tagged flora changes the predator-prey game three ways, all data-driven and OFF-neutral:
prey survive by **hiding**, not by out-running (M3); moving through cover **drags movement**
per-species (M4-b); and a predator concealed in cover is **seen late**, so ambush emerges (M5-b).
The cover state fields have a **SINGLE writer — `engine/world`** (it owns flora proximity, the
per-species §6 eval, the seeded roll, and the scent deposit — all already world-side); fauna only
READS `Animal.HiddenUntil` / `Animal.Concealment`.

Interface delta at a glance (over the core [`SPEC.md`](SPEC.md) contract):
- `Animal.HiddenUntil core.Tick` + `Animal.Concealment float64` — world-written, fauna-read (below).
- `SpeciesRule.HideChance *expr.Program` + `func (r *Rules) HideChance(sp SpeciesID, ctx expr.Context) float64` (M3).
- `SpeciesRule.CoverCost float64` + `func (r *Rules) CoverCost(sp SpeciesID) float64` (M4-b).
- `CombatParams` gains `HiddenFlushFactor`/`HideDurationTicks`/`HideCoverFactor` (M3),
  `CoverRadiusFactor` (M4-b), `ConcealFactor` (M5-b) — declared in [`SPEC-combat.md`](SPEC-combat.md).
- NO new `Snapshot`/`Intent`/`AttrOperands()` surface.

## Cover-hiding (M3)

Prey survive by **hiding**, not by out-running: a fleeing herbivore that reaches `cover`-tagged flora may
break the predator's detection for a while. **`HiddenUntil` has a SINGLE writer — `engine/world`** (it
owns flora proximity, the per-species §6 eval, the seeded roll, and the scent deposit — all already
world-side); **fauna only READS `Animal.HiddenUntil`** here. This is the fauna-module interface delta
(behaviour/why → 클러스터 10); the entry roll + scent suppression are world's (SPEC-world-fauna M3).

- **Animal state (M3-e):** add `Animal.HiddenUntil core.Tick` — the animal is HIDDEN while
  `HiddenUntil >= snap.Tick` (0 = visible). Serialized alongside the FC combat fields (F17). fauna never
  writes it (world sets it on entry / clears it on engage); `Step` treats it as read-only snapshot state.
- **§6 hide chance (M3-b, D4/D7):** add `func (r *Rules) HideChance(sp SpeciesID, ctx expr.Context) float64`
  — the species' §6 probability program (`fauna: hide_chance`, e.g. `0.5 + Agility*0.005`). Returns **0 when
  absent** so a species with no `hide_chance` never hides (OFF-neutral; predators omit it). Parallels
  `Rules.Graze`/`Feed`/`AttackPower`/`Hit` exactly. `ReadsAttrs()` ⊆ `AttrOperands()` ∪ drive ids and
  `Reads()` ⊆ stats registry are cross-checked by `platform/config` like every other §6 (no new operand —
  `Agility` rides the existing `Stat` channel). Evaluated WORLD-side in apply (world holds `Rules`).
- **Detection exclusion (M3-c, combatTarget):** `combatTarget` SKIPS a candidate `other` with
  `other.HiddenUntil >= snap.Tick` **UNLESS** `dist <= snap.Combat.HiddenFlushFactor * snap.ScentCellSize`
  (the point-blank **flush** exception — a predator at the prey's feet still finds it). A hidden prey is
  thus un-targetable at range; combined with the world-side `scent:prey` deposit suppression it is fully
  "invisible" (sight+scent) until flushed or the timer expires.
- **Crouch / bolt steering (M3-a/d):** in the STEER phase, if the acting animal is hidden
  (`a.HiddenUntil >= snap.Tick`) it **crouches** — `NextPos == Pos` (holds still in cover, so it never
  wanders out of cover on its own; heading unchanged), OVERRIDING the chosen action's steer — **UNLESS** a
  predator is within the flush radius (`dist.predator <= HiddenFlushFactor * ScentCellSize`), in which case
  it **bolts** (steers normally, i.e. resumes Flee). It does not clear `HiddenUntil` itself (world owns the
  field); once flushed the predator's `combatTarget` flush exception + the world engage-clear take over.
- **Params (balance):** `CombatParams` gains `HiddenFlushFactor float64` (× `ScentCellSize` → flush radius;
  used by `combatTarget` + the bolt test), `HideDurationTicks int` and `HideCoverFactor float64` (× cell →
  cover reach) — the latter two consumed WORLD-side (entry). All from `content/world.yaml cadence` (D10).
- **Neutrality:** no species carries `hide_chance` ⇒ `HideChance`≡0 ⇒ `HiddenUntil` never set ⇒
  `combatTarget`/steer behave exactly as before; existing goldens byte-identical (the M3 OFF-neutral lever,
  parallel to the FC/Graze levers). NO new `AttrOperands()` entry, NO new `Snapshot`/`Intent` field.

## Cover speed resistance (M4-b)

Moving through `cover` flora slows an animal and makes its speed VARY (a continuous drag, no stumble/fall).
The mechanic is **entirely world-side** (world owns flora; scales the committed move by a cover resistance —
SPEC-world-fauna M4-b); fauna's §6 `Speed` is UNCHANGED. The only fauna-module surface is the per-species
affinity, exposed as a `Rules` accessor world reads in apply (mirrors `TerrainCost`/`Graze`/`HideChance`):

- **`SpeciesRule.CoverCost float64`** + **`func (r *Rules) CoverCost(sp SpeciesID) float64`** — the species'
  cover drag affinity (`content/objects.yaml fauna: cover_cost`, D10). Returns **0 when absent** (species
  unaffected → OFF-neutral). Parsed by `platform/config` like `terrain_cost`; compiled into the per-species
  record. Pure, read-only. NO new `Snapshot`/`Intent`/`AttrOperands` surface, NO change to `Speed`/`Step`.

## Ambush concealment (M5-b)

A predator concealed in `cover` flora is SEEN by prey only at reduced range → it closes the distance before
the prey flees → **ambush emerges** (no per-species ambush FSM, D2/D3). The concealment value is
**world-computed** (world owns flora — SPEC-world-fauna M5-b) and carried on the `Animal` exactly like M3
`HiddenUntil`; fauna only READS it, in the sight channel. §6 `Speed` unchanged; no new operand/Snapshot/Intent seam.

- **`Animal.Concealment float64`** — world-written each tick (SINGLE WRITER = world), ≥0, `0` = fully
  exposed. A derived transient (cover density × world `conceal_factor`); read-only to fauna.
- **`sightQuery` change:** for each predator candidate, skip it when `dist > sightRadius / (1 +
  other.Concealment)` — the twin of the M3 `combatTarget` `other.HiddenUntil` gate (a per-candidate field
  read on `snap.Animals[idx]`, so NO new Snapshot seam). `Concealment=0` ⇒ effRadius = sightRadius ⇒
  unchanged. Only the sight→Flee channel narrows; the scent→Wary channel is untouched (smell ignores cover).
  Deterministic, no RNG (D12).
