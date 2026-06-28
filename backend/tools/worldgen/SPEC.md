# SPEC — `tools/worldgen` · World-Gen Generator + Fixture Loader (WI-P4 input)

> Status: `DRAFT`
> Leaf level: composition / `config`-class IO-init (architecture §3; world-gen.md WG7 — **engine 아님**)
> Owner agent: `<filled by implementer>`
> Scope = **WI-P4 input** (`docs/world-integration.md` §2 / `docs/world-gen.md` / W1/W9).

## Purpose

The **author-time generator** + the **run-time fixture loader** for the world (`docs/world-gen.md`).
Two roles over ONE shared `Fixture` format (`content/schema/fixture.schema.json`, W9 — the unified
world-gen-OR-scenario shape):
1. **Generate (author-time):** `seed → Fixture` via the deterministic WG1-a pipeline
   (elevation → slope → flow-accumulation hydrology → moisture → base material → resource
   distribution → entity seeding, `docs/world-gen.md §1`). Run by a separate tool/cmd; writes a
   fixture file. **Runtime generation = 0** (D12) — the engine only LOADS.
2. **Load (run-time):** `Fixture → env states → world.InstallEnv/InstallFauna + Spawn/PlaceObject`.
   Parses + validates the fixture, builds `navmap`/`climate`/`flora`/`decay` states (+ the animal set
   + scent grid) from the terrain layout + placements, and installs them into a constructed world,
   joining the **compiled `Rules`** `platform/config` already built (WI-P0). This is the composition
   step `backend/engine/world/SPEC-world-env.md` / `SPEC-world-fauna.md` deferred to "the fixture
   loader" and `platform/config/SPEC-world.md` left "out of scope".

It is **not the engine** (it imports engine constructors but the engine never imports it, architecture
§7 "fixtures are per-run, not content"). It owns no simulation logic — only generation + parse + the
construction wiring.

## Public Interface

```go
package worldgen

import (
    "github.com/dogring/bdg/engine/kernel/core"
    "github.com/dogring/bdg/engine/kernel/rng"
    "github.com/dogring/bdg/engine/world"
    "github.com/dogring/bdg/platform/config"
)

// ── Fixture (the unified per-run format, W9; content/schema/fixture.schema.json) ──
// The shape produced by Generate AND consumed by Load — identical for a generated world and a
// hand-authored scenario. Pos values are continuous (D11). All blocks except Seed are optional;
// an absent block ⇒ that subsystem is OFF (env-neutral).
type Fixture struct {
    SchemaVersion int
    Seed          int64
    Bounds        *Bounds          // nil ⇒ use world.yaml default (config.WorldEnv)
    Terrain       *TerrainLayout   // nil ⇒ no env terrain (flat); cells row-major, ids ⊆ terrain.yaml
    Objects       []ObjectPlacement
    Agents        []AgentPlacement
    Animals       []AnimalPlacement // empty ⇒ fauna OFF
    Flora         []FloraPlacement  // empty ⇒ flora OFF
    Lots          []LotPlacement
}
type Bounds struct{ Min, Max core.Vec2 }
type TerrainLayout struct{ Cols, Rows int; Cells []core.Tag } // len(Cells) == Cols*Rows (row-major)
type ObjectPlacement struct{ ID core.ObjectID; Kind core.Tag; Pos core.Vec2; Remaining int; Owner core.AgentID }
type AgentPlacement  struct{ ID core.AgentID;  Pos core.Vec2; Values map[core.Dimension]float64 }
type AnimalPlacement struct{ ID core.ObjectID; Species core.Tag; Pos core.Vec2; Heading float64 }
type FloraPlacement  struct{ ID core.ObjectID; Species core.Tag; Pos core.Vec2; Length, Width float64 }
type LotPlacement    struct{ ID core.ObjectID; Kind core.Tag; Qty int; DecayAge float64; Location string }

// ── Parse / Encode (the fixture wire; schema-validated) ──────────────────────────
// Parse decodes + schema-validates a fixture blob (content/schema/fixture.schema.json) into a Fixture.
// It REJECTS (typed error) a malformed blob, a schema_version mismatch, or a Cells length != Cols*Rows.
// No engine state is built here (pure parse). Encode is its inverse (Generate writes via Encode).
func Parse(blob []byte) (Fixture, error)
func Encode(fx Fixture) ([]byte, error)

// ── Generate (AUTHOR-TIME; seeded, deterministic — WG1-a) ────────────────────────
// Generate runs the water-centric procedural pipeline (docs/world-gen.md §1) on a seeded RNG and
// returns a Fixture: elevation(noise) → slope → flow-accumulation rivers/lakes/sea → moisture →
// base material (§6/threshold) → resource distribution (affinity, ore_node) → flora/fauna/agent
// seeding (suitability + Value/threat lever). PURE function of (cfg, seed): same seed ⇒ byte-
// identical Fixture (D12; runtime generation 0 — this is author-time only). The pipeline COEFFICIENTS
// (noise octaves, sea level, river threshold, erosion, ore density) are GenConfig data (world-gen.md §2).
func Generate(cfg GenConfig, seed int64) Fixture

// GenConfig carries the WG1-a generation coefficients (world-gen.md §2 — data, not new mechanism),
// injected (e.g. from a generation YAML); NOT hardcoded (D10). Bounds + grid resolution come from
// content/world.yaml (config) so a generated world's grids match the engine's.
type GenConfig struct{ /* opaque: noise/octaves, sea_level, river_accum_threshold, erosion,
                          ore_density, flora/fauna/agent seeding rates; + bounds/resolution from world.yaml. */ }

// ── Load (RUN-TIME; fixture → states → install) ──────────────────────────────────
// Load builds the env states from fx + the compiled Rules in reg (config WI-P0), and installs them
// into the (empty) world w: it builds navmap.New(reg.NavCfg, terrainAt(fx.Terrain), reg.TerrainTypes),
// climate.New(reg.ClimateCfg, terrainAt) [SAME terrainAt ⇒ grids agree at t=0], flora.New(fx.Flora→
// plants), decay.New(fx.Lots→lots), the fauna.Animal set (fx.Animals), then calls
// w.InstallEnv(reg.WorldEnv, nav, climate, reg.ClimateRules, flora, reg.FloraRules, decay,
// reg.DecayRules) and (iff Animals non-empty) w.InstallFauna(reg.WorldEnv, reg.FaunaRules, animals);
// finally w.Spawn(agent) for each AgentPlacement (RealStats SAMPLED from the GenSpec via rng) and
// w.PlaceObject for each ObjectPlacement (Remaining for ore_node). bounds = fx.Bounds ?? reg.WorldEnv.
// rng is the run's root (the fixture Seed seeds it). When a block is absent, that subsystem stays OFF
// (env-neutral — existing goldens hold). Returns a typed error on any cross-check failure.
func Load(fx Fixture, reg *config.Registries, w *world.World, rng *rng.RNG) error
```

> `terrainAt(fx.Terrain)` maps a continuous `Vec2` → the layout cell's terrain id (D11 index read; the
> grid is navmap-resolution over bounds). Absent `Terrain` ⇒ a constant default terrain (soil) ⇒ no
> climate transitions / no terrain-driven Mine (env-terrain-neutral).

## Determinism (D12)
- **Generate** is a pure function of `(cfg, seed)` on a seeded `*rng.RNG`; same seed ⇒ byte-identical
  `Fixture` across runs/processes (no wall-clock, no global rand). Runtime generation is **0** — the
  engine never calls `Generate`; only the author-time tool/cmd does.
- **Load** is deterministic: `Parse` is pure; state construction iterates placements in fixture order
  (sorted by id where order matters); agent/animal stat sampling draws from the injected seeded `rng`
  in fixed id order (mirrors `world.Spawn`). Same `(fixture, registries, seed)` ⇒ byte-identical
  installed world.
- **Resume parity:** a loaded world + its captured snapshot (`platform/persist`) resume byte-identically
  (the loader is the t=0 builder; persist is the t>0 round-trip).

## Acceptance Criteria (testable)
- [ ] **Parse round-trips + validates** — `Encode`→`Parse` reproduces a `Fixture`; a blob with a bad
  `schema_version`, an unknown terrain id, or `len(Cells) != Cols*Rows` is rejected with a typed error.
- [ ] **Generate is seeded-deterministic** — `Generate(cfg, seed)` twice ⇒ byte-identical `Fixture`; a
  different seed differs; a known seed produces a dendritic river (flow-accumulation) + sea below sea
  level + mountains (smoke test over the WG1-a stages, world-gen.md §1).
- [ ] **Load builds + installs env** — `Load` of a fixture with terrain+flora+animals builds navmap/
  climate (grids agree at t=0), installs env + fauna, spawns agents (RealStats sampled), places objects
  (ore_node `Remaining` set); a subsequent `world.Tick()` runs the env sub-phase.
- [ ] **Absent-block neutrality** — a fixture with no terrain/animals/flora loads an env-OFF world
  (InstallEnv/InstallFauna effectively neutral; no env render keys); a minimal `{seed}` fixture loads an
  empty world. Existing scenario behavior (agents+objects only) is unchanged.
- [ ] **Cross-checks** — `Load` rejects a flora/animal `species` absent from `reg.FloraRules`/
  `FaunaRules`, an object `kind` absent from the catalog, a terrain id absent from `reg.TerrainTypes`,
  or a `Terrain` grid whose `Cols`/`Rows` disagree with `reg.WorldEnv` bounds/`navmap_cell_size`.
- [ ] **Determinism golden** — `Generate(seed)`→`Encode`→`Parse`→`Load`→N ticks is byte-identical to a
  second run from the same seed; the generated fixture digest is stable across processes.

## Out of Scope
- **The WG1-a algorithm COEFFICIENTS** (noise octaves, sea level, river/erosion thresholds, ore/flora/
  fauna seeding rates) → `docs/world-gen.md §2` + a generation config (data, D10). This SPEC fixes the
  pipeline INTERFACE + determinism; the tuned numbers are author-time data.
- **The compiled `Rules` + Configs** (`navmap.Config`/`climate.Config`/`climate.Rules`/`flora.Rules`/
  `fauna.Rules`/`decay.Rules`/`TerrainTypes`/`WorldEnv`) → `platform/config` (WI-P0,
  `backend/platform/config/SPEC-world.md`). Load CONSUMES `*config.Registries`; it compiles nothing.
- **`InstallEnv`/`InstallFauna` semantics + the env sub-phase** → `engine/world`
  (`SPEC-world-env.md`/`SPEC-world-fauna.md`). Load only CALLS them with the built states.
- **The engine module constructors** (`navmap.New`/`climate.New`/`flora.New`/`decay.New`/`scent.New`/
  `world.Spawn`/`PlaceObject`/`InstallEnv`/`InstallFauna`) → each module's SPEC. Load wires them.
- **Snapshot serialization / resume** → `platform/persist` (`SPEC-world.md`, WI-P4 output). Load is the
  t=0 builder; persist is the t>0 round-trip.
- **The live-emergence Value/threat SEEDING policy** (what Values to seed so social emergence isn't
  underseeded) → `docs/world-gen.md §6` + memory `live-emergence-underseeded`. The fixture CARRIES
  `agents[].values`; choosing them is generation/scenario authoring.

## Open Questions
> `docs/world-integration.md` W1-W9 + `docs/world-gen.md` WG1-7 RESOLVED. Plumbing seams:
- **Animal base-stat source (fauna content seam) — ✅RESOLVED (a), 2026-06-28: per-species `GenSpec` sample (agent parity).** `fauna.Animal.Stats` is an open base-attribute
  vector; the fixture carries only species+pos. Options: **(a)** sample animal stats from a per-species
  `GenSpec` (like agents) with the seeded rng; **(b)** fixed per-species base stats in `content/
  objects.yaml fauna:`. **rec: (a)** — parity with agents, gives variation; the species `GenSpec` is a
  small `fauna:` content addition. **Flag to the fauna content owner** (it is a `fauna:` schema add,
  like the agent stat GenSpec). Non-blocking for the loader interface.
- **Loader code home / scenario unification.** Today scenario setup is in `main.go` (config SPEC Out of
  Scope). `worldgen.Load` is the unified path; the existing Go-test scenarios can migrate to fixtures or
  keep their Go setup. rec: `worldgen.Load` is the one runtime loader; main.go calls it; the Go-test
  scenarios stay as-is until a fixture migration phase. Non-blocking.

## Notes
- One `Fixture` format for generation AND scenarios (W9) is what makes "world-gen can come after the
  mechanisms" true (`docs/world-gen.md §0-6`): until the generator is built, hand-authored fixtures (or
  Go scenarios) drive runs; the generator just becomes another `Fixture` source later.
- `Generate` is **author-time** (a separate cmd writes the fixture file); `Load` is **run-time** (main
  reads it). The engine binary imports only `Load` (+ the `Fixture` type); the generator cmd uses
  `Generate`. Neither is imported by `engine/*` (architecture §7).
- The loader is the single place the env module constructors are wired from a layout — keeping the
  "positions/layout are the source of truth, the engine builds indices from them" rule (navmap/climate
  from `terrainAt`, scent/spatial rebuilt from positions, D11/D12).
- Reference paths: `docs/world-gen.md` (WG1-a pipeline + §2 coefficients), `docs/world-integration.md`
  (W1/W9, WI-P4), `content/schema/fixture.schema.json` (the format), `backend/platform/config/SPEC-world.md`
  (the Rules/Configs Load consumes), `backend/engine/world/SPEC-world-env.md` + `SPEC-world-fauna.md`
  (InstallEnv/InstallFauna), `backend/engine/space/navmap/SPEC.md` + `backend/engine/env/climate/SPEC.md`
  (the `New(terrainAt)` constructors), `docs/data-contracts.md §10` (the persist round-trip).
