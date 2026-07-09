# Diorama Render — Implementation Plan (2.5D pseudo-3D frontend, "Don't Starve"-like)

Concept & rationale: this file. Sits beside `docs/plans/frontend.md` (that plan built the flat top-down
viewer; this one restyles the **rendered** scene into a 2.5D billboard diorama for aesthetics) and
`docs/plans/hex-grid.md` (a parallel visual migration of the same terrain layer — see §6 coordination). It is
the **roadmap**: scope, phasing, per-file render deltas, purity/determinism, and the Open-question gate.
It does **not** restate the SPECs (those are edited in Phase S1, SPEC-first, only after the gate opens).

> **Goal (author-stated):** the viewer should read as a **2.5D diorama** — a tilted ground plane with
> creatures/plants/objects standing on it as upright cutout sprites, depth-sorted, casting soft ground
> shadows — the "Don't Starve" look, on the web. Purely a **presentation** motivation.
>
> **This is frontend-only. No backend, no wire, no invariant edit.** The sim stays authoritative
> continuous 2D (D11); the streamed frames are unchanged; determinism (D12) is untouched because none
> of this feeds back into state. `docs/core/data-contracts.md` does not change. `docs/core/design.md` does not
> change. The whole blast radius is `frontend/src/render/` + `frontend/src/assets/` + `frontend/dev/`.

## 0. Decisions locked (author-set direction — do not re-open)

- **DL1 — Presentation transform only.** 2.5D is a projection + draw-order change in the render layer.
  Positions stay continuous floats (D11); interpolation/poses/fx never feed back into state (D12). No
  new SSE field, no `/api/*` change, no `docs/core/data-contracts.md` edit.
- **DL2 — Axonometric (affine), NOT perspective.** The projection stays a **linear/affine** map so the
  single shared world→canvas transform and its **inverse `toWorld`** survive — hit-testing and
  cursor-anchored zoom (render SPEC ACs) depend on invertibility. Perspective foreshortening (things
  shrink with distance) is **rejected**: it makes the transform non-linear and breaks the round-trip.
- **DL3 — Upright billboard sprites.** Entities draw as **screen-vertical cutouts anchored at their
  ground point**, never rotated by heading. (Replaces fauna's current `ctx.rotate(heading)`.)
- **DL4 — Depth via painter's algorithm.** Entities are drawn **back→front by world depth** so nearer
  ones occlude farther ones. (Today each layer iterates its own slice with no cross-entity ordering.)
- **DL5 — Soft ground shadows** anchor each billboard to the plane.
- **DL6 — Directional sprites are authored per facing; the camera is FIXED (no free rotation).**
  Free camera rotation would multiply facings × camera-angles → rejected. Facing is chosen by
  quantizing the continuous `heading` to the nearest authored facing (with horizontal mirror). The
  facing **count is an Open question** (Q2), but that facings *must be authored* is locked.
- **DL7 — Scope of the billboard model:** fauna, flora sprites, and standing objects (shelter) become
  billboards; terrain becomes the tilted ground plane. Agents' treatment is an Open question (Q7).

## 1. Current baseline (what exists today — measured from the code)

- **Canvas 2D**, single RAF loop in `components/WorldCanvas.tsx`; `performance.now()` sampled once/frame.
- **One separable transform** `transform.ts: Transform {sx,sy,ox,oy}`; `wx=x·sx+ox`, `wy=y·sy+oy`,
  `toWorld` inverts per-axis. `buildTransform` sets `sx=sy=cam.zoom` (isotropic → flat top-down).
- **Per-slice draw, no depth sort.** Layer order per frame: terrain → world-bounds → flora → objects →
  fauna → agents → fx → ambient. Each `draw*` iterates its own slice independently.
- **fauna.ts:** sprite drawn **centered** at `(cx,cy)`, `ctx.rotate(heading)` (sheets face +x), size
  `max(20, 3·sx)` px; status label above (screen-space).
- **flora.ts:** ground-cover *coverage wash* (radial-gradient stamps on the ground) + per-plant sprites
  drawn **centered**. **objects.ts:** circle/square markers + label, **centered**. **agents.ts:**
  colored **dots** + cluster/role aura + selection ring (the social-layer signal, frontend plan Q8).
- **terrain.ts:** **square** raster — `makeTerrainRaster` paints 1px/cell over `grid.w×grid.h`,
  `drawTerrain` blits it via `drawImage(x0,y0, w·cellSize·sx, h·cellSize·sy)`. `types.ts TerrainGrid`
  is `{cellSize,w,h,terrain[],wear?}` (**square**). ⚠️ The render SPEC already describes *hex* terrain,
  but the **code + type are still square** — the hex migration (`docs/plans/hex-grid.md`) is not yet in the
  frontend. See §6 for how the two visual migrations coordinate.

The good news the baseline gives us: because `wx/wy` is the one transform every layer already shares
(D11), and sprite **sizes** are derived from `sx` (not `sy`), a vertical foreshortening (`sy=zoom·k`)
foreshortens **positions and the ground** while leaving **sprite sizes square** — most of the diorama
effect falls out of a tiny transform change + anchoring/ordering changes (see Q1). This is why the
blast radius is small.

## 2. Phases (each independently shippable and **visually checkable** in the mock viewer)

> Verification each phase: `npm run dev` + `npm run mock`, eyeball in-browser; keep **env-off
> neutrality** (empty env slices ⇒ byte-identical to the social-only render) as a tripwire.

### S0 — This plan + resolve Open questions (§8). **Hard stop:** no SPEC edit, no code, until the
Phase-A questions (Q1, Q2, Q4, Q5, Q7, Q10) are RESOLVED. spec-architect/implementer MUST refuse to
start a phase while a question tagged to it is OPEN, and return the OPEN list instead of guessing.

### S1 — SPEC-first (no code), then a `reviewer` coherence pass
Edit, in this order:
- `frontend/src/render/SPEC.md` — Transform (tilt/projection per Q1), **upright base-anchored billboard**
  rule (DL3, Q4), **merged depth-sorted pass** (DL4, Q5), **shadow pass** (DL5, Q6), **facing selection
  + mirror** (DL6, Q2), terrain ground under tilt (Q8). Update the layer-order + ACs.
- `frontend/src/assets/SPEC.md` — spritesheet schema gains a **facing** dimension (Q3); manifest facing
  tables + fallback (missing facing → nearest / glyph).
- `frontend/dev/SPEC.md` — `generate-spritesheets.mjs` emits **directional billboard** placeholders (Q3).
- `frontend/SPEC.md` — parent: layer order → depth pass; camera "no rotation" note; rendering summary.
- Cross-links: `docs/plans/frontend.md` (pointer), `docs/plans/hex-grid.md` (coordination note, Q8),
  `docs/core/glossary.md` (billboard / facing / depth-sort / ground-shadow vocabulary).
- **Not touched:** `docs/core/data-contracts.md`, any `backend/**`, `docs/core/design.md` (stated for the record).

### S2 — Projection + upright billboards + depth + shadows (**core look; existing sprites, stood up**)
`transform.ts` tilt (Q1); `WorldCanvas` merged depth pass (Q5) + shadow pass (Q6); fauna/flora/objects
switch to **bottom-center anchor, no rotation** (Q4); terrain auto-foreshortens through the tilted
transform (Q8). Uses the **current (top-down) sprites** as-is — this is the first frame that reads as a
diorama and answers "does the direction feel right" before any new art. `toWorld` round-trip AC must
still pass (DL2).

### S3 — Directional sprites (the facings)
`assets` manifest facing schema + `dev/generate-spritesheets.mjs` directional placeholders (Q2, Q3);
draw picks facing = `quantize(heading)` + horizontal mirror; fallback missing facing → nearest → glyph.
Now flee/chase reads with a body facing, not a rotated top-down cutout.

### S4 — Polish (deferred cues)
Agents treatment (Q7), occlusion legibility for selected/followed (Q9), optional oblique **skew or
isometric** upgrade (Q1's deferred branch — needs Transform → 2×3 matrix), edge/horizon depth cue,
day-night lighting interplay with billboards. Each optional; land only what earns its keep visually.

## 3. Per-file render deltas (frontend only)

| File | Change |
|------|--------|
| `render/transform.ts` | tilt per Q1. Foreshorten-only ⇒ `buildTransform` sets `sy=zoom·TILT_K` (**type unchanged**, `toWorld` still inverts per-axis). Skew/iso (Q1 deferred) ⇒ `Transform` becomes a 2×3 matrix + matrix inverse. |
| `render/fauna.ts` | drop `ctx.rotate(heading)`; **bottom-center anchor**, draw upright (Q4); **facing selection + mirror** (Q2, S3); status label offset from sprite top. |
| `render/flora.ts` | per-plant sprites → bottom-center anchor (trees stand up). Coverage wash stays on the ground (drawn through the tilted transform → foreshortens with the plane; no change needed). |
| `render/objects.ts` | standing kinds (shelter) → bottom-center billboard; flat kinds (water) may stay ground-marker. |
| `render/agents.ts` | Q7: keep dots + aura but **participate in the depth pass** + small ground shadow; (billboard art = S4/out-of-scope). |
| `render/terrain.ts` | ground foreshortens automatically via the tilted `sy` blit (Q8). Skew/iso (Q1 deferred) ⇒ blit through the matrix / redraw foreshortened cells. |
| `components/WorldCanvas.tsx` | **shadow pass** (Q6) then a **single depth-sorted sprite pass** (Q5) replacing the fixed per-slice fauna/flora/objects/agents calls; terrain + coverage wash + world-bounds stay background; fx + ambient stay on top. |
| `assets/` (manifest + sprites + `dev/generate-spritesheets.mjs`) | facing dimension in the sheet schema + placeholder generator (Q2, Q3). |

## 4. Purity & determinism (the render SPEC's invariants — must hold every phase)

- **Render purity** (frontend's D12 analog): draw/animator fns stay pure `(state, camera, clockMs) →
  pixels`; no `Math.random`/`Date.now`/`performance.now` inside; `clockMs` is injected; the depth-sort
  is over a **stable key** (world-y, ties broken by id) so the same frame is byte-identical every RAF.
- **Invertibility (DL2):** `toWorld(wx(p),wy(p)) ≈ p` and cursor-anchored zoom must still hold — the
  round-trip AC is the guard that we stayed affine. (Foreshorten-only keeps the existing per-axis
  inverse; a skew/iso matrix needs a matrix inverse added with its own round-trip test.)
- **Open-content mapping (D10 mirror):** facing/pose/species → sprite only via the assets manifest +
  fallback chain; an unknown species or a missing facing draws a fallback, never throws. The grep guard
  (no species/action string conditionals in `render/`) stays.
- **Env-off neutrality:** empty env slices ⇒ the depth pass and shadow pass emit nothing; canvas equals
  the social-only render (kept as a per-phase tripwire).

## 5. Performance

- **Depth sort:** O(n log n) per frame over on-screen sprites (tens–low hundreds) — negligible; sort a
  reused index array, not fresh allocations, to avoid GC churn in the RAF loop.
- **Shadow pass:** one ellipse per sprite (`ctx.ellipse`+fill) — cheap; batch by disabling shadowBlur.
- **Facings** multiply spritesheet pixels (≤ ~5 drawn facings × poses × frames) but the **cache** holds
  one decoded image per species; no per-frame cost. Sheet memory grows ~facings×; fine for a handful
  of species.
- Terrain raster is still built once per grid identity and blitted; the tilt only changes blit args.

## 6. Cross-cutting consistency & risk playbook

- **Coordination with the hex migration (`docs/plans/hex-grid.md`).** Both restyle the **same terrain layer**
  and both are frontend-visual. They are *orthogonal in mechanism* — hex changes the ground **cell
  shape**; diorama changes the ground **projection + entity draw model**. Both reduce to "a raster/polys
  blitted through the shared transform." **Risk:** two unshipped branches editing `render/terrain.ts` +
  `types.ts TerrainGrid` collide. **Playbook (Q8):** pick an order and land one before starting the
  other on top; do not interleave. Building diorama on the current **square** terrain is fine — the hex
  swap later only changes cell shape, not the projection.
- **Invertibility regression** → the transform round-trip + cursor-zoom ACs are the tripwire; if a Q1
  skew/iso lands, add the matrix-inverse round-trip test in the same phase.
- **Occlusion hiding the thing you're watching** (this is a god's-eye *observation* viewer, not a game)
  → Q9: always draw the selected/followed entity + label on top of the depth order; defer occluder-fade.
- **Sprite art vs open content (D10).** Directional billboards need art per species×facing×pose, but
  species are data (D10) and emergent — you cannot pre-draw unwritten content. **Playbook:** the
  fallback chain (missing facing → nearest → glyph) must stay total so a new species always renders;
  placeholder generator covers the authored set, unknowns get the chevron glyph.
- **Rollback / escape hatch:** `TILT_K = 1` reproduces the flat top-down look, so S2 is reversible with
  one constant; phases are independently shippable; everything is on a branch, `main`/live untouched.

## 7. Build order (slots into `docs/core/architecture.md §5`, frontend `L10`)

S1 (SPEC-first) → S2 (projection + billboards + depth + shadows, existing sprites) → S3 (directional
sprites) → S4 (polish). SPEC edits precede code each phase; visual check in the mock viewer within the
phase that changes the look.

## 8. Open questions (resolve before the phase that needs them — spec-architect/implementer MUST refuse to proceed while OPEN; only the human flips `OPEN`→`RESOLVED: <answer>`)

- **Q1 — Tilt model & angle** *(blocks S2)*. (a) **Foreshorten-only**: `sy=zoom·TILT_K`, Transform type
  unchanged, per-axis inverse survives — smallest blast radius, reads as "oblique top-down"; (b) oblique
  **skew** (cabinet) — needs `Transform`→2×3 matrix + matrix inverse, stronger tilt; (c) true
  **isometric** (45°+2:1) — most "game-like", biggest change (ground axes rotate). Plus the tilt factor
  `TILT_K`. **Rec: (a) foreshorten-only, TILT_K≈0.6 for S2**; defer (b)/(c) to S4 if wanted. `OPEN`
- **Q2 — Facing count** *(blocks S3; art scope)*. 4 / 6 / 8 authored facings with horizontal mirror.
  6 is awkward for billboards (can't give clean cardinal + side profiles at even 60°) and is **not**
  tied to the hex grid (facing = view-quantization, independent of cell topology). **Rec: 8 facings
  authored as 5 sprites + L/R mirror**; fall back to 4 (draw 3) if art budget is tight. `OPEN`
- **Q3 — Sheet schema + placeholder pipeline** *(blocks S3)*. How facings encode: facing = row-group with
  pose×frame inside one sheet, vs one sheet per facing. And: extend `dev/generate-spritesheets.mjs` to
  emit directional billboard placeholders vs hand-author. **Rec: single sheet, facing as an outer
  row-group (facing × pose × frame); extend the generator to emit N-facing placeholders; manifest gains
  a `facings` field; missing facing → nearest → glyph.** `OPEN`
- **Q4 — Billboard anchor & height** *(blocks S2)*. bottom-center anchor (feet at the projected ground
  point, sprite extends screen-up) vs center anchor; sprite screen-height source. **Rec: bottom-center
  anchor; height = worldUnits·`sx` (NOT foreshortened by `sy`, so sprites stand full-height while the
  ground compresses).** `OPEN`
- **Q5 — Depth-sort architecture** *(blocks S2)*. One **merged** sorted pass over
  {flora-sprites, objects, animals, agents} vs per-layer sort only; and the refactor shape — a
  coordinator in `WorldCanvas` that collects lightweight draw-items and sorts, vs keeping the `draw*`
  fns and feeding each a pre-sorted slice. **Rec: one merged depth pass keyed by world-y (ties by id);
  a small coordinator collects `{y, draw()}` items; terrain + coverage-wash + world-bounds stay
  background, fx + ambient stay foreground.** `OPEN`
- **Q6 — Ground shadows** *(blocks S2)*. Separate shadow pass (all shadows on the ground beneath all
  sprites) vs per-sprite inline (a nearer sprite could shadow-over a farther one). Ellipse params.
  **Rec: separate shadow pass before the sorted sprite pass; ellipse w=footprint, h=footprint·TILT_K,
  alpha≈0.25, centered at the ground point.** `OPEN`
- **Q7 — Agents: dots vs billboards** *(blocks S2 for agents)*. Keep colored dots + cluster aura (the
  social-layer signal, frontend plan Q8) and just add shadow + depth participation, vs author agent
  billboard sprites now. **Rec: keep dots for S2/S3 (agents join the depth pass + get a small shadow so
  they sit on the plane); agent billboard art is S4/out-of-scope.** `OPEN`
- **Q8 — Terrain under tilt + hex coordination** *(blocks S2)*. Build diorama on the **current square**
  terrain (raster blitted through the tilted transform — works today) vs wait for the hex migration; and
  which visual migration lands first. **Rec: build on current square terrain now (orthogonal to hex);
  land diorama and hex sequentially, not interleaved (§6).** `OPEN`
- **Q9 — Occlusion legibility** *(blocks S4)*. Observation viewer vs DS-style foreground occlusion.
  do-nothing / always-draw selected+followed on top / fade occluders near the followed entity.
  **Rec: always draw selected + followed entity + its label on top of the depth order; defer
  occluder-fade.** `OPEN`
- **Q10 — Rollout / comparability** *(informs S2)*. Replace the flat look outright vs keep a flat↔2.5D
  dev toggle. Since `TILT_K=1` reproduces the flat render, a single constant already gives A/B.
  **Rec: single `TILT_K` constant (k=1 ⇒ old look) as the escape hatch; a UI toggle is optional, not
  required.** `OPEN`
