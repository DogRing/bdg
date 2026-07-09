# Shelter / Exposure — Q-C·Q-W·Q-S·Q-M1 심의 기록 (Why)

> **성격:** `docs/plans/shelter.md`의 Open-Question **심의 전문**(동굴 Q-C1~C5 · 바람 차폐 Q-W1~W7 · 비/기온 SH3
> Q-S1~S6 · 맵 해상도 Q-M1 — 옵션·기각 근거·SH1 SPEC-design 원문). 확정값(What)은 plan §1 Resolutions가 권위,
> 구현(How)은 `engine/env/exposure/SPEC.md`·`engine/world/SPEC-world-shelter.md`. 이 파일 = 2026-07-09 다이어트
> 때 잘라낸 **pre-diet 원문 스냅샷**(RESOLUTIONS 블록 + 심의 블록 전체; 이후 갱신되지 않음).

---
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
> - **SH1 SPEC-design (2026-07-08, spec-architect review of the drafts):**
>   - **(i) scent consumption = per-position only.** Exposure attenuates `fauna.EnvSample.Wind` at the
>     animal position; `scent.Read(pos,r,wind)` inherits it via fauna. `scent.Spread` (bulk diffusion,
>     single-wind API) stays on world-uniform wind — no scent API change (plan §0/§3). Per-cell scent
>     *diffusion* (a wall blocking scent drift) is a later increment, not SH1.
>   - **(ii) shadow ε formula** pinned in `env/exposure/SPEC.md`: linear per-cell falloff, **multiplicative**
>     blocker combination (order-independent ⇒ the D12 sort is belt-and-suspenders, not load-bearing).
>   - **(iii) SH1 builds the exposure leaf + wind injection only.** SH2 (portals/interiors/enter-exit) stays
>     design-ahead in `world/SPEC-world-shelter.md`, not this build; SH1 calls `Build` with `interiors=nil`.
>   - **(iv) blocker derivation (SHIPPED 2026-07-08).** `blocks_wind` is an **object-kind tag** (Q-W2 = a):
>     `config.buildWindBlockerKinds` collects tagged kinds (mirrors `CoverKinds`); `worldgen.buildWindBlockers`
>     turns each placed instance into one `exposure.Blocker` (footprint = the object's navmap cell, sorted-ID
>     order) and calls `w.InstallShelter`. **No tagged kinds ⇒ no blockers ⇒ shelter OFF (byte-identical).**
>     SH1 uses a **uniform per-blocker height/opacity** (worldgen `shelter*` constants); **per-kind strength
>     from tag data, terrain-sourced blockers, and moving the coefficients into `balance.yaml` are follow-ups**,
>     not SH1. (Content authoring — adding a `blocks_wind` kind to `content/objects.yaml` — is a data change
>     owned by the human, deliberately not made by this build.)
> - **SH3 RESOLVED 2026-07-09 (human, AskUserQuestion — see the SH3 question block for options/rationale):**
>   **Q-S1 = (a)** separate **overhead `ε_cover`** field (isotropic, footprint-local; caves force to 0) —
>   distinct from SH1's directional `ε_wind`. **Q-S2 = (a)** read-time attenuation of the sensed
>   Temperature/Moisture; **climate STATE untouched** (§0). **Q-S3 = (b)** *(user overrode the rec.)*
>   exterior cover **also buffers Temperature** toward a baseline by coverage, not wind-chill-only.
>   **Q-S4 = (a)** new object-kind tag **`covers`** → `ε_cover` casters, uniform SH3 strength (worldgen
>   constant; per-kind = follow-up, same shape SH1 shipped). **Q-S5 = (a)** buffering baseline = climate's
>   **daily-MEAN** temperature (climate exposes its base/daily-delta for a §0-safe read). **Q-S6 = (b)**
>   *(user overrode the rec.)* moisture attenuation is **gated on the global Raining flag** — cover reduces
>   felt moisture only while raining. Semantics: `ε_cover∈[0,1]` (1=open, 0=covered);
>   `felt_temp = lerp(actual, dailyMean, 1−ε_cover)`; `felt_moisture = raining ? actual×ε_cover : actual`.
>   Consumers = fauna `EnvSample.{Temperature,Moisture}` at read-time (mirror SH1 `localWindAt`); agents
>   out of scope. **Not built yet** — this is the design gate; SPEC + implementation are the next step.

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

### Rain + temperature — SH3 (feature 3 extension; scope opened by Q-W5=(c))
> Enumerated 2026-07-09 (SH1 shipped; this is the SH3 phase's Open-Question deliverable — options + rec
> only, NO mechanism chosen). Grounding facts: climate emits per-cell `Moisture∈[0,1]` + `Temperature°C`
> and a **global** `RainProcess` (world-wide Raining flag; while raining Moisture accumulates and
> Temperature drops by `TempRainDrop`). fauna already senses `EnvSample.{Temperature,Moisture}`; the
> `thermal` drive is a **§6 `apparent_temp` content formula** over `temperature/moisture/wind`, so
> **wind-chill relief under shelter already flows for free from SH1** (attenuated wind → warmer apparent_temp).
> SH3 therefore only has to add shelter's effect on the **direct** temp/moisture forcing.

- **Q-S1 — Rain/temperature shelter GEOMETRY.** Rain falls from above, so a roof/canopy/cave protects the
  cell it covers (footprint-local, isotropic) — unlike SH1's *directional leeward* wind-shadow.
  - (a) **Separate overhead-coverage field `ε_cover`** from a new `covers`/`overhead` tag: a covered cell
    is sheltered, neighbours are not; no wind-sector dependence (one field, not six). Distinct from `ε_wind`.
    Caves force both to 0. **rec.**
  - (b) Reuse the directional `ε_wind` shadow for rain too — wrong physics (rain isn't leeward).
  - (c) Wind-driven-rain hybrid — overhead coverage modulated by wind angle (slanted rain reaches under
    eaves). Most realistic, most complex; couples the two fields.
  - `RESOLVED: (a) separate overhead ε_cover field (isotropic, footprint-local; caves force to 0).`
- **Q-S2 — Where shelter acts: read-time value vs climate accumulation.**
  - (a) **Read-time attenuation of the VALUES a consumer senses** (mirror SH1 wind): world reduces the
    Temperature/Moisture an animal reads at its position; **climate STATE untouched** (§0 "climate untouched").
    Cheap, deterministic, "felt" wetness/warmth. **rec.**
  - (b) Modify climate's per-cell accumulation so sheltered *ground* actually stays dry / thermally stable —
    couples climate→shelter, changes climate goldens, bigger blast radius. **Rejected-leaning** (§0).
  - `RESOLVED: (a) read-time value attenuation of sensed Temperature/Moisture; climate STATE untouched.`
- **Q-S3 — Does EXTERIOR shelter get a temperature mechanic, or is SH1 wind-chill relief enough?**
  - (a) **No separate exterior temp mechanic** — exterior shelter = rain/wetness relief + (free) wind-chill
    relief via SH1; direct Temperature buffering is a **cave-only** thing (SH2 Q-C4 stable temp). Minimal, clean. **rec.**
  - (b) Exterior overhead cover also **buffers Temperature toward a stable baseline** by coverage factor
    (damps diurnal swing + rain-drop) — richer, but needs a baseline definition + mutes climate's daily signal.
  - (c) Middle ground — cover suppresses only the **rain-chill (`TempRainDrop`)** component while raining, no
    diurnal buffering. "Stay out of the cold rain" without flattening the day/night cycle.
  - `RESOLVED: (b) exterior overhead cover ALSO buffers Temperature toward a stable baseline by coverage.`
    ⇒ **opens Q-S5 (what baseline?) + Q-S6 (moisture attenuation shape).**
- **Q-S4 — Coverage source + strength schema (mirrors `blocks_wind`).**
  - (a) **New object-kind tag `covers` (or `overhead`)** → `ε_cover` casters, uniform SH3 strength from a
    worldgen constant; per-kind coverage strength from tag data is a follow-up (same pattern SH1 shipped). **rec.**
  - (b) Per-kind coverage strength/height from tag data now (fuller schema up front).
  - (c) Reuse the SAME `blocks_wind` tag for both wind + overhead (a wall both blocks wind AND covers) —
    simplest content, but conflates a vertical wall (no roof) with a roof (overhead). Physically wrong for walls.
  - `RESOLVED: (a) new `covers` tag + uniform SH3 strength (worldgen constant); per-kind strength = follow-up.`
> Consumers (not a question): fauna `EnvSample.{Temperature,Moisture}` at read-time, exactly like SH1's
> `localWindAt` — recommended fauna-only for SH3. `mind/needs` has no thermal need today, so agent thermal
> comfort is out of SH3 scope (agents already have a separate `TakeShelter` action, unrelated to this field).

#### Follow-on questions opened by Q-S3=(b) (temperature buffering)
> Semantics fixed by the resolutions above: `ε_cover ∈ [0,1]`, 1 = open sky, 0 = fully covered.
> `felt_temp = lerp(actual, baseline, 1−ε_cover)` (covered → baseline); `felt_moisture` per Q-S6.
- **Q-S5 — What is the temperature buffering baseline?** (covered cell's felt temp trends to this)
  - (a) **Climate's daily-MEAN temperature** — damps the diurnal swing toward the day's average (annual +
    seasonal cycle preserved); physical "thermal mass". Needs climate to expose its base/daily-delta so SH3
    can read the mean (a read, not a state mutation — §0-safe). **rec.**
  - (b) A fixed content constant `CoverStableTemperature` (milder cousin of caves' stable temp), from
    balance/content. Zero climate change, but a hot-season covered spot feels like the constant year-round.
  - (c) The annual-mid temperature (climate's yearly baseline) — a constant from climate config; ignores
    season entirely, flattest.
  - `RESOLVED: (a) baseline = climate daily-MEAN temperature; climate exposes its base/daily-delta for a §0-safe read.`
- **Q-S6 — Moisture attenuation shape under cover.**
  - (a) **Multiplicative always**: `felt_moisture = actual × ε_cover` (covered = drier microclimate, even on
    dry days). Simplest, mirrors SH1 wind exactly. **rec.**
  - (b) **Gate on the global Raining flag**: cover reduces moisture only WHILE raining (keeps rain off);
    dry-day ground moisture unchanged. More physical (a roof doesn't dry soil on a sunny day).
  - (c) Reduce toward the pre-rain baseline moisture (subtract rain-accumulated excess) — most physical,
    needs a per-cell baseline-moisture reference.
  - `RESOLVED: (b) moisture attenuation GATED on the global Raining flag — cover reduces felt moisture only while raining.`

### Map resolution (from feature 1 — actionable follow-up, NOT part of shelter build but decided here)
- **Q-M1 — Terrain/nav grid resolution vs perception radius.** Today `config/world_content.go:110`
  **hard-errors unless `navmap_cell_size == spatial_hash_cell`** — a pre-hex-migration coupling. Options:
  - (a) **Decouple: drop the sync check; keep `spatial_hash_cell`≈12 (perception), lower
    `navmap_cell_size` to ~6** (⇒ 57×50 hexes, 4× denser terrain, perception/gameplay unchanged). **rec.**
  - (b) Keep coupling; lower both to ~6 (finer terrain **and** finer perception buckets — changes fauna
    perception + more hash buckets).
  - (c) Leave at 12 (status quo).
  - `OPEN`
