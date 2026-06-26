# SPEC — `engine/env/decay`

> Status: `READY` (Dm1–Dm5 RESOLVED as (a) — `docs/materials.md §1`; no open holes)
> Leaf level: `L1`  ·  Owner agent: `<filled by implementer>`

## Purpose
The **decay driver**: a pure, deterministic transform over the set of perishable item **lots**
(wherever they live — floor, inventory, or storage — owner-agnostic, Dm4). Given the prior decay
state, the per-lot environment (`{Temperature, Moisture, StorageRateMult}`, supplied as VALUES by
`world` from the climate output shape + the storage structure), and the data-defined decay `Rules`, it
advances each lot's continuous `DecayAge`, fires **discrete state transitions** (fresh→stale→rotten→gone)
when `DecayAge` crosses the next state's threshold, and emits the **transform products** a transition
produces (D9 locality — decay produces an item, it does not silently vanish mass except at the terminal
`gone` state). It owns *how items spoil* — it does **not** own *when* it runs (cadence), the climate
state it reads (those are values `world` injects), the storage-structure model, the per-lot position
sampling, or the object mutation itself (that is `engine/world`, the sole object mutator). No IO, no
wall-clock, no global rand: every output is a function of `(prev, inputs, elapsedTicks, Rules, rng)`
(D12). Mirrors `engine/env/flora`'s "pure read → return delta → world applies" shape exactly. Concept &
rationale: `docs/design.md §5/§9` + `docs/materials.md` (Q1–Q5 binding; §0/eng locked; Dm1–Dm5 RESOLVED).

## Public Interface
```go
package decay

import (
    "github.com/dogring/bdg/engine/kernel/core"
    "github.com/dogring/bdg/engine/kernel/expr" // Dm3(a): decay owns + evaluates the §6 accel Program (flora parity)
    "github.com/dogring/bdg/engine/kernel/rng"
)

// ── Identity & state ──────────────────────────────────────────────────────────────

// KindID names an item_kind from content/objects.yaml that carries a `decay:` block (e.g.
// "berries", "raw_meat"). core.Tag underlying so the content catalog validates it; decay never
// parses YAML (D10).
type KindID = core.Tag

// Lot is one perishable item LOT's decay-owned dynamic state — the decay UNIT (Dm5(a)). A lot is a
// group of `Qty` identical-kind items that age together, identified by a stable instance id (NOT an
// agent + kind), so a lot decays identically whether it sits on the floor, in a Body.Inventory, or in
// storage (Dm4(a) owner-agnostic; eng-locked 바닥·인벤토리·저장 동일 틱) — a dying agent's dropped lots
// keep decaying with no special case. Lots NEVER auto-merge in P_m2: a freshly produced lot is a NEW
// instance with DecayAge=0, so a kind's `{Tag:int}` inventory count is the SUM over its live lots
// (the merge/age-averaging policy is deferred — Out of Scope). The lot's Pos + storage handle are
// world's (world samples climate at Pos + computes StorageRateMult); decay reads only the resulting
// EnvInput values.
//
//   Kind     — the decaying item_kind (selects the Rules entry).
//   Qty      — the lot quantity (Dm5(a)): all Qty items share this lot's single DecayAge/State.
//   DecayAge — the continuous effective-decay-time accumulator (Dm1(a)): monotone ≥ 0, advanced by
//              `DecayAge += elapsedTicks · effectiveRate` per Step (Dm2(a)). One scalar to snapshot,
//              resume-safe. The discrete State is DERIVED from DecayAge via the kind's thresholds
//              (never stored, mirrors flora Length→Stage; D9).
type Lot struct {
    ID       core.ObjectID // stable instance id (world owns the id space; loose floor lots + inventory lots alike)
    Kind     KindID
    Qty      int           // lot quantity (Dm5(a)); all share one DecayAge
    DecayAge float64       // continuous effective-decay-time accumulator (Dm1(a)), in EFFECTIVE-TIME units
}

// EnvInput is the per-lot exogenous environment world samples at the lot's position and injects each
// decay step. Decay is a pure transform over VALUES — it does NOT import climate (mirrors flora's
// SiteInput rule). Temperature/Moisture are the SHAPE of the climate output (climate.CellState's
// normalized [0,1] fields), passed as plain numbers — the `accel` §6 Context operand names match the
// climate/flora Context vocabulary exactly (`temperature`, `moisture`).
//
// StorageRateMult is the storage-structure rate multiplier (eng-locked: 저장 구조물이 rate 곱 감속 →
// cold-storage emerges, D2). world computes it from the structure the lot is stored in (a cold cellar
// < 1 slows decay; open air = 1; a warm hearth > 1 speeds it) and injects it as a value — decay does
// NOT model storage structures (that is world's, like flora's NeighborCount). It composes
// MULTIPLICATIVELY: `effectiveRate = baseRate · accel(temperature, moisture) · StorageRateMult` (Dm2(a)).
type EnvInput struct {
    Temperature     float64 // climate Temperature at the lot's Pos ∈ [0,1] (accel operand `temperature`)
    Moisture        float64 // climate Moisture at the lot's Pos ∈ [0,1] (accel operand `moisture`)
    StorageRateMult float64 // storage-structure rate multiplier (≥ 0; 1 = neutral, < 1 = preserved, 0 = halted)
}

// State is the whole decay field for one run: the live Lot set keyed by ObjectID, iterated in sorted
// ObjectID order (D12). Snapshot-serializable (data-contracts §8: periodic full + sparse
// age/transition/transform/gone deltas). Owned by engine/world (one per run); Step returns a fresh
// next and never mutates prev (so plan-snapshot and apply-write don't alias, mirroring flora.State).
type State struct{ /* opaque; see Owned Data. Holds the Lot set in ObjectID order. */ }

// ── Construction ────────────────────────────────────────────────────────────────

// New builds the initial decay State from an already-placed perishable-lot set. Placement (which lots
// exist, where) is world's, not this module's. Pure; no RNG draw at construction.
func New(lots []Lot) *State

// ── The pure transform world calls (mirrors flora.Step) ────────────────────────────

// Step advances the whole decay field ONE decay step (= N ticks; world owns the multi-rate cadence
// and only calls Step when tick % N == 0, eng-locked "부패 틱 cadence(N틱)"). It is a PURE function of
// (prev, inputs, elapsedTicks, rules, rng): it does NOT mutate prev, and returns the deltas world
// applies in the apply phase. For each lot, in sorted ObjectID order (D12):
//   effectiveRate = rules.BaseRate(Kind) · rules.Accel(Kind, inputs[ID]) · inputs[ID].StorageRateMult  (Dm2(a))
//   DecayAge'     = DecayAge + float64(elapsedTicks) · effectiveRate                                     (Dm1(a))
//   State'        = rules.StateAt(Kind, DecayAge')   (derived; flora Length→Stage parity)
// A lot whose derived State increased this step emits a TransitionDelta; the state it ENTERED emits its
// `transform` products (TransformOut, Q3) and, if that state is terminal `gone` (no transform), the lot
// is added to Gone (the only mass removal, D9). A lot whose State is unchanged but DecayAge advanced
// emits an AgeDelta. (Several thresholds may be crossed in one step if elapsedTicks·effectiveRate is
// large: the final derived State is used; every transform on each state passed THROUGH is emitted, in
// ascending state order, so no product is skipped — D9 locality.)
//
//   elapsedTicks — the number of ticks since the last decay step (the cadence interval N). Scales the
//                  DecayAge advance linearly (Dm1(a)).
//
// inputs[lot.ID] is the EnvInput for that lot; a missing entry is a world-contract bug (panic, like
// flora's missing SiteInput) — every live perishable lot must have its environment sampled.
// rng is the per-step seeded fork world supplies (mirrors flora; reserved — discrete-threshold decay
// is deterministic and draws nothing, but kept in the signature for shape parity + forward-compat with
// a future stochastic-spoilage variant; it is NOT drawn from in P_m2).
func Step(
    prev *State,
    inputs map[core.ObjectID]EnvInput,
    elapsedTicks int64,
    rules *Rules,
    rng *rng.RNG,
) (next *State, deltas StepDeltas)

// StepDeltas is the world-applied result of one decay step. world is the sole object mutator: it
// updates the DecayAge of survivors (Aged), records the new derived State (Transitioned), adds the
// Transformed products to objects[]/inventory, and removes Gone lots. All slices are in sorted
// ObjectID order (D12). A lot appears in at most one of {Aged, Transitioned} (Transitioned implies its
// DecayAge also advanced — the new DecayAge is carried on the TransitionDelta, so Aged is the
// no-state-change subset only); a transitioning lot may additionally contribute to Transformed/Gone.
type StepDeltas struct {
    Aged         []AgeDelta        // lots whose DecayAge advanced with NO state change this step
    Transitioned []TransitionDelta // lots that crossed into a new discrete State this step
    Transformed  []TransformOut    // transform products emitted on a transition (Q3; world adds them)
    Gone         []core.ObjectID   // lots that reached the terminal `gone` state (removed by world)
}

// AgeDelta carries a survivor's new ABSOLUTE DecayAge (not an increment, so apply is idempotent and
// order-free across survivors, mirroring flora.GrowthDelta).
type AgeDelta struct {
    ID       core.ObjectID
    DecayAge float64 // new absolute effective-decay-time accumulator
}

// TransitionDelta carries a lot that entered a new discrete State this step. State is the derived
// index into the Kind's ordered `states` (NOT stored on Lot; world records it for render/value). The
// per-state supply OVERRIDE (decayed food feeds less) is read from Rules.SupplyAt by the consumer.
type TransitionDelta struct {
    ID       core.ObjectID
    DecayAge float64 // the new absolute accumulated measure (this lot also advanced; not also in Aged)
    State    int     // derived discrete state index it ENTERED (0 = fresh …)
}

// TransformOut is a transform product a transition produces (Q3 — D9 locality, NOT vanish). world
// places Qty of Item into the same location the source lot occupied (the inventory/floor/storage the
// source was in). SourceID names the lot that produced it (so world knows where to place + what to
// consume/replace). The produced Qty scales with the SOURCE LOT'S Qty (Dm5(a) — a lot of 4 berries
// rotting yields 4·transform_qty of the product); the source lot's items are consumed by the transition.
type TransformOut struct {
    SourceID core.ObjectID // the decaying lot that transitioned and produced this
    State    int           // the derived state index whose `transform` emitted this (the state ENTERED)
    Item     KindID        // the produced item_kind (objects.yaml decay.states[].transform[].item)
    Qty      int           // produced quantity = transform[].qty · sourceLot.Qty (Dm5(a))
}

// ── Rules (the data-defined decay table) ────────────────────────────────────────────

// Rules is the compiled, immutable per-KindID decay table from content/objects.yaml `decay:` blocks:
// for each kind, the baseRate, the ordered discrete states (name + entry threshold + transform
// products + optional per-state supply override), and the compiled `accel` §6 Program (Dm3(a) — decay
// OWNS the Program and evaluates it via engine/kernel/expr, flora parity). Built once by platform/config
// (parse the §6 accel via engine/kernel/expr, validate kind/item ids, enforce strictly-ascending thresholds).
// engine/env/decay evaluates it read-only; it never parses YAML (D10). Mirrors flora.Rules.
type Rules struct{ /* opaque; per-KindID baseRate + ordered states + the compiled accel expr.Program. */ }

// StateAt maps a continuous DecayAge (effective-time units, Dm1(a)) to the kind's DERIVED discrete
// state index via the kind's ordered thresholds (mirrors flora.Stage over Length; D9 — state is never
// stored). DecayAge below the first non-zero threshold ⇒ state 0; at/above the last threshold ⇒ the
// terminal state. Pure, no RNG.
func (r *Rules) StateAt(kind KindID, decayAge float64) int

// SupplyAt returns the supply Effect a kind delivers in a given derived state — the per-state override
// if the state declares one, else the item_kind base supply (decayed food feeds less, D9 — still a
// supply Effect on the object, no future field). Read by world/values when an item from the lot is
// consumed. Pure, no RNG.
func (r *Rules) SupplyAt(kind KindID, state int) map[core.Dimension]float64

// BaseRate returns the kind's data base decay rate (Q2: 기본 rate는 데이터), in effective-decay-time
// units per tick at neutral env+storage. It is the first multiplicative term of effectiveRate (Dm2(a)).
func (r *Rules) BaseRate(kind KindID) float64

// Accel evaluates the kind's §6 acceleration multiplier over the env (Temperature/Moisture). Decay
// builds an expr.Context from EnvInput (operands `temperature`, `moisture`) and calls the compiled
// accel Program (Dm3(a) — decay-owned). It is the second multiplicative term of effectiveRate (Dm2(a));
// cold+dry → < 1 (slows), warm+wet → > 1 (speeds), per the authored formula. Pure, no RNG (a §6 numeric
// expression; design.md §6). Domain clamp (≥ 0) is decay's, matching the climate/flora clamp contract.
func (r *Rules) Accel(kind KindID, in EnvInput) float64
```

## Dependencies
- `engine/kernel/core` — `Tag`, `ObjectID`, `Dimension`. No IO.
- `engine/kernel/expr` — the shared §6 `Formula` evaluator (L0, already decided). decay OWNS the compiled
  per-kind `accel` `Program` inside its `Rules` (Dm3(a)) and evaluates it: it builds an `expr.Context`
  from `EnvInput` (operands `temperature`, `moisture`) and calls the `Program`, exactly as flora
  evaluates its suitability/rate programs. decay does NOT parse the DSL (that is `platform/config`'s
  compile step) and never imports `engine/mind/gates`.
- `engine/kernel/rng` — `*RNG` (injected seeded fork; reserved for forward-compat, mirrors flora; discrete-
  threshold decay is deterministic and draws nothing in P_m2).
- *(NOT `engine/env/climate`)* — one-way: world samples climate `Moisture`/`Temperature` at the lot
  position and injects them as `EnvInput` values (mirrors flora's no-climate rule). decay reads them as
  numbers; it never imports climate.
- *(NOT `engine/world`)* — world owns objects[]/inventory; it APPLIES `StepDeltas` (age/transition/
  transform/gone) and is the sole object mutator (D12 apply phase). decay returns deltas and never
  mutates world state.
- *(NOT `engine/env/flora`, `engine/space/navmap`, `engine/agent`)* — decay is an independent L1 leaf; it does not
  import siblings (would break the DAG). The storage-structure model, the per-lot position sampling, and
  the owner-agnostic lot enumeration (floor + every inventory + storage) are world's (Dm4(a)).

## Owned Data
- The **live `Lot` set** (`ID, Kind, Qty, DecayAge`) held in `State`, snapshot-serialized
  (data-contracts §8). `State` is owned by `engine/world` (one per run); `decay.Step` returns a fresh
  `next` and never mutates `prev`.
- The **`Rules`** table is owned by `platform/config` (built from `content/objects.yaml` `decay:`
  blocks, with the `accel` §6 compiled via `engine/kernel/expr`), injected read-only; this module never parses
  or mutates it.
- This module owns **no** object instances in `objects[]`, **no** inventory entries, **no** storage
  state — it only emits `StepDeltas`.

## Invariants
- **D12 determinism** — `Step` is a pure function of `(prev, inputs, elapsedTicks, Rules, rng)`. Same
  inputs ⇒ byte-identical `next` State and `StepDeltas`. No `time.Now()`, no global rand, no wall-clock;
  the only randomness source is the injected `*rng.RNG` (unused in P_m2 — deterministic thresholds).
- **D12 no map-iteration for logic** — the `Lot` set is iterated in **sorted `ObjectID` order** for the
  Step sweep, transitions, and serialization; the `inputs` map is read by sorted lot id, not by
  map-range. `Aged`/`Transitioned`/`Transformed`/`Gone` are in sorted `ObjectID` order. Float
  accumulation order is fixed (one `DecayAge` advance per lot in a fixed order).
- **D9 no future field** — a `Lot` carries `DecayAge` (current accumulated measure) only; State and
  supply are DERIVED from it. It carries **no** "time-until-rot", "remaining freshness", or "future
  need" field.
- **D9 locality — transform, not vanish (Q3)** — a state transition emits its `transform` products
  (`TransformOut`), an item, not nothing; only the terminal `gone` state (no transform) removes mass.
  Crossing multiple states in one step emits EVERY passed-through state's transform (ascending state
  order) so no product is skipped. Mirrors flora regen→object.
- **Effective-time accumulation is fixed (Dm1(a))** — `DecayAge` is a single continuous monotone
  accumulator in effective-time units; thresholds are in the same units (env-independent); env/storage
  acceleration shows up as a faster `DecayAge` advance, never as a moving threshold.
- **Multiplicative env coupling is fixed (Dm2(a))** — `effectiveRate = baseRate · accel(temperature,
  moisture) · StorageRateMult` and `DecayAge += elapsedTicks · effectiveRate`. The three terms compose
  multiplicatively; `StorageRateMult = 0` halts aging; `accel < 1` (cold+dry) preserves.
- **D2 — no hardcoded preservation/spoilage meta** — cold-storage, salting, etc. EMERGE: the storage
  rate multiplier is a value world injects and the env coupling is data (`accel` §6). There is **no**
  bespoke per-kind Go spoilage function and **no** hardcoded "fridge"/"salt" rule (D4/D10).
- **D4/D10 data-defined** — `baseRate`, the ordered states + thresholds, transform products, per-state
  supply, and `accel` are all `content/objects.yaml` `decay:` data; an unknown kind/item id is caught
  at load (`platform/config`), never in `Step`.
- **Discrete-state model is fixed (Q1)** — decay is ordered discrete states; the derived `State` index
  comes from `DecayAge` crossing the kind's thresholds (mirrors flora Stage). A continuous-quality model
  is NOT used.
- **Owner-agnostic, lot-granular (Dm4(a)/Dm5(a))** — `Step` operates on a flat lot set regardless of
  owner/location; lots never auto-merge; a kind's `{Tag:int}` inventory count is the sum over its live
  lots (the view world maintains). A dead agent's dropped lots keep decaying with no special case.
- **Read-only inputs** — `Step` never mutates `prev`, `inputs`, or `Rules`; `StateAt`/`SupplyAt`/
  `BaseRate`/`Accel` never mutate `State`/`Rules`.
- **Outcome-neutral until activated** — decay ships with empty `Rules` / no perishable lots placed so
  `Step` emits no transitions/transforms and leaves `DecayAge` unchanged; existing world goldens hold.
  Activation is a deliberate later phase with its own re-baseline (mirrors flora/climate M-staging).
- **No wall-clock / no global rand** — the cadence is `tick % N` (world's), not wall-clock; the env is
  injected, not read from a clock; decay advances by `elapsedTicks`, never `time.Now()`.

## Acceptance Criteria (testable)
- [ ] **Discrete state derived from DecayAge (Q1/Dm1/D9)** — `StateAt` returns the kind's state index
  for a given `DecayAge` (effective-time units) per the ordered thresholds; crossing a threshold changes
  the state with no stored state field on `Lot`; below the first threshold ⇒ state 0; at/above the last
  ⇒ terminal. Table-driven over below/at/above each threshold.
- [ ] **Effective-rate accumulation (Dm1/Dm2)** — over a step `DecayAge += elapsedTicks · baseRate ·
  accel · StorageRateMult`; a higher `baseRate` ages faster; doubling `elapsedTicks` doubles the
  advance; the `AgeDelta`/`TransitionDelta` carries the new ABSOLUTE `DecayAge`. Table-driven.
- [ ] **Env-coupled acceleration is multiplicative §6 (Q2/Dm2/Dm3)** — two lots differing only in
  `EnvInput.Temperature` (warm vs cold) reach different `DecayAge` after N steps; warm+wet ages faster,
  cold+dry slower (cold-storage emerges); the `accel` is loaded from the `decay:` block, compiled +
  evaluated via `engine/kernel/expr` (operands `temperature`/`moisture`), NOT hardcoded; `effectiveRate` is the
  product `baseRate · accel · StorageRateMult`.
- [ ] **Storage rate multiplier slows/halts decay (eng-locked/Dm2)** — a lot with `StorageRateMult < 1`
  ages slower than an identical lot at `1.0` over N steps; `0` halts aging entirely. Table-driven.
- [ ] **Transform on transition (Q3/D9)** — crossing into a state with a `transform` emits the product
  in `Transformed` (item + qty), the source lot is consumed/replaced, and crossing multiple states in
  one step emits every passed-through transform in ascending state order; the terminal `gone` state
  emits no transform and lands the source in `Gone` (the only mass removal). Table-driven.
- [ ] **Transform qty scales with lot Qty (Dm5)** — a lot of `Qty = n` transforming produces
  `transform[].qty · n` of the product; lots never auto-merge (a second `New`-placed lot of the same
  kind ages independently). Table-driven.
- [ ] **Per-state supply override (D9)** — `SupplyAt` returns the per-state override when present (stale
  food < fresh food), else the base supply; the override is data, not hardcoded. Table-driven.
- [ ] **Owner-agnostic, sorted-order determinism (Dm4/D12)** — `Aged`/`Transitioned`/`Transformed`/
  `Gone` are in ascending `ObjectID` order regardless of where each lot lives; the `inputs` map is
  consumed by sorted lot id; shuffling the `inputs` insertion order yields byte-identical deltas.
- [ ] **Determinism golden (D12)** — a fixed `(initial lots, EnvInput sequence, elapsedTicks, Rules)`
  over N steps yields a byte-identical digest of the lot set + per-step `StepDeltas`. A second run from
  a registry built from the same `content/objects.yaml` reproduces it (cross-process). First established
  **decay-off** (neutral), then re-baselined on activation.
- [ ] **Resume invariant** — capturing `State` at step T and resuming yields the same step-T+k state +
  deltas as running 0→T+k uninterrupted (data-contracts §5).
- [ ] **Missing EnvInput panics** — `Step` with a live lot absent from `inputs` panics (world-contract
  guard, mirrors flora), never silently skips a lot.
- [ ] **Decay-off neutrality** — with empty `Rules` a multi-step run emits zero transitions/transforms
  and leaves `DecayAge` unchanged; existing world goldens hold.
- [ ] **No wall-clock / no global rand / no forbidden import (D12 guard)** — grep guard: no `time`
  import for logic, no global `rand`; no `engine/env/climate`/`engine/world`/`engine/env/flora`/`engine/space/navmap`/
  `engine/agent`/`engine/mind/gates` import (only `core`, `expr`, `rng`).
- [ ] **No hardcoded constant / id (D10 guard)** — grep guard: no rate / threshold / kind-name /
  item-name string literal in logic; every constant/formula flows from `Rules`.

## Out of Scope
- *When* to call `Step` (the `tick % N` multi-rate cadence), the per-step RNG-fork derivation, sampling
  `climate.Moisture`/`Temperature` at each lot's position to build `EnvInput`, computing the per-lot
  `StorageRateMult` from the storage structure, enumerating the decayable-lot set (floor + every agent's
  inventory + storage; a dying agent's dropped lots included, Dm4(a)), and APPLYING `StepDeltas`
  (advancing DecayAge / replacing transformed lots / removing Gone in objects[]+inventory) →
  `engine/world` (sole object mutator, D12 apply phase). decay is a pure transform.
- The **storage-structure model** (what a cold cellar is, how it sets `StorageRateMult`) → `engine/world`
  + `content/objects.yaml` (a storage object_kind's tag/attribute). decay only reads the injected scalar.
- Parsing/validating `content/objects.yaml` `decay:` blocks (ordered states, ascending thresholds,
  transform/supply ids), compiling the `accel` §6 Formula into `Rules`, and cross-checking kind/item
  ids → `platform/config` (`content/schema/objects.schema.json`).
- Serialization wire format / Redis / SSE of the decay field → `platform/persist` + `data-contracts §8`
  (this module exposes the `Lot` set as the periodic-full source + `StepDeltas` as the delta source).
- The **lot-merge / age-averaging policy** (whether a freshly-produced lot merges into an existing one)
  → deferred (Dm5(a): NO auto-merge in P_m2). decay never merges lots.
- The **Craft action** consuming decayed materials, and any value/planner reaction to decay state →
  `engine/mind/actions` (P_m3) + `engine/mind/values`/`engine/mind/planner`.

## Open Questions
> None. `docs/materials.md §1` Q1–Q5 and the SPEC-surfaced mechanism choices **Dm1–Dm5 are all
> `RESOLVED: (a)`** (human-confirmed). This SPEC is finalized to those resolutions and re-decides
> nothing. (P_m3 Craft + P_m4 extraction are separate phases with their own SPECs — not this module.)

## Notes
- `Step` deliberately mirrors `flora.Step`'s and `climate.Step`'s "pure read → return delta → world
  applies" shape: it returns `(next, deltas)` and never writes objects[]/inventory, exactly as
  `flora.Step` returns `(next, deltas)` and `climate.Step` returns `(next, transitions)`. This keeps
  `world` the single object mutator (D12 apply phase). The DecayAge→State derivation (Dm1(a)) matches
  flora's Length→Stage and climate's continuous-state→threshold-transition shape.
- The `EnvInput.{Temperature, Moisture}` shape is byte-identical to `climate.CellState`'s exported
  fields ([0,1] floats), and the `accel` §6 Context operand names (`temperature`, `moisture`) match the
  climate/flora Context vocabulary — decay reads the climate OUTPUT SHAPE as a value, with no climate
  import (mirrors flora's `SiteInput.Moisture/Temperature`). world samples climate per lot position.
- Tuning + behavior live in `content/objects.yaml` `decay:` (D10). Adding a perishable, changing a
  spoilage curve, or adding a transform product is a content change, never a code change — the §6 DSL +
  the ordered-states data are the extension seam (D2/D3: salting/cold-storage/preservation must emerge).
- Reference paths: `docs/materials.md` (Q1–Q5 + Dm1–Dm5 all RESOLVED), `docs/design.md §5/§9`,
  `backend/engine/env/flora/SPEC.md` (the structural template), `backend/engine/env/climate/SPEC.md` (the env
  output shape decay consumes as a value), `backend/engine/kernel/expr/SPEC.md` (the §6 evaluator decay owns +
  references, Dm3(a)), `docs/data-contracts.md §1/§8` (the lot inventory shape + the periodic-full +
  delta serialization), `docs/glossary.md` (`Moisture`/`Temperature`/`Effect`/`Tag`/`decay`/`accel`).
```
