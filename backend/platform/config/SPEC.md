# SPEC — `platform/config`

> Status: `DRAFT`
> Leaf level: `(platform, stage 8)`  ·  Owner agent: `<filled by implementer>`

## Purpose

The single **IO boundary** between the `content/` data layer and the engine registries.
`platform/config` reads each `content/*.yaml`, validates it **structurally** against the
matching `content/schema/*.json` (JSON Schema 2020-12), runs cross-file **referential
integrity** checks, then hands the parsed bytes/readers to the engine's pure registry
constructors (`engine/stats.Load`, and later `needs`/`actions`/`gates`). It also computes
`config_hash` for replay. The engine stays IO-free (architecture §1); this module owns all
filesystem access for content.

## Public Interface

```go
package config

import (
    "github.com/dogring/bdg/backend/engine/stats"
    // (later) "…/engine/needs", "…/engine/actions", "…/engine/gates"
)

// Paths locates the content files and their schemas. Injected — no path is hardcoded
// (D10). Defaults point at the repo's content/ + content/schema/ for tests.
type Paths struct {
    Stats       string // content/stats.yaml
    StatsSchema string // content/schema/stats.schema.json
    // Needs, NeedsSchema, Actions, ActionsSchema, Gates, GatesSchema, … added per stage.
}

// Registries is the immutable bundle the engine consumes after a successful Load.
type Registries struct {
    Stats *stats.Registry
    // Needs *needs.Registry; Actions *actions.Catalog; Gates *gates.Registry … added per stage.

    ConfigHash string // deterministic hash over all loaded+validated content (data-contracts §3)
}

// Load reads, schema-validates, and cross-checks every content file under p, builds the
// engine registries, and returns them. It is the ONLY place content files are opened.
// Returns a descriptive error on the first failure (which file, which schema rule or which
// dangling reference). Pure-functional w.r.t. its inputs: same bytes → same Registries +
// same ConfigHash.
func Load(p Paths) (*Registries, error)

// ValidateStats reads statsPath and validates it against schemaPath WITHOUT building the
// registry — exposed so a CI/lint step (or a focused test) can check content alone.
// Returns nil iff the file is structurally valid against the schema.
func ValidateStats(statsPath, schemaPath string) error
```

> The engine constructors (e.g. `stats.Load(io.Reader)`) do **semantic** validation
> (bounds, duplicates, unknown kinds). `platform/config` does **structural** JSON-schema
> validation + cross-file referential integrity, then calls them. The split keeps the engine
> free of schema/IO machinery (see `backend/engine/stats/SPEC.md`).

## Dependencies

- `engine/core` — id types (referential checks).
- `engine/stats` — `stats.Load`, `stats.Registry` (this stage). Later: `needs`, `actions`, `gates`.
- A JSON-Schema validator library (e.g. `santhosh-tekuri/jsonschema`) — platform-only dependency.
- A YAML decoder (to convert YAML → the generic doc the schema validator checks, and to feed the
  engine loaders). All filesystem access lives here.

## Owned Data

- `Paths`, `Registries` value types. The returned `*stats.Registry` (and future registries) are
  **immutable** and owned downstream by `engine/world`; `platform/config` retains no mutable
  global state. `ConfigHash` is computed once and frozen.

## Invariants

- **Structural validation before construction**: every content file is validated against its
  `content/schema/*.json` **before** its bytes reach an engine loader. A schema failure aborts
  `Load` with an error and builds no registries.
- **Referential integrity** (content/README §"Load & validate flow" step 3): every `StatID`
  referenced by `gates.yaml`/`actions.yaml` exists in `stats.yaml`; analogous checks for
  needs/objects/tag-levels as those stages land. A dangling reference is a `Load` error.
- **Determinism (D12)**: `Load` does not iterate maps for logic where order is observable
  (e.g. building `ConfigHash` sorts keys); same content bytes → identical `Registries` and
  identical `ConfigHash` across runs and machines (data-contracts §3, replay).
- **IO is confined here**: no other engine module touches the filesystem for content; the engine
  receives readers/parsed data only.
- **Fail closed**: any validation error (schema, semantic via the engine loader, or referential)
  prevents a partial/half-built `Registries` from being returned.

## Acceptance Criteria (testable)

- [ ] **Structural validation of `content/stats.yaml`**: `ValidateStats("content/stats.yaml",
  "content/schema/stats.schema.json")` returns `nil` — the shipped content passes its schema.
  (This is the AC moved out of `engine/stats`; it requires file IO + the schema, owned here.)
- [ ] **Validation runs in `Load` before handing data to `engine/stats`**: `Load` with the real
  `Paths` returns a `Registries` whose `Stats` contains the expected ids (incl. the D7
  capabilities `Strength`, `Agility`, `Intelligence`); and a fixture `stats.yaml` that violates
  `stats.schema.json` (missing `range`, bad `kind` enum, extra property) makes `Load` fail **at
  the schema step**, before `stats.Load` runs (negative test).
- [ ] **Referential integrity**: a fixture referencing an undefined `StatID` from `gates.yaml`
  (when that stage exists) makes `Load` fail with a dangling-reference error. (Stub/skip until
  `gates` content is wired; track here so it is not lost.)
- [ ] **`ConfigHash` determinism**: two `Load`s over identical content produce the identical
  `ConfigHash`; changing any content byte changes it (data-contracts §3).
- [ ] **Fail-closed**: on any validation error, `Load` returns `(nil, err)` and no usable
  registry.

## Out of Scope

- Building the stat `Registry` itself, semantic stat validation (bounds/duplicates/kinds) →
  `engine/stats.Load` (`backend/engine/stats/SPEC.md`).
- Need / action / gate registries and their schemas → their engine modules + `content/*.yaml`
  (wired into `Paths`/`Registries` as those stages land).
- Redis / Postgres / snapshot serialization → `platform/persist` (data-contracts §1–§3).
- Event stream / SSE → `platform/events`.

## Open Questions

- **Schema validator dependency choice** (Go JSON-Schema library) is an implementation detail;
  any 2020-12-compliant validator is acceptable. Does not block — pick at implement time.

## Notes

- YAML → JSON-Schema: decode YAML to a generic `any` tree, then validate with the schema
  (validators operate on the decoded document, not raw bytes).
- Keep `ValidateStats` (and future per-file validators) public so `docs/testing.md` §2
  ("Registries: load `content/` → pass schema → assert expected entries") has a direct hook and
  CI can validate content without constructing the whole engine.
- `config_hash` participates in replay reproducibility (data-contracts §3: "Reproduce from
  `seed + config_hash + last snapshot`") — compute it over the canonical (sorted) content.
