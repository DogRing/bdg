# Shelter / Exposure — Implementation Plan (Tier-2)

Concept & rationale: `docs/core/design.md §5` (world/space) + `docs/plans/climate.md §1c` (wind). This plan unifies
**two user-requested features into one mechanism**: (2) **caves** and (3) **wind shielding by
walls/buildings**. Both are the same idea — *a place can be sheltered from the world's forcing* — so they
share one **exposure field** + one **interior-state** model rather than two bespoke systems.

Sits between `design.md` (concept) and the module SPECs. spec-architect/implementer **MUST refuse** to
start a phase while any Open question tagged to it is `OPEN` (CLAUDE.md gate).

> **One-line thesis.** A cell's *local* wind (and later rain/temperature) = the world-uniform forcing
> attenuated by a **shelter/exposure factor ε ∈ [0,1]** derived from `blocks_wind`-tagged blockers
> (walls, buildings, mountains, dense forest) and interior states (cave/house). Caves are just the
> extreme case (ε=0 interior).

> **Gate status: every question RESOLVED** (Q-C1..C5, Q-W1..W7, Q-S1..S6, Q-M1 — human 2026-07-08/09).
> **This file = decision record + phase status.** Option deliberations = **`docs/decisions/shelter-gates.md`**.
> **Implementation authority = the module SPECs:** `backend/engine/env/exposure/SPEC.md` (the ε leaf) ·
> `backend/engine/world/SPEC-world-shelter.md` (injection/cave wiring) · `backend/tools/worldgen/SPEC.md`
> (blocker/coverer/cave placement).

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
  `Wind{Dir,Mag}` (`docs/plans/climate.md §1c`). Exposure is a **separate multiplicative layer world owns**;
  climate is untouched.
- **Determinism (D12).** Exposure recompute is a pure function of {blocker footprints, wind}, iterated in
  sorted/fixed order; no map-iteration for logic, no wall-clock, seeded RNG only if any is needed
  (placement uses worldgen's existing seeded `*rng.RNG`).

---

## 1. Resolutions (human-confirmed; deliberation texts = `docs/decisions/shelter-gates.md`)

### Cave (Q-C, 2026-07-08)
- **Q-C1 = (b) linked continuous sub-space** *(user overrode the rec)*: cave interior is a **separately
  generated small CONTINUOUS region with its own render context**, reached via a portal; entering swaps
  the active space, exiting returns. Still continuous (D11) — NOT a tiled world. ⚠ Grows SH2: interior
  generator + portal transition + frontend render-space swap + which env layers apply inside (interior is
  calm/ε=0 by construction).
- **Q-C2 = (a)** caves seeded on mountain/bare_rock above an elevation threshold (worldgen, run-seed
  deterministic; fixture override = later extension).
- **Q-C3 = (a)** unlimited occupancy (capacity-contested shelter = possible later emergence lever, D2).
- **Q-C4 = (a) full interior override** — wind ε=0 + rain blocked + temperature buffered.
- **Q-C5 = (b)** scent/vision hiding deferred (link fauna M3 cover-hiding when opened).

### Wind shielding (Q-W, 2026-07-08)
- **Q-W1 = (a)** per-navmap-cell scalar ε ∈ [0,1]; local wind = global × ε.
- **Q-W2 = (a)** blockers = `blocks_wind` object-kind tag (walls/buildings/mountains/forest; D4/D10).
- **Q-W3 = (a)** directional **leeward shadow** (ε drops N downwind cells, decays with distance).
- **Q-W4 = (a)** wind dir quantized into **6 hex sectors**, ε-field cached per sector; recompute only on
  blocker change.
- **Q-W5 = (c) full shelter VIA PHASING** — wind first (SH1), then rain + temperature (SH3); cave interior
  (Q-C4) and wind-shadow converge on one ε concept.
- **Q-W6 = (a)** consumers = scent spread bias (already wired) + fauna thermal (apparent_temp, F40).
- **Q-W7 = (a)** new leaf **`engine/env/exposure`** — pure fn over {blocker footprints, wind} → ε field;
  world drives cadence + injection.
- **SH1 SPEC-design (2026-07-08) — shipped; authority = exposure SPEC + SPEC-world-shelter:** scent
  consumption = per-position only (`Spread` stays on world-uniform wind; per-cell scent diffusion = later
  increment) · shadow ε = linear per-cell falloff, multiplicative blocker combination (order-independent) ·
  SH1 = exposure leaf + wind injection only (`interiors=nil`; SH2 stays design-ahead) · blocker derivation =
  `config.buildWindBlockerKinds` → `worldgen.buildWindBlockers` → `InstallShelter`, uniform per-blocker
  strength (per-kind strength / terrain-sourced blockers / balance.yaml coefficients = follow-ups). No
  `blocks_wind` content ⇒ shelter OFF ⇒ byte-identical.

### Rain + temperature — SH3 (Q-S, 2026-07-09)
- **Q-S1 = (a)** separate **overhead `ε_cover`** field (isotropic, footprint-local — rain falls from above,
  unlike the directional wind shadow; caves force to 0). Distinct from SH1's `ε_wind`.
- **Q-S2 = (a)** **read-time attenuation** of the sensed Temperature/Moisture; **climate STATE untouched** (§0).
- **Q-S3 = (b)** *(user overrode the rec)* exterior cover **also buffers Temperature** toward a baseline by
  coverage (not wind-chill-only) ⇒ opened Q-S5/Q-S6.
- **Q-S4 = (a)** new object-kind tag **`covers`** → `ε_cover` casters, uniform SH3 strength (worldgen
  constant; per-kind strength = follow-up, same shape SH1 shipped).
- **Q-S5 = (a)** buffering baseline = climate's **daily-MEAN** temperature (climate exposes base/daily-delta
  for a §0-safe read — a read, not a state mutation).
- **Q-S6 = (b)** *(user overrode the rec)* moisture attenuation **gated on the global Raining flag** — cover
  reduces felt moisture only while raining (a roof doesn't dry soil on a sunny day).
- **Semantics (binding):** `ε_cover∈[0,1]` (1=open, 0=covered); `felt_temp = lerp(actual, dailyMean, 1−ε_cover)`;
  `felt_moisture = raining ? actual×ε_cover : actual`. Consumers = fauna `EnvSample.{Temperature,Moisture}`
  at read-time (mirror SH1 `localWindAt`); **agents out of SH3 scope** (`mind/needs` has no thermal need;
  the agent `TakeShelter` action is unrelated to this field). Wind-chill relief under shelter is free via
  SH1 (attenuated wind → warmer `apparent_temp`).

### Map resolution (Q-M1, 2026-07-08 — decided here, actioned as a standalone config change, §5)
- **Q-M1 = (a) DECOUPLE** — drop the `navmap_cell_size == spatial_hash_cell` config check; keep
  `spatial_hash_cell = 12` (perception), set **`navmap_cell_size = 5`** ⇒ 500×500 world = **68×59 hexes**.

---

## 2. Phases (each independently shippable; tests + determinism golden per phase)

### SH1 — Exposure field leaf + local wind injection (wind only) — ✅ SHIPPED
New leaf `engine/env/exposure` (Q-W7): per-navmap-cell ε from `blocks_wind` blockers (Q-W2) with directional
shadow (Q-W3), sector-cached (Q-W4). World multiplies `climate.Wind() × ε(cell)` and injects the result as
the **local** `scent.Wind` and into fauna Context. No cave yet.
> Shipped: `exposure` leaf (`Build`/`Field`/`Cache`) + `world.localWindAt` + `config.buildWindBlockerKinds`
> → `worldgen.buildWindBlockers` → `InstallShelter`. OFF-neutral (no `blocks_wind` content ⇒ byte-identical).

### SH2 — Cave = portal to a generated interior sub-space (Q-C1 = b) — ⬜ DESIGN-AHEAD
worldgen places cave **entrances** (Q-C2) as `shelters`-tagged portals on mountain/bare_rock. Entering (a
tag-derived action, D3/D4) **swaps the agent's active space** to a **separately generated small continuous
interior region** with its own render context; exiting returns. Interior is calm by construction (ε=0) and
confers the full interior effects (Q-C4). Occupancy unlimited (Q-C3). New plumbing this phase introduces:
(1) interior-region generator (seeded, deterministic), (2) portal enter/exit transition + which-space-is-active
state, (3) frontend render-space swap, (4) which env layers run inside vs are forced to interior defaults.
Sub-tasks likely need their own SPECs (≤400-line rule). Design-ahead notes live in `SPEC-world-shelter.md`.

### SH3 — Extend exposure to rain + temperature (Q-W5 = c) — ✅ SHIPPED 2026-07-09
Built BEFORE SH2 (direct ε-machinery extension, lower risk). Overhead `ε_cover` (Q-S1) from the `covers` tag
(Q-S4), read-time only (Q-S2): covered cells buffer felt temperature toward the climate daily-mean
(Q-S3/Q-S5) and shed felt moisture only while raining (Q-S6).
> Shipped: `exposure.BuildCover`/`CoverField` + `climate.DailyMeanTemperature()` + `world.localTempMoistureAt`
> + `config.buildCovererKinds` → `worldgen.buildCoverers`. OFF-neutral (no `covers` content ⇒ byte-identical).
> Follow-ups: per-kind coverage strength · terrain-sourced coverers · coefficients → `balance.yaml`.
> Cave interiors (Q-C4 full ε=0 shelter) will reuse `ε_cover` via SH2 interiors.

### SH4 — Serialization + frontend — ⬜
Persist cave entrances + occupancy; stream exposure as periodic-full + sparse deltas (like `wear`). Frontend:
cave-entrance decal on rock hexes, optional wind-shadow debug overlay, "inside" indicator on agents.

---

## 3. Per-module integration deltas (beyond the new `exposure` leaf)
- **climate** — unchanged (§0); emits world-uniform `Wind()` + (SH3) exposes `DailyMeanTemperature()` (read-only).
- **navmap** — exposes blocker footprints + `InBounds(Cell)` for the world→exposure `Topology` adapter;
  gains no wind dependency.
- **world** — owns exposure recompute cadence, the `global×ε` multiply, injection into scent/fauna
  (`localWindAt`/`localTempMoistureAt`); will own cave enter/exit state (SH2).
- **fauna / scent** — consume the already-existing local-wind/EnvSample seams; no new operands, no code change.
- **content** — tags `blocks_wind`(SH1) · `covers`(SH3) · `shelters`(SH2, cave portals) + schema (D10);
  per-kind strength data = follow-up.

## 4. Determinism (D12) — must hold every phase
- Exposure recompute iterates blockers + affected cells in **sorted order** (blocker ID, then cell index),
  never map-iteration. Sector quantization is a pure fn of `wind.dir`. Cave placement + interior-region
  generation draw from worldgen's seeded `*rng.RNG` in fixed order.

## 5. Map resolution (Q-M1 = decouple) — ✅ ACTIONED 2026-07-08 (independent of the shelter build)
`content/world.yaml` `grids.navmap_cell_size: 12.0 → 5.0` (500×500 ⇒ 68×59 hexes); `balance.yaml
world.spatial_hash_cell` stays 12.0 (perception unchanged); the `navmap_cell_size == spatial_hash_cell`
hard-error in `platform/config/world_content.go` is removed (pre-hex-migration coupling — terrain/path/render
fidelity now independent of the square perception grid, per `docs/plans/hex-grid.md`). This is also the
resolution that makes SH1 wind-shadows and SH2 cave entrances legible.
