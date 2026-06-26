# Data Contracts — Serialization · Redis · Postgres · Events

> Contracts that cross module boundaries. **Freeze before implementing consumers.** Bump `schema_version` and start here for any change.
> The below defines *shapes*. Exact field types are finalized by the architect against the `core` types.

## 0. Common
- Every payload carries `schema_version` (int). Bump +1 on any backward-incompatible change.
- Serialization: JSON during early debugging; may switch to a deterministic dense encoding (gob/protobuf) after it stabilizes — but **byte-determinism** must hold (sorted map-key order).
- IDs: `RunID`, `AgentID`, `ObjectID` are strings. Tick is an integer (game-minutes).

## 1. Simulation snapshot (engine → persist)
The complete deterministic state for one tick. Same snapshot + same seed → same next tick.
```
Snapshot {                        // persist.Snapshot — JSON, snake_case keys (Go field tags)
  schema_version, run_id, tick
  world {                          // engine world.WorldState
    tick, rng_state               // rng_state: for deterministic resume
    agents[] {
      id, pos
      real_stats   { StatID: float }            // god view (live exposure policy in §4)
      stamina, mood, adrenaline
      need_intensities { Dimension: float }
      inventory { Tag: int }, goal               // count per kind = SUM over the kind's live decay lots (§8) / tool instances (§9)
      plan_actions[], plan_horizon, plan_idx, elapsed, coping, latent[]
      self_est_stats { StatID: { mean, variance } }   // ToM[self] (D8 self-channel)
      agent_cfg
    }
    objects[] { id, kind, pos, owner?, remaining? }   // in-world objects; `remaining` = finite source count (§9, Xm1)
    known[]   { agent_id, objects[] }            // per-agent known-object set
    decay_lots[]      { object_id, kind, qty, decay_age, location }  // perishable LOTS (§8, Dm5(a)); periodic-full
    tool_instances[]  { object_id, kind, durability, location }      // durable TOOLS (§9, FINAL); periodic-full
    emerged_roles[] { function, holder }         // P6 (D2-derived)
    tom_digest {                                 // cross-agent ToM, god-view projection (D6/D8)
      <observerID>: { <subjectID>: { est_stats: { StatID: { mean, variance } }, rely_on: { Function: float } } }
    }
  }
}
```
- `tom_digest` is **capture-only** (NOT consumed by resume; the running sim rebuilds beliefs). It is the
  source for the god-view "others" channel: `real` (real_stats) ≠ `self` (self_est_stats) ≠ `others`
  (mean over `tom_digest[X][subject].est_stats`) are kept SEPARATE (D6/D8) — never a single reputation scalar.
- All keys are snake_case so the read API (`platform/api`) parses the live snapshot blob directly.
- **Inventory vs per-instance state (Materials Dm5(a)/FINAL).** `inventory { Tag: int }` is the per-kind
  COUNT view used by needs/planning/render. Two kinds of inventory item carry PER-INSTANCE state in a
  separate channel, with the `inventory` count = sum over instances:
  - a perishable kind is one or more **decay lots** `{kind, qty, decay_age}` (`decay_lots[]`, §8) — lots
    never auto-merge (Dm5(a));
  - a durable **tool** is one or more **tool instances** `{kind, durability}` (`tool_instances[]`, §9) —
    `stackable:false`, so each tool is its own instance carrying its CURRENT durability.
  A plain (non-perishable, non-tool) kind has no instance rows; its `inventory` count is authoritative.
- **`objects[].remaining`** is the finite-source count for an `ore_node` (Xm1, §9); absent for objects
  that are not finite sources. `objects[].owner` is the optional owner AgentID (glossary `owner`).

## 2. Redis — live state
Keyspace (`{run}` = RunID):
```
sim:{run}:meta          HASH    { tick, schema_version, started_at, status }
sim:{run}:tick          STRING  current tick (fast read)
sim:{run}:snapshot      STRING  latest Snapshot (serialized)  // or chunked
sim:{run}:agent:{id}    HASH     live agent summary (render): pos, goal, action, mood
sim:{run}:events        STREAM   events (§4). XADD append; SSE tails
```
- Live keys hold **only what render/observation needs.** Full state lives in the snapshot key.
- TTL: keep only active runs; on completion, back up to Postgres then expire.

## 3. Postgres — periodic backup (replay / analytics)
```
runs       ( run_id PK, seed, schema_version, started_at, ended_at, status, config_hash )
snapshots  ( run_id FK, tick, blob BYTEA, created_at )            // every N ticks
events     ( run_id FK, tick, seq, agent_id, type, payload JSONB ) // why-trace persisted
```
- Backup interval from config (`backup_every_ticks`). Reproduce from `seed + config_hash + last snapshot`.
- `events` is the source for why-trace, emergence-metric analysis, and replay.

## 4. Event / SSE schema (observability + frontend)
The engine emits via the `events` interface. why-trace and SSE are **two views of the same stream.**
```
Event {
  schema_version, tick, seq, agent_id?, type, payload
}
```
Representative `type`s (payload gist):
| type | payload |
|------|---------|
| `Perceived` | sense, object_id, salience_delta |
| `GoalSelected` | dimension, target, priority, eff_value |
| `PlanBuilt` | steps[], total_cost, provisioned[] |
| `ActionStarted` / `ActionDone` | action, progress / result |
| `Interacted` | with, signal_kind, claimed_value, accepted |
| `BeliefUpdated` | about, stat, old, new, cause(`observe|gossip`) |
| `ReputationGossip` | about, from, trust, delta |
| `CopingEntered` | mode(`rebind|longing|latent|apathy`) |
| `RoleEmerged` | function, holder, reliance_share |
| `Decayed` | object_id, kind, from_state, to_state, transform[]{item, qty} (Materials §8; on a decay transition) |
| `Crafted` | recipe, outputs[]{item, qty, durability?}, consumed[]{kind, amount}, tools_worn[]{object_id, durability, broke} (Materials P_m3/FINAL; on a Craft completion — `durability` is the produced tool's start durability = basis_stat roll·wear_max) |
| `Mined` | object_id (ore_node), yields[]{item, qty}, remaining, depleted, tool, tool_broke (Materials P_m4/Xm; on a Mine completion; `depleted`+the reroute when remaining→0) |
| `ToolBroke` | object_id, kind, owner (Materials FINAL; on a tool reaching 0 durability → object-mortality) |

- **why-trace** = NFR-3. Put the *selection rationale* (competing candidates, gates, costs) into `GoalSelected` / `PlanBuilt` so "why did it do this" is reconstructable.
- **SSE view** = the frontend-graphics subset (positions, actions, major events). Sensitive god-view fields (`real_stats`) are not sent over SSE (controlled by an observation-mode flag).

## 5. Determinism & versioning
- Resuming from a snapshot must be **byte-identical** to running from the start (test: `docs/testing.md`).
- On `schema_version` mismatch, persist refuses to load and demands a migration path.

## 6. Navmap / terrain (map subsystem)
- Navmap wire = **building footprints** + **sparse `wear`** + **terrain state**. Per `design.md §5`, terrain is **dynamic** (moisture/transition), so it streams like wear — **periodic full + sparse deltas**, NOT a one-time static layout.
- Determinism (D12): the snapshot is copy-on-write; the running tick deposits `wear` and applies terrain transitions in the **serial apply** phase (sorted cell order), never during plan. Bulk terrain recompute is `tick`-triggered (`tick % N`), never wall-clock.
- An `ore_node` exhaustion (Xm2/Xm3) is one such terrain transition: on `remaining→0` the world fires ONE `navmap.SetTerrain` over the node's cells → `depleted_terrain` (e.g. `bare_rock`), streamed via `TerrainOverrides()` (the same sparse delta channel as climate transitions). NOT during plan; apply phase, sorted cells (D12).
- The exact navmap snapshot shape is finalized when `engine/navmap` serialization lands — see `docs/map-plan.md` M5 + `docs/climate.md`.

## 7. Recipes (Materials & Crafting — content, `content/recipes.yaml`)
> The recipe model is FINAL/LOCKED (`docs/materials.md §0 "Recipe model — FINAL"`): slot/alternative
> inputs, `wear|consume` modes, `ambient` stations, per-recipe `duration`, `basis_stat`.
- `recipes.yaml` is **load-time content**, not a runtime cross-module wire payload — it is compiled by
  `platform/config` into the recipe registry and never serialized into the snapshot.
- Shape (schema `content/schema/recipes.schema.json`): `recipes[] { id, inputs[], ambient[]?(tags),
  duration(ticks), basis_stat(StatID), outputs[]{item, base_qty} }`. `inputs[]` is an ordered list of
  SLOTS; each slot `{ any: [alternative,…] }` is satisfied by EXACTLY ONE alternative (OR). An
  alternative is `{ tagQuery[] (AND), amount, mode: wear|consume }`:
  - `tagQuery` is a tag set the matched item must carry (D4 — any item whose `tags` ⊇ the set
    auto-qualifies); NOT an item id.
  - `consume` → remove `amount` matching items/units (most-decayed lots first, ties by ObjectID; works
    on stackable lots AND a whole durable instance — a tool consumed as material).
  - `wear` → the matched item MUST carry a `tool` block; decrement its CURRENT durability by `amount`,
    break (object-mortality) at 0, else persist (most-worn matching tool, ties by ID).
  - `ambient` = station tags the actor must be IN RANGE of (substitutable; NOT consumed; optional).
  - `basis_stat` = the StatID whose roll drives success, qty, AND the produced instance's durability
    (a produced tool's start durability = roll·`wear_max`). `outputs[].base_qty` = the pre-roll base.
- **Craftable gate** (planner/world reads the BOUND recipe): every slot has a satisfiable alternative AND
  every `ambient` station is in range. No partial run; no static action tool tag.
- `platform/config` cross-checks: every `outputs[].item` is a known item_kind; every alternative
  `tagQuery` tag is carried by ≥1 item_kind; every `wear` alternative's tagQuery is satisfiable ONLY by
  `tool`-block item_kinds; every `ambient` tag is carried by ≥1 object_kind; `basis_stat` is a known
  StatID (D10); an unreachable recipe is a load-time error.
- **Material / tool / station tags** are content fields on `objects.yaml` item_kinds/object_kinds
  (`tags: [shaft_stock, …]`), validated by `content/schema/objects.schema.json`. They are the recipe's
  matching vocabulary; no runtime wire change.
- The `Craft` action (`content/actions.yaml`) is `recipe_mediated: true` (FINAL) and carries NO tool
  tag, NO target/station field, and NO duration — it binds a recipe at plan time and is NOT in this
  file. Craft APPLY semantics (first-satisfiable alternative per slot; `consume` most-decayed-first;
  `wear` most-worn-first + break at 0; `ambient` in-range gate; `basis_stat` outcome roll + produced
  durability; fresh decay lot for a perishable output) are `engine/world`.

## 8. Decay state (Materials & Crafting — engine `decay` → persist)
> Finalized to the RESOLVED (a) shapes — `docs/materials.md §1` Dm1–Dm5 all `RESOLVED: (a)`. The decay
> UNIT is a LOT; `decayAge` is a continuous effective-decay-time accumulator; the derived `state` is
> recomputed, never stored. Bump on any change.
- Decay streams like the navmap/flora fields: **periodic full + sparse age/transition/transform/gone
  deltas** (mirrors §6). The decaying-LOT set is serialized as the periodic-full source; per-step
  `decay.Step` deltas are the sparse channel.
- **Per-lot decay state (the periodic-full row, `decay_lots[]` in §1):** `{ object_id, kind, qty,
  decay_age, location }` where:
  - `decay_age` is the continuous **effective-decay-time** accumulator (Dm1(a)) — monotone ≥ 0, the one
    scalar that makes resume byte-identical (§5).
  - `state` (the derived discrete index) is **NOT stored** — it is recomputed from `decay_age` vs the
    kind's `decay.states[].threshold` thresholds (D9, mirrors flora `Stage`). Renderers/why-trace derive
    it on read.
  - `qty` is the **lot** quantity (Dm5(a)) — all `qty` items in the lot share one `decay_age`/state; lots
    never auto-merge, so a kind's `inventory` count (§1) is the sum over its live lots.
  - `location` discriminates where the lot lives (an agent inventory id / floor / storage handle) so the
    owner-agnostic lot (Dm4(a)) round-trips to the right place; world owns this mapping.
- **The transition/transform delta (per `decay.Step`, the `StepDeltas` shape):**
  - `aged[] { object_id, decay_age }` — lots whose `decay_age` advanced with no state change.
  - `transitioned[] { object_id, decay_age, state }` — lots that crossed into a new derived `state`.
  - `transformed[] { source_id, state, item, qty }` — transform products (Q3) a transition produced;
    `qty = decay.states[state].transform[].qty · source_lot.qty` (Dm5(a)). world places them where the
    source lot was.
  - `gone[] { object_id }` — lots that reached the terminal `gone` state (the only mass removal).
  - All slices are sorted by `object_id` (D12). world (sole object mutator) applies these to
    `objects[]`/inventory and emits the `Decayed` event (§4) on a transition.
- Determinism (D12): like climate/flora, `decay.Step` runs in the **serial apply** phase on the
  `tick % N` decay cadence (never wall-clock); the env (`Temperature`/`Moisture`) + the storage rate
  multiplier are world-sampled per lot and injected as values, and `decay_age += elapsedTicks ·
  baseRate · accel(temperature,moisture) · storageRateMult` (Dm2(a)).
- The exact wire encoding is finalized when `engine/decay` serialization lands (P_m2) — the shape above
  is fixed by Dm1–Dm5(a). See `docs/materials.md §1/§2`, `backend/engine/decay/SPEC.md`.

## 9. Tool durability + finite sources (Materials & Crafting — FINAL/Xm1 → persist)
> Finalized to the LOCKED recipe model (`docs/materials.md §0`). Tool durability + node `remaining` are
> runtime per-INSTANCE state mutated by `engine/world` (apply phase, sole mutator).
- **Tool instance (the periodic-full row, `tool_instances[]` in §1):** `{ object_id, kind, durability,
  location }`:
  - `kind` is a `tool:`-block item_kind (`crafted_tool`, `pickaxe`); `stackable:false`, so each tool is
    its own instance (NOT a `{Tag:int}` count) carrying its CURRENT durability.
  - `durability` is the tool's CURRENT durability (FINAL) — set at creation to `basis_stat roll · wear_max`
    (skilled crafters make sturdier tools, capped at the kind's `wear_max` ceiling), then DECREMENTED by
    use: a recipe `wear` alternative subtracts that alternative's `amount`; a Mine subtracts the Mine
    action's world/balance wear rate. There is NO per-item `wear_per_use` (the amount is per-recipe / a
    world rate). When `durability` reaches 0 the world REMOVES the tool (object-mortality, §7 — break =
    reaching 0) and emits `ToolBroke` (§4). One scalar to snapshot ⇒ resume byte-identical.
  - The §6 tool-quality reads `durability / wear_max` via the world's `expr.Context` operand
    `tool:<family>.quality` (Cm3) — used by the Mine yield. The `expr` L0 interface is UNCHANGED (a
    world-Context `Attr` operand, `engine/expr/SPEC.md` "callers adapt"), not a new method. (There is no
    per-item `tool.quality` formula any more — output quality comes from the crafting `basis_stat` roll.)
  - `location` discriminates which inventory/floor/storage the tool is in (parity with `decay_lots[]`).
- **Finite source (`objects[].remaining` in §1, Xm1):** an `ore_node` carries `remaining` (starts at the
  `source.initial` count). `Mine` decrements it per successful extraction; on `remaining→0` the world
  removes the node (object-mortality) + fires ONE `navmap.SetTerrain` → `source.depleted_terrain`
  (Xm2/Xm3, §6). `remaining` is object-local (D9 locality), captured in `objects[]`.
- Determinism (D12): tool durability + node `remaining` are mutated only in the **serial apply** phase,
  in fixed agent-ID then action order; per slot the FIRST satisfiable alternative is applied — `consume`
  takes most-decayed lots first (ties by `object_id`), `wear` takes the most-worn matching tool (ties by
  `object_id`). No wall-clock, no map-iteration for the apply order.
- The exact wire encoding is finalized when the Craft/Mine apply lands (P_m3/P_m4); the shape above is
  fixed by the LOCKED model. See `docs/materials.md §0/§1/§2`, `backend/engine/actions/SPEC.md`.
