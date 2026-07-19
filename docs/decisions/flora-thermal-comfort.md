# Flora suitability: temperature as a symmetric comfort band (Why)

> Deliberation cut from the living docs per the Documentation Triad. Pointer lives in
> `docs/plans/flora.md §1l`. Resolved 2026-07-19 (human RESOLVE: "fauna와 똑같이 comfort_temp 방식").

## Symptom
On the live `rabbit_meadow` world every `berry_shrub` dies within ~250 ticks. Measured census
(3000-tick headless run of the shipped fixture, throwaway probe):

| tick | ambient | berry_shrub | tall_grass | grass | wildflower |
|---|---|---|---|---|---|
| 0 | 12.5 °C | **200** | 375 | 1000 | 300 |
| 250 | 7.5 °C | **0** | 336 | 1000 | 300 |
| 3000 | 5.6 °C | **0** | 214 | 3477 | 963 |

The browse layer that `deer`/`bear` forage from (the `berries` yield) disappears permanently a few
hundred ticks into every run. `wildflower`, meanwhile, grows without bound and never dies.

## Root cause — CA3 flipped the unit, the content did not follow
Climate CA3 rebased `Temperature` to **°C** (`docs/plans/climate.md`; annual cycle
`annual_mid 12.5 ± annual_amp 17.5` ⇒ ≈ −5…30 °C). The flora §6 formulas in `content/objects.yaml`
were authored against the older **[0,1]** convention and were never migrated, so three mutually
incompatible conventions shipped side by side:

- `berry_shrub`: `(1 - temperature)*0.2` — at 12.5 °C this term is **−2.3**, dragging suitability
  below 0. `Suitability` clamps to [0,1] ⇒ 0, which is under `death_threshold 0.20`, so
  `DeathStreak` increments on every flora step and the species is wiped after
  `death_hysteresis 4` steps × cadence 60 = **240 ticks**. The measured extinction between tick 0
  and 250 matches the arithmetic exactly.
- `wildflower`: `temperature*0.3` — above ~3 °C this term alone exceeds 1, so suitability
  **saturates at 1.0 year-round**. The species is immune to drought, slope and season: it grows at
  the maximum rate and can never die.
- `dry_shrub`: `(temperature/40)*0.2` — the only °C-aware formula, but an ad-hoc normalization
  with no notion of an optimum (hotter is always better, without limit).

The unit mismatch was known in a narrow context — `scaling.md` SC4 notes that world-gen density
placement deliberately weights by `carrying_capacity` because "Suitability는 temperature °C-vs-[0,1]
불일치로 회피" — but the workaround was applied only to placement, never to the runtime growth path,
so the live defect survived.

## Options considered
- **(A) Rescale in-formula** — spell `temperature/40` everywhere, as `dry_shrub` already does.
  Minimal diff and no engine change, but it encodes no optimum: growth stays monotone in
  temperature, so "too hot" is inexpressible and every author must re-derive the magic divisor.
  It also leaves the same trap armed for the next species.
- **(B) Symmetric comfort band, mirroring fauna FA5** ← **CHOSEN**. Per-species `comfort_temp` /
  `thermal_band` scalars plus a derived §6 operand. Cold *and* heat deviations both reduce
  suitability, which is the actual botany, and the mechanism is one the project already ships,
  reviewed and tested, for animals.
- **(C) Normalize the `temperature` operand engine-side** — make flora's `temperature` mean
  "normalized [0,1]" again. Rejected: it silently re-breaks `dry_shrub`, diverges from the
  `temperature` operand every other subsystem (fauna, climate transitions) reads, and hides a unit
  in the engine that content authors cannot see. CA3's whole point was one project-wide unit.

## Resolution — (B), the fauna FA5 mechanism reused verbatim
Fauna FA5 (`docs/decisions/fauna-gates.md`, FA5 section) already answers "how does a living thing
respond to temperature": a symmetric comfort band around a species optimum,

    thermal = clamp01(|apparent_temp − comfort_temp| / thermal_band)

with `thermal_band ≤ 0` as the neutrality lever. Flora adopts the same shape, the same field names,
and the same sign convention:

    thermal_stress = clamp01(|temperature − comfort_temp| / thermal_band)

**Why expose stress rather than comfort.** Keeping fauna's polarity means one concept, one sign, one
mental model across the two living-thing subsystems — `0` is always "comfortable", `1` is always
"maximally stressed". It also makes the content fix a drop-in for the broken term: `(1 - temperature)`
becomes `(1 - thermal_stress)`, preserving each species' existing weighted-sum shape and weights.
The `[0,1]` clamp lives in flora (not, as in fauna, in the drive update) because flora suitability
terms are weighted summands that must stay in range.

**Why a derived operand instead of writing the band in the formula.** The §6 DSL has no `abs()` and
no unary minus (expr OQ-C), so a symmetric band is not expressible in content arithmetic. The engine
computes it and exposes the result as a read-only operand, exactly as fauna does.

**Fail-loud cross-validation.** `platform/config` rejects a species whose formulas read
`thermal_stress` without authoring a positive `thermal_band`. Fauna does not do this, but the
original defect was precisely a formula silently evaluating against a meaningless temperature scale;
the loader already performs this class of check (operand allow-lists, `neighbor_count` circularity),
and a load-time error costs nothing at runtime. Species that never mention the operand are
unaffected.

## Calibration
Against the shipped −5…30 °C annual cycle:

- `berry_shrub` — `comfort_temp 14`, `thermal_band 16`: a temperate shrub, saturating stress at
  −2 °C and 30 °C. Suitability stays well above `death_threshold 0.20` at ordinary moisture, so the
  species now persists and merely grows more slowly at the extremes, instead of dying out.
- `wildflower` — `comfort_temp 18`, `thermal_band 14`: a summer bloomer, saturating at 4 °C and
  32 °C. Winter suitability drops toward its `death_threshold 0.18`, so blooms genuinely thin out
  in the cold season rather than being immortal.

Both are UNTUNED placeholders in the same sense as the fauna comfort values (balance target).
`grass`, `tall_grass` and `oak` read no temperature at all and are untouched; `dry_shrub` keeps its
`temperature/40` term for now — migrating it is a separate, non-urgent content change, since it is
merely crude rather than broken.

## Consequences
- Seasonality reaches flora for the first time: growth rate now varies over the year for the two
  banded species, which is the intended coupling from `world-roadmap.md` ("계절 → 전부").
- Existing goldens are unaffected: no shipped golden digests these two species' suitability, the
  operand is inert for species that do not read it, and `thermal_band ≤ 0` reproduces the previous
  neutral value.
- `carrying_capacity` stays temperature-free for every species, so world-gen density placement
  (which runs before climate exists, `scaling.md` SC4) is unchanged.
