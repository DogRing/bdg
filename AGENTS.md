# AGENTS.md — Codex working guide for `bdg`

> Codex reads this before any task. It is the durable rule set; per-task detail lives in
> `docs/codex-prompts.md`. Review standards: `docs/code_review.md`. (Claude Code uses `CLAUDE.md`;
> this file mirrors its rules for Codex — both point at the SAME authoritative docs below.)

## What this project is
A **deterministic agent-based medieval-village simulation**. Pure simulation in a **Go backend**
(`backend/engine/*`, no IO), IO/infra in `backend/platform/*` (Redis live, Postgres backup, SSE),
graphics later via a **TypeScript/React frontend** (`frontend/`). **No LLM at runtime** (the tick is
deterministic). Content (species, stats, actions, formulas) is **data** in `content/*.yaml` evaluated
by a §6 expression DSL — behavior is authored as data, not hardcoded in Go.

## How to work here (SPEC-first)
1. **Intent lives in each folder's `SPEC.md`.** Implement code to that SPEC **exactly** — same package,
   same public function/type signatures, same acceptance criteria. Do not add or rename public surface.
2. A module's ONLY cross-module contract is its SPEC **Public Interface**. **Do not read a sibling
   module's implementation** — read its `SPEC.md` (and, if a signature is unclear, its `.go` public
   decls). Guessing an upstream signature is the #1 integration bug.
3. On a code↔SPEC mismatch, **fix the SPEC first**, then the code.
4. If a file would exceed ~400 lines, split it into sub-files.
5. Read, in this order, before editing: this file → `docs/architecture.md` (module DAG + leaf-first
   build order + import sets) → `docs/glossary.md` (every identifier uses these names) → the target
   `SPEC.md` → the Public Interface of each dependency SPEC.

## Build, test & verify (always — this is how you confirm your work)
Backend — run from `backend/`:
```
go build ./...                    # whole module must build
go test ./<pkg>/ -count=2         # green AND stable (determinism goldens must reproduce)
go vet ./<pkg>/                   # clean
gofmt -l <pkg-dir>                # MUST print nothing (run `gofmt -w` to fix)
```
Frontend — run from `frontend/`:
```
npm install && npm run build && npm test
```
A module is **not done** until: its own tests pass `-count=2`, `gofmt -l` is empty, `go vet` is clean,
AND `go build ./...` still passes (you did not break another package). Prefer **test-driven**: write
the acceptance-criterion tests, watch them fail, then implement until green — and **do not weaken a test
to make it pass** (fix the code, or fix the SPEC then the code).

## Non-negotiable invariants (violating these makes the sim non-deterministic → silently broken)
- **D12 determinism.** RNG is an **injected seeded `*rng.RNG`**. No global `rand`, no `time.Now()`, no
  wall-clock. **Never iterate a `map` for logic** — collect keys, sort, iterate the sorted slice. Tick
  order is `read → plan (read-only) → collect → apply (serial, fixed sorted-ID order)`. Same seed ⇒
  byte-identical state. Every module has a determinism-golden test that must reproduce across a second
  run/process.
- **D11 continuous space.** Positions are continuous `float` (`core.Vec2`). Grids (navmap, scent,
  climate) are **indices, not the world** — compute `floor(p/cellSize)` to read a cell; never snap an
  entity to a cell.
- **D10/D4 data-defined.** Species/action/stat/rate/threshold behavior is `content/*.yaml` §6 data via
  `engine/kernel/expr`. **No hardcoded species-name / stat-name / rate / threshold / operand literal in
  Go logic.** Most SPECs have a grep-guard test for this.
- **Forbidden-import guard = parse imports, not text.** When a test asserts "no forbidden import", parse
  the file's imports with `go/parser` and check the import paths — do NOT substring-scan raw source
  (comments mention package names and would false-positive). See `engine/env/climate` for the pattern.
- Other invariants (D1–D9) are in `docs/design.md` / `CLAUDE.md`; the ones above are the ones you will
  break by accident. Do not "fix" an intentional invariant that looks like a bug.

## The Open-Question gate (STOP, do not invent)
Each `SPEC.md` has an **Open Questions** section. If a question **tagged to the phase you are building
is `OPEN`**, you **STOP and report the OPEN list** — you do **not** pick an option and you do **not**
invent a mechanism. *Inventing a mechanism is a defect, not initiative.* The activation-phase decisions
are already **accepted** in `docs/activation-gate.md` (treat those as resolved); if you hit a *different*
open question, surface it instead of guessing.

## Current state (do NOT rebuild these)
- ✅ Built + green: `engine/space/{scent,navmap,pathfind}`, `engine/env/{climate,flora,decay}`,
  `engine/fauna`, plus `engine/ecosim` (an integration harness that runs the activated fauna+scent+
  climate loop 2000 ticks deterministically). Plus the pre-existing social engine (`engine/agent`,
  `engine/world` base tick loop, `engine/mind/*`, `engine/kernel/*`, `engine/space/spatial`) and
  `platform/{config,events,persist,api}` base.
- ⚠️ A temporary **frontend mock** lives in `engine/world/tick.go` ("MOCK ENV FOR FRONTEND TESTING",
  hardcoded `deer_1`/`wolf_1`) + `events.go` `TypeWorldFrame` + some `frontend/src` files. Leave it
  until the WI-P4 phase, which replaces it with the real `WorldFrame`.
- Remaining work = phases 3–9 in `docs/codex-prompts.md`.

## Authoritative docs (read the relevant ones per task)
- `docs/architecture.md` — module DAG, leaf-first build order, import sets.
- `docs/glossary.md` — canonical identifier names.
- The target module's `SPEC.md` (+ its sub-SPECs, e.g. `engine/world/SPEC-world-env.md`).
- `docs/activation-gate.md` — the accepted activation decisions (G1–G17).
- `docs/codex-prompts.md` — the per-phase implement + review prompts (start here for a task).
- `docs/code_review.md` — the review checklist (for review tasks).
- `HANDOFF.md` — the general handoff brief (read order, integration pitfalls).

## Reporting — end EVERY task with the block below (this rule is auto-applied; the human never repeats it)
This file is read before every task, so you MUST finish each task by emitting the matching block
**verbatim** (keys fixed, values terse + factual, no extra prose wrapping it). A hidden `FAIL` or a
vague summary breaks the human's relay to the orchestrating agent — always include the real
`go build` / `go test -count=2` / `go vet` / `gofmt -l` results, and surface any `BLOCKED_ON` (an OPEN
question needing a human decision) explicitly.

**Implement task:**
```
=== CODEX REPORT ===
TASK: <phase N — module> (implement)
STATUS: DONE | PARTIAL | BLOCKED
FILES:
  - <path> (new|edited, ~N lines)
AC_COVERAGE:
  - <SPEC acceptance criterion> -> <test func>
VERIFY:
  - go build ./...              : PASS | FAIL
  - go test ./<pkg>/ -count=2   : PASS | FAIL (<N> tests)
  - go vet ./<pkg>/             : PASS | FAIL
  - gofmt -l <pkg-dir>          : CLEAN | <files listed>
GOLDENS: none | re-baselined: <which + one-line why>
CHOICES: <SPEC-delegated functional-form/ambiguity decisions> | none
BLOCKED_ON: <OPEN question that needs a HUMAN decision> | none
DEVIATIONS: <anything not per SPEC + why> | none
NEXT: <the phase/task this unblocks>
=== END ===
```

**Review task:**
```
=== CODEX REVIEW ===
TASK: <phase N — module> (review)
VERDICT: PASS | NEEDS_FIX
VERIFY:
  - go build ./...              : PASS | FAIL
  - go test ./<pkg>/ -count=2   : PASS | FAIL (<N> tests)
  - go vet / gofmt -l           : CLEAN | <issues>
CHECKS (per docs/code_review.md): interface:? determinism:? AC:? integration:? gate:? build:?   (each PASS|FAIL)
INTEGRATION_CONTRACT: <consumer SPECs checked + do signatures match?> | n/a
FINDINGS:            (list only if NEEDS_FIX; else "none")
  - <file:line> — <violated SPEC clause / AC / invariant> — <minimal fix>
NOTES: <non-blocking observations> | none
=== END ===
```

## Don'ts
- Don't commit or open a PR unless the task says so; don't push to `main` (branch first).
- Don't modify a module's tests to make them pass — fix the code (or the SPEC, then the code).
- Don't touch the frontend mock (`world/tick.go`) except in the WI-P4 phase.
- Don't add a production dependency without saying so in your summary.
- One Codex thread **per task**, not per project (keeps context tight).
