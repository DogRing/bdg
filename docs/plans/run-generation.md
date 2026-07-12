# Run Generations / Multi-World — DEFERRED design notes

> Status: `DEFERRED` (2026-07-11) — **NOT the next implementation task. Do not start any phase
> in this file.** The supported contract today is the **current single-world development mode**
> (§1). This file only preserves the chosen direction and the open design questions for a future
> multi-world phase; nothing below is an accepted contract.

## 0. Decision record — why deferred

- 2026-07-11 (a): when the regen cross-store failure policy was reviewed, the human chose
  "regen creates a NEW run and PRESERVES the old run's data" as the preferred long-term
  direction over hardening cross-store deletion (Redis+Postgres can never be one transaction;
  *not deleting* dissolves the problem).
- 2026-07-11 (b): the human **deferred the implementation**. Reasons:
  - the application currently exposes **exactly one world** to the user — there is no world
    catalog, no world-selection screen, no historical-world resume UI;
  - generation behavior should be designed **together with the future world-selection layer**
    (UI + lifecycle + data model), not as an isolated persistence change;
  - deciding run-ID shapes, current-run pointers, SSE switching, and old-run lifecycle
    independently now would **prematurely lock the data model**.
- Until the multi-world phase is activated, the single-world regen path
  (`persist.ResetRunData` + best-effort Redis cleanup) **is** the supported implementation.
  Do not replace it with generation-based storage now.

## 1. Current single-world contract (authoritative; mirrors persist/api SPECs + data-contracts §3)

- Exactly **one active world** is presented to the user.
- `POST /api/restart` = debugging rewind: rebuild with the original seed, tick/state reset,
  **Postgres history preserved** (append-only; `LatestSnapshot` = storage recency, not tick).
- `POST /api/regen` = replace the current world under the **same run_id** with a new seed:
  Postgres reset is ONE transaction (`ResetRunData`; failure aborts the regen, old data intact);
  Redis cleanup is **best-effort** — an accepted, explicitly documented limitation of this
  testing phase (a stale per-entity hash can linger if its DEL fails).
- After a regen **all frontend viewers are expected to reload** (the initiating client reloads
  automatically AFTER a NEW `world_revision` is published and the revision-tagged baselines are
  verified — api SPEC `POST /api/regen`); there is **no live multi-viewer resynchronization
  signal**. A `202` regen response means the request was *accepted* — not that the rebuild
  succeeded or any client has resynchronized.
- There is **no** run-generation pointer, **no** automatic SSE switch between runs, **no**
  MapReset event, **no** world selector.
- **`world_revision` (data-contracts §2) is NOT a run generation.** It is an operational
  readiness marker for the current single-world mode: one run_id, one active world, and the
  marker only identifies which published map revision the live baselines belong to (regen
  publishes the next value AFTER the fresh baselines are servable; restart never changes it;
  a process boot claims stored+1). It is not user-selectable, resolves no world identity,
  points at no run, and switches no SSE stream — the future `world_id`/`run_id`/generation
  split (§2 below) remains DEFERRED and unimplemented.

## 2. Future multi-world phase — candidate model (design notes only, NOT contracts)

Candidate identity split (names are design candidates, not public contracts):

- `world_id` — persistent, user-selectable world identity;
- `run_id` — one execution session/generation of a world;
- `current_run_id` — the active run of a selected world.

These must be designed **together**, in one coordinated phase spanning backend, API/SSE,
persistence, and frontend — the frontend WILL need changes (world creation/selection UI,
baseline reacquisition on switch); do not assume it stays untouched. The phase must cover:
world lifecycle states, pause/resume, previous-world retention and deletion, per-world/per-run
Redis namespaces, SSE subscription changes when selecting or switching worlds, snapshot/event
retention by world and run, and permissions/ownership if multiple users are introduced.

Explicitly corrected from earlier drafts of this file:

- **Existing SSE connections do NOT automatically switch generations.** Whether a connected
  viewer stays pinned to its old run (frozen view), receives an explicit resubscribe/reset
  signal, or must reload is an **unresolved design question** (G9) — earlier text claimed both
  "viewers watch the frozen old run" and "SSE switches streams when the pointer moves"; neither
  is a contract.
- No phase may write a current-run pointer while the pointer's design (G2) is unresolved.
- Activation ordering (how the single-world interim migrates to multi-world, when
  `ResetRunData` is retired) is itself an open question (G10), not an implied sequence.

## 3. Open questions — ALL deferred to the multi-world phase

> Every item is `OPEN (deferred)`. The "(rec)" marks are prior design notes preserved for the
> future discussion — they are NOT accepted resolutions.
> Numbering note: **G8 is intentionally retired** (no gap to fill) — an earlier draft's
> viewer-resync question whose contradictory notes were superseded by G9; the number is not
> reused so §2's G9/G10 references stay stable.

- **G1 — run id shape**: (a) suffix `{base}-g{N}` (monotone, human-readable — rec note);
  (b) `{base}-{timestamp}`; (c) opaque uuid. Constraint: no `:` (keyspace separator hygiene).
  Must be co-designed with the `world_id`/`run_id` split (§2).
- **G2 — current-run discovery for readers**: (a) Redis pointer key (works for the standalone
  `cmd/sse` deployment — rec note); (b) in-process callback; (c) Postgres `runs` catalog scan.
- **G3 — process-restart behavior**: resume the latest run vs always start fresh; interacts
  with world lifecycle states and pause/resume (§2).
- **G4 — old-run disposal**: finalize + expire Redis keys vs TTL; retention/deletion policy is
  a product decision once worlds are user-visible.
- **G5 — events stream + seq per run**: per-run stream key and seq restart at 0 (rec note);
  the event buffer must be flushed to the OLD run before any switch (no cross-run leakage).
- **G6 — Postgres retention across runs/worlds**: keep-forever vs cap; per-world quotas.
- **G7 — runs terminal status vocabulary**: reuse `"completed"` vs add `"superseded"`.
- **G9 — connected-viewer behavior on world switch/regen**: pinned frozen view vs explicit
  reset/resubscribe signal vs mandatory reload (supersedes the earlier contradictory notes).
- **G10 — activation ordering/migration**: how the single-world interim path is retired and in
  what order backend/API/SSE/frontend changes land.

## 4. Phases — DEFERRED (do not start)

The earlier P1 (backend run switch) / P2 (reader resolution) / P3 (old-run disposal + docs)
sketches remain useful as a decomposition idea, but **must not be started** until the
multi-world phase is activated and §3's questions are resolved through the normal
Open-Question gate. They are intentionally not detailed here to avoid implying an accepted
build order.

## 5. Invariants that must survive any future design

- D12 untouched: run/world switching is platform wiring; the engine sees only the injected
  seeded RNG. Seq stays deterministic per simulation process lifetime (single-writer fan-out
  stamp; today it is process-scoped, NOT durable across process restarts — data-contracts §4
  "Scope". Making sequence identity durable per run/world is itself part of this deferred
  design, see G5).
- God-view boundary unchanged (per-run keyspaces carry the same shapes).
- Each run remains reproducible from its `seed + config_hash + last snapshot`
  (data-contracts §3, unchanged per run).
