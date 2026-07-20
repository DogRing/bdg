# SPEC — `engine/world` · Fauna & Scent Wiring (animals · combined apply · scent driving)

> Status: `DRAFT`
> Sub-spec of: `SPEC.md`  ·  Owner agent: `<filled by implementer>`
> Scope = **WI-P2** (`docs/plans/world-integration.md` §2). Builds on `SPEC-world-env.md` (WI-P1 navmap/climate).

## Scope

This sub-spec wires the **reduced-reactive animal controller** (`engine/fauna`) and the **shared
scent field** (`engine/space/scent`) into the world tick loop. It adds: the **live animal set**
(world-owned, `fauna.Animal`), `fauna.Step` in the **plan phase** (read-only, alongside agent
planning), the **combined agent+animal apply** in ONE sorted-ObjectID stream (F41 / W5), the
**scent deposit/spread/commit** driving in the Phase 4-ENV sub-phase (the WI-P1 step 4 placeholder),
and the world-side **adapters** fauna declares — `EnvSample` (climate `CellAt`/`Wind` → animal
Context) and `TerrainSampler` (navmap → `{FootprintBlocked, TerrainAt, BaseCost}`). The world stays the **sole mutator**
of animal state (move/heading/drives/stamina/vital/death, D12).

It depends on WI-P1: animals read the **navmap** (TerrainSampler) and **climate** (EnvSample) that
`InstallEnv` installs; the scent grid's `Wind` comes from `climateState.Wind()`. **Carcass +
`Butcher` + full species activation/re-baseline are P_fa3** (deferred, Out of Scope) — WI-P2 is the
core controller wiring; reproduction stays the legacy timer (W7).

Numerics (scent cell, `scent_spread`, motion DT, fauna cadence) come from `content/world.yaml`
(`docs/plans/world-integration.md` §0-9); none hardcoded (D10).

---

## Ownership & installation

### New owned state (added to `World`; empty when fauna not installed)
- `animals map[core.ObjectID]*fauna.Animal` + a precomputed **sorted-ObjectID slice** (the one
  iteration order, D12) — the live animal set. world is the sole mutator (apply phase).
- `scent *scent.Grid` — the shared scent field (world-owned shared index, F36). `cellSize` from
  `content/world.yaml grids.scent_cell_size`. Fed by world from `scent:<channel>`-tagged emitters
  (flora/fauna/decay), read by fauna (+ later perception). DERIVED state — rebuilt from emitter
  positions on resume, not separately serialized (scent SPEC).
- `scentEmitters map[core.Tag][]core.Tag` — content-extracted kind id → sorted `scent:<channel>`
  tags. The world resolves known tag tokens to scent channels; unknown future tokens are skipped
  deterministically until their channel exists.
- `faunaRules *fauna.Rules` — compiled per-species table (`content/objects.yaml fauna:` via expr).
- `faunaCfg` — the `EnvConfig` fauna/scent extension (below).

### `EnvConfig` extension (world.yaml fauna/scent keys)
```go
// Added to the SPEC-world-env.md EnvConfig (one struct; platform/config fills from content/world.yaml):
type EnvConfig struct {
    // ── (WI-P1 fields: Min/Max, NavmapCellSize, ClimateGrid*, ClimateStep/FloraStep/DecayStep) ──
    ScentCellSize  float64       // grids.scent_cell_size — MUST be ≥ MaxSpeed × ScentSpread (cell-skip floor, W3)
    ScentSpread    int           // cadence.scent_spread — Ns, scent diffusion bulk pass
    FaunaDT        float64       // motion.fauna_dt — locomotion time-step magnitude for one tick (fauna.Snapshot.DT)
    FaunaCadence   fauna.Cadence // cadence.fauna_dormant_period / fauna_wake_cooldown (F45)
    // motion.{herbivore,predator}_base_speed/max_speed are SPECIES content (objects.yaml fauna:) — MaxSpeed
    // is also surfaced here only to assert the ScentCellSize ≥ MaxSpeed·ScentSpread floor at load (platform/config).
    MaxSpeed       float64       // motion.max_speed — for the load-time scent-cell floor check (W3)
}
```

### Construction
```go
// InstallFauna installs the animal controller + scent field. Requires InstallEnv first (animals read
// the navmap/climate it installed). Until called, fauna+scent are OFF: fauna.Step is never invoked,
// no scent deposit/spread/commit runs, and the world behaves byte-identically (fauna-OFF neutrality,
// fauna SPEC — the legacy `prey` timer-respawn object is untouched, W7). Animals may be empty (the
// scent grid + plan call exist but produce nothing = neutral).
func (w *World) InstallFauna(cfg EnvConfig, faunaRules *fauna.Rules, scentEmitters map[core.Tag][]core.Tag, coverKinds map[core.Tag]bool, animals []fauna.Animal)
// (cfg is the SAME EnvConfig from InstallEnv, now carrying the fauna/scent fields; the world builds
//  scent.New(cfg.ScentCellSize) and seeds the animal set + spatial-hash entries for each animal.)
```

`platform/config` (WI-P0) builds `faunaRules` from `content/objects.yaml fauna:` blocks (via
`engine/kernel/expr`), validates `ReadsAttrs() ⊆ fauna.AttrOperands() ∪ species drive ids`, and
asserts `ScentCellSize ≥ MaxSpeed × ScentSpread`. The world receives them built (architecture §1).

### Shared id space (W5)
Agents (`AgentID`) and animals (`ObjectID`) share **one string id space** so the combined apply
sorts them in one order. The world allocates animal ids disjoint from agent ids (convention:
distinct prefix, e.g. `an:<n>`, minted by `w.allocObjectID`); the combined apply sort is plain
lexicographic over the raw id string (D12). The spatial hash already holds agents/objects/animals in
the shared `ObjectID` space (spatial SPEC).

---

## fauna.Step in the PLAN phase (Phase 2)

`fauna.Step` is a **pure read-only transform** over the frozen snapshot → it runs in **Phase 2
(PLAN)** beside agent planning, and its `[]Intent` is collected with agent intents (Phase 3). It is
called **every tick** (active/dormant is internal to fauna, F45). The world builds the
`fauna.Snapshot` from frozen state:

```
snap.Animals = w.animals in sorted ObjectID order (copied; Step never mutates)
snap.Scent   = w.scent                                   // READ side only (committed buffer, next-tick latency)
snap.Spatial = w.spatial                                 // shared proximity index (F44 sight query)
snap.Terrain = terrainSampler{ nav: w.nav }              // adapter, below (navmap exposes FootprintBlocked/TerrainAt/BaseCost)
snap.Env     = buildEnvSamples()                         // per-animal climate sample, below
snap.Tick    = w.tick
snap.Cadence = cfg.FaunaCadence                          // F45 dormant period / wake cooldown
snap.DT      = cfg.FaunaDT
fork         = envFork(w.tick, "fauna")                  // disjoint from agent + climate/flora/decay forks
intents      = fauna.Step(snap, w.faunaRules, fork)      // one Intent per animal, sorted
```

- The snapshot is frozen (Phase 1); `fauna.Step` reads only it + its fork (parallel-safe with agent
  planning — it can run as one task in the plan fan-out, or sequentially; the result is order-independent).
- `Env`/`Scent`/`climate` reflect the LAST tick's committed state (next-tick latency for scent, F33;
  climate updated only on its cadence). A missing `snap.Env` entry for a live animal is a world bug
  (fauna.Step panics) — `buildEnvSamples` MUST cover every live animal.

### `EnvSample` adapter (climate → animal Context)
```
buildEnvSamples(): for each animal a (sorted ObjectID):
    cs := climateState.CellAt(a.Pos)                     // CA3 read; D11 index read, never snaps Pos
    Env[a.ID] = fauna.EnvSample{ Temperature: cs.Temperature,   // °C
                                 Moisture:    cs.Moisture,       // [0,1]
                                 Wind:        scent.Wind{ Dir: climateState.Wind().Dir,
                                                          Mag: climateState.Wind().Mag } }
    // climate OFF (not installed) ⇒ neutral EnvSample (Temperature neutral, Wind{0,0}) ⇒ apparent_temp
    // neutral + scent spread local (fauna F10/F40/F33). The animal Context maps these onto the §6
    // operands temperature/moisture/wind.dir/wind.mag (fauna AttrOperands).
```

### `TerrainSampler` adapter (navmap → fauna's 3-method `{FootprintBlocked, TerrainAt, BaseCost}`)
fauna declares `TerrainSampler interface { FootprintBlocked(p)bool; TerrainAt(p)Tag; BaseCost(p)float64 }`
(W10b — species affinity is applied controller-side via `Rules.TerrainCost(species,terrain)`, NOT here).
The world adapter wraps the navmap accessors (D11 index read — continuous `p` → cell, never a snap):
```
terrainSampler.FootprintBlocked(p) = w.nav.FootprintBlocked(w.nav.CellOf(p))   // walls/footprints only (HARD blockers, all species)
terrainSampler.TerrainAt(p)        = w.nav.TerrainAt(w.nav.CellOf(p))          // terrain type id (controller looks up Rules.TerrainCost)
terrainSampler.BaseCost(p)         = w.nav.BaseCost(w.nav.CellOf(p))           // ≥1, override-aware; high for water
```
Per fauna R2/F35/W10b **water is traversable at high cost, NOT a hard blocker** — only footprints/walls
are `FootprintBlocked`. The controller's effective sample: blocked = `FootprintBlocked(p)` OR
`!Rules.TerrainCost(species,terrain).passable`; effective cost = `BaseCost(p) ×
Rules.TerrainCost(species,terrain).mult`. navmap exposes `FootprintBlocked`/`BaseCost` directly (see the
RESOLVED note in Open Questions), so the world does NOT re-join `terrainTypes` for the base cost.

---

## Combined agent+animal apply (Phase 3 collect + Phase 4 apply, F41 / W5)

- **Phase 3 COLLECT** gathers agent `[]Intent` AND animal `[]Intent` into ONE slice, stable-sorted
  by the actor id string (AgentID/animal ObjectID, lexicographic, D12). This single stream is the
  authoritative apply order (extends SPEC-tick.md Phase 3 — same sort, wider id set).
- **Phase 4 APPLY** iterates the combined stream serially. For an **agent** intent: the existing
  outcome/conflict path (SPEC-tick.md). For an **animal** intent the world (sole mutator):
  1. **Conflict**: if the animal's `Action`/`Target` contests a resource with another intent this
     tick (agent or animal), resolve by the relevant stat (the action's `uses:` tag over the
     contender's Real stats — for an animal, `Animal.Stats`), ties by ObjectID (the shared
     conflict model, SPEC-tick.md §Conflict resolution).
  2. **Move**: clamp+reflect `intent.NextPos`/`NextHeading` at the world bounds (`reflectAtBounds`, FM13 —
     see §Boundary reflection), apply cover drag, then `spatial.Move(a.ID, pos)`; set `a.Pos = pos`,
     `a.Heading = reflectedHeading`.
  3. **Commit fauna state**: `a.Drives = intent.Drives` (the passive per-tick evolution), `a.Stamina
     = intent.Stamina`, `a.ActiveUntil = intent.ActiveUntil`, `a.CurrentAction = intent.Action`.
  4. **Layer the action's drive Effect** (world the sole mutator): the enacted action's own effect on
     drives (`Eat`→hunger↓, `Rest`→fatigue↓) is applied HERE on top of the passive `intent.Drives`
     (fauna returns only the passive evolution; the action effect is world's, fauna SPEC Intent note).
  5. **Vital / death** (object-mortality, §7): update `a.Vital` per the outcome; if `Vital ≤ 0`,
     remove the animal (`spatial.Remove`, drop from `animals`+sorted slice) and emit the death event.
- Determinism: one sorted stream, fixed ID order, conflict ties by ID; no map-iteration. The combined
  sort runs AFTER the plan fan-out joins (independent of goroutine order, SPEC-tick.md).

---

## Emergent 2-parent reproduction (pass-2 cross-write; PD4 / P_fa4c-2, `docs/plans/fauna.md` §5)

Conception is a **cross-animal write** — it reads both partners' committed positions and writes both
cooldowns — so it rides the SECOND apply pass alongside combat, after every own-state commit, and
resolves **after** combat on each animal so a partner killed this tick cannot also conceive. Both
partners have therefore already moved when the distance between them is measured, which is what makes
courtship steering pay off.

`applyAnimalMate(intent, byID, fork)` conceives when ALL hold:
1. `intent.MateWith` is set (this animal resolved a partner this tick), and
2. the partner is **also courting** — its committed `CurrentAction` satisfies `fauna.Rules.IsCourting`
   (mutual consent, PD4-vi ⓒ). A partner that is hunting, fleeing, or grazing because it is starving
   does **not** breed: that is what keeps a species' §6 hunger/fear terms in control of its own birth
   rate rather than the mating code.
3. neither partner is refractory (`MateCooldownUntil`), and
4. they are within `conceptionReach() = FaunaCombat.DisengageRangeFactor × ScentCellSize` — the same
   proximity that holds a combat engagement together (PD4-vi ⓐ: reuse, no new species key).

**Exactly one offspring per pairing**: when both animals courted each other in the same tick, only the
lower ID proceeds, so the outcome cannot depend on apply order (D12). Both parents are then set to
`tick + Rules.MateCooldown(species)`.

`spawnOffspring` copies the lower-ID parent and strips everything acquired — `Age = 0`, no drives (a
missing drive key reads as 0, so it starts sated and unafraid), fresh `Vital`/`Stamina` and an unscarred
`VitalCap`, no combat/hiding/cooldown state, no held action, a seeded random heading (a zero heading is
due-east; a whole generation born facing east marches into the east wall, P_dist3). It is placed **at
the parent's position** — guaranteed passable, so no rejection sampling — and emits the existing
`AnimalBorn` event, so downstream consumers need no new type to see emergent births.

`blendParentStats` (PD4-iv) is the **first consumer** of the per-stat `inherit` weight in
`content/stats.yaml`, which until now was loaded and validated but never read. Per stat, in the
registry's canonical sorted order and consuming exactly one draw each (D12):

    raw   = lean·p1 + (1−lean)·p2     // lean ∈ [0,1) seeded — anywhere between the parents
    child = mean + (raw − mean)·(1 − inherit)

`inherit = 1` ⇒ exactly the parental mean; `inherit = 0` ⇒ anywhere between the parents. The variation
magnitude is the **parents' own spread**, so no σ constant is invented and the result is always
bracketed by the two parents. That bracket matters concretely: fauna fixtures author stats on a 0–100
scale while the shared registry declares range `[0,1]`, so anything scaled by the registry's Min/Max or
`gen.sd` would be wrong for animals.

**Off-lever**: a species that authors no Mate action never sets `MateWith`, the intent index is nil, and
the world allocates nothing and behaves byte-identically to pre-P_fa4c-2.

> ⚠️ **Not yet population-safe.** The birth side has no density ceiling until PD4-v lands in P_fa4c-3
> (deferred by design, one new mechanism per phase). Measured on the shipped fixture: rabbits 15 → 2407
> over 16000 ticks, still accelerating — starvation did not cap it in that window, and respawn is a
> FLOOR (re-introduction), not a cap. Do not run this live until the density gate ships.

## Scent driving (Phase 4-ENV step 4 — after intent apply, before tick advance)

The WI-P1 env sub-phase step 4 (placeholder) is realized here. It runs AFTER the combined intent
apply (so animals have moved to their new cells) and is **fully serial** (D12):

```
4. SCENT (fauna/scent installed only):
   a. DEPOSIT (every tick) — for each scent:<channel>-tagged emitter in sorted ObjectID order:
        • predator animals  → scent.Deposit(ChanPredator, pos, mag)   EVERY tick (kept fresh, F33)
        • prey animals      → scent.Deposit(ChanPrey,     pos, mag)   on the bulk cadence
        • edible flora      → scent.Deposit(ChanFood,     pos, mag)   on the bulk cadence
        • (rotting lots/carcass → ChanCarrion: P_fa3, future channel)
      `mag` (intensity ≥ 0) is derived from the emitter's magnitude tag/§6 (D4/D10 — world's content
      classification; the grid only adds what it is told). Emitters carry `scent:<channel>` tags
      (shared with perception.Smell, scent SPEC §Notes). "bulk cadence" = tick % ScentSpread.
   b. SPREAD (tick % ScentSpread == 0) — scent.Spread(climateState.Wind())
        (Wind{0,0} when climate OFF ⇒ isotropic short-range; else downwind diffusion). No RNG (D12).
   c. COMMIT (every tick) — scent.Commit()  → swaps pending→committed, realizing the next-tick
        latency (a deposit at T is visible to fauna.Step reads at T+1). Pending is rebuilt from
        current emitters each cadence ⇒ a vanished source fades (no stale accumulation).
```

- **Why after apply**: deposit needs the post-move positions (predator's current cell) so the F45
  wake (`IntensityAt(ChanPredator, Pos) > threshold`) and the early-warning read are current.
- **Wind source** = `climateState.Wind()` (CA2; WI-P1 owns climate). Climate OFF ⇒ `Wind{0,0}`.
- **Emitter classification** is the world's read of content `scent:<channel>` tags (D4/D10) — which
  kind carries which channel + how `mag` is derived is content, not engine logic.
- **Spoor decay runs first (PD9).** `runScentEnv` calls `scent.DecayTrail()` at the top of the phase,
  BEFORE this tick's deposits, so fresh sign is laid at full strength and only older sign ages. No-op
  when no channel opted in (`content/world.yaml scent_trail`), so the layer is an off-lever. Which
  channels leave trails is content: `prey` is on, `predator` deliberately is NOT — fear is SET from
  `scent.predator`, so a lingering predator trail would make prey permanently wary anywhere a wolf had
  ever walked. Mechanism + tuning invariants: `engine/space/scent/SPEC.md` §Trail.
- **One emitter, one deposit.** A flora plant is registered in BOTH `floraState` and `w.objects`
  (`env.go` PlaceObject on spawn), so the per-object deposit **MUST skip anything present in
  `floraState`** — plants are already deposited by `depositFloraScent` at their **biomass** magnitude
  (`Length+Width`). Depositing them a second time adds a flat `objectScentMagnitude` on top, which
  pegs an eaten-bare plant's food scent at a constant and **silently deletes the PD2 depletion
  feedback** below ("overgrazing ⇒ weaker food scent ⇒ migration pressure"). Regression:
  `TestFloraScentTracksBiomass`.

---

## Determinism (D12) — fauna/scent additions
- **fauna.Step is pure** (plan phase, frozen snapshot + `envFork(tick,"fauna")`, disjoint from agent
  and climate/flora/decay forks); same `(snapshot, Rules, fork)` ⇒ identical `[]Intent` incl. the
  dormant/active partition (F45 ID-phase hash + committed-buffer `IntensityAt`).
- **Combined apply** = one lexicographically-sorted id stream (agents+animals); conflict ties by id;
  no map-iteration; animal set iterated via the sorted slice.
- **Scent** = additive deposit + fixed-order diffusion stencil + commit, no RNG (scent SPEC); deposit
  over sorted emitter ids; spread over sorted cell keys.
- **Cadence** = `tick % ScentSpread` (scent) / fauna every tick (F45 internal) — never wall-clock.

## Outcome-neutral until installed (the WI-P2 lever)
With `InstallFauna` not called: no animal set, `fauna.Step` never invoked, no scent deposit/spread/
commit, the legacy `prey` timer-respawn object (W7) is untouched — every existing world golden holds.
With it installed but **no species placed** (empty animals): `fauna.Step` returns no intents, no
scent is deposited, state is unchanged (fauna-OFF neutrality). Activation (placing species, the °C
re-baseline, carcass/Butcher) is the deliberate **P_fa3** re-baseline.

---

## Acceptance Criteria (testable)
- [ ] **Fauna-OFF neutrality** — `InstallFauna` not called ⇒ `Tick()` byte-identical to WI-P1 (and to
  today); no `"fauna"` fork drawn; legacy prey respawn unchanged. Installed-but-empty ⇒ same.
- [ ] **fauna.Step in plan phase** — every tick the world builds a `fauna.Snapshot` (Animals sorted,
  committed scent, spatial, terrain sampler, per-animal Env, Tick, Cadence, DT) and calls `fauna.Step`
  with `envFork(tick,"fauna")`; a live animal missing from `Env` panics (guard).
- [ ] **EnvSample from climate** — `Env[a.ID]` = `climate.CellAt(a.Pos)` temperature(°C)/moisture +
  `climate.Wind()`; climate OFF ⇒ neutral Env (Wind{0,0}); two animals in one climate cell get equal
  Env. Table-driven.
- [ ] **TerrainSampler semantics (R2/F35/W10b)** — `FootprintBlocked(p)` is true ONLY for footprint
  cells (walls), false for deep-water terrain (traversable); `BaseCost(p)` is the terrain's base cost
  (≥1, high for water, override-aware); `TerrainAt(p)` returns the cell's terrain id. A water cell is
  `!FootprintBlocked` with high `BaseCost`; a wall footprint is `FootprintBlocked`. (Species passability/
  affinity is applied controller-side via `Rules.TerrainCost`, not in the adapter.)
- [ ] **Combined agent+animal apply (F41/W5)** — agent and animal intents apply in ONE
  lexicographically-sorted id stream; an agent↔animal conflict over one resource resolves by the
  relevant stat, ties by id; shuffling collection order yields byte-identical post-apply state.
- [ ] **Animal apply: move/commit/effect/death** — an animal intent moves it (`spatial.Move`+Pos),
  sets Heading, commits Drives/Stamina/ActiveUntil/CurrentAction, layers the action's drive Effect
  (Eat→hunger↓), and on `Vital ≤ 0` removes it (spatial.Remove + drop) and emits the death event.
- [ ] **Scent deposit/spread/commit driving** — predator scent is deposited EVERY tick at the
  predator's post-move cell; food/prey on `tick % ScentSpread`; `Spread` runs on `tick % ScentSpread`
  with `climate.Wind()`; `Commit` every tick; a deposit at T is invisible to `fauna.Step` at T,
  visible at T+1 (next-tick latency); a vanished emitter's scent fades. Table-driven.
- [ ] **Scent uses post-apply positions** — after a predator moves, the predator-channel deposit is at
  the NEW cell (so the F45 wake reflects current proximity).
- [ ] **Disjoint fork** — `envFork(tick,"fauna")` distinct from agent forks and climate/flora/decay
  forks; reproducible across runs/processes; agent count does not perturb it.
- [ ] **Scent-cell floor (load guard)** — platform/config rejects `ScentCellSize < MaxSpeed ×
  ScentSpread` (cell-skip invariant, W3) — a determinism-protecting content check.
- [ ] **Determinism golden** — with fauna+env installed and species/rules present, a fixed
  `(seed, tick sequence)` over N ticks yields a byte-identical digest of animal state + the combined
  intent stream + committed scent; a second run/process reproduces it.
- [ ] **No hardcoded constant (D10 guard)** — no scent-cell/spread/DT/cadence/magnitude literal in
  logic; all from `EnvConfig` (= `content/world.yaml`) / `faunaRules` / content `scent:<channel>` tags.

---

## Out of Scope
- **`fauna.Step` internals** (utility arbitration, drives, steering, F45 cadence, sense channels) →
  `engine/fauna` (`backend/engine/fauna/SPEC.md`). WI-P2 only DRIVES it + applies intents.
- **The scent grid internals** (deposit/spread/read/commit/IntensityAt math) → `engine/space/scent`.
  WI-P2 only schedules the ops + classifies emitters from `scent:<channel>` tags.
- **WI-P1 env state** (climate/flora/decay Step + navmap bridge + flora SiteInput) → `SPEC-world-env.md`.
- **Carcass object + `Butcher` extract + decay-lot mapping; full species activation + °C re-baseline;
  emergent drive-gated reproduction** → **P_fa3 / P_fa4** (fauna SPEC Out of Scope). WI-P2 keeps the
  legacy timer respawn (W7) and the core controller wiring only.
- **`agent.disposition` sight response (F46)** — P_fa1 sight = predator animals only; the agent-entity
  classification in the sight query is **P_fa3** (fauna SPEC). WI-P2 does not inject agent stats into
  the fauna sight query.
- **Serialization of `animals[]` / scent to Snapshot/Redis/SSE** → `platform/persist`/`platform/api`
  + `docs/core/data-contracts.md` (WI-P4, W8). scent is derived (not serialized); animals[] is WI-P4.
- **Parsing `content/objects.yaml fauna:` → `faunaRules`, the `ReadsAttrs` cross-check, the scent-cell
  floor check, and `content/world.yaml fauna/scent` keys + schema** → `platform/config` (WI-P0).
- **`Mine` terrain-driven extraction** (resources R1) → WI-P3.

---

## Open Questions
> `docs/plans/world-integration.md` W1-W9 RESOLVED (human 2026-06-27). Two **plumbing seams** surfaced
> writing this SPEC — neither a mechanism decision (no new behaviour), both small accessor/contract
> shapes to settle before implementation:

- **navmap footprint-only passability + per-cell base cost (TerrainSampler seam) — ✅RESOLVED (a) as
  BUILT, 2026-06-29: `engine/space/navmap` exposes BOTH `FootprintBlocked(cell) bool` AND
  `BaseCost(cell) float64` (now in `navmap/SPEC.md` Public Interface + tested). The TerrainSampler
  adapter calls them directly (above); the world does NOT re-join `terrainTypes` for the base cost.**
  This supersedes the prior `rec:(b)` (world-join) note: option (a) was the listed alternative and is
  what the implemented navmap provides — it is cleaner because `navmap.BaseCost` is **override-aware**
  (reflects `SetTerrain` transitions without the world re-implementing the override lookup) and keeps
  navmap self-contained for its consumers. (Species affinity stays controller-side via
  `Rules.TerrainCost`, W10b.) Background: the fauna `TerrainSampler` needs footprint-only passability
  (water traversable) + a per-cell terrain base cost; navmap's `Passable(cell)` conflates
  terrain-impassable with footprint-block, so `FootprintBlocked` separates them and `BaseCost` gives
  the single-cell terrain cost `StepCost(from,to)` did not expose.
- **Animal id allocation convention (shared id space, W5).** Animals + agents share one string id
  space for the combined apply sort. Options: **(a)** a distinct animal id prefix (`an:<n>`) minted by
  `w.allocObjectID`; **(b)** a single global counter with no prefix (rely on uniqueness). **rec: (a)**
  — prefix keeps logs/why-trace legible and guarantees disjointness from `AgentID`. Document on
  `Spawn`/`allocObjectID`. Non-blocking; finalize at impl.

---

## Combat, death & carcass apply (FC8/FC9/FC12 — phase 6b; rationale: `docs/plans/fauna.md` 클러스터 9)

World-side of the combat loop (world = sole mutator + owns death, F3):
- **Cross-animal damage (FC12):** `applyAnimalIntent` gains a targeted-damage path — an `Attack` intent
  reduces the TARGET animal's Vital by the exchange damage (today it only mutates the acting animal).
  `EngagedWith`/`NextExchangeTick`/`EngageCooldownUntil` are updated on BOTH animals in one apply, in
  sorted-id order (D12); same-target contention reuses the existing combined-conflict resolver.
- **Death → carcass (FC9):** when a prey's Vital≤0, in addition to `removeAnimal` + `AnimalDied`, world
  spawns a `carcass` world object AND a runtime decay lot via **`decay.State.WithLot`** (the new decay
  API), tracks it in `decayLotPos`, and feeds it into `decayEnvInputs`. The carcass carries the
  `scent:carrion` tag → routed to `ChanCarrion` by the SAME tag-driven deposit: add a `carrion` case to
  `scentChannelFromTag` (this is the only engine edit that channel needs — the point of tag-driven).
- **Feed (FC8):** a `Feed` intent consumes carcass supply → reduces the predator's `hunger` drive by the
  carcass's per-state food value (size-proportional). Coexists with the agent-side `Butcher` (materials).
- **Graze (herbivore feeding) + flora depletion (PD2, P_fa4b):** a `seek:food` (Graze) intent, when the
  animal is within one scent cell of a `scent:food` flora object (`applyAnimalGraze`), reduces its `hunger`.
  `applyAnimalGraze` locates the **nearest food-emitting flora PLANT that still has biomass**
  (`nearestForageFloraID`, mirroring `nearestCoverFloraID` — spatial-hash query, ascending-ID tie-break,
  D12) that is in `floraState`, then:
  `want = Graze §6`; `k = FaunaCombat.GrazeDepletion` (flora-Length per 1.0 hunger). If `k ≤ 0` (off-lever)
  or the target is not a flora plant → recover `want`, deplete nothing (**byte-identical to pre-P_fa4b**).
  Else `removed = State.GrazeLength(id, want·k)` (biomass actually cropped, ≤ the plant's Length),
  `recovery = removed / k` ⇒ `hunger -= recovery`. So a healthy plant gives full `want` (removed = want·k);
  a grazed-down plant (Length < want·k) gives proportionally less — **local famine emerges**: overgrazing
  shrinks plants → weaker food scent (mag = Length+Width) → with P_fa4a's confidence-blend, herbivores can't
  home → migration pressure/starvation ("공유지 비극"). The plant regrows via the ordinary flora Step. No food
  source in reach ⇒ no-op (hunger keeps rising → PD3 starvation). Width (cover/shade) is untouched.
  **A plant cropped to `Length ≤ 0` is NOT a food source** and the lookup skips it. Without that skip the
  bare plant stays the animal's *nearest* emitter forever, so every later Graze re-picks it and recovers
  nothing — the animal starves standing on untouched plants a few units away. Measured on the live meadow
  before the skip: the nearest food plant was an exhausted one for **95–99% of samples across
  rabbit/deer/goat/fish**, with a plant that HAD biomass inside the same reach ~80% of the time.
  Regression: `TestGrazePassesOverExhaustedPlant`.
- **Regen (FC7):** slow Vital regen toward `VitalCap` applied by world in the animal commit (balance rate).
- **Age advance (PD4-ii/P_fa4c):** `commitAnimalOwnState` advances `a.Age += FaunaDT` each tick, so age = Σ
  DT since birth (newborns start 0). The fauna Step reads start-of-tick Age for the §6 `maturity` operand
  (world increments AFTER the fauna phase). Internal state (not render-visible; not persisted, like Vital).
  §7 aging (age→senescence) is a later hook; P_fa4c uses Age only as the reproduction maturity clock.
- **Starvation death (PD3, P_fa4b):** fauna's `nextVital` bleeds `Vital` while a coupled drive (hunger) is
  saturated (fauna `SPEC-combat.md`). The world commits that lowered `Vital` (`commitAnimalVital`); the
  **existing** `applyAnimalCombat` `Vital ≤ 0` check then removes the animal. That check now labels the
  cause **`causeStarvation`** (not `causePredation`): a combat kill is already labelled + removed inside
  `applyAnimalAttack` (line-of-death), so any animal reaching the own-state `Vital ≤ 0` check died of
  non-combat vital depletion — starvation now, thermal-freeze later (same path, only the drive differs).
  Predators and herbivores share the mechanism (D4), so a predator that cannot find prey starves too — the
  cap on predator numbers is now mortality, not the respawn thermostat.

## Cover-hiding apply (M3 — fauna-realism; rationale: `docs/plans/fauna.md` 클러스터 10, gate RESOLVED 2026-07-02)

The world is the **sole writer of `Animal.HiddenUntil`** (fauna only reads it — combatTarget skip + crouch,
fauna SPEC M3). It reuses the graze machinery verbatim (`applyAnimalGraze`/`nearestForageFloraID`/`kindEmitsFood`
/`animalFeedContext`/`depositAnimalScent`):

- **Entry roll (`applyAnimalHiding`, in the animal apply path):** for a **non-predator** animal whose
  committed action's steer channel is `flee:predator` (`TagFleePred`) AND `EngagedWith == ""` AND
  `HiddenUntil < w.tick` (not already hidden) AND `nearCoverFlora(a)` → roll the seeded fauna fork
  (`w.envFork(w.tick,"fauna-hide")`, drawn in apply): if `r.Float64() < w.faunaRules.HideChance(a.Species,
  animalFeedContext{animal:a})` then `a.HiddenUntil = w.tick + HideDurationTicks`. `HideChance`≡0 for a
  species without a `hide_chance` §6 ⇒ never hides (OFF-neutral). This mirrors `applyAnimalGraze` (§6 eval +
  proximity gate) — the roll is the only new RNG draw, from the disjoint `"fauna-hide"` fork (D12).
- **`nearCoverFlora(a)`** — the `nearestForageFloraID` twin: a flora object whose kind carries the **`cover`**
  tag within `HideCoverFactor * ScentCellSize` of `a.Pos` (add `kindIsCover(kind)` beside `kindEmitsFood`,
  matching the `cover` tag in `w.scentEmitters`… — NB `cover` is NOT a scent channel; extract it from the
  content kind→tags map the same way, or a dedicated `coverKinds` set built at install from `objects.yaml`
  tags). Iterates `w.objectIDs` in sorted order (D12).
- **Scent suppression (M3-c):** in `depositAnimalScent`, when depositing the **prey** channel
  (`ChanPrey`, i.e. `!predatorCadence` non-predator path) SKIP the deposit if `a.HiddenUntil >= w.tick` —
  a hidden prey emits no prey scent, so a scent-homing predator loses the trail (invisible to smell as well
  as to the ranged `combatTarget`). Predator channel unaffected (predators do not hide).
- **Engage clear:** when `applyAnimalAttack` sets `EngagedWith` on a target (or attacker), clear that
  animal's `HiddenUntil = 0` (a flushed/engaged animal is no longer hidden). Keeps the invariant "engaged ⇒
  not hidden".
- **Snapshot wiring:** `buildFaunaSnapshot` passes `HiddenFlushFactor` through `Snapshot.Combat`
  (`CombatParams`) so fauna's `combatTarget`/bolt can compute the flush radius; `HideDurationTicks` /
  `HideCoverFactor` are consumed only world-side.
- **Config/content (D10):** `content/world.yaml cadence` gains `hide_duration` (int, → `HideDurationTicks`),
  `hidden_flush_factor` (float, → `HiddenFlushFactor`), `hide_cover_factor` (float, → `HideCoverFactor`);
  `platform/config` maps them into `EnvConfig.FaunaCombat` (`CombatParams`) + `world.schema.json`. Prey
  species in `content/objects.yaml fauna:` gain an optional `hide_chance` §6 (compiled + cross-checked like
  `graze`/`attack_power`); the `cover` tag is already on `oak`/`berry_shrub`. Absent keys ⇒ zero/no-hide ⇒
  neutral.
- **Neutrality (the M3 lever):** with no `hide_chance` authored and/or no `cover` flora placed, `HiddenUntil`
  is never set, no deposit is skipped, and `Tick()` stays byte-identical — the fixture is the deliberate
  activation site (like the fauna/climate/flora levers).

## Cover speed resistance (M4-b — fauna-realism; rationale: `docs/plans/fauna.md` 클러스터 10, gate RESOLVED 2026-07-02)

Moving through `cover` flora (forest/thicket) **slows an animal AND makes its speed vary** — a continuous
drag, **no stumble/stop/fall**. World-side (world owns flora; parallels M3 and `depositFloraScent`); fauna
only exposes the per-species affinity via `Rules.CoverCost`. This is a movement SCALE applied when world
commits the animal's move, NOT a §6 speed change (fauna's speed is unchanged).

- **Where (in `commitAnimalOwnState`, movement commit):** after
  `intended, heading := w.reflectAtBounds(intent.NextPos, intent.NextHeading)` (FM13 — clamp into bounds +
  reflect the heading off any wall crossed), scale the DISPLACEMENT by the cover resistance at the destination:
  ```
  res  := w.coverResistance(a.Species, intended)          // ≥ 1
  pos  := a.Pos.Add(intended.Sub(a.Pos).Scale(1.0 / res)) // moves LESS through denser cover; res=1 ⇒ pos=intended
  w.spatial.Move(a.ID, pos); a.Pos = pos                  // pos is between a.Pos and intended ⇒ still in bounds
  a.Heading = heading                                     // reflected inward at a wall, else == intent.NextHeading
  ```
  A crouching (M3 hidden) or engaged animal has `intended == a.Pos` (zero displacement) ⇒ resistance is a
  no-op, so M4-b never fights M3/combat.
- **Boundary reflection (`reflectAtBounds`, FM13, `docs/plans/fauna.md §4.4`):** clamps `p` into
  `[Min,Max]` AND flips the outward `cos/sin(heading)` component on each wall crossed, so an animal that
  steers off-map **bounces back inside** instead of pinning against the edge and sliding (the "everyone
  rubs the wall" bug). Fires only when a wall is actually crossed; an interior commit returns
  `(p, heading)` unchanged ⇒ byte-identical to the old `clampToBounds` path (existing goldens hold).
  `clampToBounds` is retained for off-map respawn placement (position-only, no heading). Pure trig,
  deterministic (no RNG/state). Respawn stays near-live (FM15, unchanged) but now **rejection-samples the
  offset for F18 terrain passability** (`respawnPos` → `canRespawnAt` via `faunaRules.TerrainCost`): a
  respawn must land on a cell its species can occupy (fish → water), else it retries up to
  `maxRespawnPlacementTries` and falls back to the passable base. The **first** candidate reuses the same
  fork draws as the old bounds-only placement, so already-passable respawns are byte-identical (goldens
  hold); only an impassable first candidate spends extra draws. The extinction anchor is the first
  actually-placed member (a real passable position), not the fixture-animals centroid.
- **`coverResistance(species, p)`** (nil-safe; returns 1 when nothing applies):
  ```
  cc := w.faunaRules.CoverCost(species)                   // per-species content affinity; 0 ⇒ unaffected
  if cc <= 0 || w.floraState == nil { return 1 }
  density := 0.0
  for _, pl := range w.floraState.Plants() {              // sorted-plant iteration (depositFloraScent twin)
      if !w.kindIsCover(core.Tag(pl.Species)) { continue }   // reuse the M3 cover set
      radius := w.envCfg.FaunaCombat.CoverRadiusFactor * pl.Width
      if radius <= 0 { continue }
      if d := p.Distance(pl.Pos); d < radius { density += 1 - d/radius }  // linear falloff, peaky (→ variation)
  }
  return 1 + density*cc
  ```
  `density` PEAKS near a plant and dips between plants, so as the animal moves each tick it samples a
  DIFFERENT density ⇒ resistance oscillates ⇒ **speed varies (변주)** — deterministically, no RNG (D12). Bigger
  species (higher `cover_cost`) bog down more; small prey (rabbit 0.3) slip through — the prey-into-thicket,
  predator-bogs-down asymmetry.
- **Config/content (D10):** per-species `cover_cost` (scalar ≥0) on the `content/objects.yaml fauna:` block →
  `SpeciesRule.CoverCost` (parsed like `terrain_cost`, exposed by `Rules.CoverCost`); `content/world.yaml
  cadence.cover_radius_factor` (float, × plant `Width` → cover radius) → `EnvConfig.FaunaCombat.CoverRadiusFactor`
  (`CombatParams`) + `world.schema.json`. Placeholders: rabbit 0.3 / deer 0.7 / goat 0.6 / wolf 1.2 / bear 2.0;
  predators+fish otherwise; `cover_radius_factor` ~3.0.
- **Neutrality:** no `cover_cost` authored (⇒ `CoverCost`≡0) and/or no `cover` flora placed ⇒ `res`≡1 ⇒ movement
  byte-identical to today. The starter/arena fixtures (which DO place cover) are the activation site; their
  smoke/arena harnesses have no stored golden (determinism + behavioural asserts only), so no golden re-baseline.

## Ambush concealment (M5-b — fauna-realism; rationale: `docs/plans/fauna.md` 클러스터 10, gate RESOLVED 2026-07-02)

A predator standing in dense `cover` flora is **harder for prey to SEE** (not to smell) — it closes the
distance before the prey bolts, so **ambush emerges** from cover + positioning (no per-species ambush FSM,
D2/D3). World-side (world owns flora; reuses the M4-b cover density + the M3 "world writes an `Animal`
field, fauna reads it" seam); fauna only READS the per-animal concealment in its sight query.

- **`coverDensity(p)` (extract from `coverResistance`):** the species-independent peaky cover-flora overlap
  sum (`Σ 1 − d/radius` over cover plants within `CoverRadiusFactor×Width`). `coverResistance` becomes
  `1 + coverDensity(p)×CoverCost` — M4-b behaviour unchanged, just factored.
- **Where (in `commitAnimalOwnState`, after the move commit):**
  ```
  a.Concealment = w.coverDensity(pos) * w.envCfg.FaunaCombat.ConcealFactor  // species-independent, ≥ 0
  ```
  Post-move position ⇒ prey read it NEXT tick (F33 latency, twin of scent deposit). `Animal.Concealment` is
  a **derived transient** — recomputed every tick from position+flora, **NOT persisted** (a restore
  self-heals within one tick; it is not authoritative decision state like `HiddenUntil`).
- **fauna reads it (contract for `engine/fauna` `sightQuery`):** a predator candidate is skipped when
  `dist > sightRadius / (1 + other.Concealment)` — the same saturating form as the M4-b drag. `Concealment=0`
  ⇒ effRadius = sightRadius ⇒ the query is byte-unchanged. **Only the sight (Flee) channel narrows**; the
  scent (Wary early-warning) channel and `combatTarget` (hunt / M3 hidden) are untouched — prey still SMELLS
  the predator, it just SEES it late.
- **Config/content (D10):** `content/world.yaml cadence.conceal_factor` (float ≥0, × cover density →
  concealment) → `EnvConfig.FaunaCombat.ConcealFactor` (`CombatParams`) + `world.schema.json`. M5-a is pure
  content senses tuning (prey `senses.sight_radius`/`fov_arc`) — no new field.
- **Neutrality:** `conceal_factor=0` and/or no `cover` flora ⇒ `Concealment≡0` ⇒ effRadius≡SightRadius ⇒
  sight byte-identical. Deterministic, no RNG (D12). No new Snapshot/Intent seam (mirrors M3 `HiddenUntil`).

## Hazard field wiring (P_move1 continuous hazard avoidance; rationale: `docs/plans/fauna.md` §4 FM2/FM5, gate RESOLVED 2026-07-09 · `docs/decisions/fauna-gates.md`)

Animals veer away from dangerous terrain (deep water / cliff) BEFORE dead-stopping at it — a **continuous
cost, not a block** (a strong flee/drive out-pulls it and the animal crosses, FM5). World owns the terrain,
so world builds **ONE shared static danger `field.Field`** and injects it via the fauna-declared
`HazardSampler` interface (dependency inversion — fauna imports neither `space/field` nor `space/navmap`).
Per-species differentiation is the scalar `e` (`hazard_avoidance`, §6), so one shared field suffices in
P_move1; per-species fields (FM2's full 종별) are deferred (fish-inversion likewise, currently `e=0`).

- **`cellDanger(nav, cell) → [0,1]`** (`engine/world/hazard.go`): impassable cell → `1.0` (drown/fall);
  passable-but-rough → `clamp((BaseCost−1)/hazardCostSpread, 0, 1)` (river ford / mountain stays MILDLY
  dangerous ⇒ crossable, preserving M4-a river-as-refuge); normal ground → ~0. `BaseCost`/`Passable` is
  the **runtime proxy** for the intended §5 depth/slope danger — `w.terrainAttrs` (depth/slope) is not yet
  fed at runtime, a disclosed latent gap; refining the source to true §5 attrs is a separate follow-up.
- **`buildHazardField() → *field.Field`** (nil when no navmap or no dangerous terrain ⇒ blend OFF): enumerate
  navmap cells over `[Min−pad, Max+pad]` at a sample step ≤ hex inradius (`√3/2·NavmapCellSize`, mirroring
  the climate→navmap bridge), dedup via a `seen` set (dedup ONLY — never iterated for order, so it cannot
  affect output; `field.Build` re-sorts sources by `(R,Q)`), collect cells with `danger ≥ hazardDangerFloor`
  as `field.Source{Cell, Weight:danger}`, and `field.Build(w.nav, sources, hazardDecayPerUnit, w.nav.Passable)`.
  Constants (`hazard.go`): `hazardDangerFloor 0.2`, `hazardCostSpread 6.0`, `hazardImpassable 1.0`,
  `hazardDecayPerUnit 0.03` (reach = danger/decay; untuned balance).
- **Lazy build + cache (`buildFaunaSnapshot`):** built once on first fauna snapshot and cached
  (`w.hazardField`/`w.hazardFieldBuilt`) — static terrain ⇒ build once, rebuilt only on a terrain change.
  **Typed-nil guard:** the concrete `*field.Field` is boxed into the `fauna.HazardSampler` interface ONLY
  inside `if w.hazardField != nil` — a nil field yields `Snapshot.HazardField == nil` (a true nil interface),
  never a non-nil interface wrapping a nil pointer, so hazard-OFF is byte-identical to pre-P_move1.
- **Neutrality:** no dangerous terrain (all-passable, low-cost) ⇒ no sources ⇒ nil field ⇒ no bend; and every
  species with `hazard_avoidance` unset/≤0 ⇒ fauna skips the blend regardless. Deterministic, no RNG (D12).

## Water-attraction field wiring (FM4 thirst; rationale: `docs/plans/fauna.md` §4.1 FM4/FM4-src, gates RESOLVED 2026-07-09/-10)

The **attraction** twin of the hazard field: a thirsty animal steers TOWARD drinkable water. Mirrors the
hazard wiring (shared static `*field.Field`, lazy build + cache, typed-nil guard) with two differences —
the sources are **drinkable-water** cells and fauna follows the `Gradient` (toward the source) instead of
`Repulsion`.

- **Drinkable-terrain source set (config, FM4-src):** `platform/config` derives `EnvConfig.DrinkableTerrains`
  (`map[TerrainID]bool`) from `terrain.yaml` attrs at load — `salinity ≤ water.salinity_max ∧ moisture ≥
  water.moisture_min` (river/lake in, sea/soil out). Data-defined (no Go terrain-name switch); NO runtime
  `terrainAttrs` (attrs are per-TYPE uniform ⇒ load-time derivation suffices). `moisture_min ≤ 0` / no match
  ⇒ nil set ⇒ no water field.
- **`buildWaterField() → *field.Field`** (`engine/world/water.go`; nil when no navmap, empty
  `DrinkableTerrains`, `WaterFieldDecay ≤ 0`, or no drinkable cells): enumerate navmap cells (same padded
  sample step as `buildHazardField`), collect drinkable cells as equal-weight `field.Source{Cell,
  Weight:waterSourceWeight=1.0}`, and `field.Build(w.nav, sources, w.envCfg.WaterFieldDecay, w.nav.Passable)`
  (attraction flows through the passable graph). `field.Build` re-sorts sources (D12).
- **Lazy build + cache + inject (`buildFaunaSnapshot`):** `w.waterField`/`w.waterFieldBuilt`, boxed into
  `fauna.WaterSampler` ONLY inside `if w.waterField != nil` (same typed-nil guard) → `Snapshot.WaterField`.
- **Thirst recovery (`applyAnimalDrink`, mirrors `applyAnimalGraze`):** hooked on the `seek:water` steer tag
  in the action-effect apply — a `thirst`-tracking animal ON a drinkable cell (`DrinkableTerrains[TerrainAt
  (CellOf(Pos))]`) recovers `thirst` by the species' §6 `Drink` factor. No-op off water / no `thirst` drive /
  no `drink` program.
- **Neutrality:** no species authoring `thirst`+`seek:water` ⇒ the field is built but never queried and the
  recovery never fires ⇒ **byte-identical** to pre-FM4 (world/ecosim goldens hold). Deterministic, no RNG (D12).

## Diurnal sleep injection & torpor recovery (P_sleep1; rationale: `docs/plans/fauna.md` §4 FM11/FM11b/FM12 + SS1–SS3, gate RESOLVED 2026-07-10 · `docs/decisions/fauna-gates.md` cluster 11)

Animals sleep at night and wake to a near predator (user 2026-07-10: "저녁에 자는 것 + 주변 이벤트에 기상"). The
world owns the clock, so world injects a `daylight` cue; fauna makes Sleep a §6 action that wins at night
(diurnal/nocturnal emerges from the `(1−daylight)`/`daylight` sign, D2/D10 — no per-species flag). Sleep is
the shared `Sleep` action tagged `state:sleep` (→ fauna `TagSleep`: no-loco + high wake threshold); NO new
`Animal` field (SS1: sleeping ⇔ `CurrentAction == Sleep`).

- **Daylight injection (`buildFaunaEnvSamples`):** compute ONCE per tick (world-uniform)
  `daylight = ½(1 − cos(2π · clock.DayFraction(tick)))` ∈ [0,1] — 1 at solar-noon, 0 at midnight; smooth —
  and set it on every animal's `EnvSample.Daylight`. Clock-derived ⇒ deterministic (D12); no climate
  dependency (a light cue, not weather — distinct from the FA5 diurnal temperature). `DayFraction` is the
  new `worldtime.Clock` accessor (diurnal twin of `YearFraction`).
- **Torpor deep fatigue recovery (`applyAnimalFatigue`, SS2):** a `state:sleep` action recovers fatigue at
  `CombatParams.SleepFatigueRecoverPerTick` (> ordinary `FatigueRecoverPerTick`). The `state:sleep` case is
  guarded on a POSITIVE rate AND checked BEFORE the `effort:*` cases (Sleep is also `effort:none`), so torpor
  wins the deeper rate when set; if the rate is ≤0 it falls through to the `effort:none` case so a sleeper
  still recovers at the ordinary rest rate (never LESS than a rester). Tag-driven via
  `actionHasTag(action, fauna.TagSleep)` (D4) — no hardcoded action id.
- **Wake (SS3/FM12) is fauna-side:** the `Cadence.SleepWakeScentThreshold` gate lives in `fauna.Step` Step 0
  (a sleeper wakes only to predator scent ≥ threshold); world only injects the threshold from config. Once
  awake, fear out-scores Sleep and the animal flees — emergent (D2), no wake FSM.
- **Config/content (D10):** `content/world.yaml cadence.{sleep_wake_scent_threshold, sleep_fatigue_recover_per_tick}`
  → `Cadence.SleepWakeScentThreshold` / `CombatParams.SleepFatigueRecoverPerTick` (+ `world.schema.json`).
  The `Sleep` action gains the `state:sleep` tag (`content/actions.yaml`); per-species `Sleep` §6 utility on
  the `fauna:` block (`content/objects.yaml`; placeholders — deer/rabbit/goat/bear diurnal, wolf shown
  invertible to nocturnal). `daylight` is added to `fauna.AttrOperands()` (load-time §6 cross-check).
- **Neutrality:** a species with no `Sleep` candidate never sleeps (unchanged); `sleep_wake_scent_threshold`
  0 ⇒ a sleeper wakes to any scent (as before); `sleep_fatigue_recover_per_tick` ≤0 ⇒ the guarded torpor case
  is skipped and a sleeper falls through to the ordinary `fatigue_recover_per_tick` (rest-equivalent, never
  less). Deterministic, no RNG.

## Notes
- **fauna.Step plans, world applies (F41).** The split — fauna emits intents in the plan phase, the
  world applies them in the SAME combined sorted stream as agents — is what keeps "world = sole
  mutator" and one deterministic apply order across the whole population (agents + animals).
- **Scent after apply, commit at tick end.** Depositing on post-move positions + committing at the end
  gives the F33 next-tick latency and keeps the predator channel fresh for the F45 wake without any
  O(N²) scan (`IntensityAt` is O(1) per animal).
- **Two motivation machines stay apart (D5).** Animals run the drive/utility loop (no Value/ToM); the
  world never mixes animal drives into the agent value stack. The only cross-channel is F8 (predator →
  agent Safety via the perception hostile-tag channel) — unchanged here.
- **Legacy prey coexists (W7).** Until P_fa3 activation, the `prey` timer-respawn object and the new
  `fauna.Animal` controller can both exist; fauna-OFF neutrality guarantees no interference. Migration
  (legacy prey → a placed fauna species) is the deliberate P_fa3 re-baseline (flora `berry_bush`
  parity).
- Reference paths: `docs/plans/world-integration.md` (WI-P2, W5/W6/W7), `content/world.yaml` (scent/motion/
  cadence), `backend/engine/fauna/SPEC.md` (the controller + Snapshot/Intent + apply contract),
  `backend/engine/space/scent/SPEC.md` (deposit/spread/commit/read), `SPEC-world-env.md` (WI-P1
  navmap/climate), `SPEC-tick.md` (the four-phase loop + conflict model this extends),
  `backend/engine/space/spatial/SPEC.md` (shared id space), `docs/core/data-contracts.md` (animals[]/scent).
