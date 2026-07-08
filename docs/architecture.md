# Architecture — Module Dependency DAG & Build Order

## 1. Top-level split
- `backend/engine/` — **pure deterministic simulation. No IO.** (headless, NFR-6)
- `backend/platform/` — **IO / infra.** Redis, Postgres, SSE, config loading.
- `content/` — data-driven content (stats, actions, gates, balance) + schema. `platform/config` loads it into registries.

The engine does not import platform. Platform serializes and transports engine state. Events are emitted by the engine through an **interface** that platform implements (dependency inversion).

## 2. Engine modules

**Folder grouping (on-disk layout; the DAG/levels below are *logical*).** Modules are grouped into
sub-folders by concern — the import path reflects the group, e.g. `github.com/dogring/bdg/engine/kernel/core`.
Each group folder carries a `README.md`. The dependency table and DAG use **bare module names** (a package's
logical identity = its package name, unchanged by grouping).
- `engine/kernel/` — primitives everything builds on: `core`, `rng`, `expr`, `worldtime`.
- `engine/space/` — coordinates, cost field, pathfinding, scent field: `spatial`, `navmap`, `pathfind`, `scent`.
- `engine/env/` — pure world-dynamics Step transforms (return deltas; `world` applies): `climate`, `flora`, `decay`, `exposure`.
- `engine/mind/` — agent cognition/decision faculties: `stats`, `needs`, `values`, `tom`, `gates`, `perception`, `actions`, `planner`.
- `engine/agent/`, `engine/world/` — top-of-DAG controller + orchestrator, left **flat**. The future
  `engine/fauna/` (reduced-reactive animal controller, `docs/fauna.md`) lands flat next to `agent`.

| Module | Purpose | Depends on (paths) | Leaf level |
|--------|---------|--------------------|-----------|
| `core` | Types: `StatID`, `Dimension`, `Tag`, `Pred`, `Referent`, `Vec2`, `GameMinutes`, cross-cutting interfaces. | — | **L0** |
| `rng` | Deterministic seeded RNG wrapper | — | **L0** |
| `expr` | The shared **§6 `Formula` evaluator** (`design.md §6`, line 89): arithmetic `+ - * /` + comparison + logical `& | !` over StatIDs / context vars; numeric OR boolean output. `gates`' `GateExpr` is its boolean subset (glossary "one shared evaluator"); `climate`/`flora`/`decay`/`actions`/`economy` evaluate compiled `Program`s against an abstract `Context`. Depends only on `core` (no `stats`), so L1 leaves (`climate`/`flora`/`decay`) can reuse it without a DAG break. | core | **L0** |
| `worldtime` | tick ↔ game-minute conversion, 12× scale | core | L1 |
| `spatial` | Free coordinates + spatial hash, radius queries | core | L1 |
| `stats` | `Stats` vector + `StatRegistry` | core | L1 |
| `navmap` | Navigation cost field (terrain base cost + sparse `wear` trails + building footprints) over a grid **index**; continuous positions preserved (D11). `Cost`/`Passable`/`TerrainAt`/`Deposit`/`Decay`/`StampFootprint` + **`SetTerrain` (climate-driven dynamic terrain, world-owned)** + snapshot | core | L1 |
| `climate` | **Dynamic-terrain driver** (`docs/climate.md`): pure deterministic transform `(coarse Moisture/Temperature grid + time-derived forcing + transition rules) → (next state, terrain-transition cell list)`. Rain process (seeded), Temperature model, `content/climate.yaml` transition table (§6-DSL boolean condition). Does NOT import navmap/worldtime/gates — `world` performs the `navmap.SetTerrain` write + supplies forcing | core (+ rng, expr) | L1 |
| `flora` | **Flora driver** (`docs/flora.md`): pure deterministic transform `(plant set + per-plant terrain/climate inputs + flora Rules, rng) → (growth/propagation/death deltas)` + per-plant `Shade` parameters. Continuous `Growth` (stages derived); `Suitability`/growth/shade/propagation/death/yields = §6 over `content/objects.yaml` `flora:` blocks. Seed-dispersal propagation, sustained-unsuitability death (hysteresis). Does NOT import navmap/climate/world/perception — `world` injects terrain/climate as VALUES and applies the deltas (sole object mutator); `perception` consumes the `Shade` parameter. Flora not owned unless planted. | core (+ rng, expr) | **L1** |
| `decay` | **Decay driver** (`docs/materials.md`, P_m2): pure deterministic transform `(perishable item-LOT set + per-lot env {Temperature, Moisture, StorageRateMult} + decay Rules, elapsedTicks, rng) → (age/transition/transform/gone deltas)`. Discrete decay states fresh→stale→rotten→gone derived from a continuous effective-decay-time accumulator `decayAge` (Q1/Dm1(a)); multiplicative env-coupled acceleration `effectiveRate = baseRate · accel(temperature,moisture) · storageRateMult` via a §6 `accel` Program it OWNS + evaluates through `engine/kernel/expr` (Q2/Dm2(a)/Dm3(a), flora parity); transform-on-transition emits a product, not vanish (Q3, D9 locality); lot-granular + owner-agnostic (Dm5(a)/Dm4(a)). Does NOT import climate/world/flora/navmap — `world` injects climate as VALUES + the storage multiplier, enumerates the decayable-lot set (floor + every inventory + storage), and applies the deltas (sole object mutator). Mirrors flora's pure-Step shape. **Dm1–Dm5 RESOLVED (a) — leaf is READY (after `engine/kernel/expr`).** | core (+ rng, expr) | **L1** |
| `exposure` | **Shelter/exposure field** (`engine/env/exposure`, `docs/shelter.md` SH1): pure deterministic transform `(blocks_wind blocker footprints + interiors + wind sector, hex Topology) → per-cell exposure ε ∈ [0,1]`; directional leeward wind-shadow (multiplicative → order-free, D12), 6-sector cached. `local wind = global × ε`. Imports ONLY core (combinatorial over the hex graph — no rng/expr); `world` adapts navmap (`Topology`) + climate `Wind` as VALUES and injects the attenuated local wind into the existing scent/fauna seams (no new operands). Caves = ε=0 interiors (SH2). **SH1 SPEC READY.** | core | **L1** |
| `exposure` | **Shelter/exposure field** (`docs/shelter.md`, SH1): pure deterministic transform `{blocks_wind blocker footprints + forced interior cells + wind sector} → per-cell exposure ε∈[0,1]`. world adapts navmap topology into the leaf, caches 6 wind-sector fields, and injects `globalWind×ε` into scent/fauna env samples. Does NOT import navmap/climate/scent/fauna/world; blockers/interiors are tag-derived data supplied by world/config. Caves use the same shelter concept through `world`'s portal/interior state (`SPEC-world-shelter.md`, SH2). | core | **L1** |
| `pathfind` | Deterministic A*/Theta\* over a `navmap` snapshot → waypoint path + total cost | core, navmap | L2 |
| `gates` | `Gate` registry, eval (boolean visibility predicate trees; AND across matching gates); evaluates the §6-DSL boolean subset via `expr` | core, stats, expr | L2 |
| `needs` | Need dimension catalog + rates (from balance.yaml), decay, forward-roll helpers | core, stats | L2 |
| `tom` | `Belief` (incl. self), reputation distribution, gossip, initial estimates | core, stats, rng | L2 |
| `actions` | Catalog, tags, effect, Duration, `Producers`; the recipe-mediated `Craft` + terrain-altering `Mine` shapes (Materials P_m3/P_m4, "Recipe model — FINAL") | core, stats, gates | L3 |
| `values` | Value map, Standing, Salience, appraisal | core, stats, needs, tom | L3 |
| `perception` | Three senses (Sight LoS, Smell gradient, Hearing) + **flora-shade LoS attenuation** (continuous `ShadeOccluder` composition ∏(1−opacity), "dark forest"; world adapts `flora.Shade` — perception does NOT import flora) | core, spatial | L3 |
| `planner` | HTN + GOAP, forward-sim, budget, gate application, **tag-derived cost** (+ path-cost term for locomotion); binds a `recipe` for a `recipe_mediated` action (Craft) — slots satisfiable + ambient in range | core, actions, gates, needs, values, (navmap/pathfind for cost) | L4 |
| `agent` | Decision loop, durative execution, coping; path-cost `bindTarget` + waypoint-following MoveTo/Approach | core, stats, needs, values, tom, planner, perception, navmap, pathfind | L5 |
| `fauna` | **Reduced-reactive animal controller** (`docs/fauna.md`; flat next to `agent`): per-tick **horizon-1 utility arbitration** over the shared atomic-action registry (drives + §6, **no planner/ToM**); **reads** the shared `space/scent` field (world deposits from `scent:<channel>` emitters). Emits intents; **`world` applies** (combined agent+animal ID order — dependency-inverted like climate/flora/decay). Does **NOT** import `world`/`agent`/`planner`. | core, expr, rng, actions, spatial, space/scent | L4 |
| `scent` | **Shared scent field** (`engine/space/scent`, `docs/.../scent/SPEC.md`): uniform multi-channel **scalar-intensity** index (F21 rev.) over continuous space (spatial/navmap kin, D11); deposit (∝ magnitude) / wind-spread (diffusion) / read (intensity+gradient) / `IntensityAt`. `world` deposits from `scent:<channel>` emitters (flora/fauna/decay) + drives cadence; `fauna` (and later `perception`) read. | core | L1 |
| `world` | Orchestrator: spawn, tick, intent collection, apply + conflict resolution; **owns navmap** (wear deposit on traversal, decay per tick, `SetTerrain` on climate transitions **and ore-node depletion**), **climate** (drives `climate.Step` on the `tick % N` cadence, maps transition cells → navmap cells), **flora** (drives `flora.Step` on the flora cadence, samples terrain/climate per plant, applies spawn/grow/die deltas to objects[]/spatial, adapts `flora.Shade` for perception), **decay** (drives `decay.Step` on the decay multi-rate cadence, enumerates the perishable-LOT set across floor/inventory/storage owner-agnostically, samples climate + computes the storage rate mult per lot, applies age/transition/transform/gone deltas to objects[]/inventory), **exposure/shelter** (derives tag-based blockers, caches ε fields, injects local wind, owns cave portal/interior active-space state), and the **Craft/Mine apply** (Materials FINAL: per-slot first-satisfiable alternative, `consume` most-decayed-first / `wear` most-worn-first + break at 0, `basis_stat` outcome roll + produced durability, ore-node `remaining`↓ + one `SetTerrain` at 0) | core, spatial, worldtime, rng, agent, actions, navmap, climate, flora, decay, exposure, (events iface) | L6 |

## 3. Platform modules
| Module | Purpose | Depends on |
|--------|---------|-----------|
| `config` | Load `content/` data → registries (stats, needs, actions, gates, object/item/terrain catalog, **climate transition table + rates**, **flora `Rules` — §6 formulas compiled via `expr`, yield tables**, **decay `Rules` — §6 `accel` compiled via `expr`, ordered states/thresholds/transforms**, **recipe registry — slot/alternative inputs (wear|consume) + ambient + duration + basis_stat + outputs from `content/recipes.yaml`**, **material/tool/station `tags` + `tool:{wear_max}` + `source:{}` on item/object kinds**); validate vs `content/schema/` + referential integrity (incl. climate `from`/`to`/`when`-operand cross-check, flora species/item/operand cross-check, decay kind/item/threshold-ordering cross-check, recipe output-item + alternative-tagQuery + `wear`-alternative-must-be-`tool` + ambient-tag + `basis_stat`-StatID cross-check) | core, stats, actions, gates, needs, expr |
| `events` | why-trace + event stream (implements the engine's events interface) | core |
| `persist` | Snapshot serialization, Redis live, Postgres backup | core, world (state types), data-contracts |
| `api` | health/SSE/snapshot/agent + god-view HTTP. `New` = full server; `NewSSE` = read-only (health+SSE) | events, persist, core |
| `tools/worldgen` | **(config-class IO/init, NOT engine)** world-gen generator (`Generate`: seed→`Fixture`, author-time WG1-a) + fixture **loader** (`Load`: fixture→navmap/climate/flora/decay states→`world.InstallEnv`/`InstallFauna`+Spawn/PlaceObject). Unified `Fixture` format (`content/schema/fixture.schema.json`, W9). Imported by `main` (run-time `Load`) + a gen cmd (author-time `Generate`); the engine never imports it (per-run fixtures, not content, §7). | config, world, + engine constructors (navmap/climate/flora/decay/scent), rng |

### Entrypoints (two binaries, one module)
- `backend/main.go` → **bdg-backend** (`gapi.dogring.kr`): simulation writer + full `api.New` server. Single writer (replicas: 1).
- `backend/cmd/sse` → **bdg-sse** (`sse.dogring.kr`): read-only `api.NewSSE` server tailing the valkey event stream with a read-only user. Stateless, scalable. Built/shipped separately (`backend/Dockerfile.sse`, `.github/workflows/sse.yaml`). See `docs/deployment.md`.

## 4. Dependency DAG
```
core ─┬─ worldtime ─┐
      ├─ spatial ───┼─ perception ─┐
      ├─ navmap ──── pathfind ─────┼──────────┐
      ├─ climate ──────────────────┤          │
      ├─ flora ────────────────────┤          │
      ├─ decay ────────────────────┤          │
      ├─ exposure ─────────────────┤          │
      ├─ stats ──┬── gates ──┐      │          │
      │          ├── needs ──┼── planner ── agent ── world ── persist
      │          └── tom ────┤      │           │        │
      │              values ─┘──────┘           │        events(iface)
      └─ expr ─── gates / climate / flora / decay
rng ──┬───────────────────────────────────── world
      ├─ climate                              world
      ├─ flora                                world
      └─ decay                                world
actions·spatial·expr·rng ── fauna ──────────── world   (intents → world applies)
content ── config ── (stats/actions/gates/needs/terrain/climate/flora/decay/recipe registries)
```
No cycles. Note: `values → tom` is one-way (for Other-referent evaluation); `tom` does not know `values`. `tom` also depends on `rng`. `perception` does not import `world`; it operates on a passed-in world view (`WorldSnapshot`) — and it does NOT import `flora`: the world adapts `flora.Shade`/`ShadeOf` into perception's `ShadeOccluder` view (dependency inversion, like `IsOpaque`). `navmap → pathfind` is one-way; `pathfind` is a pure query over a navmap snapshot and never mutates it. **`climate`, `flora`, and `decay` (all `core`, `rng`, `expr`) are one-way too: each returns a delta (`climate` → terrain-transition cells; `flora` → spawn/grow/die plant deltas; `decay` → age/transition/transform/gone lot deltas) and `world` performs the navmap/objects/inventory write — none imports `navmap`, `worldtime`, `gates`, `world`, or each other. This keeps `world` the single navmap+object+inventory mutator (D12 apply phase).** `decay` imports `engine/kernel/expr` to OWN + evaluate its `accel` §6 Program (Dm3(a), exactly like flora). The §6-DSL evaluator is **`engine/kernel/expr` (L0, `design.md §6`)**, shared by `gates`/`climate`/`flora`/`decay` — this resolves the prior climate escalation (it no longer needs lifting to `core`; `expr` is its own L0 leaf depending only on `core`). **`fauna` is one-way too:** it imports only `core`/`expr`/`rng`/`actions`/`spatial`, emits intents, and `world` applies them (combined agent+animal ID order) — `fauna` never imports `world`/`agent`/`planner`, so adding it introduces no cycle (same dependency-inversion as climate/flora/decay).

## 5. Leaf-first build order (topological)
```
1) core, rng, expr
2) worldtime, spatial, stats, navmap, climate, flora, decay, exposure
3) gates, needs, tom, pathfind
4) actions, values, perception
5) planner
6) agent, fauna
7) world
8) config, events, persist
9) api (full + NewSSE), main.go (bdg-backend), cmd/sse (bdg-sse)
10) (later) frontend
```
Each stage depends only on the **public interfaces (SPECs)** of earlier stages. The implementer never reads a sibling's implementation. `navmap`, `climate`, `flora`, and `decay` are independent L1 leaves in stage 2 (climate/flora/decay depend on `core` + `rng` + `expr`); their integration (cadence + the navmap `SetTerrain` bridge / the flora objects[] apply + per-plant terrain/climate sampling / the decay lot-set enumeration + per-lot climate sampling + storage-mult + inventory apply / the Craft+Mine apply) lands later in `world` (stage 7), and the flora-shade LoS extension lands in `perception` (stage 4). *Caveat:* climate's, flora's, and decay's §6 condition/formula evaluation requires `engine/kernel/expr` (stage 1) to exist; the `expr` evaluator is the shared §6 home (`design.md §6`). The introduction phases ship **outcome-neutral** (climate `Rules` empty / flora off / decay off — no perishable lots placed) so existing goldens hold; activation is a deliberate later phase with its own re-baseline (`docs/climate.md §2`, `docs/flora.md §2`, `docs/materials.md §2`). **`engine/env/decay` (P_m2) is READY: `docs/materials.md §1` Dm1–Dm5 are RESOLVED (a)** (continuous `decayAge` accumulator, multiplicative §6 `accel` decay-owned, owner-agnostic lot granularity); it builds after `engine/kernel/expr` (stage 1). **`fauna` (stage 6, beside `agent`)** depends only on `core`/`expr`/`rng`/`actions`/`spatial`; like climate/flora/decay it ships **outcome-neutral** (P_fa1/P_fa2: controller + scent grid built but no species placed → zero intents → existing goldens hold), with `world` wiring its intents + scent bulk-pass at stage 7 and the deliberate activation/re-baseline at P_fa3 (`docs/fauna.md §2`).

## 6. Module SPEC location
Each module gets `backend/engine/<group>/<m>/SPEC.md` (grouped per §2; `agent`/`world` flat at
`backend/engine/<m>/SPEC.md`) or `backend/platform/<m>/SPEC.md`.
spec-architect generates SPECs leaf-first along this DAG. A module exceeding ~400 lines is split into sub-folders, each with its own SPEC.

## 7. Content boundary (D10)
`content/stats.yaml · needs.yaml · objects.yaml · actions.yaml · gates.yaml · balance.yaml · recipes.yaml`
(+ `content/schema/` + `content/README.md`).
- `needs.yaml` — need/value Dimension **catalog** (kind, posture, setpoint, salience). Per-need
  **rate** (`decay_per_tick`, `satisfaction_threshold`) lives in `balance.yaml`'s `needs:` block —
  rate only (D9: demand is derived, never authored). `engine/mind/needs.Load` merges the two.
- `gates.yaml` — gate registry as boolean **predicate trees** (`{id, tags, expr}`; schema_version
  2). Gates are tag-matched (D4) visibility preconditions reading `ToM[self]` (D8); **cost derives
  from tags in the planner**, not from gates.
- `objects.yaml` — object_kinds + item_kinds carrying their **supply** Effect (D9). Placement/counts
  are per-run world-gen / fixtures, not content. Buildings (wall/house/door) are object_kinds whose
  **footprint** the world stamps into `navmap` (walls block, doors are portals). **FLORA species** are
  object_kinds carrying a `flora:` block — the §6 formulas + thresholds for growth/suitability/shade/
  propagation/death + a **yield table** (`harvest.yields: [{item, chance, qty:[min,max]}]`, seeded
  roll, `chance=§6(Dexterity)`). A flora species' "regen" is its yield table refilling via `Growth`,
  **local to the object** (D9) — NOT a `balance.regen.*` timer (those are legacy, for non-flora kinds).
  **MATERIAL tags** — item_kinds carry `tags` (e.g. `shaft_stock`, `blade_stock`, `tool:cutting`);
  object_kinds carry station tags (e.g. `station:bench`, `station:forge`). A recipe alternative is a
  tag-query satisfied by any kind whose `tags` is a superset, so substitutability EMERGES from tags
  (D4) and a new material is eligible by carrying the tag — no recipe edit. **TOOLS** carry a `tool`
  durable block = `{ wear_max }` (the durability ceiling; FINAL — no per-item `wear_per_use`/`quality`).
  **FINITE SOURCES** (`ore_node`) carry a `source: { initial, depleted_terrain }` block (object-local
  `remaining`, Xm1). **DECAY** — a perishable item_kind carries a `decay:` block: ordered discrete
  `states` (fresh→stale→rotten→gone), per-state entry `threshold` (in effective-decay-time units,
  Dm1(a)) + `transform` product (D9 locality — produces an item, not vanish) + optional per-state
  `supply` override, a data `baseRate`, and an env-coupled `accel` §6 Formula over the climate output
  operands `temperature`/`moisture` (Materials Q1–Q3; effective rate is `baseRate · accel ·
  storageRateMult`, Dm2(a)). The storage-structure rate multiplier (cold-storage emerges) is
  world-injected, NOT authored on the item. `platform/config` compiles the §6 formulas via `engine/kernel/expr`
  into `flora.Rules`/`decay.Rules` (`docs/flora.md`, `docs/materials.md`).
- `recipes.yaml` *(new, see `docs/materials.md §0 "Recipe model — FINAL"`, `content/schema/recipes.schema.json`)*
  — the **recipe catalog** mediating the single `Craft` atomic action (D3). Each recipe `{ id, inputs[],
  ambient[]?, duration, basis_stat, outputs[] }`: `inputs[]` is an ordered list of SLOTS, each
  `{ any: [alternative,…] }` satisfied by the FIRST satisfiable alternative `{ tagQuery (AND),
  amount, mode: wear|consume }` (D12); `ambient[]` = station tags the actor must be in range of
  (substitutable, NOT consumed); `duration` = per-recipe ticks; `basis_stat` = the StatID whose roll
  drives success/qty + produced durability; `outputs[] = {item, base_qty}`. Craft has NO tool/station
  action tag (the gate is "inputs present + ambient in range"). Recipes are DATA; the gather→craft plan
  is assembled by the planner (D3). `platform/config` cross-checks output items, alternative tagQueries,
  `wear`-alternative-must-be-a-`tool`, ambient tags, and `basis_stat`.
- `terrain.yaml` *(planned, see `docs/map-plan.md`)* — **바탕재료 = 속성 프리셋**(`grainSize·moisture·temperature·depth·slope·salinity`); cost·passability는 per-type 상수가 아니라 **속성 위 §6 수식**(`design.md §5`) + the
  **action/capability tags** required to traverse (`terrain:water`→Swim, `terrain:steep`→stat gate),
  D4/D10. The per-run terrain **layout** (regions) is world-gen / `map.yaml`, not the type catalog.
  Roads/trails are **NOT** content — they are emergent `navmap.wear` state (D2/D3). A depleted
  `bare_rock` type (the ore_node `depleted_terrain`, Xm3/Xm6) is authored here.
- `climate.yaml` *(see `docs/climate.md`, `content/schema/climate.schema.json`)* — the **dynamic-terrain
  transition table** (`from`-type × `when` §6-DSL condition → `to`-type) + the climate **balance
  rates** (rain probability, evaporation, temperature curve) + coarse-grid geometry. The 10d-expected /
  30d-forced / 2–12h-duration **shape** is fixed engine design, NOT a content knob (D9 spirit: rates
  only). `platform/config` compiles it into `climate.Rules` + `climate.Config`, validates every
  `from`/`to` against `terrain.yaml`, and validates each `when` operand via the shared §6 evaluator
  (an undefined terrain id or operand is a load-time failure, D10). Climate state
  (`Moisture`/`Temperature`) is **runtime** state, not content (D2/D9-analogous).
Engine code is **content-agnostic** — `config` loads these at startup to populate registries.
Adding a stat / need / action / gate / object / flora-species / decaying-item / recipe / material-tag / terrain / climate-rule = **a data file + passing schema**, with no code change.
`gates.yaml` is also the **contract** for the (unbuilt) `engine/mind/gates` evaluator — see `content/README.md`.
