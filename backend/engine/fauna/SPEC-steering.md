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
- **`steerFull` / `cheapPath`:** both hold when `MoveDeadband > 0 ∧ speed < MoveDeadband` (the dormant
  cheap path too, so a dormant animal doesn't crawl where an ACTIVE one would stop). Placed **after** the
  existing `speed ≤ 0` hold, so it only tightens the floor.
- **`Snapshot.MoveDeadband float64`** — world-injected from `world.yaml motion.move_deadband` (via
  `config` → `envCfg.FaunaMoveDeadband` → `buildFaunaSnapshot`). A single balance scalar (no per-species
  key in P_move-realism; per-species deadband is a later tuning lever if needed).
- **Neutrality:** `MoveDeadband ≤ 0` ⇒ only the pre-existing `speed ≤ 0` hold applies ⇒ **byte-identical**
  to pre-FM9 (existing world/ecosim goldens hold; the shipped `world.yaml` value is `0.0`, tuned up in
  P_fa4 to just above idle speed). Deterministic, no RNG/operand/state field (D12).
- **NOT here — burst-rest under drive (FM10):** the drive-gated burst→tire→pause→recover rhythm is the
  **existing M2 fatigue axis** (`effort:high` Flee/Hunt ⇒ `applyAnimalFatigue` accrues `fatigue`; every
  species' §6 `speed` reads `− fatigue`; Rest sheds it). FM9 is only the *floor* (idle stop); FM10 added
  **no new mechanism** — its remaining work is fatigue-coefficient tuning (`docs/plans/fauna.md §4.1`
  FM10a, RESOLVED "M2 재사용" 2026-07-10).

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
