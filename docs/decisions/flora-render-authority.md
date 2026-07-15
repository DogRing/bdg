# Decision — Flora render authority: baseline transport + render-state carrier

**Status:** RESOLVED (human, 2026-07-14). Implements `docs/plans/frontend.md` §9 (FE-P7).
**Context:** Fixture (initial) plants never emit `PlantSpawned` (only `flora.Step`'s propagation
does), and the SSE `flora_delta` carried only `{id,pos,stage}` — no `species`/`width`. So the
frontend had no way to learn about plants that existed before it connected, and a first-seen
grow delta could not pick or scale a sprite. The last render commit hid the `objects[]` plant
squares and drew an (always-empty) `state.flora`, so plants vanished entirely.

## Q1 — Flora baseline transport

Fixture + already-propagated plants are replayed by no SSE event, so they need a REST baseline.

- **(a) — separate `GET /api/flora`, mirroring `/api/terrain`** — expose the existing
  `sim:{run}:flora` key (now a `FloraDoc {world_revision, flora:[…]}`), revision-tagged;
  the frontend fetches it once per revision after the snapshot and applies it as an
  authoritative replacement. **← RESOLVED (human).**
- (b) — fold flora into `/api/snapshot` — **REJECTED.**

### Why (b) was argued, and why (a) was chosen anyway

The reviewer recommended (b): flora is a *cursor-consistent live entity set* (SSE `flora_delta`/
`PlantSpawned`/`PlantDied` mutate it, exactly like the agent roster that already rides the
snapshot). A separate endpoint means `world_revision` guarantees only same-*world*, not
same-*cursor*: if a backup flush lands between the snapshot read and the flora read, the flora
baseline is one interval ahead of the snapshot's `stream_cursor`, and SSE replay from the cursor
re-applies spawn/death deltas already in the baseline → **spurious spawn/death FX** (state stays
correct because the upsert is idempotent, but FX double-fire). Folding flora into the snapshot
blob (same bytes, same cursor) removes that window and needs zero new bootstrap machinery.

**Human chose (a)** — mirror terrain — accepting the reasoning that:
- The intra-flush ordering (snapshot → flora → terrain → publish, all in one synchronous flush,
  `backend/main.go` `writeLive`/`writeEnvLive`) makes the TOCTOU window sub-microsecond vs the
  frontend's HTTP round-trips, and the flora loader runs only after `snapshotLoaded`, so the
  cursor/flora skew is not realistically hittable;
- flora is "snapshot-like" (mostly static, fixed position) and belongs beside terrain in the
  render-key family, not inline with the god-view snapshot blob;
- reusing the established, tested terrain baseline machinery (revision gate, capped-backoff
  retry, `TERRAIN_LOADED`-style replacement, StreamGap reacquire) keeps the two paths symmetric.

The residual FX-double-fire risk is bounded to a micro-race and judged acceptable.

## Q2 — Flora render-state carrier (which event owns the plant's render state)

- **(a) — `flora_delta` is the sole authoritative state writer (full `{id,species,pos,stage,width}`
  row upsert by id); `PlantSpawned` = spawn FX only; `PlantDied` = remove + death FX.**
  **← RESOLVED (human).** State converges from baseline + full-row upsert even with no initial
  state or partial replay; `PlantSpawned` carries no `width`, so keeping it out of the state path
  avoids a width-0 flash. Cost: every grow-cadence delta carries `species`+`width` (modest).
- (b) — make `PlantSpawned` self-sufficient (carry `stage`+`width`), leave `flora_delta` grow-only.
  Rejected: leaves two half-complete flora wire shapes and a per-spawn special case.

## Q3 — 3D flora width (note, not a gate)

No code change: the 3D billboard height formula (`frontend/src/gl/worldGL.ts`,
`worldH = min(6, 0.8 + 0.6·col + 0.8·width)`) already includes a `width` term — it read 0 only
because `width` was never delivered. Delivering `width` via the baseline + full-row delta makes
both the 2D (`Math.max(MIN_PX, width·sx)`) and 3D paths reflect real plant size automatically.

## Consequences

- `sim:{run}:flora` shape: bare array → `FloraDoc {world_revision, flora:[…]}`
  (`persist.WriteFlora(ctx, run, FloraDoc)`), written whenever flora is INSTALLED (empty
  `flora:[]` ⇒ installed-but-empty 200, key absent ⇒ 404). `RenderView.FloraOn` gates the write.
- `GET /api/flora` route (writer server only, not `NewSSE`) forwards the bytes verbatim.
- `WorldFrame.flora_delta[]` gains `species`+`width` (full render row per entry).
- Frontend: `FLORA_LOADED` action + `floraLoaded` flag + `loadFlora`/`fetchFloraWithRetry`/
  `parseFloraDoc`, mirroring the terrain loader; `PlantSpawned` reducer becomes FX-only.
- Docs updated in the same working set: data-contracts §2/§4/§API, `platform/persist/SPEC-world.md`,
  `platform/api/SPEC.md`, `frontend/SPEC.md`, `docs/plans/frontend.md` §9.
