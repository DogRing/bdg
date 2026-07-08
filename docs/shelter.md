# Shelter / Exposure — Implementation Plan (Tier-2)

Concept & rationale: `docs/design.md §5` (world/space) + `docs/climate.md §1c` (wind). This plan unifies
**two user-requested features into one mechanism**: (2) **caves** and (3) **wind shielding by
walls/buildings**. Both are the same idea — *a place can be sheltered from the world's forcing* — so they
share one **exposure field** + one **interior-state** model rather than two bespoke systems.

Sits between `design.md` (concept) and the module SPECs. Carries **Decisions locked · Open questions ·
Phases**. spec-architect/implementer **MUST refuse** to start a phase while any Open question tagged to it
is `OPEN` (CLAUDE.md gate).

> **One-line thesis.** A cell's *local* wind (and later rain/temperature) = the world-uniform forcing
> attenuated by a **shelter/exposure factor ε ∈ [0,1]** derived from `blocks_wind`-tagged blockers
> (walls, buildings, mountains, dense forest) and interior states (cave/house). Caves are just the
> extreme case (ε=0 interior).

---

## 0. Decisions locked (do not re-decide — from invariants + shipped architecture)
- **Tag-derived, never type-hardcoded (D2/D4/D10).** What blocks wind / confers shelter is a **content
  tag** (`blocks_wind`, `shelters`, interior-effect data), not `if type=="wall"`. Adding a shelter is
  `content/` data + schema, not code. Caves, houses, walls, forests all reach shelter through the *same*
  tag path — "house" / "cave" are not privileged engine concepts.
- **Continuous coords preserved (D11).** Cave interiors are modelled as an **agent STATE** over the same
  continuous world (`inside: <shelter_id>`), OR as a small linked continuous sub-space — **never** a new
  tiled world and never snapping agents to cells. Grids stay indices.
- **Reuse the local-wind injection seam that already exists.** `scent.Wind` is already a *local* value
  world injects (`scent.go`), and fauna already reads `wind.dir`/`wind.mag` via its §6 Context
  (`fauna/context.go`). Exposure feeds **these existing seams** — no new operand names, no climate rewrite.
- **Global wind stays as-is (climate CA2).** climate keeps producing the single world-uniform
  `Wind{Dir,Mag}` (`docs/climate.md §1c`). Exposure is a **separate multiplicative layer world owns**;
  climate is untouched.
- **Determinism (D12).** Exposure recompute is a pure function of {blocker footprints, wind}, iterated in
  sorted/fixed order; no map-iteration for logic, no wall-clock, seeded RNG only if any is needed
  (placement uses worldgen's existing seeded `*rng.RNG`).

---

## 1. Open questions

> **RESOLUTIONS — human-confirmed 2026-07-08 (these SUPERSEDE the inline `OPEN` tags below).**
> Load-bearing forks were confirmed via AskUserQuestion; the remaining mechanism-detail questions take
> their `rec.` per the user's "추천대로 진행" (proceed-as-recommended). Do not re-litigate.
> - **Q-C1 = (b) linked continuous sub-space** *(user overrode the rec.)*. Cave interior is a **separately
>   generated small CONTINUOUS region with its own render context**, reached via a portal; entering swaps
>   the active space, exiting returns. Still continuous (D11 intact) — NOT a tiled world. ⚠ This grows SH2:
>   needs an interior-region generator + portal transition + frontend render-space swap + a decision on
>   which env layers (navmap/scent/climate) apply inside (interior is calm/ε=0 by construction).
> - **Q-C2 = (a)** seeded on mountain/bare_rock above elevation threshold.
> - **Q-C3 = (a) unlimited occupancy.**
> - **Q-C4 = (a) full** — interior overrides wind (ε=0) + rain (blocked) + temperature (buffered).
> - **Q-C5 = (b) defer** scent/vision hiding to a later phase (link fauna M3).
> - **Q-W1 = (a)** per-navmap-cell scalar ε. **Q-W2 = (a)** `blocks_wind` tag. **Q-W3 = (a)** directional
>   leeward shadow. **Q-W4 = (a)** 6-sector wind quantization + per-sector cache. **Q-W6 = (a)** scent +
>   fauna thermal. **Q-W7 = (a)** new leaf `engine/env/exposure`.
> - **Q-W5 = (c) full shelter VIA PHASING** — build wind first (SH1), then extend to rain + temperature
>   (SH3). Cave interior (Q-C4) and wind-shadow converge on one ε concept.
> - **Q-M1 = (a) DECOUPLE** — drop the `navmap_cell_size == spatial_hash_cell` config check; keep
>   `spatial_hash_cell = 12` (perception); set **`navmap_cell_size = 5`** ⇒ 500×500 world = **68×59 hexes**.
>   *(Actioned outside the shelter build — see §5.)*

### Cave (feature 2)
- **Q-C1 — Cave interior model.**
  - (a) **Portal + interior STATE** — entrance at a world pos; entering sets `inside: cave_X`; interior is
    NOT a separate coordinate space, just a state with overridden env (ε=0 wind, rain blocked, temp
    buffered). Reuses the building-interior seam; minimal plumbing; D11-clean. **rec.**
  - (b) **Linked sub-space** — cave interior is a small separate continuous region reachable via the
    portal; agents actually move around inside. More faithful (interior geometry, multiple spots) but new
    spatial/nav plumbing (a second navmap region + transition).
  - (c) 3D voxel / z-layer. **Rejected** (engine rewrite; whole stack assumes one 2D field).
  - `OPEN`
- **Q-C2 — Cave placement (worldgen).**
  - (a) **Seeded on mountain/bare_rock cells above an elevation threshold**, fixed count from run seed —
    same seed ⇒ same caves. Reuses `GenerateTerrain` seam. **rec.**
  - (b) Fixture-authored only. (c) Both (worldgen live + fixture override). `rec: (a), extend to (c) later.`
  - `OPEN`
- **Q-C3 — Cave capacity / contestability.**
  - (a) **Unlimited occupancy**, single shelter volume — simplest. **rec (phase 1).**
  - (b) **Capacity-limited** — a cave holds N; shelter becomes a *contested resource* → competition/claiming
    can **emerge** (D2). Richer, needs occupancy accounting + conflict resolution (by stat, ties by ID).
  - `OPEN`
- **Q-C4 — Cave interior effects (which env is overridden).**
  - (a) **wind ε=0 + rain blocked + temperature buffered** toward a stable cave value (full shelter). **rec.**
  - (b) wind + rain only. (c) wind only. (Temperature buffering is the fauna-thermal-relevant one — F40.)
  - `OPEN` (ties to Q-W5 scope)
- **Q-C5 — Does a cave hide scent/vision (predation ambush/refuge)?**
  - (a) Yes — interior is a scent/vision sink (predator can't smell an agent inside), reusing the
    hiding/cover mechanic (fauna M3). (b) No — out of scope for phase 1. `rec: (b) defer, link to fauna M3.`
  - `OPEN`

### Wind shielding (feature 3)
- **Q-W1 — Exposure field representation.**
  - (a) **Per-navmap-cell scalar ε ∈ [0,1]**; local wind = global wind × ε. Reuses the per-cell local-wind
    injection scent already consumes; cheap to read. **rec.**
  - (b) Per-position raycast against blockers at query time (no stored field) — more accurate, costlier per
    query, harder to cache. (c) Per-object shelter radius only, no directional shadow (see Q-W3).
  - `OPEN`
- **Q-W2 — What casts a shadow (source of blockers).**
  - (a) **Objects/terrain carrying a `blocks_wind` tag** (walls, buildings, mountains, dense forest;
    cave-interior forced ε=0), height/size from tag data (D4/D10). **rec.**
  - (b) Hardcode building/wall types. **Rejected** (D2).
  - `OPEN`
- **Q-W3 — Shadow geometry.**
  - (a) **Directional leeward shadow** — ε drops for N downwind cells, N from blocker height, decaying with
    distance; recomputed when wind turns. Realistic ("벽 뒤가 바람 약함"). **rec.**
  - (b) Isotropic radius — a blocker just calms its neighborhood regardless of wind direction; simpler, no
    recompute-on-turn, but not physical. (c) CFD/fluid. **Rejected.**
  - `OPEN`
- **Q-W4 — Recompute cadence (directional field depends on wind).**
  - (a) **Quantize wind dir into 6 hex sectors; cache ε-field per sector; recompute a sector only when
    blockers change.** Amortizes cost; wind wanders slowly so sector rarely flips. **rec.**
  - (b) Recompute every N ticks. (c) Every tick (expensive at fine resolution).
  - `OPEN`
- **Q-W5 — Scope: does "shelter" also attenuate rain + temperature, or wind only?**
  - (a) **Wind only** (phase 1), extend later. Smallest first cut.
  - (b) Wind + rain. (c) **Wind + rain + temperature** — full unified shelter: a sheltered cell / cave /
    house is calmer, drier, and thermally buffered; unifies cave (Q-C4) and wind-shadow under one ε.
  - `rec: build ε for wind first (SH1), then extend to rain+temp (SH3) — i.e. converge on (c) via phasing.`
  - `OPEN` (the scope-defining decision)
- **Q-W6 — Consumers of local wind / exposure.**
  - (a) **scent spread bias (already wired) + fauna thermal comfort** (apparent_temp via F40). **rec.**
  - (b) scent only (phase 1), fauna later.
  - `OPEN`
- **Q-W7 — Module boundary / DAG placement.**
  - (a) **New leaf `engine/env/exposure` (or `env/shelter`)** — pure fn over {navmap blocker footprints,
    climate wind} → per-cell ε; world drives cadence + injects local wind, exactly like it already adapts
    climate → fauna Context. **rec.**
  - (b) Inside climate (couples wind+blockers into climate — violates §0 "climate untouched").
  - (c) Inside navmap (navmap gains wind dependency — unwanted coupling).
  - `OPEN`

### Map resolution (from feature 1 — actionable follow-up, NOT part of shelter build but decided here)
- **Q-M1 — Terrain/nav grid resolution vs perception radius.** Today `config/world_content.go:110`
  **hard-errors unless `navmap_cell_size == spatial_hash_cell`** — a pre-hex-migration coupling. Options:
  - (a) **Decouple: drop the sync check; keep `spatial_hash_cell`≈12 (perception), lower
    `navmap_cell_size` to ~6** (⇒ 57×50 hexes, 4× denser terrain, perception/gameplay unchanged). **rec.**
  - (b) Keep coupling; lower both to ~6 (finer terrain **and** finer perception buckets — changes fauna
    perception + more hash buckets).
  - (c) Leave at 12 (status quo).
  - `OPEN`

---

## 2. Phases (each independently shippable; tests + determinism golden per phase)

### SH1 — Exposure field leaf + local wind injection (wind only)
New leaf `engine/env/exposure` (Q-W7): per-navmap-cell ε from `blocks_wind` blockers (Q-W2) with directional
shadow (Q-W3), sector-cached (Q-W4). World multiplies `climate.Wind() × ε(cell)` and injects the result as
the **local** `scent.Wind` (already consumed) and into fauna Context. **No cave yet.** Golden: ε-field for a
fixture with one wall + fixed wind.

### SH2 — Cave = portal to a generated interior sub-space (Q-C1 = b)
worldgen places cave **entrances** (Q-C2) as `shelters`-tagged portals on mountain/bare_rock. Entering (a
tag-derived action, D3/D4) **swaps the agent's active space** to a **separately generated small continuous
interior region** with its own render context; exiting returns. Interior is calm by construction (ε=0) and
confers the full interior effects (Q-C4: wind0 + rain-blocked + temp-buffered). Occupancy unlimited (Q-C3).
New plumbing this phase introduces: (1) interior-region generator (seeded, deterministic), (2) portal
enter/exit transition + which-space-is-active state, (3) frontend render-space swap, (4) which env layers run
inside vs are forced to interior defaults. Sub-tasks likely need their own SPECs (≤400-line rule).

### SH3 — Extend exposure to rain + temperature (only if Q-W5 = b/c)
Sheltered cells / interiors reduce rain accumulation and buffer temperature toward a shelter-stable value;
wire to climate rain read + fauna thermal (F40). Converges cave (Q-C4) and wind-shadow onto one ε.

### SH4 — Serialization + frontend
Persist cave entrances + occupancy; stream exposure as periodic-full + sparse deltas (like `wear`). Frontend:
cave-entrance decal on rock hexes, optional wind-shadow debug overlay, "inside" indicator on agents.

---

## 3. Per-module integration deltas (beyond the new `exposure` leaf)
- **climate** — unchanged; still emits world-uniform `Wind()` (§0).
- **navmap** — exposes blocker footprints (cells covered by a `blocks_wind` object) for the exposure leaf to
  read; no wind dependency added to navmap itself.
- **world** — owns exposure recompute cadence (Q-W4) + the `global×ε` multiply + injection into scent/fauna;
  owns cave enter/exit state transitions and (if Q-C3=b) occupancy accounting.
- **fauna** — reads local `wind.dir/wind.mag` (already) now attenuated; SH3 adds sheltered temperature to
  apparent_temp (F40). No new operands.
- **scent** — already takes local `Wind`; now receives the attenuated value. No code change if injection
  point is reused.
- **content** — new tags `blocks_wind` (+ height/size data), `shelters`, cave interior-effect data; schema
  extension (D10).

## 4. Determinism (D12) — must hold every phase
- Exposure recompute iterates blockers + affected cells in **sorted order** (blocker ID, then cell index),
  never map-iteration. Sector quantization is a pure fn of `wind.dir`. Cave placement + interior-region
  generation draw from worldgen's seeded `*rng.RNG` in fixed order. (Q-C3 = unlimited ⇒ no enter/exit
  contention to resolve this phase.)

## 5. Map resolution (Q-M1 = decouple) — ACTIONED 2026-07-08 (independent of the shelter build)
The visible "map too coarse" (feature 1) is a cell-size tuning, decided here but landed as a standalone
config change, not a shelter phase:
- **`content/world.yaml`** `grids.navmap_cell_size: 12.0 → 5.0` (500×500 ⇒ 68×59 hexes, ~5.3× denser).
- **`content/balance.yaml`** `world.spatial_hash_cell` stays **12.0** — perception radius unchanged.
- **`platform/config/world_content.go`** — the `navmap_cell_size == spatial_hash_cell` hard-error is
  **removed** (a pre-hex-migration coupling; navmap=hex terrain/path fidelity is now independent of the
  square spatial-hash perception grid, per `docs/hex-grid.md`). Its config-test case is retired.
- Rationale: terrain/path/render get finer; fauna perception + spatial-hash bucketing unchanged. This is
  also the resolution granularity that makes SH1 wind-shadows and SH2 cave entrances legible.
