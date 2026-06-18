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
   └─> leaf-first implement (implementer)     # order from docs/architecture.md
          └─> verify (reviewer)
                 ├─ PASS ──> next leaf
                 └─ NEEDS_FIX ──> back to implementer
```
- **Leaves with no dependencies first**, one at a time. Dependencies are seen only through their *public interface (SPEC)*.
- The implementer **does not read sibling implementations**. The reviewer enforces interface, contract, and determinism.

## Rules
- Before writing/editing code, check/refresh that folder's `SPEC.md`. On mismatch, fix the SPEC first.
- A file exceeding **~400 lines → split into sub-modules**, each with its own SPEC.
- Vocabulary: `docs/glossary.md`. Contracts: `docs/data-contracts.md`. Invariants: `CLAUDE.md` (D1–D12).
- Stats, actions, and gates are added as `content/` data + schema, not code.
- Determinism and test rules: `docs/testing.md`.

## Escalation
- To touch an invariant (D1–D12), stop, fix `docs/design.md` first, then get human approval.
- New modules or contract changes update `architecture.md` / `data-contracts.md` first.