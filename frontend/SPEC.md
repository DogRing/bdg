# SPEC — `frontend`

> Status: `DRAFT`
> Leaf level: `L10` (depends only on the HTTP boundary of `platform/api` — SSE + REST endpoints)
> Language: **TypeScript** (no Go). Bundled with Vite. No framework.

## Purpose

A **god's-eye simulation viewer**: connect to the running backend via SSE and REST, render the live
world state in a browser tab, and expose a filterable event log. This is a read-only observer tool —
no simulation controls, no auth beyond same-origin. The goal is to watch agents wander, gossip,
form reliance clusters, and shift power in real time; a simple 2D map plus a scrolling narrative log
suffices for P1.

## Backend Contract (what the frontend reads)

All data comes from `platform/api` endpoints (see `backend/platform/api/SPEC.md`). Frontend never
reads Redis or Postgres directly.

### SSE stream — `GET /sse`
Primary real-time channel. Each message is:
```
data: {"schema_version":1,"tick":42,"seq":0,"agent_id":"farmer_1","type":"ActionDone","payload":{...}}\n\n
```
The `type` field determines how the event is rendered. The frontend handles these types:

| `type` | Payload fields (gist) | UI treatment |
|---|---|---|
| `TickDone` | `tick` | Advance the tick counter; may trigger a position refresh |
| `GoalSelected` | `dimension, target, eff_value` | Log line: "🎯 farmer_1 → Satiety (berry_bush_1)" |
| `PlanBuilt` | `steps[], total_cost` | Log line (detail level): plan steps |
| `ActionStarted` | `action` | Move dot to destination; log line |
| `ActionDone` | `action, result` | Log line |
| `Interacted` | `with, signal_kind, claimed_value` | Log line: "💬 farmer_1 → guard_1 (gossip)" |
| `BeliefUpdated` | `about, stat, old, new, cause` | Log line (detail level) |
| `ReputationGossip` | `about, from, trust, delta` | Log line: "🗣 farmer_1 heard about guard_1" |
| `RoleEmerged` | `function, holder, reliance_share` | Banner: "👑 guard_1 is now Safety holder" |
| `CopingEntered` | `mode` | Log line: "😶 farmer_1 → Apathy" |
| `WorldFrame` | `tick, hour_of_day, day_night, temperature, apparent_temp?, raining, wind{dir,mag}, agents[]{id,pos,action}, animals[]{id,pos,species,action,heading}, flora_delta[]{id,pos,stage}, terrain_delta[]{cell,terrain,wear}` | Env render frame (WI-P4): move agents + animals, merge flora stage deltas, terrain, ambient weather. Returns early (no log spam) |
| `AnimalBorn` / `AnimalDied` | `object_id, species, pos / cause` | Add / remove an animal sprite; log line (WI-P4) |
| `PlantSpawned` / `PlantDied` | `object_id, species, pos` | Add / remove a plant sprite (WI-P4) |

Unknown `type` values are logged as raw JSON at the detail level and otherwise ignored.

### Env render channel — `WorldFrame` (WI-P4, data-contracts §4/§10)
Once the env subsystem is installed backend-side, the world streams a periodic **`WorldFrame`** — the
graphics frame carrying agent + animal positions/actions, flora **stage** deltas, terrain deltas, and
the ambient weather (`hour_of_day`/`day_night`, `temperature`/`apparent_temp`, `raining`, `wind`). The
frontend stores it into `WorldState.animals` / `flora` / `climate` (`src/types.ts`: `AnimalState`,
`PlantState`, `ClimateState`, `WorldFramePayload`; reducer in `src/hooks/useWorld.ts`). It is the env
analogue of `TickDone` (which carries only agents). **God-view (`real_stats`/drives/stats) is NEVER in
a `WorldFrame`** — same observation boundary as today. Derived display values arrive ready-to-render:
`day_night` (from `hour_of_day`), `stage` (from a plant's length). When env is OFF, no `WorldFrame` is
emitted and `animals`/`flora`/`climate` stay empty — the viewer is unchanged.

`RenderConfig` (`bounds` + `pixelsPerUnit`, from `content/world.yaml`) sizes the canvas + sprite
scale; it loads from REST world-geometry (not SSE) and is `null` until fetched.

### REST endpoints (initial load)
- `GET /api/snapshot` — full world state blob on page load. Extracts agent positions and tick.
- `GET /api/agents/{id}` — optional: fetch a single agent's live state on click.

God-view routes (`/api/god/*`) and `?god=true` are **out of scope for P1**. The frontend never
sends `?god=true` (no `real_stats`, no divergence/reputation/relations). They are P2+ once a
deploy-flag-gated mode is added.

## UI Layout

```
┌─────────────────────────────────────────────────────┐
│  bdg  ·  tick 42  ·  agents 8  ·  ● LIVE          │  ← StatusBar
├───────────────────────────┬─────────────────────────┤
│                           │  EVENT LOG              │
│     2D WORLD CANVAS       │  ─────────────────────  │
│                           │  [filter: all ▾]        │
│  · farmer_1 (satiety)     │  TickDone 42            │
│  · guard_1  (safety)      │  🎯 farmer_1 → Satiety  │
│       ↳ cluster_guard_1   │  💬 farmer_1 → guard_1  │
│  · blacksmith_1 (rest)    │  👑 guard_1 Safety hld  │
│                           │  😶 elder_1 → Apathy    │
│                           │  [load more…]           │
└───────────────────────────┴─────────────────────────┘
```

- **StatusBar** (top strip): run id, tick counter, agent count, connection indicator.
- **WorldCanvas** (left, 70% width): HTML5 Canvas. Agents as labeled dots. Reliance cluster shown as
  a dim hull (convex polygon) around cluster members. Faction colour comes from the cluster holder's
  `faction_id` hash. Clicking a dot opens an agent detail tooltip.
- **EventLog** (right, 30% width): scrolling div. Newest events at top. Filter dropdown: `all`,
  `social` (Interacted/Gossip/ReputationGossip), `goals` (GoalSelected/PlanBuilt), `roles`
  (RoleEmerged/CopingEntered), `actions` (ActionStarted/ActionDone). Max 500 entries; older entries
  are trimmed.

## Module Structure

```
frontend/
  index.html          HTML shell (no framework; single <div id="app">)
  package.json        Vite + typescript devDeps only
  tsconfig.json
  vite.config.ts      base: "/" (prod) or proxy to :8080 (dev)
  src/
    main.ts           Entry: creates modules, wires SSE, triggers snapshot load
    types.ts          TypeScript event + world types (mirror core.Event, AgentView shapes)
    sse.ts            EventSource wrapper: connect, reconnect backoff, dispatch by type
    world.ts          AgentState map, tick counter, role-holder registry — pure state, no DOM
    canvas.ts         WorldCanvas: renders world.ts state onto <canvas>; redraws on world change
    log.ts            EventLog: DOM list, filter, trim — appends from SSE dispatch
    api.ts            fetchSnapshot(url) → WorldSnapshot; fetchAgent(id) → AgentView
    status.ts         StatusBar: tick / agent count / connection badge DOM update
```

## Types (key shapes, `src/types.ts`)

```typescript
// Mirrors core.Event (data-contracts §4)
export interface SimEvent {
  schema_version: number;
  tick: number;
  seq: number;
  agent_id: string | null;
  type: string;
  payload: Record<string, unknown>;
}

// Mirrors persist.AgentView (data-contracts §2)
export interface AgentView {
  id: string;
  pos: { x: number; y: number };
  goal: string;       // dimension string
  action: string;
  mood: number;
}

// Frontend-only: enriched agent state maintained in world.ts
export interface AgentState extends AgentView {
  cluster: string | null;       // "cluster_<holder>" or null
  copingMode: string | null;    // current CopingEntered.mode or null
}

// Role/reliance cluster (from RoleEmerged events)
export interface RoleHolder {
  function: string;
  holder: string;
  relianceShare: number;
}
```

## SSE Connection Logic (`src/sse.ts`)

- Open `new EventSource("/sse")`.
- On `message`: parse JSON → `SimEvent`, dispatch to `world.applyEvent(ev)`, `log.append(ev)`,
  `status.update(ev)`.
- On `error`: set status badge to ⚠ RECONNECTING; wait `min(2^retries × 500ms, 30s)` then
  reconnect (capped exponential backoff). Reset retry counter on successful first message.
- Do **not** pass `?lastEventID`; the stream starts from `$` and the viewer is a live tail only
  (no replay on reconnect for P1 — the event log shows the gap as a timestamp break line).
- `EventSource` is a native browser API; no polyfill required for modern browsers.

## Canvas Rendering (`src/canvas.ts`)

- Coordinate system: the world is free 2D (no tiles, D11). Map agent `pos.x/pos.y` linearly to
  canvas pixels. If the world bounds are not yet known (before snapshot), auto-fit to observed
  positions.
- **Agent dot**: filled circle, radius 8px. Colour = cluster colour (hashed from holder id) or
  grey if unclustered. Label = agent `id` above the dot.
- **Cluster hull**: dim semi-transparent convex hull polygon around all agents in the same cluster.
  Computed on each redraw when cluster membership changes.
- **Goal arrow**: thin line from agent's current pos toward the recorded `goal` dimension direction
  (conceptual, not geographic; skip if no current positional target is known).
- Redraw on: `world.change` event (a custom EventTarget dispatch from world.ts whenever state
  mutates). Use `requestAnimationFrame` — do not redraw in the SSE message handler directly.
- On canvas click: find nearest agent within 15px radius; show tooltip (`id, goal, action, mood,
  cluster`) near the click point. Clear tooltip on next click elsewhere.

## Event Log (`src/log.ts`)

- Render as `<ul>` with fixed max height + `overflow-y: scroll`.
- Each entry: `[tick] icon type: human-readable summary`. Icon is a single emoji per event type
  (see type table above). Unknown types: `⁉`.
- Human-readable summaries are generated in `log.ts`; they read the `payload` fields directly.
  No translation layer needed — the payload is already JSON with canonical names from the glossary.
- Filter: a `<select>` above the list; changing it re-filters the in-memory `entries[]` array
  and re-renders. The array itself is always unfiltered (filter is display-only).
- Trim: if `entries.length > 500`, remove the oldest 100 entries.
- Scroll: when a new entry arrives and the list is already scrolled to the top (newest-first), keep
  it pinned. If the user has scrolled down (reading history), do not auto-scroll.

## Public Interface (none — this is a leaf)

The frontend exposes no API. It is a browser app; the backend is the source of truth. The only
"public interface" is the set of backend endpoints it reads (listed above in Backend Contract).

## Invariants

- **Read-only.** The frontend never sends POST/PUT/DELETE. All mutations go through the simulation
  itself (the engine is the single writer, D12).
- **No god-view for P1.** `?god=true` is never sent; `real_stats` is never displayed. The viewer
  is a mortal observer: it sees `goal`, `action`, `mood`, and social events — not the hidden
  `RealStats`.
- **Reconnect on failure.** If SSE drops, the UI shows a warning and retries. It never requires
  a page reload.
- **No global mutable state.** All shared state lives in `world.ts` (exported singleton). No
  `window.*` stores, no module-level `let` outside the singleton. Canvas and log receive the
  singleton via dependency injection in `main.ts`.
- **SSE payload bytes are not re-serialised.** The frontend decodes once (`JSON.parse`) and reads
  the parsed object. No re-stringification for display (format human strings directly from the
  parsed payload fields).
- **Tick counter only advances.** `world.tick` only increases (or stays the same on out-of-order
  delivery). If `ev.tick < world.tick`, update state but do not decrement the displayed tick.

## Acceptance Criteria (testable)

> P1: all tests are browser smoke-tests / Vitest unit tests (no Playwright for P1).

- [ ] **SSE connect**: `sse.ts` opens `EventSource("/sse")`; on the first `message` event, the
  status badge changes from ⚠ CONNECTING to ● LIVE and the tick counter updates.
- [ ] **SSE reconnect**: close the `EventSource` and verify `sse.ts` reopens it within 2× the
  backoff window; status badge shows ⚠ RECONNECTING during the gap.
- [ ] **Event log filter**: `GoalSelected` events appear under filter `goals` and `all`; they do
  NOT appear under filter `social`.
- [ ] **Event log trim**: adding 600 entries to an EventLog instance trims to ≤ 500 (oldest 100
  removed); the newest entry is still present.
- [ ] **World state — agent position**: dispatching an `ActionStarted` event with `pos` in payload
  updates `world.agents[id].pos`; the canvas re-requests a `requestAnimationFrame` redraw.
- [ ] **RoleEmerged**: dispatching `RoleEmerged{function:"Safety", holder:"guard_1"}` updates
  `world.roles` and assigns `cluster = "cluster_guard_1"` to all agents with `RelyOn.Safety` >0
  on their next `BeliefUpdated` / snapshot refresh.
- [ ] **Canvas hit-test**: clicking within 15px of a known agent's canvas position shows the
  tooltip with the agent's `id`, `goal`, `action`, `mood`; clicking elsewhere clears it.
- [ ] **No `real_stats` leak**: a snapshot blob that contains `real_stats` inside an agent entry
  does not display it; `api.ts` only reads `id`, `pos`, `goal`, `action`, `mood`.

## Out of Scope (P2+)

- **God-view mode** (`?god=true`, divergence/reputation/relations display) — requires a deploy-flag
  and auth layer; deferred.
- **Time scrubbing / replay** — the SSE stream is live-only for P1. A replay mode would require
  `/api/snapshot` history or a separate event-replay endpoint.
- **Social graph panel** (force-directed graph of `/api/god/relations`) — P2; needs god-view.
- **Writing / player controls** — the frontend is read-only; no input to the engine is planned.
- **Mobile layout** — two-panel layout is desktop-only for P1.
- **Authentication / CORS** — handled at the gateway (`platform/auth` / nginx); the frontend is
  same-origin in production.

## Build & Dev

```bash
# install
cd frontend && npm install

# dev server (proxies /sse, /api/* to localhost:8080)
npm run dev

# production build → frontend/dist/
npm run build
```

`vite.config.ts` in dev mode proxies `/sse` and `/api` to `http://localhost:8080` so the backend
can run separately. The production build is a static bundle (`dist/`) served by nginx (see
`deploy/k8s/` for the ingress config).

## Notes

- **Why Vite + vanilla TypeScript, no framework?** The UI is two panels plus a canvas; no routing,
  no forms, no server-side rendering. A framework would add dead weight. Vanilla TS + DOM is the
  minimal viable surface; the module split gives the same separation of concerns as a component
  tree.
- **Why newest-first in the log?** The most interesting events are the latest ones. Oldest-first
  requires constant scrolling; newest-first keeps the interesting content at eye level.
- **Cluster colour hashing**: `hash(holder_id) % PALETTE_SIZE` where `PALETTE` is a set of 8
  distinguishable colours. Agents with no cluster get a neutral grey. The palette is fixed in
  `canvas.ts` (not data-driven) — for P1 there are at most a handful of roles.
- **Coordinate fit**: the world has no declared bounds (D11). The canvas auto-scales to the
  bounding box of all known agent positions, with 10% padding. If all agents are at the same
  point (initial state), show a 200×200 unit default view.
