# Testing — Determinism · Golden Snapshots · Scenario Fixtures

The highest-leverage check is **"run seed S for N ticks, diff against golden."** It lets the reviewer catch regressions without running the whole sim.

## 1. Determinism (the prerequisite; nothing else matters if broken)
- RNG is **injected by seed** (`rng.New(seed)`). No global rand, `time.Now()`, wall-clock, or goroutine races.
- No driving logic by `map` iteration — use sorted keys / slices when order matters.
- Tick: read (snapshot) → plan (parallel, read-only ok) → collect intents → **apply serially in fixed agent-ID order**.
- Conflicts (same resource at once) → compare the relevant stat; ties break by ID.
- **Invariant**: running twice with the same (seed, config, initial state) yields byte-identical snapshots every tick.
- **Resume invariant**: resuming from a tick-T snapshot is byte-identical to running from the start to T+k.

## 2. Per-module unit tests (from SPEC acceptance criteria)
- Every module ships tests. A SPEC's **Acceptance Criteria** items are the test cases.
- Pure functions (gate eval, cost terms, value evaluation, forward-roll) get table-driven tests.
- Registries (stats, gates, actions): load `content/` → pass schema → assert expected entries.

## 3. Golden snapshots (engine regression)
- `testdata/golden/{caseName}/{seed, tickN, expected.snapshot.json}`.
- Test: run seed for N ticks → serialize → diff against golden. On intentional change, regenerate via an `-update` flag and have a human review the diff.
- Keep cases small and deterministic (1–3 agents, short N) for speed.

## 4. Scenario fixtures = integration tests (intent ↔ code)
Encode scenarios A–H from `docs/core/design.md` / `docs/core/PRD.md` as named fixtures. Each = `{initial state, seed, expected-emergence assertion}`.
> **World-layer (물리/세계) 시나리오 W1–W16** — 지형·기후·식생·물질사슬·부패·경제·생애 창발 — 은 **`docs/plans/scenarios-world.md`** 에 별도 카탈로그로 있다(의존 subsystem·`BLOCKED` 상태 포함). 사회 시나리오(A–L)를 보완.
| Fixture | Assertion (example) |
|---------|---------------------|
| B starving pauper | given enough urgency, theft becomes *visible* and is selected |
| C deceptive trade | after exposure, only the *witness cluster's* `ToM[A].Honesty` drops (other clusters unchanged) |
| D love-driven crime | `wellbeing(child)` weight lowers the conscience threshold more strongly than urgency |
| F dead-end goal | high-Intelligence rebinds; low-Intelligence fixates (no rebind) |
| G chief emerges (P6) | chained theft → collective safety↓ → Patrol → `RelyOn` converges to one holder (Votes accelerate under shared urgency) → `RoleEmerged{function:"safety"}` on the rising edge (golden); higher-Influence signals shift ToM more |
| H long journey | high-Intelligence inserts a provisioning subgoal before departure; low-Intelligence does not |
- Assertions test **direction / existence** (did it emerge or not), not exact numbers. Numbers are tuned in §5.

## 5. Auto-tuning targets (emergence metrics)
Balance constants (rate, α, β, λ, thresholds) are not hand-tweaked. Target **emergence statistics** with grid/CMA-ES:
- Crime rate correlates with poverty (plausible frequency of scenarios B/D).
- Reputation variance persists across faction boundaries (C produces per-faction contradiction).
- Role convergence reproduces on stable seeds (G).
- Unprovisioned starvation concentrates in low-Intelligence agents (H).
Target-metric definitions are finalized late (P7). Until then, the §4 directional assertions suffice.

## 6. Reviewer usage
PASS requires: public interface = SPEC; acceptance-criteria tests exist and pass; determinism invariants hold; golden matches (or an update diff is human-approved); vocabulary = glossary; schemas = data-contracts; files ≤ ~400 lines.
On failure, emit a structured `NEEDS_FIX` (SPEC/file/line, specific).