# SPEC — `frontend/dev`

> Status: `APPROVED` (plan: `docs/frontend-plan.md`, Q1/Q7 RESOLVED 2026-07-02)
> Dev-only tooling — never bundled into `dist/`, never imported by `src/`. Node ≥ 20, **zero npm
> dependencies** (std `node:http`, `node:zlib`, `node:fs`). Parent: [`frontend/SPEC.md`](../SPEC.md).

## Purpose

Two standalone scripts that unblock frontend development while backend WI-P4 (`WorldFrame`
emission) does not exist yet:
1. **`mock-server.mjs`** (Q7) — a contract-parity fake backend: serves the REST initial-load
   endpoints and an SSE stream whose events **byte-match the shapes in `docs/data-contracts.md §4`**
   (field names, casing, nesting — the whole point is that the real backend can replace it with
   zero frontend changes). Supersedes the drifted prototype `frontend/mock-server.js`
   (`world.Agents` casing, `/mock-api/*` paths — deleted on landing).
2. **`generate-spritesheets.mjs`** (Q1) — regenerates the placeholder PNG spritesheets in
   `frontend/public/assets/` from code, deterministically, so placeholder art is reproducible,
   diff-stable, and trivially replaceable by real art.

## `mock-server.mjs`

- **Run**: `node dev/mock-server.mjs [--port 8080] [--seed 42] [--tick-ms 500]`; also
  `--dump N` prints the first N ticks' events as JSON lines and exits (no server) — the
  determinism-AC harness.
- **Routes** (same paths/shapes the real `platform/api` serves — no `/mock-*` prefixes):
  - `GET /api/snapshot` — `{tick, agents:[{id,pos,goal,action,mood}], objects:[{id,kind,pos}]}`.
  - `GET /api/terrain` — the Q5 · hex shape: `{cell_size, orientation:'flat', size:{cols,rows},
    terrain:[...], wear:[...]}` — a flat-top hex offset(col,row) array (`i=row·cols+col`, mirrors
    navmap hex.go) with a small authored map: **river (N–S band), soil (grass), forest (NE), sand
    (village fields)**. `terrain_delta.cell` is the offset index (not `{x,y}`).
  - `GET /sse` — `text/event-stream`; every `tick-ms` emits one `TickDone` and one `WorldFrame`
    (data-contracts §4 keys: `hour_of_day, day_night, temperature, raining, wind{dir,mag},
    agents[], animals[]{id,pos,species,action,heading}, flora_delta[], terrain_delta[]`), plus
    scripted lifecycle events.
  - `POST /api/restart` — parity with the real api's restart control route: resets the scripted
    world (tick 0, re-seeded PRNG, initial animal/flora/agent/wear state) and responds
    `202 {"status":"restarting"}`, mirroring the backend's deterministic fixture rebuild.
  - `POST /api/regen` — parity with the real api's regen control route: re-rolls the mock seed
    (optional `?seed=` pins it), rebuilds the terrain grid + scripted state from the new seed and
    responds `202 {"status":"regenerating"}`, mirroring the backend's new-seed world rebuild.
- **Scripted scenario** (seeded PRNG from `--seed`; deterministic given seed): deer herd grazing →
  a wolf hunts (action `hunt` → pose attack near contact) → one `AnimalDied` → respawn later via
  `AnimalBorn`; plants `PlantSpawned`, `stage` increments (grow FX), one `PlantDied`; a full
  day-night cycle + a rain spell; agents wander between objects. Exercises every motion/FX path.
- **Invariants**: shapes/casing byte-match data-contracts §4 (a fixture test in `src` may import
  the same JSON the mock emits); never emits god-view fields (`real_stats`/`drives`/`stats`);
  the only mutating routes are `POST /api/restart` and `POST /api/regen` (contract parity with
  the real api); no npm deps.

## `generate-spritesheets.mjs`

- **Run**: `node dev/generate-spritesheets.mjs` → writes `frontend/public/assets/fauna/*.png` +
  `frontend/public/assets/flora/*.png` per the assets-SPEC layout.
- **Output** (placeholder pixel-art, top-down, facing +x at heading 0):
  - `fauna/deer.png`, `fauna/wolf.png` — 32×32 frames, 4 columns, 6 rows in pose order
    `idle, walk, run, eat, attack, dying` (matching the manifest's `PoseClip.row` values).
  - `flora/tree.png`, `flora/bush.png` — 32×32 frames, 1 row, 4 columns = growth stages.
- **Implementation**: draws RGBA pixel buffers with tiny shape helpers (rect/ellipse/line) and
  encodes PNG manually (`node:zlib` deflate + CRC32 chunks) — **no image library**.
- **Invariants**: deterministic — same script version ⇒ byte-identical PNGs (fixed palette, no
  randomness, fixed zlib level); frames within a pose row differ (leg offsets / lunge) so animation
  is visible; every sheet dimension = `frameW*cols × frameH*rows` exactly.

## Acceptance Criteria

- [ ] `curl /api/snapshot`, `/api/terrain` return the documented shapes; `/sse` streams
  `data: {...}\n\n` frames including `TickDone`, `WorldFrame`, and at least one of each lifecycle
  event within the scripted loop; two runs with the same `--seed` emit identical event sequences.
- [ ] `vite.config.ts` dev proxy pointed at the mock ⇒ the app renders terrain, animals with
  walk/run/eat/attack motion, spawn/death/grow FX, day-night + rain, with **zero** `src/` changes.
- [ ] Running `generate-spritesheets.mjs` twice produces byte-identical files; each PNG parses
  (valid signature/IHDR/IDAT/IEND) and has the exact expected dimensions.

## Out of Scope

- The manifest/loader that *consumes* the sheets → [`src/assets/SPEC.md`](../src/assets/SPEC.md).
- Real backend emission (WI-P4), `/api/terrain` server implementation → `backend/platform/api`.
- Art quality — placeholders communicate pose/species/stage, nothing more.
