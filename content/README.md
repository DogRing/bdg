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
| `needs.yaml` | need/value dimension **catalog** (kind, posture, setpoint, salience) | D9 (rate only; demand is derived) |
| `objects.yaml` | object_kinds + item_kinds | D9 (objects carry only their **supply** Effect) |
| `actions.yaml` | atomic action catalog | D3 (atomic only; no trees), D4 (cost/gates from tags) |
| `gates.yaml` | gate registry (boolean **predicate trees**) | D4 (tag-matched), D8 (decisions read `ToM[self]`) |
| `balance.yaml` | global scalars **+ per-need rate block (`needs:`)** | tuning target (untuned defaults; `docs/testing.md` §5) |
| `schema/` | JSON Schema (2020-12) | the loader rejects any file that fails its schema |

Every file carries `schema_version` (`docs/data-contracts.md` §0). Bump it **and** the matching
schema's `const` together on any incompatible change. (`gates.yaml`/`gates.schema.json` are at
`schema_version: 2` after the predicate-tree change; the others at `1`.)

## Need definitions split across two files

A need / value **Dimension** is defined in two places (merged by `engine/needs.Load`, which
`platform/config` feeds both readers):

- **`needs.yaml`** — the dimension *catalog*: `id`, `kind` (`consumable`|`conditional`), default
  `posture`/`setpoint`/`referent`, and the `salience` curve. No tunable rate here.
- **`balance.yaml` `needs:` block** — the per-need *rate* constants for **consumable** needs:
  `decay_per_tick` (D9: the only authored demand input — demand = rate × predicted-time is
  derived by the planner, never authored) and `satisfaction_threshold`. Conditional needs
  (rate 0, event-driven) are **not** listed in this block.

Keeping rates in `balance.yaml` puts every tunable scalar in the single auto-tuning file
(`docs/testing.md` §5); the catalog stays in `needs.yaml`. `platform/config` cross-checks that
every consumable dimension has a matching `balance.yaml needs:<id>` entry and vice-versa.

## Load & validate flow (platform/config)

1. Parse YAML → generic map.
2. Validate against `content/schema/<file>.schema.json` (structural: shapes, enums, bounds).
3. **Referential integrity** (the loader, not JSON Schema): every `StatID` used in `gates.yaml`
   (stat leaves) exists in `stats.yaml`; every need id in `objects.yaml`/`actions.yaml` exists in
   `needs.yaml`; every `target_kind`/item id in `actions.yaml` exists in `objects.yaml`; every
   `balance.yaml needs:<id>` key names a **consumable** dimension in `needs.yaml` (and every
   consumable dimension has a rate entry); every `tag_level`/`cost_terms` family referenced by
   `actions.yaml`'s tag cost composition exists in `balance.yaml`.
4. Build registries; compute a `config_hash` (`docs/data-contracts.md` §3) for replay.

Schemas use an **identifier pattern**, not a hardcoded stat enum, so adding a stat needs no
schema edit — step 3 catches typos by cross-checking against `stats.yaml`.

## Tag grammar (D4)

Actions are annotated with tags; **cost derives entirely from tags** (in the planner), and
**gate visibility is tag-matched**, never from per-action code. Form: `family:value` or a bare
`family`.

| Family | Values | Read by |
|--------|--------|---------|
| `uses:` | `Strength` `Agility` `Intelligence` | capability_floor gate (visibility); outcome resolution |
| `effort:` | `none` `low` `med` `high` | effort cost-term (planner); stamina runtime check (planner/agent) |
| `risk:` | `low` `med` `high` | risk cost-term (planner) |
| `violent:` | `low` `med` `high` | moral cost-term magnitude (planner) |
| `noise:` | `low` `med` `high` | perception/detection (hearing radius) |
| `abstraction:` | `low` `med` `high` | knowledge gate (visibility, × Intelligence) |
| `norm:transgressive` | (flag) | conscience gate (visibility) + moral cost (planner) |
| `social`, `social:covert`, `cooperative` | (flags) | social cost-term (planner) |
| `time:by_distance` | (flag) | time cost-term scales with travel distance (planner) |

Numeric magnitudes for each level live in `balance.yaml.tag_levels`; cost-term weights in
`balance.yaml.cost_terms` (both read by the **planner's** cost composition). **Violence ≠
transgression**: hunting is `violent:low` but not `norm:transgressive`, so the conscience gate
does not suppress it.

## Gate-evaluator contract (gates.yaml → engine/gates), schema_version 2

`engine/gates` is not yet implemented; `gates.yaml` is its **contract**. A gate is
`{ id, tags:[Tag…], expr }`. The gate is matched to a candidate action iff the action carries
**any** tag in `tags` (empty/absent `tags` = matches all). For a matched action the gate's
recursive boolean **`expr`** is evaluated against the agent snapshot; an action is **visible**
iff **every** matching gate's `expr` is true (hard AND across matching gates). Gates are
evaluated **at planning time** and are **never** stored on objects (D5).

`expr` grammar (recursive):

- leaf — `{ stat: <StatID>, op: ">="|">"|"<="|"<"|"=="|"!=", value: <number> }` (compares the
  agent's `ToM[self]` value of the stat, per **D8** — never Real Stats; gates never correct it),
  or `{ tag: <Tag> }` (true iff the candidate action carries that exact tag).
- composite — `{ and: [expr…] }` / `{ or: [expr…] }` / `{ not: expr }`.

When `engine/gates` lands, its `SPEC.md` must implement this grammar exactly; on any mismatch,
**fix the contract here first** (`CLAUDE.md` SPEC-first rule).

> **Scope change (schema_version 1 → 2, human-approved).** Gates are now **boolean visibility
> preconditions only**. They carry **no cost channel** — action cost derives entirely from tags
> in the **planner** (`cost_terms` × `tag_levels`); the former preference/cost gates
> (capability_cost, caution, apathy, conscience_cost, adrenaline, sociability) moved there. Leaf
> comparisons read **stats (ToM[self]) only**; dynamic visibility that depended on body scalars /
> Urgency / Adrenaline (the old `stamina` gate, the conscience loosening) moved to
> planner/agent runtime checks outside this file. See the header note in `gates.yaml` and
> `backend/engine/gates/SPEC.md` §Open Questions for the scope transfer to the planner.

## Adding content (examples)

- **New stat**: add an entry to `stats.yaml` (passes the identifier pattern). Reference it from
  gates/actions as needed. No code, no schema edit.
- **New consumable need**: add a catalog entry to `needs.yaml` (`kind: consumable`) **and** a
  `needs:<id>` rate entry (`decay_per_tick`, `satisfaction_threshold`) to `balance.yaml`.
- **New action**: add to `actions.yaml` with `tags` (so cost/gates apply automatically),
  `requires`/`produces` predicates (for GOAP), and either an `effect` or a consumed item.
- **New gate**: add to `gates.yaml` with an `id`, a `tags` match list, and a boolean `expr` tree
  of stat-comparison / tag-membership leaves combined with `and`/`or`/`not`.
