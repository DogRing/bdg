<!--
SPEC.template.md — every module SPEC follows this section structure.
Parent SPECs stay abstract; children reference by path (no copy-paste).
Do not leave empty sections as "N/A" — delete them. Fill every <...>.
-->

# SPEC — `<engine|platform>/<module>`

> Status: `DRAFT | READY | IMPLEMENTED | NEEDS_FIX`
> Leaf level: `<L0..L6>`  ·  Owner agent: `<filled by implementer>`

## Purpose
<What this module is responsible for. 1–3 sentences. Anything beyond goes under "Out of Scope".>

## Public Interface
<The *only* contract other modules depend on. Types and function signatures. This is all a sibling reads.>
```go
// e.g. func New(reg *stats.Registry) *Evaluator
// e.g. type Gate interface { Reads() []core.StatID; Eval(a *core.Agent, s *core.State) (bool, float64) }
```

## Dependencies
<By path only. No copy-paste. One line per dependency naming *which interface* you use.>
- `engine/kernel/core` — `<StatID, Vec2, …>`
- `engine/mind/stats` — `<Registry>`

## Owned Data
<State/types this module owns or transfers ownership of. What other modules must not mutate.>

## Invariants
<What must always hold. Determinism, units, bounds. A violation is a bug.>
- e.g. Never iterate a `map` for logic (use sorted keys).
- e.g. Do not mutate the input Agent (read-only).

## Acceptance Criteria (testable)
<The reviewer checks these mechanically. Each item maps to at least one test.>
- [ ] <behavior verified by a table-driven unit test>
- [ ] <determinism / golden, if applicable>
- [ ] <related scenario-fixture assertion — docs/testing.md>

## Out of Scope
<What is NOT done here, and the path to where it is.>

## Open Questions
<Blocked decisions to escalate to architect/human. Mark whether it blocks P1.>

## Notes
<Implementation hints, pitfalls. Reference paths into data-contracts / glossary.>