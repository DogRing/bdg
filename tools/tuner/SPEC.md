# SPEC — `tools/tuner`

> Status: `DRAFT`
> Leaf level: `tool` (standalone CLI; NOT an engine simulation module — lives outside `backend/engine/`)
> Owner agent: `<filled by implementer>`

## Purpose

A standalone, headless **balance auto-tuning CLI**. It searches `content/balance.yaml`'s seven
emergence-critical constants for a value set that drives the simulation into a target band of four
**emergence metrics** (crime rate, faction-reputation variance, role convergence, provisioning
gap), the future tool that `docs/core/testing.md §5` explicitly defers. It evaluates each candidate by
running the deterministic engine (`engine/world`) for a fixed seed set, scores the emitted per-tick
event stream against the metric targets, and writes a recommended `best_params.yaml` plus an
auditable `results_table.csv`. It writes **no engine code** and never mutates the simulation; it is
a search loop around the engine's existing public interface.

## Public Interface

### CLI invocation

```
go run ./tools/tuner [flags]
go build -o tuner ./tools/tuner && ./tuner [flags]
```

| Flag | Type | Default | Meaning |
|------|------|---------|---------|
| `-content` | string | `./content` | Content dir loaded via `platform/config.Load` (the base `balance.yaml` the tuner overlays). |
| `-ticks` | int | `1440` | Game-ticks each candidate is simulated (1440 = 1 game-day). |
| `-workers` | int | `0` | Parallel candidate workers; `0` → `runtime.NumCPU()`. Each worker owns its own engine + RNG. |
| `-samples` | int | `2000` | Phase-1 Latin-Hypercube sample size drawn from the grid lattice (≤ the full grid). |
| `-out` | string | `./results` | Output directory (created if absent), relative to the invocation CWD. |
| `-cmaes` | bool | `true` | Enable Phase-2 CMA-ES fallback when no Phase-1 sample passes all four metrics. |
| `-cmaes-budget` | int | `400` | Max candidate evaluations Phase 2 may spend before giving up. |
| `-seeds` | string | `1,2,3` | Comma-separated seed list (FIXED default `1,2,3`; overridable for experiments, but the role-convergence metric is DEFINED over exactly seeds 1,2,3 — see Invariants). |
| `-resume` | bool | `true` | Skip candidates already present (by param-tuple key) in an existing `results_table.csv`; append new rows. |
| `-seed` | int64 | `0` | Seed for the SAMPLER/CMA-ES proposal RNG (the search seed; distinct from engine seeds). `0` → fixed default so the candidate sequence is reproducible. |

The tuner exits `0` if at least one candidate passes all four metrics (best written to
`best_params.yaml`), `1` if none passed, and `2` on a fatal setup error (content load failure, bad
flag). Per-candidate panics/timeouts are NOT fatal (see Invariants — Failure isolation).

### Go package public API

```go
package tuner

// Constant is one tunable knob: a dotted balance.yaml key path and its grid lattice.
type Constant struct {
    Key  string  // dotted path into balance.yaml, e.g. "gossip.alpha"
    Min  float64
    Max  float64
    Step float64
}

// Candidate is one fully-specified assignment of all seven constants (the search-space point).
// It is the resume key: two Candidates are equal iff every Value is bit-identical.
type Candidate struct {
    Values map[string]float64 // Constant.Key -> chosen value (the seven keys below)
}

// Metrics are the four emergence statistics scored for a Candidate (averaged/combined across seeds
// per each metric's own rule — see "Owned Data / metric semantics").
type Metrics struct {
    CrimeRate                  float64 // mean over seeds of (theft+attack ticks / total ticks)
    FactionReputationVariance  float64 // variance of per-faction mean ToM[C] across agents
    RoleConvergence            bool    // RoleEmerged fired in ALL of seeds 1,2,3
    ProvisioningGap            float64 // low-Intel starvation rate / high-Intel starvation rate
}

// Result is one evaluated row: the candidate, its metrics, pass/fail, and any error.
type Result struct {
    Candidate Candidate
    Metrics   Metrics
    Pass      bool   // all four metric targets met
    Err       string // non-empty ⇒ candidate was skipped (panic/timeout); Metrics/Pass meaningless
}

// Options bundles the CLI flags for the library entry point (so the tool is testable headless).
type Options struct {
    ContentDir  string
    Ticks       int
    Workers     int
    Samples     int
    OutDir      string
    CMAES       bool
    CMAESBudget int
    Seeds       []int64
    SearchSeed  int64
    Resume      bool
}

// RunTuner is the single library entry point the main() wraps. It loads content, builds the search
// space, runs Phase 1 (Latin-Hypercube over the grid) then conditionally Phase 2 (CMA-ES), writes
// results/best_params.yaml + results/results_table.csv, and returns the best passing Result (ok=false
// if none passed). It is deterministic for a fixed (Options, content): same inputs → same ranking
// and same files. It performs IO only under OutDir and reads only ContentDir.
func RunTuner(opt Options) (best Result, ok bool, err error)

// Evaluate runs ONE candidate end-to-end for the given seeds and returns its Metrics. It is the unit
// of parallelism (one engine instance per call, injected RNG per seed) and the unit of failure
// isolation (a panic inside is recovered and surfaced as Result.Err by the caller). Pure given
// (candidate, seeds, ticks, content): identical inputs → identical Metrics (no wall-clock, no global
// rand, no map-iteration in the scoring).
func Evaluate(reg *config.LoadOutput, cand Candidate, seeds []int64, ticks int) (Metrics, error)
```

> The tuner DEFINES no simulation vocabulary. `RoleEmerged`/`Event` are `engine/core`'s; `Take`,
> `Attack`, `Patrol`, `Vote` are `content/actions.yaml` ActionIDs (glossary); `ToM[C]` is
> `engine/tom`'s belief distribution; the seven knob names are `content/balance.yaml` key paths.

## Dependencies

By path only — the tuner consumes existing public interfaces; it reads no sibling implementation.

- `backend/platform/config` — `config.Load(contentDir) → *LoadOutput`; `LoadOutput.Balance`
  (the parsed `balance.yaml`), `LoadOutput.WorldConfig/AgentConfig/PlannerConfig/ValuesConfig/
  PerceptConfig/ClockConfig` accessors, the engine registries (`StatsRegistry`, `ActionsRegistry`,
  `NeedsRegistry`, `GatesRegistry`), and `ConfigHash()`. The tuner overlays its seven values onto
  an **in-memory copy** of the loaded balance, then re-derives the engine config structs from it.
- `backend/engine/world` — `world.New(cfg, clock, rootRNG, svc, actReg, emit)`, `(*World).Spawn`,
  `(*World).PlaceObject`, `(*World).Tick`, `(*World).AgentIDs`, `(*World).AgentOf`. The driver loop.
- `backend/engine/agent` — `agent.Services`, `agent.Config`, `(*Agent)` read accessors used by the
  scorer to read each agent's perceived Intelligence (for the provisioning-gap cohort split) and
  ToM[C] beliefs (for reputation variance), via the world's public `AgentOf`/`Snapshot` surface.
- `backend/engine/core` — `core.EventEmitter`, `core.Event` (the tuner injects a **collecting
  emitter** to capture `RoleEmerged` and action-start events per tick), `core.AgentID`, `core.Tick`.
- `backend/engine/tom` — `tom.Belief` (read-only) to read per-observer `ToM[C]` for the
  reputation-variance metric (D6 — reputation is the DISTRIBUTION, never a stored scalar).
- `backend/engine/rng`, `backend/engine/worldtime`, `backend/engine/spatial`,
  `backend/engine/perception`, `backend/engine/planner` — assembled exactly as `backend/main.go`
  does (`rng.New(seed)`, `worldtime.NewClock`, `spatial.New`, `perception.NewSensor`, `planner.New`)
  to build one engine instance per candidate run.
- **Contract — NOT imported / NOT mutated**: `content/balance.yaml` is READ as the base; the tuner
  never writes the original during a run (only `results/best_params.yaml` is a fresh file).
- Third-party: a CMA-ES implementation (a small vendored/pure-Go optimizer) for Phase 2; the
  standard library (`encoding/csv`, `gopkg.in/yaml.v3`, `runtime`, `sync`, `context`) otherwise.

## Owned Data

### The seven target constants (search space; dotted `balance.yaml` keys)

| # | Key | Engine meaning (read-only context) |
|---|-----|------------------------------------|
| 1 | `gossip.alpha` | ToM gossip update rate α (`engine/agent`/`engine/tom`). |
| 2 | `self_calibration.beta` | per-stat ToM[self] update rate β (D8). |
| 3 | `mood.lambda` | Mood sensitivity λ. |
| 4 | `gates.conscience_urgency_threshold` | urgency above which the conscience barrier lowers. |
| 5 | `adrenaline.trigger_urgency` | urgency that triggers the adrenaline surge. |
| 6 | `politics.vote_urgency_threshold` | distributed urgency above which a `Vote` is emitted. |
| 7 | `politics.vote_rely_threshold` | reliance strength that licenses a `Vote`. |

All seven keys already exist in `content/balance.yaml` (verified) — the tuner overwrites VALUES
only, never adds/removes keys, so the schema and every other module keep working unchanged.

### The four emergence metrics + targets (the scoring contract)

| Metric | Definition | Target |
|--------|------------|--------|
| `crime_rate` | fraction of all (agent × tick) action-starts that are a `Take` or `Attack` action, over the run; reported as the MEAN across seeds. | `[0.02, 0.08]` |
| `faction_reputation_variance` | variance of **per-faction mean `ToM[C]`** across agents — for each emergent faction (the agent-id clustering induced by reliance edges, D6: reputation is the distribution of per-observer beliefs, never a stored scalar), take the mean `ToM[C]` its members hold about subject(s), then take the variance of those per-faction means. | `> 0.001` |
| `role_convergence` | boolean: a `RoleEmerged` event fires at least once in EACH of seeds 1, 2, AND 3. | `true` (all three) |
| `provisioning_gap` | starvation rate among low-Intelligence agents (perceived Intel `< 0.4`) divided by starvation rate among high-Intelligence agents (perceived Intel `>= 0.4`). Starvation = an agent whose Satiety/Hydration satisfaction crosses the unmet setpoint at end of run (or an emitted starvation marker). | `> 2.0` |

A candidate **passes** iff all four targets hold simultaneously.

### Output files (under `-out`, default `./results/`)

- `best_params.yaml` — the recommended values for the seven constants in `balance.yaml` nesting
  (e.g. `gossip:\n  alpha: 0.12`), ready to splice into the real file by a human. Written ONLY at
  the end, ONLY if a passing candidate exists.
- `results_table.csv` — one row per evaluated candidate. Columns, in fixed order:
  `gossip.alpha, self_calibration.beta, mood.lambda, gates.conscience_urgency_threshold,
  adrenaline.trigger_urgency, politics.vote_urgency_threshold, politics.vote_rely_threshold,
  crime_rate, faction_reputation_variance, role_convergence, provisioning_gap, pass, error`.
  Header row written on first create; rows APPENDED on resume.
- stdout — one structured log line per evaluated candidate (`OK`/`FAIL`/`ERROR` + params + metrics),
  plus phase banners and a final summary.

The tuner owns the search-space lattice, the LHS sampler, the CMA-ES driver, the per-candidate
in-memory balance overlay, the scoring functions, and the CSV/YAML writers. It owns NO engine
state — each candidate's `*World` is created, run, and discarded inside `Evaluate`.

## Invariants

- **Engine determinism is preserved (NFR-1).** The tuner injects only seeded `*rng.RNG`
  (`rng.New(seed)`); it adds NO `time.Now()`, wall-clock, or global rand into any evaluation path.
  Same `(Candidate, seed, ticks, content)` → byte-identical engine run → identical `Metrics`.
- **Same candidate ranking for the same search seed.** Two full `RunTuner` invocations with the same
  `(Options, content)` produce the SAME set of evaluated candidates, the SAME per-candidate metrics,
  and the SAME ranked best (the LHS draw uses the fixed `SearchSeed`; CMA-ES uses the same seeded
  proposal RNG). Parallelism MUST NOT change the result (see below).
- **Parallelism is result-invariant.** Candidates are evaluated on a worker pool of size `-workers`;
  each worker builds its OWN engine instance and its OWN per-seed `*rng.RNG`. There is NO shared
  mutable state across workers (the base `*LoadOutput` is treated read-only; each candidate gets a
  deep-copied balance overlay). The candidate→metrics mapping is independent of which worker ran it
  or in what order results returned. Run `-race`-clean.
- **No map-iteration drives any decision (D12).** Metric scoring iterates agents via
  `world.AgentIDs()` (sorted), seeds via the ordered `-seeds` slice, and constants via the fixed
  seven-key order. Reduction order is fixed so floating-point sums are reproducible.
- **The original `balance.yaml` is never written during a run.** Each candidate mutates only an
  in-memory copy of the parsed `Balance`; the only file ever written back is `best_params.yaml`
  (a fresh file under `-out`). A run leaves `content/balance.yaml` byte-unchanged.
- **Resume is keyed by the param tuple.** On `-resume`, a candidate whose seven values bit-match a
  row already in `results_table.csv` is SKIPPED (not re-run); new candidates append. The skip key is
  the canonical fixed-precision string of the seven values (so float formatting round-trips exactly).
- **Failure isolation.** A candidate whose `Evaluate` panics or exceeds a per-candidate timeout is
  caught (recover + `context` deadline), logged as `ERROR` with the param tuple, recorded as a row
  with `pass=false` and a non-empty `error` column, and the run CONTINUES. One bad candidate never
  aborts the search.
- **Role-convergence metric is defined over seeds 1,2,3.** The `role_convergence` boolean requires
  `RoleEmerged` in ALL THREE of seeds 1,2,3 by definition (`docs/core/testing.md §5`). If `-seeds` is
  overridden, the tool still requires emergence in EVERY listed seed (the contract generalizes to
  "all seeds"), and logs a warning that the canonical metric uses 1,2,3.
- **No naming drift / no new vocabulary.** The seven keys, the four metrics, and the action names
  `Take`/`Attack`/`Patrol`/`Vote` use the existing glossary / `balance.yaml` / `actions.yaml`
  identifiers verbatim. The tool introduces no engine type and no balance key.
- **No simulation-design rule is touched.** The tuner does not hardcode a meta-system (D2), does not
  store reputation as a scalar (D6 — it reads the per-observer ToM distribution), and reads perceived
  Intelligence (ToM[self], D8) — never RealStats — for the provisioning-gap cohort split.

## Acceptance Criteria (testable)

- [ ] **Knob overlay round-trips (unit).** Given a base `Balance` and a `Candidate` setting all seven
  keys, the in-memory overlay produces a `Balance` whose seven fields equal the candidate values and
  whose every OTHER field is byte-identical to the base (table-driven over the seven keys; the
  original `*LoadOutput` is unmutated — a deep-copy guard).
- [ ] **`best_params.yaml` is valid balance nesting (unit).** The written YAML, re-parsed, yields the
  seven values under their correct nested keys (`gossip.alpha`, `self_calibration.beta`,
  `mood.lambda`, `gates.conscience_urgency_threshold`, `adrenaline.trigger_urgency`,
  `politics.vote_urgency_threshold`, `politics.vote_rely_threshold`) and contains no extra keys.
- [ ] **CSV schema + append (unit).** A fresh run writes the header then one row per candidate in the
  fixed 13-column order; a `-resume` run over the same `results_table.csv` writes NO header, skips
  every already-present param tuple, and appends only new rows. The `role_convergence` column
  serializes as `true`/`false`; `error` is empty for non-error rows.
- [ ] **Crime-rate metric (unit, fixture event stream).** Feeding a synthetic per-tick event stream
  with a known count of `Take`/`Attack` action-starts over a known (agent×tick) total yields the
  expected `crime_rate`; non-crime action-starts do not count; the result is the seed-mean.
- [ ] **Reputation-variance metric (unit, D6).** Given agents partitioned into ≥2 emergent factions
  with deliberately divergent per-observer `ToM[C]`, the metric returns the variance of the
  per-faction mean beliefs (`> 0.001` when factions disagree; `≈ 0` when all agree). A guard asserts
  the metric reads the ToM DISTRIBUTION and never a single stored reputation scalar.
- [ ] **Role-convergence metric (unit).** A `RoleEmerged` captured in all of seeds 1,2,3 → `true`;
  missing in any one seed → `false` (table-driven over the 2^3 presence combinations).
- [ ] **Provisioning-gap metric (unit, D8).** With low-Intel (perceived Intel `< 0.4`) agents
  starving at a higher rate than high-Intel (`>= 0.4`), the ratio is `> 2.0`; equal rates → `≈ 1.0`;
  zero high-Intel starvation is handled (no divide-by-zero — documented sentinel/large value). The
  cohort split reads perceived Intelligence (ToM[self]), not RealStats.
- [ ] **End-to-end determinism (golden).** `RunTuner` over a tiny lattice (e.g. 2 knobs × 2 points,
  `-ticks` small, seeds `1,2,3`) run twice produces byte-identical `results_table.csv` and identical
  `best`/`ok`. A second process reproduces it (cross-process determinism, `docs/core/testing.md §1`).
- [ ] **Parallelism is result-invariant (`-race`).** The same tiny lattice run with `-workers 1` and
  `-workers 4` produces identical `results_table.csv` content (modulo row ORDER, which is
  re-sorted by the fixed param-tuple key before comparison) and an identical `best`. Race detector
  reports no data race.
- [ ] **Failure isolation.** A candidate injected to panic (or to exceed a tiny per-candidate
  timeout) is logged `ERROR`, recorded with `pass=false` + non-empty `error`, and the run completes
  evaluating the remaining candidates and still writes outputs.
- [ ] **Phase-2 trigger (unit/integration).** When NO Phase-1 sample passes all four metrics and
  `-cmaes=true`, CMA-ES starts from the best (highest-scoring) grid point and runs ≤ `-cmaes-budget`
  evaluations; when at least one Phase-1 sample passes, Phase 2 is skipped. With `-cmaes=false`,
  Phase 2 never runs.
- [ ] **LHS bounded by `-samples`.** Phase 1 evaluates at most `-samples` distinct lattice points
  (≤ the full grid), each a valid Cartesian-lattice point within every constant's `[Min,Max]` step
  grid (no out-of-bounds or off-step value ever proposed).
- [ ] **Original balance untouched.** After a full run, `content/balance.yaml` is byte-identical to
  before (filesystem hash compare); only files under `-out` were written.
- [ ] **Exit codes.** `RunTuner`/`main` returns the documented codes: pass found → `0`/best written;
  none → `1`; fatal setup error → `2` (table-driven).

## Out of Scope

- **No engine changes.** The tuner adds no balance key, no event type, no engine code. If a metric
  needs a signal the engine does not emit (e.g. an explicit starvation marker), that is an
  `engine/world`/`engine/agent` SPEC change escalated to the architect — NOT done here (see Open
  Questions).
- **Writing tuned values into the live `content/balance.yaml`.** The tool only RECOMMENDS via
  `best_params.yaml`; splicing into the real file is a human/CI step.
- **Multi-objective Pareto fronts / weighted scalarization tuning UI.** P1 is pass/fail against the
  four fixed bands; richer objective shaping is a follow-up.
- **Distributed / multi-machine search**, GPU evaluation, surrogate models (Bayesian opt) —
  P1 is in-process worker-pool grid + CMA-ES only.
- **Live dashboard / SSE of tuning progress** — stdout logging + CSV only.
- **Tuning constants beyond the seven listed** — the search space is fixed to those keys; widening it
  is a SPEC change.

## Open Questions

- **Faction definition for `faction_reputation_variance` (may block the metric).** "Faction" is an
  EMERGENT cluster (D2 — no Faction type exists). P1 proposal: derive factions from the reliance-edge
  clustering the world already detects (agents sharing a `RoleEmerged` holder = a faction), or fall
  back to a deterministic clustering over the `ToM[C]` belief vectors. Confirm the clustering rule
  with the architect before locking the metric; flag if it needs a new world accessor. **Does not
  block the other three metrics.**
- **Starvation signal for `provisioning_gap`.** The engine does not currently emit an explicit
  "starved" event. P1 proposal: score starvation as "Satiety OR Hydration satisfaction below the
  `needs.*.satisfaction_threshold` at end of run" read via the world's public agent state. If a
  cleaner marker is wanted, that is an `engine/agent` SPEC change to escalate. Flag before finalizing
  the metric definition (`docs/core/testing.md §5` says metric defs are finalized late, P7).
- **CMA-ES on a discrete lattice.** CMA-ES is continuous; Phase 2 proposes continuous points within
  each `[Min,Max]` and either snaps to the nearest lattice step (keeping the discrete contract) or
  evaluates off-lattice (richer but breaks the LHS-grid framing). P1 proposal: snap to step. Confirm.
- **Per-candidate timeout value.** The failure-isolation deadline needs a default (proposal: a
  generous multiple of the median candidate wall-time, computed adaptively after the first N
  candidates). Not blocking; tune after first real runs.

## Notes

### Grid-search bounds per constant (the Phase-1 lattice — encode verbatim)

| Constant (`balance.yaml` key) | Min | Max | Step |
|-------------------------------|-----|-----|------|
| `gossip.alpha` | 0.04 | 0.24 | 0.04 |
| `self_calibration.beta` | 0.02 | 0.18 | 0.04 |
| `mood.lambda` | 0.10 | 0.50 | 0.10 |
| `gates.conscience_urgency_threshold` | 0.50 | 0.85 | 0.05 |
| `adrenaline.trigger_urgency` | 0.45 | 0.80 | 0.05 |
| `politics.vote_urgency_threshold` | 0.05 | 0.30 | 0.05 |
| `politics.vote_rely_threshold` | 0.20 | 0.60 | 0.10 |

- **The full grid is impractically large.** Point counts per constant are 6, 5, 5, 8, 8, 6, 5 →
  the full Cartesian product is `6×5×5×8×8×6×5 ≈ 115,200` points. At ~1 game-day per candidate × 3
  seeds, an exhaustive grid is infeasible. **Phase 1 therefore evaluates a Latin-Hypercube Sample of
  ≤ `-samples` (default 2,000) points DRAWN FROM the lattice** — every sampled value is a valid
  on-step lattice value, and the sample stratifies each constant's range. If no LHS point passes all
  four metrics, fall back to **Phase 2 CMA-ES** seeded from the best-scoring Phase-1 point.

### Search strategy

- **Phase 1 — Latin-Hypercube over the grid.** Stratify each of the seven constants into its
  lattice levels, draw a balanced LHS of `-samples` points (using the fixed `SearchSeed` so the draw
  is reproducible), evaluate each. Score and record every candidate. Stop early and report success if
  a passing candidate appears (but still complete recording for the audit trail / resume).
- **Phase 2 — CMA-ES (conditional).** Triggered ONLY when Phase 1 yields zero passing candidates and
  `-cmaes=true`. Initialize the CMA-ES mean at the best-scoring Phase-1 point, σ a fraction of each
  constant's range; propose continuous points, **snap to the nearest lattice step** (Open Questions),
  evaluate, and feed the metric "distance-to-target" as the fitness. Cap at `-cmaes-budget`
  evaluations. The fitness is a deterministic scalarization of the four metric gaps (documented at
  implementation; lower = closer to all four bands).

### Parallelism model

- A worker pool of `-workers` goroutines pulls candidates from a queue; each worker calls
  `Evaluate(reg, cand, seeds, ticks)` with its OWN freshly built engine per (candidate, seed) and a
  fresh `rng.New(seed)`. No worker shares mutable engine/balance state. Results stream back through a
  channel and are written to CSV by a SINGLE writer goroutine (CSV append is not concurrent). Because
  each candidate's metrics are a pure function of its inputs, the worker that happens to run it and
  the completion order do not affect the recorded metrics or the final ranking.
- For comparison/golden stability, results are SORTED by the canonical param-tuple key before
  ranking/equality checks, so worker-completion order never leaks into the output decision.

### Engine assembly (mirror `backend/main.go`)

- Per candidate run: overlay the seven values onto a deep copy of `LoadOutput.Balance`; rebuild the
  derived configs (`WorldConfig`, `AgentConfig(NeedsRegistry)`, `PlannerConfig`, `ValuesConfig`,
  `PerceptConfig`, `ClockConfig`); build `planner.New(...)`, `perception.NewSensor(...)`,
  `worldtime.NewClock(...)`, assemble `agent.Services`, `world.New(...)` with a **collecting
  `core.EventEmitter`** (captures `RoleEmerged` + action-start events; discards the rest), spawn the
  standard headless population (same scheme as `main.go`'s random spawn, or a fixed scenario), then
  `Tick()` `-ticks` times. After the loop, read the captured events + the world's public agent state
  (`AgentIDs`/`AgentOf`/`Snapshot`) to compute `Metrics`.
- The collecting emitter MUST be cheap (append-only, no IO) so it does not perturb determinism or
  dominate wall-time; it lives entirely inside `Evaluate`.

### References

- `docs/core/testing.md §5` (auto-tuning targets — this tool's mandate), `§1` (determinism / cross-process
  reproducibility the tuner relies on), `§4` (scenario G = the role-convergence source).
- `docs/core/data-contracts.md` (`RoleEmerged{function, holder, reliance_share}`; `config_hash`).
- `content/balance.yaml` (the seven keys, all present today) and its `content/schema/balance.schema.json`.
- `backend/main.go` (the canonical engine-assembly sequence to mirror).
- D2 (no Faction type — clusters are emergent), D6 (reputation = ToM distribution, never a scalar),
  D8 (perceived Intelligence via ToM[self] for the cohort split), D12 (determinism rules).
