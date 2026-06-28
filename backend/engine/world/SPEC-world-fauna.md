# SPEC — `engine/world` · Fauna & Scent Wiring (animals · combined apply · scent driving)

> Status: `DRAFT`
> Sub-spec of: `SPEC.md`  ·  Owner agent: `<filled by implementer>`
> Scope = **WI-P2** (`docs/world-integration.md` §2). Builds on `SPEC-world-env.md` (WI-P1 navmap/climate).

## Scope

This sub-spec wires the **reduced-reactive animal controller** (`engine/fauna`) and the **shared
scent field** (`engine/space/scent`) into the world tick loop. It adds: the **live animal set**
(world-owned, `fauna.Animal`), `fauna.Step` in the **plan phase** (read-only, alongside agent
planning), the **combined agent+animal apply** in ONE sorted-ObjectID stream (F41 / W5), the
**scent deposit/spread/commit** driving in the Phase 4-ENV sub-phase (the WI-P1 step 4 placeholder),
and the world-side **adapters** fauna declares — `EnvSample` (climate `CellAt`/`Wind` → animal
Context) and `TerrainSampler` (navmap → `{Passable, Cost}`). The world stays the **sole mutator**
of animal state (move/heading/drives/stamina/vital/death, D12).

It depends on WI-P1: animals read the **navmap** (TerrainSampler) and **climate** (EnvSample) that
`InstallEnv` installs; the scent grid's `Wind` comes from `climateState.Wind()`. **Carcass +
`Butcher` + full species activation/re-baseline are P_fa3** (deferred, Out of Scope) — WI-P2 is the
core controller wiring; reproduction stays the legacy timer (W7).

Numerics (scent cell, `scent_spread`, motion DT, fauna cadence) come from `content/world.yaml`
(`docs/world-integration.md` §0-9); none hardcoded (D10).

---

## Ownership & installation

### New owned state (added to `World`; empty when fauna not installed)
- `animals map[core.ObjectID]*fauna.Animal` + a precomputed **sorted-ObjectID slice** (the one
  iteration order, D12) — the live animal set. world is the sole mutator (apply phase).
- `scent *scent.Grid` — the shared scent field (world-owned shared index, F36). `cellSize` from
  `content/world.yaml grids.scent_cell_size`. Fed by world from `scent:<channel>`-tagged emitters
  (flora/fauna/decay), read by fauna (+ later perception). DERIVED state — rebuilt from emitter
  positions on resume, not separately serialized (scent SPEC).
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
func (w *World) InstallFauna(cfg EnvConfig, faunaRules *fauna.Rules, animals []fauna.Animal)
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
snap.Terrain = terrainSampler{ nav: w.nav, types: w.terrainTypes }   // adapter, below
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

### `TerrainSampler` adapter (navmap → {Passable, Cost})
```
terrainSampler.Passable(p) = !w.nav.FootprintBlocked(w.nav.CellOf(p))   // TRUE blockers only (walls/footprints)
terrainSampler.Cost(p)     = w.terrainTypes[ w.nav.TerrainAt(w.nav.CellOf(p)) ].BaseCost   // ≥1; high for water
```
Per fauna R2/F35 **water is traversable at high Cost, NOT `!Passable`** — only footprints/walls are
`!Passable`. navmap's existing `Passable(cell)` conflates terrain-impassable (deep water) with
footprint-block, so the adapter needs a **footprint-only** passability + a **per-cell base cost**
(see Open Questions — a small navmap accessor or the world's `terrainTypes` join). D11 index read
(continuous `p` → cell), never a snap.

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
  2. **Move**: `spatial.Move(a.ID, intent.NextPos)`; set `a.Pos = NextPos`, `a.Heading = NextHeading`.
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
- [ ] **TerrainSampler semantics (R2/F35)** — `Passable(p)` is false ONLY for footprint-blocked cells
  (walls), TRUE for deep-water terrain (traversable); `Cost(p)` is the terrain's base cost (≥1, high
  for water). A water cell is `Passable:true` with high `Cost`; a wall footprint is `Passable:false`.
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
  + `docs/data-contracts.md` (WI-P4, W8). scent is derived (not serialized); animals[] is WI-P4.
- **Parsing `content/objects.yaml fauna:` → `faunaRules`, the `ReadsAttrs` cross-check, the scent-cell
  floor check, and `content/world.yaml fauna/scent` keys + schema** → `platform/config` (WI-P0).
- **`Mine` terrain-driven extraction** (resources R1) → WI-P3.

---

## Open Questions
> `docs/world-integration.md` W1-W9 RESOLVED (human 2026-06-27). Two **plumbing seams** surfaced
> writing this SPEC — neither a mechanism decision (no new behaviour), both small accessor/contract
> shapes to settle before implementation:

- **navmap footprint-only passability + per-cell base cost (TerrainSampler seam).** The fauna
  `TerrainSampler` needs (1) `Passable` = footprint-block ONLY (water traversable), and (2) `Cost` =
  the cell's terrain base cost. navmap currently exposes `Passable(cell)` (terrain-impassable AND
  footprint conflated) and `StepCost(from,to)` (not a single-cell base cost). Options: **(a)** add
  `navmap.FootprintBlocked(cell) bool` + `navmap.BaseCost(cell) float64` accessors (small, pure);
  **(b)** the world joins `nav.TerrainAt(cell)` → its `terrainTypes[id].BaseCost` (world holds the
  types table it gave `navmap.New`) and adds only `FootprintBlocked`. **rec: (b)** — keeps navmap a
  cost field, world owns the TerrainID→attrs/cost join (parity with the WI-P1 TerrainAttrs seam); only
  one tiny `FootprintBlocked` accessor is new. **Flag to navmap owner before WI-P2 impl.**
- **Animal id allocation convention (shared id space, W5).** Animals + agents share one string id
  space for the combined apply sort. Options: **(a)** a distinct animal id prefix (`an:<n>`) minted by
  `w.allocObjectID`; **(b)** a single global counter with no prefix (rely on uniqueness). **rec: (a)**
  — prefix keeps logs/why-trace legible and guarantees disjointness from `AgentID`. Document on
  `Spawn`/`allocObjectID`. Non-blocking; finalize at impl.

---

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
- Reference paths: `docs/world-integration.md` (WI-P2, W5/W6/W7), `content/world.yaml` (scent/motion/
  cadence), `backend/engine/fauna/SPEC.md` (the controller + Snapshot/Intent + apply contract),
  `backend/engine/space/scent/SPEC.md` (deposit/spread/commit/read), `SPEC-world-env.md` (WI-P1
  navmap/climate), `SPEC-tick.md` (the four-phase loop + conflict model this extends),
  `backend/engine/space/spatial/SPEC.md` (shared id space), `docs/data-contracts.md` (animals[]/scent).
