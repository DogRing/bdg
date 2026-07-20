# Scent: the static layer was never diffused (Why)

> Audit cut from the living docs per the Documentation Triad. Pointer lives in
> `docs/plans/fauna.md` 클러스터 8b. Found 2026-07-20 by a review of the 48h fauna/scent work.
> This is a **defect record**, not an option deliberation: 클러스터 8b was RESOLVED (B) by the human
> on 2026-07-19 and the code does not implement it. No mechanism is being chosen here, so **no new
> Open-Question gate applies** — the fix restores the already-resolved design.

## Symptom

None visible. Every test is green (1358 across 34 packages), every scent golden holds, and the
verification quoted in 064c930's commit body — "food scent at a plant now reads 7.820 on EVERY tick"
— is true. That measurement was taken **at the plant's own cell**, which is the one place the defect
does not show.

## Root cause — the shared kernel writes to the wrong buffer

`spreadInto` was extracted in 064c930 so both layers would diffuse by identical arithmetic. It takes
the buffer to diffuse as a parameter and snapshots it correctly, but every write goes to the
hardcoded dynamic buffer instead of the parameter (`engine/space/scent/scent.go`, the source-cell
subtraction and the neighbour distribution inside the sorted-key loop):

```go
func (g *Grid) spreadInto(buf map[cellKey]cellVals, wind Wind) {
    for k, v := range buf { snap[k] = v; ... }   // reads the parameter ✓
    ...
        cv := g.pending[key]                      // writes the dynamic layer ✗
        ...
        nv := g.pending[nk]                       // writes the dynamic layer ✗
```

`Spread()` passes `g.pending`, so the dynamic path is correct by coincidence. `CommitStatic()`
passes `g.staticPend`, so the static path is wrong in both directions at once.

## Evidence

Throwaway probe against the package internals (`New(1.0)`, one `DepositStatic(ChanFood, (0.5,0.5),
10)`, one `CommitStatic(Wind{})`, no other traffic):

| buffer | content after `CommitStatic` |
|---|---|
| `static` | `{(0,0): 10.0}` — the raw deposit, **no halo** |
| `pending` | `{(-1,-1): 0.3, (0,-1): 0.3, (1,-1): 0.3, (-1,0): 0.3, (1,0): 0.3, (-1,1): 0.3, (0,1): 0.3, (1,1): 0.3}` |
| `committed` | `{}` |

The numbers match the constants exactly: `donated = 10 × spreadFraction 0.40 = 4.0`, delivered
`4.0 × spreadFalloff 0.60 = 2.4`, split over 8 isotropic weights = `0.3` each.

Three consequences follow.

1. **The static layer never diffuses.** A plant's food scent exists in the one cell it stands in and
   nowhere else, forever. This is the behaviour of option **(C)** ("정적은 확산하지 않음"), which
   클러스터 8b enumerated and **rejected** — so the shipped world runs the rejected option while the
   plan, the SPEC and the commit body all describe (B).
2. **A phantom halo is injected into the dynamic layer once per bulk cadence.** `runScentEnv` calls
   `CommitStatic` after `Commit`, so the halo lands in the freshly-cleared pending buffer, rides into
   `committed` on the next tick (diffusing a second time on the way), and is cleared the tick after.
   The 1-in-6 flicker 클러스터 8b existed to remove is therefore still present — moved from the peak
   to the halo, where nobody was looking.
3. **Field mass is not conserved.** The source-cell subtraction `cv[ch] -= donated[ch]` is also
   applied to `pending`, where the value is 0 and clamps back to 0, so the static layer keeps its
   full deposit *and* the halo is created on top of it. Total field intensity rises by 24% of every
   static deposit on each rebuild tick.

## Reach, in the shipped world's numbers

`scent_cell_size` is 10.0 and herbivore `smell_radius` is 7–10 (fish 7, rabbit 8, goat 9, deer 10),
so a herbivore's `Read` window is its own cell plus — depending on where inside the cell it happens
to stand — its nearest one or two neighbours. The **food channel is entirely static** (all six
flora species carry `scent:food`), so the only mechanism that could ever carry a plant's scent into
an adjacent cell is the static diffusion that does not run. Food homing still works, because `Read`
scans neighbouring cells directly and the gradient points at whichever cells hold plants; it is
short-ranged and steppy rather than blind.

The **carrion channel is static too** (`carcass` carries `scent:carrion`, deposited through
`depositObjectScent`), so FC10 scavenger/predator homing to a kill has the same zero-extent plume.

The prey/predator channels are dynamic and unaffected; the trail layer is written only by `Deposit`
and is unaffected.

## Why nothing caught it

- 064c930 shipped two new public methods (`DepositStatic`, `CommitStatic`) with **zero unit tests**.
  As of this audit no test in the repository calls either one — the layer's only coverage is
  indirect, through world-level runs that read at the source cell.
- The SPEC was not updated in that commit either. The static layer was documented one feature later,
  in e3f37fc, whose own body records the gap ("the scent SPEC had never mentioned [the static
  layer]"). A SPEC written after the code cannot catch the code.
- The defect is **deterministic** — it is wrong the same way every run — so every golden reproduces
  it and D12 guards are silent by construction. Goldens pin behaviour; they cannot pin intent.

The generalisable lesson is the second bullet: the acceptance criteria are what would have caught
this, and the SPEC had none for the static layer. Adding them is part of the fix, not paperwork
after it.

## Resolution

Make `spreadInto` write back into the buffer it read, so it is genuinely layer-agnostic, and add the
acceptance criteria the static and trail layers never had. No interface change: the Public Interface
in `engine/space/scent/SPEC.md` already specifies the correct behaviour ("CommitStatic … which also
diffuses it once, under `wind`") and is unchanged by this.

Rejected: **re-resolving 클러스터 8b to (C)** and documenting no-diffusion as intended. It would be
the cheaper edit, and the field has evidently been usable without static diffusion — but (C) was
enumerated and rejected on a stated ground ("바람에 실려오는 풀냄새가 사라진다"), the human resolved
(B) knowing the cost, and no measurement has been taken that argues against it. Ratifying an
accident is not a decision.

## What this re-bases

클러스터 8b's own resolution already warns that any change here re-bases goldens across the board;
that warning applies to the fix as much as to the original landing, and the human RESOLVE of (B)
covers it. Specifically invalidated as measurement baselines:

- every scent golden, and any world golden containing flora or carcasses;
- PD9 spoor `strength` (0.010) — tuned over 5 seeds on a field whose static half was point sources;
- P_fa4a `scent_acuity` — 클러스터 8b already listed it as a re-measurement target and it was never
  re-measured;
- PD4-v `graze_depletion_per_hunger` (5.0) and the 클러스터 10 re-fit — both read the food field,
  which is about to gain reach.

Land the fix on its own, with before/after on the same beds, exactly as PD11 is required to land
(`docs/plans/fauna.md` PD11 선행 경고): a correctness fix and a re-tune in one commit cannot be
attributed afterwards.

## Reference paths

- `backend/engine/space/scent/SPEC.md` — §Static layer, §Invariants (layer isolation), §Acceptance Criteria
- `docs/plans/fauna.md` — 클러스터 8b (What: resolution + defect status)
- `backend/engine/world/SPEC-world-fauna.md` — §Spoor / scent cadence (world drives the calls)
- commits: 064c930 (introduced), e3f37fc (documented the layer, did not touch the kernel)
