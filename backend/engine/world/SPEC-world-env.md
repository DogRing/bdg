# SPEC — `engine/world` · Env Orchestration (climate · flora · decay · navmap terrain)

> Status: `DRAFT`
> Sub-spec of: `SPEC.md`  ·  Owner agent: `<filled by implementer>`
> Scope = **WI-P1** (`docs/plans/world-integration.md` §2). Sibling: `SPEC-world-fauna.md` (WI-P2 — animals + scent).

## Scope

This sub-spec wires the **pure env transforms** (`engine/env/{climate,flora,decay}`) and the
**terrain cost field** (`engine/space/navmap`) into the world's deterministic tick loop, as an
**env sub-phase appended to Phase 4 (apply)** (`docs/plans/world-integration.md` W4 — apply 후 직렬
env-phase). The world stays the **sole mutator** (D12): each env module returns next-state +
deltas; the world applies them serially in fixed module + sorted-cell/ObjectID order. It owns the
**climate→navmap `SetTerrain` bridge** (RESOLVED #3/#6 — climate emits `[]Transition`, world maps
`GridCell`→`[]navmap.Cell` and writes), the **env sampling** that fills `flora.SiteInput` /
decay env from navmap+climate at each entity's position, and the **flora-shade → perception**
projection (`WorldSnapshot.ShadeOccluders`).

**Animals, `fauna.Step`, the combined agent+animal apply (F41), and the scent grid
(deposit/spread/commit) are NOT here** → `SPEC-world-fauna.md` (WI-P2). This piece reads no scent
and emits no animal intents; it is the env-state foundation WI-P2 builds on (fauna samples the
navmap + climate this installs).

All numeric geometry/cadence comes from `content/world.yaml` (`docs/plans/world-integration.md` §0-9);
the world hardcodes none (D10).

---

## Env subsystem ownership & installation

The env subsystems are **opt-in installed** state. **Until `InstallEnv` is called, env is OFF**:
the env sub-phase (below) is a no-op, `WorldSnapshot.ShadeOccluders` returns empty, and the world
behaves byte-identically to today — every existing scenario/golden holds (this is the union of the
"outcome-neutral until activated" invariants already in `climate`/`flora`/`decay`/`navmap` SPECs).

### New owned state (added to `World`, all nil/empty when env OFF)
- `nav *navmap.NavMap` — the terrain cost field (terrain layout + base cost + footprints + wear +
  dynamic terrain). The **terrain authority** for env + (WI-P2) fauna `TerrainSampler` + flora
  `SiteInput.Terrain`. Built from the fixture terrain layout (world-gen / scenario), `docs/plans/world-gen.md`.
- `climateState *climate.State` + `climateRules *climate.Rules` — coarse climate grid + rain + wind.
- `floraState *flora.State` + `floraRules *flora.Rules` — the live plant set.
- `decayState` + decay rules — the perishable-LOT set (`engine/env/decay`; shape per
  `docs/core/data-contracts.md §8` + `backend/engine/env/decay/SPEC.md`).
- `envCfg EnvConfig` — the world.yaml-derived geometry + cadence (below).
- The world's `objects[]` set now ALSO holds flora plants as object records (RESOLVED flora 1i:
  flora joins `objects[]`; the `flora.State` carries the morphology, the object record carries
  `{id, kind, pos}` for the spatial hash + perception + render).

### Construction

```go
// EnvConfig is the world.yaml-derived env geometry + cadence (platform/config parses content/world.yaml
// and builds this; NEVER hardcoded, D10). Bounds/grids feed the module Configs (climate.Config,
// navmap.Config) at build time; cadence is read by the env sub-phase. (Scent + fauna cadence/motion
// keys extend this in SPEC-world-fauna.md.)
type EnvConfig struct {
    Min, Max         core.Vec2 // world.yaml bounds (fixture may override) — the finite rect the grids span
    NavmapCellSize   float64   // world.yaml grids.navmap_cell_size (= balance world.spatial_hash_cell)
    ClimateGridCols  int       // world.yaml grids.climate_grid_cols
    ClimateGridRows  int       // world.yaml grids.climate_grid_rows
    ClimateStep      int       // world.yaml cadence.climate_step (ticks; e.g. 60)
    FloraStep        int       // world.yaml cadence.flora_step
    DecayStep        int       // world.yaml cadence.decay_step
}

// InstallEnv installs the env subsystems built by platform/config from the fixture + content. The
// navmap + climate are built from the SAME per-run terrain layout so their grids agree at t=0
// (climate.New(terrainAt) uses the layout navmap.New baked, climate SPEC §New). flora/decay start
// from the fixture's placed plants / lots (may be empty → env runs but produces nothing = neutral).
// Idempotent install is NOT required (call once at world build). When NOT called, env stays OFF.
func (w *World) InstallEnv(
    cfg          EnvConfig,
    nav          *navmap.NavMap,
    climateState *climate.State, climateRules *climate.Rules,
    floraState   *flora.State,   floraRules   *flora.Rules,
    decayState   *decay.State,   decayRules   *decay.Rules,
)
```

> `platform/config` builds `navmap.NavMap` (from fixture terrain + `content/terrain.yaml` types),
> `climate.State` (from the same `terrainAt` + `content/world.yaml` Init*), the compiled `Rules`
> (climate transition table, flora `flora:` §6, decay), and the initial `flora.State`/`decay.State`
> from the fixture. The world only RECEIVES and DRIVES them (it never parses content, architecture §1).

---

## The env sub-phase (Phase 4 extension — serial, after intent apply)

`Tick()` Phase 4 (APPLY) gains a final **env sub-phase**, run AFTER the agent (WI-P2: + animal)
intents are applied and BEFORE the tick counter advances / `TickDone` is emitted. When env is OFF
it is skipped entirely. The sub-phase is **fully serial** (world = sole mutator) and runs the
modules in this **fixed order** (D12):

```
Phase 4 (existing) — apply agent (+ animal, WI-P2) intents in sorted-ObjectID order
Phase 4-ENV (NEW, env installed only):
  1. CLIMATE  (if w.tick % envCfg.ClimateStep == 0):
       f := Forcing{ HourOfDay: clock.HourOfDay(tick), AbsHour: clock.AbsHour(tick),
                     YearFraction: clock.YearFraction(tick) }          // worldtime-derived (D12)
       next, transitions := climate.Step(climateState, f, climateRules, envFork(tick,"climate"))
       climateState = next
       for each t in transitions (already sorted GridCell order):       // climate→navmap BRIDGE
           cells := climateCellToNavCells(t.Cell)                       // world-owned mapping (below)
           nav.SetTerrain(cells, t.To)                                  // apply-phase write (navmap SPEC)
  2. FLORA    (if w.tick % envCfg.FloraStep == 0):
       inputs := buildFloraSiteInputs()                                 // sample navmap+climate (below)
       nextF, deltas := flora.Step(floraState, inputs, floraRules, w.allocObjectID, envFork(tick,"flora"))
       floraState = nextF
       applyFloraDeltas(deltas)                                         // spawn→objects[]+spatial,
                                                                        // die→remove, grow→update morphology
  3. DECAY    (if w.tick % envCfg.DecayStep == 0):
       env := buildDecayEnv()                                           // climate temperature/moisture per lot
       // decay.Step takes elapsedTicks (the cadence interval) as its 3rd arg — pass DecayStep
       // (Step runs every DecayStep ticks, so elapsedTicks == envCfg.DecayStep). See engine/env/decay SPEC.
       nextD, dd := decay.Step(decayState, env, int64(envCfg.DecayStep), decayRules, envFork(tick,"decay"))
       decayState = nextD
       applyDecayDeltas(dd)                                             // aged/transitioned/transformed/gone
  4. (SCENT deposit/spread/commit → SPEC-world-fauna.md WI-P2)
```

- **Cadence is `tick % N`, never wall-clock** (D12). climate/flora/decay each on their own N from
  `EnvConfig`; an off-N tick skips that module (its state is unchanged that tick).
- **Per-env RNG fork** — `envFork(tick, channel)` is a deterministic derivation from the root RNG
  keyed by `(tick, channel-tag)`, **disjoint from the per-agent forks** (SPEC-tick.md `forkRNG`):
  no env draw depends on an agent's draw count and vice-versa. Channels are the fixed strings
  `"climate"`/`"flora"`/`"decay"` (+ WI-P2 `"fauna"`). Same `(seed, tick)` ⇒ identical env forks.
- **`w.allocObjectID`** mints fresh `ObjectID`s for flora `Spawned` plants from the world's own id
  counter, called in flora.Step's sorted parent-then-draw order (flora SPEC `idAlloc` contract) so
  the assignment is reproducible. The world owns the id space; flora never invents global ids.
- **Apply order within a delta set** is the module's sorted contract (climate transitions = sorted
  `GridCell`; flora `Spawned`/`Died`/`Grown` = sorted `ObjectID`; decay slices = sorted `object_id`).

---

## climate → navmap bridge (world-owned `GridCell` → `[]navmap.Cell` mapping, W2)

climate owns a **coarse SQUARE** grid; navmap is **fine flat-top HEX** (one climate cell covers many
hex cells; `docs/plans/hex-grid.md` surgical scope keeps climate square). climate emits a transition per
coarse cell; the world expands it to the navmap HEX cells whose CENTRE falls in that coarse cell's
continuous region and calls `navmap.SetTerrain` (climate never imports navmap, RESOLVED #6):

```
climateCellSize = (envCfg.Max − envCfg.Min) / (ClimateGridCols, ClimateGridRows)   // per axis, SQUARE
region(gc)      = [ Min + gc·climateCellSize , Min + (gc+1)·climateCellSize )       // continuous, per axis
climateCellToNavCells(gc) =
    sample region(gc) finely (step ≤ hex inradius on each axis), collect the DISTINCT navmap.CellOf
    hexes whose centre ∈ region(gc), de-duplicated, returned in R-major-then-Q sorted order (D12 —
    matches navmap.SetTerrain's sorted-slice contract so last-write/accumulation order is fixed).
```

The bounds + both grid geometries come from `EnvConfig` (= `content/world.yaml`), so the mapping is
pure data. `navmap.SetTerrain` with an unknown `TerrainID` panics (content contract, D10) — the
climate transition table's `to`-types are load-validated against `content/terrain.yaml`, so this is
a config bug, never a runtime branch.

---

## Building `flora.SiteInput` (env sampling, per live plant)

Before `flora.Step`, the world builds `inputs[plant.ID]` for **every** live plant (a missing entry
makes flora.Step panic — flora SPEC contract). Each `SiteInput` is sampled at the plant's `Pos`:

```
in.Terrain      = nav.TerrainAt(nav.CellOf(plant.Pos))               // terrain type id
in.TerrainAttrs = <§5 attribute vector for that terrain>             // grainSize/slope/depth/salinity…
in.Moisture     = climateState.CellAt(plant.Pos).Moisture            // [0,1]
in.Temperature  = climateState.CellAt(plant.Pos).Temperature         // °C (CA3)
in.NeighborCount= spatial.NearbyEntities(plant.Pos, propRadius)      // same-species count (flora W: rec same-species)
                    filtered to flora of the species, len
```

`CellAt`/`TerrainAt` are **index reads over continuous space** (D11 — Pos is never snapped). The
terrain-attribute vector source is the navmap terrain type's attribute preset (`content/terrain.yaml`
§5) — flagged below (the exact accessor is a navmap/terrain content seam). `propRadius` is the
species' propagation radius from `floraRules` (flora is queried for it, or world reads the balance
radius). All map reads use sorted plant ids (D12).

## Building decay env (per lot)

Before `decay.Step`, the world samples each lot's environment (decay SPEC / data-contracts §8 — the
env multiplier is world-sampled per lot): `temperature`/`moisture` at the lot's `location` position
via `climateState.CellAt`, plus the storage-rate multiplier (lot location). Lots iterated in sorted
`object_id` order (D12). When climate is OFF the neutral env yields the legacy decay rate.

---

## `WorldSnapshot.ShadeOccluders` (flora shade → perception, flora 1d)

`WorldSnapshot` (the world's `agent.WorldView`/`perception.WorldSnapshot` impl) gains the occluder
accessor perception declared (perception SPEC + flora 1d):

```go
func (s *WorldSnapshot) ShadeOccluders(center core.Vec2, radius float64) []perception.ShadeOccluder
```

It returns the flora shade casters whose footprint may intersect a sight query around `center`: the
world gathers candidate plant ids from the spatial hash (`NearbyEntities`, already ObjectID-sorted),
calls `floraState.ShadeOf(id)` for each, and projects `flora.Shade{Radius,Opacity}` →
`perception.ShadeOccluder` (dependency inversion — perception never imports flora). **When flora is
OFF (or no plants near), it returns empty**, so `Sight` reduces EXACTLY to the pre-shade boolean
behavior (perception flora-off neutrality AC). Result sorted by ascending `ObjectID` (D12).

---

## Determinism (D12) — env additions

- **Pure transforms + sole mutator** — climate/flora/decay are pure (`Step` never mutates `prev`);
  the world applies returned deltas in the serial env sub-phase. No env mutation occurs before
  Phase 4-ENV.
- **Disjoint deterministic forks** — `envFork(tick, channel)` is a pure derivation from the root
  seed + `(tick, channel)`, independent of agent forks and of each other; same `(seed, tick)` ⇒
  identical forks across runs/processes/goroutine orders.
- **Sorted everything** — transitions (GridCell), flora deltas (ObjectID), decay deltas (object_id),
  `climateCellToNavCells` (Y-major/X), SiteInput/decay-env map reads (sorted ids), `ShadeOccluders`
  (ObjectID). No `map` iteration drives logic.
- **Worldtime-derived forcing** — `Forcing.{HourOfDay,AbsHour,YearFraction}` come from
  `worldtime.Clock`, never a wall-clock (CA1 annual phase rides the 120-day `YearFraction` float).
- **Fixed module order** — climate → flora → decay (→ scent, WI-P2) every tick; cadence gates only
  whether a module runs, never the order.

---

## Acceptance Criteria (testable)

- [ ] **Env-OFF neutrality (the WI-P1 lever)** — with `InstallEnv` NOT called, `Tick()` is
  byte-identical to today (every existing world scenario/golden holds); `ShadeOccluders` returns
  empty so `Sight` is unchanged; no env fork is drawn from the root.
- [ ] **Climate cadence + bridge** — with a transition rule that fires, on a `tick % ClimateStep == 0`
  tick the world calls `climate.Step`, swaps state, and for each `Transition` calls
  `navmap.SetTerrain(climateCellToNavCells(cell), to)`; on an off-cadence tick climate state is
  unchanged and no `SetTerrain` is called. The nav cells written equal the k×k cells covering the
  coarse cell's region (table-driven over a known bounds/grid geometry).
- [ ] **`climateCellToNavCells` correctness + D12 sort** — for a coarse cell it returns exactly the
  navmap cells whose centers fall in the cell's continuous region, de-duplicated, Y-major/X sorted;
  bounds-edge cells clamp; two runs identical.
- [ ] **Flora step + apply** — on a `tick % FloraStep == 0` tick the world builds a `SiteInput` for
  every live plant (sampled from navmap+climate at Pos), calls `flora.Step` with `w.allocObjectID`,
  swaps state, and applies `Spawned`→objects[]+spatial / `Died`→remove / `Grown`→morphology update;
  a live plant missing from `inputs` would panic (guard test). Minted ids are unique + reproducible.
- [ ] **Decay step + apply** — on a `tick % DecayStep == 0` tick the world samples env per lot
  (climate temperature/moisture at location), calls `decay.Step`, and applies the
  aged/transitioned/transformed/gone deltas in sorted order; `Decayed` events emitted on transitions
  (data-contracts §4).
- [ ] **Disjoint deterministic env forks** — `envFork(tick,"climate"|"flora"|"decay")` are pairwise
  distinct, independent of `forkRNG` (agent) draws, and reproduce across a second run/process;
  changing agent count does not change the env draws for a given tick.
- [ ] **ShadeOccluders projection + neutrality** — with flora installed and a plant near the query,
  `ShadeOccluders` returns its `{Radius,Opacity}` from `ShadeOf`, ObjectID-sorted; with flora OFF it
  is empty and `Sight` byte-matches the pre-shade golden.
- [ ] **Env-phase order + serial** — within Phase 4-ENV the modules run climate→flora→decay in fixed
  order after the intent apply; no env mutation occurs in Phase 1–3; a determinism golden over N
  ticks (with env installed, rules present) is byte-identical across runs.
- [ ] **No hardcoded geometry/cadence (D10 guard)** — no bounds/cell/grid/cadence literal in logic;
  all flow from `EnvConfig` (= `content/world.yaml`). No `time` import for env logic.

---

## Out of Scope

- **Animals, `fauna.Step`, combined agent+animal apply (F41), `EnvSample`/`TerrainSampler` adapters,
  and the scent grid (deposit/spread/commit)** → `SPEC-world-fauna.md` (WI-P2). This sub-phase emits
  no animal intents and drives no scent (the scent deposit step 4 is added there).
- **Parsing `content/world.yaml`/`content/terrain.yaml`/`content/climate.yaml`, building
  `EnvConfig`/`navmap`/`climate.State`/the compiled `Rules`/the initial flora+decay state, and the
  `content/schema/world.schema.json`** → `platform/config` (WI-P0). The world receives them built.
- **The terrain LAYOUT + attribute presets** (`terrainAt`, the §5 attribute vector per terrain type)
  → `content/terrain.yaml` + world-gen fixture (`docs/plans/world-gen.md`); the world only SAMPLES them via
  navmap. The exact `TerrainAttrs` accessor is a navmap/terrain content seam (Open Questions).
- **°C threshold re-baseline** of flora suitability / decay accel / climate.yaml `when:` to actual °C
  → each module's activation phase (climate SPEC FLAG; `docs/plans/world-integration.md §3`). WI-P1 wires
  the operands; the threshold values are tuned at activation.
- **Serialization of climate/flora/decay/terrain state to Snapshot/Redis/SSE** → `platform/persist`
  + `platform/api` + `docs/core/data-contracts.md` extension (WI-P4, W8). WI-P1 only holds the live state.
- **Agent pathfinding over navmap cost** (navmap currently serves env terrain + WI-P2 fauna
  TerrainSampler + flora SiteInput; agent MoveTo still uses distance/`arrival_epsilon`) → a separate
  future wiring, not WI-P1.
- **`Mine` terrain-driven extraction** (resources R1) → WI-P3 / `engine/mind/actions` + materials.

---

## Open Questions

> `docs/plans/world-integration.md` W1-W9 are RESOLVED (human 2026-06-27; numerics in `content/world.yaml`).
> This SPEC writes from those resolutions and invents no mechanism. Remaining seams are
> content/config plumbing, not blocking decisions:

- **Terrain attribute-vector accessor (content seam, WI-P0/P1).** `flora.SiteInput.TerrainAttrs`
  (§5 grainSize/slope/depth/salinity) needs a per-terrain attribute preset. navmap holds the
  `TerrainID`; the attribute vector lives in `content/terrain.yaml`. Options: (a) the world reads a
  `map[TerrainID]Attrs` injected by `platform/config` and samples it by `nav.TerrainAt`; (b) navmap
  carries the attrs on its `TerrainType`. **rec: (a)** — keeps navmap a pure cost field; the world
  joins TerrainID→attrs. Finalize with the `content/terrain.yaml` schema (WI-P0).
- **Flora as `objects[]` records vs a separate render set (non-blocking).** flora 1i puts plants in
  `objects[]`; the world must keep the object record (`{id,kind,pos}` for spatial/perception/render)
  in sync with `flora.State` morphology on every spawn/die/grow. rec: the apply-flora-deltas step is
  the single sync point (spawn adds both, die removes both, grow updates morphology only). Documented;
  not a mechanism choice.

---

## Notes

- The env sub-phase deliberately mirrors the existing apply phase's "world is the sole mutator,
  serial, sorted" shape — it just adds env-state evolution after intent apply. Keeping it a distinct
  trailing sub-phase (not interleaved with intent apply) keeps the conflict/outcome model
  (SPEC-tick.md) untouched and the env additions outcome-neutral until installed.
- climate/flora/decay are all the SAME "pure read → return (next,deltas) → world applies" template
  (their SPEC Notes say so explicitly); this sub-phase is the single place that template is driven.
- `envFork` keys on a channel tag so adding the WI-P2 `"fauna"`/scent draws never perturbs the
  climate/flora/decay streams (and vice-versa) — the per-tick env RNG stays partitioned by concern.
- Reference paths: `docs/plans/world-integration.md` (WI-P1, W1-W9 resolutions), `content/world.yaml`
  (geometry/cadence), `backend/engine/env/{climate,flora,decay}/SPEC.md` (the pure transforms),
  `backend/engine/space/navmap/SPEC.md` (`SetTerrain`/`CellOf`/`TerrainAt`), `SPEC-tick.md` (the
  four-phase loop this extends), `backend/engine/mind/perception/SPEC.md` (`ShadeOccluder`),
  `docs/core/data-contracts.md §6/§8` (decay + terrain delta shapes), `SPEC-world-fauna.md` (WI-P2).
