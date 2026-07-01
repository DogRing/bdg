# code_review.md — review checklist for `bdg` (Codex + humans)

> Apply this when reviewing an implemented module/phase. Return a verdict: **PASS** or **NEEDS_FIX**
> (with exact `file:line` + the violated SPEC clause/AC + a minimal fix). Review is **read-only** —
> propose fixes, don't silently rewrite. Keep the report compact.

## 0. Ground truth
Read the module's `SPEC.md` first; the code conforms to it, not the other way around. If the code is
right but the SPEC is stale, that's a SPEC fix (flag it) — but a code↔SPEC mismatch is a defect.

## 1. Public Interface conformance
- Every type/function/const in the SPEC's **Public Interface** exists with the **exact** signature.
- **No public surface drift** — no extra exported symbols unless the SPEC or a consumer requires them
  (if extra, is it justified? e.g. a serialization accessor named in the SPEC's Out-of-Scope).
- Package name matches; canonical `docs/glossary.md` names used (no synonyms/coinages).

## 2. Determinism (D12) — the highest-value check
- RNG is the **injected `*rng.RNG`** only — no `math/rand` global, no `time.Now()`, no wall-clock.
  (Verify by parsing imports, not by trusting comments.)
- **No `map` iteration drives observable output** — sorted keys/slices everywhere order matters
  (sweep order, delta slices, returned lists, tie-breaks).
- The **determinism-golden test reproduces** across a second run (`go test -count=2`) and, where the
  SPEC says so, across a fresh process. Ties broken by a fixed rule (ID / cell (Y,X) / sorted key).

## 3. Continuous space (D11) & data-defined (D10/D4)
- Positions stay continuous `Vec2`; grids are read via `floor(p/cellSize)`, never snapped.
- **No hardcoded** species/stat/action/rate/threshold/operand literal in logic — all from injected
  config/Rules/registries. Confirm the SPEC's grep-guard AC exists and is real.
- **Forbidden-import guard uses `go/parser`** (checks actual import paths), not a raw-source substring
  scan (which false-positives on SPEC-explaining comments). If it uses substring scan → NEEDS_FIX.

## 4. Acceptance Criteria coverage
- **Every** checkbox in the SPEC's "Acceptance Criteria (testable)" maps to a REAL test that
  meaningfully exercises the behavior (not a stub/always-true). List AC → test.
- Panics/guards specified by the SPEC (e.g. missing-input panic) are tested.
- Outcome-neutral / "OFF" levers (empty Rules ⇒ no effect) are tested where the SPEC requires them.

## 5. Integration contracts (the cross-module "does it plug in?" check)
This is where SPEC-alone breaks. A module's public API must match what its **consumers** call:
- **grep the consumer SPECs** for how they call this module, and confirm the actual signatures match.
  Lessons already found: `navmap` had to expose `FootprintBlocked`/`BaseCost`/`CellCenter`/
  `MinCostFactor` for its consumers (fauna TerrainSampler + pathfind); `world-env` SPEC's
  `decay.Step(...)` call had to include the `elapsedTicks` arg. Look for the same class of gap.
- Check the module's `NewRules`/constructor shape matches what `platform/config` (WI-P0) will call, and
  its `Step`/apply shape matches what `engine/world` (WI-P1/P2) will call.
- Operand vocabulary (`temperature`/`moisture`/`wind.dir`/`wind.mag`, drive/Attr names) matches the
  shared vocabulary across climate/flora/decay/fauna so the same content thresholds work.

## 6. Invariants & gate
- The design invariants (D1–D12, `CLAUDE.md`/`docs/design.md`) are honored — and intentional ones are
  NOT "fixed". No `Role`/institution type invented (emergence, D2); no stored skill (D7); no future-need
  field (D9); ToM/self-perception not "corrected" (D6/D8) where applicable.
- **Open-Question gate respected**: no mechanism silently invented for an OPEN question. If the code
  resolves something the SPEC left OPEN, that's a NEEDS_FIX (should have stopped).

## 7. Build health (run these)
```
go test ./<pkg>/ -count=2     # green + stable
go vet ./<pkg>/               # clean
gofmt -l <pkg-dir>            # empty
go build ./...                # whole repo still builds (didn't break a consumer)
```
For world/persist phases, also run the affected existing suites (e.g. `go test ./engine/world/ -count=2`)
to confirm no regression — except a **deliberate activation re-baseline**, which the task will call out
(then verify the re-baselined goldens are sensible, not just "regenerated to pass").

## 8. File hygiene
- Files < ~400 lines (split otherwise); test files may exceed. No dead code beyond a documented
  reserved seam. `gofmt`/`vet` clean.

## Verdict format
```
PASS — <one line on what you verified, incl. the integration-contract check>
```
or
```
NEEDS_FIX
- <file:line> — <violated SPEC clause / AC / invariant> — <minimal fix>
- ...
```
