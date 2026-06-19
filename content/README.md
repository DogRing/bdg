# content/ — data-driven simulation content (D10)

Everything here is **data, not code**. `platform/config` loads these files at startup,
validates each against `content/schema/*.json`, and populates the engine registries
(`stats`, `needs`, `actions`, `gates`, plus the object/item catalog). Adding or changing a
stat / need / action / gate / object is a **data diff + a passing schema** — the engine is
content-agnostic and never changes (invariant **D10**, `docs/design.md` §2).

> Authoritative vocabulary: `docs/glossary.md`. Invariants D1–D12: `CLAUDE.md`.
> Cross-module shapes: `docs/data-contracts.md`. This README explains only the content layer.

## Files

| File | Registry | Key invariants |
|------|----------|----------------|
| `stats.yaml` | stat registry | D7 (no skills — competence = stat composition) |
| `needs.yaml` | need/value dimensions + rates | D9 (rate only; demand is derived) |
| `objects.yaml` | object_kinds + item_kinds | D9 (objects carry only their **supply** Effect) |
| `actions.yaml` | atomic action catalog | D3 (atomic only; no trees), D4 (cost/gates from tags) |
| `gates.yaml` | gate registry | D4 (tag-matched), D8 (decisions read `ToM[self]`) |
| `balance.yaml` | global scalars | tuning target (untuned defaults; `docs/testing.md` §5) |
| `schema/` | JSON Schema (2020-12) | the loader rejects any file that fails its schema |

Every file carries `schema_version` (`docs/data-contracts.md` §0). Bump it **and** the matching
schema's `const` together on any incompatible change.

## Load & validate flow (platform/config)

1. Parse YAML → generic map.
2. Validate against `content/schema/<file>.schema.json` (structural: shapes, enums, bounds).
3. **Referential integrity** (the loader, not JSON Schema): every `StatID` used in `gates.yaml`
   exists in `stats.yaml`; every need id in `objects.yaml`/`actions.yaml` exists in `needs.yaml`;
   every `target_kind`/item id in `actions.yaml` exists in `objects.yaml`; every `tag_level`
   family referenced by `gates.yaml` exists in `balance.yaml.tag_levels`.
4. Build registries; compute a `config_hash` (`docs/data-contracts.md` §3) for replay.

Schemas use an **identifier pattern**, not a hardcoded stat enum, so adding a stat needs no
schema edit — step 3 catches typos by cross-checking against `stats.yaml`.

## Tag grammar (D4)

Actions are annotated with tags; **cost and gates derive entirely from tags**, never from
per-action code. Form: `family:value` or a bare `family`.

| Family | Values | Read by |
|--------|--------|---------|
| `uses:` | `Strength` `Agility` `Intelligence` | capability gate; outcome resolution |
| `effort:` | `none` `low` `med` `high` | effort cost-term; stamina + apathy gates |
| `risk:` | `low` `med` `high` | risk cost-term; caution gate (× RiskAversion) |
| `violent:` | `low` `med` `high` | moral cost-term magnitude |
| `noise:` | `low` `med` `high` | perception/detection (hearing radius) |
| `abstraction:` | `low` `med` `high` | knowledge gate (× Intelligence) |
| `norm:transgressive` | (flag) | conscience gate (visibility + moral cost) |
| `social`, `social:covert`, `cooperative` | (flags) | social cost-term; sociability gate |
| `time:by_distance` | (flag) | time cost-term scales with travel distance |

Numeric magnitudes for each level live in `balance.yaml.tag_levels`; cost-term weights in
`balance.yaml.cost_terms`. **Violence ≠ transgression**: hunting is `violent:low` but not
`norm:transgressive`, so the conscience gate does not suppress it.

## Gate-evaluator contract (gates.yaml → engine/gates)

`engine/gates` is not yet implemented; `gates.yaml` is its **contract**. The module implements
exactly two generic primitives (no per-gate code), each summing weighted product terms:

- `threshold_gte` (`kind: visibility`) → `value = Σ terms`; **visible** iff `value ≥ threshold`,
  with `deadband` hysteresis to stop flicker. All visibility gates AND together (hard).
- `affine_cost` (`kind: preference`) → `costMod = clamp(base + Σ terms, min, max)`. The final
  action multiplier is the **product** of all matching preference gates (soft).

A `term` is `{ w, factors: [ref…] }` contributing `w · Π factor`. A `ref` reads a `stat`
(from `self` = `ToM[self]` by default, per **D8**; `@uses` binds to the action's `uses:` stat),
a `body` scalar (`Stamina|Adrenaline|Mood`), the derived `signal: Urgency`, a `tag_level:<family>`
magnitude, or a `const`; `complement: true` uses `1 − value`. `when` (tag match) selects which
actions a gate governs; `per_matched_tag` sums a gate once per matching tag (for multi-capability
actions like `Hunt`). Full field semantics are commented at the top of `gates.yaml`.

When `engine/gates` lands, its `SPEC.md` must implement these two primitives exactly; on any
mismatch, **fix the contract here first** (`CLAUDE.md` SPEC-first rule).

## Adding content (examples)

- **New stat**: add an entry to `stats.yaml` (passes the identifier pattern). Reference it from
  gates/actions as needed. No code, no schema edit.
- **New action**: add to `actions.yaml` with `tags` (so cost/gates apply automatically),
  `requires`/`produces` predicates (for GOAP), and either an `effect` or a consumed item.
- **New gate**: add to `gates.yaml` choosing `threshold_gte` or `affine_cost`, a `when` tag match,
  and `terms`. If it needs a new tag family, add its magnitudes to `balance.yaml.tag_levels`.
