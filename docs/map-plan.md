# Map / Navigation — Implementation Plan

Concept & rationale: `docs/design.md §5`. New leaf SPECs: `backend/engine/space/navmap/SPEC.md`,
`backend/engine/space/pathfind/SPEC.md`. This file is the **roadmap**: phasing, per-module integration
deltas, determinism/perf, serialization, frontend, and open questions. It does not restate the SPECs.

## 0. Decisions locked
- **Substrate** — navigation **cost field on a grid _index_** (not a tiled world). Continuous agent
  positions preserved (D11 supplement, `design.md §5` + `CLAUDE.md` D11). **Index cells are now flat-top
  HEXAGONS** (axial `q,r`) for terrain-render aesthetics — see `docs/hex-grid.md` (surgical scope:
  navmap+pathfind+layout+wire+frontend hex; spatial/scent/climate stay square). Hex is a cell *shape*,
  still an index — D11 intact.
- **Roads = emergent `navmap.wear`**, a sparse cell-keyed decaying field — **not** content objects and
  **not** a separate object system. The "road = TTL object" idea is absorbed as the decay/refresh
  semantics of `wear` (use → `+WearOnUse`, idle → `Decay`, pave → `+WearOnPave`).
- **Paving = Degree 1 (in scope) + social side-effects (in scope); Degree 2 parked.**
  - In scope: emergent `wear` from traffic + an **opportunistic `Pave`** action, plus its **social
    side-effects** — `Pave`/`Build` are `cooperative` deeds that shift reputation (ToM, D6) and feed
    gossip, so Standing-valuing agents may be drawn to them (desire-driven, D1).
  - **Parked frontier:** Degree 2 = deliberate ROI paving as a planner-forward-provisioned sub-goal
    ("provision the path like food", D9-analogous). Needs the planner to value a delayed/indirect
    payoff — a planner *capability* extension, documented in §6, not designed here.

## 1. Current baseline (post-navigation-fix, already shipped)
The straight-line navigation pieces these phases build on already exist:
- `agent.execute`/`moveDestination` bind `Intent.Move` (MoveTo → next object-step pos; Approach →
  nearest agent). `isLocomotion(Produces)` = produces `at_target`|`near_other`.
- `world` steps `Pos` a `MoveSpeedPerTick` fraction toward `Intent.Move`; **arrival-based completion**
  within `cfg.ArrivalEpsilon`; distance-derived safety cap.
- `agent.bindTarget` picks the **Euclidean**-nearest object.

The map work **replaces the straight line with a route** and **Euclidean with path cost** — the
intent/completion/cap machinery stays.

## 2. Phases (each independently shippable; tests + determinism golden per phase)

### M1 — Pathfinding plumbing (uniform cost, no terrain/walls/wear yet)
- Build `engine/space/navmap` (uniform `BaseCost=1`, everywhere passable) + `engine/space/pathfind` (A* + string-pull).
- `world` constructs a `NavMap` at `New`, exposes a snapshot to the plan phase (alongside `currentSnap`).
- `agent.execute`: for a locomotion step, call `pathfind.Path(start→destination)`; set `Intent.Move`
  to the **next waypoint** (not the final goal). Advance to the next waypoint on arrival; the action
  completes when the **final** waypoint is reached.
- Net behaviour on a uniform field ≈ today's straight line — proves the pipeline end-to-end without
  changing outcomes. Determinism golden must hold (snapshot byte-identical to a pre-M1 uniform run, or
  a re-baselined golden).

### M2 — Terrain (river / mountain): cost + capability gates
- `content/terrain.yaml` (**바탕재료=속성 프리셋** `{grainSize·moisture·temperature·depth·slope·salinity}`; cost·passability·gates는 per-type 상수가 아니라 **속성 위 §6 수식**, `design.md §5`) + per-run layout (`map.yaml`
  region polygons → a `terrainAt(Vec2)` sampler). `platform/config` loads + validates (schema).
- `navmap.New` consumes the sampler/types. `pathfind` honours `Caps` (agent's traversal tags).
- `content/actions.yaml`: add `Swim` (tag `terrain:water`, gated by Strength/Agility) so a river is
  either crossed (capable agent) or routed around. Mountains = high `BaseCost` (slow) or `Passable:false`.
- `agent` derives `Caps` from its visible/available action tags. Agents now detour around mountains
  and either ford or circle rivers.

### M3 — Buildings: walls / houses / doors
- `content/objects.yaml`: building object_kinds carry a **footprint** (cells relative to Pos) +
  door cells. `world` `StampFootprint`s on placement, un-stamps on removal.
- `Build` (exists) extended to instantiate a building object → settlements emerge (D2; no city-planner).
- `pathfind` already routes around impassable footprints and through door portals (M1 capability).
- "New object ⇒ reroute": placing a building invalidates nothing in pathfind (it reads live snapshot);
  agents replan next tick; trails crossing the new footprint fade via decay.

### M4 — Desire paths (wear) + opportunistic Pave  [Degree 1]
- `navmap` wear field active: `world` `Deposit`s along the cells each mover crossed (apply phase,
  serial), `Decay()` once per tick (post-apply). `StepCost` = `base × f(wear)`.
- `content/actions.yaml`: `Pave` — a durative `cooperative` action depositing `WearOnPave` (≫ use) on
  the agent's current cell/segment. Selected **opportunistically** (e.g. while on a high-traffic worn
  cell), not via ROI foresight (that is Degree 2, §6).
- Emergent trails between frequented resource/social clusters; unused trails fade; paved roads persist.

### M4b — Social side-effects of deeds (reputation + gossip)  [reuses tom/Signal]
- Tag `Pave`/`Build` `cooperative`/public-good. Add an **observed-deed → ToM fold**: an agent that
  perceives another performing a cooperative public-good action nudges `ToM[actor]` upward (reputation
  distribution shifts, D6). Generalizes today's interaction-only (trade/vote) ToM updates.
- Add a **deed gossip topic** so "X paved/built" propagates as hearsay through the existing Signal
  path, amplifying reputation beyond eyewitnesses.
- Result: Standing-valuing agents have an emergent, desire-driven reason to do public works. Reputation
  is a *consequence others observe*, never the actor's stored goal (D1/D6). Touches `engine/mind/tom`,
  `engine/mind/perception`/`agent`, and the Signal system — **verify/generalize before building**.

### M5 — Serialization + frontend
- `data-contracts.md`: navmap snapshot = terrain layout (**now dynamic** — terrain carries moisture/transition state per `design.md §5`, so it streams as periodic full + sparse **deltas like wear**, NOT static-once) + building footprints +
  **sparse `ActiveWear()`** (dynamic). Stream wear as periodic full + deltas, not every cell every tick.
- Frontend `canvasRenderer`: terrain regions (river=blue, mountain=grey, plain=parchment), buildings
  (walls/houses/doors), and a **trail heatmap** from active-wear cells. Extends the existing object
  renderer (`WorldCanvas`/`drawObjects`).

## 3. Per-module integration deltas (what changes, beyond the new leaves)

| Module | Change |
|--------|--------|
| `agent` (`bindTarget`) | rank candidate objects by `pathfind.EstimateCost` (cheapest-to-reach), not `Pos.Distance`. Cache per tick (§perf). |
| `agent` (`execute`/`moveDestination`) | locomotion target = next **waypoint** from a cached `pathfind.Path`; recompute path on goal/target change or when the cached path is invalidated. |
| `agent` (`Caps`) | derive traversal tags from available actions so `pathfind`/terrain gates know if it can Swim, etc. |
| `planner` (cost) | MoveTo/locomotion gets a **path-cost term** added to its tag-derived cost (D4: a cost *term*, composed — not a bespoke per-action function). Uses `EstimateCost` since the planner picks actions abstractly; exact route cost lands at execution. (P1: keep planner abstract, let bindTarget + execution carry path cost; promote to planner cost later.) |
| `world` (tick) | owns the `NavMap`; freezes a navmap snapshot in phase-1 read; deposits wear along traversed cells in phase-4 apply (serial, D12); `Decay()` in post-apply; `StampFootprint` on Build/RemoveObject. |
| `actions` (content) | `Swim` (M2), `Pave` (M4, `cooperative`); `Build` extended (M3, `cooperative`). All tag-driven (D4), data-only (D10). |
| `tom` / `perception` / `agent` (M4b) | **observed-deed → ToM fold**: perceiving a `cooperative` public-good act nudges `ToM[actor]` up (reputation D6), generalizing today's trade/vote-only updates. |
| Signal / gossip (M4b) | **deed gossip topic** so "X paved/built" propagates as hearsay, amplifying reputation beyond eyewitnesses. |
| `content` + schema | `terrain.yaml` + schema; `map.yaml` (layout); `objects.yaml` building footprints + schema. |
| `platform/config` | load/validate terrain types + map layout → build the `terrainAt` sampler the world passes to `navmap.New`. |
| `platform/persist` + `data-contracts` | navmap wire format (static terrain once; sparse wear delta stream). |
| frontend | terrain + buildings + wear heatmap rendering. |

## 4. Determinism (D12) — must hold every phase
- Pathfinding: fixed neighbour order + cell-ID tie-break; admissible heuristic; bounded expansions.
- Wear deposit: apply phase is already serial in sorted agent-ID order → accumulation order fixed.
- Decay: single pass over **sorted** cells; sparse-map never iterated for logic.
- Plan phase reads a **navmap snapshot** (copy-on-write), never the live field being mutated in apply.
- Re-baseline golden snapshots at each phase that changes outcomes (M1 may be outcome-neutral on a
  uniform field; M2+ will shift them — expected, like the navigation-fix re-baseline).

## 5. Performance
- `pathfind` per agent per replan is the hot path. Mitigations: cache the path on the agent (recompute
  only on goal/target change or path invalidation), `EstimateCost` (cheap) for bindTarget ranking,
  bounded expansions, moderate cell size.
- navmap snapshot per tick: copy-on-write / double-buffer; only the **sparse wear** changes most ticks
  (terrain/footprints are near-static), so snapshot cost ∝ active-trail size, not world area.
- Stream sparse wear deltas, not the dense grid.

## 6. Open questions (resolve before implementing the relevant phase)
- **`Pave` = instrumental sub-goal, NOT a new terminal value (DECIDED).** Paving serves an agent's
  *existing* values: repeated need satisfaction (e.g. trading with a distant partner) → repeated
  traversal of the same corridor → accumulated travel cost → "provision the path so future plans are
  cheaper." This is **D9 applied to path cost instead of need supply** ("provision the path like food").
  No new Dimension. Two degrees, with very different cost:
  - **Degree 1 — emergent (ships in M4 as planned).** Traffic *is* wear, so a frequently-used route
    gets cheaper automatically — a commons that provisions itself, no decision required. Explicit `Pave`
    is an opportunistic action (deposit `WearOnPave` while on a worn cell). Delivers ~80% of the vision,
    D1/D2-pure.
  - **Degree 2 — deliberate ROI (planner frontier, bigger than the map). PARKED (decided).** Would
    insert `Pave` as a **forward-provisioned sub-goal** when `predicted_traversals × Δcost > pave_cost`,
    requiring the planner to value a **delayed, indirect payoff** (route-demand forward-sim) — GOAP/HTN
    optimizes only the *current* plan today. This is a planner *capability* extension parallel to D9
    need-provisioning; **not designed into these SPECs**. The same delayed-payoff machinery would later
    also enable deliberate "Pave for Standing". Revisit after the map ships.
- **Cell size** — reuse spatial 8.0 vs finer (path fidelity ↔ memory/stream). Default: `Config.CellSize`, start at 8.0.
- **A* vs Theta\*** — any-angle paths vs LoS cost. Default A*+string-pull for P1.
- **Map layout source** — procedural world-gen vs authored `map.yaml` fixtures vs both. Likely both
  (fixtures for scenario tests, world-gen for live).
- **Terrain in the render snapshot** — terrain is now **dynamic** (moisture/transition, `design.md §5` + `docs/climate.md`): stream as periodic full + sparse deltas like wear, not static-once. Open: full-vs-delta cadence.
- **Toll / deliberate paving needs an economy** — "pave → charge for use → block for profit" requires **money · ownership · private-property** primitives that **don't exist yet** (`Inventory` does; money/ownership don't). New design thread; parks alongside Degree 2 above.

## 7. Build order (slots into `docs/architecture.md §5`)
`navmap` joins stage 2 (with spatial), `pathfind` stage 3. Then the integration deltas land in
`planner` (5) → `agent` (6) → `world` (7) → `config`/`persist` (8) → frontend (10), phase by phase.
