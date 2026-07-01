# Activation Gate — human decisions before the ecosystem turns ON

> **Why this doc.** The ecosystem SPECs (climate / flora / decay / fauna / scent + the world env/fauna
> wiring) are built to ship **inert** ("outcome-neutral until activated") so existing goldens hold. The
> machinery can be IMPLEMENTED from the SPECs without these decisions. But **turning it ON** (placing
> species, climate ON, flora `Rules` ON, the °C re-baseline) crosses the **Open-Question gate**
> (`CLAUDE.md`): each item below is `OPEN` until **the human** flips it to `RESOLVED: <answer>`. An
> implementation agent (incl. an external one) **MUST NOT pick an option or invent a mechanism** for an
> `OPEN` item tagged to the phase it is building — it stops and returns the list. *"Inventing a
> mechanism is a defect, not initiative."*
>
> This file **collects** the OPEN items already living in the module SPECs/plans (it does not replace
> them — the authoritative text stays in each SPEC's "Open Questions"). Recommendations are quoted from
> those SPECs.

> **✅ ACCEPTED 2026-06-29 — the human confirmed ALL recommendations.** Every `PROPOSED` below is now
> the chosen answer. Concrete inputs for the two flagged items:
> - **G5:** activate the starter-fixture species at **`deer×6 · rabbit×8 · goat×2 · wolf×1 · bear×1 ·
>   fish×8`** (per-species population target = the placed count); **order: env (climate+flora) first →
>   re-baseline → fauna**; population maintenance = W11 respawn-to-target (no birth).
> - **G4:** annual band **`AnnualMid 12.5 / AnnualAmp 17.5`** (≈ −5…30 °C), °C re-baseline **per
>   consumer at its activation**.
>
> The owning SPECs' Open-Question entries are to be flipped to `RESOLVED` as each dependent phase is
> built. The original PROPOSED text is kept below for traceability.

Legend: **[BLOCK]** = blocks ecosystem activation. **[STAGE]** = a "when do we build this later phase"
call. **[DEFAULT]** = non-blocking; a default is already chosen — confirm or leave.

---

## Part 1 — Activation-blocking decisions (resolve before flora/fauna go live)

### G1 [BLOCK] Flora harvest actions: `Forage` / `Fell` / `Plant` (P_f4)
- **Source:** `backend/engine/env/flora/SPEC.md` §Open Questions; `docs/flora.md` 1e.
- **Blocks:** the active flora resource loop (harvesting yields, destructive removal).
- **Options:** (a) add `Fell`(destructive→object-mortality)+`Plant`(owner-setting) to
  `content/actions.yaml` and wire `world` apply to call `Rules.Yield` + (for Fell) add target to
  `flora.Died`; (b) keep only the existing `Forage` for now, defer `Fell`/`Plant`.
- **SPEC rec:** **(a) for `Fell`+yield, defer `Plant`** to the economy phase (Plant only sets `Owner`,
  inert in P1).
- `DECISION:` ✅ **PROPOSED (rec):** add `Fell` (destructive harvest → `Rules.Yield` + `flora.Died`) +
  keep `Forage` (non-destructive) now; **defer `Plant`** to the economy phase (it only sets `Owner`,
  inert in P1).  *(awaiting your confirm)*

### G2 [BLOCK] `berry_bush` migration to a flora species (P_f1/P_f4)
- **Source:** `backend/engine/env/flora/SPEC.md` §Open Questions.
- **Blocks:** whether the live hunger loop's `berry_bush` becomes a growing/propagating flora species
  (affects existing hunger-loop goldens).
- **Options:** (a) convert `berry_bush` in place (add `flora:` + `yields:`, drop `depletes` +
  `balance.regen.berry_bush`), accept a deliberate hunger-loop golden re-baseline; (b) keep
  `berry_bush` as a legacy object_kind and add a NEW `berry_shrub` flora species (zero golden churn
  while flora-off).
- **SPEC rec:** **(b) for the flora-off phases**, then **(a) at activation** with the re-baseline.
- `DECISION:` ✅ **PROPOSED (rec):** (b) add a new `berry_shrub` flora species while flora is OFF (zero
  golden churn); then (a) migrate `berry_bush` in place at activation with a deliberate hunger-loop
  golden re-baseline.  *(awaiting your confirm)*

### G3 [BLOCK] Perception ↔ flora shade interface (P_f3)
- **Source:** `backend/engine/env/flora/SPEC.md` §Open Questions (two linked items); `SPEC-world-env.md`
  `ShadeOccluders`.
- **Blocks:** the "dark forest" LoS attenuation — flora shade affecting agent sight.
- **Option set 3a (occluder accessor):** (a) `WorldSnapshot.ShadeOccluders(center,radius)
  []ShadeOccluder` (perception composes the multiplicative attenuation); (b)
  `WorldSnapshot.LightTransmission(segment) float64` (world/flora composes). **rec: (a).**
- **Option set 3b (`Sight` semantics):** (a) make `Sight` return a continuous visibility STRENGTH
  (re-baselines perception goldens); (b) threshold the composed transmission (seen iff ≥ τ) — keeps
  `Sight` boolean, smaller blast radius. **rec: (b).**
- `DECISION 3a:` ✅ **PROPOSED:** (a) `ShadeOccluders(center,radius)` — perception composes the
  multiplicative attenuation (mirrors existing `IsOpaque`/`EntitiesInRadius`).
  `DECISION 3b:` ✅ **PROPOSED:** (b) threshold the composed transmission (`seen iff ≥ τ`) — keeps
  `Sight` boolean, minimal golden churn.  *(awaiting your confirm)*

### G4 [BLOCK] Cross-module °C threshold re-baseline (climate activation)
- **Source:** `backend/engine/env/climate/SPEC.md` §Open Questions ("Cross-module °C re-baseline");
  `docs/world-integration.md §3`. (Schemas already updated to CA1-3; this is the DATA threshold pass.)
- **Blocks:** correct behaviour once climate is ON. `temperature` is now **actual °C** (CA3, may be
  sub-zero). Every consumer's §6 thresholds authored against the old normalized [0,1] temperature MUST
  be re-based to °C: **flora** suitability/growth, **decay** `accel` (Dm3), **fauna** `apparent_temp`
  (F40), and `content/climate.yaml` `when: temperature > X` transition thresholds.
- `DECISION:` ✅ **PROPOSED (rec):** re-baseline °C thresholds **per consumer at its own activation**
  (not one big-bang), each with its own golden re-baseline; target annual swing **≈ −5…30 °C**
  (`AnnualMid≈12.5`, `AnnualAmp≈17.5`).  *(awaiting your confirm)*

### G5 [BLOCK] Species placement + `Install*` activation switch
- **Source:** `engine/world/SPEC-world-fauna.md` + `SPEC-world-env.md` (the `InstallEnv`/`InstallFauna`
  + fixture placement); `docs/world-integration.md` W11 (population). The starter fixture already places
  deer/wolf/rabbit/goat/bear/fish (recent commits) — placement DATA exists; this is the ON switch + order.
- **Blocks:** literally turning it on — until species are placed AND `InstallFauna`/`InstallEnv` are
  called with non-empty `Rules`, `fauna.Step`/env Step produce nothing.
- `DECISION:` ✅ **PROPOSED (rec):** activate the species already in the starter fixture; **order: env
  first** (climate + flora ON → re-baseline env goldens) **→ then fauna** (re-baseline fauna goldens).
  Population maintenance = **W11 respawn-to-target at a hidden wild location** (no birth — wiring only,
  not a decision); legacy `prey` timer-respawn coexists until migrated (W7).  ✏️ *Confirm species set /
  counts / per-species starting population targets, or adjust.*

---

## Part 2 — Phase-staging decisions (later fauna phases; decide WHEN to build)

### G6 [STAGE] `apparent_temp` activation — weather→thermal→movement (P_fa4)
- **Source:** `backend/engine/fauna/SPEC.md` §Out of Scope (climate operand source + apparent_temp);
  `docs/climate.md §1c` CA1–CA3.
- **Effect when activated:** climate-ON makes `apparent_temp` live → the `thermal` drive rises → it
  drives `Rest` preference AND `speed` (the `thermal` term added to every species' speed §6 on
  2026-06-29; P1 climate-OFF ⇒ thermal 0 ⇒ neutral). Also enables wind-driven long-range scent +
  upwind homing (F33).
- `DECISION:` ✅ **PROPOSED (rec):** schedule **P_fa4 after climate activation (G4)**; keep the current
  first-draft per-species thermal→speed coefficients (deer 0.6 / wolf 0.5 / rabbit 0.7 / goat 0.3 /
  bear 0.25 / fish 0.5) and re-tune them in the P_fa4 balance pass.  *(awaiting your confirm)*

### G7 [STAGE] Agent-as-threat sight: `agent.disposition` (F46, P_fa3)
- **Source:** `backend/engine/fauna/SPEC.md` §Out of Scope (F46).
- **Effect:** a herbivore that SIGHTS an agent reads the agent's disposition (a signed §6 over the
  agent's REAL base stats) as the operand `agent.disposition`: positive → stay, negative → flee.
  "Hunter" emerges from stats (D2), not a flag. P_fa1 sight = predator ANIMALS only.
- `DECISION:` ✅ **PROPOSED (rec):** §6 `agent.disposition = Sociability − Aggression − Vindictiveness`
  (coefficients in `content`/`balance`, D4); world injects agent base stats into the fauna sight query;
  schedule **P_fa3**.  *(awaiting your confirm)*

### G8 [STAGE] Carcass + `Butcher` + decay-lot mapping (P_fa3)
- **Source:** `backend/engine/fauna/SPEC.md` §Out of Scope (F11/F12/F37/F38).
- **Effect:** a dead animal becomes a `carcass` object → `Butcher` extract → maps into the decay-lot
  system (rotting → a future `scent:carrion` channel).
- `DECISION:` ✅ **PROPOSED (rec):** schedule **P_fa3** — add a `carcass` object_kind + `Butcher` action
  + decay-lot mapping (a carcass becomes a perishable lot); the `Graze`/`Flee`/`Wary`/`Hunt`/`Rest`
  actions already exist in `content/actions.yaml`, so only the `IDs()`/`Producers` golden re-baseline is
  needed there.  *(awaiting your confirm)*

---

## Part 3 — Non-blocking (default already chosen; confirm or leave as-is)

| ID | Item | Source | Default (rec) |
|----|------|--------|---------------|
| G9 | Grass/flower shade & yield shape | flora SPEC | (a) tiny `width`+`width→0` shade coefficients (no schema change) |
| G10 | Flora bulk cadence N vs climate N | flora SPEC / world | (a) N = 60 (aligned); profile later |
| G11 | `NeighborCount` species scope (propagation density) | flora SPEC / `SPEC-world-env.md` | (a) same-species only |
| G12 | Animal id allocation convention | `SPEC-world-fauna.md` | (a) distinct prefix `an:<n>` (disjoint from AgentID) |
| G13 | Terrain attribute-vector accessor (`SiteInput.TerrainAttrs`) | `SPEC-world-env.md` | (a) world reads `map[TerrainID]Attrs` from `terrain.yaml` (config), samples by `nav.TerrainAt` |
| G14 | Climate-cell ↔ navmap-cell size ratio | climate SPEC / `SPEC-world-env.md` | coarse climate (e.g. 32u) over fine navmap (8u) ⇒ 4×4; profile |
| G15 | Initial moisture/temperature seeding | climate SPEC | single `Config.Init*` value for P1 (per-terrain later) |
| G16 | `wind.dir`/`wind.mag` glossary coin | climate SPEC | register in `docs/glossary.md` (naming only) |
| G17 | flora-as-`objects[]` sync point | `SPEC-world-env.md` | apply-flora-deltas is the single sync (spawn adds both, die removes both, grow updates morphology) |

- `DECISION (Part 3):` ✅ **PROPOSED:** accept all G9–G17 defaults as-is (each is the SPEC's rec and
  non-blocking).  ✏️ *Override any single row if you disagree.*

> **Already RESOLVED (no longer open):**
> - navmap footprint-only passability + per-cell base cost → RESOLVED (a)-as-built 2026-06-29
>   (`navmap.FootprintBlocked` + `navmap.BaseCost` in `navmap/SPEC.md`; `SPEC-world-fauna.md` Open Qs).
> - thermal→speed link → added to content §6 + fauna F35 (2026-06-29).
> - WI-P0 schemas (`fauna:` block / `climate` CA1-3 / `terrain.yaml`+schema) → AUTHORED 2026-06
>   (`platform/config/SPEC-world.md` status table corrected).

---

## How to use this

1. The DECISION lines above are **pre-filled proposals**. Review each: accept (✅) or override (✏️).
2. For each accepted item, flip the corresponding **Open Questions** entry in the named SPEC to
   `RESOLVED: <answer>` (the SPEC stays authoritative; this doc is the index).
3. Only then dispatch the dependent phase (flora/fauna activation, P_fa3/P_fa4). Part 1 gates the
   activation; Part 2 gates the named later phase; Part 3 can proceed on its defaults.
