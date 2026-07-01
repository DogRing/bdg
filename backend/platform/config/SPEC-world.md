# SPEC — `platform/config` · World/Env Loading (world.yaml · climate.yaml · fauna/flora §6)

> Status: `DRAFT`
> Sub-spec of: `SPEC.md`  ·  Owner agent: `<filled by implementer>`
> Scope = **WI-P0** (`docs/world-integration.md` §2). Feeds `world.InstallEnv`/`InstallFauna`
> (`backend/engine/world/SPEC-world-env.md` + `SPEC-world-fauna.md`).

## Scope

This sub-spec extends `platform/config` (the single content-loading + schema-validation layer) to
load the **world-integration content** and build the env subsystems' inputs: it parses
`content/world.yaml` → `world.EnvConfig` + the per-module geometry Configs, compiles the `fauna:`/
`flora:` §6 formulas and the `climate.yaml` transition table into the engine `Rules` (via
`engine/kernel/expr`), runs the **load-time cross-checks** the engine modules require, and folds the
new files into `ConfigHash`. It produces the **already-validated, already-compiled** inputs the
composition layer hands to `world.InstallEnv`/`InstallFauna`. The engine never parses YAML and never
sees an unvalidated formula (architecture §1; D10).

It does **not** build the live `navmap.NavMap`/`climate.State`/initial `flora.State`/`decay.State`
from a terrain layout — those need the **fixture** (`terrainAt`, placed plants/animals) which is the
world-gen / scenario loader (W9, WI-P4). WI-P0 produces the **Configs + compiled Rules + terrain
type table**; the State construction + the `InstallEnv`/`InstallFauna` *calls* are the composition
layer once the fixture loader lands. (Until then, env stays OFF — every existing golden holds.)

## Content files added to the load pipeline

`LoadContent(dir)` (SPEC.md) gains these files (read, schema-validated BEFORE any engine build,
then compiled). All are **optional**: a missing file ⇒ that subsystem stays OFF (neutral), so the
current `content/` keeps loading unchanged.

| file | schema | builds | status |
|------|--------|--------|--------|
| `world.yaml` | `world.schema.json` ✅ | `world.EnvConfig` + `climate.Config` geometry + `navmap.Config` + scent cellSize + `fauna.Cadence`/DT | schema ✅ |
| `climate.yaml` | `climate.schema.json` ✅ | `climate.Rules` (transition table) + `climate.Config` rates | schema ✅ (CA1-3 updated 2026-06; °C, annual, wind) |
| `objects.yaml` `flora:` | `objects.schema.json` (`flora:` ✅) | `flora.Rules` (per-species §6) | schema ✅ |
| `objects.yaml` `fauna:` | `objects.schema.json` (`fauna:` ✅) | `fauna.Rules` (per-species §6 + senses + drives) | schema ✅ (authored 2026-06) |
| `objects.yaml` `decay:` | `objects.schema.json` (`decay:` ✅) | `decay.Rules` | schema ✅ |
| `terrain.yaml` | `terrain.schema.json` ✅ | `map[TerrainID]navmap.TerrainType` + the §5 attribute presets | file+schema ✅ (exist) |

**Schema work this phase requires — ALL DONE as of 2026-06 (verify, do NOT re-author):**
1. ✅ `world.schema.json` — written (`content/schema/world.schema.json`).
2. ✅ `objects.schema.json` **`fauna:` block** — AUTHORED (`$defs/fauna`): candidate `actions[]` +
   per-action `utility` §6, `drives[]` (ids + rate/level params), `apparent_temp` §6, `speed` §6,
   `threat:`/`scent:` tags, diet, `senses{smell_radius,sight_radius,fov_arc}`, `terrain_cost`/`impassable`.
3. ✅ `climate.schema.json` **CA1-3** — UPDATED: `initial_temperature` is °C (no clamp), `balance`
   carries `annual_mid`/`annual_amp`/`annual_phase` (CA1) + `wind_*` (CA2) + per-°C `evap_temp_scale`.
4. ✅ `terrain.yaml` + `terrain.schema.json` — EXIST (the terrain type catalog: base material →
   `BaseCost`/`Passable`/`RequiredTags` + §5 attribute presets).

> The SCHEMAS (shapes) are in place. What remains is the **tuned DATA at each subsystem's activation**
> (the final `fauna:` species values, the `climate.yaml` °C `when:` thresholds re-based to actual °C —
> the G4 re-baseline in `docs/activation-gate.md`) + the loader CODE itself (this module's implementation).

## Build outputs (new `Registries` fields + accessors)

`LoadContent` returns the new compiled artifacts on `Registries` (immutable, caller-owned). The
`BalanceDoc` accessor pattern (SPEC.md) extends with world geometry; the compiled `Rules` are built
during `LoadContent` (they need cross-registry validation), not lazily.

```go
// Added to Registries (all nil when the source file is absent ⇒ subsystem OFF):
type Registries struct {
    // … existing: Stats, Gates, Actions, Needs, Balance …
    WorldEnv     *world.EnvConfig            // from world.yaml (geometry + grids + cadence + DT)
    ClimateCfg   *climate.Config             // climate rates + grid geometry (world.yaml grids + climate.yaml balance)
    ClimateRules *climate.Rules              // compiled transition table (climate.yaml transitions via expr)
    NavCfg       *navmap.Config              // navmap geometry from world.yaml bounds + navmap_cell_size
    TerrainTypes map[navmap.TerrainID]navmap.TerrainType // terrain type table (terrain.yaml) — navmap.New input + world TerrainSampler join
    FloraRules   *flora.Rules               // compiled per-species flora §6 (objects.yaml flora:)
    FaunaRules   *fauna.Rules               // compiled per-species fauna §6 (objects.yaml fauna:)
    DecayRules   *decay.Rules               // compiled decay table (objects.yaml decay:)
    ScentEmitters map[core.Tag][]core.Tag    // object kind id → sorted raw `scent:<channel>` tags; world resolves known tokens
}
```

> Naming: `world.EnvConfig` (env GEOMETRY/cadence, this build) is DISTINCT from `config.EnvConfig`
> (deployment env vars: SEED/RUN_ID/…, SPEC.md). Different packages, package-qualified — no Go
> collision. (Open Questions: rename `world.EnvConfig`→`world.EnvSetup` if the qualified pair reads
> confusingly.)

`WorldEnv`/`ClimateCfg`/`NavCfg` are assembled from `world.yaml` + `climate.yaml`:
- `world.EnvConfig{Min,Max}` ← `world.yaml bounds` (a fixture may override at compose time, W1).
- `world.EnvConfig{NavmapCellSize, ClimateGridCols/Rows, ClimateStep/FloraStep/DecayStep,
  ScentCellSize, ScentSpread, FaunaDT, FaunaCadence, MaxSpeed}` ← `world.yaml grids/cadence/motion`.
- `climate.Config{GridCols/Rows, WorldMin/Max}` ← `world.yaml grids + bounds` (so climate's grid and
  navmap's agree, climate SPEC §New); `climate.Config{rates, AnnualMid/Amp/Phase, Wind*}` ←
  `climate.yaml balance` (post CA1-3 update).
- `navmap.Config{CellSize, MinX/Y, MaxX/Y, Wear*}` ← `world.yaml grids.navmap_cell_size + bounds` +
  `balance.yaml` wear knobs.

## §6 compilation + load-time cross-checks (the WI-P0 validation, D10)

`platform/config` does the YAML parse + the `expr.Parse` compile + ALL referential validation, then
builds each opaque `Rules` via the module's config-facing constructor (the module receives compiled
`expr.Program`s, never YAML — see Open Questions on the constructor seam). The checks, each a typed
load error that builds NO partial registry (SPEC.md invariant):

1. **§6 compiles** — every `fauna:`/`flora:` formula + every `climate.yaml` `when:` compiles via
   `expr.Parse`; a parse/identifier error names the species + formula (expr load-fail policy, D10).
2. **fauna operand cross-check** — each compiled fauna program's `expr.ReadsAttrs()` ⊆
   `fauna.AttrOperands()` ∪ that species' drive ids; `expr.Reads()` (Stat channel) ⊆ the stats
   registry; candidate `actions[]` ⊆ `actions.Registry.IDs()`; diet/`threat:` tags well-formed
   (fauna SPEC AC parity). A typo Attr (silently 0 in expr) is a LOAD failure.
3. **flora operand cross-check** — flora program operands ⊆ the flora operand set (suitability
   moisture/temperature/terrain attrs); yield `item`s ⊆ item_kind ids (flora SPEC).
4. **climate cross-check** — every transition `from`/`to` ⊆ `terrain.yaml` ids; `when:` operands ⊆
   `{moisture, temperature}` (+ permitted); the transition table compiles (climate SPEC).
5. **scent-cell floor (W3)** — `world.yaml grids.scent_cell_size ≥ motion.max_speed ×
   cadence.scent_spread` — a determinism-protecting check (the scent cell-skip invariant). Reject with
   a descriptive error naming the three values.
6. **grid sync** — `world.yaml grids.navmap_cell_size == balance.yaml world.spatial_hash_cell`
   (warn-or-error; they index the same continuous space, W2). bounds: `max > min` per axis.
7. **terrain ids** — base-material / resource-affinity terrain ids ⊆ `terrain.yaml` (resources R7,
   world-gen WG3/WG4 references).

## ConfigHash + determinism

- `ConfigHash()` (SPEC.md) folds the NEW files into the fingerprint: `world.yaml`, `climate.yaml`,
  `objects.yaml`, `terrain.yaml` raw bytes in the same FIXED lexicographic filename order, so a run
  stays reproducible from `seed + config_hash + last snapshot` (data-contracts §3). Adding a file
  changes the hash (intended).
- **No new IO/clock/rand** — same pure files-in → registries-out contract; the §6 compile is pure
  (expr.Parse), the cross-checks iterate sorted ids (D12), no map-order dependence.
- **Schema-before-build** — every new file is schema-validated before its engine build; a structural
  violation returns a typed error and builds NO registry (SPEC.md invariant, extended to the new files).

## Acceptance Criteria (testable)

- [ ] **world.yaml load + EnvConfig** — `LoadContent` parses a valid `world.yaml` into
  `Registries.WorldEnv`/`ClimateCfg` geometry/`NavCfg` with the documented field mapping; a
  `world.schema.json` violation (e.g. missing `bounds`, negative cell) returns a typed error and no
  registry. A run WITHOUT `world.yaml` loads exactly as today (`WorldEnv == nil`, env OFF).
- [ ] **scent-cell floor enforced (W3)** — a `world.yaml` with `scent_cell_size < max_speed ×
  scent_spread` is rejected with an error naming the three values; the boundary (`==`) passes.
- [ ] **grid-sync + bounds checks** — `navmap_cell_size ≠ balance.spatial_hash_cell` and `max ≤ min`
  on an axis each produce a descriptive error (table-driven).
- [ ] **fauna §6 compile + operand cross-check** — a `fauna:` species whose `utility` references an
  Attr NOT in `AttrOperands() ∪ drives` fails to load with an error naming the species + operand; a
  Stat not in the registry, or a candidate action not in `actions.Registry`, likewise fails; a valid
  block builds `FaunaRules`. (Mirrors fauna SPEC's config cross-check ACs.)
- [ ] **flora/climate §6 compile + cross-check** — a `flora:` formula or a `climate.yaml` `when:`
  with an unknown operand / unknown terrain id fails to load with a descriptive error; valid content
  builds `FloraRules`/`ClimateRules`.
- [ ] **ConfigHash includes the new files** — adding/altering one byte of `world.yaml`/`climate.yaml`/
  `objects.yaml`/`terrain.yaml` changes `ConfigHash`; two identical loads match (D12).
- [ ] **Optional-file neutrality** — with none of world/climate/terrain present and no flora/fauna
  blocks, `LoadContent` returns the existing registries unchanged + nil env fields (env OFF golden).
- [ ] **No IO/clock/rand beyond reading the listed files** — determinism guard via in-memory fs
  (SPEC.md parity); the §6 compile + cross-checks add no nondeterminism.

## Out of Scope

- **Building the live `navmap.NavMap`/`climate.State`/initial `flora.State`+`decay.State` from a
  terrain layout, and CALLING `world.InstallEnv`/`InstallFauna`** → the fixture/world-gen loader +
  the composition layer (W9, WI-P4; `docs/world-gen.md`). WI-P0 produces Configs + Rules + the
  terrain type table only.
- **Authoring the tuned DATA** — the `fauna:` species blocks, the `climate.yaml` °C thresholds +
  annual/wind constants, the `terrain.yaml` catalog values → each subsystem's activation phase
  (P_fa3 / climate M-phase / world-gen). WI-P0 fixes the SHAPES (schemas) + the loader.
- **The engine modules' Rules CONSTRUCTORS** (the exact `NewRules`/`Compile` signature each of
  climate/flora/fauna/decay exposes for config to call with compiled `expr.Program`s) → each engine
  module's SPEC (some need a small addition — see Open Questions).
- **Snapshot/Redis/SSE serialization of env state** → `platform/persist`/`platform/api` (WI-P4, W8).
- **Scenario YAML loading** (`-scenario`) → stays the dev-tool path (SPEC.md Out of Scope), now also
  a candidate `InstallEnv` caller once it carries a terrain layout.

## Open Questions

> `docs/world-integration.md` W1-W9 RESOLVED. These are config/engine **plumbing seams** surfaced
> writing this sub-spec — interface shapes to settle before implementation, not mechanism choices:

- **Rules-constructor seam — RESOLVED (a), 2026-06-28.** Each env module now exposes a config-facing
  `NewRules(...)` taking **already-compiled** `expr.Program`s + thresholds (config does YAML +
  `expr.Parse`, hands Programs in — "module never parses YAML" stays literally true, D5):
  `climate.NewRules([]TransitionRule)`, `flora.NewRules(map[SpeciesID]SpeciesRule)`,
  `fauna.NewRules(map[SpeciesID]SpeciesRule)` (+`DriveRule`), `decay.NewRules(map[KindID]KindRule)`
  (+`StateRule`/`TransformRule`). See each module's SPEC Public Interface. platform/config calls these
  after the §6 compile + cross-checks.
- **`world.EnvConfig` vs `config.EnvConfig` naming.** Two `EnvConfig`s (env geometry vs deployment
  knobs), different packages. Options: **(a)** keep both (package-qualified); **(b)** rename
  `world.EnvConfig` → `world.EnvSetup`/`world.EnvParams`. **rec: (a)** for now (no Go conflict),
  rename only if it reads confusingly in the composition layer. Non-blocking.
- **`terrain.yaml` ownership — RESOLVED 2026-06 (file + schema EXIST).** `content/terrain.yaml` +
  `content/schema/terrain.schema.json` are authored (the shared terrain type catalog: base material →
  cost/passable/§5 attrs, referenced by navmap/climate/flora/resources/world-gen). Implementation reads
  the §5 attribute preset vector from it for the `SiteInput.TerrainAttrs` seam (`SPEC-world-env.md` G13).

## Notes

- This sub-spec mirrors SPEC.md's existing contract (schema-validate → engine build → cross-check →
  ConfigHash) — it just adds the world/env files + the §6 compile step + the env cross-checks. The
  `BalanceDoc` accessor pattern is unchanged; the env Configs/Rules are new `Registries` fields
  because they require cross-registry validation at load (not lazy accessors).
- The §6 compile is the one place `platform/config` uses `engine/kernel/expr` (the L0 shared
  evaluator) — `expr.Parse` per formula, then the module `NewRules` with the compiled `Program`s.
  This realizes the "platform/config compiles, the engine evaluates" split every env SPEC names.
- **Optional-file neutrality is the WI-P0 safety lever**: until the world/climate/terrain files +
  flora/fauna blocks are authored AND a fixture installs the state, env is OFF and every existing
  golden holds — matching the engine-side `InstallEnv`/`InstallFauna` opt-in (SPEC-world-env/fauna).
- Reference paths: `docs/world-integration.md` (WI-P0, W1-W9), `content/world.yaml` +
  `content/schema/world.schema.json`, `SPEC.md` (the base loader pipeline + ConfigHash + accessors),
  `backend/engine/world/SPEC-world-env.md` + `SPEC-world-fauna.md` (the InstallEnv/InstallFauna
  consumers), `backend/engine/env/{climate,flora,decay}/SPEC.md` + `backend/engine/fauna/SPEC.md`
  (the `Rules` shapes + AttrOperands cross-check), `backend/engine/kernel/expr/SPEC.md` (`Parse`/
  `ReadsAttrs`/`Reads`), `content/schema/{climate,objects}.schema.json` (the schemas to update/extend).
