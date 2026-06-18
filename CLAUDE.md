# CLAUDE.md — Operating Rules

Medieval village simulator. A **deterministic agent-based simulation runs in a Go backend**.
Live state in **Redis**, periodic backup to **Postgres**, graphics later via **SSE → frontend**.
**No LLM at runtime** (this is a simulation-design rule, not a statement about dev tooling).

> `docs/PRD.md` and `docs/design.md` are **human-authored input** (Korean). Read them, but the
> authoritative, agent-facing rules — including the invariants below — live in **this file (English)**.

## Read before starting
- `docs/PRD.md` — what we build (requirements; Korean input doc)
- `docs/design.md` — why (concept model + rationale; Korean input doc). Invariants are mirrored below.
- `docs/glossary.md` — **single source of vocabulary. Every identifier uses these names.**
- `docs/architecture.md` — module dependency DAG + leaf-first build order
- `docs/data-contracts.md` — serialization / Redis / Postgres / event schemas (cross-module)
- `docs/testing.md` — determinism, golden snapshots, scenario fixtures
- `docs/templates/SPEC.template.md` — the SPEC format
- `docs/claude-spec-system.md` — the spec-first / layered-module methodology

## SPEC-first workflow (essentials)
1. Never read large files whole. **Intent lives in per-folder `SPEC.md`.**
2. Before writing or editing code, check/refresh that folder's `SPEC.md`.
3. Code conforms to the latest SPEC. On mismatch, **fix the SPEC first**.
4. Parent SPECs stay abstract; children are **referenced by path** (never copy-pasted).
5. If a file would exceed **~400 lines, split it** into sub-modules, each with its own SPEC.
6. Implement **leaf modules first** (the order in `docs/architecture.md`).
7. Standard flow: decompose (architect) → implement leaf (implementer) → verify (reviewer) → `NEEDS_FIX` loop.
8. The main session sees **only top-level SPECs and summaries.** Detailed work is delegated to subagents.

## Invariants — "do NOT fix these even if they look like bugs"
These are intentional. To change one, edit `docs/design.md` first and get human approval.
- **D1** — Value is the end; objects are means. Goals are abstract values; an object is a path, not a goal.
- **D2** — Do not hardcode meta-systems/institutions. Crime, politics, factions, roles emerge from base mechanics.
- **D3** — Define only atomic actions and methods; plans/trees are assembled by the planner, never hand-drawn.
- **D4** — Cost and gates are **derived from action Tags**, not bespoke per-action functions.
- **D5** — Keep "what is wanted" (value/goal) and "how to get it" (planning) in separate modules.
- **D6** — **Never store reputation as a single value.** Reputation = the distribution of per-observer `ToM[C]`; the mean is derived.
- **D7** — **No individual skills.** Competence = composition of base attributes (Strength/Agility/Intelligence).
- **D8** — Self-perception = `ToM[self]`, calibrated by action (per-stat). Underestimation is self-sealing; do not "correct" it away.
- **D9** — **No "future need" field on objects.** Provisioning = need-rate × predicted time, forward-simulated by the planner. Objects only carry their satisfaction Effect (supply).
- **D10** — Stats, actions, and gates are added as **`content/` data + schema**, not code.
- **D11** — Free 2D coordinates + spatial hash (freedom + locality). The world is not tiled.
- **D12** — Determinism: injected seeded RNG; no map-iteration for logic; fixed agent-ID apply order.

## Determinism rules (NFR-1; violating these makes regression tests meaningless)
- RNG is an **injected seeded `*rand.Rand`**. No global rand, no `time.Now()`, no wall-clock.
- **Never drive logic by `map` iteration.** Use sorted keys or slices when order matters.
- Tick order: `read (snapshot) → plan (parallel ok, read-only) → collect intents → apply serially`. **Apply in fixed agent-ID order.**
- Conflicts (same resource grabbed at once) resolve by the relevant stat; ties break by ID.

## Code rules
- Language: Go. Every module ships **tests**; where applicable, a **golden snapshot** (`docs/testing.md`).
- Use the canonical identifiers from `docs/glossary.md` (no naming drift).
- Pure simulation lives in `backend/engine/` (no IO); IO/infra in `backend/platform/`.
- A module's only cross-module contract is its SPEC **Public Interface**. Do not read a sibling's *implementation*.

## .claudeignore should exclude
`vendor/`, build artifacts, `node_modules/` (future frontend), secrets, large generated blobs.
**Keep SPECs, contracts, glossary, and architecture always visible.**