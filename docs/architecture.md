# Architecture — Module Dependency DAG & Build Order

## 1. Top-level split
- `backend/engine/` — **pure deterministic simulation. No IO.** (headless, NFR-6)
- `backend/platform/` — **IO / infra.** Redis, Postgres, SSE, config loading.
- `content/` — data-driven content (stats, actions, gates, balance) + schema. `platform/config` loads it into registries.

The engine does not import platform. Platform serializes and transports engine state. Events are emitted by the engine through an **interface** that platform implements (dependency inversion).

## 2. Engine modules
| Module | Purpose | Depends on (paths) | Leaf level |
|--------|---------|--------------------|-----------|
| `core` | Types: `StatID`, `Tag`, `Pred`, `Referent`, `Vec2`, `GameMinutes`, cross-cutting interfaces | — | **L0** |
| `rng` | Deterministic seeded RNG wrapper | — | **L0** |
| `worldtime` | tick ↔ game-minute conversion, 12× scale | core | L1 |
| `spatial` | Free coordinates + spatial hash, radius queries | core | L1 |
| `stats` | `Stats` vector + `StatRegistry` | core | L1 |
| `gates` | `Gate` registry, eval (visibility AND, costMod product) | core, stats | L2 |
| `needs` | Need dimensions, rates, decay, forward-roll helpers | core, stats | L2 |
| `tom` | `Belief` (incl. self), reputation distribution, gossip, initial estimates | core, stats | L2 |
| `actions` | Catalog, tags, effect, Duration, `Producers` | core, stats, gates | L3 |
| `values` | Value map, Standing, Salience, appraisal | core, stats, needs, tom | L3 |
| `perception` | Three senses (Sight LoS, Smell gradient, Hearing) | core, spatial | L3 |
| `planner` | HTN + GOAP, forward-sim, budget, gate application | core, actions, gates, needs, values | L4 |
| `agent` | Decision loop, durative execution, coping | core, stats, needs, values, tom, planner, perception | L5 |
| `world` | Orchestrator: spawn, tick, intent collection, apply + conflict resolution | core, spatial, worldtime, rng, agent, actions, (events iface) | L6 |

## 3. Platform modules
| Module | Purpose | Depends on |
|--------|---------|-----------|
| `config` | Load `content/` data → registries (stats, needs, actions, gates, object/item catalog); validate vs `content/schema/` + referential integrity | core, stats, actions, gates, needs |
| `events` | why-trace + event stream (implements the engine's events interface) | core |
| `persist` | Snapshot serialization, Redis live, Postgres backup | core, world (state types), data-contracts |
| `api` (later) | SSE endpoint | events, persist |

## 4. Dependency DAG
```
core ─┬─ worldtime ─┐
      ├─ spatial ───┼─ perception ─┐
      ├─ stats ──┬── gates ──┐      │
      │          ├── needs ──┼── planner ── agent ── world ── persist
      │          └── tom ────┤      │           │        │
      │              values ─┘──────┘           │        events(iface)
rng ──────────────────────────────────────── world
content ── config ── (stats/actions/gates/needs registries)
```
No cycles. Note: `values → tom` is one-way (for Other-referent evaluation); `tom` does not know `values`. `perception` does not import `world`; it operates on a passed-in world view.

## 5. Leaf-first build order (topological)
```
1) core, rng
2) worldtime, spatial, stats
3) gates, needs, tom
4) actions, values, perception
5) planner
6) agent
7) world
8) config, events, persist
9) (later) api/sse, frontend
```
Each stage depends only on the **public interfaces (SPECs)** of earlier stages. The implementer never reads a sibling's implementation.

## 6. Module SPEC location
Each module gets `backend/engine/<m>/SPEC.md` or `backend/platform/<m>/SPEC.md`.
spec-architect generates SPECs leaf-first along this DAG. A module exceeding ~400 lines is split into sub-folders, each with its own SPEC.

## 7. Content boundary (D10)
`content/stats.yaml · needs.yaml · objects.yaml · actions.yaml · gates.yaml · balance.yaml`
(+ `content/schema/` + `content/README.md`).
- `needs.yaml` — need/value Dimensions + per-need `rate` only (D9: demand is derived, never authored).
- `objects.yaml` — object_kinds + item_kinds carrying their **supply** Effect (D9). Placement/counts
  are per-run world-gen / fixtures, not content.
Engine code is **content-agnostic** — `config` loads these at startup to populate registries.
Adding a stat / need / action / gate / object = **a data file + passing schema**, with no code change.
`gates.yaml` is also the **contract** for the (unbuilt) `engine/gates` evaluator — see `content/README.md`.