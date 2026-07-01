# SPEC — `frontend`

> Status: `DRAFT`
> Leaf level: `L10` (depends only on the HTTP boundary of `platform/api` — SSE + REST endpoints)
> Language: **TypeScript + React 19** (Vite). The viewer is a component tree, not vanilla DOM.

## Purpose

A **god's-eye simulation viewer**: connect to the running backend via SSE and REST, render the live
world state in a browser tab, and expose a filterable event log. Read-only observer — no simulation
controls beyond a local view pause, no auth beyond same-origin. The goal is to watch the **social
layer** (agents wander, gossip, form reliance clusters, shift power) AND the **ecosystem layer**
(animals graze/flee/hunt, flora grow/spread, weather + wind + day-night shift) in real time, on one
2D map plus a scrolling narrative log.

> **Implementation status (read before editing):** the **data/state layer is built and ahead of the
> old SPEC** — `src/hooks/useWorld.ts` already reduces the env-inclusive `WorldFrame` into
> `WorldState.animals` / `flora` / `climate`, and `src/types.ts` carries every env shape. The **render
> layer is partial**: `src/utils/canvasRenderer.ts` + `WorldCanvas.tsx` draw terrain + placed objects +
> agents only. **Drawing animals, live flora, wind, day-night/temperature ambient, and rain — and
> wiring those state slices into `WorldCanvas` — is the remaining work this SPEC specifies (§Ecosystem
> rendering).** Backend `WorldFrame`/env keys are WI-P4 (data-contracts §4/§10); until they stream,
> `animals`/`flora`/`climate` stay empty and the viewer renders exactly as today (neutral).

## Backend Contract (what the frontend reads)

All data comes from `platform/api` endpoints (`backend/platform/api/SPEC.md`). The frontend never
reads Redis or Postgres directly and never sends god-view (`?god=true`).

### SSE stream — `GET /sse`
Primary real-time channel. Each message is one `core.Event` (data-contracts §4):
```
data: {"schema_version":1,"tick":42,"seq":0,"agent_id":"farmer_1","type":"ActionDone","payload":{...}}\n\n
```
`type` selects how the event is reduced + rendered. `src/hooks/useWorld.ts` (`applyEvent`) +
`src/utils/eventFormatter.ts` (`formatEvent`) handle these; unknown types log as raw JSON and are
otherwise ignored.

| `type` | Payload (gist) | Treatment |
|---|---|---|
| `TickDone` | `tick, agents[]{id,pos,goal,action,mood}` | Per-tick **agent** render frame: merge agent pos/goal/mood/action; advance tick. Returns early (no log line) |
| `WorldFrame` | env frame (below) | **Env render frame** (WI-P4): merge agent + animal positions, flora stage deltas, terrain deltas, ambient weather. Returns early (no log line) |
| `GoalSelected` | `dimension, target, eff_value` | Set agent goal; log "🎯 farmer_1 → Satiety" |
| `PlanBuilt` | `steps[], total_cost, provisioned[]` | Log (detail) |
| `ActionStarted` / `ActionDone` | `action, pos? / action, result` | Move agent dot (if `pos`); log |
| `Interacted` | `with, signal_kind, claimed_value, accepted` | Log "💬 farmer_1 → guard_1 (gossip)" |
| `BeliefUpdated` | `about, stat, old, new, cause` | Mood drift; log (detail) |
| `ReputationGossip` | `about, from, trust, delta` | Log "🗣 …" |
| `RoleEmerged` | `function, holder, reliance_share` | Update `roles`, tag holder cluster; banner/log "👑 guard_1 Safety holder" |
| `CopingEntered` | `mode` | Set coping mode; log "😶 → Apathy" |
| `Decayed` / `Crafted` / `Mined` / `ToolBroke` | (Materials, data-contracts §4) | Log line |
| `AnimalBorn` / `AnimalDied` | `object_id, species, pos / cause` | Add / remove an animal; log (WI-P4) |
| `PlantSpawned` / `PlantDied` | `object_id, species, pos` | Add / remove a plant; log (WI-P4) |

### Env render channel — `WorldFrame` (WI-P4, data-contracts §4/§10)
Once the env subsystem is installed backend-side, the world streams a periodic **`WorldFrame`** — the
graphics frame carrying agent + animal positions/actions, flora **stage** deltas, terrain deltas, and
the ambient weather. Wire shape (snake_case; `WorldFramePayload` in `src/types.ts`):
```ts
{ tick, hour_of_day, day_night:'day'|'night', temperature, apparent_temp?, raining,
  wind:{dir, mag},
  agents:[{id, pos, action}],
  animals:[{id, pos, species, action, heading}],
  flora_delta:[{id, pos, stage}],
  terrain_delta:[{cell, terrain?, wear?}] }
```
The reducer stores it into `WorldState.animals` (`Map<id, AnimalState>`), `flora` (`PlantState[]`,
merged by id from the sparse delta), and `climate` (`ClimateState`). **God-view
(`real_stats`/`drives`/`stats`) is NEVER in a `WorldFrame`** — same observation boundary as `TickDone`.
Derived display values arrive ready-to-render: `day_night` (from `hour_of_day`), `stage` (from a
plant's `length`), `apparent_temp` (per-animal, fauna F40 — optional). When env is OFF no `WorldFrame`
is emitted and `animals`/`flora`/`climate` stay empty.

### REST endpoints (initial load)
- `GET /api/snapshot` — full world blob on page load (`loadSnapshot` in `useWorld.ts`): extracts tick,
  agents (`{id,pos,goal,action,mood}`), and placed objects (`{id,kind,pos}`).
- `GET /api/agents/{id}` — optional: a single agent's live state on click.
- `GET` world-geometry (bounds + `pixelsPerUnit`) → `RenderConfig` — sizes the canvas + sprite scale;
  loaded from REST (not SSE), `null` until fetched (drives the data-driven transform, §Canvas).

God-view routes (`/api/god/*`, `?god=true`) are **out of scope for P1** (no `real_stats`, divergence,
reputation, or relations display).

## UI Layout

```
┌───────────────────────────────────────────────────────────┐
│  bdg · tick 42 · 8 agents · 6 fauna · ☀ 14°C · ● LIVE      │  ← Header
├───────────────────────────────┬───────────────────────────┤
│                               │  Sidebar                  │
│        WorldCanvas            │  ┌─ AgentDetail (on click)─┐│
│  · farmer_1 (satiety)         │  └─────────────────────────┘│
│  ▷ deer  (graze)  ← heading   │  EventLog                  │
│  ▷ wolf  (hunt)               │  [filter: all ▾]           │
│  ♣ oak (stage 3)              │  TickDone 42               │
│  ~wind→  ☂rain   ◐dusk        │  🎯 farmer_1 → Satiety     │
└───────────────────────────────┴───────────────────────────┘
```

- **Header** (`components/Header.tsx`): run id, tick, agent count, **fauna count + ambient weather
  chip** (day-night icon, temperature, rain/wind) when `climate` present, connection badge, theme +
  pause toggles.
- **WorldCanvas** (`components/WorldCanvas.tsx`, left, flex-1): HTML5 Canvas, RAF render loop. Draws
  (back→front) terrain → flora → placed objects → animals → agents → ambient FX overlay. Clicking
  selects the nearest agent.
- **Sidebar** (`components/Sidebar.tsx`, right): `AgentDetail` (selected agent) over `EventLog`
  (scrolling, filtered, newest-first, trimmed to 500).

## Module Structure (actual — React/TS)

```
frontend/
  index.html              shell: <div id="root">
  package.json            React 19 + Vite + TS
  vite.config.ts          dev proxy /sse + /api → :8080
  tsconfig*.json
  src/
    main.tsx              entry: ReactDOM.createRoot(...).render(<App/>)
    App.tsx               root: theme state, useWorld(), useSSE(), Header/WorldCanvas/Sidebar layout
    config.ts             API_BASE
    theme.ts              LIGHT / DARK ThemeTokens (colours, fonts, roleColours, glow flag)
    types.ts              all shapes: SimEvent, AgentState, WorldObject, AnimalState, PlantState,
                          ClimateState, TerrainDelta, WorldFramePayload, RenderConfig, LogEntry,
                          WorldState, WorldAction
    hooks/
      useSSE.ts           EventSource wrapper: connect, reconnect backoff, dispatch by type
      useWorld.ts         useReducer state machine (applyEvent + reducer) + loadSnapshot (REST)
    components/
      Header.tsx          status strip (tick / counts / ambient chip / connection / toggles)
      WorldCanvas.tsx     canvas element + RAF loop + ResizeObserver + click hit-test
      Sidebar.tsx         AgentDetail + EventLog container
      EventLog.tsx        scrolling <ul>, filter <select>, trim
      AgentDetail.tsx     selected-agent panel (id, goal, action, mood, cluster, coping)
    utils/
      canvasRenderer.ts   pure draw fns: buildTransform, drawTerrain, drawObjects, drawAgents,
                          hitTestAgent  (+ NEW: drawFlora, drawAnimals, drawAmbient — §Ecosystem)
      eventFormatter.ts   formatEvent(ev) → LogEntry | null  (icon + human summary + variant)
```

State lives in the `useWorld` reducer (single source of truth, passed down as props). No
module-level mutable singletons; no `window.*` stores.

## Key Types (`src/types.ts` — already implemented)

`SimEvent` (mirrors `core.Event`), `AgentState` (`id, pos, goal, action, mood, cluster, copingMode,
role?`), `WorldObject` (`id, kind, pos`), and the env shapes:
```ts
interface AnimalState  { id; pos; species; action; heading /*radians*/; stamina }
interface PlantState   { id; pos; species; stage; width }
interface ClimateState { temperature/*°C*/; apparentTemp|null; moisture; raining;
                         windDir/*rad*/; windMag/*[0,1]*/; hourOfDay; dayNight:'day'|'night'; yearFraction }
interface TerrainDelta { cell:{x,y}; terrain?; wear? }
interface RenderConfig { bounds:{min,max}; pixelsPerUnit }
interface WorldState   { tick; agents:Map; objects[]; roles[]; log[]; connectionStatus;
                         selectedAgentId; paused; food; wood;
                         animals:Map; flora:PlantState[]; climate:ClimateState|null; render:RenderConfig|null }
```

## SSE Connection (`src/hooks/useSSE.ts`)

- Open `new EventSource(\`${API_BASE}/sse\`)`. On `message`: `JSON.parse` → `SimEvent` → `dispatchEvent(ev)`.
- On `open` / first message: `setConnection('live')`. On `error`: `setConnection('reconnecting')`, then
  reconnect after `min(2^retries × 500ms, 30s)` (capped exponential backoff); reset on success.
- Live tail only — no `lastEventID`/replay for P1; a reconnect gap is shown as a break in the log.
- Decode once; read parsed fields directly (no re-stringify for display).

## Canvas Rendering (`src/utils/canvasRenderer.ts` + `WorldCanvas.tsx`)

- **One shared world→canvas transform** (`buildTransform`). Today it auto-fits the bounding box of
  `objects ∪ agents`. **Target:** when `WorldState.render` (RenderConfig) is present, anchor the
  transform to `render.bounds` + `pixelsPerUnit` so the view is stable and **all** entity layers
  (agents, animals, flora, objects) share the same mapping (D11 free 2D — positions map linearly to
  pixels; never tiled).
- **RAF loop** (`WorldCanvas`): redraw when any input slice changes (`dirtyRef` flagged on prop
  change), via `requestAnimationFrame` — never draw inside the SSE handler.
- **Layer order (back→front):** terrain → flora → placed objects → animals → agents → ambient FX.
- **Agent dot** (`drawAgents`): filled circle, colour = role/cluster (`roleColours`), aura, label +
  dashed ring when selected. **Click**: `hitTestAgent` within 15px → select; clear on empty click.
- **Terrain** (`drawTerrain`): currently a **hardcoded decorative** layout (forests/fields/river/
  buildings) keyed off canvas size — a P1 stand-in. **Target:** drive it from `RenderConfig.bounds` +
  the streamed `terrain_delta` (navmap terrain type + `wear` trails), so the map reflects real
  dynamic terrain (climate transitions, emergent paths). Until terrain streams, the decorative layer
  stays.

### Ecosystem rendering (the remaining work — §Implementation status)
`WorldCanvas` must additionally receive `animals`, `flora`, and `climate` from `WorldState` (App.tsx
passes only `agents`/`objects` today — wire the env slices through) and draw them:

- **`drawAnimals(ctx, animals, tr, t)`** — one sprite per `AnimalState`, **oriented by `heading`** (a
  triangle/chevron pointing along the steering direction so flee/hunt motion reads visually).
  Per-species style (colour + glyph) from a `SPECIES_STYLE` map keyed by `species`
  (deer/wolf/rabbit/goat/bear/fish); **predators vs prey visually distinct** (e.g. predators a sharper/
  red-tinted marker). Optional: dim the sprite as `stamina`→0 (a tiring predator reads as "giving
  up"); label with `action` (graze/flee/hunt) at a detail zoom. A despawned animal (`AnimalDied` /
  absent from the next frame) is removed.
- **`drawFlora(ctx, flora, tr, t)`** — one sprite per live `PlantState`, **scaled by `stage`/`width`**
  (seedling → mature), per-species colour (tree/bush/grass green ramp). Plants are dense + static-ish;
  draw them under animals/agents. New plants (`PlantSpawned`) appear, dead ones (`PlantDied`) vanish —
  so regeneration/clustering is visible.
- **`drawAmbient(ctx, climate, W, H, t)`** — the weather/atmosphere overlay from `ClimateState`:
  - **Day-night tint**: a full-canvas overlay whose colour/alpha derives from `dayNight` + `hourOfDay`
    (clear by day, blue-dark at night, warm at dawn/dusk).
  - **Temperature cue**: a cold (blue) ↔ hot (red) edge/vignette tint scaled by `temperature`(°C)
    (sub-zero allowed) — the visual of "추워짐/더워짐" that drives the fauna `thermal` slowdown.
  - **Rain**: an animated dot/streak overlay when `raining`.
  - **Wind**: a HUD arrow (direction `windDir`, length/opacity ∝ `windMag`) + optional faint drifting
    streaks across the field, so "냄새가 바람따라 퍼진다"/upwind homing has a visible cause. Show
    `apparentTemp` next to `temperature` in the Header chip when present.
- **Scent field**: NOT streamed (derived, not serialized — data-contracts §10), so it has no render
  here in P1. A scent-debug overlay would need a dedicated debug endpoint → out of scope (Notes).

## Event Log (`src/components/EventLog.tsx` + `utils/eventFormatter.ts`)

- Scrolling `<ul>`, fixed max height, newest-first. Each entry: `[tick] icon summary` with a `variant`
  (normal/positive/negative/special) for colour. `formatEvent` reads `payload` fields directly
  (canonical glossary names — no translation layer); unknown types → `⁉` raw.
- Filter `<select>`: `all`, `social` (Interacted/Gossip/ReputationGossip), `goals`
  (GoalSelected/PlanBuilt), `roles` (RoleEmerged/CopingEntered), `actions` (ActionStarted/ActionDone),
  **`ecosystem` (AnimalBorn/AnimalDied/PlantSpawned/PlantDied)**. Filter is display-only over the full
  in-memory array.
- Trim: `> 500` entries → slice to 400 (drop oldest). Keep pinned to top when already at top.

## Invariants

- **Read-only.** No POST/PUT/DELETE; the engine is the single writer (D12). The local pause toggle
  only freezes the *view* reducer (`paused` short-circuits `applyEvent`), not the simulation.
- **No god-view for P1.** `?god=true` is never sent; `real_stats`/`drives`/`stats` are never read or
  displayed — not for agents, not for animals. `api.ts`/`loadSnapshot` read only the render subset.
- **Reconnect on failure.** SSE drop → warning badge + capped-backoff retry; never a page reload.
- **Single source of truth.** All shared state is the `useWorld` reducer value, passed as props. No
  `window.*`, no module-level mutable `let` outside pure render helpers.
- **Decode once.** Parse the SSE payload once; format human strings directly from parsed fields.
- **Tick counter only advances.** `tick` only increases (`ev.tick > tick`); out-of-order delivery
  updates state but never decrements the displayed tick.
- **One world transform.** Agents, animals, flora, and objects share ONE `buildTransform` mapping so
  every layer aligns (D11 continuous coords — never snap to a tile).
- **Env-off neutrality.** With no `WorldFrame` streamed, `animals`/`flora`/`climate` stay empty and the
  canvas/Header render exactly as the social-only viewer — adding the env layers changes nothing until
  the backend streams them.

## Acceptance Criteria (testable)

> Vitest unit tests on the reducer + pure render helpers; browser smoke for the canvas (no Playwright for P1).

- [ ] **SSE connect/reconnect** — `useSSE` opens `EventSource("/sse")`; first message → badge `live`;
  on forced error it reopens within 2× the backoff window showing `reconnecting`.
- [ ] **TickDone agent frame** — dispatching `TickDone{agents:[{id,pos,...}]}` updates
  `world.agents[id].pos` and does not append a log line.
- [ ] **WorldFrame env reduce** — dispatching a `WorldFrame` merges `animals` (by id, with
  `heading`/`species`/`action`/`stamina`), `flora` (sparse delta merged by id), and `climate`
  (temperature/wind/day_night/raining); no god-view field is stored even if injected.
- [ ] **Event log filter + trim** — `GoalSelected` shows under `goals`/`all`, not `social`;
  `AnimalDied` shows under `ecosystem`/`all`; adding 600 entries trims to ≤ 500, newest kept.
- [ ] **RoleEmerged** — updates `world.roles` and tags the holder's `cluster`.
- [ ] **Canvas hit-test** — clicking within 15px of an agent shows its detail; elsewhere clears it;
  animals are NOT agent-selectable (distinct layer).
- [ ] **No `real_stats` leak** — a snapshot/event carrying `real_stats`/`drives` does not display them.
- [ ] **One shared transform** — agents, animals, flora, and objects at the same world coord map to the
  same canvas pixel (transform is shared, not per-layer).
- [ ] **drawAnimals orientation + species** — an animal renders a heading-oriented sprite styled by
  `species`; a predator species is visually distinct from a prey species; `stamina→0` dims it.
- [ ] **drawFlora stage scale** — a higher-`stage`/`width` plant renders larger than a seedling; a
  `PlantDied` removes it.
- [ ] **drawAmbient** — `dayNight:'night'` applies a darkening overlay; `raining:true` shows rain;
  `windMag>0` shows a wind arrow oriented to `windDir`; the Header chip shows `temperature`(°C) (+
  `apparentTemp` when present).
- [ ] **Env-off neutrality** — with no `WorldFrame`, the canvas + Header byte-match the social-only
  render (no animal/flora/ambient layers drawn).

## Out of Scope (P2+)

- **God-view mode** (`?god=true`, divergence/reputation/relations, real_stats/drives panels) — needs a
  deploy-flag + auth layer.
- **Time scrubbing / replay** — SSE is live-only for P1.
- **Scent / cost-field / navmap debug overlays** — the scent grid + navmap cost field are derived /
  not streamed (data-contracts §10); a debug visualizer would need dedicated endpoints.
- **Social graph panel** (force-directed `/api/god/relations`) — needs god-view.
- **Player controls / writing to the engine** — the frontend is read-only.
- **Mobile layout** — desktop two-panel only for P1.
- **Authentication / CORS** — handled at the gateway; the frontend is same-origin in production.

## Build & Dev

```bash
cd frontend && npm install
npm run dev      # Vite dev server; proxies /sse + /api/* → http://localhost:8080
npm run build    # static bundle → frontend/dist/ (served by nginx; deploy/k8s ingress)
```

## Notes

- **Why React (not vanilla)?** The viewer grew past the original two-panel sketch: a Header chip, a
  selectable AgentDetail, a filtered EventLog, theming, and the env reducer all benefit from
  component state + a single `useReducer`. The module split keeps render (canvas/util pure fns)
  separate from state (`useWorld`) and transport (`useSSE`).
- **The data layer is ahead of the renderer.** `useWorld` already reduces `WorldFrame` into
  `animals`/`flora`/`climate`; the open work is purely *drawing* them + threading those slices into
  `WorldCanvas`. Keep the reducer the single source of truth; render functions stay pure
  (`state → pixels`), tested in isolation.
- **Ambient FX is the visible cause of fauna behaviour.** Day-night/temperature/wind are not just
  decoration: temperature drives the `thermal` slowdown (fauna F35/F40), wind drives scent spread +
  upwind homing (fauna F33), so rendering them makes the ecosystem legible ("why did the deer slow /
  flee upwind"). Keep the ambient overlay readable, not overwhelming.
- **Cluster colour hashing**: `hash(holder_id) % PALETTE_SIZE`; unclustered = neutral grey.
- **Coordinate fit**: until `RenderConfig.bounds` is fetched, auto-fit the bounding box of known
  positions with padding (degenerate all-at-one-point → a default view). Once bounds are known, anchor
  to them so the map does not jitter as entities move.
- Reference: `backend/platform/api/SPEC.md` (endpoints), `docs/data-contracts.md §2/§4/§10`
  (AgentView / WorldFrame / env keys), `backend/engine/fauna/SPEC.md` (heading/action/species),
  `backend/engine/env/flora/SPEC.md` (stage/width), `backend/engine/env/climate/SPEC.md`
  (temperature/wind/day-night).
