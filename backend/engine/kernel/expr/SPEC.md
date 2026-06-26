# SPEC — `engine/kernel/expr`

> Status: `DRAFT`
> Leaf level: `L0`  ·  Owner agent: `<filled by implementer>`

## Purpose
The **single shared §6 `Formula` evaluator** (`docs/design.md §6` line 89–90, binding): a pure,
deterministic evaluator over a compiled, immutable **AST** `Program` against an abstract `Context`
the caller supplies — arithmetic `+ - * /` → numeric, comparison `> < >= <= == !=` → boolean,
logical `& | !` → boolean, plus boolean predicate calls (`has`/`isOwner`/`paid`). It is the *one*
evaluator the whole engine shares (`gates`' `GateExpr` is its boolean subset; `climate`/`flora`/
`actions`/`economy` evaluate compiled `Program`s) — a second §6 evaluator is forbidden (glossary
"one shared evaluator"). It is a **dumb** evaluator: it knows nothing of plan/apply phases, of
`ToM` vs real, or of RNG (chance→roll is the caller's, with injected RNG). It depends only on
`core` (no `stats`, no `rng`), so L1 leaves (`climate`, `flora`) reuse it without a DAG break.

## Public Interface
<The *only* contract other modules depend on. `platform/config` produces a `Program` from formula
text; engine modules build a `Context` and call `EvalNumber`/`EvalBool`. Nothing reads the AST node
types except `platform/config` (which constructs them) — siblings hold `Program` opaquely.>

```go
package expr

import "github.com/dogring/bdg/engine/kernel/core"

// ── Value (RESOLVED #7: explicit sum type, NO implicit num↔bool coercion) ─────────

// Kind tags a Value as numeric or boolean. There is NO implicit coercion between them:
// arithmetic operands must be Num, logical/comparison-result operands must be Bool — enforced
// at compile time by static typing (#5). A bool is never 0.0/1.0 and a number is never truthy.
type Kind uint8

const (
    KindNum Kind = iota // float64 payload in Num
    KindBool            // bool   payload in Bool
)

// Value is the result of evaluating a Program (or any node). Exactly one payload is meaningful,
// selected by Kind. Callers normally use EvalNumber/EvalBool instead of reading Value directly.
type Value struct {
    Kind Kind
    Num  float64 // valid iff Kind == KindNum
    Bool bool    // valid iff Kind == KindBool
}

// ── Context (RESOLVED #1: typed methods; (b)+(c) — expr DECLARES, callers ADAPT) ──

// Context is the abstract operand/predicate channel. expr DECLARES this interface; each caller
// (gates' AgentSnapshot, climate's CellState, flora's SiteInput+Plant, economy's portal view)
// implements it as a thin ADAPTER over its own snapshot. expr never knows which channel it is —
// plan-time callers build a ToM[self]-backed Context (→ boolean attempt-gates, D8), apply-time
// callers build a real-backed Context (→ numeric chance/qty). The caller swaps the channel; expr
// returns only numbers/bools, never rolls (D12). All three methods are READ-ONLY and must be pure
// for a given snapshot (same Context state ⇒ same answer) so eval stays deterministic.
type Context interface {
    // Stat resolves a subject stat id (STR, AGI, …) to its value. A StatID a Program references
    // is guaranteed to exist (validated at load, D10), so this returns a plain float64.
    Stat(id core.StatID) float64

    // Attr resolves a target/environment attribute named as a core.Tag (terrain.depth,
    // door.lockStrength, plant.length, moisture, …). ok=false means "this attribute is not
    // present on this subject" — the caller decides absence semantics; expr applies the fixed
    // deterministic policy for a missing operand (see Invariants), it never panics.
    Attr(name core.Tag) (val float64, ok bool)

    // Pred resolves a boolean predicate call by NAME + its Tag argument (has(itemID), paid(toll),
    // or the arity-0 bare isOwner). The predicate name + arity were checked against the known-
    // predicate table at load (#3), so an unknown name can never reach here; an arity-0 predicate is
    // called with arg="" (OQ-B). New predicates are added by a caller implementing this method for the
    // new name + registering it in the table (no core edit, D10). Returns the predicate's truth.
    Pred(name string, arg core.Tag) bool
}

// ── Program (RESOLVED #2: immutable AST; built+validated by platform/config) ──────

// Program is a compiled, immutable §6 formula: an opaque handle over an AST node tree. It is
// produced ONLY by platform/config's Parse step (engine modules receive it inside their compiled
// Rules and never build it). It carries its statically inferred result Kind (see ResultKind) and
// the set of identifiers it reads (see Reads), both fixed at compile time. A Program is safe to
// evaluate concurrently against different Contexts (it is read-only; eval allocates no shared
// state) — supporting the parallel plan phase.
type Program struct{ /* opaque: root AST node + inferred ResultKind + cached Reads(). Immutable. */ }

// ResultKind is the statically inferred result type of the whole Program (#5). platform/config
// asserts it matches the call site's required type at load: a gate/condition context requires
// KindBool, a cost/suitability/chance/qty context requires KindNum. A mismatch is a LOAD failure
// (D10), never a runtime error.
func (p *Program) ResultKind() Kind

// Reads returns the union of subject StatIDs this Program references, in sorted order, for
// load-time validation, snapshot pre-fill, and golden introspection (mirrors gates.Registry.Reads).
// Deterministic: same Program ⇒ identical slice (sorted, de-duplicated). It does NOT report Attr
// names or predicate names (those are caller-owned namespaces); use ReadsAttrs/ReadsPreds for those.
func (p *Program) Reads() []core.StatID

// ReadsAttrs returns the union of attribute names (terrain.depth, moisture, …) the Program
// references via Attr, sorted, so platform/config can cross-check them against the caller's
// operand vocabulary at load (climate moisture/temperature, flora terrain attrs). Deterministic.
func (p *Program) ReadsAttrs() []core.Tag

// ReadsPreds returns the union of predicate names (has, isOwner, paid, …) the Program calls,
// sorted, so platform/config can cross-check them against the known-predicate table (#3) at load.
// Deterministic.
func (p *Program) ReadsPreds() []string

// ── Runtime entry points (RESOLVED #5: split by required result type) ─────────────

// EvalNumber evaluates a numeric Program against ctx and returns the scalar. Calling it on a
// Program whose ResultKind is KindBool is a PROGRAMMING error (the load-time type check should
// have rejected wiring a boolean Program into a numeric site); guarded by the compile-time kind
// assertion, so in correct wiring it always sees a numeric Program. Pure, deterministic, no RNG.
// Arithmetic edge cases (div-0, NaN) follow the fixed deterministic policy below (no error return,
// D12). Domain clamping ([0,1] etc.) is the CALLER's, not expr's.
func (p *Program) EvalNumber(ctx Context) float64

// EvalBool evaluates a boolean Program against ctx and returns the verdict. Calling it on a
// numeric Program is the symmetric programming error, prevented by the load-time kind assertion.
// Pure, deterministic, no RNG. (climate Rules conditions and gate predicates use this; flora
// suitability/rates use EvalNumber.)
func (p *Program) EvalBool(ctx Context) bool

// ── Parse (RESOLVED #2/#3/#5/#6: platform/config is the ONLY producer of a Program) ──

// KnownPred describes one registered predicate's signature for load-time arity/name checking (#3).
// expr ships the §6 base table — `has`/`paid` arity 1 over a Tag, `isOwner` arity 0 (OQ-B RESOLVED:
// a BARE predicate, no parentheses, as written in the §9 portal formula `… | isOwner`). A caller
// adding a new predicate passes an EXTENDED table to Parse so its new name validates — and implements
// Context.Pred for that name (no core edit, D10).
type KnownPred struct {
    Name  string // predicate identifier as written in the formula (e.g. "has", "isOwner")
    Arity int    // number of Tag arguments: 0 for a bare predicate (isOwner), 1 for has/paid
}

// BasePreds is the §6 base predicate table: has (arity 1), isOwner (arity 0), paid (arity 1).
// platform/config passes this (optionally extended by a caller's own predicates) as the known set
// to Parse. An arity-0 predicate is evaluated via Context.Pred(name, "").
func BasePreds() []KnownPred

// Parse compiles §6 formula text into an immutable Program and performs ALL static validation
// (RESOLVED #2/#3/#5/#6 — everything catchable at load is caught here, never at eval):
//   • lexes/parses the §6 grammar with FIXED operator precedence (D12): unary ! ; * / ; + - ;
//     comparisons > < >= <= == != ; logical & ; logical | (parentheses override). The ONLY unary
//     operator is `!` (OQ-C RESOLVED): there is NO unary minus, and numeric literals are non-negative
//     (write `0 - x` for negation). Format is fixed; identifier names are free.
//   • static type inference per node (#5/#7): arithmetic requires Num operands and yields Num;
//     comparison requires Num operands and yields Bool; logical &|! require Bool operands and
//     yield Bool; Stat/Attr are Num; a Pred call is Bool; a numeric literal is Num. There is NO
//     num↔bool coercion — a type clash (e.g. `moisture & 0.5`, or arithmetic on a predicate) is a
//     LOAD failure.
//   • operand classification + identifier validation (D10, OQ-A RESOLVED — case-based): a `name(args)`
//     form is a Pred. A BARE token is classified: a known arity-0 predicate (isOwner) → Pred; else
//     UPPERCASE-INITIAL → Stat; else (lowercase-initial) → Attr. A DOTTED/colon token (terrain.depth,
//     tool:cutting.quality) is always an Attr. Every Stat token must be in knownStats and every
//     predicate NAME+arity in knownPreds — an undefined Stat / undefined predicate / wrong arity is a
//     LOAD failure (the error names the offending identifier). Attr names are a caller namespace
//     (recorded via ReadsAttrs for the caller's own cross-check; Parse has no fixed attr vocabulary).
//     CONVENTION (enforced by this rule): stat IDs are uppercase-initial (PascalCase, e.g. Strength,
//     Dexterity — see content/stats.yaml); attribute operands are lowercase-initial or dotted.
//   • result-kind assertion: the inferred whole-Program ResultKind must equal want (KindBool for a
//     gate/condition site, KindNum for a cost/suitability/chance site). A mismatch is a LOAD failure.
// On success it returns a Program safe to evaluate. On any violation it returns a descriptive error
// (no partial Program). Parse is the SOLE Program constructor; engine modules never call it.
func Parse(text string, want Kind, knownStats StatSet, knownPreds []KnownPred) (*Program, error)

// StatSet is the read-only set of valid StatIDs platform/config supplies (built from the loaded
// stats registry) so Parse can reject an undefined stat at load. expr declares the minimal shape
// it needs; platform/config adapts the stats registry to it (mirrors the Context (c) pattern).
type StatSet interface {
    Has(id core.StatID) bool
}
```

> `Parse` takes formula TEXT + the validation sets, never a file path — the engine and this L0 leaf
> perform **no filesystem IO**. `platform/config` reads `content/*.yaml`, extracts each formula
> string, builds `knownStats`/`knownPreds`, calls `Parse`, and stores the resulting `Program` inside
> the consuming module's compiled `Rules` (`climate.Rules`, `flora.Rules`, the gate `expr` tree).

## Dependencies
- `engine/kernel/core` — `StatID`, `Tag` (operand/predicate-argument identifier types). Nothing else.
- *(NOT `engine/mind/stats`)* — expr never imports `stats`; the valid-StatID set arrives as the abstract
  `StatSet` interface (platform/config adapts the `stats.Registry` to it). This is what keeps expr
  at L0 so L1 leaves (`climate`, `flora`) can depend on it without a DAG break.
- *(NOT `engine/kernel/rng`)* — expr does NO randomness. chance→roll lives in the caller (injected RNG);
  expr returns only the numeric chance/qty (D12).
- *(NOT `engine/mind/gates`)* — the reverse: `gates` is the consumer (its `GateExpr` is the boolean
  subset; staged unification below). expr must not import any consumer.
- **Contract**: there is no `content/expr.yaml`; expr's "data" are the formula STRINGS embedded in
  each consumer's content (`content/climate.yaml` `when:`, `content/objects.yaml` `flora:` formulas,
  `content/gates.yaml` `expr` — once unified). `platform/config` owns reading those + calling `Parse`.

## Owned Data
- The `Value`/`Kind` value types, the `Program` AST (opaque; root node + inferred `ResultKind` +
  cached `Reads`/`ReadsAttrs`/`ReadsPreds`), and the `KnownPred`/`StatSet` shapes. A `Program` is
  **immutable after `Parse`** — no setter, no exported AST node; returned introspection slices are
  copies. Consumers (climate/flora/gates) hold `Program` values inside their own `Rules` and must
  not attempt to mutate or reconstruct them.
- `Context`, `StatSet` are **borrowed, read-only** interfaces the caller implements and owns; expr
  never mutates them and never retains a reference past an `Eval*` call.

## Invariants
- **D12 determinism** — `EvalNumber`/`EvalBool` are pure functions of `(Program, Context)`. No
  `time.Now()`, no wall-clock, no global rand, **no RNG of any kind**. Same `Program` + same
  `Context` state ⇒ identical result, every run, every process. A `Program` is concurrency-safe to
  evaluate (read-only), supporting the parallel plan phase.
- **D12 no map-iteration for logic** — eval walks the fixed AST tree in fixed child order; any
  internal set (Reads union, predicate table) is iterated by sorted key. `Reads`/`ReadsAttrs`/
  `ReadsPreds` return sorted, de-duplicated slices. Float accumulation order is fixed by the AST
  (operator precedence + left-to-right within a precedence level).
- **Fixed operator precedence (D12)** — `!` > `* /` > `+ -` > comparisons > `&` > `|`, parentheses
  override. The grammar is fixed; identifier names are free (D10). Precedence is decided at `Parse`,
  baked into the tree, never re-decided at eval.
- **No implicit coercion (#7)** — `Value` carries an explicit `Kind`; arithmetic operates only on
  `KindNum`, logical/comparison only on the kinds their static type rule allows. A boolean is never
  treated as 0.0/1.0 and a number is never truthy. Coercion clashes are caught at load, so eval
  never sees an ill-typed node.
- **Identifier/type errors are LOAD failures, not runtime (#6, D10)** — an undefined StatID, an
  undefined/wrong-arity predicate, or a type clash makes `Parse` fail. By the time `Eval*` runs the
  `Program` is well-typed and all identifiers resolve, so the deterministic tick (D12) carries **no
  error branch** — `Eval*` returns a plain `float64`/`bool`, not `(_, error)`.
- **Fixed deterministic arithmetic-edge policy (#6, D12)** — runtime arithmetic edges resolve to a
  FIXED value, never NaN/Inf and never an error: **division by zero → 0**; any operation that would
  produce NaN → 0; the result is always a finite `float64`. A **missing operand** (`Attr` returns
  `ok=false`) resolves to **0** (numeric) — the caller owns presence semantics if it needs other
  behaviour. expr applies **no domain clamp**; clamping into `[0,1]` (or any range) is the CALLER's
  job (climate/flora already clamp). This keeps the determinism path branch-free and total.
- **Read-only inputs** — `Eval*` never mutates `Context`, `Program`, or any argument; `Parse` never
  mutates `StatSet`/`knownPreds`. `Context` methods are required to be pure for a given snapshot.
- **No IO (architecture §1)** — imports no `os`/`net`/filesystem/`time` package. `Parse` consumes a
  string, not a path; file reading + JSON-schema validation are `platform/config`'s.
- **gates byte-identity (staged, #4)** — expr's leaf/composite/comparison semantics MUST be
  byte-identical to gates' current `GateExpr` eval (`backend/engine/mind/gates/eval.go`: `evalExpr` +
  `cmpOp` — short-circuit but side-effect-free `and`/`or`/`not`, the six comparison ops, leaf truth)
  so the later swap is golden-neutral. **This SPEC does NOT migrate gates** — it must not touch the
  P3 gates golden (`testdata/golden/p3_gates.json`) or `gates.schema.json` (schema_version 3). The
  unification is a separate, deliberate later phase (`docs/expr.md §2 Pxb`).

## Acceptance Criteria (testable)
- [ ] **Arithmetic → numeric (#5/#7)** — `Parse("STR*0.5 + AGI*0.3", KindNum, …)` then `EvalNumber`
  over a `Context` stub yields `STR·0.5 + AGI·0.3` to float tolerance; precedence is correct
  (`* /` before `+ -`, parentheses override). Table-driven over several formulas.
- [ ] **Comparison/logical → boolean (#5/#7)** — `Parse("(STR*0.5 + AGI*0.3 > 0.5) | (AGI >
  terrain.depth)", KindBool, …)` then `EvalBool` returns the §6 example's truth for stub Contexts;
  `& | !` short-circuit but are side-effect-free (result independent of order). Table-driven.
- [ ] **Predicate calls via Context.Pred (#3)** — `Parse("has(key) | STR > door.lockStrength |
  paid(toll) | isOwner", KindBool, …, BasePreds())` evaluates each predicate through `Context.Pred`
  with the written Tag argument; flipping a stub `Pred` answer flips the result. (The §9 portal
  access formula.) Table-driven.
- [ ] **Undefined identifier → load failure (D10)** — `Parse` errors on a formula naming a StatID
  absent from `StatSet`, on an unknown predicate name, and on a wrong-arity predicate call; the
  error names the offending identifier; no `Program` is returned. Table-driven.
- [ ] **Type clash → load failure (#5/#7)** — `Parse` errors on `moisture & 0.5` (logical over a
  numeric), on arithmetic over a predicate (`has(key) + 1`), and on a `want=KindBool` site given an
  arithmetic-result formula (and the symmetric `want=KindNum` over a boolean formula). Table-driven.
- [ ] **ResultKind + want assertion (#5)** — a numeric formula has `ResultKind()==KindNum` and
  parses only under `want=KindNum`; a boolean formula `KindBool` under `want=KindBool`; the mismatch
  is the load failure above (gate site=bool, cost/suitability site=num).
- [ ] **Div-0 / NaN policy (#6, D12)** — `EvalNumber("1 / x")` with `x=0` returns `0` (not Inf/NaN);
  any NaN-producing op returns `0`; the result is always finite. A missing operand (`Attr` ok=false)
  contributes `0`. No panic, no error. Table-driven.
- [ ] **No domain clamp by expr (#6)** — a formula evaluating to `1.7` returns `1.7` (expr does NOT
  clamp to `[0,1]`); the caller is responsible for clamping (documents the climate/flora contract).
- [ ] **Reads / ReadsAttrs / ReadsPreds introspection (#2)** — for a mixed formula the three return
  exactly the referenced StatIDs / attr names / predicate names, each sorted + de-duplicated;
  identical across repeated calls and a second `Parse` of the same text (golden introspection).
- [ ] **gates leaf-eval byte-identity (#4 — parallel check, gates untouched)** — a test re-expresses
  each shipped gate predicate (`capability_floor`, `knowledge`, base `conscience`, `stamina`,
  `apathy`, `adrenaline`'s expr) as an `expr.Program` over an adapter Context and asserts EvalBool
  matches `gates.evalExpr` for a battery of `AgentSnapshot`s — **without** importing gates into expr
  or modifying gates (the test lives outside expr's package boundary). Proves the swap is
  golden-neutral before any migration. (This AC documents Pxb's readiness gate; it does not migrate.)
- [ ] **Determinism golden (D12)** — a fixed set of `(formula, Context-stub sequence)` over numeric
  + boolean programs yields a byte-identical digest of results; a second `Parse`+eval of the same
  text reproduces it (cross-process). No `time`/global-rand/RNG anywhere.
- [ ] **Read-only inputs** — a property test confirms the `Context` stub and `Program` are unchanged
  after `Eval*`; `StatSet`/`knownPreds` unchanged after `Parse`.
- [ ] **No IO / no RNG / no forbidden import (guard)** — grep guard: no `os`/`net`/filesystem/`time`
  import, no `rand`, no `engine/kernel/rng`, no `engine/mind/stats`, no `engine/mind/gates` import in `engine/kernel/expr`.
- [ ] **Concurrent eval safe (plan phase)** — a `Program` evaluated from many goroutines against
  distinct Contexts produces consistent results with `-race` clean (read-only eval).

## Out of Scope
- **Reading `content/*.yaml`, JSON-schema validation, extracting formula strings, building
  `StatSet`/`knownPreds`, and calling `Parse`** → `platform/config`
  (`backend/platform/config`). It is the sole `Program` producer; it stores each `Program` inside
  the consuming module's compiled `Rules` (climate/flora) or the gate `expr` tree. expr only
  *compiles + evaluates*; it never touches a file.
- **The chance→success roll** (turning a numeric `Eval` chance into a hit/miss) and any RNG draw →
  the calling action/world layer with the injected `*rng.RNG` (D12). expr returns the number only.
- **Domain clamping** (`[0,1]` for moisture/suitability, stat bounds) → the caller (climate/flora
  already clamp). expr emits raw numbers under the fixed div-0/NaN policy.
- **gates MIGRATION** (lifting `GateExpr`/`Op`/leaf-eval onto expr, re-baselining
  `p3_gates.json` + `gates.schema.json`) → a deliberate **later phase** (`docs/expr.md §2 Pxb`).
  This SPEC ships expr standalone with a byte-identity *check* only; it does not touch P3 gates.
- **Wiring climate/flora's `Program` compilation into `platform/config`** → `platform/config` +
  the climate/flora SPECs (`docs/expr.md §2 Pxc`). expr provides `Parse`; the wiring is config's.
- **New predicate IMPLEMENTATIONS** (`has`/`isOwner`/`paid` over a real inventory/ownership view,
  any economy/portal predicate) → the caller's `Context.Pred` adapter + its `knownPreds` extension
  (`docs/expr.md §2 Pxd`). expr ships the table shape + `BasePreds()`; it implements no predicate.
- **The `Context` ADAPTERS** themselves (gates `AgentSnapshot`, climate `CellState`, flora
  `SiteInput`+`Plant`, economy portal view) → each consumer module. expr only DECLARES `Context`.

## Open Questions
> `docs/expr.md §1` is ALL RESOLVED (7/7 adopted-as-`rec`); this SPEC writes from those resolutions
> and re-decides nothing. The items below are NEW seams surfaced while writing the SPEC — none
> re-opens a resolved §1 item, none blocks Pxa (the core), and each is flagged to its later phase.

### Resolved during Pxa implementation (human-confirmed 2026-06-26)
- **OQ-A `RESOLVED: case-based operand classification` (blocking, now folded into the Parse contract).**
  A bare token → arity-0 known predicate (isOwner) ⇒ Pred; else uppercase-initial ⇒ Stat (validated
  against knownStats, undefined ⇒ LOAD failure); else lowercase-initial ⇒ Attr (caller namespace,
  unvalidated). Dotted/colon token ⇒ always Attr. Backed by content/stats.yaml (all stat IDs
  PascalCase) + climate/flora operands (all lowercase/dotted). Convention now enforced + documented
  above; mirror it in `docs/glossary.md` (stat IDs uppercase-initial, attrs lowercase/dotted).
  (Rejected: all-bare=Stat — breaks bare `moisture`; knownStats-first — undefined stat silently
  becomes a 0-valued Attr, defeats the load-failure AC.)
- **OQ-B `RESOLVED: isOwner is arity 0` (bare predicate).** `BasePreds() = {has:1, isOwner:0, paid:1}`;
  an arity-0 predicate is written bare (`… | isOwner`, §9 portal) and evaluated via `Context.Pred(name,
  "")`. The earlier "each arity 1" SPEC phrasing was wrong (AC formula + glossary are ground truth).
- **OQ-C `RESOLVED: no unary minus` for Pxa.** The only unary operator is `!`; numeric literals are
  non-negative (write `0 - x` for negation). Revisit only if a negative-literal need is actually authored.

- **gates schema/golden re-baseline at the swap (Pxb — flag to gates owner; NOT blocking Pxa).**
  The byte-identity AC proves expr can reproduce `gates.evalExpr`, but the actual migration
  (import expr into gates, drop the duplicate `GateExpr`/`cmpOp`, possibly bump
  `gates.schema.json` schema_version 3 → 4 if the on-disk leaf grammar shifts to share expr's
  parser) is a deliberate re-baseline of `testdata/golden/p3_gates.json`. **Decision needed before
  Pxb: does gates keep its own on-disk YAML leaf shape (and only swap the eval engine), or move to
  expr's textual formula syntax (a content + schema migration)?** Recommend swap-the-engine-only
  first (golden-neutral), syntax migration later. Return to the gates owner/human before Pxb.
- **Attr-name vocabulary ownership (Pxc — non-blocking).** `ReadsAttrs` reports the attr names a
  Program references, but expr has no fixed attr vocabulary (it is a per-caller namespace:
  climate `moisture`/`temperature`, flora terrain attrs, portal `door.lockStrength`). Each
  consumer's `platform/config` step must cross-check `ReadsAttrs()` against ITS own operand set at
  load (D10) — there is no single global attr registry. Document the chosen cross-check per consumer
  in Pxc; do not centralize attr names into core/expr (they are caller data). Non-blocking.
- **Predicate-argument richness (Pxd — non-blocking).** The §6 base predicates (`has`/`isOwner`/
  `paid`) are arity-1 over a `core.Tag`. If a future predicate needs a numeric/literal argument
  (e.g. `withinHours(3)`) the `KnownPred`/`Context.Pred` arity-1-Tag shape would need extending.
  Recommend keeping arity-1-Tag for Pxd (covers the §9 portal seam) and revisiting the signature
  shape only when a multi/typed-arg predicate is actually authored. Flag to the economy/portal owner
  before adding such a predicate. Non-blocking.

## Notes
- expr deliberately mirrors gates' "compile once, evaluate many" + introspection shape: `Program`
  ↔ the parsed `GateExpr` tree, `Reads()` ↔ `gates.Registry.Reads()`, `ResultKind` ↔ the gate's
  implicit boolean type. The staged unification (#4/Pxb) is exactly: make these byte-identical, then
  swap gates onto expr without churning its golden.
- The `Context` (b)+(c) split (typed `Stat`/`Attr`/`Pred` methods, declared by expr, implemented by
  each caller as an adapter) is what lets `Parse` do real static type checking: a single
  `Lookup(string)` would lose the predicate argument (`has(itemID)`) and the operand kind
  (num vs bool), defeating the compile-time check #5 needs (`docs/expr.md §1 #1`).
- Why an AST (#2) over a closure or bytecode VM: the AST gives `platform/config` load-time identifier
  validation (D10), `Reads`/golden introspection, and natural gates-tree compatibility (#4); a
  closure would lose introspection, a bytecode VM is over-engineering for an L0 leaf
  (`docs/expr.md §1 #2`).
- The fixed div-0→0 / no-NaN policy (#6) is what keeps the determinism path total and branch-free —
  there is no error/panic branch inside a deterministic tick (D12). Domain meaning (clamp to [0,1])
  stays with the caller, which already owns it (climate/flora clamp; gates/cost have their own
  bounds).
- Reference paths: `docs/design.md §6` (line 89 evaluator home + line 90 eval model/Context, both
  binding), `docs/expr.md` (the §1 resolutions this SPEC implements + the §2 phases),
  `docs/glossary.md` (`Formula`/`GateExpr` — "one shared evaluator (engine/kernel/expr, L0)"),
  `docs/architecture.md` (expr = L0, `core`-only, build stage 1),
  `backend/engine/env/climate/SPEC.md` + `backend/engine/env/flora/SPEC.md` (the two consumers that already
  assume `expr.Program` compiled from content + evaluated against an `expr.Context`).
