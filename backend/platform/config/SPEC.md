# SPEC — `platform/config`

> Status: `DRAFT`
> Leaf level: `L8` (platform leaf — architecture §3, build-order stage 8)  ·  Owner agent: `<filled by implementer>`
> Sub-spec: [`SPEC-world.md`](SPEC-world.md) — **WI-P0** world/env loading (`world.yaml`/`climate.yaml`/`fauna:`/`flora:` §6 compile + cross-checks → `world.InstallEnv`/`InstallFauna` inputs).

## Purpose

The single **content-loading + environment-parsing** layer. It encapsulates the init logic
currently scattered in `backend/main.go` (`loadStats` / `loadNeeds` / `loadActions` / `loadGates`
/ `parseBalance` / `agentConfigFromBalance`) behind two entry points — `LoadContent(dir)` and
`ParseEnv()` — and adds the **JSON-Schema validation that main.go does not do today**. It opens
the `content/` files, structurally validates each against `content/schema/<file>.schema.json`
**before** the engine loader sees it, runs cross-file referential-integrity checks, then hands the
already-validated bytes to the engine `Load` functions (which do IO-free *semantic* validation).
The result is an immutable `Registries` bundle plus a deterministic `ConfigHash()` for
`RunRecord.ConfigHash` (data-contracts §3). It also parses all deployment env vars into a plain
`EnvConfig`.

## Public Interface

```go
package config

import (
    "github.com/dogring/bdg/engine/mind/actions"
    "github.com/dogring/bdg/engine/agent"
    "github.com/dogring/bdg/engine/mind/gates"
    "github.com/dogring/bdg/engine/mind/needs"
    "github.com/dogring/bdg/engine/mind/perception"
    "github.com/dogring/bdg/engine/mind/planner"
    "github.com/dogring/bdg/engine/mind/stats"
    "github.com/dogring/bdg/engine/mind/values"
    "github.com/dogring/bdg/engine/world"
    "github.com/dogring/bdg/engine/kernel/worldtime"
)

// ── Environment parsing ─────────────────────────────────────────────────────────

// EnvConfig holds all deployment knobs. Parsed once at startup by ParseEnv. It is a PLAIN
// struct with NO methods; every field is exported. It carries no content registries (those
// come from LoadContent) and performs no IO of its own past os.Getenv.
type EnvConfig struct {
    Seed             int64   // SEED;               default 1
    RunID            string  // RUN_ID;             default "dev"  (= RunRecord.run_id keyspace prefix)
    ContentDir       string  // CONTENT_DIR;        default "./content" (passed to LoadContent)
    TicksPerSecond   float64 // TICKS_PER_SECOND;   default 1.0
    BackupEveryTicks int     // BACKUP_EVERY_TICKS; default 1440 (= 1 game-day; data-contracts §3)
    RedisAddr        string  // REDIS_ADDR;         default "" (live store disabled)
    PostgresDSN      string  // POSTGRES_DSN;       default "" (backup disabled)
    HTTPAddr         string  // HTTP_ADDR;          default ":8080"
    LogLevel         string  // LOG_LEVEL;          default "info"
}

// ParseEnv reads every deployment env var, applies the documented default for any that are
// unset or empty, and returns a fully-populated EnvConfig. A malformed numeric env var
// (SEED, TICKS_PER_SECOND, BACKUP_EVERY_TICKS) returns a DESCRIPTIVE error naming the var and
// the bad value — never a silent zero. Reads no files, no clock, no rand.
func ParseEnv() (EnvConfig, error)

// ── Content loading ─────────────────────────────────────────────────────────────

// Registries is the IMMUTABLE bundle returned by LoadContent. The caller (main / platform)
// owns it; LoadContent never retains or mutates it after returning. Balance is exposed only
// through BalanceDoc's typed accessor methods — callers never touch raw YAML tags.
type Registries struct {
    Stats   *stats.Registry
    Gates   *gates.Registry
    Actions *actions.Registry
    Needs   *needs.Registry
    Balance BalanceDoc // opaque parsed balance.yaml; accessor methods build engine config structs
}

// ConfigHash returns the SHA-256 (hex) of the canonical content fingerprint: the raw bytes of
// the loaded YAML files concatenated in a FIXED lexicographic filename order, each prefixed by
// its filename. Deterministic across processes given identical file contents (no path, no
// mtime, no map iteration). Used as RunRecord.ConfigHash (data-contracts §3) so a run is
// reproducible from `seed + config_hash + last snapshot`.
func (r Registries) ConfigHash() string

// LoadContent loads, schema-validates, referential-integrity-checks, and builds every registry
// from the content directory `dir` (typically EnvConfig.ContentDir). Pipeline, in order:
//   1. read   dir/{stats,needs,actions,gates,balance}.yaml (the ONLY content files it touches).
//   2. schema each file is validated against dir/schema/<file>.schema.json BEFORE any engine
//             Load runs (the schema dir is content/schema/, a child of dir; see Notes).
//   3. semantic+build  the validated bytes are passed to stats.Load → gates.Load(_, statsReg)
//             → actions.Load → needs.Load(needsBytes, balanceBytes); each does its IO-free
//             semantic check.
//   4. referential cross-file integrity (unknown GateID/ActionID/StatID/NeedID — see ACs).
//   5. parse  balance.yaml into the opaque BalanceDoc.
// On ANY schema, semantic, or referential error it returns (nil, err) with a TYPED, descriptive
// error and builds NO partial registry. It performs no IO beyond reading the listed files
// (no network, no wall-clock, no rand) — pure files-in → registries-out.
func LoadContent(dir string) (*Registries, error)

// ── BalanceDoc: typed accessors over the parsed balance.yaml ──────────────────────

// BalanceDoc is the parsed balance.yaml (mirrors main.go's private balanceDoc, lines 473–568).
// The struct fields are UNEXPORTED; callers read it ONLY through the accessor methods below,
// each of which assembles the corresponding engine config struct. This is the same translation
// main.go does today, moved behind one type so no caller ever sees a `yaml:"..."` tag.
type BalanceDoc struct{ /* unexported: world/mood/adrenaline/stamina/urgency/planner/coping/
                           tag_levels/trade/social/threats/politics/generation/gossip/… blocks */ }

// WorldConfig builds world.Config from the balance world: + politics: blocks (SpatialHashCell,
// RoleConvergenceThreshold, OutcomeDifficultyBase, BackupEveryTicks, MoveSpeedPerTick).
func (b BalanceDoc) WorldConfig() world.Config

// PlannerConfig builds planner.PlannerConfig from the balance planner: block (Budget,
// BaseHorizonTicks, UrgencyThreshold, TagCosts — core.Tag-keyed).
func (b BalanceDoc) PlannerConfig() planner.PlannerConfig

// AgentConfig builds agent.Config from the mood/adrenaline/stamina/urgency/self_calibration/
// gossip/resentment/planning/generation/coping/tag_levels/trade/social/threats/politics blocks.
// It needs needsReg to resolve SafetyDim (the single Conditional PreventBelow dimension) and the
// EffortLevels / ThreatTags / tom.Rates sub-structs, exactly as main.go's agentConfigFromBalance.
func (b BalanceDoc) AgentConfig(needsReg *needs.Registry) agent.Config

// ValuesConfig returns the values arbitration config parsed from the balance values: block.
// (Engine's values.Load returns *values.Config; this accessor returns the form callers consume —
// see Open Questions on pointer-vs-value.)
func (b BalanceDoc) ValuesConfig() values.Config

// PerceptConfig returns the perception.PerceptionConfig parsed from the balance perception: block
// (mirrors perception.LoadConfig).
func (b BalanceDoc) PerceptConfig() perception.PerceptionConfig

// ClockConfig builds worldtime.Config from the balance world: block (TickMinutes, DayMinutes,
// DaysPerSeason, SeasonsPerYear). Callers pass it to worldtime.NewClock.
func (b BalanceDoc) ClockConfig() worldtime.Config
```

> The accessor return types are owned by the engine modules listed in the import block. `platform/config`
> is allowed to import platform siblings and the **full engine layer** (architecture §1: platform may
> depend on engine; the engine never imports platform).

## Dependencies

- `engine/mind/stats` — `stats.Load(io.Reader)`, `*stats.Registry` (built first; passed into gates.Load).
- `engine/mind/gates` — `gates.Load(io.Reader, *stats.Registry)`, `*gates.Registry`.
- `engine/mind/actions` — `actions.Load(io.Reader)`, `*actions.Registry`.
- `engine/mind/needs` — `needs.Load(needsDoc, balanceDoc io.Reader)`, `*needs.Registry` (merges needs.yaml + balance needs:).
- `engine/world` — `world.Config` type only (WorldConfig accessor return).
- `engine/mind/planner` — `planner.PlannerConfig`, `planner.Budget`, `core.Tag`-keyed `TagCosts` (PlannerConfig accessor).
- `engine/agent` — `agent.Config` type only (AgentConfig accessor return).
- `engine/mind/values` — `values.Config` type (ValuesConfig accessor return).
- `engine/mind/perception` — `perception.PerceptionConfig` type (PerceptConfig accessor return).
- `engine/kernel/worldtime` — `worldtime.Config` type (ClockConfig accessor return).
- `engine/kernel/core` / `engine/mind/tom` — `core.Tag` / `core.Dimension` / `tom.Rates` used when assembling the accessor structs.
- **Contracts**: `content/schema/{stats,needs,actions,gates,balance,objects}.schema.json` (structural shapes);
  `docs/data-contracts.md` §3 (`config_hash`, `backup_every_ticks`, `run_id`).
- Standard library only: `os`, `crypto/sha256`, `encoding/json`, `io/fs`, `path/filepath`, `sort`, plus the
  YAML decoder already vendored (`gopkg.in/yaml.v3`). **No third-party JSON-schema library needed for P1**
  (lightweight struct-unmarshal + required-field check; a full validator can replace it without changing
  the Public Interface).

## Owned Data

- `EnvConfig` (plain value), `Registries` (immutable bundle), `BalanceDoc` (opaque parsed balance).
- The schema-validation step and the `ConfigHash` fingerprint logic. `platform/config` **owns the file IO**
  for `content/`; the engine `*Registry` values it returns are owned by the **caller** after `LoadContent`
  returns — this module retains no reference and mutates nothing post-return.

## Invariants

- **`LoadContent` never writes state**; it produces a fresh `*Registries` the caller owns. No global state,
  no package-level mutable vars.
- **`ConfigHash` is deterministic across processes** given identical file contents — fixed lexicographic
  filename order, raw bytes only, no mtime/path/absolute-dir in the digest, no `map` iteration (D12).
- **No wall-clock, no global rand, no network** anywhere in this package. The only IO is reading the listed
  `content/` + `content/schema/` files; testable against an in-memory `io/fs` (D12 determinism guard).
- **Schema-validate BEFORE engine `Load`**: a structural schema violation returns a typed error and **no
  registry is built** — the engine semantic loaders are never reached for a malformed file.
- **Referential integrity after schema**: cross-file references (StatIDs in gates/actions, GateIDs,
  ActionIDs, NeedIDs in effects) are checked; the first violation aborts with a descriptive error.
- **No naming drift**: all identifiers (`StatID`, `Dimension`/`NeedID`, `Tag`, gate/action ids) use the
  glossary canonical names; this package introduces no new vocabulary.
- **BalanceDoc fields stay unexported**: callers reach balance data only through the accessor methods —
  a sibling never sees a `yaml:"..."` tag (D5: config translation is centralized here).

## Acceptance Criteria (testable)

### Content loading
- [ ] `LoadContent(dir)` reads `stats.yaml`, `actions.yaml`, `gates.yaml`, `balance.yaml`, and `needs.yaml`
  from `dir/` and returns a populated `*Registries` for valid content (golden against the shipped `content/`).
- [ ] Each YAML is validated against `content/schema/<file>.schema.json` **before** being passed to the
  engine loader; a **schema violation returns a typed error and no registry is built** (assert `*Registries`
  is nil and no engine `Load` ran — e.g. a `stats.yaml` with a malformed `range` is rejected at the schema
  step, table-driven per file).
- [ ] **Unknown `StatID` referenced in `gates.yaml` → error** (gates.Load already enforces this via its
  `*stats.Registry` arg; this AC documents that `LoadContent` surfaces that error from the gates step).
- [ ] **Unknown `GateID` or `ActionID` in any YAML → error** (semantic/referential check after the schema
  pass; e.g. an action referenced that no `actions.yaml` entry defines, or a gate id referenced out of band).
- [ ] Returns a `Registries` bundle containing `*stats.Registry`, `*gates.Registry`, `*actions.Registry`,
  `*needs.Registry`, plus a `BalanceDoc` accessed only through typed methods (callers never touch raw YAML).
- [ ] **`ConfigHash() string`** is the SHA-256 of the concatenated sorted YAML bytes; equal for two
  `LoadContent` calls on byte-identical content and different when any one file changes by one byte
  (used as `RunRecord.ConfigHash`, data-contracts §3).

### Environment parsing
- [ ] `ParseEnv()` reads all env vars with the documented defaults: `SEED`=1, `RUN_ID`="dev",
  `CONTENT_DIR`="./content", `TICKS_PER_SECOND`=1.0, `BACKUP_EVERY_TICKS`=1440, `REDIS_ADDR`="",
  `POSTGRES_DSN`="", `HTTP_ADDR`=":8080", `LOG_LEVEL`="info" (table-driven: unset → default; set → parsed).
- [ ] **Malformed numeric env vars return a descriptive error** naming the var and value (`SEED=abc`,
  `TICKS_PER_SECOND=x`, `BACKUP_EVERY_TICKS=1.5`) — **no silent zero** (table-driven).
- [ ] `EnvConfig` is a plain struct with **no methods**; all fields exported (compile/grep guard).

### BalanceDoc accessors
- [ ] Each accessor (`WorldConfig`, `PlannerConfig`, `AgentConfig`, `ValuesConfig`, `PerceptConfig`,
  `ClockConfig`) reproduces the exact struct main.go assembles today from the same `balance.yaml`
  (golden/equality test against the shipped `content/balance.yaml`).
- [ ] `AgentConfig(needsReg)` resolves `SafetyDim` to the single Conditional `PreventBelow` dimension and
  populates `EffortLevels`, `ThreatTags`, and `tom.Rates` exactly as `agentConfigFromBalance` (D6/D7/D9
  config values are passed through, not invented).

### Determinism guard
- [ ] `LoadContent` performs **no IO beyond reading the listed files** (no network, no `time.Now()`, no
  `rand`); a test driving it through an in-memory `io/fs` (or a temp dir) produces a registry and a stable
  `ConfigHash`, and a second identical call yields the byte-identical hash (D12).

## Out of Scope

- Redis / Postgres clients and snapshot serialization → `platform/persist` (`backend/platform/persist/SPEC.md`).
- HTTP / SSE endpoint → `platform/api`.
- Scenario YAML loading (`-scenario`, `loadScenario`/`spawnScenario`/`placeObjects`) → a one-off dev-tool
  concern that stays in `main.go` (it is run composition, not content; D12 authored-order spawn).
- The why-trace / event stream → `platform/events`.
- Runtime config **hot-reload** — parse-once at startup only; no watcher.
- The engine `Load` functions' **semantic** validation (range bounds, duplicate ids, predicate-tree shape)
  — owned by each engine module; `platform/config` only adds the **structural** schema pass + cross-file
  referential checks that the engine cannot do (it has no file/schema access).

## Open Questions

- **`ValuesConfig` pointer-vs-value (NOT blocking P1).** `engine/mind/values.Load` returns `*values.Config`,
  but the brief's accessor signature is `func (b BalanceDoc) ValuesConfig() values.Config`. The implementer
  should return whichever matches what callers (`agent.Services.Values`) actually consume — if that field
  holds a `*values.Config`, change this accessor to return the pointer. Confirm the caller's field type
  before fixing the signature (Public Interface may need a one-line adjustment).
- **`objects.yaml` (NOT blocking P1).** architecture §3 lists the object/item catalog under `config`, and
  `content/schema/objects.schema.json` exists, but main.go does not yet load objects through this path
  (placement is world-gen/scenario). P1 ships the five files in the ACs; `objects.yaml` loading is a follow-up
  once `engine` exposes an object/item registry `Load`. Flagged so the reviewer does not require it for P1.
- **Schema engine (NOT blocking P1).** The P1 lightweight validator (struct-unmarshal + required-field check)
  must still honour each schema's `schema_version.const` and `oneOf` shapes (notably `gates.yaml`
  schema_version 3, with its body-scalar leaf + `cost_rule`). If a future schema's `oneOf`/conditional
  complexity outgrows the lightweight approach, swap in a full JSON-schema validator — the Public Interface
  does not change.

## Notes

- The `balanceDoc` struct in `main.go` lines 473–568 is the private implementation to lift verbatim into the
  unexported `BalanceDoc` body; the YAML tags move with it. Only the **accessor methods** are public — this
  is the one place balance YAML tags exist after the refactor. `agentConfigFromBalance` (lines 596–689),
  `tagCostMap`, and `effortLevelMap` become the bodies of `AgentConfig` / `PlannerConfig`.
- **Schema validation (P1 lightweight path):** decode each file into its schema-mirroring struct with the
  decoder's unknown-field rejection enabled and check required fields, OR run a minimal `oneOf`/required check
  driven by the JSON schema. Either satisfies the "validated before engine Load" ACs. **Important:**
  `balance.yaml` has many top-level blocks (`needs`, `values`, `perception`, `gates`, `planner`, …) consumed
  by different engine modules — the balance validator must permit all of them, mirroring main.go's
  deliberately-non-strict `parseBalance` (it does NOT use KnownFields, so unrelated top-level keys are
  allowed). Do not regress that by over-strict whole-document rejection.
- **`schema_version` cross-check:** `gates.yaml` is `schema_version: 3` (engine/mind/gates SPEC, data-contracts §0).
  `LoadContent` must refuse a file whose `schema_version` does not equal the schema's `const`.
- **Engine import path prefix is `github.com/dogring/bdg/engine/...`** (module `github.com/dogring/bdg`,
  go.mod), as used in `main.go` — not `.../backend/engine/...`.
- **Schema directory resolution:** schemas live at `content/schema/` (a child of the content dir, per the
  shipped layout). Resolve each schema at `filepath.Join(dir, "schema", "<file>.schema.json")` relative to
  `dir`, never the binary's CWD.
- `ConfigHash` feeds `RunRecord.ConfigHash`; `BackupEveryTicks` feeds the backup interval (data-contracts §3:
  "Reproduce from seed + config_hash + last snapshot"). Keep the hash a pure function of file bytes so a
  resumed run validates against the persisted record.
