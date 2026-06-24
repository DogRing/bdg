# Architecture — Module Dependency DAG & Build Order

## 1. Top-level split
- `backend/engine/` — **pure deterministic simulation. No IO.** (headless, NFR-6)
- `backend/platform/` — **IO / infra.** Redis, Postgres, SSE, config loading.
- `content/` — data-driven content (stats, actions, gates, balance) + schema. `platform/config` loads it into registries.

The engine does not import platform. Platform serializes and transports engine state. Events are emitted by the engine through an **interface** that platform implements (dependency inversion).

## 2. Engine modules
| Module | Purpose | Depends on (paths) | Leaf level |
|--------|---------|--------------------|-----------|
| `core` | Types: `StatID`, `Dimension`, `Tag`, `Pred`, `Referent`, `Vec2`, `GameMinutes`, cross-cutting interfaces. | — | **L0** |
| `rng` | Deterministic seeded RNG wrapper | — | **L0** |
| `expr` | The shared **§6 `Formula` evaluator** (`design.md §6`, line 89): arithmetic `+ - * /` + comparison + logical `& | !` over StatIDs / context vars; numeric OR boolean output. `gates`' `GateExpr` is its boolean subset (glossary "one shared evaluator"); `climate`/`flora`/`actions`/`economy` evaluate compiled `Program`s against an abstract `Context`. Depends only on `core` (no `stats`), so L1 leaves (`climate`/`flora`) can reuse it without a DAG break. | core | **L0** |
| `worldtime` | tick ↔ game-minute conversion, 12× scale | core | L1 |
| `spatial` | Free coordinates + spatial hash, radius queries | core | L1 |
| `stats` | `Stats` vector + `StatRegistry` | core | L1 |
| `navmap` | Navigation cost field (terrain base cost + sparse `wear` trails + building footprints) over a grid **index**; continuous positions preserved (D11). `Cost`/`Passable`/`TerrainAt`/`Deposit`/`Decay`/`StampFootprint` + **`SetTerrain` (climate-driven dynamic terrain, world-owned)** + snapshot | core | L1 |
| `climate` | **Dynamic-terrain driver** (`docs/climate.md`): pure deterministic transform `(coarse Moisture/Temperature grid + time-derived forcing + transition rules) → (next state, terrain-transition cell list)`. Rain process (seeded), Temperature model, `content/climate.yaml` transition table (§6-DSL boolean condition). Does NOT import navmap/worldtime/gates — `world` performs the `navmap.SetTerrain` write + supplies forcing | core (+ rng, expr) | L1 |
| `flora` | **Flora driver** (`docs/flora.md`): pure deterministic transform `(plant set + per-plant terrain/climate inputs + flora Rules, rng) → (growth/propagation/death deltas)` + per-plant `Shade` parameters. Continuous `Growth` (stages derived); `Suitability`/growth/shade/propagation/death/yields = §6 over `content/objects.yaml` `flora:` blocks. Seed-dispersal propagation, sustained-unsuitability death (hysteresis). Does NOT import navmap/climate/world/perception — `world` injects terrain/climate as VALUES and applies the deltas (sole object mutator); `perception` consumes the `Shade` parameter. Flora not owned unless planted. | core (+ rng, expr) | **L1** |
| `pathfind` | Deterministic A*/Theta\* over a `navmap` snapshot → waypoint path + total cost | core, navmap | L2 |
| `gates` | `Gate` registry, eval (boolean visibility predicate trees; AND across matching gates); evaluates the §6-DSL boolean subset via `expr` | core, stats, expr | L2 |
| `needs` | Need dimension catalog + rates (from balance.yaml), decay, forward-roll helpers | core, stats | L2 |
| `tom` | `Belief` (incl. self), reputation distribution, gossip, initial estimates | core, stats, rng | L2 |
| `actions` | Catalog, tags, effect, Duration, `Producers` | core, stats, gates | L3 |
| `values` | Value map, Standing, Salience, appraisal | core, stats, needs, tom | L3 |
| `perception` | Three senses (Sight LoS, Smell gradient, Hearing) + **flora-shade LoS attenuation** (continuous `ShadeOccluder` composition ∏(1−opacity), "dark forest"; world adapts `flora.Shade` — perception does NOT import flora) | core, spatial | L3 |
| `planner` | HTN + GOAP, forward-sim, budget, gate application, **tag-derived cost** (+ path-cost term for locomotion) | core, actions, gates, needs, values, (navmap/pathfind for cost) | L4 |
| `agent` | Decision loop, durative execution, coping; path-cost `bindTarget` + waypoint-following MoveTo/Approach | core, stats, needs, values, tom, planner, perception, navmap, pathfind | L5 |
| `world` | Orchestrator: spawn, tick, intent collection, apply + conflict resolution; **owns navmap** (wear deposit on traversal, decay per tick, `SetTerrain` on climate transitions), **climate** (drives `climate.Step` on the `tick % N` cadence, maps transition cells → navmap cells), and **flora** (drives `flora.Step` on the flora cadence, samples terrain/climate per plant, applies spawn/grow/die deltas to objects[]/spatial, adapts `flora.Shade` for perception) | core, spatial, worldtime, rng, agent, actions, navmap, climate, flora, (events iface) | L6 |

## 3. Platform modules
| Module | Purpose | Depends on |
|--------|---------|-----------|
| `config` | Load `content/` data → registries (stats, needs, actions, gates, object/item/terrain catalog, **climate transition table + rates**, **flora `Rules` — §6 formulas compiled via `expr`, yield tables**); validate vs `content/schema/` + referential integrity (incl. climate `from`/`to`/`when`-operand cross-check + flora species/item/operand cross-check) | core, stats, actions, gates, needs, expr |
| `events` | why-trace + event stream (implements the engine's events interface) | core |
| `persist` | Snapshot serialization, Redis live, Postgres backup | core, world (state types), data-contracts |
| `api` | health/SSE/snapshot/agent + god-view HTTP. `New` = full server; `NewSSE` = read-only (health+SSE) | events, persist, core |

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
      ├─ stats ──┬── gates ──┐      │          │
      │          ├── needs ──┼── planner ── agent ── world ── persist
      │          └── tom ────┤      │           │        │
      │              values ─┘──────┘           │        events(iface)
      └─ expr ─── gates / climate / flora
rng ──┬───────────────────────────────────── world
      ├─ climate                              world
      └─ flora                                world
content ── config ── (stats/actions/gates/needs/terrain/climate/flora registries)
```
No cycles. Note: `values → tom` is one-way (for Other-referent evaluation); `tom` does not know `values`. `perception` does not import `world`; it operates on a passed-in world view (`WorldSnapshot`) — and it does NOT import `flora`: the world adapts `flora.Shade`/`ShadeOf` into perception's `ShadeOccluder` view (dependency inversion, like `IsOpaque`). `tom` also depends on `rng`. `navmap → pathfind` is one-way; `pathfind` is a pure query over a navmap snapshot and never mutates it. **`climate` and `flora` (both `core`, `rng`, `expr`) are one-way too: each returns a delta (`climate` → terrain-transition cells; `flora` → spawn/grow/die plant deltas) and `world` performs the navmap/objects write — neither imports `navmap`, `worldtime`, `gates`, `world`, or each other. This keeps `world` the single navmap+object mutator (D12 apply phase).** The §6-DSL evaluator is now **`engine/expr` (L0, `design.md §6`)**, shared by `gates`/`climate`/`flora` — this resolves the prior climate escalation (it no longer needs lifting to `core`; `expr` is its own L0 leaf depending only on `core`).

## 5. Leaf-first build order (topological)
```
1) core, rng, expr
2) worldtime, spatial, stats, navmap, climate, flora
3) gates, needs, tom, pathfind
4) actions, values, perception
5) planner
6) agent
7) world
8) config, events, persist
9) api (full + NewSSE), main.go (bdg-backend), cmd/sse (bdg-sse)
10) (later) frontend
```
Each stage depends only on the **public interfaces (SPECs)** of earlier stages. The implementer never reads a sibling's implementation. `navmap`, `climate`, and `flora` are independent L1 leaves in stage 2 (climate/flora depend on `core` + `rng` + `expr`); their integration (cadence + the navmap `SetTerrain` bridge / the flora objects[] apply + per-plant terrain/climate sampling) lands later in `world` (stage 7), and the flora-shade LoS extension lands in `perception` (stage 4). *Caveat:* climate's and flora's §6 condition/formula evaluation requires `engine/expr` (stage 1) to exist; the `expr` evaluator is the shared §6 home (`design.md §6`). The introduction phases ship **outcome-neutral** (climate `Rules` empty / flora off) so existing goldens hold; activation is a deliberate later phase with its own re-baseline (`docs/climate.md §2`, `docs/flora.md §2`).

## 6. Module SPEC location
Each module gets `backend/engine/<m>/SPEC.md` or `backend/platform/<m>/SPEC.md`.
spec-architect generates SPECs leaf-first along this DAG. A module exceeding ~400 lines is split into sub-folders, each with its own SPEC.

## 7. Content boundary (D10)
`content/stats.yaml · needs.yaml · objects.yaml · actions.yaml · gates.yaml · balance.yaml`
(+ `content/schema/` + `content/README.md`).
- `needs.yaml` — need/value Dimension **catalog** (kind, posture, setpoint, salience). Per-need
  **rate** (`decay_per_tick`, `satisfaction_threshold`) lives in `balance.yaml`'s `needs:` block —
  rate only (D9: demand is derived, never authored). `engine/needs.Load` merges the two.
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
  `platform/config` compiles the §6 formulas via `engine/expr` into `flora.Rules` (`docs/flora.md`).
- `terrain.yaml` *(planned, see `docs/map-plan.md`)* — **바탕재료 = 속성 프리셋**(`grainSize·moisture·temperature·depth·slope·salinity`); cost·passability는 per-type 상수가 아니라 **속성 위 §6 수식**(`design.md §5`) + the
  **action/capability tags** required to traverse (`terrain:water`→Swim, `terrain:steep`→stat gate),
  D4/D10. The per-run terrain **layout** (regions) is world-gen / `map.yaml`, not the type catalog.
  Roads/trails are **NOT** content — they are emergent `navmap.wear` state (D2/D3).
- `climate.yaml` *(see `docs/climate.md`, `content/schema/climate.schema.json`)* — the **dynamic-terrain
  transition table** (`from`-type × `when` §6-DSL condition → `to`-type) + the climate **balance
  rates** (rain probability, evaporation, temperature curve) + coarse-grid geometry. The 10d-expected /
  30d-forced / 2–12h-duration **shape** is fixed engine design, NOT a content knob (D9 spirit: rates
  only). `platform/config` compiles it into `climate.Rules` + `climate.Config`, validates every
  `from`/`to` against `terrain.yaml`, and validates each `when` operand via the shared §6 evaluator
  (an undefined terrain id or operand is a load-time failure, D10). Climate state
  (`Moisture`/`Temperature`) is **runtime** state, not content (D2/D9-analogous).
Engine code is **content-agnostic** — `config` loads these at startup to populate registries.
Adding a stat / need / action / gate / object / flora-species / terrain / climate-rule = **a data file + passing schema**, with no code change.
`gates.yaml` is also the **contract** for the (unbuilt) `engine/gates` evaluator — see `content/README.md`.
