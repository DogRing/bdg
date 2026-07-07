# Hex Grid — Implementation Plan (terrain visual → hexagonal)

Concept & rationale: this file. Sits beside `docs/map-plan.md` (that plan built the square grid; this
one migrates the **rendered** grid to hex for aesthetics). It is the **roadmap**: scope, phasing,
per-module integration deltas, determinism/perf, wire, frontend, cross-module consistency, and the
risk playbook. It does **not** restate the SPECs (those are edited in Phase H1, SPEC-first).

> **Goal (author-stated):** the terrain in the viewer should render as **hexagonal tiles** — a purely
> visual/aesthetic motivation. Everything else follows from doing that with the least blast radius.
>
> **D11 is NOT touched.** Hex vs square is the *shape of an index cell*, not "is the world tiled."
> Agents/objects stay continuous `Vec2`; every grid remains an index that computes over continuous
> space. No `docs/design.md` invariant edit / approval is required.

## 0. Decisions locked

- **Surgical scope (the key move).** The backend has **four independent grid coordinate systems** +
  a frontend mirror; they share no cells, only continuous positions. So only the *user-visible* one
  goes hex:
  - **→ HEX:** `engine/space/navmap` (owns the rendered terrain layer + cost/footprint/wear),
    `engine/space/pathfind` (has **no own grid** — it operates on `navmap.Cell`), the **terrain
    layout representation** (`tools/worldgen` + fixtures), the **wire** (`/api/terrain` +
    `WorldFrame.terrain_delta`), and the **frontend render**.
  - **→ STAY SQUARE (unchanged):** `engine/space/spatial` (proximity accelerator — `NearbyEntities`
    does an exact `DistSq ≤ r²` filter after a coarse bucket gather, so bucket shape is invisible and
    results are byte-identical either way), `engine/space/scent` (invisible smell field; hex would be
    marginally more isotropic but is a pure-quality option, deferred), `engine/env/climate` (coarse
    weather grid; cell shape irrelevant to weather).
  - Rationale: this leaves the determinism-heavy scent/spatial/climate goldens **untouched** and
    roughly halves the change surface, while fully delivering the visual goal.
- **`navmap` is the single hex authority.** Orientation, neighbor set/order, cell↔pixel mapping,
  `cellSize`↔hex-size relationship, and the D12 canonical sort live in **one** place (navmap) and are
  **consumed** by pathfind and (via the wire) the frontend — never re-derived. Drift here is the #1
  latent bug; an interface + guard test enforces it (Phase H1/H3).
- **`terrainAt(Vec2)→Tag` stays the shared navmap↔climate bridge** (`tools/worldgen` builds it once;
  `navmap.New` and `climate.New` both take it — architecture.md §build). Its **signature is
  unchanged**, so a hex terrain *layout* does not break the navmap/climate "grids agree at t=0" rule:
  each grid samples the same continuous field at its own cell centers.
- **`content/terrain.yaml` is unaffected** — it is a TYPE catalog (attributes per terrain id), not a
  layout. No schema change there.
- **Building footprints (M3) are out of scope now** — `StampFootprint` has **no production `world`
  caller yet** (tests only); the footprint *layer* mechanism stays, only test coords change.
- **Everything lands on a branch.** The sim is green + live (game.dogring.kr); `main` is untouched
  until backend + frontend are both green and live-parity is checked.

## 1. The four grids (why they are separate — the fact that makes this surgical)

| Grid | cellSize source | Per-cell payload | Persisted? | Hex? |
|------|-----------------|------------------|-----------|------|
| `navmap` | `NavmapCellSize` (path fidelity) | terrain base_cost / passable / footprint / wear | ✅ | **HEX** |
| `spatial` | `spatial_hash_cell` = 8.0 (≈perception radius) | entity IDs in the bucket | ❌ (rebuilt) | square |
| `scent` | `ScentCellSize` (∝ smell radius) | per-channel scalar intensity | ❌ (rebuilt) | square |
| `climate` | `GridCols×GridRows` (coarse; 1 clim cell = many nav cells) | moisture / temperature / terrain | ✅ | square |

`pathfind` reuses `navmap.Cell` (no own grid). Frontend `TerrainGrid` mirrors navmap terrain for draw.

## 2. Phases (each independently shippable; SPEC-first; golden-gated; on the branch)

### H0 — Plan + resolve Open questions (§6). **Hard stop:** no SPEC edit until Q0–Q2 + Q5 RESOLVED.

### H1 — SPEC-first (no code) — ✅ DONE 2026-07-06 (+ consistency pass found world/persist/api/climate-bridge deltas)
Edit, in this order, then a `reviewer` coherence pass:
- `engine/space/navmap/SPEC.md` — `Cell` = hex axial semantics; `CellOf`/`CellCenter` hex mapping;
  **new `Neighbors(c) []Cell`** (the single neighbor authority pathfind consumes); `StepCost` = uniform
  6-neighbor distance (the √2-diagonal case disappears); bounds; **canonical hex sort** replacing
  "Y-major then X" for `ActiveWear`/`TerrainOverrides`/`Decay`. Footprint/`SetTerrain`/wear keep shape.
- `engine/space/pathfind/SPEC.md` — neighbor source = `navmap.Neighbors` (drop the hardcoded
  `neighbourDirs`); tie-break order → canonical axial; **heuristic unchanged** (Euclidean × MinCostFactor
  stays admissible under hex); string-pull/`lineClear` sampling note.
- `frontend/SPEC.md` + `frontend/src/render/SPEC.md` — `TerrainGrid` hex shape + hex raster/draw; the
  wire carries **orientation + size** so the frontend never hardcodes the convention.
- `docs/data-contracts.md` — `/api/terrain` + `WorldFrame.terrain_delta` `cell` encoding + grid shape.
- `backend/tools/worldgen/SPEC.md` — `TerrainLayout` hex representation (see Q5).
- `docs/map-plan.md` (substrate note cross-link), `docs/glossary.md` (cell vocabulary).

### H2 — Hex coordinate primitive + tests (leaf; TDD; **highest bug risk → land rock-solid first**) — ✅ DONE 2026-07-06 (`engine/space/navmap/hex.go`+`hex_test.go`, 6 property/oracle tests green, navmap still square-green)
Small pure helper (navmap-owned unless a second hex consumer appears): `pixelToHex` (+ `cubeRound`),
`hexToPixel` (cell center), `neighbors`, `distance`, in-bounds, canonical sort. Tests:
- **round-trip** `hexToPixel(pixelToHex(p))` lands in p's cell for random points (± several sizes),
- **brute-force oracle** for neighbor set + distance (mirrors `spatial`'s oracle test),
- negative/large coords bucket correctly; determinism digest; no RNG/wall-clock.

### H3 — `navmap` on the hex primitive — ✅ DONE 2026-07-06 (Cell{Q,R}, CellOf/CellCenter/StepCost/inBounds/sort on hex.go + `Neighbors`; navmap_test + goldens re-baselined, 21 green, vet clean. Note: Cell rename cascades compile errors into pathfind (H4) + world (H6) — expected, fixed in those phases.)
Rewire `CellOf`/`CellCenter`/`StepCost`/bounds/sort onto H2; add `Neighbors`. Footprint/`SetTerrain`/
`TerrainOverrides`/`ActiveWear`/wear keep their contract (only `Cell` semantics change).
Re-baseline `navmap_golden_test.go` **with behavioral asserts** (cost ratio 2×, impassable=+Inf,
door passable, wear→WearCostMin, decay→0, SetTerrain transitions) — not just "bytes changed."

### H4 — `pathfind` on hex navmap — ✅ DONE 2026-07-06 (neighbours from `navmap.Neighbors` (dropped hardcoded 8-dir), tie-break R-then-Q, Euclidean heuristic unchanged; tests rebuilt with world-region walls, 8 green. `engine/space/...` now a fully green hex-core unit; scent/spatial still square & green = scope held.)
Neighbor set from `navmap.Neighbors` (6); tie-break canonical; string-pull/`lineClear` hex-aware
sampling (`cellSize` already derived from `CellCenter` deltas). Update wall-scenario test coords
(`StampFootprint(... Cell ...)`). Heuristic untouched. Assert: routes around a wall, uses a door,
unreachable stays unreachable, budget cap holds.

### H4.5 — navmap offset-layout API (foundation for H5/H6/H7) — ✅ DONE 2026-07-06
`navmap_offset.go`: `Orientation()`, `OffsetDims()`, `OffsetToCell(col,row)`, `CellToOffset(c)` — the
hex authority's offset(col,row)↔axial bridge that worldgen (layout sampler), world (render view +
terrain_delta), and persist/api all consume instead of re-deriving the flat-top odd-q convention.
Tested green. Also: navmap.go split → `navmap_snapshot.go` (≤400-line rule); stale "Y-major" comments
fixed; `Neighbors`-alloc noted as an H8 perf follow-up. **+ pure Config helpers** `OffsetDimsOf(cfg)` /
`OffsetIndexAt(cfg,p)` added later (H5 needed offset indexing BEFORE a NavMap exists — New samples
terrainAt at construction); instance methods delegate to them → one convention. SPEC Public Interface +
the (previously contradictory) "offset lives at the wire edge" note reconciled to "owned here".

### H5 — terrain layout (`tools/worldgen`) + fixtures — ✅ DONE 2026-07-06
`terrainSampler(fx, navCfg)` now snaps point→hex via `navmap.OffsetIndexAt` then indexes the offset
`Cells[row*Cols+col]` (was square `floor` bucketing); `validateTerrain` checks `Cols/Rows ==
navmap.OffsetDimsOf(navCfg)` (1:1 authoring↔navmap). Fixtures regenerated to flat-top hex offset dims:
`starter_village` 16×16→**12×11**, `predation_arena` 8×8→**7×6**. **River re-authored to follow hex**
(user decision; Kenney hex river tileset `frontend/public/origin/kenney_hexagon-kit` — a river is a
connected hex chain): at circumradius=12 the hex grid is coarser than the old square grid so the thin
straight-column river was re-laid as a **connected hex chain down an even column** (river-straight),
verified via `navmap.Neighbors`. 3 placements hex-snapped (unavoidable at the deferred cell size — a hex
is ~24u wide): `water_source` onto the river column (x99→108), riparian oaks 15/16 onto the east bank
(x114→126). No world **scenario** goldens use these fixtures (world tests use their own testEnvConfig),
so only worldgen goldens were affected; full backend green (1126 tests). Cell-size tuning (Q3, deferred)
will regenerate fixtures again — the resample/author tooling pattern is the mechanism.

### H6 — world bridge + api + persist wire — ✅ DONE 2026-07-06 (backend); `dev/mock-server.mjs` = H7-adjacent
`world`: `TerrainRenderView{Cols,Rows,Orientation}` + `buildTerrainGrid` over `OffsetDims/OffsetToCell`;
`terrain_delta.cell` → offset index `i=row*Cols+col`; `climateCellToNavCells` rewritten to **fine-sample**
the coarse climate rect (step ≤ hex inradius, centre∈region filter) — robust hex cover; `sortNavCells` →
R-major/Q. `persist.TerrainView{Orientation, Size{Cols,Rows}}` + `/api/terrain` shape + `main.go` wire.
Tests re-baselined (world 148, persist/api green). **Remaining:** `dev/mock-server.mjs` terrain payload +
frontend consumption move with H7.

### H7 — frontend hex render (the visible payoff) — ✅ DONE 2026-07-06
`types.ts`: `TerrainGrid{cellSize,cols,rows,orientation,terrain[],wear?}`, `TerrainDelta.cell`→offset
index (number). `useWorld.ts`: terrain_delta applies by index (`idx=cell`, bounds `0..cols*rows`),
`loadTerrain` parses `size{cols,rows}`+`orientation`, `TERRAIN_LOADED` bounds = hex pixel extent
(`cols·1.5·cellSize × rows·√3·cellSize`, generous framing). `render/terrain.ts`: **hex polygon fills** —
`makeTerrainRaster` draws each cell as a flat-top hexagon at its offset→axial→pixel centre (mirrors
`hex.go`) into a WORLD-coordinate offscreen canvas at `TERRAIN_RASTER_SCALE` px/unit, recording
`origin/worldW/worldH`; `drawTerrain` blits at that placement (odd columns dip negative → origin
tracked). `manifest.ts`: added `river/mountain/sea/bare_rock` styles. `dev/mock-server.mjs`: emits the
hex payload (offset grid, `cell` index deltas, navmap-mirroring JS hex helpers). Tests re-baselined
(envLayers hex fills, world reducer index deltas + hex bounds, assets ids); **71 vitest green**, `tsc -b`
+ `vite build` clean; mock `/api/terrain` smoke = 44×38 flat, canonical ids. Env-off neutrality kept
(`grid===null` ⇒ draws nothing).

### H8 — integration, tuning, full golden re-baseline, live-parity on branch
End-to-end run; grep-guards for stale square assumptions (below); balance sweep if nav cost shift
changed emergent outcomes; final live-parity screenshot before merge.

## 3. Per-module integration deltas

| Module | Change |
|--------|--------|
| `navmap` | `Cell`→axial; `CellOf`/`CellCenter`/`StepCost`/bounds/sort on H2; **add `Neighbors`**; footprint/SetTerrain/wear unchanged in shape. |
| `pathfind` | neighbors from `navmap.Neighbors`; canonical tie-break; hex-aware string-pull; heuristic unchanged. |
| `tools/worldgen` | `TerrainLayout` hex representation; `terrainSampler`/`validateTerrain`; regenerate fixtures. |
| `world` | `TerrainRenderView{Cols,Rows,Orientation,…}` (SPEC.md:227); `climateCellToNavCells` square→hex enumeration + R-major/Q sort (SPEC-world-env.md); scent deposit by `Vec2` → unchanged. |
| `fauna` (`step.go`) | `FootprintBlocked(CellOf(pos))` is navmap-transparent → no change (verify). |
| `ecosim` | `NavAdapter`/`NavCell` config only; boundary-footprint coords. |
| `platform/api` + `persist` | `persist.TerrainView` / `TerrainSize{Cols,Rows}` + `Orientation` (SPEC.md); `/api/terrain` hex shape (bytes forwarded verbatim); `terrain_delta.cell` = offset index. |
| `frontend` | `TerrainGrid` type, `useWorld` terrain apply/bounds, `render/terrain.ts` hex draw, `mock-server`. |
| `content/terrain.yaml` | **none** (type catalog, not layout). |
| SPECs/docs | navmap/pathfind/frontend/render/worldgen SPECs + data-contracts + map-plan + glossary. |

## 4. Determinism (D12) — must hold every phase

- `pixelToHex`/`cubeRound` are pure float ops with fixed rounding → deterministic; the **canonical hex
  sort** (Q6) replaces "Y-major then X" everywhere navmap emits sorted cells.
- pathfind: fixed neighbor order (from `navmap.Neighbors`) + fixed cell tie-break → reproducible path.
- Re-baseline goldens **phase by phase**, each behind a behavioral-invariant assert so a real
  regression can't hide inside an expected numeric shift.

## 5. Performance

- Hot path: `CellOf` (now `pixelToHex`+`cubeRound`, ~15–20 ops vs ~2) and pathfind neighbor gen. Not
  the tick bottleneck (A* expansions / §6 eval dominate) — expected negligible, **but benchmark
  `navmap.CellOf` + a representative `pathfind.Path` before/after** in H3/H4 and micro-opt only if hot.
- No new allocations from the key shape: axial `Cell{Q,R int}` matches the old comparable-struct key.
- **Perf follow-up (H8 benchmark gate):** `navmap.Neighbors` allocates a 6-elem slice per A* expansion
  (the old code used a fixed `[8][2]int` array, zero-alloc). Negligible for typical path lengths;
  revisit with a `NeighborsInto(c, buf)` variant only if the H4 benchmark shows it hot.

## 6. Cross-module consistency & the unexpected-issue playbook

**Consistency invariants to hold (and how):**
1. **One hex convention.** navmap owns orientation/neighbors/sort/size; pathfind calls
   `navmap.Neighbors`; the frontend reads orientation+size from `/api/terrain`. **Guard:** a test that
   the frontend-declared and engine-declared cell centers coincide for a sample grid; grep-guard that
   pathfind defines no local hex offsets.
2. **square grids stay decoupled.** `spatial`/`scent`/`climate` must not import `navmap.Cell`. **Guard:**
   they already use own keys (verified) — an import guard keeps it that way; their goldens must remain
   byte-identical post-migration (a regression tripwire proving scope held).
3. **navmap↔climate agree at t=0.** Both take the same `terrainAt`; signature unchanged → keep the
   worldgen "grids agree" test green as the proof.
4. **climate→navmap SetTerrain covers exactly.** The new square→hex enumeration is tested for full,
   non-overlapping cover of each climate cell's footprint.

**Playbook for surprises:**
- **`cubeRound` edge cases** (the classic hex bug: rounding under `x+y+z=0`) → gated behind H2's
  round-trip + oracle property tests *before* anything depends on it.
- **Golden churn masking a real bug** → every re-baseline pairs with behavioral asserts (§H3/H5);
  never "regenerate + eyeball."
- **Live viewer breakage** → branch isolation; env-off neutrality preserved; backend+frontend both
  green before merge; keep a pre-merge live-parity screenshot.
- **Hidden square assumptions after migration** → grep-guards for `y*w+x`, `Cols*Rows` misuse, `√2`/
  `Sqrt(2)`, and stray `floor(p/cellSize)` outside the hex helper.
- **Perf regression** → the H3/H4 benchmark gate.
- **Rollback / escape hatches** — phase boundaries are the safety net: you can **stop after H6**
  (backend hex, keep the square renderer) or **abort the branch** entirely; nothing on `main` moved.
  navmap's `Cell` change is the single pivot the rest hangs off.

## 7. Build order (slots into `docs/architecture.md §5`)

H2 (hex primitive, L1-leaf) → H3 (`navmap`) → H4 (`pathfind`) → H5 (`worldgen`/fixtures) → H6
(`world` bridge + `api`/`persist` wire) → H7 (frontend) → H8 (integration/tuning). SPEC edits (H1)
precede each module; goldens re-baseline within the phase that shifts them.

## 8. Open questions (resolve before the phase that needs them — spec-architect/implementer MUST refuse to proceed while OPEN)

- **Q0 — Scope confirm.** HEX = navmap+pathfind+layout+wire+frontend; SQUARE (unchanged) =
  spatial+scent+climate. `RESOLVED: Surgical (2026-07-06).`
- **Q1 — Coordinate system.** `RESOLVED: axial (q,r) storage + cube rounding for pixel→hex (2026-07-06).`
  `Cell` = `{Q,R int}`; neighbor offsets + sort defined on axial.
- **Q2 — Orientation.** flat-top vs pointy-top. Affects neighbor offsets, pixel↔hex formulas, **and the
  render look**. `RESOLVED: flat-top (2026-07-06).` (Flat-top ⇒ offset is per-COLUMN — odd columns
  shifted vertically half a hex; the neighbor set + pixel↔hex use the flat-top formulas.)
- **Q3 — Hex size semantics.** redefine `CellSize`: hex circumradius (center→vertex) vs edge length.
  `RESOLVED: circumradius = current CellSize for now; TUNE LATER once cells are visible (2026-07-06).`
  (Note: at circumradius=CellSize a hex covers ~2.6× the old square cell's area ⇒ fewer/coarser cells;
  shrink circumradius (~0.62×) later for equal granularity.)
- **Q4 — Map shape / bounds.** `RESOLVED: rectangular offset arrangement over the existing rectangular
  world bounds (2026-07-06).` A hex is in-bounds iff its center falls within `[MinX,MaxX]×[MinY,MaxY]`.
- **Q5 — Layout + wire representation.** `RESOLVED: offset (Cols×Rows) rectangular array (2026-07-06).`
  Fixture, `/api/terrain`, and frontend keep a rectangular `cells[i]` array (i = row·cols + col,
  unchanged shape); flat-top ⇒ odd COLUMNS shift vertically half a hex. Convert offset(col,row)↔axial(q,r)
  only at the navmap edge (a 2-line formula).
- **Q6 — D12 canonical sort.** `RESOLVED: R-major then Q (2026-07-06).` Replaces "Y-major then X" in
  every sorted navmap output (`ActiveWear`/`TerrainOverrides`/`Decay`). Arbitrary but consistent; zero
  behavioral effect.
- **Q7 — scent hex later?** `RESOLVED: defer — scent stays square now (2026-07-06).` Revisit hex
  diffusion as a pure-quality follow-up.

> **All Open questions RESOLVED 2026-07-06 → H1 (SPEC-first) is unblocked.**
