# Claude Spec System — Spec-First, Layered-Module Methodology

`CLAUDE.md` carries the summary; this document is the full explanation.

## Principles
1. **Spec-first**: intent lives in per-folder `SPEC.md`. Code merely implements the SPEC.
2. **Layered modules**: never read large files whole. Parent SPECs are abstract; children are referenced by path.
3. **Context economy**: the main session sees only top-level SPECs and summaries. Detail is handled in isolation by subagents.

## Roles
| Agent | Responsibility | Tools | Output |
|-------|----------------|-------|--------|
| spec-architect | Decompose PRD/design into module SPECs | read/write (no code) | SPECs, dependency graph |
| implementer | Implement a single module | read/write/bash | code + tests |
| reviewer | Verify against the SPEC | read-only | PASS / NEEDS_FIX |

## Standard flow
```
decompose (architect)
   └─> leaf-first implement (implementer)     # order from docs/core/architecture.md
          └─> verify (reviewer)
                 ├─ PASS ──> next leaf
                 └─ NEEDS_FIX ──> back to implementer
```
- **Leaves with no dependencies first**, one at a time. Dependencies are seen only through their *public interface (SPEC)*.
- The implementer **does not read sibling implementations**. The reviewer enforces interface, contract, and determinism.

## Rules
- Before writing/editing code, check/refresh that folder's `SPEC.md`. On mismatch, fix the SPEC first.
- A file exceeding **~400 lines → split into sub-modules**, each with its own SPEC.
- Vocabulary: `docs/core/glossary.md`. Contracts: `docs/core/data-contracts.md`. Invariants: `CLAUDE.md` (D1–D12).
- Stats, actions, and gates are added as `content/` data + schema, not code.
- Determinism and test rules: `docs/core/testing.md`.

## The Documentation Triad — where words live
Every piece of documentation has exactly one home. Writing it elsewhere is drift; keeping all three in
one file is bloat (the pre-diet 73K `fauna.md` failure mode).

| Layer | Content | Home |
|---|---|---|
| **What** | decisions, resolved gates, phase status, invariant guards | the subsystem plan (`docs/plans/<subsystem>.md`) — a **decision record**: resolutions + status + SPEC pointers, never mechanisms |
| **How** | mechanisms, schemas, signatures, constraints, parameters, ACs | the owning module's `SPEC.md` (split at ~400 lines) |
| **Why** | option deliberations, rejected alternatives, superseded designs, audits | **`docs/decisions/<subsystem>-gates.md`** — `.claudeignore`d (zero ambient tokens), read explicitly on demand |

- **Retiring "Why" text:** cut the deliberation out of the plan into `docs/decisions/<subsystem>-gates.md`
  (append if the file exists; give it a provenance header: source plan, resolution dates), and replace it
  with a one-line pointer (`옵션 전문 = docs/decisions/<file>`) next to the surviving resolution. The
  plan's resolution table stays authoritative — the decisions file is a frozen record, never re-edited
  into a second source of truth.
- **Recovering past rationale:** do NOT guess, do NOT re-litigate, and do NOT grep the living docs for
  it — deliberations are deliberately absent from plans/SPECs. **Explicitly Read the pointed-to file in
  `docs/decisions/`** (every diet pointer names one). Git history remains the fallback for anything
  predating the folder (e.g. fauna F1–F24 at commit `bebc643`).
- **Writing a new plan/SPEC:** apply the triad from the start — enumerate options in the plan only while
  the gate is OPEN; once the human resolves it, compress the plan to the resolution and move the debate
  to `docs/decisions/`.

## Escalation
- To touch an invariant (D1–D12), stop, fix `docs/core/design.md` first, then get human approval.
- New modules or contract changes update `docs/core/architecture.md` / `docs/core/data-contracts.md` first.