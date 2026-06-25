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
      inventory { Tag: int }, goal               // count per kind = SUM over the kind's live decay lots (§8, Dm5(a))
      plan_actions[], plan_horizon, plan_idx, elapsed, coping, latent[]
      self_est_stats { StatID: { mean, variance } }   // ToM[self] (D8 self-channel)
      agent_cfg
    }
    objects[] { ... }
    known[]   { agent_id, objects[] }            // per-agent known-object set
    decay_lots[] { object_id, kind, qty, decay_age, location }  // perishable LOTS (§8, Dm5(a)); periodic-full
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
- **Inventory vs decay lots (Materials Dm5(a) RESOLVED).** `inventory { Tag: int }` is the per-kind
  COUNT view used by needs/planning/render. The decay UNIT is a **lot** `{kind, qty, decay_age}` (§8) —
  a perishable kind held by an agent is one or more lots, and its `inventory` count is the **sum over
  that kind's live lots** (lots never auto-merge in P_m2). The lot rows live in the separate
  `decay_lots[]` channel (§8) keyed by `object_id` + a `location` discriminator (which agent's inventory
  / floor / storage), so a lot's decay state is captured exactly without bloating the count view. A
  non-perishable kind has no lot rows; its `inventory` count is authoritative on its own. This shape is
  fixed by Dm5(a); see `docs/materials.md §1 Dm5`.

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
| `Decayed` | object_id, kind, from_state, to_state, transform[]{item, qty} (Materials §8; emitted on a decay transition) |

- **why-trace** = NFR-3. Put the *selection rationale* (competing candidates, gates, costs) into `GoalSelected` / `PlanBuilt` so "why did it do this" is reconstructable.
- **SSE view** = the frontend-graphics subset (positions, actions, major events). Sensitive god-view fields (`real_stats`) are not sent over SSE (controlled by an observation-mode flag).

## 5. Determinism & versioning
- Resuming from a snapshot must be **byte-identical** to running from the start (test: `docs/testing.md`).
- On `schema_version` mismatch, persist refuses to load and demands a migration path.

## 6. Navmap / terrain (map subsystem)
- Navmap wire = **building footprints** + **sparse `wear`** + **terrain state**. Per `design.md §5`, terrain is **dynamic** (moisture/transition), so it streams like wear — **periodic full + sparse deltas**, NOT a one-time static layout.
- Determinism (D12): the snapshot is copy-on-write; the running tick deposits `wear` and applies terrain transitions in the **serial apply** phase (sorted cell order), never during plan. Bulk terrain recompute is `tick`-triggered (`tick % N`), never wall-clock.
- The exact navmap snapshot shape is finalized when `engine/navmap` serialization lands — see `docs/map-plan.md` M5 + `docs/climate.md`.

## 7. Recipes (Materials & Crafting — content, `content/recipes.yaml`)
- `recipes.yaml` is **load-time content**, not a runtime cross-module wire payload — it is compiled by
  `platform/config` into the recipe registry and never serialized into the snapshot.
- Shape (schema `content/schema/recipes.schema.json`): `recipes[] { id, inputs[]{tagQuery[], qty},
  outputs[]{item, qty}, tool?(tag), station?(tag) }`. Inputs are **tag-queries** (substitutability
  emerges from tags, D4) — NOT item ids; a kind satisfies a slot iff its `objects.yaml` `tags` is a
  superset of `tagQuery`. `platform/config` cross-checks output items + input/tool/station tag
  reachability against the object/item catalog (D10); an unreachable recipe is a load-time error.
- **Material / tool / station tags** are content fields on `objects.yaml` item_kinds/object_kinds
  (`tags: [shaft_stock, …]`), validated by `content/schema/objects.schema.json`. They are the recipe's
  matching vocabulary; no runtime wire change.

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
