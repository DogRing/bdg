# Data Contracts — Serialization · Redis · Postgres · Events

> Contracts that cross module boundaries. **Freeze before implementing consumers.** Bump `schema_version` and start here for any change.
> The below defines *shapes*. Exact field types are finalized by the architect against the `core` types.

## 0. Common
- Every payload carries `schema_version` (int). Bump +1 on any backward-incompatible change.
- Serialization: JSON during early debugging; may switch to a deterministic dense encoding (gob/protobuf) after it stabilizes — but **byte-determinism** must hold (sorted map-key order).
- IDs: `RunID`, `AgentID`, `ObjectID` are strings. Tick is an integer (game-minutes).

## 1. Simulation snapshot (engine → persist)
The complete deterministic state for one tick. Same base snapshot + same seed → same next tick.
```
Snapshot {                        // persist.Snapshot — JSON, snake_case keys (Go field tags)
  schema_version, run_id, tick
  world_revision?                  // publication marker (§2); stamped by the run-driver at flush
  stream_cursor?                   // Redis events-stream entry ID the state reflects (§2); flush-stamped
  terrain?                         // "on" | "off" — explicit terrain availability of this
                                   // revision (§2); absent ⇒ unknown (legacy blob)
  flora?                           // "on" | "off" — explicit flora availability of this revision
                                   // (§2, mirrors `terrain`); absent ⇒ unknown (legacy blob)
  world {                          // engine world.WorldState
    tick, rng_state               // rng_state: for deterministic resume
    agents[] {
      id, pos
      real_stats   { StatID: float }            // god view (live exposure policy in §4)
      stamina, mood, adrenaline
      need_intensities { Dimension: float }
      inventory { Tag: int }, goal               // count per kind = SUM over the kind's live decay lots (§8) / tool instances (§9)
      plan_actions[], plan_horizon, plan_idx, elapsed, coping, latent[]
      self_est_stats { StatID: { mean, variance } }   // ToM[self] (D8 self-channel)
      agent_cfg
    }
    objects[] { id, kind, pos, owner?, remaining? }   // in-world objects; `remaining` = finite source count (§9, Xm1)
    known[]   { agent_id, objects[] }            // per-agent known-object set
    decay_lots[]      { object_id, kind, qty, decay_age, location }  // perishable LOTS (§8, Dm5(a)); periodic-full
    tool_instances[]  { object_id, kind, durability, location }      // durable TOOLS (§9, FINAL); periodic-full
    flora[]    { object_id, species, pos, length, width, death_streak } // live plants (§10, WI-P4); periodic-full
    animals[]  { object_id, species, pos, stats{StatID:float}, drives{DriveID:float},
                 stamina, vital, vital_cap, heading, current_action, active_until,
                 engaged_with?, next_exchange_tick?, engage_cooldown_until?,
                 hidden_until?, concealment? }  // fauna incl. Phase-6 combat/hiding fields (§10, WI-P4); periodic-full
    shelters?  { interiors[]{id, bounds, portals[]{id, kind, exterior_pos, interior_pos}, occupants[]},
                 active_spaces[]{object_id, space_kind, space_id?, pos} } // SH2 cave/interior state; absent ⇒ shelter OFF
    climate    { cells[]{cell, moisture, temperature, terrain, frozen_from}, rain{raining, rain_ends_at_hour,
                 p_rain, hours_since_rain}, wind{dir, mag}, snow_cover } // climate field (§10, WI-P4); periodic-full
    emerged_roles[] { function, holder }         // P6 (D2-derived)
    tom_digest {                                 // cross-agent ToM, god-view projection (D6/D8)
      <observerID>: { <subjectID>: { est_stats: { StatID: { mean, variance } }, rely_on: { Function: float } } }
    }
  }
}
```
- `tom_digest` is **capture-only** (NOT consumed by resume; the running sim rebuilds beliefs). It is the
  source for the god-view "others" channel: `real` (real_stats) ≠ `self` (self_est_stats) ≠ `others`
  (mean over `tom_digest[X][subject].est_stats`) are kept SEPARATE (D6/D8) — never a single reputation scalar.
- All keys are snake_case so the read API (`platform/api`) parses the live snapshot blob directly.
- **Two storage views.** Each backup cadence starts with one `CaptureSnapshot` base. The run-driver
  encodes that untouched base for the Postgres `snapshots.blob`; because the wrapper fields are
  zero/empty and `omitempty`, `world_revision`, `stream_cursor`, `terrain`, and `flora` are absent.
  It then copies the base, stamps those fields, and separately encodes the Redis live snapshot.
- **Wrapper vs state (determinism scope).** `world_revision` / `stream_cursor` / `terrain` / `flora` are
  OPERATIONAL Redis publication metadata (wall-clock-derived stream IDs, boot-dependent revision),
  not deterministic simulation state. They never enter the Postgres backup blob. Determinism ACs
  (§5, `docs/core/testing.md`) cover the base snapshot; resume consumes the base state only.
- **Inventory vs per-instance state (Materials Dm5(a)/FINAL).** `inventory { Tag: int }` is the per-kind
  COUNT view used by needs/planning/render. Two kinds of inventory item carry PER-INSTANCE state in a
  separate channel, with the `inventory` count = sum over instances:
  - a perishable kind is one or more **decay lots** `{kind, qty, decay_age}` (`decay_lots[]`, §8) — lots
    never auto-merge (Dm5(a));
  - a durable **tool** is one or more **tool instances** `{kind, durability}` (`tool_instances[]`, §9) —
    `stackable:false`, so each tool is its own instance carrying its CURRENT durability.
  A plain (non-perishable, non-tool) kind has no instance rows; its `inventory` count is authoritative.
- **`objects[].remaining`** is the finite-source count for an `ore_node` (Xm1, §9); absent for objects
  that are not finite sources. `objects[].owner` is the optional owner AgentID (glossary `owner`).
- **Env state (WI-P4, §10).** `flora[]`/`animals[]`/`climate` are the periodic-full sources for the
  env subsystems; they stream like decay/navmap — **periodic full + sparse per-step deltas** (§10).
  Derived discrete views are NOT stored: a plant's `stage` derives from `length`, an animal's
  active/dormant from `active_until` (recomputed on read, mirrors decay `state`). The **scent grid is
  DERIVED** (rebuilt from `scent:<channel>` emitter positions on resume) and is **NOT serialized**
  (spatial-hash parity, §1). **Dynamic terrain** (climate transitions) streams via the navmap
  `TerrainOverrides()` sparse delta (§6) — the base layout comes from the fixture. When env is OFF
  (`InstallEnv`/`InstallFauna` not called) these blocks are **absent/empty** — existing snapshots are
  byte-unchanged (the schema_version bump is additive; absent ⇒ env-off).
- **Shelter state (SH2).** `shelters` is present only when `InstallShelter` is called. `interiors[]`
  is the periodic-full source for cave/house interior regions and their portal links; `active_spaces[]`
  records entities currently inside an interior plus their local continuous `pos`. Exterior entities
  need no row. The exposure field itself is derived from blockers/interiors and is not serialized
  except optional debug/render deltas (§6).

## 2. Redis — live state
Keyspace (`{run}` = RunID):
```
sim:{run}:meta          HASH    { tick, schema_version, started_at, status,
                                  world_revision, terrain, flora }  // publication marker, below
sim:{run}:tick          STRING  current tick (fast read)
sim:{run}:snapshot      STRING  latest Snapshot (serialized)  // or chunked
sim:{run}:agent:{id}    HASH     live agent summary (render): pos, goal, action, mood
sim:{run}:animal:{id}   HASH     live animal summary (render): pos, species, action, heading, stamina (WI-P4)
sim:{run}:flora         STRING   live plant render baseline: {world_revision, flora:[{object_id, species, pos, stage, width}]} (WI-P4; GET /api/flora — revision-tagged like :terrain; empty flora:[] ⇒ installed-but-no-plants, key absent ⇒ flora-off; the meta `flora` flag ("on"/"off") is the frontend-facing availability signal, mirroring `terrain`)
sim:{run}:climate       HASH     ambient: temperature, apparent_temp?, moisture, raining, snow_cover,
                                 wind_dir, wind_mag, hour_of_day, day_night, year_fraction (WI-P4 — frontend
                                 ambient FX; snow_cover ∈[0,1] world-uniform snowpack CS2b, plan §1d)
sim:{run}:terrain       STRING   render terrain: base layout + overrides (climate transitions) + wear (trails)
                                 + optional per-cell elevation[] ∈[0,1] (generated worlds; render-only relief)
                                 + world_revision (below) (WI-P4)
sim:{run}:events        STREAM   ALL events (§4), incl. the per-frame render deltas
                                 (AgentFrame/WorldFrame). XADD append; SSE tails this ONE stream —
                                 there is NO separate frame stream/key
```
- Live keys hold **only what render/observation needs.** Full state lives in the snapshot key.
- TTL: keep only active runs; on completion, back up to Postgres then expire.
- The env render keys (`animal:{id}`/`flora`/`climate`/`terrain`) exist only when env is
  installed; absent ⇒ env-off (WI-P4). `stage`/`day_night` are DERIVED (from `length`/`hour_of_day`)
  on write — the live keys carry the render-ready view, not the raw sim state.
- **`world_revision` — the single-world publication marker (NOT a run generation).** One run_id
  exposes exactly one active world; `world_revision` identifies which published map revision the
  live baselines belong to. It is an operational readiness marker for the current single-world
  mode — not a user-selectable world identity, not `run_id`, not the deferred multi-world
  generation (`docs/plans/run-generation.md`). Semantics:
  · the snapshot blob (§1 wrapper), the terrain blob, the flora blob and the meta hash each carry
    the revision of the world they were written for, so a reader can verify that a snapshot/terrain/
    flora set belongs to one published world without atomic multi-key reads (mismatch ⇒ refetch);
  · a successful `POST /api/regen` publishes the NEXT revision; `POST /api/restart` and failed
    regens never change it; publication is LAST — after the mandatory Postgres reset, the
    best-effort Redis cleanup AND the regenerated snapshot+terrain+flora baseline writes succeed,
    the run-driver HSETs `world_revision` + `terrain` + `flora` (each "on"/"off" — explicit env-off
    markers) onto `sim:{run}:meta` in one command. A reader that observes the new revision in meta
    is therefore guaranteed revision-tagged baselines are already servable;
  · a transient baseline-write failure — terrain **or flora** — defers publication to the next
    backup-cadence flush (revision stays pending, old revision stays visible): a published revision
    must always serve BOTH `/api/terrain` and `/api/flora`, so the flora write gates publication
    exactly as terrain does (`writeEnvLive` returns false on either failure);
  · process restart: the run-driver reads the stored revision at boot and publishes
    `stored+1` with its FIRST baseline flush (never backwards, never reused — a restarted
    process rebuilds from the fixture and may publish a different map, so reusing the stored
    revision could mislabel it). Until that first flush, meta/baselines still describe the
    previous process's world self-consistently.
- **`stream_cursor` — the snapshot's transport replay boundary.** The snapshot wrapper (§1)
  records the Redis events-STREAM entry ID (`ms-seq`, assigned by XADD) of the LAST event that
  the captured state already reflects. Entries with IDs ≤ cursor are baked into the snapshot;
  entries strictly after it must be replayed/delivered (SSE `?cursor=`, §4). The cursor is the
  Redis ENTRY ID, never `Event.seq` (seq is process-local and repeats across process restarts;
  entry IDs are wall-clock monotone even across stream re-creation). Captured on the single sim
  writer after the tick's emissions completed; a failed XADD never advances it.

## 3. Postgres — periodic backup (replay / analytics)
```
runs       ( run_id PK, seed, schema_version, started_at, ended_at, status, config_hash )
snapshots  ( id PK, run_id FK, tick, blob BYTEA, created_at, last_event_seq )  // every N ticks
events     ( id PK, run_id FK, tick, seq, agent_id, type, payload JSONB, created_at ) // why-trace persisted
```
- Backup interval from config (`backup_every_ticks`). Reproduce from `seed + config_hash + last snapshot`.
- `snapshots.blob` is the deterministic base snapshot encoding from §1. Redis-only publication
  fields (`world_revision`, `stream_cursor`, `terrain`, `flora`) are absent, so transport cursor or
  publication changes do not change Postgres backup bytes for an otherwise identical world state.
- `events` is the source for why-trace, emergence-metric analysis, and replay.
- **High-frequency exclusion.** Render/operational events (`TickDone`, `AgentFrame`, `WorldFrame`,
  `SnapshotReady`) are NEVER written to `events` — they live only on the Redis stream (§2). Their
  seqs therefore appear as gaps in the Postgres rows.
- **`last_event_seq`** = the max `Event.seq` among the why-trace rows written in the SAME backup
  flush as that snapshot; `NULL` when the flush carried no why-trace events. **One flush = one
  Postgres transaction** (persist `WriteBackup`): the event batch and its snapshot row commit
  together or roll back together, so the boundary can never reference rows that failed to persist
  and no partial batch can exist; on failure the run-driver re-buffers the batch and the next
  flush retries it. Seq is stamped ONCE by the run-driver's fan-out emitter, so the Redis stream
  (§4) and these rows share one numbering. **Seq scope = one simulation process lifetime** (§4):
  `(snapshot at tick T, events seq ≤ last_event_seq)` is a consistent replay cut only among
  events written during the same process lifetime — a real process restart starts a new seq
  epoch at 0 under the same run_id, so seq values repeat across lifetimes and cross-restart
  replay is NOT currently supported (durable cross-process identity: deferred,
  `docs/plans/run-generation.md`).
- **Bounded backup buffer.** Pending Postgres why-trace is capped at 5,000 events. When a prolonged
  database outage fills it, the oldest diagnostic events are discarded in chunks and the dropped
  count is logged at the next backup attempt. Simulation state and Redis render events are not in
  this buffer and are unaffected; this deliberately trades old why-trace for bounded backend memory.
- **Postgres retention (created_at-based).** The run-driver requests pruning after each successful
  backup, but SQL maintenance runs immediately on the first request and then at most once per run
  per 6 hours. Newer than 1h: snapshots keep full cadence. 1h–24h: snapshots keep at most one row per 10-minute bucket.
  24h–3d: snapshots keep one row per 1-day bucket. Snapshots and why-trace events strictly older
  than 3 days are deleted. Buckets are epoch-aligned; the newest snapshot of a bucket survives
  (latest `created_at`, ties by `tick`, then row id). Compression is out of scope.
- **Snapshot form.** Postgres stores periodic full snapshots, not incremental chains. Incremental
  snapshots require an independently retained base plus an ordered, atomic delta chain and
  chain-aware restore/retention; that recovery complexity is deferred while the 3-day TTL and
  downsampling keep full snapshots bounded.
- **`POST /api/regen` ("new map", same run_id — current single-world development mode)** resets
  the run in ONE Postgres transaction (`ResetRunData`: delete the run's `events` + `snapshots`
  rows AND upsert the `runs` row to the new seed/started_at) BEFORE the regenerated world's
  first flush; a failure at any step rolls the whole reset back and ABORTS the regen (the
  current world keeps running, old history AND old runs metadata intact — a half-cleaned run is
  never presented as a new map). Redis cleanup stays best-effort — an accepted, documented
  limitation of this phase (a stale per-entity hash can linger if its DEL fails). After a regen
  ALL frontend viewers must reload (no multi-viewer resync signal exists); a `202` response
  means the signal was accepted, not that the rebuild succeeded or that clients have
  resynchronized. The initiating client reads the published `world_revision` (§2) via
  `GET /api/meta` before submitting, then polls (capped backoff, bounded timeout) until a NEW
  revision is published — publication happens only AFTER the regenerated snapshot+terrain
  baselines are servable — loads the revision-matching baselines, and only then reloads; on
  timeout/abort it reports failure and keeps the current view (api SPEC `POST /api/regen`).
  Exactly one world is
  exposed — no world catalog/selector, no run pointer, no automatic SSE run switching.
  Multi-world / run-generation is **DEFERRED** (design notes: `docs/plans/run-generation.md`).
  **`POST /api/restart` is a debugging rewind** — Postgres history is preserved (append-only),
  never deleted; "latest snapshot" therefore means the newest **persisted** row (`created_at`,
  row id), never the highest tick.

## 4. Event / SSE schema (observability + frontend)
The engine emits via the `events` interface. why-trace and SSE are **two views of the same stream.**
```
Event {
  schema_version, tick, seq, agent_id?, type, payload
}
```
- `seq` is stamped ONCE by the run-driver's fan-out emitter (`backend/main.go`; the Redis emitter
  runs `events.WithCallerSeq`), so the SSE stream and the Postgres `events` rows (§3) carry the
  SAME seq for the same event — two views of one stream, one numbering. **Scope: seq is monotone
  and deterministic within ONE simulation process lifetime.** `POST /api/restart` rebuilds
  in-process, so the sequence continues across that debugging rewind; a REAL process restart
  begins a new process-local seq epoch at 0 while the run_id (and its restart-preserved Postgres
  history) persists — durable cross-process sequence identity is deferred to the world/run
  lifecycle design (`docs/plans/run-generation.md`).
Representative `type`s (payload gist):
| type | payload |
|------|---------|
| `Perceived` | sense, object_id, salience_delta |
| `GoalSelected` | dimension, target, priority, eff_value |
| `PlanBuilt` | steps[], total_cost, provisioned[] |
| `ActionStarted` / `ActionDone` | action, progress / result |
| `Interacted` | with, signal_kind, claimed_value, accepted |
| `BeliefUpdated` | about, stat, old, new, cause(`observe|gossip`) |
| `ReputationGossip` | about, from, trust, delta |
| `CopingEntered` | mode(`rebind|longing|latent|apathy`) |
| `RoleEmerged` | function, holder, reliance_share |
| `Decayed` | object_id, kind, from_state, to_state, transform[]{item, qty} (Materials §8; on a decay transition) |
| `Crafted` | recipe, outputs[]{item, qty, durability?}, consumed[]{kind, amount}, tools_worn[]{object_id, durability, broke} (Materials P_m3/FINAL; on a Craft completion — `durability` is the produced tool's start durability = basis_stat roll·wear_max) |
| `Mined` | object_id (ore_node), yields[]{item, qty}, remaining, depleted, tool, tool_broke (Materials P_m4/Xm; on a Mine completion; `depleted`+the reroute when remaining→0) |
| `ToolBroke` | object_id, kind, owner (Materials FINAL; on a tool reaching 0 durability → object-mortality) |
| `AnimalBorn` / `AnimalDied` | object_id, species, pos / cause (WI-P4; fauna spawn + object-mortality §7) |
| `PlantSpawned` / `PlantDied` | object_id, species, pos (WI-P4; flora propagation + object-mortality) |
| `AgentFrame` | tick, agents[]{id,pos?,goal?,mood?,action?}, removed[] (sparse changed-agent fields for the frontend; snapshot is the late-join baseline; god-view EXCLUDED) |
| `WorldFrame` | tick, day_of_run, hour_of_day, minute_of_day, day_night, temperature, apparent_temp?, raining, snow_cover, wind{dir,mag}, animals[]{id,pos,species,action,heading,cover_id?}, flora_delta[]{id,species,pos,stage,width}, terrain_delta[]{cell,terrain?,wear?} (cell = offset index `i=row·cols+col` into the flat-top hex grid, `docs/plans/hex-grid.md`; `day_of_run` is worldtime.DayOfRun, the 0-based day index since run start, and `minute_of_day` ∈[0,DayMinutes) the game-minute within the day — together they drive the frontend date/clock HUD, live-refreshed each frame; snow_cover ∈[0,1] world-uniform snowpack, CS2b/plan §1d; `cover_id` is the occupied cover flora while hidden, empty otherwise; WI-P4 — the frontend graphics frame; god-view EXCLUDED) |
| `EnteredShelter` / `ExitedShelter` | actor_id, portal_id, interior_id, pos (SH2; active-space transition) |

- **why-trace** = NFR-3. Put the *selection rationale* (competing candidates, gates, costs) into `GoalSelected` / `PlanBuilt` so "why did it do this" is reconstructable.
- **SSE view** = the frontend-graphics subset (positions, actions, major events). Sensitive god-view fields (`real_stats`) are not sent over SSE (controlled by an observation-mode flag).
- **SSE transport cursor (lossless late join).** Every SSE frame carries the Redis stream entry
  ID as its standard `id:` line. A client that loaded a snapshot (whose wrapper carries
  `stream_cursor`, §1) connects with `GET /sse?cursor=<entry-id>` (or the standard
  `Last-Event-ID` header on reconnect): the server XREADs strictly AFTER that ID, so retained
  entries between the snapshot capture and the connection are REPLAYED and flow gap-free into
  the live tail (one loop, no duplicates — each frame advances the cursor). No cursor ⇒ live
  tail only (`$`, previous behavior). **Trim/gap detection**: the stream is MAXLEN-trimmed; at
  connect the server compares the requested cursor against the stream's `max-deleted-entry-id`
  (XINFO) — if entries AFTER the cursor were deleted, the server sends one
  `{"type":"StreamGap"}` control frame (no `id:` line) and closes: the client must reacquire a
  fresh snapshot/cursor pair instead of silently applying a partial sparse history. Regen
  DELETES the stream; a recreated stream's `max-deleted-entry-id` restarts, and entry IDs stay
  wall-clock monotone, so a post-regen snapshot cursor that references deleted construction
  entries is NOT a gap (nothing after it was lost) and old-world entries (all deleted, all
  smaller IDs) can never replay over a new-world snapshot.
- **`AgentFrame`** is the sparse SSE delta for agent render/status fields. It carries only changed `pos`/`goal`/`mood`/`action` fields plus `removed[]`; the REST snapshot (an AUTHORITATIVE roster: agents absent from it are gone) plus replay-after-`stream_cursor` is the lossless late-join baseline.
- **`WorldFrame`** (WI-P4) is the periodic env graphics frame the frontend renders: animal positions/actions, flora stage deltas, terrain deltas, the in-world calendar (`day_of_run`, `hour_of_day`, `minute_of_day`, `day_night`), and the ambient weather (temperature/apparent_temp, rain, snow_cover, wind). It is the SSE projection of the live render keys (§2); it carries NO god-view (`real_stats`/`tom_digest`) and NO raw drive/stat vectors. `day_night` derives from `hour_of_day`; the frontend date/clock HUD reads `day_of_run` (0-based; shown as Day N+1) + `minute_of_day` (HH:MM) live from this frame; `stage` from `length`, `width` scales the sprite. Static terrain is loaded via `/api/terrain`; `terrain_delta` carries only changed cells after that baseline. Flora that existed **before** the client connected (fixtures + already-propagated plants — no SSE event replays them) is loaded via `/api/flora` (a revision-tagged baseline, §2); each `flora_delta` entry is a **FULL render row** the reducer upserts by id (so a first-seen plant still carries `species`+`width`), and `PlantSpawned`/`PlantDied` own only the spawn/death FX (state is the baseline + `flora_delta` upsert). Emitted only when env is installed.

## 5. Determinism & versioning
- Resuming from a snapshot must be **byte-identical** to running from the start (test: `docs/core/testing.md`).
- On `schema_version` mismatch, persist refuses to load and demands a migration path.

## 6. Navmap / terrain (map subsystem)
- **Hex grid** (`docs/plans/hex-grid.md`): terrain cells are **flat-top hexagons**; the initial layout +
  `terrain_delta.cell` use an **offset (col,row) rectangular array** (`i = row·cols + col`), and
  `/api/terrain` carries `{cell_size, orientation:'flat', size:{cols,rows}, terrain[], wear?[], elevation?[]}`.
  `elevation` (∈[0,1], len cols*rows) is present only for generated worlds (worldgen GenerateTerrain) and is
  **static render-only relief** — full grid only, never in `terrain_delta`; absent ⇒ the frontend uses
  per-type heights. The wire stays a rectangular array (offset↔axial conversion is engine-internal).
  Sorted-cell order is the canonical hex order (R-major then Q). `spatial`/`scent`/`climate` stay SQUARE
  (surgical scope).
- Navmap wire = **building footprints** + **sparse `wear`** + **terrain state**. Per `docs/core/design.md §5`, terrain is **dynamic** (moisture/transition), so it streams like wear — **periodic full + sparse deltas**, NOT a one-time static layout.
- Determinism (D12): the snapshot is copy-on-write; the running tick deposits `wear` and applies terrain transitions in the **serial apply** phase (sorted cell order), never during plan. Bulk terrain recompute is `tick`-triggered (`tick % N`), never wall-clock.
- An `ore_node` exhaustion (Xm2/Xm3) is one such terrain transition: on `remaining→0` the world fires ONE `navmap.SetTerrain` over the node's cells → `depleted_terrain` (e.g. `bare_rock`), streamed via `TerrainOverrides()` (the same sparse delta channel as climate transitions). NOT during plan; apply phase, sorted cells (D12).
- Shelter/exposure render/debug wire is optional and sparse: `exposure_delta[]{cell, epsilon}` lists
  cells whose epsilon differs from 1 for the current wind sector. It is derived state; replay/resume
  rebuilds it from blockers/interiors + wind sector.
- The exact navmap snapshot shape is finalized when `engine/space/navmap` serialization lands — see `docs/plans/map.md` M5 + `docs/plans/climate.md`.

## 7. Recipes (Materials & Crafting — content, `content/recipes.yaml`)
> The recipe model is FINAL/LOCKED (`docs/plans/materials.md §0 "Recipe model — FINAL"`): slot/alternative
> inputs, `wear|consume` modes, `ambient` stations, per-recipe `duration`, `basis_stat`.
- `recipes.yaml` is **load-time content**, not a runtime cross-module wire payload — it is compiled by
  `platform/config` into the recipe registry and never serialized into the snapshot.
- Shape (schema `content/schema/recipes.schema.json`): `recipes[] { id, inputs[], ambient[]?(tags),
  duration(ticks), basis_stat(StatID), outputs[]{item, base_qty} }`. `inputs[]` is an ordered list of
  SLOTS; each slot `{ any: [alternative,…] }` is satisfied by EXACTLY ONE alternative (OR). An
  alternative is `{ tagQuery[] (AND), amount, mode: wear|consume }`:
  - `tagQuery` is a tag set the matched item must carry (D4 — any item whose `tags` ⊇ the set
    auto-qualifies); NOT an item id.
  - `consume` → remove `amount` matching items/units (most-decayed lots first, ties by ObjectID; works
    on stackable lots AND a whole durable instance — a tool consumed as material).
  - `wear` → the matched item MUST carry a `tool` block; decrement its CURRENT durability by `amount`,
    break (object-mortality) at 0, else persist (most-worn matching tool, ties by ID).
  - `ambient` = station tags the actor must be IN RANGE of (substitutable; NOT consumed; optional).
  - `basis_stat` = the StatID whose roll drives success, qty, AND the produced instance's durability
    (a produced tool's start durability = roll·`wear_max`). `outputs[].base_qty` = the pre-roll base.
- **Craftable gate** (planner/world reads the BOUND recipe): every slot has a satisfiable alternative AND
  every `ambient` station is in range. No partial run; no static action tool tag.
- `platform/config` cross-checks: every `outputs[].item` is a known item_kind; every alternative
  `tagQuery` tag is carried by ≥1 item_kind; every `wear` alternative's tagQuery is satisfiable ONLY by
  `tool`-block item_kinds; every `ambient` tag is carried by ≥1 object_kind; `basis_stat` is a known
  StatID (D10); an unreachable recipe is a load-time error.
- **Material / tool / station tags** are content fields on `objects.yaml` item_kinds/object_kinds
  (`tags: [shaft_stock, …]`), validated by `content/schema/objects.schema.json`. They are the recipe's
  matching vocabulary; no runtime wire change.
- The `Craft` action (`content/actions.yaml`) is `recipe_mediated: true` (FINAL) and carries NO tool
  tag, NO target/station field, and NO duration — it binds a recipe at plan time and is NOT in this
  file. Craft APPLY semantics (first-satisfiable alternative per slot; `consume` most-decayed-first;
  `wear` most-worn-first + break at 0; `ambient` in-range gate; `basis_stat` outcome roll + produced
  durability; fresh decay lot for a perishable output) are `engine/world`.

## 8. Decay state (Materials & Crafting — engine `decay` → persist)
> Finalized to the RESOLVED (a) shapes — `docs/plans/materials.md §1` Dm1–Dm5 all `RESOLVED: (a)`. The decay
> UNIT is a LOT; `decayAge` is a continuous effective-decay-time accumulator; the derived `state` is
> recomputed, never stored. Bump on any change.
- Decay streams like the navmap/flora fields: **periodic full + sparse age/transition/transform/gone
  deltas** (mirrors §6). The decaying-LOT set is serialized as the periodic-full source; per-step
  `decay.Step` deltas are the sparse channel.
- **Per-lot decay state (the periodic-full row, `decay_lots[]` in §1):** `{ object_id, kind, qty,
  decay_age, location }` where:
  - `decay_age` is the continuous **effective-decay-time** accumulator (Dm1(a)) — monotone ≥ 0, the one
    scalar that makes resume byte-identical (§5).
  - `state` (the derived discrete index) is **NOT stored** — it is recomputed from `decay_age` vs the
    kind's `decay.states[].threshold` thresholds (D9, mirrors flora `Stage`). Renderers/why-trace derive
    it on read.
  - `qty` is the **lot** quantity (Dm5(a)) — all `qty` items in the lot share one `decay_age`/state; lots
    never auto-merge, so a kind's `inventory` count (§1) is the sum over its live lots.
  - `location` discriminates where the lot lives (an agent inventory id / floor / storage handle) so the
    owner-agnostic lot (Dm4(a)) round-trips to the right place; world owns this mapping.
- **The transition/transform delta (per `decay.Step`, the `StepDeltas` shape):**
  - `aged[] { object_id, decay_age }` — lots whose `decay_age` advanced with no state change.
  - `transitioned[] { object_id, decay_age, state }` — lots that crossed into a new derived `state`.
  - `transformed[] { source_id, state, item, qty }` — transform products (Q3) a transition produced;
    `qty = decay.states[state].transform[].qty · source_lot.qty` (Dm5(a)). world places them where the
    source lot was.
  - `gone[] { object_id }` — lots that reached the terminal `gone` state (the only mass removal).
  - All slices are sorted by `object_id` (D12). world (sole object mutator) applies these to
    `objects[]`/inventory and emits the `Decayed` event (§4) on a transition.
- Determinism (D12): like climate/flora, `decay.Step` runs in the **serial apply** phase on the
  `tick % N` decay cadence (never wall-clock); the env (`Temperature`/`Moisture`) + the storage rate
  multiplier are world-sampled per lot and injected as values, and `decay_age += elapsedTicks ·
  baseRate · accel(temperature,moisture) · storageRateMult` (Dm2(a)).
- The exact wire encoding is finalized when `engine/env/decay` serialization lands (P_m2) — the shape above
  is fixed by Dm1–Dm5(a). See `docs/plans/materials.md §1/§2`, `backend/engine/env/decay/SPEC.md`.

## 9. Tool durability + finite sources (Materials & Crafting — FINAL/Xm1 → persist)
> Finalized to the LOCKED recipe model (`docs/plans/materials.md §0`). Tool durability + node `remaining` are
> runtime per-INSTANCE state mutated by `engine/world` (apply phase, sole mutator).
- **Tool instance (the periodic-full row, `tool_instances[]` in §1):** `{ object_id, kind, durability,
  location }`:
  - `kind` is a `tool:`-block item_kind (`crafted_tool`, `pickaxe`); `stackable:false`, so each tool is
    its own instance (NOT a `{Tag:int}` count) carrying its CURRENT durability.
  - `durability` is the tool's CURRENT durability (FINAL) — set at creation to `basis_stat roll · wear_max`
    (skilled crafters make sturdier tools, capped at the kind's `wear_max` ceiling), then DECREMENTED by
    use: a recipe `wear` alternative subtracts that alternative's `amount`; a Mine subtracts the Mine
    action's world/balance wear rate. There is NO per-item `wear_per_use` (the amount is per-recipe / a
    world rate). When `durability` reaches 0 the world REMOVES the tool (object-mortality, §7 — break =
    reaching 0) and emits `ToolBroke` (§4). One scalar to snapshot ⇒ resume byte-identical.
  - The §6 tool-quality reads `durability / wear_max` via the world's `expr.Context` operand
    `tool:<family>.quality` (Cm3) — used by the Mine yield. The `expr` L0 interface is UNCHANGED (a
    world-Context `Attr` operand, `engine/kernel/expr/SPEC.md` "callers adapt"), not a new method. (There is no
    per-item `tool.quality` formula any more — output quality comes from the crafting `basis_stat` roll.)
  - `location` discriminates which inventory/floor/storage the tool is in (parity with `decay_lots[]`).
- **Finite source (`objects[].remaining` in §1, Xm1):** an `ore_node` carries `remaining` (starts at the
  `source.initial` count). `Mine` decrements it per successful extraction; on `remaining→0` the world
  removes the node (object-mortality) + fires ONE `navmap.SetTerrain` → `source.depleted_terrain`
  (Xm2/Xm3, §6). `remaining` is object-local (D9 locality), captured in `objects[]`.
- Determinism (D12): tool durability + node `remaining` are mutated only in the **serial apply** phase,
  in fixed agent-ID then action order; per slot the FIRST satisfiable alternative is applied — `consume`
  takes most-decayed lots first (ties by `object_id`), `wear` takes the most-worn matching tool (ties by
  `object_id`). No wall-clock, no map-iteration for the apply order.
- The exact wire encoding is finalized when the Craft/Mine apply lands (P_m3/P_m4); the shape above is
  fixed by the LOCKED model. See `docs/plans/materials.md §0/§1/§2`, `backend/engine/mind/actions/SPEC.md`.

## 10. Env state (flora · animals · climate — WI-P4 → persist)
> The env subsystems serialize like decay/navmap: **periodic full + sparse per-step deltas**. All env
> blocks are **absent when env is OFF** (`world.InstallEnv`/`InstallFauna` not called) — the addition is
> backward-compatible (an old env-off snapshot loads unchanged). Bump `schema_version` on env activation.
> Owner: `platform/persist` (the writer) + `engine/world` (exposes the live state); shapes fixed here.

- **Flora (`flora[]` in §1, periodic-full):** `{ object_id, species, pos, length, width, death_streak }`
  — the live `flora.Plant` set in sorted `object_id` order (`flora.State.Plants()`). The discrete
  `stage` is **DERIVED** from `length` (not stored, D9, mirrors decay `state`); `shade` is derived from
  `width` (perception recomputes — not serialized). Per-step delta = the `flora.StepDeltas`:
  `spawned[]{…}` / `died[]{object_id}` / `grown[]{object_id, length, width, death_streak}`, sorted by
  `object_id` (the spawn/grow/die sparse channel; world emits `PlantSpawned`/`PlantDied`, §4).
- **Animals (`animals[]` in §1, periodic-full):** `{ object_id, species, pos, stats{StatID:float},
  drives{DriveID:float}, stamina, vital, vital_cap, heading, current_action, active_until,
  engaged_with?, next_exchange_tick?, engage_cooldown_until?, hidden_until?, concealment? }` — the
  live `fauna.Animal` set in sorted `object_id` order. The Phase-6 combat/hiding fields
  (`vital_cap` + the five optional engagement/hiding fields) are part of the round-trip: a snapshot
  captured mid-engagement or mid-hide must resume with that state intact (D12), not silently reset.
  `current_action`/`heading` are render-relevant;
  `stats`/`drives` are god-view (NOT sent over SSE, §4). active/dormant is DERIVED from `active_until`
  vs tick (not stored). Per-step delta = move/commit/die: `moved[]{object_id, pos, heading}` /
  `updated[]{object_id, drives, stamina, vital, current_action, active_until}` / `died[]{object_id}`,
  sorted by `object_id` (world emits `AnimalBorn`/`AnimalDied`, §4). The legacy `prey` timer-respawn
  object stays an `objects[]` row until fauna activation migrates it (W7).
- **Climate (`climate` in §1, periodic-full):** `{ cells[]{cell, moisture, temperature, terrain,
  frozen_from}, rain{raining, rain_ends_at_hour, p_rain, hours_since_rain}, wind{dir, mag}, snow_cover }`
  — the coarse `climate.State` (`Cells()`/`Rain()`/`Wind()`/`SnowCover()`) in sorted `GridCell`
  (Y-major then X) order. `snow_cover` ∈ [0,1] is the world-uniform snowpack (CS2b, plan §1d; a single
  scalar, resumed via `Restore(…, snow)` — absent on a pre-snow snapshot ⇒ 0, a snowless resume).
  `frozen_from` is the per-cell pre-freeze terrain id for `ice` cells (ICE, plan §1e; `""`/absent on
  every non-ice cell and pre-ice snapshot) — resumed so a frozen river thaws back to river, not a guess.
  `terrain` per cell IS serialized here: `CellState.Terrain` is climate's own authoritative input to
  `Rules.Eval` on every subsequent `Step` (transition source state), not merely a navmap mirror —
  omitting it would silently stop terrain transitions from firing after a resume. `temperature` is **°C**
  (CA3, may be sub-zero), `moisture` ∈ [0,1], `wind.dir` radians / `wind.mag` ∈ [0,1]. `rng_state` for
  the rain+wind process resumes byte-identically via the run's root rng fork (no separate field). The
  per-cell delta channel is the changed `cells[]` since the last full + the per-step `wind`; terrain
  transitions ride navmap `TerrainOverrides()` (§6), NOT here. `apparent_temp` is per-animal (fauna
  F40), NOT a climate field — it is derived for the SSE `WorldFrame` (§4), not stored.
- **Scent — NOT serialized.** The scent grid is derived (rebuilt from `scent:<channel>` emitter
  positions on resume), mirroring the spatial hash (§1) — no snapshot row.
- **Determinism (D12):** every env block is captured in sorted id/cell order; resume is byte-identical
  (a captured `(flora,animals,climate)` + `rng_state` at tick T reproduces T+k, §5). The exact wire
  encoding lands with `platform/persist` env serialization (`backend/platform/persist/SPEC-world.md`).
