# SPEC — `platform/persist` · Env State Serialization (flora · animals · climate · render keys)

> Status: `DRAFT`
> Sub-spec of: `SPEC.md`  ·  Owner agent: `<filled by implementer>`
> Scope = **WI-P4** (`docs/world-integration.md` §2) — the serialization/stream half. Contract:
> `docs/data-contracts.md §1/§2/§4/§6/§10`.

## Scope

Extends `platform/persist` to serialize the **env subsystems' state** (flora plants, animals,
climate field) into the `Snapshot` blob + the **Redis live render keys** (animals/flora/climate/
terrain/frame), and to drive the **`WorldFrame`** render projection the frontend consumes over SSE
(`platform/api` streams it; persist/world build the render view). It is the OUTPUT half of WI-P4 —
the **fixture-load INPUT half** (world-gen fixture → `InstallEnv`/`InstallFauna`) is a loader/
composition concern, cross-referenced below, not this sub-spec.

The env state rides on the existing `world.WorldState` (so `Snapshot{World world.WorldState}` is
unchanged in shape — `WorldState` gains the env blocks, an engine-side addition flagged below).
Everything stays **backward-compatible**: an **env-OFF** run (no `InstallEnv`/`InstallFauna`) emits
**absent/empty** env blocks and **no env render keys**, so existing snapshots/goldens are
byte-unchanged (the `SchemaVersion` bump is additive; absent ⇒ env-off).

## What persist serializes (the WI-P4 additions)

### Snapshot blob (Postgres / full god-view, data-contracts §1/§10)
`CaptureSnapshot` reads the world's env state through `world.WorldState` (the engine exposes it) and
encodes the periodic-full rows; `RestoreInto` round-trips them for byte-identical resume (D12):
- **`flora[]`** `{object_id, species, pos, length, width, death_streak}` — sorted `object_id`
  (`flora.State.Plants()`). `stage` (from `length`) and `shade` (from `width`) are DERIVED, not stored.
- **`animals[]`** `{object_id, species, pos, stats{}, drives{}, stamina, vital, heading,
  current_action, active_until}` — sorted `object_id`. active/dormant derives from `active_until`.
- **`climate`** `{cells[]{cell, moisture, temperature°C}, rain{...}, wind{dir, mag}}` — sorted
  `GridCell` (Y-major/X). The rain+wind process resumes via the run's root `rng_state` (no separate field).
- **Scent is NOT serialized** — derived state, rebuilt from `scent:<channel>` emitter positions on
  resume (spatial-hash parity, §1). `RestoreInto` triggers the world's scent-grid rebuild (world owns it).
- **Dynamic terrain** rides the navmap `TerrainOverrides()` sparse delta (§6) the navmap blob already
  carries — persist does NOT duplicate it under climate.

### Redis live keys (render-visible only, data-contracts §2)
Written each render cadence from the live world; **god-view excluded** (`stats`/`drives`/`vital` are
NOT in the live keys, only the snapshot blob):
- `sim:{run}:animal:{id}` HASH — `pos, species, action, heading, stamina`
- `sim:{run}:flora` STRING — `[{object_id, species, pos, stage, width}]` (`stage` DERIVED from `length`)
- `sim:{run}:climate` HASH — `temperature, apparent_temp?, moisture, raining, wind_dir, wind_mag,
  hour_of_day, day_night, year_fraction` (`day_night` DERIVED from `hour_of_day`)
- `sim:{run}:terrain` STRING — base layout + overrides (climate transitions) + wear (trails)
- `sim:{run}:frame` STREAM — the per-frame `WorldFrame` deltas (below); SSE tails it
These keys exist only when env is installed; absent ⇒ env-off.

### `WorldFrame` (the SSE graphics frame, data-contracts §4)
The frontend graphics projection — built from the live render keys, streamed by `platform/api` over
SSE (persist/world supply the render view; api owns the HTTP stream):
```
WorldFrame { tick, hour_of_day, day_night, temperature, apparent_temp?, raining, wind{dir,mag},
             agents[]{id, pos, action}, animals[]{id, pos, species, action, heading},
             flora_delta[]{id, pos, stage}, terrain_delta[]{cell, terrain, wear} }
```
- **God-view EXCLUDED** (no `real_stats`/`tom_digest`/raw drives) — the observation-mode boundary
  (data-contracts §4). `day_night` from `hour_of_day`, `stage` from `length`, `apparent_temp` derived
  for display (fauna F40), none stored.
- `apparent_temp` is optional (only when fauna is active and a representative value is meaningful).

## Versioning & determinism

- `SchemaVersion` bumps +1 on env activation (data-contracts §0/§10). The bump is **additive**: a
  pre-env blob (no `flora`/`animals`/`climate`) still decodes (the fields default to absent ⇒ env-off),
  so `Decode`/`RestoreInto` do NOT reject an old env-off blob — only a true shape break rejects
  (`ErrSchemaMismatch`). (Decision: env blocks are optional/omitempty so env-off snapshots are
  byte-identical to today.)
- **Resume is byte-identical (D12, §5):** a captured `(flora, animals, climate)` + `rng_state` at tick
  T reproduces T+k; the scent grid + spatial hash + shade are rebuilt from positions (derived), never
  from a serialized copy. Encoding is byte-stable (sorted ids/cells, sorted map keys, §0).
- Env render keys are written in the same render cadence as the existing agent keys; no wall-clock.

## Acceptance Criteria (testable)
- [ ] **Env-OFF byte-identity** — a run without `InstallEnv`/`InstallFauna` produces a `Snapshot` blob
  byte-identical to the pre-WI-P4 encoding (env fields omitted), and writes NO env Redis keys; existing
  persist goldens hold. `Decode` of a pre-env blob succeeds (no `ErrSchemaMismatch`).
- [ ] **Flora/animals/climate round-trip** — `CaptureSnapshot` → `Encode` → `Decode` → `RestoreInto`
  reproduces `flora.State`/the animal set/`climate.State` exactly (sorted ids/cells); a subsequent tick
  is byte-identical to an uninterrupted run (resume invariant, §5).
- [ ] **Scent rebuilt, not serialized** — the blob contains NO scent grid; after `RestoreInto` the
  world's scent grid is rebuilt from emitter positions and the next tick's fauna reads match the
  uninterrupted run.
- [ ] **God-view boundary** — `animal` live keys + `WorldFrame` carry NO `stats`/`drives`/`vital`/
  `real_stats`; those appear ONLY in the Postgres blob (grep/struct guard, parity with agent keys).
- [ ] **Derived views on the render side** — `flora` live key + `WorldFrame.flora_delta` carry `stage`
  (= `Rules.Stage(length)`), `climate`/`WorldFrame` carry `day_night` (from `hour_of_day`); none is read
  from a stored field (recompute-on-write).
- [ ] **Climate °C + wind round-trip** — `temperature` serializes as °C (sub-zero allowed, no clamp),
  `moisture` ∈ [0,1], `wind{dir radians, mag [0,1]}`; rain process resumes via `rng_state`.
- [ ] **Determinism golden** — with env installed, a fixed `(seed, N ticks)` yields a byte-identical
  env-inclusive snapshot digest across runs/processes; a second run reproduces it.

## Out of Scope
- **The engine-side `world.WorldState` env fields + the world's render-view accessors** → `engine/world`
  (the env state is owned there, SPEC-world-env/fauna; persist only READS via `world.State()` and writes
  via `world.RestoreState`). The exact `WorldState` field additions are an engine SPEC item (flagged).
- **The SSE HTTP stream + observation-mode flag** → `platform/api` (it tails `sim:{run}:frame` and
  applies the god-view filter; this sub-spec fixes the `WorldFrame` SHAPE, data-contracts §4).
- **The fixture-load INPUT half** (world-gen / scenario fixture → terrain layout + placed plants/
  animals → `navmap.New`/`climate.New`/`flora.New`/`decay.New` → `world.InstallEnv`/`InstallFauna`) →
  the composition layer + `backend/tools/worldgen` + `platform/config` fixture loader (W9). persist is
  OUTPUT only. The contract: the loader builds the env state, the world installs it, persist captures it.
- **Navmap/terrain serialization** (base layout + `TerrainOverrides` + wear) → already
  `docs/data-contracts.md §6` + the navmap blob; this sub-spec only references it (climate transitions
  ride that channel, not a duplicate climate-terrain field).
- **Content compile / Rules building** → `platform/config` (`SPEC-world.md`, WI-P0).

## Open Questions
> `docs/world-integration.md` W1-W9 RESOLVED. Remaining are wiring shapes, not mechanism choices:
- **`world.WorldState` env-field additions (engine seam, WI-P4 prerequisite) — ✅RESOLVED (b), 2026-06-28: the world exposes a `RenderView()` (live-key/WorldFrame projection) + full state for the blob; the god-view filter lives in ONE place (world), persist just writes.** persist serializes
  whatever `world.WorldState` exposes; the world must add `Flora`/`Animals`/`Climate` to its
  serializable state + a render-view accessor (the live-key/WorldFrame projection). Options: **(a)**
  the world exposes the raw env state on `WorldState` and persist builds both the blob + the render
  projection; **(b)** the world exposes a separate `RenderView()` for the live keys/WorldFrame and the
  full state for the blob. **rec: (b)** — keeps the god-view filter in ONE place (the world builds the
  render projection; persist just writes it), mirroring how it already exposes `Snapshot()` vs the
  god-view. **Flag to the `engine/world` owner.**
- **`SchemaVersion` bump value + omitempty policy.** Env blocks are `omitempty` so env-off blobs are
  byte-identical; the bump is taken at activation. Confirm the JSON tags are `omitempty` and that
  `Decode` treats absent env blocks as env-off (not an error). Non-blocking; settle at impl.

## Notes
- This sub-spec mirrors `SPEC.md`'s split: the **blob** is the full god-view (Postgres, replay source),
  the **live keys** are the render subset (Redis), the **`WorldFrame`** is the SSE graphics frame — env
  state slots into all three exactly as agent state already does. The only new principle is that env
  discrete views (`stage`/`day_night`/active-dormant/`apparent_temp`) are DERIVED on the render side,
  never stored (D9, decay-`state` parity).
- The scent grid + spatial hash + flora shade are all derived (rebuilt from positions on resume) — a
  consistent "positions are the source of truth, indices are rebuilt" rule across the world.
- Reference paths: `docs/data-contracts.md §1/§2/§4/§6/§10` (the contract), `SPEC.md` (Snapshot/Encode/
  Decode/Capture/Restore + Redis keyspace), `backend/engine/world/SPEC-world-env.md` +
  `SPEC-world-fauna.md` (the env state owners), `backend/platform/api/SPEC.md` (SSE stream),
  `docs/world-integration.md` (WI-P4, W8/W9).
