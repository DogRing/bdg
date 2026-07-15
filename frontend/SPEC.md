# SPEC — `frontend`

> Status: `APPROVED` (ecosystem render phase; plan: `docs/plans/frontend.md`, Q1–Q8 RESOLVED 2026-07-02)
> Leaf level: `L10` (depends only on the HTTP boundary of `platform/api` — SSE + REST endpoints)
> Language: **TypeScript + React 19** (Vite). The viewer is a component tree, not vanilla DOM.
> Children (referenced, not restated): [`src/gl/SPEC.md`](src/gl/SPEC.md) (**primary 3D renderer**) ·
> [`src/render/SPEC.md`](src/render/SPEC.md) (2D library behind the Minimap) ·
> [`src/assets/SPEC.md`](src/assets/SPEC.md) · [`dev/SPEC.md`](dev/SPEC.md)

## Purpose

A **god's-eye simulation viewer**: connect to the running backend via SSE and REST, render the live
world state in a browser tab, and expose a filterable event log. Read-only observer — no simulation
controls beyond a local view pause, no auth beyond same-origin. The goal is to watch the **social
layer** (agents wander, gossip, form reliance clusters, shift power) AND the **ecosystem layer**
(river/grass/forest terrain, plants growing and dying, animals grazing / fleeing / hunting with
visible **movement, attack, and death motion**, weather + wind + day-night) in real time, in a
**3D curved-world view** (`src/gl`) plus a scrolling narrative log.

> **Viewer: 3D main view + 2D minimap (2026-07-15).** The single full-window view is the WebGL
> **curved-world** renderer (`WorldCanvas3D` + `src/gl`, `src/gl/SPEC.md`). The former flat top-down
> **2D `WorldCanvas`** full view is **removed** (its `src/render` draw library is retained); the 2D
> layer now lives on as a small **bottom-right `Minimap`** overlay (`components/Minimap.tsx`) — a
> whole-world overview drawn through the same pure render helpers, showing terrain + entity dots + a
> marker for where the 3D camera is looking. There is no 2D/3D toggle. Decisions + rationale:
> `docs/plans/frontend.md` §10–§11.

> **Implementation status:** the data/state layer (types + `useWorld` reducer + `useSSE`) is built.
> The render layer is the current work — specified across this file (state/UI contract) and the
> child SPECs (`src/render` draw/camera/motion, `src/assets` manifest/spritesheets, `dev` mock +
> asset generator). The hardcoded decorative terrain mockup in the old `utils/canvasRenderer.ts` is
> **deleted** by this phase. Backend `AgentFrame`/`WorldFrame` emission (WI-P4) and
> `GET /api/terrain` are live; `dev/mock-server.mjs` remains the contract-parity offline harness.
> An env-off backend still renders social-only (env-off neutrality below).

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
| `AgentFrame` | `tick, agents[]{id,pos?,goal?,mood?,action?}, removed[]` | Per-tick **sparse agent delta** (replaced the removed full-roster `TickDone`): merge only the fields present onto the known agent (REST snapshot = the baseline), delete `removed[]` ids; advance tick. Returns early (no log line) |
| `StreamGap` | `reason` | **Transport control frame**: bump `baselineRetries`, set `snapshotLoaded:false` (closing SSE), and re-arm `floraLoaded:false`; retain cursor-tagged `pendingFloraOps` until the fresh snapshot and a flora baseline at/after its cursor arrive. Never logged/rendered. |
| `WorldFrame` | env frame (below) | **Env render frame** (WI-P4): merge animal positions (keeping prev values for interpolation), **`flora_delta` full-row upserts by id** (grow FX on stage increase) — buffered into `pendingFloraOps` while `!floraLoaded` (§Bootstrap step 7), terrain deltas, ambient weather. Carries NO agents — agent state is `AgentFrame`'s. Returns early (no log line) |
| `GoalSelected` | `dimension, target, eff_value` | Set agent goal; log "🎯 farmer_1 → Satiety" |
| `PlanBuilt` | `steps[], total_cost, provisioned[]` | Log (detail) |
| `ActionStarted` / `ActionDone` | `action, pos? / action, result` | Move agent dot (if `pos`); log |
| `Interacted` | `with, signal_kind, claimed_value, accepted` | Log "💬 farmer_1 → guard_1 (gossip)" |
| `BeliefUpdated` | `about, stat, old, new, cause` | Mood drift; log (detail) |
| `ReputationGossip` | `about, from, trust, delta` | Log "🗣 …" |
| `RoleEmerged` | `function, holder, reliance_share` | Update `roles`, tag holder cluster; banner/log "👑 guard_1 Safety holder" |
| `CopingEntered` | `mode` | Set coping mode; log "😶 → Apathy" |
| `Decayed` / `Crafted` / `Mined` / `ToolBroke` | (Materials, data-contracts §4) | Log line |
| `AnimalBorn` / `AnimalDied` | `object_id, species, pos / cause` | Add / remove an animal **+ enqueue spawn / death FX**; log (WI-P4) |
| `PlantSpawned` / `PlantDied` | `object_id, species, pos` | `PlantSpawned` = **enqueue spawn FX only** (render STATE is owned by the authoritative `flora_delta` upsert, which carries `width`; the spawn's paired `flora_delta` row arrives the same tick). `PlantDied` = **remove the plant + enqueue death FX** — the removal is buffered into `pendingFloraOps` while `!floraLoaded` (§Bootstrap step 7; FX fire immediately either way); log (WI-P4) |

### Env render channel — `WorldFrame` (WI-P4, data-contracts §4/§10)
Once the env subsystem is installed backend-side, the world streams a periodic **`WorldFrame`** —
the graphics frame carrying animal positions/actions, flora **stage** deltas, terrain
deltas, and the ambient weather. Wire shape (snake_case; `WorldFramePayload` in `src/types.ts`):
```ts
{ tick, day_of_run, hour_of_day, minute_of_day, day_night:'day'|'night', temperature, apparent_temp?, raining,
  wind:{dir, mag},
  animals:[{id, pos, species, action, heading}],
  flora_delta:[{id, species, pos, stage, width}],   // each entry a FULL render row → upsert by id
  terrain_delta:[{cell, terrain?, wear?}] }
```
The reducer stores it into `WorldState.animals` / `flora` / `climate` / `terrain`, and — for motion
— shifts each entity's previous `pos/heading` + arrival timestamp into `prevPos/prevHeading/
prevFrameAtMs` so the render layer can interpolate between frames at any streaming cadence
(`src/render/SPEC.md` §animator). **God-view (`real_stats`/`drives`/`stats`) is NEVER in a
`WorldFrame`.** Derived display values arrive ready-to-render: `day_night`, `stage`,
`apparent_temp`. The in-world calendar rides along too — `day_of_run` (0-based day index since run
start) + `minute_of_day` ([0,DayMinutes)) — driving the top-right date/clock HUD (`StatusHud`) live
each frame. When env is OFF no `WorldFrame` is emitted and the env slices stay empty.

### REST endpoints (initial load)
- `GET /api/snapshot` — full world blob (`loadSnapshot` in `useWorld.ts`): tick,
  agents (`{id,pos,goal,action,mood}`), placed objects (`{id,kind,pos}`), plus the publication
  wrapper `world_revision` / `stream_cursor` / `terrain:"on"|"off"` (data-contracts §1; absent
  on legacy/mock backends). **The required baseline** for applying `AgentFrame` deltas
  (§Bootstrap below).
- `GET /api/meta` — the published run metadata (`{tick, schema_version, started_at, status,
  world_revision?, terrain?}`, all strings): the §New-map flow's readiness marker. Not used by
  the regular bootstrap (the snapshot self-identifies).
- `GET /api/terrain` — **initial terrain grid** (plan Q5 · hex `docs/plans/hex-grid.md`; *pending backend
  SPEC delta on `platform/api`*): `{cell_size, orientation:'flat', size:{cols,rows}, terrain:[…],
  wear?:[…]}` — an **offset (col,row) rectangular array** (`i = row·cols + col`) of flat-top hex cells;
  fetched once at connect, kept current by SSE `terrain_delta`. Until the endpoint exists, `terrain`
  stays `null` and no terrain layer draws.
- `GET /api/flora` — **flora baseline** (`persist.FloraDoc`, data-contracts §2): `{world_revision, stream_cursor,
  flora:[{object_id, species, pos, stage, width}]}` — the plants present before connect (fixtures +
  already-propagated) that no SSE event replays; fetched once per revision after the snapshot, kept
  current by SSE `flora_delta`/`PlantSpawned`/`PlantDied`. `404` ⇒ flora-off; `flora:[]` ⇒
  installed-but-empty. Applied as an authoritative replacement (§Bootstrap step 7).
- `GET /api/agents/{id}` — optional: a single agent's live state on click.
- World-geometry (bounds + `pixelsPerUnit`) → `RenderConfig` — anchors the initial camera;
  `null` until fetched (auto-fit fallback). Source: DERIVED from the `GET /api/terrain` grid
  (`bounds` = the flat-top hex pixel extent of `cols×rows` at `cell_size`) on `TERRAIN_LOADED` — the terrain grid IS the
  world geometry; a future dedicated geometry endpoint may supersede it (the reducer never
  clobbers an already-present config).

God-view routes (`/api/god/*`, `?god=true`) are **out of scope** (no `real_stats`, divergence,
reputation, or relations display).

### Bootstrap & baseline acquisition (`useSSE` + `useWorld`) — snapshot-first + stream cursor

`AgentFrame` is sparse, so deltas are only meaningful over a snapshot baseline — and the
baseline pairs with a **transport cursor**: the snapshot wrapper's `stream_cursor` (the Redis
events entry ID the captured state already reflects, data-contracts §1/§4). The flow:

1. **Snapshot FIRST.** On mount, `loadSnapshot` retries with capped exponential backoff
   (`retryUntil`, base 500 ms → cap 10 s, unbounded attempts, generation-cancelled on unmount;
   stale responses discarded) until a REAL snapshot arrives — an empty synthetic snapshot is
   NEVER a baseline, and a transient failure can never permanently blank the page.
2. **Authoritative acceptance.** `SNAPSHOT_LOADED` REPLACES the agent roster (agents absent
   from the snapshot are gone — no ghost survives a merge accident; SSE-only fields like
   `cluster`/`copingMode` re-derive from post-cursor events), records
   `worldRevision`/`snapshotCursor`/`terrainStatus`, and sets `lastAppliedStreamId` to the
   cursor. Within the SAME revision, a snapshot whose cursor is BEHIND `lastAppliedStreamId`
   is REJECTED (`baselineRetries++` → refetch after ~2 s) — accepting it would roll sparse
   state back across deltas that will never replay. A DIFFERENT `worldRevision` always accepts
   and first CLEARS every old-revision slice (agents via replacement, animals, flora, climate,
   fx, terrain + queued terrain deltas); tick may rewind across revisions.
3. **SSE after the baseline, WITH the cursor.** `useSSE` is enabled only once
   `snapshotLoaded`; every (re)connect passes `?cursor=` = `lastAppliedStreamId` (falling back
   to `snapshotCursor`). The server replays retained entries strictly after it into the live
   tail (api SPEC `GET /sse`), so the [capture, connect) window and any disconnect window are
   recovered losslessly — including agents that moved, changed goal/mood/action, or were
   REMOVED before the connection existed.
4. **Ordered, at-most-once application.** Every SSE frame carries its Redis entry id
   (`MessageEvent.lastEventId`); the reducer applies an identified frame only when its id is
   strictly after `lastAppliedStreamId` — duplicates, pre-cursor stragglers and old-revision
   leftovers (always ≤ the new cursor: regen deletes the old entries and IDs stay monotone)
   are all dropped by one rule. Id-less frames (legacy/mock transport) apply unconditionally.
   Events arriving before any baseline are dropped (SSE is gated, so only mis-ordered
   stragglers can).
5. **Gap ⇒ reacquire, never a partial history.** If the server detects the requested cursor
   is older than the retained backlog (MAXLEN trim), it sends one `StreamGap` control frame
   and closes. The reducer bumps `baselineRetries` (→ fresh snapshot/cursor after ~2 s);
   the auto-reconnect loop keeps re-offering the current cursor and converges once the new
   baseline lands. Bounded memory: nothing is queued while disconnected.
6. **Terrain by explicit availability.** `terrainStatus` comes from the snapshot's `terrain`
   flag: `'off'` ⇒ env-off — terrain is never fetched and never polled; `'on'`/`'unknown'` ⇒
   `loadTerrain(worldRevision)` retries with capped backoff, and a grid tagged with a
   DIFFERENT `world_revision` counts as not-ready (another regen landed between reads).
   env-off is NEVER inferred from a failed fetch. `terrain_delta`s arriving before the grid
   queue in `pendingTerrainDeltas` and apply when it lands.
7. **Flora baseline (`GET /api/flora`).** Flora that existed BEFORE this client connected
   (fixtures + already-propagated plants) is replayed by NO SSE event, so it needs a REST
   baseline — mirroring terrain. Gated by its OWN explicit signal `floraStatus` (from the
   snapshot's `flora` flag, decoupled from terrain): `'off'` ⇒ env-off, never fetched (and the
   reducer short-circuits `flora:[]` + `floraLoaded:true`); `'on'`/`'unknown'` ⇒ once
   `snapshotLoaded`, `loadFlora(worldRevision,snapshotCursor)` fetches once per revision/reacquisition
   (capped backoff; a different revision or `stream_cursor < snapshotCursor` is retried). `FLORA_LOADED`
   applies it as an **AUTHORITATIVE REPLACEMENT** of `WorldState.flora` (never merged — a
   regenerated world must leave no ghost plants), then **replays `pendingFloraOps`** (see below)
   on top so post-cursor mutations win, and sets `floraLoaded` (the once-per-revision guard,
   distinct from `flora.length === 0` which is a valid installed-empty state).
   - **Pre-baseline buffering (mirrors `pendingTerrainDeltas`).** SSE opens BEFORE the flora
     baseline lands, so a `flora_delta` upsert or `PlantDied` remove can arrive while
     `!floraLoaded`. Applying it to the empty `flora` map then wholesale-replacing it at
     `FLORA_LOADED` would LOSE a post-cursor spawn or RESURRECT a post-cursor death (the baseline is
     as-of an OLDER `stream_cursor`). Instead, while `!floraLoaded` these mutations are buffered as
     cursor-tagged `pendingFloraOps` and replayed after replacement only when
     `op.streamId > baseline.stream_cursor`, so the newer live state wins. `PlantSpawned`/`PlantDied` FX still fire immediately
     (FX carry no state).
   - **Revision switch / StreamGap re-arm.** A revision switch clears old-world ops. A `StreamGap`
     sets `snapshotLoaded:false` (closing SSE), retains cursor-tagged ops, accepts a fresh snapshot,
     then fetches flora at/after that cursor; the flora baseline cursor classifies which ops remain newer.

   After the baseline, SSE `flora_delta` (full-row upsert) / `PlantSpawned` (spawn FX) / `PlantDied`
   (remove + death FX) keep it current; a first-seen `flora_delta` row still carries `species`+`width`,
   so a plant that propagated between the baseline and now renders correctly even without its
   `PlantSpawned`.

## UI Layout

```
┌───────────────────────────────────────────────────────────┐
│  bdg · tick 42 · 8 agents · 6 fauna · ☀ 14°C · ● LIVE      │  ← Header
├───────────────────────────────┬───────────────────────────┤
│  WorldCanvas3D (orbit/tilt/   │  Sidebar                  │
│                zoom/pan)      │  ┌─ AgentDetail (click) ──┐│
│  ≈≈ river   ▒ forest          │  └────────────────────────┘│
│  · farmer_1 (satiety)         │  EventLog                  │
│  ▷ goat (walk→run, flee)      │  [filter: all ▾]           │
│  ▶ wolf (attack lunge)        │  ⚒ farmer_1 Crafted axe    │
│  ♣ oak (stage 3, grow fx)     │  🐺 wolf_1 killed deer_2   │
│  [±]           ┌────────┐     │  🎯 farmer_1 → Satiety     │
│  ~wind→ ☂rain  │minimap ◹│ ◐  │                            │
└────────────────┴────────┴─────┴───────────────────────────┘
```
(`[±]` = 3D zoom buttons, bottom-left; `minimap` = the 2D overview overlay, bottom-right, with the
camera-look marker `◹`.)

- **Header** (`components/Header.tsx`): run id, tick, agent count, fauna count + ambient weather
  chip (day-night icon, temperature + `apparentTemp`, rain/wind), connection badge, theme + pause
  toggles, and a **new-map button** (§New-map flow below). The backend's `POST
  /api/restart` (same-seed deterministic reset) stays available but has no header button.

  **§New-map flow** (`App.tsx regenWorld` + `src/utils/regenReady.ts`; api SPEC `POST
  /api/regen`; data-contracts §2 `world_revision`): `202` means *accepted*, not completed, so
  the reload is completion-aware — never a fixed timer, never tick heuristics (tick is
  simulation time, not world identity): (1) read the PUBLISHED `world_revision` from
  `GET /api/meta`; unreadable ⇒ report a recoverable error and DO NOT submit (readiness could
  not be verified); (2) `POST /api/regen`; (3) poll `/api/meta` with capped backoff
  (500 ms → 4 s) until a DIFFERENT revision is published — the backend publishes only after
  the regenerated snapshot+terrain baselines are servable, and restart/failed regens never
  change the marker; (4) verify the servable `/api/snapshot` (and, when that meta poll said
  `terrain:"on"`, `/api/terrain`) carries exactly that `world_revision` (a further regen
  between reads shows as a tag mismatch ⇒ keep polling); (5) only then
  `window.location.reload()` so client state starts fresh. A bounded budget (60 s) ends the
  wait: on timeout or POST failure an alert reports it and the current view stays usable
  (re-submission allowed; concurrent submissions coalesce behind a busy guard). Other
  connected viewers still reload manually — single-world contract, no resync signal.
- **StatusHud** (`components/StatusHud.tsx`): a floating top-right overlay on the canvas showing the
  in-world date + clock + temperature — `☀/🌙 Day N · HH:MM` over `T°C` (with ☂/❄ when raining /
  snow lying). `Day N` = `climate.dayOfRun + 1` (dayOfRun is 0-based); `HH:MM` from
  `climate.minuteOfDay` (÷60 / %60). Hidden when `climate === null` (env OFF / pre-first-frame).
  Display-only (`pointerEvents:'none'`) so camera drag/wheel pass through to the canvas beneath.
- **WorldCanvas3D** (`components/WorldCanvas3D.tsx`, left, flex-1) — **the main viewer**: a WebGL
  **curved-world** renderer (`src/gl/SPEC.md`). Terrain draws as lit, curved hex prisms; entities are
  billboards seated on the relief; atmosphere (day-night, weather, wind) rides the same climate slice.
  **Camera:** orbit (Shift+wheel) · tilt (Alt+wheel) · dolly (Ctrl/Meta+wheel zoom) · drag pan; click
  an entity to select; **+/− zoom buttons bottom-left**. Plain wheel is intentionally inert. WebGL
  unavailable ⇒ an in-place "WebGL required" notice (no 2D fallback view). It writes its ground-plane
  camera focus (`getFocus()`) into a ref each frame for the Minimap.
- **Minimap** (`components/Minimap.tsx`, **bottom-right overlay**, display-only): a small whole-world
  overview (`docs/plans/frontend.md` §11 — capability = camera-marker, detail = simplified). It runs its
  **own RAF loop** and each frame: computes a **bounds-fit 2D transform** via the render library
  (`initialCamera` → `buildTransform`), reuses a **cached terrain raster** (rebuilt only on grid
  identity change), and draws **only** terrain colours + **agent/animal dots** + a **selection ring** +
  a **camera-look cone** at the 3D camera's ground focus (read from the `CameraFocus` ref; opens toward
  the view heading, sized by zoom). It does **not** draw flora / fx / ambient / motion interpolation
  (those library draws stay unwired). Its size follows the world aspect; geometry (cone direction/radius,
  sizing) lives in `minimapGeom.ts` (unit-tested), and the cone **heading reuses `gl/cameraGeom.
  groundForward`** — the same yaw convention the 3D camera uses, so marker and view can't diverge.
  **No input handlers** — the 3D view owns camera + selection; the minimap only reads the read-only
  `CameraFocus`.
- **Sidebar** (`components/Sidebar.tsx`, right): `AgentDetail` (selected agent; a followed
  *animal* shows no detail panel) over `EventLog` (scrolling, filtered, newest-first, ≤500).

## Module Structure

```
frontend/
  index.html              shell: <div id="root">
  package.json            React 19 + Vite + TS
  vite.config.ts          dev proxy /sse + /api → :8080 (or dev/mock-server.mjs)
  public/assets/          PNG spritesheets — fauna/<species>.png, flora/<species>.png
                          (layout + example files: src/assets/SPEC.md + dev/SPEC.md)
  dev/                    mock-server.mjs + generate-spritesheets.mjs   → dev/SPEC.md
  src/
    main.tsx              entry
    App.tsx               root: theme, useWorld(), useSSE(), sprite cache creation, layout
    config.ts             API_BASE
    theme.ts              LIGHT / DARK ThemeTokens
    types.ts              all shapes (below)
    hooks/
      useSSE.ts           EventSource wrapper: connect, reconnect backoff, dispatch by type
      useWorld.ts         useReducer state machine (applyEvent + reducer) + loadSnapshot/loadTerrain
    components/
      Header.tsx          status strip
      StatusHud.tsx       top-right canvas overlay: date (Day N) + clock (HH:MM) + temperature
      WorldCanvas3D.tsx   WebGL curved-world view — MAIN viewer                 → src/gl/SPEC.md
      Minimap.tsx         bottom-right 2D minimap overlay (whole-world + camera marker) → src/render/SPEC.md
      minimapGeom.ts      pure minimap geometry (size, camera-cone dir/radius) — unit-tested
      Sidebar.tsx         AgentDetail + EventLog container
      EventLog.tsx        scrolling list, filter, trim
      AgentDetail.tsx     selected-agent panel
    gl/                   stateful WebGL curved-world renderer (3D) — MAIN view  → src/gl/SPEC.md
    render/               pure 2D draw / camera / animation library (powers the Minimap) → src/render/SPEC.md
    assets/               manifest (data tables) + spritesheet cache    → src/assets/SPEC.md
    utils/
      eventFormatter.ts   formatEvent(ev) → LogEntry | null
      retry.ts            retryUntil (capped-backoff, cancellable) — loader backbone
      streamId.ts         compareStreamIds (Redis entry-id ordering — §Bootstrap cursor guard)
      regenReady.ts       fetchPublishedRevision + waitForRegenReady (§New-map flow)
```
`src/utils/canvasRenderer.ts` is **deleted** (absorbed into `src/render/`). World state lives in the
`useWorld` reducer (single source of truth, passed down as props). **Camera ownership:** the 3D camera
is internal state of `WorldGL` (`src/gl`, the main view); `WorldCanvas3D` exposes it **read-only** as a
`CameraFocus` written into a `focusOut` ref each frame; the `Minimap` reads that ref and derives its
own **per-frame** bounds-fit 2D transform (it keeps no persistent camera). `App` owns no camera
state — it only wires the shared `CameraFocus` ref between the two. No module-level mutable
singletons; no `window.*` stores.

## Key Types (`src/types.ts`)

`SimEvent`, `AgentState`, `WorldObject`, plus the env/motion shapes:
```ts
interface AnimalState  { id; pos; species; action; heading; stamina;
                         prevPos; prevHeading; frameAtMs; prevFrameAtMs }   // interpolation (Q3)
interface PlantState   { id; pos; species; stage; width }
interface ClimateState { temperature; apparentTemp|null; moisture; raining;
                         windDir; windMag; hourOfDay; minuteOfDay; dayNight:'day'|'night';
                         dayOfRun; yearFraction }   // dayOfRun 0-based, minuteOfDay ∈[0,DayMinutes) — date HUD
interface TerrainGrid  { cellSize; cols; rows; terrain:string[]; wear?:Float32Array; elevation?:Float32Array; orientation:'flat' } // /api/terrain + deltas; flat-top hex, offset(col,row) array (hex-grid.md); elevation ∈[0,1] static (generated worlds — 3D per-cell height; absent ⇒ per-type heights)
interface FxInstance   { kind:'spawn'|'death'|'attack'|'grow'; at:ms; pos; id; species?; heading? }
interface RenderConfig { bounds:{min,max}; pixelsPerUnit }
interface WorldState   { tick; agents:Map; objects[]; roles[]; log[]; connectionStatus;
                         selectedEntity:{kind:'agent'|'animal', id}|null; paused; food; wood;
                         animals:Map; flora:PlantState[]; climate:ClimateState|null;
                         terrain:TerrainGrid|null; fx:FxInstance[]; render:RenderConfig|null;
                         snapshotLoaded;
                         worldRevision:number|null;      // accepted baseline's published revision
                         snapshotCursor; lastAppliedStreamId; // §Bootstrap transport cursor
                         terrainStatus:'on'|'off'|'unknown';  // explicit availability
                         pendingTerrainDeltas:TerrainDelta[]; // pre-grid deltas
                         floraLoaded;                         // once-per-revision flora baseline guard
                         floraStatus:'on'|'off'|'unknown';    // explicit flora availability (decoupled from terrain)
                         pendingFloraOps:FloraOp[];           // pre-baseline flora upserts/removes (§Bootstrap step 7)
                         baselineRetries }                    // gap/stale → reacquire
```
`Pose`, `CameraState`, and manifest types are owned by the children (`src/assets/SPEC.md`,
`src/render/SPEC.md`).

## SSE Connection (`src/hooks/useSSE.ts`)

- Enabled only once `snapshotLoaded` (§Bootstrap step 3). Every (re)connect opens
  `new EventSource(\`${API_BASE}/sse?cursor=<id>\`)` where the cursor comes from `getCursor()`
  (= `lastAppliedStreamId` ∥ `snapshotCursor`; '' ⇒ no query, live tail — legacy/mock). The
  server replays retained entries strictly after the cursor into the live tail (api SPEC), so
  reconnects resume after the last applied frame with neither gap nor duplicates.
- On `message`: `JSON.parse` → `SimEvent` → dispatch with `MessageEvent.lastEventId` (the
  frame's Redis entry id from the server's `id:` line; the reducer's ordering guard).
- On `open`: `setConnection('live')`. On `error` (including the server's close after a
  `StreamGap` control frame): `'reconnecting'`, retry after `min(2^retries × 500ms, 30s)`;
  reset on success. Decode once; no re-stringify for display.

## Rendering & Motion (contract summary — detail in `src/render/SPEC.md`)

> This section is the contract of the **2D render library** (`src/render`) that now powers the
> bottom-right **Minimap** (`components/Minimap.tsx`); the Minimap uses a subset (bounds-fit camera +
> shared transform + terrain raster) drawing simplified dots, the rest remaining as tested library
> code. The **main 3D** rendering/atmosphere/camera contract lives in `src/gl/SPEC.md`. Both consume
> the same reducer-owned `WorldState` slices (below), so the reducer/bootstrap contract is view-agnostic.

- **One shared world→canvas transform** built from `CameraState` (center/zoom/follow) — all layers
  (terrain, flora, objects, animals, agents, fx) map through it (D11: continuous coords, never
  tiled/snapped). Camera: init from `RenderConfig` (auto-fit until fetched), then zoom/pan/follow.
- **RAF loop** (owned by the consuming component): today's simplified `Minimap` redraws continuously
  from reducer state + the read-only camera-focus ref; it does not sample a clock or call the
  animation/fx/ambient draw functions. A fuller consumer of the retained 2D library samples
  `performance.now()` once at the frame boundary and passes it into pure render functions as
  `clockMs`, redrawing on state change or while animation is live. Never draw in the SSE handler.
- **Motion**: entity positions/headings interpolate between the last two SSE frames (adaptive
  lerp, plan Q3); animals draw a spritesheet frame chosen by `species × pose × clockMs`, where
  pose = data-driven mapping of the open-content `action` (plan Q2; `hunt/attack→attack` is the
  **attack motion**), rotated by heading, dimmed by stamina.
- **Transition FX** (plan Q4): the reducer appends `FxInstance`s on lifecycle events / attack-pose
  entry / stage increase; the render layer evaluates them time-parametrically (spawn fade-in,
  **death fade + corpse ~1.5 s**, attack lunge, grow tween) and the reducer prunes expired ones.
- **Terrain**: `GET /api/terrain` + `terrain_delta` → offscreen raster of **flat-top hex** cells in
  data-driven flat colours (river/grass/forest = `water/plain/forest` TerrainIDs), wear trails overlay.
  Offset(col,row) rectangular array; the frontend mirrors navmap's hex convention from the payload
  (`docs/plans/hex-grid.md`). The old hardcoded decorative map is gone.
- **Ambient**: day-night tint, temperature vignette, rain, wind arrow from `ClimateState`.
- **Scent field**: NOT streamed (derived, data-contracts §10) — no render.

## Event Log (`src/components/EventLog.tsx` + `utils/eventFormatter.ts`)

- Scrolling list, newest-first; `[tick] icon summary` + variant colour. `formatEvent` reads payload
  fields directly (glossary names); unknown types → `⁉` raw.
- Filter: `all`, `social`, `goals`, `roles`, `actions`, **`ecosystem`
  (AnimalBorn/AnimalDied/PlantSpawned/PlantDied)**. Display-only over the in-memory array.
- Trim: `> 500` entries → slice to 400 (drop oldest).

## Invariants

- **Read-only observation, one sim control.** No writes except the two documented sim control
  signals (`POST /api/regen` behind the new-map button's confirm, `POST /api/restart` unexposed);
  the local pause toggle freezes the *view* reducer only.
- **No god-view.** `?god=true` never sent; `real_stats`/`drives`/`stats` never read or displayed —
  not for agents, not for animals.
- **Reconnect on failure.** SSE drop → badge + capped-backoff retry; never a page reload.
- **Single source of truth.** All world state (incl. `fx[]`, `terrain`, interpolation fields) is
  the `useWorld` reducer value. The 3D camera is `WorldGL`-internal (surfaced read-only as a
  `CameraFocus` ref); the Minimap's 2D `CameraState` is a per-frame derived value (bounds-fit via
  `initialCamera`), not persistent user state; `App` owns no camera state. No `window.*`, no
  module-level mutable state outside pure render helpers (see child invariants).
- **Render purity.** Render/animator fns are pure `(state, camera, clockMs) → pixels`; sprite
  cache injected; no `Math.random`/wall-clock reads inside (child SPECs own the guards).
- **Data-driven content mapping (D10 mirror).** species/ActionID/TerrainID → sprite/pose/colour
  only via the assets manifest + fallback chain; unknown content renders a fallback, never throws.
- **Decode once.** Parse the SSE payload once; format from parsed fields.
- **Tick counter only advances WITHIN a revision.** A `world_revision` switch (regen) is the
  one legitimate rewind; everything else keeps `max(tick, ev.tick)`.
- **One world transform (D11).** Every layer shares one `buildTransform` per frame.
- **Env-off neutrality.** No `WorldFrame`/terrain streamed ⇒ env slices empty ⇒ canvas + Header
  render exactly as the social-only viewer.

## Acceptance Criteria (parent-level; per-layer ACs live in the child SPECs)

> Vitest on the reducer + integration of pure render helpers; browser smoke via `dev/mock-server.mjs`.

- [ ] **SSE connect/reconnect** — `useSSE` opens `EventSource("/sse")`; first message → `live`;
  forced error → reopen within 2× backoff showing `reconnecting`.
- [ ] **AgentFrame sparse merge** — a delta carrying only `pos` moves the agent while its
  `goal/mood/action` keep the baseline values; `removed[]` deletes the agent; no log line.
- [ ] **Loader retry (§Bootstrap steps 1/6)** — `fetchSnapshotWithRetry` / `fetchTerrainWithRetry`
  survive a 404/network/malformed response and resolve on a later success, back off
  exponentially to the cap, and return null (no dispatch) once cancelled — stale responses after
  a generation bump are discarded; the snapshot parser surfaces the publication wrapper
  (`revision`/`cursor`/`terrain`, legacy ⇒ null/''/'unknown'); a terrain grid tagged with a
  different revision than expected is retried, never dispatched.
- [ ] **Cursor-ordered application (§Bootstrap step 4)** — with a baseline at cursor C: a frame
  at C is ignored (already in the snapshot), frames after C apply once in id order, duplicate
  ids and older ids are ignored, `lastAppliedStreamId` tracks the applied head; id-less frames
  (legacy/mock) apply unconditionally; pre-baseline events are dropped.
- [ ] **Lossless late join (§Bootstrap steps 3–4)** — a stale periodic snapshot plus replayed
  post-cursor frames recovers pre-connect changes: moved `pos`, changed `goal/mood/action`, and
  an agent removed after capture but before connection (via a replayed `removed[]`).
- [ ] **Authoritative roster (§Bootstrap step 2)** — a reaccepted snapshot REPLACES the agent
  map (ghosts absent from it disappear); post-cursor deltas re-apply on top; a snapshot whose
  cursor is behind `lastAppliedStreamId` (same revision) is rejected with `baselineRetries++`
  and nothing rolls back.
- [ ] **Revision switch (§Bootstrap step 2)** — a snapshot with a different `world_revision`
  clears agents/animals/flora/climate/fx/terrain/queued deltas, re-arms `floraLoaded:false` +
  clears `pendingFloraOps`, accepts a lower cursor and a rewound tick, and subsequent new-stream
  frames apply normally.
- [ ] **StreamGap ⇒ reacquisition (§Bootstrap step 5)** — the control frame increments
  `baselineRetries`, sets `snapshotLoaded:false` to close SSE, and re-arms flora without prematurely
  deleting buffered ops; after the fresh snapshot, only ops strictly newer than the accepted flora
  baseline cursor replay.
- [ ] **Terrain availability (§Bootstrap step 6)** — `terrain:"off"` completes bootstrap without
  ever fetching terrain and clears any grid/queued deltas; `"on"` retries transient failures and
  revision mismatches; `"unknown"` (legacy/mock) keeps the capped-backoff fallback.
- [ ] **Flora availability + pre-baseline buffering (§Bootstrap step 7)** — `flora:"off"` completes
  bootstrap without ever fetching `/api/flora` (`flora:[]`, `floraLoaded:true`); `"on"`/`"unknown"`
  fetch once per revision with capped backoff + revision/cursor-mismatch retry. A `flora_delta` upsert or
  `PlantDied` remove arriving while `!floraLoaded` is buffered into `pendingFloraOps` and replayed
  AFTER the baseline replacement (a post-cursor spawn survives; a post-cursor death is not
  resurrected); spawn/death FX fire immediately regardless.
- [ ] **New-map completion-aware reload (§New-map flow)** — `waitForRegenReady` reports ready
  only after a NEW `world_revision` is published AND the snapshot (and terrain when that
  revision says on) carries exactly that revision; an unchanged revision (rebuild running,
  regen aborted, restart) times out as not-ready within the bounded budget; readiness never
  fires on the old baseline; an unreadable pre-regen revision blocks submission with a
  recoverable error.
- [ ] **WorldFrame env reduce + motion fields** — a second `WorldFrame` for the same animal moves
  `pos→prevPos`, `heading→prevHeading`, stamps `frameAtMs/prevFrameAtMs`; merges flora (by id),
  terrain deltas (grid object replaced), climate; ignores injected god-view fields.
- [ ] **Fx queue** — `AnimalDied` removes the animal AND appends `{kind:'death'}` with its last
  pos/species/heading; a flora `stage` increase appends `grow`; an animal whose pose enters
  `attack` appends `attack` once (not every frame); expired fx are pruned by a later reduce.
- [ ] **Event log filter + trim** — `AnimalDied` under `ecosystem`/`all`; 600 entries → ≤ 500.
- [ ] **RoleEmerged** — updates `roles`, tags holder cluster.
- [ ] **Selection + follow** — clicking an agent selects it (detail panel) and the camera follows;
  clicking an animal follows without a detail panel; empty click / manual pan clears follow.
- [ ] **No `real_stats` leak** — snapshot/event carrying god-view fields never displays them.
- [ ] **Env-off neutrality** — with no `WorldFrame`/terrain, canvas + Header byte-match the
  social-only render.
- [ ] **Mock smoke** — against `dev/mock-server.mjs`: river/grass/forest terrain visible, goat
  walk→run→flee motion, wolf attack lunge, a death fade + corpse, a plant grow tween, day-night +
  rain ambient — with zero `src/` changes when later pointed at the real backend.

## Out of Scope (P2+)

- **God-view mode** (`?god=true`, divergence/reputation/relations, real_stats/drives panels).
- **Time scrubbing / replay** — SSE is live-only.
- **Scent / cost-field / navmap debug overlays** (not streamed; needs dedicated endpoints).
- **Social graph panel**; **player controls / writes**; **mobile layout**; **auth/CORS** (gateway).
- **Backend work**: `WorldFrame` emission (WI-P4) and the `GET /api/terrain` server route —
  tracked in `docs/plans/frontend.md` (Q5 pending backend SPEC delta on `platform/api`).

## Build & Dev

```bash
cd frontend && npm install
npm run dev                        # Vite dev server; proxies /sse + /api/* → :8080
node dev/mock-server.mjs           # contract-parity fake backend on :8080 (dev/SPEC.md)
node dev/generate-spritesheets.mjs # regenerate placeholder PNG assets
npm run build                      # static bundle → frontend/dist/
```

## Notes

- **Why React (not vanilla)?** Header chip, selectable detail, filtered log, theming, and the env
  reducer benefit from component state + a single `useReducer`; render stays pure fns.
- **Ambient FX is the visible cause of fauna behaviour.** Temperature drives the fauna `thermal`
  slowdown (F35/F40), wind drives scent spread + upwind homing (F33) — rendering them makes the
  ecosystem legible. Keep the overlay readable, not overwhelming.
- **Motion is client-side presentation only.** Interpolation, poses, and FX never feed back into
  state or imply simulation semantics; the sim's truth is the streamed frames (D12 stays intact).
- **Cluster colour hashing**: `hash(holder_id) % PALETTE_SIZE`; unclustered = neutral grey.
- Reference: `backend/platform/api/SPEC.md` (endpoints), `docs/core/data-contracts.md §2/§4/§10`,
  `backend/engine/fauna/SPEC.md` (heading/action/species are open content),
  `backend/engine/env/flora/SPEC.md` (stage/width), `backend/engine/env/climate/SPEC.md`,
  `docs/plans/frontend.md` (phases FE-P1…P5 + resolved decisions Q1–Q8).
