# Decision — Flora render authority: baseline transport + render-state carrier

**Status:** RESOLVED (human, 2026-07-14); **hardened 2026-07-15** after a post-ship review — the
"spurious-FX-only micro-race" assessment in Q1 was WRONG (it is a real state-divergence window);
see **§Follow-up correction** at the bottom. Implements `docs/plans/frontend.md` §9 (FE-P7).
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
re-applies spawn/death deltas already in the baseline. **[Corrected 2026-07-15]** The original
text claimed this was only **spurious spawn/death FX** with "state stays correct because the upsert
is idempotent" — that was WRONG. The baseline is applied as a wholesale REPLACEMENT, and SSE opens
BEFORE it lands, so a `flora_delta`/`PlantDied` arriving in the pre-baseline window is either LOST
(a post-cursor spawn is overwritten by the older baseline) or RESURRECTED (a post-cursor death is
undone by the baseline re-adding the plant) — real state divergence, not just double-fired FX. See
§Follow-up correction for the fix (pre-baseline buffering). Folding flora into the snapshot blob
(same bytes, same cursor) removes that window and needs zero new bootstrap machinery.

**Human chose (a)** — mirror terrain — accepting the reasoning that:
- The intra-flush ordering (snapshot → flora → terrain → publish, all in one synchronous flush,
  `backend/main.go` `writeLive`/`writeEnvLive`) makes the TOCTOU window sub-microsecond vs the
  frontend's HTTP round-trips, and the flora loader runs only after `snapshotLoaded`, so the
  cursor/flora skew is not realistically hittable;
- flora is "snapshot-like" (mostly static, fixed position) and belongs beside terrain in the
  render-key family, not inline with the god-view snapshot blob;
- reusing the established, tested terrain baseline machinery (revision gate, capped-backoff
  retry, `TERRAIN_LOADED`-style replacement, StreamGap reacquire) keeps the two paths symmetric.

The intra-flush skew was judged too small to hit — but that reasoning does NOT cover the
SSE-opens-before-baseline window, which is HTTP-round-trip-sized, not sub-microsecond. That gap is
what the follow-up buffering fix (§Follow-up correction) actually closes; the transport choice (a)
stands, the "acceptable residual" framing does not.

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

## Follow-up correction (2026-07-15) — post-ship review, 4 findings

A code review of `fix/flora-render-baseline` found that the "residual FX-double-fire, state stays
correct" assessment in Q1 understated the risk: because SSE opens before the flora baseline lands
and the baseline is a wholesale REPLACEMENT as-of an OLDER `stream_cursor`, pre-baseline SSE flora
mutations diverged the render state (lost spawns / resurrected ghosts). Transport (a) and carriers
Q2/Q3 are UNCHANGED; the following hardening was added (human-approved, all on the same branch):

- **① Pre-baseline buffering.** While `!floraLoaded`, `flora_delta` (upsert) and `PlantDied`
  (remove) mutations buffer into `pendingFloraOps` and are replayed ON TOP of the baseline at
  `FLORA_LOADED` (mirrors the existing `pendingTerrainDeltas`), so the newer post-cursor live state
  wins. Spawn/death FX still fire immediately (FX carry no state). **This is the real fix for the
  Q1 window.**
- **② StreamGap / stale-snapshot re-arm.** Both now reset `floraLoaded:false` (+ clear
  `pendingFloraOps`) so the baseline re-fetches for the new cursor (previously flora went stale
  after a gap — symmetric with terrain now).
- **③ Explicit flora availability flag.** A `flora:"on"|"off"` flag is published in `sim:{run}:meta`
  (via `PublishWorldRevision(…, terrainOn, floraOn)`) and carried in the snapshot wrapper, mirroring
  the existing `terrain` flag. The frontend `floraStatus` gates the loader INDEPENDENTLY of terrain
  (**RESOLVED: (a) explicit flag**, human 2026-07-15) — env-off is never inferred from a failed
  `/api/flora` fetch, and a flora-off / terrain-on world (or vice-versa) is handled correctly.
- **④ Flora write gates publication.** `writeEnvLive` now returns false on a `FloraOn` `WriteFlora`
  failure (previously the error was ignored while terrain's gated), so a published `world_revision`
  always serves `/api/flora` — symmetric with terrain.

Docs corrected in the same working set: this file, data-contracts §1/§2/§3, `platform/persist/SPEC.md`
+ `SPEC-world.md`, `platform/api/SPEC.md`, `frontend/SPEC.md` (§Bootstrap step 7 + state shape + ACs),
`docs/plans/frontend.md` §9 (Follow-up hardening).
