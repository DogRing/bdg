# HANDOFF — Implementing the remaining SPECs (read this FIRST)

You are an implementation agent picking up a **spec-first** Go simulation. The SPECs are detailed and
build-ready, but a SPEC file is **not self-contained on its own** — getting code right requires the
read order, the determinism rules, and the open-question discipline below. Two leaf modules
(`engine/space/scent`, `engine/space/navmap`) were already built **from their SPECs alone** and passed
review, so the workflow works — *if you follow it*. The failure modes are not "the SPEC is unclear";
they are "skipped CLAUDE.md", "guessed an upstream signature", "invented a mechanism at an OPEN gate",
or "broke determinism in the wiring layer". This doc exists to prevent exactly those.

---

## 0. Mandatory read order (before writing ANY code)

1. **`CLAUDE.md`** (repo root) — the operating rules. The **Invariants D1–D12** and the **Determinism
   rules** and the **Open-Question gate** are binding and override anything else. Non-negotiable.
2. **`docs/core/architecture.md`** — the module dependency DAG, the import sets, and the **leaf-first build
   order** (§5). You implement in that order; never out of order.
3. **`docs/core/glossary.md`** — every identifier uses these names. No naming drift.
4. **The target module's `SPEC.md`** — implement to it exactly.
5. **The `SPEC.md` Public Interface of every module the target depends on** (listed in the target's
   "Dependencies"). You match those signatures by reading the upstream **SPEC** (and, only if needed,
   the upstream code) — **never by guessing**. A guessed `expr.Context` / `spatial.NearbyEntities` /
   `actions.Registry` signature compiles in isolation and breaks at integration.
6. For the env/fauna wiring modules: the relevant **Tier-2 plan** in `docs/` (`docs/plans/fauna.md`,
   `docs/plans/flora.md`, `docs/plans/climate.md`, `docs/plans/world-integration.md`) — these hold the binding resolutions
   the SPEC derives from.

If a SPEC says "see `docs/X` / module Y's SPEC", you read it. Do not implement from a single file.

---

## 1. Non-negotiable rules (these cause silent, expensive failures if missed)

- **Determinism (D12).** RNG is an **injected seeded `*rng.RNG`** — no global `rand`, no `time.Now()`,
  no wall-clock. **Never iterate a `map` for logic** — use sorted keys/slices when order matters. Tick
  order is `read → plan (read-only) → collect → apply (serial, fixed sorted-ID order)`. Same seed ⇒
  byte-identical state. Every module here has a "determinism golden" acceptance criterion — it must
  re-run byte-identical, including across a second process.
- **Continuous space (D11).** Positions are continuous `float` (`core.Vec2`). Grids (navmap, scent,
  climate) are **indices, not the world** — you compute `floor(p/cellSize)` to read a cell, you never
  snap an entity to a cell.
- **The Open-Question gate (the biggest risk for an external agent).** Each SPEC has an **Open
  Questions** section. If a question tagged to the phase you are building is `OPEN`, you **STOP and
  return the OPEN list** — you do **not** pick an option, and you do **not** invent a mechanism.
  *"Inventing a mechanism is a defect, not initiative"* (CLAUDE.md). Most leaf SPECs here have no
  blocking OPEN (scent: "None"; climate CA1–3: RESOLVED; fauna: "None remaining"); flora's OPEN items
  are all tagged to LATER activation phases (P_f3/P_f4) and do **not** block the pure-transform build.
  But the **activation** phase (placing species, turning climate/flora ON) has real human-gated OPENs
  (flora `Fell`/`Plant` actions, `berry_bush` migration) — when you reach those, **stop and ask the
  human**, do not author them. **All activation-blocking decisions are collected in
  `docs/plans/activation-gate.md`** (G1–G17, with options + recommendations + a `DECISION:` line each); an
  item there is `OPEN` until the human fills it in and flips the owning SPEC's Open-Question entry. Do
  not proceed past an `OPEN` Part-1/Part-2 item; Part-3 items have safe defaults.
- **Outcome-neutral until activated.** climate/flora/decay/fauna/scent all ship **inert** first (empty
  `Rules` / no species placed / `Install*` not called) so existing world goldens hold. Building the
  module ≠ activating it. Do not "turn it on" to make a demo pass — activation is a deliberate,
  human-gated, golden-re-baselining phase.
- **Data-defined, not coded (D4/D10).** Species behaviour, stats, gates, formulas live in `content/`
  `*.yaml` as §6 expressions compiled via `engine/kernel/expr`. Never hardcode a species name, stat
  name, rate, threshold, or formula in Go logic. There is a grep-guard AC for this in most SPECs.
- **Pure engine, no IO.** `engine/` imports no `os`/`net`/filesystem. IO lives in `platform/`.

---

## 2. Per-module workflow (the loop that already works)

For each module, in build order:
1. Read its `SPEC.md` fully + the Public Interface of each dependency (per §0).
2. Implement the Public Interface **exactly** (no surface drift, no extra exported symbols unless the
   SPEC/its consumers require them — see §4).
3. Honor every Invariant; write a test for **every checkbox** in "Acceptance Criteria (testable)" —
   each AC is a real test, including the determinism golden (must reproduce on a second run).
4. Split any file > ~400 lines into sub-files (CLAUDE.md rule 5).
5. Verify, from `backend/`:
   ```
   gofmt -l <pkg-dir>          # must print nothing
   go vet ./<pkg-path>/        # clean
   go test ./<pkg-path>/ -count=2   # green and STABLE (golden reproduces)
   go build ./...              # whole module still builds
   ```
6. **Review against the SPEC** before moving on (the `implement → review → NEEDS_FIX` loop). Do not
   build the next module until the current one is PASS.

Toolchain note: ensure the Go toolchain is on `PATH` (in the reference env it is; fallback path
`/home/coder/go/bin`). Run all commands from `backend/`.

---

## 3. Build order & current status

Leaf-first (from `docs/core/architecture.md §5`). **Implement the ⬜ rows, in order.**

| Stage | Module | Status |
|---|---|---|
| 1 | `kernel/core`, `kernel/rng`, `kernel/expr`, `kernel/worldtime` | ✅ done |
| 2 | `space/spatial`, `mind/stats` | ✅ done |
| 2 | `space/navmap` | ✅ done (just built) |
| 2 | **`env/climate`** | ⬜ build (deps: core, expr, rng — all done) |
| 2 | **`env/flora`** | ⬜ build (deps: core, expr, rng) |
| 2 | **`env/decay`** | ⬜ build (deps: core, expr, rng; SPEC marks it READY) |
| 3 | `mind/gates`, `mind/needs`, `mind/tom` | ✅ done |
| 3 | **`space/pathfind`** | ⬜ build (deps: core, navmap — done) |
| 2 | `space/scent` | ✅ done (just built) |
| 4 | `mind/actions`, `mind/values`, `mind/perception` | ✅ done |
| 5 | `mind/planner` | ✅ done |
| 6 | `engine/agent` | ✅ done |
| 6 | **`engine/fauna`** | ⬜ build (deps: core, expr, rng, actions, spatial, **scent** — all done) |
| 7 | `engine/world` (base tick loop) | ✅ done |
| 7 | **`engine/world` WI-P1 env wiring** (`SPEC-world-env.md`) | ⬜ build — `InstallEnv`, env sub-phase, climate→navmap `SetTerrain` bridge, flora `SiteInput` sampling, decay env, `ShadeOccluders` |
| 7 | **`engine/world` WI-P2 fauna wiring** (`SPEC-world-fauna.md`) | ⬜ build — `InstallFauna`, `fauna.Step` in plan phase, combined agent+animal apply (F41), scent deposit/spread/commit, `EnvSample`/`TerrainSampler` adapters |
| 8 | `platform/config`, `events`, `persist` | ✅ base done |
| 8 | **`platform/config` WI-P0 env/fauna loading** (`platform/config/SPEC-world.md`) | ⬜ build — parse `content/objects.yaml` `fauna:`/`flora:` + `climate.yaml`, **compile each §6 via `expr`**, build `fauna.Rules`/`flora.Rules`/`climate.Rules`, run the cross-checks (e.g. fauna `ReadsAttrs() ⊆ AttrOperands() ∪ species drive ids`), assemble `InstallEnv`/`InstallFauna` inputs |
| 9 | `platform/api`, `main.go` | ✅ base done; ⬜ `WorldFrame` SSE projection (WI-P4) when env activates |
| 10 | **`frontend` ecosystem rendering** (`frontend/SPEC.md` §Ecosystem) | ⬜ `drawAnimals`/`drawFlora`/`drawAmbient` + wire env state into `WorldCanvas` (data layer already reduces it) |

Recommended critical path to *any* visible fauna behaviour: `climate` + `flora` → `pathfind` →
`fauna` → config WI-P0 → world WI-P1 → world WI-P2 → (human-gated) activation → frontend rendering.

---

## 4. Cross-module integration — where SPEC-alone breaks (read carefully)

A per-module SPEC can omit a requirement that lives in a **consumer's** SPEC. **Confirmed example:** the
`engine/space/navmap` SPEC does **not** list the `FootprintBlocked(cell) bool` and `BaseCost(cell)
float64` accessors — those are specified only in `engine/world/SPEC-world-fauna.md` (a RESOLVED note),
because the fauna `TerrainSampler` needs them (water = traversable-at-high-cost, not impassable; only
walls block). The already-built navmap **does** include them, but only because the builder was told to
read the world-fauna SPEC. **Lesson: before finalizing a module, grep the consumer SPECs for
requirements on it.** Specifically:
- Before `navmap`/`climate`/`flora`/`scent`: read `engine/world/SPEC-world-env.md` +
  `SPEC-world-fauna.md` for the accessors/adapters the world expects (`TerrainSampler`,
  `EnvSample`, `CellAt`, `Wind()`, `ShadeOf`, `IntensityAt`, etc.).
- Before `fauna`: read `SPEC-world-fauna.md` (the apply contract, `EnvSample`/`TerrainSampler` shapes)
  and `platform/config/SPEC-world.md` (how `fauna.Rules` is built + the `AttrOperands()` cross-check).

The two genuinely hard integration layers (build them carefully, they are mostly prose/pseudocode and
you translate to real APIs):
- **`platform/config` WI-P0** is the glue that turns content `§6` YAML into the compiled `*Rules`
  inputs. If the `expr` compile or the cross-checks are wrong, everything builds but **nothing
  activates / nothing moves**. The cross-checks (operand ⊆ allowed set, ids ⊆ registries) are what
  turn a content typo into a *load-time* failure instead of a silent zero.
- **`engine/world` WI-P1/WI-P2 wiring** must preserve determinism while adapting modules to each other:
  the env sub-phase runs **after** intent apply, **serial**, in fixed module order (climate→flora→
  decay→scent); the combined agent+animal apply is ONE lexicographically-sorted id stream; per-env RNG
  is `envFork(tick, channel)` **disjoint** from agent forks; scent deposits use **post-move** positions
  then `Commit` for next-tick latency. Re-read `SPEC-world-env.md`/`SPEC-world-fauna.md` "Determinism"
  sections — these are the easiest places to introduce a non-deterministic bug.

---

## 5. "Will it run the desired scenario?" — what SPEC-alone does NOT guarantee

The SPECs verify **unit mechanisms**, not **emergent scenarios**. The headline ecosystem behaviours
(deer grazes then flees; predator gives up when its stamina drops first; scent spreads downwind and
prey homes upwind; temperature slows movement) are **emergent from §6 balance coefficients** in
`content/`, and only appear **after activation**. To make them real and verifiable you (with the human)
still need, beyond building the modules:
1. **Activation** — place species in the fixture, turn climate ON (wind/°C), turn flora `Rules` ON, and
   re-baseline the affected goldens. **Human-gated** — see **`docs/plans/activation-gate.md`** Part 1 (G1–G5).
   Do not self-author these.
2. **Balance tuning** — the §6 utility/speed/drive coefficients are first-draft; the emergent behaviour
   needs iteration. (Example already in place: each species' `speed` §6 now subtracts a `thermal` term
   so body-temperature stress slows it — but the coefficient magnitudes are unverified.)
3. **End-to-end scenario tests** — the FA1–FA8 ecosystem scenarios in `docs/plans/scenarios-world.md` are
   **not yet encoded as tests**. Until they are, "runs the desired scenario" is unverified. Adding them
   is the acceptance gate for activation.

Treat "modules build + unit ACs pass" and "the ecosystem behaves as desired" as **two separate
milestones**. The first is what these SPECs deliver; the second needs activation + tuning + scenario
tests on top.

---

## 6. Quick verification (any module)

```bash
cd backend
gofmt -l <pkg-dir>                 # nothing
go vet ./<pkg-path>/               # clean
go test ./<pkg-path>/ -count=2     # green + golden stable
go build ./...                     # whole module builds
```
Determinism smoke for a wired build: run the same seed twice, diff the per-tick state/intent digest —
must be byte-identical, including from a fresh process.

---

*If anything in a SPEC forces you to choose between options or invent a mechanism that isn't written
down: STOP and surface it. That is the single most important rule here.*
