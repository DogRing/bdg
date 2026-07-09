# SPEC — `engine/world` · Emergent Detection & ToM Pruning

> Status: `READY`
> Sub-spec of: `SPEC.md`  ·  Owner agent: `implementer`

## Scope

This sub-spec covers **emergent reliance-cluster detection** (`RoleEmerged`, D2 — detection only,
no role type) and **ToM pruning** (scale: bounded O(N²) ToM memory). These run after the apply
phase each tick. The tick loop mechanics that precede them live in [`SPEC-tick.md`](SPEC-tick.md).

---

## Emergent reliance-cluster detection (D2 — detection only, no role type)

After the apply phase, scan reliance edges across agents to detect emergent functional roles
(design §1.6 "창발 제도": 역할은 자료형이 아니다).

### Full scan (P6 — ACTIVATED)

`engine/mind/tom` formalizes `type Function string` and `Belief.RelyOn map[Function]float64` (see
`engine/mind/tom/SPEC.md` §P6 Reliance & Influence Contract), so the P1 no-op stub is **replaced** by
the scan below. The share threshold is the P6 key `politics.role_convergence_threshold`
(`Config.RoleConvergenceThreshold`), which **supersedes** the P1 placeholder
`world.reliance_threshold` (the old key/field is retired).

```
RelyOn edges live on each agent's ToM ABOUT OTHERS: agent x relies on holder h for f iff
x.ToM[h].RelyOn[f] is the strongest such edge x holds for f (x's chosen provider for f).

per Function f, in sorted Function order over the union of functions referenced across agents (D12):
    for each agent x (AgentIDs() order, D12):
        h* = argmax_h x.ToM[h].RelyOn[f]   (the holder x relies on most for f; skip if max == 0)
        votes[f][h*]++
    holder  = argmax_h votes[f][h]          (ties → lower AgentID, D12)
    share   = votes[f][holder] / agent_count
    if share ≥ cfg.RoleConvergenceThreshold AND (f,holder) was NOT already emerged last scan:
        emit RoleEmerged{function: f, holder: holder, reliance_share: share}   // RISING EDGE only
    if share < cfg.RoleConvergenceThreshold: clear (f,*) from the emerged set   // allow re-emergence
```

- **Emit on the RISING EDGE only.** The world keeps a small `emerged map[Function]AgentID` of the
  currently-emerged (function→holder) pairs and emits `RoleEmerged` only when a (function,holder)
  pair first crosses the threshold — NOT every tick it stays converged (keeps the event stream and
  the Scenario-G golden clean). A holder change for the same function (succession) is a new rising
  edge → a new event. Dropping below the threshold clears the entry so a later re-convergence emits
  again. The `emerged` set is owned state, serialized with the world (so resume stays byte-identical).
- This is **emergent detection ONLY**: the world defines **no** `Role` type, no chief/leader enum,
  no institution (D2). It reads the `RelyOn` distribution that `engine/agent` maintains on
  `tom.Belief` and reports a statistic. The grep/struct guard (Invariants) forbids a `Role` type
  here.
- Deterministic: functions, agents, and holders are iterated/argmax'd in sorted order; ties break
  by lower `AgentID`; no rng.

Activation checklist (P6 — all met):
- [x] `engine/mind/tom` formalizes `type Function string` and `Belief.RelyOn map[Function]float64` (§P6).
- [x] `engine/agent` populates `RelyOn` edges during deliberation (see `engine/agent/SPEC.md` §P6
  VoteAction & reliance trigger).
- [x] `content/balance.yaml politics.role_convergence_threshold` is present (Config source in
  `SPEC.md` §Public Interface).
- [x] Scenario G fixture + golden recorded (`docs/core/testing.md §4`; AC below).

---

## ToM pruning (scale — bounded O(N²) ToM memory)

To keep per-agent ToM size bounded as the population and runtime grow, the world runs a periodic
**prune pass** that drops stale subject beliefs. Each `tom.Belief` already tracks `LastSeen` (the
tick of the last direct observation; see `engine/mind/tom/SPEC.md`). The prune pass uses that gap.

```
prune pass — runs after the reliance scan, ONLY on ticks where
    cfg.PruneInterval > 0 AND w.tick mod cfg.PruneInterval == 0:

for each agent a in AgentIDs() (sorted, D12):
    for each subject s in a.ToM.Subjects() (sorted, D12):
        if s == a.ToM.SelfID(): continue                 // never prune ToM[self] (D8)
        if (w.tick - a.ToM[s].LastSeen) > cfg.PruneThreshold:
            decay a.ToM[s].Belief by ×cfg.PruneDecayFactor    // 0.0 ⇒ zero out
            remove subject s from a.ToM                        // then drop the entry entirely
```

- **Decay then remove.** The Belief is multiplied by `cfg.PruneDecayFactor` and then the map entry
  is removed. With the default `PruneDecayFactor = 0.0` the decay zeros the Belief before removal
  (a no-op observable difference vs straight removal, but the hook is in place for a future fade
  feature). A value like `0.5` would let a faint trace persist if a later phase chooses to keep
  decayed-but-nonzero entries; for P1 the entry is always removed after decay.
- **Re-encounter re-initializes (NOT a bug).** If a pruned subject is later perceived again (Sight
  returns them), the next observation calls the SAME first-encounter initialization path
  (`engine/mind/tom` initial-estimate seed via `Observe`/`GossipUpdate` on an unknown subject) — there
  is **no** special "warm-start" branch. The re-seeded Belief uses the engine/mind/tom defaults
  (LastSeen updated to the current tick; EstStats reset to the prior-from-perception seed). This is
  the intended behaviour: forgetting is real, and re-acquaintance starts fresh.
- **Self is never pruned (D8).** `ToM[self]` is exempt — self-perception is calibrated only by
  action, never aged out.
- **Deterministic (D12):** the prune pass iterates agents in `AgentIDs()` (sorted) order and,
  within each agent, subjects in `ToM.Subjects()` (sorted) order; no `map` is ranged for logic.
  No rng.

---

## Invariants (emergent detection · ToM pruning scope)

- **No hardcoded meta-system / role type (D2)**: there is **no** `Role`/`Chief`/`Faction`/`Crime`
  type in this package. `RoleEmerged` is a derived statistic over `RelyOn`; a struct/grep guard
  confirms no institution type exists.
- **Reliance scan is deterministic (D12)**: functions, agents, and holders are iterated/argmax'd
  in sorted `Function` / `AgentID` order; argmax-holder ties break by lower `AgentID`; no rng.
  The `emerged` set is serialized with the world (so resume is byte-identical even for
  convergence state).
- **ToM prune re-init (engine/mind/tom contract)**: a pruned ToM subject is decayed by
  `cfg.PruneDecayFactor` then removed from the ToM map; a later re-encounter calls the SAME
  first-encounter initialization path (`engine/mind/tom` unknown-subject seed) — there is no special
  "warm-start" branch. `ToM[self]` is never pruned (D8). The prune pass iterates agents and
  subjects in sorted order (D12).

---

## Acceptance Criteria (emergent · ToM prune)

- [ ] **No `Role` type exists (D2 guard, all phases)**: a struct/grep guard confirms NO
  `Role`/`Chief`/`Faction`/institution type in `engine/world`; `RoleEmerged` carries only
  `{function, holder, reliance_share}` — a statistic over `RelyOn`, never a stored role object.
- [ ] **Reliance full scan emits RoleEmerged on the rising edge (P6, D2)**: with agents whose
  `ToM[h].RelyOn["safety"]` points a super-threshold share at one holder `h`, `relianceScan`
  emits exactly **one** `RoleEmerged{function:"safety", holder:h, reliance_share:share}` on the
  tick the share first crosses `cfg.RoleConvergenceThreshold`, and **nothing** on subsequent ticks
  while it stays converged (rising-edge debounce via the owned `emerged` set). Share below
  threshold → no emission and the entry clears. Holder change at/above threshold → a new event
  (succession). Deterministic over two runs (D12); tie-break by lower `AgentID`.
- [ ] **Scenario G — chained theft → RoleEmerged (P6, GOLDEN)**: the integration chain end-to-end.
  Setup (fixture, fixed seed): several villagers + one capable guardian near a `village_center`; a
  low-asset agent commits repeated `Take` (chained theft) so each victim's `Safety` intensity rises
  (collective Safety drops). Per `engine/agent` §P6, victims whose **safety Function** is
  self-unsolvable (gate-blocked / cost > threshold) `AdjustRelyOn` toward the guardian
  (`BestProviderFor("safety", …)`), optionally accelerated by `Vote`; the guardian's
  `defensiveCollectiveGoal` fires → it `Patrol`s. Assertions:
  - direction: mean collective Safety intensity rises across the theft window; the guardian executes
    ≥1 `Patrol`; the guardian's received-RelyOn share for `"safety"` crosses
    `RoleConvergenceThreshold`.
  - **exactly one** `RoleEmerged{function:"safety", holder:guardian, reliance_share:s}` on the
    rising edge; none on the converged ticks that follow.
  - **golden**: the `RoleEmerged` payload (`function`, `holder`, `reliance_share` to fixed
    precision) and its tick, byte-stable across two runs (D12). Recorded under
    `testdata/golden/scenario_g_*.json`.
- [ ] **ToM prune removes after threshold (table-driven)**: after `prune_threshold` ticks of no
  Sight contact with a subject, the subject's ToM entry is removed. Table: a gap JUST BELOW
  threshold → the entry is still present; AT/ABOVE threshold → the entry is decayed (×
  `prune_decay_factor`) then removed. `ToM[self]` is never pruned regardless of gap.
- [ ] **Prune re-encounter re-initializes (NOT a bug)**: after a subject is pruned, re-encountering
  the agent (Sight returns them → next `Observe`) re-initializes the ToM entry to the first-encounter
  defaults (`LastSeen` updated to the current tick; Belief reset to the prior-from-perception seed,
  not the pre-prune values). Asserts the SAME initialization path is taken, with no warm-start
  branch.
- [ ] **Prune iteration order is sorted (D12)**: the prune pass iterates agent IDs in sorted order
  and, within each agent, ToM subjects in sorted order — verified by logging the iteration order in
  a determinism test and matching it to `AgentIDs()` / `ToM.Subjects()`. Two runs are byte-identical.

---

## Notes — Scale (ToM prune)

- **`prune_decay_factor = 0.0` means "zero then remove".** The default multiplies a stale Belief
  by 0 (zeroing it) and then removes the map entry — observationally the same as a straight removal
  at P1, but the decay hook is deliberately in place. A future value like `0.5` would let a Belief
  fade gradually (a faint, decaying rumor trace) before removal — useful for rumor-persistence
  scenarios — and would be enabled behind a feature flag once the "keep decayed-but-nonzero" path
  is specified. For P1 the entry is always removed after the decay multiply.
- **Re-encounter after prune is intentional forgetting.** Because the prune pass removes the subject
  entirely, a later Sight contact re-seeds the Belief through the same unknown-subject initialization
  `engine/mind/tom` uses on a first encounter. The agent "forgets" and then "re-learns" — this is the
  designed memory bound, not a regression. Tests assert the re-init path is taken (no warm-start).
