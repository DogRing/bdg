# SPEC — `engine/stats`

> Status: `DRAFT`
> Leaf level: `L1`  ·  Owner agent: `<filled by implementer>`

## Purpose

Owns the **open stat vector** (`Stats = map[StatID]float64`) and the immutable
**`Registry`** of stat definitions built from `content/stats.yaml` (D10: stats are data,
not code). The registry is the single authority on *which* `StatID`s exist, their `[min,max]`
range, default value, generation distribution, and `capability`/`disposition` kind. Every other
engine module (`gates`, `needs`, `tom`, `values`, `actions`, `agent`) reads stat metadata
through this registry — there are **no hardcoded stat names anywhere in engine code** (D7:
competence is a composition of these base attributes, never a per-skill field).

## Public Interface

```go
package stats

import (
    "io"
    "github.com/dogring/bdg/backend/engine/core"
)

// ── The stat vector ──────────────────────────────────────────────────────────

// Stats is the open per-agent stat vector (glossary: "Open stat vector").
// It is NOT a fixed struct — adding a stat is a content edit, not a code edit (D7/D10).
// The same shape backs Real Stats and every ToM[X] belief (data-contracts §1 real_stats).
//
// Stats is a map for storage/lookup ONLY. Per D12, code MUST NOT drive logic by ranging
// over it; iterate via Registry.IDs() (canonical sorted order) instead.
type Stats map[core.StatID]float64

// Clone returns a deep copy (maps are reference types; the apply phase must not alias).
func (s Stats) Clone() Stats

// Get returns the value for id (0 if absent — callers should pre-fill via Registry.Defaults).
func (s Stats) Get(id core.StatID) float64

// ── Stat definitions ──────────────────────────────────────────────────────────

// Kind classifies a stat (glossary §"Capability vs disposition").
type Kind uint8

const (
    Capability  Kind = iota // Strength, Agility, Intelligence — gates/outcomes/prediction read these
    Disposition             // value-weighting axes (Aggression, Honesty, Greed, …)
)

// Def is one immutable stat definition (mirrors a content/stats.yaml entry, after
// platform/config has validated that file against content/schema/stats.schema.json).
type Def struct {
    ID      core.StatID // canonical identifier (docs/glossary.md §StatID)
    Label   string      // human-readable name; UI-only, IGNORED by engine logic (see Notes).
    Kind    Kind        // capability | disposition
    Min     float64     // inclusive lower bound (range[0])
    Max     float64     // inclusive upper bound (range[1])
    Default float64     // fallback value when Gen is absent; Min ≤ Default ≤ Max (see Notes)
    Gen     GenSpec     // agent-generation distribution; takes precedence over Default (see Notes)
    Inherit float64     // parent-inheritance weight in [0,1]
}

// GenSpec is the generation distribution for a stat (content/stats.yaml gen.*).
type GenSpec struct {
    Dist string  // "normal" | "uniform"
    Mean float64
    SD   float64 // ≥ 0
}

// Clamp returns v constrained to [Min, Max].
func (d Def) Clamp(v float64) float64

// ── The registry ──────────────────────────────────────────────────────────────

// Registry is the immutable, read-only set of stat definitions. After Load it never
// changes (no setters, no exported mutable fields). Shared freely across goroutines
// in the read/plan phase.
type Registry struct{ /* opaque: defs map[StatID]Def + a precomputed sorted []StatID */ }

// Load parses the stats document from r (the bytes of content/stats.yaml — the path is
// injected by platform/config, NEVER a file path in engine/stats, keeping the engine
// IO-free) and builds an immutable Registry. It performs SEMANTIC validation (see
// Invariants) and returns an error describing the first violation. STRUCTURAL JSON-schema
// validation (content/schema/stats.schema.json) is NOT done here — it is platform/config's
// responsibility, run before this call (see that SPEC).
func Load(r io.Reader) (*Registry, error)

// IDs returns ALL stat ids in the canonical, fixed order: sorted lexicographically by
// StatID. This is the ONE ordering every consumer must use to iterate stats (D12).
// The returned slice is a copy; callers may retain it. Identical across calls and across
// processes for the same content/stats.yaml.
func (reg *Registry) IDs() []core.StatID

// Def returns the definition for id and whether it exists.
func (reg *Registry) Def(id core.StatID) (Def, bool)

// Has reports whether id is a known stat. Used to reject unknown StatIDs referenced
// elsewhere (the planner / gates / config referential-integrity check).
func (reg *Registry) Has(id core.StatID) bool

// Len returns the number of defined stats.
func (reg *Registry) Len() int

// Kinds returns the lexicographically-sorted ids of all stats of the given Kind (e.g. the
// three Capability stats), so consumers compose competence without naming stats in code (D7).
func (reg *Registry) Kinds(k Kind) []core.StatID

// Defaults returns a fresh Stats pre-filled with every stat's Default value.
func (reg *Registry) Defaults() Stats

// Clamp returns s with every value constrained to its Def range; ids absent from the
// registry are dropped (rejects unknown stats sneaking into a vector). Iterates s via
// IDs() order, not map order (D12).
func (reg *Registry) Clamp(s Stats) Stats
```

> `Load` takes an `io.Reader`, not a path — the engine performs **no filesystem IO** (architecture
> §1). `platform/config` opens `content/stats.yaml`, runs the JSON-schema validation, and passes
> the reader/bytes here.

## Dependencies

- `engine/core` — `StatID`. (Plus a YAML decoder, e.g. `gopkg.in/yaml.v3`, and stdlib `sort`.)
- **Contract**: `content/schema/stats.schema.json` defines the on-disk shape; `content/stats.yaml`
  is the data. `platform/config` (architecture §3) bridges file → schema-validate → `Load`.

## Owned Data

- `Registry` and `Def`/`GenSpec` value types. The `Registry` is **immutable after `Load`** and
  owns its internal map + precomputed sorted id slice — no other module mutates it. `Stats` maps
  are owned by whoever holds the agent state (`engine/agent` / `engine/world`); this module only
  provides the shape, `Defaults`, and `Clamp` helpers.

## Invariants

- **Canonical ordering (D12)**: the `Registry` precomputes and exposes a single fixed-order
  `[]core.StatID` (sorted **lexicographically** by `StatID`) via `IDs()`. **All stat iteration —
  here and in every consuming module — MUST use this slice; ranging over a `Stats` map or the
  registry's backing map for logic is forbidden.** Because `stats` is read by the whole engine, a
  map-ordering bug here would corrupt every golden snapshot at once; this invariant prevents that.
  `Kinds()`, `Defaults()`, and `Clamp()` all iterate in `IDs()` order.
- **Immutable after init**: `Registry` exposes no setter and no writable field; once `Load`
  returns, its contents never change for the run's lifetime. Returned slices/maps are copies.
- **Unknown stats rejected at load time**: `Load` fails if an entry's `kind` is not
  `capability|disposition`, if `id` violates the glossary identifier pattern, or if an `id`
  duplicates an earlier one. After `Load`, an unregistered `StatID` is rejected by `Has()==false`
  and dropped by `Clamp()`.
- **Bounds well-formed (semantic range check)**: every `Def` has `Min ≤ Max` and
  `Min ≤ Default ≤ Max`; `Gen.SD ≥ 0`; `Inherit ∈ [0,1]`. `Load` rejects violations.
- **No hardcoded stat names (D7/D10)**: this package never references `"Strength"` etc. as
  literals in logic; all stats come from the loaded data. (`Capability`/`Disposition` are the
  only fixed classification; the *members* are data.)
- **No IO**: `engine/stats` imports no `os`/`net`/filesystem package; it reads from an injected
  `io.Reader` only (architecture §1, engine is IO-free). It does not perform JSON-schema
  validation (that requires the schema file and lives in `platform/config`).

## Acceptance Criteria (testable)

- [ ] **Loads from an injected `io.Reader`**: `Load` builds a `Registry` from in-memory YAML
  bytes (no file path in the call); the registry contains exactly the expected ids — including
  the three D7 capabilities `Strength`, `Agility`, `Intelligence` plus the disposition axes.
- [ ] **Unknown / unregistered StatID is rejected**: (a) `Load` errors on a `kind` outside
  `{capability, disposition}`, a bad-pattern `id`, and a duplicate `id` (table-driven);
  (b) after a successful `Load`, `Has("NotAStat") == false` and `Clamp` drops an unregistered id.
- [ ] **`Min ≤ Default ≤ Max` is enforced (semantic range check)**: `Load` errors on
  `Min > Max` and on `Default` outside `[Min,Max]`; accepts a valid in-range `Default`
  (table-driven).
- [ ] **`IDs()` ordering is deterministic (D12)**: `IDs()` returns the same lexicographically
  sorted order across repeated calls within a process AND yields the identical order in a second
  freshly-`Load`-ed registry from the same bytes (cross-process stability). For the shipped
  content, `Kinds(Capability)` == `[Agility, Intelligence, Strength]`.
- [ ] `Defaults()` returns one entry per defined stat, each equal to its `Default` and within
  `[Min,Max]`.
- [ ] `gen` precedence: `Def` faithfully carries both `Gen` and `Default` from the source entry
  (unit-level). When `gen` is absent, `Default` is the documented fallback; the
  sampling-vs-default behaviour itself is tested where sampling lives (`engine/agent`/`world`).
- [ ] **Immutable after initialization**: the public API exposes no mutator; mutating a returned
  `Stats` (from `Defaults`) or a returned id slice does not change the registry (subsequent reads
  identical).
- [ ] `Clamp` constrains out-of-range values and drops ids absent from the registry.
- [ ] `Stats.Clone()` is a deep copy (mutating the clone leaves the original unchanged).
- [ ] No literal stat name (`"Strength"`, …) appears in `engine/stats` source (grep guard, D7).

> Structural JSON-schema validation of `content/stats.yaml` against
> `content/schema/stats.schema.json` is **not** an AC here — it lives in
> `backend/platform/config/SPEC.md` (it needs file IO + the schema, which this module does not
> own). Keeping it out prevents the reviewer from flagging an AC `engine/stats` cannot prove.

## Out of Scope

- Reading the file from disk and JSON-schema validation → `platform/config`
  (architecture §3; this module receives an `io.Reader` + already-passed schema).
- Sampling actual agent stat values at spawn (uses `GenSpec` + injected `engine/rng`) →
  `engine/agent` / `engine/world` generation.
- `ToM[self]` / `ToM[X]` belief vectors and self-calibration (β) → `engine/tom` (they reuse the
  `Stats` shape but live there).
- Need/value dimensions (a separate registry) → `engine/needs` + `content/needs.yaml`.
- Gate evaluation reading stats → `engine/gates` + `content/gates.yaml`.

## Open Questions

None blocking. (The schema-validation responsibility split is now resolved: structural
validation in `platform/config`, semantic validation in `engine/stats.Load`.)

## Notes

- **`default` vs `gen` precedence** (mirrors `content/schema/stats.schema.json`): `gen` is the
  agent-**generation** distribution sampled at spawn with the injected RNG (D12) and is the
  normal source of a fresh agent's value. `default` is the **fallback** used when `gen` is absent
  and is the value `Registry.Defaults()` fills in (e.g. zero-config / reset / a missing key). When
  both are present, **`gen` takes precedence for initial assignment**; `default` is never used for
  sampling. If `default` is omitted in content, the loader uses the range midpoint `(Min+Max)/2`.
- **`label`**: UI/why-trace display string only. **No engine or platform logic reads it today**;
  it is carried through for forward-compat and ignored by the engine. If omitted, it falls back to
  `ID`. Reviewers should treat it as optional, non-load-bearing metadata.
- The on-disk shape (`content/stats.yaml`) uses `range: [min, max]`, `kind`, `gen`, `inherit`,
  and optional `label`/`default`. The loader maps `range[0]→Min`, `range[1]→Max`.
- Capability vs disposition (glossary): only the three **capabilities** are read by capability
  gates / outcome resolution / prediction; dispositions are value weights. Consumers pick members
  via `Kinds(k)` — never by hardcoded name.
- `Stats` is reused (by shape) for `real_stats` in the snapshot (data-contracts §1) and for every
  `ToM[X]` belief; serialization sorts keys for byte-determinism (data-contracts §0) — the same
  `IDs()` ordering this module guarantees.
