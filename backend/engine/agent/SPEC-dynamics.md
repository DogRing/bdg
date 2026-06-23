# SPEC — `engine/agent` · Dynamics (P3)
> Scope: Stamina drain/regen, Adrenaline surge/crash, Mood baseline pull, Apathy budget reduction
> Status: P3/P5 · Leaf level: L5 · Owner agent: implementer
> Source: split from `engine/agent/SPEC.md` (monolith)

## 1. Purpose

This file owns the **physiological dynamics** of the agent — the biological / metabolic state
machines that span the decision loop. It covers the **Stamina** drain/regen loop, the **Adrenaline**
surge/crash loop (including the crash → Stamina-debt + Mood-dip coupling), the **Mood** baseline pull,
and the **Apathy-reduced planner budget** that shapes the planner call.

These are the per-agent Body dynamics the agent feeds into the gate/planner snapshot each tick so the
P3 gates (`stamina`, `apathy`, `adrenaline`-cost) can read them (`engine/gates/SPEC.md`). The agent
**writes** the live Body scalars (`Stamina`, `Mood`, `Adrenaline`, `Urgency`) into the snapshot; it
never reads them back from the gate registry, and it never applies the gate predicates itself (those
live in `engine/gates`). All rate constants are injected from `content/balance.yaml` (D10); this
domain hardcodes none.

**What is NOT here (pointers, no duplication):**
- **Resentment accrual + the Aggression-Drift threshold check** → `SPEC-coping.md` §B / §B-drift.
  Only the **Mood dip** side-effect of the Adrenaline crash is owned here (the dip math lives in
  `updateDynamics`).
- **Coping cascade transitions** (Rebinding → Longing → Latent → Apathy) → `SPEC-coping.md` §A.
- **Apathy recovery** (single plan success) → `SPEC-coping.md` §A.
- **The gate predicates themselves** (`stamina`/`apathy`/`adrenaline`) → `engine/gates`.
- **Reliance / Vote / Influence (politics)** → `SPEC-politics.md`.

`updateDynamics` is called in **phase 8** of the loop. The exact 8-phase ordering and the snapshot
construction (`replan`) live in `SPEC-core.md`; this file owns the formulas those phases run.

## 2. §D — Stamina loop (drain / regen / gate)

VERBATIM from the monolith:

```
DRAIN  (in execute, per acting tick):
  effortLevel = tag_levels.effort[<the action's effort:* tag>]   (none=0, low=.20, med=.50, high=.90)
  Stamina -= DrainPerEffort × effortLevel                        (DrainPerEffort = stamina.drain_per_effort)

REGEN  (while executing a recovery action):
  if the action's effect_per_minute names Rest (Rest action):  Stamina += RegenRest  × tickMinutes
  if it is Sleep (stronger Rest supply):                        Stamina += RegenSleep × tickMinutes
  clamp: Stamina ∈ [0, StaminaMax]   (StaminaMax = 1.0)

GATE   (visibility, via engine/gates):
  Stamina < gates.stamina_effort_high_threshold (0.20) → effort:high actions INVISIBLE
  (the agent does not branch on this; it injects Stamina into the snapshot and the `stamina`
   gate hides the action — D4/D10).
```

- Current `drainStamina` hardcoded a `0.5` default effort and a literal effort-level table
  (`parseEffortLevel`). **P3 resolves effort level from the injected `tag_levels.effort` config**
  (no literals — D10) and defaults `effort:none` to `0`, not `0.5`.
- Rest/Sleep detection is **data-driven** (the action carries `effort:none` and an
  `effect_per_minute` on the `Rest` dimension) — resolved via the actions registry, no `"Rest"`/
  `"Sleep"` action-id literal in logic (D7/D10). The two regen rates distinguish Rest vs Sleep by
  the magnitude of their `Rest` `effect_per_minute` (Sleep > Rest), or by an injected mapping.

P3 completion note: effort-level data-driven, no literal (D10); no action-id literal (D7). The gate
predicate is owned by `engine/gates` (the agent only injects the `Stamina` scalar) — see §6.

### Method specs

**`applyStaminaDelta(actionID, actReg, tickMinutes)`** — applies Stamina drain (for effort) and regen
(for Rest/Sleep) for one tick of executing `actionID`. Called from `execute` (phase 6) on both the
first (`IntentStart`) and continuing (`IntentContinue`) acting ticks.

```
def, ok := actReg.Get(actionID)        // nil registry / unknown action → no-op
effortLevel := a.resolveEffortLevel(def.Tags)
drain := a.Cfg.DrainPerEffort * effortLevel * float64(tickMinutes)

regen := 0.0
if effortLevel == 0 && a.hasRestEffectPerMinute(def) {   // recovery actions have zero effort
    regen = a.resolveRegenRate(def)
}

a.Stamina = clamp(a.Stamina - drain + regen, 0, a.Cfg.StaminaMax)
```

**`resolveEffortLevel(tags) float64`** — returns the effort level from the action's tags using the
injected `EffortLevels` map (P3: data-driven, no hardcoded literal — D10). Iterates the action's
tags; returns the first tag found in `a.Cfg.EffortLevels`. Defaults to `0.0` (no effort tag →
`effort:none = 0`).

**`hasRestEffectPerMinute(def) bool`** — returns true iff the action has an `effect_per_minute` on the
**Rest** dimension. P3: data-driven, no action-id literal (D7/D10). The Rest dimension is resolved
from `a.Cfg.RestDim` (injected by platform/config — no hardcoded `"Rest"` literal). The caller must
verify zero effort level separately via `resolveEffortLevel`.

```
_, hasRest := def.EffectPerMinute[a.Cfg.RestDim]
return hasRest
```

**`resolveRegenRate(def) float64`** — returns the regen rate for a recovery action, distinguishing
Rest vs Sleep by the magnitude of the `Rest` `effect_per_minute` (Sleep > Rest). P3: data-driven, no
action-id literal.

```
restRate, ok := def.EffectPerMinute[a.Cfg.RestDim]   // not present → 0
// Sleep has a higher Rest effect_per_minute (0.0030) vs Rest (0.0010).
// Use the magnitude to distinguish: >= 0.0030 → Sleep, otherwise Rest.
if restRate >= 0.0030 {
    return a.Cfg.RegenSleep
}
return a.Cfg.RegenRest
```

> The `0.0030` Sleep-threshold is the content-authored discriminator between the Rest and Sleep
> recovery actions (Sleep's `Rest` `effect_per_minute` is `0.0030`; Rest's is `0.0010`). It is a
> content fact read off the action def, not a balance constant; the two regen *rates* (`RegenRest`,
> `RegenSleep`) it selects are injected (D10).

## 3. §E — Adrenaline loop (surge / crash / stamina debt / cost)

VERBATIM from the monolith:

```
Urgency = clamp01( max Salience across Dimensions × UrgencyFromDeficit )

SURGE: if Urgency > AdrTriggerUrgency (0.65):
           Adrenaline = min(AdrMax, Adrenaline + AdrSurge)
CRASH: else:
           Adrenaline = max(0, Adrenaline − AdrDecay)
           Stamina   -= AdrDecay × adrenaline.crash_stamina_penalty   (0.50)   ← NEW stamina debt
           Mood      += Lambda × (−AdrDecay)                          (the post-surge dip, existing)

EFFECT (via engine/gates, NOT a branch here):
  Adrenaline ≥ 0.70 → the `adrenaline` gate sets Result.CostMultiplier = 0.50 for
                      effort:high / risk:high / violent:* actions (the planner applies it).
  After the crash, Stamina < 0.20 → effort:high returns to invisible via the `stamina` gate.
```

- The agent **does not reach into the gate registry**; it only writes `Adrenaline`/`Stamina` into
  the snapshot. The cost discount and the post-crash hiding are pure consequences of the injected
  body scalars + the P3 gates (D2: no hardcoded adrenaline meta-system).
- The crash stamina debt (`Stamina -= AdrDecay × crash_stamina_penalty`) is the P3 addition that
  links the two loops: a long panic drains Stamina, so after the adrenaline fades the agent is too
  exhausted to see high-effort actions until it rests.

### Method spec — `updateDynamics(priorities []planner.DimensionPriority)` (phase 8)

Updates Adrenaline (surge/crash), Mood (pull toward baseline), the crash Stamina debt (P3), and the
post-surge Mood dip (P3), then delegates Resentment drift to `updateResentment` (owned by
`SPEC-coping.md` §B-drift — pointer only). Verbatim from `dynamics.go`:

```
// Compute current Urgency.
maxSalience := 0.0
for _, p := range priorities {
    if float64(p.Salience) > maxSalience {
        maxSalience = float64(p.Salience)
    }
}
urgency := clamp01(maxSalience * a.Cfg.UrgencyFromDeficit)

// Adrenaline surge / crash.
if urgency > a.Cfg.AdrTriggerUrgency {
    a.Adrenaline = clamp(a.Adrenaline + a.Cfg.AdrSurge, 0, a.Cfg.AdrMax)
} else {
    // Crash: drain toward 0 with stamina debt (P3).
    a.Adrenaline = clamp(a.Adrenaline - a.Cfg.AdrDecay, 0, a.Cfg.AdrMax)
    // Mood dip from adrenaline crash (λ × decay as penalty).
    a.Mood += a.Cfg.Lambda * (-a.Cfg.AdrDecay)
    // NEW P3: crash stamina debt — adrenaline crash drains Stamina.
    a.Stamina = clamp(a.Stamina - a.Cfg.AdrDecay*a.Cfg.CrashStaminaPenalty, 0, a.Cfg.StaminaMax)
}

// Mood: pull toward baseline.
a.Mood += a.Cfg.MoodDecay * (a.Cfg.MoodBaseline - a.Mood)

// Resentment drift from latent goals (owned by SPEC-coping §B — pointer only).
a.updateResentment()
```

- The `Urgency` computed here is the same scalar fed into the gate/planner snapshot in phase 5 (see
  §6); P5 additionally folds the max Other/Place/Collective-referent Priority into the snapshot
  `Urgency` (the referent-aware urgency proxy is owned by `SPEC-core.md`). This phase-8
  `updateDynamics` computes the Salience-driven Urgency for the **adrenaline** surge/crash decision.
- `updateResentment()` is called here every tick but is **owned by `SPEC-coping.md`** (the
  Aggression-Drift threshold check) — included in the call site only; do not duplicate its body here.

## 4. Mood

Mood is **computed here** (in `updateDynamics`); the **Apathy gate reads it** (the agent injects the
`Mood` scalar into the snapshot; the `apathy` gate predicate lives in `engine/gates` — pointer, §6).

Three Mood effects own a piece of this domain:

- **Baseline pull** (every tick, in `updateDynamics`):
  ```
  Mood += MoodDecay × (MoodBaseline − Mood)
  ```
- **Post-surge dip from the Adrenaline crash** (on a crash tick, in `updateDynamics`):
  ```
  Mood += Lambda × (−AdrDecay)
  ```
- **Bump on Apathy recovery** (in `ApplyOutcome`, on a single completed plan success while
  `Coping == Apathy`):
  ```
  Mood += ApathyRecoverMood
  ```
  > The Apathy-recovery **trigger** (single completed success → `Coping = Idle`, `FailStreak = 0`) is
  > owned by `SPEC-coping.md` §A. Only the `Mood += ApathyRecoverMood` term and the `ApathyRecoverMood`
  > Config field are referenced here; the recovery-state transition is NOT duplicated.

NOTE: the β / forward-sim `expected` Mood update on action completion (`Mood += λ·(actual−expected)`)
is unchanged from P1/P2 and lives with the outcome-fold path (`SPEC-core.md`); this domain owns only
the three terms above.

## 5. §A-budget — Apathy budget reduction (gap-closure fix, phase 5)

This is where the **budget computation lives conceptually** (it shapes the planner call). The reduced
Apathy budget reaches the planner through **one channel only**: the optional
`ApathyBudget *planner.Budget` field on `planner.AgentSnapshot`. When non-nil, the planner uses
`*ApathyBudget` for that `Plan` call instead of its construction-time `PlannerConfig.Budget`. When
nil, the planner uses its configured budget unchanged. The agent **never mutates the planner's
config**; it only sets the per-call pointer.

Per-agent base budget (computed every tick in `replan`, not only under Apathy):

```
effective = BudgetBase + int(perceivedIntelligence × BudgetPerIntelligence)
```

where `perceivedIntelligence = a.normalizedIntelligence(svc.Stats)` ∈ [0,1] (reads `ToM[self]`, D8).

Apathy reduction (only when `Coping == Apathy && ApathyBudgetPenalty > 0`):

```
factor = 1 − ApathyBudgetPenalty                       // e.g. 0.50 with penalty 0.50
reducedNodes   = max(1, int(effective                 × factor))
reducedActions = max(1, int(BudgetBase                × factor))
reducedDepth   = max(1, int((BudgetBase / 4)          × factor))
snapshot.ApathyBudget = &planner.Budget{
    MaxNodes:   reducedNodes,
    MaxActions: reducedActions,
    MaxDepth:   reducedDepth,
}
```

Otherwise:

```
snapshot.ApathyBudget = nil   // not apathetic → planner uses its configured budget unchanged
```

> The exact derivation of `reducedActions` / `reducedDepth` follows the current `replan` code:
> `reducedActions = max(int(float64(BudgetBase) * factor), 1)` and
> `reducedDepth = max(int(float64(BudgetBase/4) * factor), 1)`. `reducedNodes` reduces the
> Intelligence-scaled `effective` nodes by `factor`.

Determinism (D12): the budget is a pure function of (perceived Intelligence, Config, Coping state);
no rng draw. Reducing the budget changes only the planner's search caps, not its ordering, so a
reduced-budget plan is still deterministic.

**Cross-reference:** The budget is applied (the snapshot constructed) in `replan()` — see
`SPEC-core.md` §replan. The planner reads `agent.ApathyBudget` — see `engine/planner/SPEC.md`
§AgentSnapshot (the consuming side; the two SPECs are co-authored). This is the §A-budget from the
gap-closure SPEC update; it is the SOLE budget-override path (no per-call config mutation).

## 6. Snapshot body-scalar injection (gate coupling)

When building the planner `AgentSnapshot` (phase 5, in `replan`), the agent populates the body
scalars the P3 gates read — VERBATIM the four fields:

- `Stamina` — the live Stamina (the `stamina` gate hides `effort:high` below
  `gates.stamina_effort_high_threshold = 0.20`).
- `Mood` — the live Mood (the `apathy` gate narrows `abstraction:med`+ actions at
  `Mood ≤ gates.apathy_mood_threshold = -0.60`).
- `Adrenaline` — the live Adrenaline (the `adrenaline` gate applies `CostMultiplier = 0.50` to
  `effort:high`/`violent:*` at `Adrenaline ≥ 0.70`).
- `Urgency` — the referent-aware urgency proxy (normalized `combinedPriority`; the conscience gate
  reads it).

The planner forwards the body scalars into `gates.AgentSnapshot` when evaluating each candidate. The
agent computes these from its own Body/Coping; it **never reads them back from the gate registry**,
and the gate predicates themselves are out of scope (→ `engine/gates`).

**Cross-reference:** the snapshot construction (including the `Stamina`/`Mood`/`Adrenaline`/`Urgency`
assignment and the optional `ApathyBudget`) lives in `replan()` — see `SPEC-core.md` §replan. This
domain owns the *values* of those four scalars (the Stamina/Adrenaline/Mood loops above); core owns
the snapshot assembly.

## 7. Config fields (dynamics-relevant)

Verbatim from the agent `Config` struct (`agent.go`) — only the fields this domain uses. Every field
is injected from `content/balance.yaml` / `content/needs.yaml` (no hardcoded constant, D10):

```go
// mood
Lambda       float64 // balance.yaml mood.lambda
MoodDecay    float64 // balance.yaml mood.decay
MoodBaseline float64 // balance.yaml mood.baseline

// adrenaline
AdrTriggerUrgency float64 // balance.yaml adrenaline.trigger_urgency
AdrSurge          float64 // balance.yaml adrenaline.surge
AdrDecay          float64 // balance.yaml adrenaline.decay (the crash)
AdrMax            float64 // balance.yaml adrenaline.max

// stamina dynamics
StaminaMax     float64 // balance.yaml stamina.max
DrainPerEffort float64 // balance.yaml stamina.drain_per_effort (effort tag level)
RegenRest      float64 // balance.yaml stamina.regen_rest
RegenSleep     float64 // balance.yaml stamina.regen_sleep

// urgency mapping
UrgencyFromDeficit float64 // balance.yaml urgency.from_deficit

// P3: stamina effort-level resolution
EffortLevels map[core.Tag]float64 // balance.yaml tag_levels.effort

// adrenaline stamina coupling
CrashStaminaPenalty float64 // balance.yaml adrenaline.crash_stamina_penalty (Stamina -= AdrDecay × this on crash)

// coping cascade (budget — owned by dynamics)
BudgetBase            int     // base GOAP/HTN search nodes
BudgetPerIntelligence int     // + perceived Intelligence * this
ApathyBudgetPenalty   float64 // balance.yaml coping.apathy_budget_penalty (planner budget × (1−this) while Apathy)

// Apathy-recovery Mood bump (the recovery TRIGGER is owned by SPEC-coping §A).
ApathyRecoverMood float64 // balance.yaml coping.apathy_recover_mood (Mood += this on Apathy→Idle)

// stamina/rest dimension (data-driven, no literal D10)
RestDim core.Dimension // content/needs.yaml Rest dimension id, resolved by platform/config; replaces hardcoded "Rest" literal (D10)
```

> `BudgetBase` / `BudgetPerIntelligence` are the existing P1/P2 budget fields, consumed by `replan`
> to size the per-call `ApathyBudget`. `ApathyBudgetPenalty` is the gap-closure Apathy-budget field.
> `ApathyRecoverMood` is listed here because the **Mood bump** is a dynamics effect; the Apathy-
> recovery state transition that fires it is owned by `SPEC-coping.md` §A.

### DefaultConfig values (dynamics-relevant)

Verbatim from `DefaultConfig()` (`agent.go`):

```go
// mood
Lambda:       0.25,
MoodDecay:    0.02,
MoodBaseline: 0.0,

// adrenaline
AdrTriggerUrgency: 0.65,
AdrSurge:          0.6,
AdrDecay:          0.03,
AdrMax:            1.0,

// stamina
StaminaMax:     1.0,
DrainPerEffort: 0.015,
RegenRest:      0.010,
RegenSleep:     0.030,

// urgency
UrgencyFromDeficit: 1.4,

// budget
BudgetBase:            24,
BudgetPerIntelligence: 60,

// P3: effort levels from balance.yaml tag_levels.effort
EffortLevels: map[core.Tag]float64{
    "effort:none": 0.0,
    "effort:low":  0.20,
    "effort:med":  0.50,
    "effort:high": 0.90,
},

// P3: adrenaline-stamina coupling
CrashStaminaPenalty: 0.50,

// P3: coping cascade (dynamics-owned: budget penalty + recovery Mood bump)
ApathyRecoverMood:   0.15,
ApathyBudgetPenalty: 0.5,

// D10: Rest dimension id (default for tests; platform/config resolves from registry when content loaded)
RestDim: "Rest",
```

## 8. Dependencies (dynamics-relevant)

- `engine/actions` — `*Registry`: effort-tag resolution for Stamina drain (`resolveEffortLevel` reads
  `ActionDef.Tags` against `Config.EffortLevels`); Rest/Sleep detection (`hasRestEffectPerMinute` /
  `resolveRegenRate` read `ActionDef.EffectPerMinute[Config.RestDim]`). No action-id literal (D7).
- `engine/gates` — receives the `Stamina`/`Mood`/`Adrenaline` scalars (and `Urgency`) via the
  snapshot (pointer only); owns the `stamina` / `apathy` / `adrenaline` gate predicates. This domain
  writes the scalars; it never reads the registry or applies the predicates.
- `engine/planner` — `planner.Budget` / `AgentSnapshot.ApathyBudget` (the §A-budget per-call
  override), `planner.DimensionPriority` (the `priorities` arg to `updateDynamics`, for the max
  Salience → Urgency computation).
- `engine/needs` — `Def.UpdateConditionalNeeds` for the Safety-intensity drive (pointer to
  `SPEC-core.md` §F; the conditional-need arithmetic is the need layer's, not this domain's).
- `content/balance.yaml` — all dynamics blocks (`mood:`, `adrenaline:` incl.
  `crash_stamina_penalty`, `stamina:`, `urgency:`, `tag_levels.effort`,
  `coping.apathy_budget_penalty` / `coping.apathy_recover_mood`); `content/needs.yaml` for the
  `RestDim` id resolution (via `platform/config`).

## 9. Invariants (dynamics-relevant)

- **All rates injected (D10)**: no literal for effort levels, drain, regen, crash penalty, budget, or
  the Urgency mapping (`UrgencyFromDeficit`). The effort-level table, `CrashStaminaPenalty`,
  `RegenRest`/`RegenSleep`, `DrainPerEffort`, `BudgetBase`/`BudgetPerIntelligence`,
  `ApathyBudgetPenalty`, and `ApathyRecoverMood` all come from `Config` (read from balance.yaml).
- **No action-id literal (D7)**: Rest/Sleep are detected by `effect_per_minute` magnitude on the
  `Config.RestDim` dimension, never by a `"Rest"`/`"Sleep"` action-id literal in logic.
- **Bounds**: `Stamina ∈ [0, StaminaMax]` (every `applyStaminaDelta` and the crash debt clamp);
  `Adrenaline ∈ [0, AdrMax]` (the surge/crash clamps). Each `ApathyBudget` cap ≥ 1
  (`max(1, …)`).
- **Determinism (D12)**: `updateDynamics`, `applyStaminaDelta`, and the §A-budget sizing are pure
  functions of (Body state, `priorities`, Config, action def) — no rng draw, no `time.Now()`, no map
  iteration for logic. The max-Salience scan iterates the `priorities` slice (ordered), and effort
  resolution iterates the action's `Tags` slice (authored order).

## 10. Acceptance Criteria (dynamics-relevant)

Copied verbatim from the monolith (each maps to a unit / golden / scenario):

- [ ] **Stamina drain is data-driven (no literal)**: executing an `effort:high` action drains
  `DrainPerEffort × tag_levels.effort[effort:high]` per tick; `effort:none` drains 0.
- [ ] **Rest/Sleep regen (data-driven)**: executing `Rest` adds `RegenRest × tickMinutes`;
  executing `Sleep` adds `RegenSleep × tickMinutes`; Stamina clamps at `StaminaMax`.
- [ ] **Stamina gate hides effort:high (scenario, golden)**: an agent with `Stamina = 0.10` builds
  a snapshot with `Stamina = 0.10`; the planner's `PlanBuilt` contains **no** `effort:high` action.
  At `Stamina = 0.30` the high-effort action can appear.
- [ ] **Adrenaline surge then crash with stamina debt**: `Urgency > AdrTriggerUrgency (0.65)` raises
  `Adrenaline` by `AdrSurge` (clamped `AdrMax`); once urgency falls, `Adrenaline` drains by `AdrDecay`
  per tick AND `Stamina` drops by `AdrDecay × CrashStaminaPenalty`.
- [ ] **Adrenaline cost discount reaches the planner (scenario)**: with `Adrenaline = 0.75` injected,
  the `adrenaline` gate returns `CostMultiplier = 0.50` for an `effort:high`/`violent:*` candidate.
- [ ] **Apathy narrows cognition via the gate**: an `Apathy` agent whose `Mood ≤ -0.60` builds a
  snapshot with `Mood ≤ -0.60`; the planner does not select an `abstraction:med`+ action. Once Mood
  recovers above `-0.60`, the abstract action is visible again.
- [ ] **Apathy reduces the planner budget via ApathyBudget (gap-closure)**: while `Coping == Apathy`,
  the `AgentSnapshot` built by `replan` has a non-nil `ApathyBudget` whose `MaxNodes` equals
  `max(1, int(effectiveNodes × (1 − ApathyBudgetPenalty)))` (and likewise for depth/actions); while
  `Coping != Apathy`, `ApathyBudget == nil`. A planner-level companion AC (in `engine/planner`)
  asserts the override is honored for that call only. Table-driven over {Apathy, non-Apathy}.

> The body-scalar injection ACs above (Stamina/Adrenaline/Mood gate coupling) assert the agent writes
> the live scalar; the gate-predicate side (the actual visibility / cost-multiplier decision) is an
> `engine/gates` AC.

## 11. Out of Scope (dynamics)

- **Resentment accrual and the Aggression-Drift threshold check** → `SPEC-coping.md` §B / §B-drift.
  Only the Adrenaline-crash **Mood dip** is computed here.
- **Coping cascade transitions** (Rebinding → Longing → Latent → Apathy) → `SPEC-coping.md` §A.
- **Apathy recovery on a single plan success** (the state transition that fires the Mood bump) →
  `SPEC-coping.md` §A. Only the `Mood += ApathyRecoverMood` term is owned here.
- **The `stamina` / `apathy` / `adrenaline` gate predicates themselves** → `engine/gates` (the agent
  only injects the Body scalars into the snapshot).
- **Applying the `CostMultiplier` to the tag-cost sum** → `engine/planner`.
- **Consuming the `ApathyBudget` override in the search** → `engine/planner` (this domain only sizes
  and sets the optional `AgentSnapshot.ApathyBudget` pointer).
- **The referent-aware Urgency proxy assembly + the snapshot construction** → `SPEC-core.md` §replan
  (this domain computes the Salience-driven Urgency for the adrenaline decision and owns the four
  Body-scalar values; core assembles the snapshot).
- **The conditional Safety-intensity arithmetic** (`Def.UpdateConditionalNeeds`) → `engine/needs`;
  the §F drive that calls it → `SPEC-core.md` §F.

## 12. Open Questions (dynamics-relevant)

- **None blocking.** The Sleep-vs-Rest discriminator (`restRate >= 0.0030`) reads the content-authored
  `effect_per_minute` magnitude off the action def. If future content adds a third recovery action
  whose `Rest` effect lands between Rest and Sleep, replace the magnitude test with an injected
  Rest/Sleep mapping (the monolith already names this alternative — "or by an injected mapping").
  Non-blocking; the two regen *rates* are already injected (D10), so a retune is a content edit.
