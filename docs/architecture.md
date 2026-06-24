# Architecture — Module Dependency DAG & Build Order

## 1. Top-level split
- `backend/engine/` — **pure deterministic simulation. No IO.** (headless, NFR-6)
- `backend/platform/` — **IO / infra.** Redis, Postgres, SSE, config loading.
- `content/` — data-driven content (stats, actions, gates, balance) + schema. `platform/config` loads it into registries.

The engine does not import platform. Platform serializes and transports engine state. Events are emitted by the engine through an **interface** that platform implements (dependency inversion).

## 2. Engine modules
| Module | Purpose | Depends on (paths) | Leaf level |
|--------|---------|--------------------|-----------|
| `core` | Types: `StatID`, `Dimension`, `Tag`, `Pred`, `Referent`, `Vec2`, `GameMinutes`, cross-cutting interfaces. *(PROPOSED: home of the shared §6 `Formula` boolean evaluator so `gates` + `climate` reuse it — see `docs/climate.md` escalation; not yet in the `core` SPEC.)* | — | **L0** |
| `rng` | Deterministic seeded RNG wrapper | — | **L0** |
| `worldtime` | tick ↔ game-minute conversion, 12× scale | core | L1 |
| `spatial` | Free coordinates + spatial hash, radius queries | core | L1 |
| `stats` | `Stats` vector + `StatRegistry` | core | L1 |
| `navmap` | Navigation cost field (terrain base cost + sparse `wear` trails + building footprints) over a grid **index**; continuous positions preserved (D11). `Cost`/`Passable`/`TerrainAt`/`Deposit`/`Decay`/`StampFootprint` + **`SetTerrain` (climate-driven dynamic terrain, world-owned)** + snapshot | core | L1 |
| `climate` | **Dynamic-terrain driver** (`docs/climate.md`): pure deterministic transform `(coarse Moisture/Temperature grid + time-derived forcing + transition rules) → (next state, terrain-transition cell list)`. Rain process (seeded), Temperature model, `content/climate.yaml` transition table (§6-DSL boolean condition). Does NOT import navmap/worldtime/gates — `world` performs the `navmap.SetTerrain` write + supplies forcing | core (+ rng) | L1 |
| `pathfind` | Deterministic A*/Theta\* over a `navmap` snapshot → waypoint path + total cost | core, navmap | L2 |
| `gates` | `Gate` registry, eval (boolean visibility predicate trees; AND across matching gates); currently the home of the §6-DSL boolean (`GateExpr`) evaluator | core, stats | L2 |
| `needs` | Need dimension catalog + rates (from balance.yaml), decay, forward-roll helpers | core, stats | L2 |
| `tom` | `Belief` (incl. self), reputation distribution, gossip, initial estimates | core, stats, rng | L2 |
| `actions` | Catalog, tags, effect, Duration, `Producers` | core, stats, gates | L3 |
| `values` | Value map, Standing, Salience, appraisal | core, stats, needs, tom | L3 |
| `perception` | Three senses (Sight LoS, Smell gradient, Hearing) | core, spatial | L3 |
| `planner` | HTN + GOAP, forward-sim, budget, gate application, **tag-derived cost** (+ path-cost term for locomotion) | core, actions, gates, needs, values, (navmap/pathfind for cost) | L4 |
| `agent` | Decision loop, durative execution, coping; path-cost `bindTarget` + waypoint-following MoveTo/Approach | core, stats, needs, values, tom, planner, perception, navmap, pathfind | L5 |
| `world` | Orchestrator: spawn, tick, intent collection, apply + conflict resolution; **owns navmap** (wear deposit on traversal, decay per tick, `SetTerrain` on climate transitions) **and climate** (drives `climate.Step` on the `tick % N` cadence, maps transition cells → navmap cells) | core, spatial, worldtime, rng, agent, actions, navmap, climate, (events iface) | L6 |

## 3. Platform modules
| Module | Purpose | Depends on |
|--------|---------|-----------|
| `config` | Load `content/` data → registries (stats, needs, actions, gates, object/item/terrain catalog, **climate transition table + rates**); validate vs `content/schema/` + referential integrity (incl. climate `from`/`to`/`when`-operand cross-check) | core, stats, actions, gates, needs |
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
      ├─ stats ──┬── gates ──┐      │          │
      │          ├── needs ──┼── planner ── agent ── world ── persist
      │          └── tom ────┤      │           │        │
      │              values ─┘──────┘           │        events(iface)
rng ──┬───────────────────────────────────── world
      └─ climate                              world
content ── config ── (stats/actions/gates/needs/terrain/climate registries)
```
No cycles. Note: `values → tom` is one-way (for Other-referent evaluation); `tom` does not know `values`. `perception` does not import `world`; it operates on a passed-in world view. `tom` also depends on `rng` (injected seeded generator for the calibrated self-belief at construction). `navmap → pathfind` is one-way; `pathfind` is a pure query over a navmap snapshot and never mutates it (wear deposit/decay/SetTerrain is owned by `world`). **`climate` (core, rng) is one-way too: it returns a terrain-transition cell list and `world` performs the `navmap.SetTerrain` write — `climate` never imports `navmap`, `worldtime`, or `gates` (world supplies the time-derived forcing). This keeps `world` the single navmap mutator.** The §6-DSL boolean evaluator climate's `Rules` conditions use is today in `gates`; reusing it in `climate` requires lifting it to `core` (or compiling conditions in `platform/config`) — the one open escalation, see `docs/climate.md`.

## 5. Leaf-first build order (topological)
```
1) core, rng
2) worldtime, spatial, stats, navmap, climate
3) gates, needs, tom, pathfind
4) actions, values, perception
5) planner
6) agent
7) world
8) config, events, persist
9) api (full + NewSSE), main.go (bdg-backend), cmd/sse (bdg-sse)
10) (later) frontend
```
Each stage depends only on the **public interfaces (SPECs)** of earlier stages. The implementer never reads a sibling's implementation. `navmap` and `climate` are independent L1 leaves in stage 2 (climate depends only on `core` + `rng`); their integration (cadence + `SetTerrain` bridge) lands later in `world` (stage 7). *Caveat:* climate's condition evaluation (M2) is gated on the §6-Formula-evaluator-home decision (escalation, `docs/climate.md`); `navmap.SetTerrain` (M1) and world wiring with an empty rule table (M3) are not.

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
  **footprint** the world stamps into `navmap` (walls block, doors are portals).
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
Adding a stat / need / action / gate / object / terrain / climate-rule = **a data file + passing schema**, with no code change.
`gates.yaml` is also the **contract** for the (unbuilt) `engine/gates` evaluator — see `content/README.md`.
