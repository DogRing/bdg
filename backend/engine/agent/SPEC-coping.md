# SPEC — `engine/agent` · Coping & Emotion (P3)
> Scope: Coping cascade, Intelligence-gated object-fixation, Resentment accrual, Aggression Drift, β self-calibration
> Status: P3/P5 · Leaf level: L5 · Owner agent: implementer
> Source: split from `engine/agent/SPEC.md` (monolith)

## 1. Purpose

This file owns the **psychological coping and emotion layer** of the agent decision loop: the
per-agent dynamics that span a dead-end goal into emergent psychological drift. Specifically it owns
the **coping cascade** state machine (`rebind → longing → latent → apathy`), the **Intelligence-gated
object-fixation** shortcut (low-Intelligence agents fixate on a blocked object instead of
re-binding), **Resentment accrual** while resident in `Latent`, the **Aggression Drift** that hardens
self-perceived Aggression once Resentment exceeds threshold, and per-stat **self-calibration** (β,
D8) that folds world-given evidence into `ToM[self]`.

It is part of the orchestrator (D5): the coping logic sequences and threads state, but it computes no
Standing/Priority of its own (that is `engine/values`), assembles no plan (that is `engine/planner`),
and persists belief drift through `engine/tom` primitives (`Observe`, `AdjustAffinity`). The coping
branch and all drift are **deterministic** (D12, rng-free) and **emergent** (D2): Resentment is a
scalar drift feeding the ordinary value gradient, not a hardcoded "grudge" or "enemy" type. Every
rate constant is injected from `content/balance.yaml` (D10); the package hardcodes none, and every
stat id (Intelligence / Vindictiveness / Aggression) is resolved from `Config`, never a literal (D7).

**Out of this file (pointers, NOT duplicated):**
- **Stamina / Adrenaline / Mood** drain/regen/surge/crash mechanics → `SPEC-dynamics.md`. This file
  only *consumes* Mood (Apathy lowers it) and *points at* the budget reduction.
- **Planner-budget sizing / Apathy-reduced `ApathyBudget`** → `SPEC-dynamics.md §A-budget`. Apathy
  lowers the plan budget; the exact `ApathyBudget` sizing lives there.
- **Reliance / Vote / Influence-weighted gossip** → `SPEC-politics.md`.

## 2. CopingState enum and LatentGoal type

Copied verbatim from `types.go`:

```go
type CopingState uint8
const (
    Idle      CopingState = iota
    Rebinding
    Longing
    Latent
    Apathy
)

type LatentGoal struct {
    Dim       core.Dimension
    Since     core.Tick
    Intensity float64
}
```

- `Idle` — pursuing a goal with a valid plan (or no goal raised); the normal state.
- `Rebinding` — no plan found: substitute the next-Priority goal.
- `Longing` — the unmet goal is stored in latent memory; a substitute is searched.
- `Latent` — the goal persists below the surface; **Resentment accrual starts here**.
- `Apathy` — gave up: goal suppressed, Mood decays toward baseline, plan budget reduced.

`LatentGoal` carries the Dimension that could not be satisfied, when it was first deferred (`Since`),
and its residual urgency (`Intensity`) — the latter drives the Aggression-Drift `latentFactor` (§6).

## 3. §A — Coping cascade — full transition logic

The cascade (design §3 — "막다른 목표 = 드라마의 엔진"):

```
Idle
  ↓ (plan fail)
Rebinding   ← only if perceived Intelligence ≥ rebind threshold; else SKIP straight to Latent
  ↓ (n consecutive fails)
Longing     ← goal kept, substitute searched
  ↓ (substitute also fails)
Latent      ← goal deferred, alt-goal pursued; Resentment accrual STARTS here
  ↓ (FailStreak ≥ apathy threshold)
Apathy      ← global Mood↓, plan budget↓; recovers on a SINGLE plan success
```

The cascade advances **one step per failed re-plan** (called when phase-5 planning fails). Which step
is taken is a **pure function** of `(failure cause, perceived Intelligence, FailStreak, prior
CopingState)` — rng-free, deterministic (D12).

### FailStreak

- **Increment** `FailStreak` on **every** failed re-plan (first line of `enterCopingCascade`).
- **Reset** to `0` on **any** plan success or a completed action (handled in the Tick coping success
  path / `ApplyOutcome`, not in `enterCopingCascade`).

### Low-Intelligence shortcut (design §1.4 object-fixation)

If perceived `Intelligence < RebindMinIntelligence (0.4)` (`balance.yaml intelligence.rebind_threshold`),
the agent **cannot enter `Rebinding`** and on the first plan fail goes **directly to `Latent`**,
skipping `Rebinding` AND `Longing`. This is the object-fixation behaviour: a low-Intelligence agent
keeps fixating on the blocked object instead of re-binding to a substitute. High-Intelligence agents
traverse the full `Rebinding → Longing → Latent` path.

> P3 replaces the prior budget-derived rebind threshold with the explicit
> `intelligence.rebind_threshold` constant (cleaner; resolves the prior Open Question) and adds the
> "skip to Latent" shortcut for the low branch.

### Apathy entry

When `Coping == Latent` **and** `FailStreak ≥ ApathyFailStreak` (`balance.yaml coping.apathy_fail_streak`),
enter `Apathy`.

### Apathy effects

- **Mood↓** — Apathy suppresses the active goal so no positive progress term fires; combined with the
  Mood-decay pull and the adrenaline crash this pulls Mood down (the Mood/adrenaline mechanics
  themselves live in `SPEC-dynamics.md §Mood`).
- **Plan budget↓** — while `Coping == Apathy`, `replan` shapes a reduced per-call planner budget
  (see `SPEC-dynamics.md §A-budget` for the exact `ApathyBudget` sizing). Apathy does NOT mutate the
  planner config; it only sets the per-call `snapshot.ApathyBudget` pointer.

### Apathy recovery (single success)

In `ApplyOutcome`, when `Status == Succeeded && Completed == true` while `Coping == Apathy`:

- reset `Coping = Idle`
- reset `FailStreak = 0`
- `Mood += ApathyRecoverMood (+0.15)` (`balance.yaml coping.apathy_recover_mood`)
- emit `CopingEntered(Idle)`

A *different* goal becoming plannable also exits Apathy via the normal phase-5 success path.

### `enterCopingCascade()` — full logic (from `coping.go`)

```
func (a *Agent) enterCopingCascade(err, now, priorities, statsReg, emit):
  a.FailStreak++                                              // increment on every failed re-plan
  perceivedIntel := a.normalizedIntelligence(statsReg)        // [0,1], reads ToM[self] (D8)

  // ── Low-Intelligence shortcut (object-fixation, design §1.4) ──
  if perceivedIntel < a.Cfg.RebindMinIntelligence:
      switch a.Coping:
        Idle, Rebinding, Longing → a.enterLatentDirect(now, emit); return  // skip Rebinding AND Longing
        Latent                   → if a.FailStreak >= a.Cfg.ApathyFailStreak:
                                        a.enterApathy(); emit CopingEntered(Apathy)
                                    return
        Apathy                   → return                                    // stay in Apathy

  // ── High-Intelligence path: full cascade ──
  switch a.Coping:
    Idle:
        if a.canRebind(statsReg):
            a.Coping = Rebinding; emit CopingEntered(Rebinding)
            if a.rebindSubstitute(priorities):                 // substituted the next-Priority goal
                a.Coping = Idle; a.FailStreak = 0; return       // success → back to Idle
            // substitution failed — fall through to Longing
        a.enterLonging(); emit CopingEntered(Longing)          // cannot rebind OR rebind failed
    Rebinding:
        a.enterLonging(); emit CopingEntered(Longing)          // rebind already failed → Longing
    Longing:
        a.Coping = Latent; emit CopingEntered(Latent)          // → Latent
    Latent:
        if a.FailStreak >= a.Cfg.ApathyFailStreak:
            a.enterApathy(); emit CopingEntered(Apathy)         // → Apathy at the streak threshold
        // else: stay Latent, continue accruing Resentment each tick
    Apathy:
        // already in Apathy — stay until single plan success / different goal plannable
```

Supporting methods (all from `coping.go`):

- **`enterLatentDirect(now, emit)`** — the low-Intelligence shortcut target. Appends a `LatentGoal{Dim:
  a.Goal, Since: now, Intensity: a.NeedIntensities[a.Goal]}`, sets `a.Coping = Latent`, clears the
  goal (`a.clearGoal()`), and emits `CopingEntered(Latent)`. Skips Rebinding AND Longing.
- **`canRebind(statsReg) bool`** — returns `a.normalizedIntelligence(statsReg) >= a.Cfg.RebindMinIntelligence`.
  Reads `ToM[self]` (D8); uses the explicit `RebindMinIntelligence` constant (no budget-curve
  derivation).
- **`rebindSubstitute(priorities) bool`** — the first Priority failed, so it tries the second
  (`priorities[1].Dim`); if that equals the current goal it tries the third
  (`priorities[2].Dim`). On a substitute it sets `a.Goal = substituteDim`, clears `a.Plan` /
  `a.PlanIdx` / `a.Elapsed`, and returns `true`; returns `false` if `len(priorities) < 2` or no
  effective substitute exists.
- **`enterLonging()`** — appends a `LatentGoal{Dim: a.Goal, Since: 0, Intensity:
  a.NeedIntensities[a.Goal]}`, sets `a.Coping = Longing`, and clears the goal. (Does NOT emit; the
  caller emits `CopingEntered(Longing)`.)
- **`enterApathy()`** — sets `a.Coping = Apathy`, clears the goal, crashes `a.Adrenaline = 0`, and
  clears the latent goals (`a.Latent = nil` — the agent has given up on them). (Does NOT emit; the
  caller emits `CopingEntered(Apathy)`.)

### `CopingEntered` event (data-contracts §4)

`emitCopingEntered` emits one `CopingEntered` event carrying a `mode` string (NOT `int(state)`). The
canonical mode strings (`copingModeString`):

| CopingState | mode string |
|-------------|-------------|
| `Idle`      | `idle`      |
| `Rebinding` | `rebind`    |
| `Longing`   | `longing`   |
| `Latent`    | `latent`    |
| `Apathy`    | `apathy`    |

`idle` is emitted on Apathy recovery; `rebind`/`longing`/`latent`/`apathy` on the corresponding
cascade transitions.

## 4. §C — Apathy ↔ gates coupling

- Each tick the agent injects its **live Mood** into the planner/gate `AgentSnapshot` (the
  `Mood`/`Stamina`/`Adrenaline`/`Urgency` body scalars on `gates.AgentSnapshot`,
  `engine/gates/SPEC.md`). The Mood value itself is owned/computed in `SPEC-dynamics.md §Mood`; coping
  is the consumer that drives it down via Apathy.
- When `Mood ≤ gates.apathy_mood_threshold (−0.60)`, the `apathy` gate makes `abstraction:med+`
  actions **invisible** — so a deeply apathetic agent's planner sees a narrowed action set for free.
- The gate does this **from the injected Mood**; there is **no special-case in the agent** (D4/D10).
- A single plan success (Apathy recovery, §3) bumps `Mood += ApathyRecoverMood`, which — once Mood
  climbs above `−0.60` — **re-widens the visible action set** on the next tick (the gate re-admits
  `abstraction:med+`).

> See `SPEC-dynamics.md §C` for the Mood ≤ −0.60 threshold detail and the snapshot body-scalar
> injection; coping only owns the cause (Apathy lowers Mood, recovery raises it).

## 5. §B — Resentment accrual (Latent residence)

While `Coping == Latent`, each tick a **trigger event** may arrive (a rejection without an Offer in
return, or losing a resource conflict — both reported by the world via `ApplyOutcome` /
`WorldView.ResentmentTriggers`):

```
Resentment += resentment.per_trigger × Vindictiveness         (resentment.per_trigger = 0.05)
Affinity[toward trigger agent] -= AffinityDrop × Vindictiveness (balance.yaml resentment.affinity_drop)
```

- `Vindictiveness` = `ToM[self].EstStats[VindictivenessStatID].Mean` (D8 — read self-belief; the stat
  id resolved from `cfg.VindictivenessStatID` via `resolveVindictivenessStatID`, never a
  `"Vindictiveness"` literal — D7).
- The per-trigger Affinity drop **persists** on the actual `Belief` toward the trigger agent via
  `a.ToM.AdjustAffinity(triggerID, −AffinityDrop × Vindictiveness)` (the tom storage primitive — it
  writes through, not on a copy). A `BeliefUpdated` event records the Affinity drop (data-contracts
  §4).
- Resentment is **emergent, not a hardcoded grudge type** (D2): it is a scalar drift that feeds the
  normal value gradient (avoidance/aggression), not an "enemy" object.

### `accrueResentment(triggers, statsReg, emit)` — method spec (from `coping.go`)

```
func (a *Agent) accrueResentment(triggers []core.AgentID, statsReg, emit):
  if a.Coping != Latent: return                                // only while Latent
  if len(triggers) == 0: return                                // early return: no triggers, nothing to drop

  vindictivenessStatID := resolveVindictivenessStatID(a.Cfg)   // D7-resolved from cfg.VindictivenessStatID
  vindictiveness       := a.perceivedStat(vindictivenessStatID)// D8: ToM[self].EstStats[…].Mean

  for triggerID in triggers:                                   // triggers are AgentID-stable order (D12)
      a.Resentment += a.Cfg.ResentmentPerTrigger * vindictiveness
      affinityDelta := -a.Cfg.AffinityDrop * vindictiveness
      a.ToM.AdjustAffinity(triggerID, affinityDelta)           // persists on the real Belief
      emit BeliefUpdated{
          subject:    string(triggerID),
          field:      "Affinity",
          delta:      affinityDelta,
          resentment: a.Resentment,
      }                                                        // guarded by emit != nil
```

> **The Aggression-Drift threshold check is NOT in `accrueResentment`** — it was MOVED to
> `updateResentment` (§6 / §B-drift gap-closure). `accrueResentment` owns ONLY the trigger-gated
> Affinity drop + Resentment accumulation. Net effect: the Affinity drop is **trigger-gated** (it
> needs a specific trigger agent to drop toward); the Aggression drift is **threshold-gated and
> time-continuous** (§6).

## 6. §B-drift — Aggression Drift (gap-closure ③)

This was gap-closure issue ③. The Resentment-threshold → Aggression-Drift fold runs **every tick**,
regardless of whether new triggers arrived, so it MUST live in `updateResentment()`, NOT
`accrueResentment()`.

- **Location**: `updateResentment()` in `dynamics.go`, called **every tick** from `updateDynamics`
  (phase 8). It is **NOT gated by trigger arrival**.
- **Trigger**: `if a.Resentment > a.Cfg.ResentmentThreshold` AND `len(a.Latent) > 0` (an early
  `if len(a.Latent) == 0 { return }` guards the no-latent-goals case so `latentFactor == 0` ⇒ no
  drift).
- **Formula**:

  ```
  latentFactor := clamp01(Σ over lg in a.Latent of lg.Intensity)   // 0 if no latent goals → no drift
  if a.Resentment > a.Cfg.ResentmentThreshold:
      aggStatID  := resolveAggressionStatID(a.Cfg)                 // D7-resolved, never a literal
      selfID     := a.ToM.SelfID()
      currentAgg := a.perceivedStat(aggStatID)
      a.ToM.Observe(selfID, tom.StatEvidence{
          Stat:     aggStatID,
          Observed: currentAgg + a.Cfg.AggressionDrift * latentFactor,
          Weight:   0.5,
          Tick:     0,
      })
  ```

  The drift magnitude is `AggressionDrift × latentFactor` (the residual latent intensity scales it;
  with no latent goals `latentFactor == 0` so no drift — preserving the old "latent residence"
  intent). This runs **regardless of triggers**, so a steeped grudge keeps hardening Aggression even
  on quiet ticks.

- **NOT in `accrueResentment`**: the per-tick Aggression-Drift threshold check was MOVED from
  `accrueResentment` (where it sat **behind** the `if len(triggers) == 0 { return }` early return,
  causing the gap — the drift never applied on threshold-exceeding ticks with no fresh trigger) to
  `updateResentment`, so it fires on trigger-free ticks. Keeping it in both places would
  **double-apply** the drift on a trigger tick that also exceeds the threshold; the check is in
  `updateResentment` ONLY.
- **Stat IDs** are resolved from `cfg.AggressionStatID` / `cfg.VindictivenessStatID` — never literals
  (D7) — via the helpers:

  ```go
  func resolveAggressionStatID(cfg Config) core.StatID    { return cfg.AggressionStatID }
  func resolveVindictivenessStatID(cfg Config) core.StatID { return cfg.VindictivenessStatID }
  ```

> Why it moved: the drift is a function of accumulated `Resentment` (a continuous scalar), not of
> this tick's triggers — so it must run every tick. Leaving it behind `accrueResentment`'s
> `len(triggers)==0` early return meant a steeped grudge stopped hardening on quiet ticks. The
> Affinity drop stays trigger-gated (it needs a specific target).

## 7. Self-calibration (β, D8)

The self-calibration layer folds world-given evidence into `ToM[self]` after a resolved action.

### `foldEvidence(outcome ActionOutcome)` (from `coping.go`)

```
func (a *Agent) foldEvidence(outcome ActionOutcome):
  if outcome.Status == Invisible || outcome.Status == Interrupted:
      return                                       // D8: underestimation is self-sealing — NO fold
  selfID := a.ToM.SelfID()
  for ev in outcome.Evidence:                      // world-precomputed StatEvidence
      a.ToM.Observe(selfID, ev)                     // β fold into ToM[self]
```

- On **Invisible / Interrupted** → NO evidence is folded (self-sealing, D8): an action a gate hid, or
  one cut short, generates no self-belief update, so an agent's underestimation is self-sealing and
  must NOT be "corrected" away.
- The evidence is **pre-computed by the world** from the action outcome; the agent NEVER reads
  `RealStats` here — it only applies the evidence items it is handed (D8).
- `Beta` (`balance.yaml self_calibration.beta`) is the calibration rate the `tom.Observe` fold uses;
  it is threaded via `Config` and consumed inside `engine/tom` (the agent supplies the evidence, not
  the rate math).

### Accessors (D8 — read `ToM[self]`, never RealStats)

```go
// normalizedIntelligence returns self-perceived Intelligence normalized to [0,1] by
// dividing by the stat's Max range (uses cfg.IntelligenceStatID; falls back to raw if
// no Def/Max). Used by the rebind threshold + budget sizing.
func (a *Agent) normalizedIntelligence(statsReg *stats.Registry) float64

// perceivedIntelligence returns the raw self-perceived Intelligence from ToM[self].
// Resolves cfg.IntelligenceStatID; if empty, falls back to the first Capability stat.
func (a *Agent) perceivedIntelligence(statsReg *stats.Registry) float64

// perceivedStat returns ToM[self].EstStats[statID].Mean (0 if no self-belief / no stat).
func (a *Agent) perceivedStat(statID core.StatID) float64
```

`perceivedStat` is the single read primitive: `a.ToM.Self(a.ToM.SelfID()).EstStats[statID].Mean`.
Vindictiveness, Aggression, and Intelligence all read through it (D8); none reads `RealStats`.

## 8. Config fields (coping-relevant)

Copied verbatim from `agent.go` (`Config` struct):

```go
// beta self-calibration (D8): applied ONLY on a resolved attempt, per used stat.
Beta float64 // balance.yaml self_calibration.beta

// resentment drift fed by latent goals
AffinityDrop    float64 // balance.yaml resentment.affinity_drop
AggressionDrift float64 // balance.yaml resentment.aggression_drift

// coping cascade
RebindMinIntelligence float64 // balance.yaml intelligence.rebind_threshold
ApathyFailStreak      int     // balance.yaml coping.apathy_fail_streak
ApathyRecoverMood     float64 // balance.yaml coping.apathy_recover_mood
ApathyBudgetPenalty   float64 // balance.yaml coping.apathy_budget_penalty (→ SPEC-dynamics §A-budget)

// resentment accrual
ResentmentPerTrigger float64 // balance.yaml resentment.per_trigger
ResentmentThreshold  float64 // balance.yaml resentment.threshold

// D10: stat IDs resolved from the stats registry (no hardcoded glossary literals in engine code).
IntelligenceStatID    core.StatID // capability stat for Intelligence lookups
VindictivenessStatID  core.StatID // disposition stat for Vindictiveness lookups
AggressionStatID      core.StatID // disposition stat for Aggression lookups

// cross-tick goal-switching anti-thrash
Stickiness   float64 // bonus to the current goal's Priority (anti-thrash)
GoalDeadband float64 // don't switch goals until a rival beats current by this margin
```

> `ApathyBudgetPenalty` is consumed by `replan` to size the per-call `ApathyBudget` — the exact
> sizing is owned by `SPEC-dynamics.md §A-budget`. This file owns only that Apathy *enables* the
> reduction.

### DefaultConfig values (from `agent.go` `DefaultConfig()`)

| field | default |
|-------|---------|
| `Beta` | `0.08` |
| `AffinityDrop` | `0.15` |
| `AggressionDrift` | `0.02` |
| `RebindMinIntelligence` | `0.4` |
| `ApathyFailStreak` | `3` |
| `ApathyRecoverMood` | `0.15` |
| `ApathyBudgetPenalty` | `0.5` |
| `ResentmentPerTrigger` | `0.05` |
| `ResentmentThreshold` | `0.30` |
| `Stickiness` | `0.15` |
| `GoalDeadband` | `0.08` |
| `IntelligenceStatID` | `"Intelligence"` (test default; platform/config resolves from registry) |
| `VindictivenessStatID` | `"Vindictiveness"` (test default; platform/config resolves from registry) |
| `AggressionStatID` | `"Aggression"` (test default; platform/config resolves from registry) |

> The `*StatID` defaults are glossary-canonical IDs used **only** when config is not loaded from
> content (tests). When content IS loaded, `platform/config` resolves these from the registries;
> `engine/agent` code never hardcodes the ID literals in logic (D7/D10).

## 9. Dependencies (coping-relevant)

- `engine/tom` — `ToM.Observe` (β self-calibration fold + the §B-drift Aggression fold);
  `ToM.AdjustAffinity` (the persisted Resentment Affinity drop, writes through the real `Belief`);
  `ToM.Self` / `ToM.SelfID` (reads `ToM[self]`, D8); `tom.StatEvidence`.
- `engine/stats` — `*Registry` (capability set for Intelligence normalization;
  **Vindictiveness** / **Aggression** disposition ids resolved from `Config`, never a literal — D7).
- `engine/planner` — sentinel errors `ErrUnreachable` / `ErrBudgetExceeded` (the cascade trigger,
  observed at the planner boundary in phase 5); the planner budget reference (Apathy reduction →
  `SPEC-dynamics.md §A-budget`); `planner.DimensionPriority` (passed to `rebindSubstitute`).
- `content/balance.yaml` — `resentment.*` (`per_trigger`, `threshold`, `affinity_drop`,
  `aggression_drift`), `coping.*` (`apathy_fail_streak`, `apathy_recover_mood`,
  `apathy_budget_penalty`), `intelligence.rebind_threshold`, `self_calibration.beta`. Injected via
  `platform/config` (D10).
- **Contract — NOT imported**: `engine/world` implements `WorldView` (dependency inversion); it
  supplies the resentment triggers via `WorldView.ResentmentTriggers(self)`.

## 10. Invariants (coping-relevant)

- **Self-calibration (D8)**: the β fold only runs on Succeeded/Completed evidence; on
  Invisible/Interrupted NO evidence is folded (self-sealing — underestimation is not "corrected"
  away). `Tick`/coping never reads `RealStats`.
- **Coping branch is deterministic (D12)**: the cascade step is a pure function of `(failure cause,
  perceived Intelligence, FailStreak, prior CopingState)` — rng-free; no rng draw selects the branch.
- **Resentment is emergent (D2)**: Resentment is a scalar drift feeding the value gradient
  (avoidance/aggression), not a hardcoded "enemy"/grudge type; the Aggression drift is threshold-gated
  and time-continuous.
- **Stat ids injected (D7)**: `IntelligenceStatID`, `VindictivenessStatID`, `AggressionStatID` are
  resolved from `Config` (via `resolveVindictivenessStatID` / `resolveAggressionStatID` /
  `perceivedIntelligence`); no `Intelligence`/`Vindictiveness`/`Aggression` literal in logic.
- **Affinity drops persist (not on a copy)**: `a.ToM.AdjustAffinity` writes through to the real
  `Belief`; the prior "mutates a copy" concern no longer applies.
- **Aggression Drift runs every tick (not just on trigger events)**: the gap-closure invariant —
  `updateResentment` fires from `updateDynamics` regardless of `len(triggers)`, so a steeped grudge
  keeps hardening Aggression on quiet ticks.
- **No double-application of Aggression Drift**: the threshold check is in `updateResentment` ONLY
  (REMOVED from `accrueResentment`); a trigger tick that also exceeds threshold drifts by exactly one
  step, not two.
- **Bounds**: `Resentment ≥ 0`; `FailStreak ≥ 0`; `latentFactor ∈ [0,1]` (clamp01).

## 11. Acceptance Criteria (testable)

Copied verbatim from the monolith (coping/resentment ACs only):

- [ ] **Low-Intelligence skips Rebinding to Latent (scenario F, golden)**: an agent with perceived
  `Intelligence = 0.3` (< `RebindMinIntelligence = 0.4`) on a plan fail emits **one**
  `CopingEntered(Latent)` with **no** `CopingEntered(Rebinding)` and no `Longing`. A
  perceived-`Intelligence = 0.7` agent emits `CopingEntered(Rebinding)` first and (on repeated fail)
  `Longing → Latent`. Golden over the event sequence.
- [ ] **Cascade order + FailStreak (design §3)**: a contrived `ErrUnreachable` sequence drives a
  high-Intelligence agent `Idle → Rebinding → Longing → Latent → Apathy`; `FailStreak` increments
  per fail and resets to 0 on a success; `Apathy` is entered only at `FailStreak ≥ ApathyFailStreak`.
- [x] **Latent Resentment trigger drops Affinity (golden)**: an agent in `Latent` that receives a
  `ResentmentTriggers == [B]` event accrues `Resentment += per_trigger × Vindictiveness` and the
  persisted `Affinity` toward `B` drops by `AffinityDrop × Vindictiveness`; an empty trigger list
  causes no Affinity drop; a non-Latent agent does not accrue. A `BeliefUpdated` event records the
  Affinity drop.
- [ ] **Resentment threshold drives Aggression drift EVERY tick (gap-closure)**: once `Resentment >
  ResentmentThreshold`, `updateResentment` (called every tick from `updateDynamics`) folds
  `AggressionDrift × latentFactor` into `ToM[self]` Aggression **even on a tick with NO new
  triggers** (the previous bug: the check sat behind `accrueResentment`'s `len(triggers)==0` early
  return, so quiet ticks never drifted). Assert: (a) with `Resentment > threshold` and an empty
  trigger list, Aggression still rises; (b) `accrueResentment` no longer contains the threshold
  check (no double-apply on a trigger tick — Aggression rises by exactly one drift step, not two);
  (c) with no latent goals (`latentFactor == 0`) no drift applies. Table-driven over the boundary.
- [ ] **Apathy single-success recovery (golden)**: an `Apathy` agent receiving
  `ApplyOutcome{Status: Succeeded, Completed: true}` resets `Coping = Idle`, `FailStreak = 0`,
  `Mood += ApathyRecoverMood`, and emits `CopingEntered(Idle)`.
- [ ] **Apathy narrows cognition via the gate**: an `Apathy` agent whose `Mood ≤ -0.60` builds a
  snapshot with `Mood ≤ -0.60`; the planner does not select an `abstraction:med`+ action. Once Mood
  recovers above `-0.60`, the abstract action is visible again. (Mood ≤ −0.60 threshold detail →
  `SPEC-dynamics.md §C`.)
- [ ] **No constant / id hardcoded (D10/D7 guard)**: grep guard — no rebind/apathy/resentment literal
  and no `Intelligence`/`Vindictiveness`/`Aggression`/`Mood` string literal in coping logic (all
  registry-/config-resolved).

## 12. Open Questions (coping-relevant)

- **`engine/tom` Affinity write path (RESOLVED in code).** `accrueResentment` persists the Resentment
  Affinity drop via `a.ToM.AdjustAffinity(triggerID, delta)`; the prior "mutates a copy" concern no
  longer applies. (The §B-drift gap-closure moves only the Aggression-Drift threshold check to
  `updateResentment`; the Affinity-drop path is unchanged.)
