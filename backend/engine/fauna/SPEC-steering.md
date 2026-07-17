# SPEC — `engine/fauna` · Steering deltas (M6 turn-rate inertia · M7 multi-predator flee · FM9 locomotion deadband)

> Status: `DRAFT`
> Sub-spec of: [`SPEC.md`](SPEC.md)  ·  Owner agent: `<filled by implementer>`
> Scope = **M6 / M7** (fauna-realism; gates RESOLVED 2026-07-02 / 2026-07-08 — rationale:
> `docs/plans/fauna.md` 클러스터 10) **+ FM9** (P_move-realism deadband; gate RESOLVED 2026-07-09/-10 —
> `docs/plans/fauna.md` §4.3, deliberation: `docs/decisions/fauna-gates.md`).
> M6/M7 are **entirely fauna-side** (M6 adds only the `turn_rate` content key). FM9 adds one
> world-injected balance scalar (`Snapshot.MoveDeadband`, `world.yaml motion.move_deadband`). All
> three refine the SENSE/STEER steps of the core `Step` pipeline.

## Purpose

Two refinements of the core steering: an animal cannot instantly reverse heading (M6 — per-species
`turn_rate` makes prey juke and heavier predators overshoot, both emergent), and a prey fleeing ≥2
simultaneously visible predators aggregates ALL of them instead of running into the second half of a
pincer (M7).

Interface delta at a glance (over the core [`SPEC.md`](SPEC.md) contract):
- `SpeciesRule.TurnRate *expr.Program` + `func (r *Rules) TurnRate(sp SpeciesID, ctx expr.Context) float64` (M6).
- M7 adds NO public surface (an internal `sightQuery`/`baseSteerDir` change only).

## Turn-rate inertia (M6)

An animal cannot instantly reverse heading: each tick the desired steering heading is **clamped to within
`±turn_rate×DT`** of its current `Heading`. Asymmetric per-species `turn_rate` gives the juke/overshoot
dynamic — nimble prey out-turn a heavier predator, which cannot cut sharply and **overshoots** (it keeps going
more-forward). This is **entirely fauna-side** (steering only; no world/state/serialization change), reuses the
existing `Speed`/`HideChance` §6 accessor pattern and the existing `angularDiff` helper, and adds **no**
`Snapshot`/`Intent`/operand/RNG. `Heading` is the only state, already present.

- **`SpeciesRule.TurnRate *expr.Program`** + **`func (r *Rules) TurnRate(sp SpeciesID, ctx expr.Context) float64`**
  — max turn RATE (radians per unit time, `×DT` gives per-tick cap), a §6 composition (D7 — e.g.
  `base + Agility*k`, so agility emerges; parallels `Speed`). **Returns 0 when the program is nil/unauthored,
  and `turn_rate ≤ 0` MUST mean "unlimited — do NOT clamp"** (a 0 here is the OFF-neutral sentinel, NOT a
  frozen turn). `content/objects.yaml fauna: turn_rate` (D10), compiled by `platform/config` like `speed`/`hide_chance`.
- **`steerFull` clamp (the one behavioural change):** after computing the desired heading
  `desired := atan2(dir) + wander`, clamp it toward `a.Heading`:
  ```
  tr := rules.TurnRate(a.Species, ctx)
  if tr > 0 {
      maxTurn := tr * snap.DT
      if d := angularDiff(desired, a.Heading); math.Abs(d) > maxTurn {
          desired = a.Heading + math.Copysign(maxTurn, d)  // no normalize helper needed: cos/sin + angularDiff tolerate any angle
      }
  }
  heading := desired   // then dir = {cos,sin}(heading); movement + nextHeading use the clamped heading
  ```
  Movement then proceeds along the **clamped** heading (so a predator that can't turn enough overshoots).
- **Neutrality:** `turn_rate` unauthored (⇒ `TurnRate≡0`) or authored ≥ `π/DT` ⇒ clamp never binds ⇒
  `heading == desired` ⇒ movement byte-identical to today. Deterministic, no RNG (D12). Content-only asymmetry
  (D10): prey `turn_rate` high (juke), predators low (overshoot).
- **Scope (M6-c):** heading-rate only; speed `accel` inertia is deferred. **Cheap/DORMANT path:** the clamp
  lives in `steerFull` (full pipeline); `cheap.go` mostly holds `Heading`, so it is naturally inertial — apply
  the same clamp there only if a cheap-steer branch changes heading materially (verify at implementation).

## Locomotion deadband (FM9)

**"위협·필요 없으면 대체로 안 움직임" (energy conservation).** After the §6 `Speed` eval, if the speed
is **below `Snapshot.MoveDeadband`** the animal **HOLDS position** (`NextPos == Pos`, `NextHeading ==
Heading`) instead of crawling forward. This kills the classic field-blend failure mode of restless
drift / constant gliding (`docs/plans/fauna.md §4.3` realism 수용기준 ③ "기본 정지").

- **Why a speed threshold, not a blend-vector magnitude (FM9a, RESOLVED 2026-07-10):** `baseSteerDir`
  returns a **unit direction** (keystone = §6 discrete pick + a thin blend, `docs/plans/fauna.md §4.0`),
  so the blend vector carries no drive magnitude. Drive magnitude lives in **§6 `Speed`** (species author
  `speed` reading `fear`/`hunger`/… — e.g. deer `Agility*0.003 + fear*0.2 − fatigue*0.1`). So a
  sub-deadband speed *is* the "no salient drive" signal; the deadband is applied to `Speed`.
- **`steerFull` / `cheapPath`:** both hold when `db > 0 ∧ speed < db`, where `db :=
  rules.moveDeadband(species, snap.MoveDeadband)` (the dormant cheap path too, so a dormant animal doesn't
  crawl where an ACTIVE one would stop). Placed **after** the existing `speed ≤ 0` hold, so it only
  tightens the floor.
- **Per-species deadband (FM14a, RESOLVED 2026-07-12):** `rules.moveDeadband(species, global)` returns the
  species' own `SpeciesRule.MoveDeadband` (content `objects.yaml fauna.move_deadband`) when POSITIVE, else
  the global `Snapshot.MoveDeadband`. A single global scalar can't separate a fast idler (rabbit idle ≈
  Agility·0.004 ≈ 0.34) from a slow forager whose graze-seek speed equals its idle speed (deer ≈ 0.21) —
  stopping the former with a global would freeze the latter. Per-species overrides break that cross-species
  tie; the global stays the fallback (unauthored species + FM9 off-lever byte-identical).
- **`Snapshot.MoveDeadband float64`** — world-injected global fallback, from `world.yaml
  motion.move_deadband` (via `config` → `envCfg.FaunaMoveDeadband` → `buildFaunaSnapshot`).
- **Neutrality:** species `move_deadband` unset (0) AND global `MoveDeadband ≤ 0` ⇒ only the pre-existing
  `speed ≤ 0` hold applies ⇒ **byte-identical** to pre-FM9 (existing world/ecosim goldens hold; shipped
  values are all `0.0`). Deterministic, no RNG/operand/state field (D12).
- **Values need a §6 hunger term (FM14b, RESOLVED (a) 2026-07-12):** a deadband set *above* idle speed also
  freezes FORAGING unless purposeful movement is faster than idle wander — and originally **no species' §6
  `speed` rose with hunger** (only `fear` did, deer/rabbit), so idle-wander == graze-seek speed. The fix is
  a **hunger-restlessness term** added to each herbivore's §6 `speed` (`+ hunger·k`): a sated animal
  (hunger≈0) sits at idle speed and is held by its deadband (rest, energy conservation); a hungry one's
  speed rises above the deadband and it roams to forage (exploration even with no food scent in range; the
  `Graze` steer then homes on food when scent appears). So **rest-when-sated + roam-when-hungry emerges from
  the §6 sign** (D2/D10), not a state flag. Applied to **herbivores only** (deer/rabbit/goat) with
  per-species `move_deadband` just above each species' sated idle speed (deer 0.27>0.21, rabbit 0.42>0.34,
  goat 0.21>0.174 — all UNTUNED, P_fa4); predators/fish keep `move_deadband` 0 (always roam) to leave the
  hunt-success band undisturbed. Verified: arena hunt-success 12.8% (in band), prey population stable over
  3000 ticks (no deadband-starvation). Wall-piling itself is fixed by FM13 (reflect), independent of this.
- **NOT here — burst-rest under drive (FM10):** the drive-gated burst→tire→pause→recover rhythm is the
  **existing M2 fatigue axis** (`effort:high` Flee/Hunt ⇒ `applyAnimalFatigue` accrues `fatigue`; every
  species' §6 `speed` reads `− fatigue`; Rest sheds it). FM9 is only the *floor* (idle stop); FM10 added
  **no new mechanism** — its remaining work is fatigue-coefficient tuning (`docs/plans/fauna.md §4.1`
  FM10a, RESOLVED "M2 재사용" 2026-07-10).

## Boundary de-sink: blocked-heading commit + reflect (FM13)

**"동물들이 다들 벽에 가서 벽을 비빈다" fix** (`docs/plans/fauna.md §4.4`, P_dist1). The map boundary was an
absorbing sink: an animal that steered outward got its position clamped back in but kept its outward heading,
so it slid along the wall; and an animal that steered into impassable terrain had its heading **frozen**, so
it re-proposed the same blocked step every tick. Two composed fixes turn the edge from a sink into a mirror:

- **Blocked-heading commit (fauna, `steerFull`):** when the tentative `NextPos` is blocked
  (`FootprintBlocked ∨ !passable`), HOLD position (`NextPos == Pos`) but return the **already-turned
  `heading`** (post wander + hazard blend + turn-rate clamp) instead of the old `a.Heading`. The step is
  still held; only the heading is no longer frozen, so the next tick re-evaluates from a new angle and the
  animal rotates off deep sea / a cliff (hazard repulsion + wander do the work) rather than pinning against
  it. Byte-identical for any animal whose step is NOT blocked. The `cheapPath` hold is unchanged (dormant,
  no turn to commit).
- **Boundary reflection (world, `reflectAtBounds`, `commitAnimalOwnState`):** replaces the bare
  `clampToBounds` in the animal own-state commit. It clamps `NextPos` into `[Min,Max]` AND, for each wall it
  overshot, **reflects the outward heading component** (`vx`/`vy` sign flip) so the animal bounces back inside
  instead of sliding along the edge. Only fires when a wall is actually crossed; an interior commit returns
  `(pos, heading)` unchanged (byte-identical to the old clamp path). `clampToBounds` itself is retained for
  respawn placement. Deterministic (pure trig, no RNG/state).
- **Distribution rationale:** a reflecting boundary makes a wandering (persistent-random-walk) population
  equilibrate toward roughly uniform interior density instead of collecting at the walls — with a thin
  residual edge layer that later `move_deadband` tuning (FM14, deferred pending the FM14a shape gate) calms.
  Respawn stays **near-live** (FM15, unchanged): once survivors are spread through the interior, near-live
  respawn seeds there too.

## Water attraction (FM4)

**Thirst-driven pull toward drinkable water.** A species that authors a `thirst` drive + a `seek:water`
action steers **up the water-attraction field Gradient** (toward the nearest drinkable water) when that
action wins §6 utility. Motivation lives in §6 (the thirst-gated action utility); DIRECTION lives in the
field (D5) — exactly the food/`seek:food` pattern, but the direction is the `Snapshot.WaterField` Gradient
instead of a scent channel.

- **`Snapshot.WaterField WaterSampler`** (`Gradient(p) core.Vec2` = UNIT direction toward the nearest/
  strongest drinkable-water source). world builds ONE shared static attraction field from the config-derived
  drinkable-terrain cells and injects it (dependency inversion, like `HazardSampler`; world adapts
  `*engine/space/field.Field` whose `Gradient` = toward increasing intensity = attraction). nil ⇒ no water
  steering.
- **`steerFull`:** computes `waterDir` **only when the chosen steer channel is `TagSteerWater`** (`seek:water`),
  from `snap.WaterField.Gradient(a.Pos)`; a non-drinking species never touches the field. `baseSteerDir`'s
  `TagSteerWater` case returns `waterDir`, else falls through to continue-heading (nil field / flat gradient ⇒
  no spurious pull). The always-on hazard/turn-rate/deadband stages then apply as usual (a strong flee/hazard
  still out-pulls the water attraction — it is one pull among the blend, not a lock).
- **Thirst recovery (world side, FM4):** while on a drinkable cell and enacting the `seek:water` action, the
  world reduces `thirst` by the species' §6 `Drink` factor — the exact mirror of `Graze` (hunger↓ at forage).
  See `docs/plans/world-integration.md` / `SPEC-world-fauna.md`.
- **Neutrality:** no species authoring `thirst`/`seek:water` ⇒ the field is built but never queried and the
  recovery never fires ⇒ **byte-identical** to pre-FM4 (world/ecosim goldens hold). Deterministic (D12): the
  field is a static index; no RNG/wall-clock; the drinkable source set is order-free (membership only).
  Source identification is **config-time terrain-attr derivation** (salinity/moisture), NOT a Go
  terrain-name switch and NOT runtime `terrainAttrs` (`docs/plans/fauna.md §4.1` FM4-src).

## Multi-predator flee steering (M7)

With ≥2 predators simultaneously visible (both FOV and concealment gates passed), fleeing from only the
nearest one runs the prey INTO the second predator of a pincer. The fix is **entirely fauna-side** (no
world/config/content change): the flee direction aggregates **all** visible predators as a
distance-weighted repulsion — real ungulates slip out sideways between two closing predators, and that
lateral escape **emerges** from the vector sum (D2/D3, no cornering FSM).

- **`sightQuery` accumulation:** for EACH predator passing the FOV/concealment gates, accumulate the
  repulsion contribution `(a.Pos − ent.Pos)/dist²` in `nearby`-slice order (ObjectID-sorted by spatial ⇒
  fixed-order float summation, D12) and count it. Nearest-predator tracking (`distPred`/`nearPredPos`) is
  **unchanged** — fear scaling, the M3 flush radius, and hidden checks stay nearest-based. The query
  returns an extra `fleeDir *core.Vec2`: `normalize(Σ)` iff `predCount ≥ 2 ∧ |Σ| > 0`, else `nil`
  (a ~zero sum from a symmetric pincer falls back to the single-nearest path — deterministic).
- **`baseSteerDir`:** the `flee:predator` / `wary:predator` branches return `*fleeDir` when non-nil, else
  the legacy `normalize(Pos − nearPredPos)` single-target direction. Scent-flee fallback, `dist.predator`
  consumers, and the DORMANT cheap path (no sight-flee) are untouched.
- **Neutrality:** with at most one visible predator the aggregate never fires, so behaviour is
  **byte-identical** to the pre-M7 single-target path (existing world/ecosim goldens hold). The
  inverse-square exponent `p=2` is a fauna constant (hot loop; promote to balance only if tuning demands
  it). Nearer predators dominate the sum, yet a second one bends the escape sideways. No new `Snapshot`
  seam / operand / RNG / config.

## Scent-tracking confidence (PD1 · P_fa4a)

**A faint scent gives an unreliable DIRECTION.** The scent field already fades a channel's *intensity* with
distance + wind, but `ChannelReading.Dir` is a **unit** gradient — full-precision no matter how faint — so a
predator homed on a whiff of far-off prey just as accurately as on a kill at its feet. That let a predator
vacuum the whole map (no spatial refuge for prey) and is the precondition-gap for emergent
starvation/reproduction (`docs/plans/fauna.md §5`, PD1 RESOLVED 2026-07-16). The fix degrades tracking
*confidence* as intensity falls, **entirely fauna-side** (steering only; the shared scent grid stays a pure,
RNG-free physical field — the sensor imperfection is a property of the animal's NOSE, not the field, D5/PD1a).

- **Where (`steerFull`, after `baseSteerDir`, before the hazard/turn-rate stages):** for the scent-**homing**
  channels only — `TagSteerFood` (Food), `TagSteerPrey` (Prey), `TagFeed` (Carrion), i.e. steer-**toward** a
  source — blend the resolved scent gradient toward the heading-continuation baseline (the same "keep
  going / wander" direction `baseSteerDir` uses at zero scent) by a confidence weight:

  ```
  confidence = g·I / (1 + g·I)          I = that channel's Reading.Intensity (committed, next-tick latency)
                                        g = Rules.ScentAcuity(species, ctx)  (§6, keenness gain ≥ 0)
  dir = headingDir·(1 − confidence) + scentDir·confidence     headingDir = (cos Heading, sin Heading)
  ```

  `I → ∞` (at the source) ⇒ confidence → 1 ⇒ exact scent Dir. `I → 0` (faint) ⇒ confidence → 0 ⇒ the animal
  wanders (can't localize). **Higher `g` = keener nose** (homes at a lower intensity; half-confidence at
  `I = 1/g`) — a wolf out-tracks a bear via a bigger `g`. Only the resulting ANGLE is used (the later
  `atan2` + wander jitter + turn-rate cap + hazard blend apply unchanged). Flee/`wary` (steer-**away**) and
  early-warning are **not** degraded — a prey's perfect alarm at a faint predator scent HELPS coexistence and
  keeps sight-priority flee intact.
- **`ScentAcuity` (§6, `scent_acuity`, PD1b RESOLVED (c) §6 stat composition):** an OPTIONAL per-species
  keenness gain composed from base stats (e.g. `"10 + Perception*0.15"`, D7 — competence is stat
  composition, a "good tracker" role emerges, not a stored skill). Compiled like `TurnRate`
  (`parseOptionalFaunaFormula`). **Off-lever = neutral:** no `scent_acuity` authored (nil program) OR
  `g ≤ 0` ⇒ the blend is **skipped** ⇒ exact scent Dir ⇒ **byte-identical** to pre-PD1 (species that don't
  author it, and every existing golden, are untouched — a species opts IN by authoring a positive gain).
- **Determinism (D12):** the blend is pure arithmetic over the committed `Reading` + the §6 `ScentAcuity`
  evaluation — **no RNG, no new operand, no `Snapshot` seam**. The `confidence` is a deterministic function
  of state, so same seed ⇒ same steering. NOTE: the RESOLVE preview wrote the confidence as
  `I/(I+scent_acuity)` (higher acuity = *worse*, mis-signed against its own "wolf tracks better" label); the
  shipped form is the keenness-gain `g·I/(1+g·I)` (= `I/(I + 1/g)`), which matches the resolved intent AND
  the preview's numbers (wolf's larger value ⇒ keener).
