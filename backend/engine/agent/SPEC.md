# SPEC — `engine/agent`

> Status: `P5`
> Leaf level: `L5`  ·  Owner agent: `implementer`
>
> (P1/P2 loop + P3 coping cascade, Stamina/Adrenaline, and Resentment are IMPLEMENTED & tested.
> The world FEEDS conflict-loss resentment triggers (`world.conflictResentmentTriggers` →
> `WorldView.ResentmentTriggers`); end-to-end coverage in `world/scenario_resentment_test.go`.
> P5 adds referent-aware urgency wiring, the Intelligence-block key rename, and hostile/encroach
> perception → defensive Safety goal insertion.
> **P6 Blocker Group A — §F is now ACTIVATED (was deferred): the phase-1 threat scan must also
> RAISE the conditional Safety need intensity via `needs.UpdateConditionalNeeds`, and main.go must
> populate `Config.ThreatTags` / `Config.SafetyDim`. Without the intensity driver the forced-goal
> reflex still fires, but the *appraisal chain* never sees Safety pressure, so no Safety goal /
> reliance emerges on its own — see "Implement (P5) §F".**)
> **P6 gap-closure batch — clarifies six previously-ambiguous mechanisms so the implementer can
> write them with no guesswork: (1) how the per-agent Apathy-reduced planner budget reaches the
> planner [`AgentSnapshot.ApathyBudget`], (2) which function owns the per-tick Aggression-Drift
> threshold check [`updateResentment`, NOT `accrueResentment`], (3) the `FunctionSpec` type +
> `Config.Functions` table that replaces the hardcoded `goalToFunction` mapping, (4) the
> `emit core.EventEmitter` parameter on `handleRelianceTrigger`, (5) `emitVoteIfEligible` taking the
> already-computed `combinedPriority` instead of recomputing it [`distributedUrgency` removed], and
> (6) the influence-weighted gossip fold in `processIncomingSignals`. See §B/§G/§H/§I and the
> "Decision-loop model" updates.**

## Purpose

Drives one agent through one tick of the decision loop (design §1.1):
`perception → value appraisal → goal mediation → planning (HTN+GOAP) → durative execution →
signal → belief/reputation/reliance update`. It is the **orchestrator** (D5): it owns the
agent's dynamic `Body` state and `ToM`, sequences the upstream pure modules, and emits **intents**
— it never mutates the world directly and never decides *what is wanted* (`values`/`needs`) or
*how to get it* (`planner`). It additionally owns the per-agent dynamics that span the loop: the
**coping cascade** (rebind → longing → latent → apathy), per-stat **self-calibration** (β, D8),
the **Stamina** drain/regen loop, the **Adrenaline** surge/crash, the **Mood** update, and
**Resentment** drift. It feeds the live Body scalars (Stamina, Mood, Adrenaline, Urgency) into the
gate/planner snapshot each tick so the P3 gates (`stamina`, `apathy`, `conscience`-relief,
`adrenaline`-cost) can read them (`engine/gates/SPEC.md`). All rate constants are injected from
`content/balance.yaml` (D10); the package hardcodes none.

## Current state (P1/P2 — IMPLEMENTED, DO NOT regress)

Shipped and tested across `agent.go`, `types.go`, `tick.go`, `execution.go`, `outcome.go`,
`coping.go` (+ `agent_test.go`, `golden_test.go`, `scenario_c_test.go`):

- The 8-phase `Tick` loop (perceive → decay needs → appraise → mediate goal → plan → execute →
  signal → dynamics) and `ApplyOutcome` (durative completion/interruption, β fold, Mood update,
  Effect application, completed-goal reset to Idle).
- The `Agent`/`Intent`/`Signal`/`ActionOutcome`/`Config`/`Services`/`WorldView`/`KnownObject`/
  `LatentGoal` types; `CopingState{Idle,Rebinding,Longing,Latent,Apathy}`.
- **Coping skeleton** (`coping.go`): `enterCopingCascade` advances one step per failed re-plan;
  `canRebind` (budget-derived Intelligence threshold), `rebindSubstitute`, `enterLonging`,
  `enterApathy`. Emits `CopingEntered`.
- **Stamina/Adrenaline skeleton**: `drainStamina` (per-effort drain in `execute`), `updateDynamics`
  (Adrenaline surge/crash + Mood-baseline pull + `updateResentment`).
- β self-calibration via `foldEvidence` (Invisible/Interrupted → no fold, self-sealing D8).
- `mediateGoal` (Stickiness/goal_deadband anti-thrash, owned here per planner OQ).

P3 **completes** the gaps in this skeleton. Below, "Current" marks what exists; "Implement (P3)"
marks the new work. The Public Interface adds four `Agent` fields and threads body scalars into the
snapshot.

## Implement (P3 — what this batch completes)

### A. Coping cascade — full transition logic

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

- **`FailStreak`** (new field): incremented each consecutive failed re-plan; reset to 0 on any
  plan success or a completed action. The cascade step is a function of `(failure cause,
  perceived Intelligence, FailStreak, prior CopingState)` — **rng-free, deterministic** (D12).
- **Low-Intelligence shortcut (design §1.4 object-fixation)**: if perceived `Intelligence < 0.4`
  (`intelligence.rebind_threshold`, see Data Contract) the agent **cannot enter `Rebinding`** and
  on the first plan fail goes **directly to `Latent`** (skipping `Rebinding` AND `Longing`). High-
  Intelligence agents traverse `Rebinding → Longing → Latent`.
  > Current `canRebind` derives the threshold from the budget curve; P3 replaces it with the
  > explicit `intelligence.rebind_threshold` constant (cleaner, resolves the existing Open
  > Question) and adds the "skip to Latent" shortcut for the low branch.
- **Apathy entry**: when `Coping == Latent` and `FailStreak ≥ coping.apathy_fail_streak`, enter
  `Apathy`. Apathy lowers planning budget (use a reduced `Budget` next tick — see §A-budget below
  and the "Coping cascade" section for the exact `ApathyBudget` mechanism) and pulls Mood down
  (the Mood-decay pull plus the adrenaline crash already do this; Apathy additionally suppresses
  the active goal so no positive progress term fires).
- **Apathy recovery (single success)**: in `ApplyOutcome`, a single completed plan success
  (`Status == Succeeded && Completed == true`) while `Coping == Apathy` resets `Coping = Idle`,
  `FailStreak = 0`, and bumps `Mood += coping.apathy_recover_mood` (`+0.15`). A *different* goal
  becoming plannable also exits Apathy via the normal phase-5 success path.

#### A-budget. Apathy-reduced planner budget — exact mechanism (gap-closure)

The reduced Apathy budget reaches the planner through **one channel only**: the optional
`ApathyBudget *planner.Budget` field on `planner.AgentSnapshot` (added in `engine/planner/SPEC.md`
this batch). When non-nil, the planner uses `*ApathyBudget` for that `Plan` call instead of its
construction-time `PlannerConfig.Budget`. The agent computes it in `replan` **every tick** (not only
under Apathy) as follows:

```
// 1. per-agent base budget (from Config; reads perceived Intelligence, ToM[self] — D8)
perceivedIntel := a.normalizedIntelligence(svc.Stats)        // [0,1]
effectiveNodes := a.Cfg.BudgetBase + int(perceivedIntel * float64(a.Cfg.BudgetPerIntelligence))
// derive depth/actions from the same scale; cap nodes to the planner's configured MaxNodes so an
// override never EXCEEDS the configured ceiling (the planner applies whatever caps it is handed).

// 2. Apathy reduction (gap-closure): only when the agent is currently apathetic.
if a.Coping == Apathy {
    factor := 1.0 - a.Cfg.ApathyBudgetPenalty                 // e.g. 0.40 with penalty 0.60
    reducedNodes   = max(1, int(float64(effectiveNodes)   * factor))
    reducedDepth   = max(1, int(float64(effectiveDepth)   * factor))
    reducedActions = max(1, int(float64(effectiveActions) * factor))
    snapshot.ApathyBudget = &planner.Budget{
        MaxDepth:   reducedDepth,
        MaxActions: reducedActions,
        MaxNodes:   reducedNodes,
    }
} else {
    snapshot.ApathyBudget = nil   // not apathetic → planner uses its configured budget unchanged
}
```

- The agent **never mutates the planner's config**; it only sets the per-call `ApathyBudget`
  pointer on the snapshot it passes to `Plan`. When `a.Coping != Apathy`, `ApathyBudget` is `nil`
  and the planner falls back to its configured `PlannerConfig.Budget` (so non-apathetic deliberation
  is unaffected). This is the ONLY budget-override path; there is no per-call config mutation.
- `BudgetBase` / `BudgetPerIntelligence` are the existing `Config` fields; `ApathyBudgetPenalty` is
  the existing `Config` field (`balance.yaml coping.apathy_budget_penalty`). All injected (D10).
- Determinism (D12): the budget is a pure function of (perceived Intelligence, Config, Coping
  state); no rng draw. Reducing the budget changes only the planner's search caps, not its ordering,
  so a reduced-budget plan is still deterministic.
- See `engine/planner/SPEC.md` `AgentSnapshot.ApathyBudget` + "Per-call budget override" for the
  consuming side. The two SPECs are co-authored this batch.

### B. Resentment accrual (Latent residence) + Aggression-Drift threshold (gap-closure)

While `Coping == Latent`, each tick a **trigger event** may arrive (a rejection without an Offer in
return, or losing a resource conflict — both reported by the world via `ApplyOutcome` /
`WorldView`):

```
Resentment += resentment.per_trigger × Vindictiveness               (resentment.per_trigger = 0.05)
Affinity[toward trigger agent] -= AffinityDrop × Vindictiveness     (balance.yaml resentment.affinity_drop)
```

- `Vindictiveness` is `ToM[self].EstStats[Vindictiveness].Mean` (D8 — read self-belief; the stat id
  resolved from `Config.VindictivenessStatID`, never a `"Vindictiveness"` literal — D7).
- The per-trigger Affinity drop **persists** on the actual `Belief` toward the trigger agent via
  `a.ToM.AdjustAffinity(triggerID, −AffinityDrop × Vindictiveness)` (the tom storage primitive). A
  `BeliefUpdated` event records the Affinity drop (data-contracts §4).
- Resentment is **emergent, not a hardcoded grudge type** (D2): it is a scalar drift that feeds
  the normal value gradient (avoidance/aggression), not an "enemy" object.

#### B-drift. Where the Aggression-Drift threshold check lives (gap-closure)

The Resentment-threshold → Aggression-Drift fold runs **every tick**, regardless of whether new
triggers arrived — so it MUST live in `updateResentment()` (called every tick from `updateDynamics`,
phase 8), NOT in `accrueResentment()`. The previous placement (inside `accrueResentment`, after its
`if len(triggers) == 0 { return }` early-return) meant the drift never applied on threshold-exceeding
ticks with no fresh trigger. The corrected split is:

- **`updateResentment()`** (every tick, in `updateDynamics`): owns the per-tick **Aggression-Drift
  threshold check**. When `a.Resentment > a.Cfg.ResentmentThreshold`, fold the drift into
  `ToM[self].Aggression`:

  ```
  latentFactor := clamp01(Σ over a.Latent of lg.Intensity)   // 0 if no latent goals → no drift
  if a.Resentment > a.Cfg.ResentmentThreshold {
      aggStatID := a.Cfg.AggressionStatID                     // D7-resolved, never a literal
      a.ToM.Observe(a.ToM.SelfID(), tom.StatEvidence{
          Stat:     aggStatID,
          Observed: a.perceivedStat(aggStatID) + a.Cfg.AggressionDrift * latentFactor,
          Weight:   <the existing self-evidence weight>,    // unchanged from the prior placement
          Tick:     0,                                        // filled by caller
      })
  }
  ```

  The drift magnitude is `AggressionDrift × latentFactor` (the residual latent intensity scales it;
  with no latent goals `latentFactor == 0` so no drift — preserving the old "latent residence"
  intent). This runs **regardless of triggers** so a steeped grudge keeps hardening Aggression even
  on quiet ticks.
- **`accrueResentment()`** (only when `Coping == Latent` AND `len(triggers) > 0`): owns ONLY the
  trigger-based Affinity drop + Resentment accumulation (the two lines at the top of §B). The
  Aggression-Drift threshold block is **REMOVED from `accrueResentment`** — keeping it in both
  places would double-apply the drift on a trigger tick that also exceeds the threshold.

> Net effect: the Affinity drop is trigger-gated (it needs a specific trigger agent to drop toward);
> the Aggression drift is threshold-gated and time-continuous (it depends only on accumulated
> Resentment, not on this tick's triggers).

### C. Apathy ↔ gates coupling

- Each tick the agent injects its **live Mood** into the planner/gate `AgentSnapshot` (new
  `Mood`/`Stamina`/`Adrenaline`/`Urgency` fields on `gates.AgentSnapshot`, `engine/gates/SPEC.md`).
  When `Mood ≤ gates.apathy_mood_threshold` (`-0.60`), the `apathy` gate makes `abstraction:med`+
  actions invisible — so a deeply apathetic agent's planner sees a narrowed action set for free
  (no special-case in the agent; the gate does it from the injected Mood).
- A single plan success folds back through `ApplyOutcome` (B above) and bumps Mood, which — once
  Mood climbs above `-0.60` — re-widens the visible action set on the next tick.

### D. Stamina loop (drain / regen / gate)

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

- Current `drainStamina` hardcodes a `0.5` default effort and a literal effort-level table
  (`parseEffortLevel`). **P3 must resolve effort level from the injected `tag_levels.effort`
  config** (no literals — D10) and default `effort:none` to `0`, not `0.5`.
- Rest/Sleep detection must be **data-driven** (the action carries `effort:none` and an
  `effect_per_minute` on the `Rest` dimension) — resolve via the actions registry, no `"Rest"`/
  `"Sleep"` action-id literal in logic (D7/D10). The two regen rates distinguish Rest vs Sleep by
  the magnitude of their `Rest` `effect_per_minute` (Sleep > Rest), or by an injected mapping.

### E. Adrenaline loop (surge / crash / stamina debt / cost)

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

## Implement (P5 — what this batch adds to the agent loop)

### F. Hostile/encroach perception → Safety-intensity rise + defensive Safety goal  `ACTIVATED (P6 Blocker Group A)`

> **Status: ACTIVATED.** This phase was previously authored but only the forced-goal reflex was
> wired; the **Safety-intensity driver was missing**, so the conditional `Safety` need stayed at 0
> and the *appraisal* chain (phase 3) never raised a Safety Priority on its own. P6 Blocker Group A
> completes it: the phase-1 threat scan now ALSO drives `NeedIntensities[cfg.SafetyDim]` via
> `needs.UpdateConditionalNeeds`, so collective Safety satisfaction can actually drop and the
> downstream Safety goal / reliance machinery (Scenario G) can fire.

In tick **phase 1 (perceive)**, after collecting perceived entities (from `svc.Sensor.Sight`) and
sound events (from `svc.Sensor.Hearing`), the agent scans them for a **threat tag**. The set of
threat tags is **data-driven** and injected — `Config.ThreatTags []core.Tag`, populated from
`balance.yaml threats.hostile_tags`. The agent does **not** branch on specific tag names in logic
(D7/D10); it tests membership against the injected set.

**Threat detection (the producer of the threat list):**

```
phase-1 (perceive):
  seen     = svc.Sensor.Sight(a.Pos, world)         -- []perception.PerceivedEntity (Tags copied)
  threats  = []core.AgentID{}
  for each e in seen (already Distance-then-ObjectID sorted, D12):
      if e.Tags ∩ Config.ThreatTags ≠ ∅:            -- any tag of e is in the injected threat set
          if e.ID names an agent:                   -- only agents are threats (entity tagged "agent",
                                                     --   matching the near_other test in execution.go)
              threats = append(threats, core.AgentID(e.ID))
  dedupe + keep threats sorted by AgentID (D12 count-stability for UpdateConditionalNeeds)
  threatPerceived = len(threats) > 0
```

**(1) Safety-intensity drive (the NEW BLOCKER-1 wiring — runs EVERY tick, threat or not):**

```
def := svc.Needs.Def(cfg.SafetyDim)               -- the conditional Safety dimension (Rate==0)
cur := a.NeedIntensities[cfg.SafetyDim]
a.NeedIntensities[cfg.SafetyDim] =
    def.UpdateConditionalNeeds(cur, threats, cfg.ThreatPerThreatGain, cfg.ThreatSafetyDecay)
```

- This MUST run BEFORE phase-3 appraise so the raised Safety intensity feeds the ordinary appraisal
  chain (`appraise` already iterates `needsReg.IDs()`, which includes the conditional `Safety`
  dimension, and reads `a.NeedIntensities[dim]`). When `threats` is empty the intensity DECAYS
  toward 0 over `⌈cur/ThreatSafetyDecay⌉` ticks; when `len(threats) ≥ 1` it RISES by
  `ThreatPerThreatGain × len(threats)`, clamped to `[0,1]`. The arithmetic lives in
  `engine/needs.Def.UpdateConditionalNeeds` (pure); the agent only supplies the threat list + the
  two injected constants and stores the result (D5: the need-pressure math is the need layer's; the
  perception is the agent's).
- The two constants `ThreatPerThreatGain` / `ThreatSafetyDecay` are injected from
  `balance.yaml threats.per_threat_intensity` / `threats.safety_decay` (no literal in agent logic —
  D10).

**(2) Defensive-goal reflex (unchanged from the prior authoring — pre-empts phase-4):**

```
if threatPerceived:
    a.Goal = cfg.SafetyDim                     -- forcibly select the Safety Dimension (injected id,
                                               --   never a "Safety" literal — D7/D10)
    a.Plan = planner.Plan{}                    -- clear the current plan so the planner RE-PLANS for
                                               --   Safety on the next planning phase
    emit GoalSelected{ dimension: cfg.SafetyDim, priority: 1.0, eff_value: 1.0 }
                                               -- emitted immediately (phase 1), ahead of the normal
                                               --   phase-4 goal mediation

if NOT threatPerceived:
    -- normal goal mediation at phase 4 proceeds uninterrupted (no override);
    -- the decayed (possibly residual) Safety intensity still feeds phase-3 appraise.
```

- The defensive override **pre-empts** the normal phase-4 goal mediation: when a threat is
  perceived, Safety is forced for this tick regardless of the Priority arbitration. This is the
  only place a goal is forced outside the appraisal chain, and it is justified as a reflex (a
  perceived hostile/encroach event is, by content authoring, a Safety threat — not a hardcoded
  meta-system, D2: the *reaction* is the ordinary Safety-need pursuit, only the trigger is the
  perceived threat tag).
- `cfg.SafetyDim` and the threat-tag set are both injected (no `"Safety"`/`"hostile"` literal in
  logic, D7/D10). The agent never decides *what Safety means* — it forces the Safety **Dimension**,
  drives the Safety **intensity**, and the planner/values resolve how to satisfy it as usual (D5).
- Determinism: the threat scan iterates the perceived entities in a stable order (sorted by
  ObjectID, D12); the threat list is deduped + sorted so `UpdateConditionalNeeds`'s count is
  count-stable; the override is rng-free.

### Referent-aware Urgency wiring (P5 — feeds Scenarios D and E)

The agent aggregates the **maximum Priority across ANY referent** (Self, Other, and Place) as the effective
`Urgency` it injects into the planner/gate snapshot:

```
UrgencyProxy = max( Self-Priority(over Dimensions), Other-Priority(over cared-for Others), Place-Priority(over Place values) ) / max_priority
```

- The Self-Priority terms come from the existing appraisal chain (`values.ComputePriority` over the
  agent's own need Dimensions) — and now, via §F (1), the Self-Priority for `Safety` reflects the
  driven Safety intensity (a perceived threat raises Self.Safety Priority through the ordinary chain).
- The Other-Priority terms come from `values.DeriveReferentInput(Other, …)` →
  `values.ComputeStanding/Salience` → `values.ComputePriority` for each cared-for Other, with the
  per-agent **bond multiplier** applied to the base weight by the agent loop (the multiplication is
  the agent's job, not values' — `engine/values/SPEC.md` Out of Scope "Per-agent weight
  perturbation by Affinity/bond").
- The Place-Priority terms (Scenario E) come from the new `appraisePlace` method, which iterates
  over the agent's `Values` field (those with `Ref.Kind == core.Place`), calls
  `world.PlaceQuality(ref.ID)` to get the current quality, applies the posture mapping
  (Maximize → quality as-is; MaintainAbove/PreventBelow → deficit below setpoint), then
  `values.DeriveReferentInput(Place, …)` → `ComputeStanding/Salience` → `ComputePriority`.
- When the highest Priority across ANY referent exceeds `conscience_urgency_threshold`, the
  planner's conscience-loosening fires (the planner reads `agent.Urgency`), so caring for an Other
  in distress or a Place in degradation can lower the effective conscience threshold.

**Add to `Config`** (the P5 threat fields + the NEW BLOCKER-1 conditional-driver scalars):

```go
// P5 additions
ThreatTags []core.Tag      // balance.yaml threats.hostile_tags — tags that trigger defensive Safety goal insertion
SafetyDim  core.Dimension  // content/needs.yaml Safety dimension id, resolved by platform/config

// P6 Blocker Group A — conditional Safety-intensity driver (BLOCKER-1). Injected, no literal (D10).
ThreatPerThreatGain float64 // balance.yaml threats.per_threat_intensity — Safety intensity added per perceived threat
ThreatSafetyDecay   float64 // balance.yaml threats.safety_decay — Safety intensity removed per tick with no threat
```

> **`Config.ThreatTags` / `Config.SafetyDim` MUST be populated by `main.go` (BLOCKER-2).**
> `agentConfigFromBalance` in `backend/main.go` currently does NOT set `ThreatTags`, `SafetyDim`,
> `ThreatPerThreatGain`, or `ThreatSafetyDecay`, so they default to nil/"" /0 and the entire §F
> phase is inert (no threat ever matches, Safety intensity never rises). The fix is **main.go
> wiring only** (no engine code): parse `balance.yaml threats.hostile_tags`, `per_threat_intensity`,
> and `safety_decay` into the `balanceDoc.Threats` struct, and resolve the canonical **Safety**
> dimension id from the loaded `needs.Registry` (the single `Kinds(needs.Conditional)` entry whose
> posture is `PreventBelow`, or by the glossary id resolved from `needs.yaml`), then set all four
> fields in `agentConfigFromBalance`. See the Data Contract below and the BLOCKER-2 ACs.

**Add to `Agent`:**

```go
// Values holds the agent's value directions beyond Self-needs.
// Each entry names a Dimension + Referent + Posture + Setpoint.
// Populated at construction; read by appraisePlace (Place referent).
Values []core.Value
```

## Implement (P6 — emergent institutions & politics)

P6 makes the agent the **policy** layer over `engine/tom`'s reliance/Influence primitives: it
decides *when* to rely on another, casts delegation **Vote** signals, and weights incoming social
signals by the source's **Influence**. The world then detects the resulting `RelyOn` cluster as a
`RoleEmerged` statistic (no role type anywhere — D2). All thresholds/ratios are injected (D10).

### G. Reliance trigger — when a Function is self-unsolvable

A **Function** (glossary Reliance edge: `safety`, `judgment`, `knowledge`, …) maps to the goal
Dimension(s) and the capability stat-set it draws on. The mapping is **data-driven** (D7 — no
literal in logic): the agent reads the injected `Config.Functions []FunctionSpec` table to resolve
a goal `Dimension` to its `Function` and the capability `Stats` that Function draws on. During
phase 5 (plan), when the agent pursues a goal serving Function `f` and **cannot self-solve it**, it
relies on another:

- **Function resolution (gap-closure — replaces the hardcoded `goalToFunction`).** The agent resolves
  `(Function, Stats)` for the current goal Dimension by scanning `Config.Functions` for the entry
  whose `Dim == a.Goal` (iterate the slice in its authored order — it is a small fixed table, D12),
  returning that entry's `ID` (the Function) and `Stats` (the capability set for
  `BestProviderFor`). If no entry matches the goal Dimension, **no Function is resolved and no
  reliance forms** (return early — the hardcoded "Safety → FuncSafety, else FuncKnowledge" fallback
  is REMOVED). The `Stats` come from the matched `FunctionSpec`, NOT from the agent's own
  `EstStats` keys.
- **Self-unsolvable** = the planner returns `ErrUnreachable`/`ErrBudgetExceeded` for `f`'s goal
  (gate-blocked: no visible producer), **OR** the cheapest reachable plan's cost exceeds
  `Config.RelyCostThreshold` (`balance.yaml politics.rely_cost_threshold`). Both are observed at the
  planner boundary — the agent already has the trace, so no gate/cost logic is duplicated (D4/D5).
- **Whom**: among perceived others, pick `ToM.BestProviderFor(f, fnStats, candidates)` (trusted AND
  competent, D6/D8 — reads ToM beliefs, never Real Stats), where `fnStats` is the matched
  `FunctionSpec.Stats`. If none qualifies, no reliance forms.
- **Apply**: `ToM.AdjustRelyOn(provider, f, δ)` with `δ = Config.RelyOnDelta`
  (`balance.yaml politics.relyon_delta`). Reliance thus **accretes** over repeated self-failure —
  the substrate that, once a plurality points at one provider, the world reads as a role.
- Emit a `BeliefUpdated{about: provider, field: "RelyOn["+f+"]", cause: "reliance"}` for the trace.

#### G-emit. `handleRelianceTrigger` takes an `emit core.EventEmitter` (gap-closure)

`handleRelianceTrigger` must accept an `emit core.EventEmitter` parameter so it can emit the
`BeliefUpdated{cause:"reliance"}` event on a successful `AdjustRelyOn`. Updated signature:

```go
func (a *Agent) handleRelianceTrigger(world WorldView, planCost float64, planErr error, emit core.EventEmitter)
```

- Both `tick.go` call sites (the plan-failure path and the high-plan-cost path) pass the loop's
  `emit`: `a.handleRelianceTrigger(world, 0, err, emit)` and
  `a.handleRelianceTrigger(world, trace.TotalCost, nil, emit)`.
- After a successful `a.ToM.AdjustRelyOn(target, f, a.Cfg.RelyOnDelta)`, emit:

  ```go
  emit.Emit(core.Event{
      SchemaVersion: 1,
      Tick:          0,                       // filled by the platform layer
      AgentID:       a.ID,
      Type:          "BeliefUpdated",
      Payload: map[string]any{
          "about": string(target),
          "field": "RelyOn[" + string(f) + "]",
          "cause": "reliance",
          "delta": a.Cfg.RelyOnDelta,
      },
  })
  ```

  The emit is guarded by `emit != nil`. No event is emitted on the no-trigger / no-provider early
  returns (only on an actual reliance shift).

This is **emergent** (D2): the agent never labels anyone a chief; it just keeps relying on whoever
best covers a need it cannot meet alone.

### H. VoteAction — broadcasting a delegation

`Vote` is an atomic **social signal** (content/actions.yaml; tags `[social, effort:low,
abstraction:med]`, requires `near_other`) that publicly delegates Function `f` to a target — it
lets reliance **converge faster than private accretion alone**. In phase 7 (signal), when the agent
holds a strong private reliance (`ToM[target].RelyOn[f] ≥ Config.VoteRelyThreshold`) **and** its
distributed urgency is high (the combined urgency proxy already computed in phase 3 exceeds
`Config.UrgencyThreshold`), it emits:

```go
Signal{Kind: SignalVote, Toward: target, Function: f, Intensity: relianceStrength}
```

#### H-urgency. `emitVoteIfEligible` takes the already-computed `combinedPriority` (gap-closure)

`emitVoteIfEligible` MUST accept the `combinedPriority float64` already computed in phase 3 of
`Tick` (the `max(selfMax, maxOther, maxPlace, maxCollective)` urgency proxy, which already includes
`maxCollective`), instead of recomputing a separate distributed urgency. Updated signature and call
site:

```go
func (a *Agent) emitVoteIfEligible(now core.Tick, world WorldView, combinedPriority float64) Intent
// tick.go phase 7:
voteIntent := a.emitVoteIfEligible(now, world, combinedPriority)
```

- The eligibility test becomes:
  1. `relyTarget, relyStrength := a.bestRelyOnFor(f)`; require `relyTarget != "" && relyStrength >
     a.Cfg.VoteRelyThreshold`.
  2. `combinedPriority > a.Cfg.UrgencyThreshold` (the same combined proxy from phase 3). Note: this
     uses `Config.UrgencyThreshold`, the canonical distributed-urgency threshold named in §H — if
     the current code reads `Config.VoteUrgencyThreshold`, reconcile to a single field
     (`UrgencyThreshold`) and document it in the Data Contract; do not keep two near-duplicate
     thresholds.
  3. If both hold, return the `IntentSignal` with the `SignalVote` payload (`Function: f`,
     `Target: relyTarget`, `Intensity: relyStrength`); otherwise `IntentNone`.
- The `distributedUrgency(world, safetyDim)` helper is **DELETED** — its recomputed mean Safety
  intensity is exactly the `maxCollective` term already folded into `combinedPriority` in phase 3, so
  recomputing it (and re-hardcoding the Safety dimension parameter) is redundant. The Function `f` is
  resolved via the `Config.Functions` table (§G-emit / gap-closure), not the removed
  `goalToFunction`.

A receiver folds a heard `Vote` as evidence to **also** `AdjustRelyOn(target, f, δ_vote)` — so under
shared distress (everyone's Safety dropping at once) votes pile onto the same capable holder and the
`RelyOn` share crosses `role_convergence_threshold` in far fewer ticks than independent accretion.
Low distributed urgency ⇒ no votes ⇒ slow/no convergence (the AC contrast). `Vote` produces no need
Effect; it never competes with survival goals (the planner won't pick it for a need goal).

### I. Influence — weighting incoming signals

At the signal-fold site, the agent scales the source's credibility by the source's **Influence**
before calling `tom.GossipUpdate`. This is owned by `processIncomingSignals`, which today only
handles `SignalVote` and must now ALSO handle gossip/hearsay signals:

    signalWeight = clamp01( trust · (1 + Config.InfluenceWeight · ToM.Influence(source, f, observers)) )

`Config.InfluenceWeight` = `balance.yaml politics.influence_weight`. A heavily-relied-upon
(high-Influence) source therefore shifts the agent's beliefs **more** for the same claim — the
table AC. `GossipUpdate`'s signature is unchanged (Influence is folded into the weight the agent
passes, per `engine/tom/SPEC.md` §P6).

#### I-fold. Gossip handling in `processIncomingSignals` (gap-closure)

`processIncomingSignals` iterates `world.IncomingSignals(a.ID)`. For each signal:

- If it is a `SignalVote` → `processVoteSignal` (unchanged).
- Otherwise (a **gossip / hearsay** signal from a known agent — i.e. a non-Vote signal that carries
  a belief claim about some `subject`): fold it through `GossipUpdate` with the Influence-weighted
  trust:

  ```
  source := sig.Source                                   -- the telling agent
  subject := <the agent the claim is about>              -- from the signal payload (the gossiped subject)
  srcBelief, ok := a.ToM.Self(source)                    -- the receiver's belief ABOUT the source
  if !ok || srcBelief.Trust <= 0 { continue }            -- ignore untrusted / unknown sources

  trust := srcBelief.Trust                               -- the receiver's Trust in the source
  observers := a.allBeliefs()                            -- the receiver's beliefs about every other
                                                          --   agent (sorted by SubjectID, D12), the
                                                          --   observer set Influence aggregates over
  f := <the Function the signal concerns; if the signal carries no Function, resolve it from the
        subject's gossiped goal via Config.Functions, or use the signal's own Function field>
  infl := a.ToM.Influence(source, f, observers)          -- per-function Influence aggregate (D6-style)
  signalWeight := clamp01(trust * (1 + a.Cfg.InfluenceWeight * infl))

  // sourceModel is the source's Belief ABOUT the subject (what the source is claiming);
  // delivered in the signal payload or looked up from world.BeliefOf(source, subject).
  a.ToM.GossipUpdate(subject, sourceModel, signalWeight)
  ```

  > **Verify the exact tom API before coding.** Confirmed against `engine/tom/SPEC.md` §P4/§P6:
  > the fold method is `tom.ToM.GossipUpdate(subject core.AgentID, source Belief, trustWeight
  > float64) map[core.StatID]float64`, where `source` is the **source's Belief about the subject**
  > (not the raw signal), and `Influence` has signature
  > `tom.ToM.Influence(subject core.AgentID, function Function, observers []Belief) float64` — note
  > it takes a `function` argument (the §I pseudocode's `Influence(source, observers)` shorthand
  > omits it). There is no `GossipUpdate` that consumes a raw `Signal`; the agent must convert the
  > signal into a `(subject, sourceBeliefAboutSubject, weight)` triple. If a future tom change adds
  > a signal-shaped overload, update this section first.
- `GossipUpdate` returns the per-stat mean delta; the agent emits one `ReputationGossip` event per
  changed stat (the existing P4 contract, `engine/tom/SPEC.md` Out of Scope) when `emit` is
  threaded — pass `emit` into `processIncomingSignals` if the gossip-event emission is wired this
  batch (Open Question below).

**Add to `Config`** (all from `balance.yaml politics.*` / content, injected — D10):

```go
Functions          []FunctionSpec // function id → (goal Dimension, capability stat-set); content/glossary, D7
RelyCostThreshold   float64       // plan cost above which a Function counts self-unsolvable
RelyOnDelta         float64       // δ added to RelyOn on a self-failure (politics.relyon_delta)
VoteRelyThreshold   float64       // private reliance strength that licenses a Vote
UrgencyThreshold    float64       // politics.urgency_threshold — combined-urgency proxy above which a Vote is cast (§H)
InfluenceWeight     float64       // politics.influence_weight — Influence→signal-weight ratio
```

## Public Interface

```go
package agent

import (
    "github.com/dogring/bdg/engine/core"
    "github.com/dogring/bdg/engine/needs"
    "github.com/dogring/bdg/engine/perception"
    "github.com/dogring/bdg/engine/planner"
    "github.com/dogring/bdg/engine/rng"
    "github.com/dogring/bdg/engine/stats"
    "github.com/dogring/bdg/engine/tom"
    "github.com/dogring/bdg/engine/values"
)

// CopingState — unchanged from P1/P2.
type CopingState uint8
const (
    Idle      CopingState = iota
    Rebinding
    Longing
    Latent
    Apathy
)

// IntentKind / Intent / Signal / SignalKind — unchanged from P1/P2 (see types.go).

// FunctionSpec maps a Function id to the goal Dimension(s) it serves and the
// capability stat-set required to perform it. Injected via Config.Functions (D7/D10):
// it replaces the hardcoded goalToFunction mapping — the agent resolves a goal Dimension
// to its Function + capability Stats by scanning this table, never a literal.
type FunctionSpec struct {
    ID    core.Function  // e.g. core.FuncSafety
    Dim   core.Dimension // goal dimension this function covers (e.g. "Safety")
    Stats []core.StatID  // capability stats required to provide this function (passed to BestProviderFor)
}

// Agent is one simulated villager. P3 adds Resentment + FailStreak to the Body/coping state.
type Agent struct {
    ID  core.AgentID
    Pos core.Vec2

    // Body — dynamic state.
    Inventory       map[core.Tag]int
    Stamina         float64                    // consumable effort budget, [0, StaminaMax]
    Mood            float64                    // signed; += λ·(actual−expected), decays to baseline
    Adrenaline      float64                    // urgency-driven surge, [0, AdrMax]
    NeedIntensities map[core.Dimension]float64 // incl. the conditional Safety intensity (driven in §F)

    Resentment float64 // NEW (P3): accrues while Latent on trigger events; drives Aggression/Affinity drift (D2)

    RealStats stats.Stats // god view; OUTCOMES only, never read by decisions (D8)
    ToM       tom.ToM     // includes ToM[self]; decisions read ToM[self] (D8)
    Cfg       Config

    // Deliberation state across ticks.
    Goal    core.Dimension
    Plan    planner.Plan
    PlanIdx int
    Elapsed core.GameMinutes

    Coping     CopingState
    FailStreak int          // NEW (P3): consecutive failed re-plans; resets on any plan/action success
    Latent     []LatentGoal
    Values     []core.Value // P5: value directions beyond Self-needs (Place/Collective referents)
}

// New — unchanged signature; initializes Resentment = 0, FailStreak = 0.
func New(id core.AgentID, pos core.Vec2, realStats stats.Stats, selfToM tom.ToM, cfg Config) *Agent

// Config — P3 adds the coping + Stamina/Adrenaline-coupling constants; P5 adds the threat-tag set
// and the Safety dimension id; P6 Blocker Group A adds the conditional Safety-intensity scalars;
// P6 politics adds the Functions table + reliance/vote/influence ratios.
// Every field from content/balance.yaml / content/needs.yaml; no hardcoded constant (D10).
type Config struct {
    // ── existing P1/P2 fields (mood, adrenaline, stamina, urgency, β, resentment.{affinity_drop,
    //    aggression_drift}, stickiness, budget, gossip) — see agent.go ──
    //    incl. BudgetBase int and BudgetPerIntelligence int (per-agent base planner budget;
    //    consumed by replan to size the per-call ApathyBudget — see §A-budget).

    // stamina effort-level resolution (P3): effort tag → level (tag_levels.effort).
    EffortLevels map[core.Tag]float64 // balance.yaml tag_levels.effort, e.g. {effort:none:0,…,effort:high:.90}

    // adrenaline ↔ stamina coupling (P3).
    CrashStaminaPenalty float64 // balance.yaml adrenaline.crash_stamina_penalty (Stamina -= AdrDecay × this on crash)

    // coping cascade (P3 — replaces the budget-derived rebind threshold).
    RebindMinIntelligence float64 // < this → skip Rebinding, go Latent
    ApathyFailStreak      int     // balance.yaml coping.apathy_fail_streak (Latent → Apathy at this streak)
    ApathyRecoverMood     float64 // balance.yaml coping.apathy_recover_mood (Mood += this on Apathy→Idle)
    ApathyBudgetPenalty   float64 // balance.yaml coping.apathy_budget_penalty (planner budget × (1−this) while Apathy)

    // resentment accrual (P3).
    ResentmentPerTrigger float64 // balance.yaml resentment.per_trigger (Resentment += this × Vindictiveness)
    ResentmentThreshold  float64 // balance.yaml resentment.threshold (above → aggression_drift applies, every tick — §B-drift)

    // P5 additions
    ThreatTags []core.Tag      // balance.yaml threats.hostile_tags — tags that trigger defensive Safety goal insertion
    SafetyDim  core.Dimension  // content/needs.yaml Safety dimension id, resolved by platform/config

    // P6 Blocker Group A — conditional Safety-intensity driver (BLOCKER-1). Injected (D10).
    ThreatPerThreatGain float64 // balance.yaml threats.per_threat_intensity (Safety intensity += this × threat count)
    ThreatSafetyDecay   float64 // balance.yaml threats.safety_decay (Safety intensity -= this when no threat perceived)

    // P6 politics — reliance, vote, influence (all injected from balance.yaml politics.* / content).
    Functions         []FunctionSpec // function id → (goal Dimension, capability stat-set); replaces goalToFunction (D7)
    RelyCostThreshold float64        // plan cost above which a Function counts self-unsolvable (politics.rely_cost_threshold)
    RelyOnDelta       float64        // δ added to RelyOn on a self-failure (politics.relyon_delta)
    VoteRelyThreshold float64        // private reliance strength that licenses a Vote (politics.vote_rely_threshold)
    UrgencyThreshold  float64        // combined-urgency proxy above which a Vote is cast (politics.urgency_threshold, §H)
    VoteRelyOnDelta   float64        // δ a RECEIVER folds toward the voted holder on a heard Vote (politics.vote_relyon_delta)
    InfluenceWeight   float64        // politics.influence_weight — Influence→signal-weight ratio

    // collective-safety defensive trigger (BLOCKER-2 of the prior batch). When the mean collective
    // satisfaction (1 − mean member need-intensity) for a held Collective value's dimension drops
    // below this, the holder adopts that dimension as a defensive goal in phase 4 (→ Patrol). 0 disables it.
    SafetyThreatThreshold float64 // balance.yaml threats.safety_threat_threshold

    // trade-signal deceptive-claim band (Offer emission).
    ClaimInflateMin float64    // balance.yaml trade.claim_inflate_min (claim at perceived Honesty 1)
    ClaimInflateMax float64    // balance.yaml trade.claim_inflate_max (claim at perceived Honesty 0)
}

// DefaultConfig — extends the canonical Config with the P3/P5/P6 fields above, including a default
// Functions table (test defaults — platform/config provides the real ids):
//   Functions: []FunctionSpec{
//       {ID: core.FuncSafety,    Dim: "Safety",    Stats: []core.StatID{"Strength"}},
//       {ID: core.FuncKnowledge, Dim: "Knowledge", Stats: []core.StatID{"Intelligence"}},
//   }
func DefaultConfig() Config

// Services — unchanged (Sensor, Planner, Values, Needs, Stats, Actions).

// Tick — unchanged signature. P3 changes its internals: phase 5 sizes the per-call planner budget
// (ApathyBudget) from BudgetBase/BudgetPerIntelligence, reducing it while Apathy; phase 6 uses
// data-driven Stamina drain + Rest/Sleep regen; phase 8 adds the crash stamina debt + Resentment
// accrual + the per-tick Aggression-Drift threshold check (updateResentment). P5 adds the phase-1
// hostile/encroach scan (→ forced Safety goal) and folds the max Other-referent Priority into the
// injected Urgency. P6 Blocker Group A: phase 1 also drives the conditional Safety intensity via
// needs.UpdateConditionalNeeds (before phase-3 appraise). P6 politics: phase 0 folds incoming
// gossip (Influence-weighted) + votes; phase 5a runs the reliance trigger; phase 7 emits a Vote
// when combinedPriority exceeds UrgencyThreshold.
func (a *Agent) Tick(world WorldView, now core.Tick, rng *rng.RNG, svc Services, emit core.EventEmitter) []Intent

// WorldView — unchanged, but the world must now also report resentment-trigger events. P3 adds:
//   ResentmentTriggers(self core.AgentID) []core.AgentID
type WorldView interface {
    perception.WorldSnapshot
    SoundEvents() []perception.SoundEvent
    KnownObjects(self core.AgentID) []KnownObject
    BeliefOf(self, subject core.AgentID) (tom.Belief, bool)
    HasPendingOffer(receiver core.AgentID) bool
    ResentmentTriggers(self core.AgentID) []core.AgentID // NEW (P3)
    PlaceQuality(placeID core.ObjectID) float64           // NEW (P5, Scenario E)
    MemberNeedIntensities() map[core.AgentID]map[core.Dimension]float64 // NEW (P5)
    AgentIDs() []core.AgentID                                           // NEW (P6)
    IncomingSignals(self core.AgentID) []core.Signal                    // NEW (P6) — votes + hearsay/gossip
}

// ApplyOutcome — unchanged signature. P3 adds: on Status==Succeeded && Completed while
// Coping==Apathy → Coping=Idle, FailStreak=0, Mood += ApathyRecoverMood; FailStreak reset on any
// success; FailStreak increment is handled in the Tick coping path (not here).
func (a *Agent) ApplyOutcome(outcome ActionOutcome, rng *rng.RNG, cfg Config, reg *stats.Registry, emit core.EventEmitter)

// ActionOutcome / OutcomeStatus — unchanged from P1/P2.
```

> `tom.*`, `planner.*`, `perception.*`, `gates.*` types are the contracts those SPECs expose. This
> module composes them; it adds **no** vocabulary outside `docs/glossary.md` (Resentment, FailStreak,
> Stamina, Adrenaline, Mood, Urgency, Vindictiveness, Referent, Safety, Function, Reliance,
> Influence are all glossary terms).

## Decision-loop model (authoritative ordering)

Unchanged 8-phase order (perceive → decay → appraise → mediate → plan → execute → signal →
dynamics), with a phase-0 incoming-signal fold (P6). P3 touches **phase 5** (coping cascade
transitions + the per-call `ApathyBudget` sizing/reduction), **phase 6** (data-driven Stamina drain
+ Rest/Sleep regen), and **phase 8** (Adrenaline crash stamina debt + Resentment accrual + the
per-tick Aggression-Drift threshold check). P5 touches **phase 1** (hostile/encroach scan → forced
Safety goal, pre-empting phase 4) and **phase 3/5** (folding the max Other-referent Priority into the
injected Urgency). **P6 Blocker Group A adds the Safety-intensity drive in phase 1 — it MUST run
before phase-3 appraise.** P6 politics: **phase 0** folds incoming gossip (Influence-weighted) +
votes; **phase 5a** runs the reliance trigger (with `emit`); **phase 7** emits a Vote using the
phase-3 `combinedPriority`. The planner/gate snapshot built in phase 5 now carries the four live
body scalars, the referent-aware Urgency, and the optional `ApathyBudget`.

### Snapshot body-scalar injection (P3 — the gate coupling)

When building the planner `AgentSnapshot` (phase 5), the agent also populates the body scalars the
P3 gates read — `Stamina`, `Mood`, `Adrenaline`, `Urgency` (the same `Urgency` already computed for
adrenaline; P5 folds the max Other-referent Priority into it) — AND the optional `ApathyBudget`
(§A-budget). The planner forwards the body scalars into `gates.AgentSnapshot` when evaluating each
candidate and uses `ApathyBudget` (when non-nil) as the per-call search budget. The agent computes
these from its own Body/Coping; it never reads them back from the gate registry.

### Coping cascade (design §3) — completed transitions

```
phase-5 plan fail:
  FailStreak++
  perceived_intel = ToM[self].EstStats[<Intelligence>].Mean         (D8, D7-resolved id)

  if perceived_intel < RebindMinIntelligence:
      enter Latent directly (skip Rebinding + Longing — object-fixation, design §1.4)
  else switch Coping:
      Idle    → Rebinding (substitute the next-Priority goal; success → Idle, fail → Longing)
      Rebinding → Longing  (store the unmet goal as a LatentGoal)
      Longing → Latent     (start Resentment accrual)
      Latent  → if FailStreak ≥ ApathyFailStreak: Apathy   else: stay Latent (accrue Resentment)
      Apathy  → stay (until a different goal becomes plannable, or ApplyOutcome single-success)

phase-5 plan success (any state):
  FailStreak = 0; Coping = Idle
```

- The branch is a **pure function** of (failure cause, perceived Intelligence, FailStreak, prior
  state) — no rng draw (D12).
- While `Coping == Apathy`, `replan` shapes a **reduced** per-call budget by setting
  `snapshot.ApathyBudget = &planner.Budget{…}` with each cap multiplied by `(1 − ApathyBudgetPenalty)`
  (floored at 1) — so the apathetic agent deliberates less. When `Coping != Apathy`,
  `snapshot.ApathyBudget = nil` and the planner uses its configured budget. This is the sole budget
  channel (§A-budget); the planner config is never mutated.

### Stamina / Adrenaline / Mood / Resentment — see "Implement (P3)" §D/§E/§B above

The formulas there are authoritative. Mood and the existing β/forward-sim `expected` term are
unchanged from P1/P2. The Aggression-Drift threshold check is owned by `updateResentment` (every
tick), NOT `accrueResentment` (trigger-gated) — see §B-drift.

### Hostile/encroach Safety drive + defensive goal — see "Implement (P5/P6)" §F above

The phase-1 threat scan + Safety-intensity drive (`needs.UpdateConditionalNeeds`) + forced Safety
goal + immediate `GoalSelected` emission are authoritative there. The override is rng-free and
data-driven (`Config.ThreatTags`, `Config.SafetyDim`, `Config.ThreatPerThreatGain`,
`Config.ThreatSafetyDecay`).

### Reliance / Vote / Influence — see "Implement (P6)" §G/§H/§I above

`handleRelianceTrigger` resolves the Function via `Config.Functions` (no hardcoded mapping), takes
`emit` and emits `BeliefUpdated{cause:"reliance"}`; `emitVoteIfEligible` takes the phase-3
`combinedPriority` (the `distributedUrgency` helper is removed); `processIncomingSignals` folds both
votes and Influence-weighted gossip.

## Dependencies

(Unchanged set; P5/P6 add no new module import.)
- `engine/core` — ids, `Vec2`, `Tick`, `GameMinutes`, `Dimension`, `Tag`, `AgentID`, `Referent`,
  `Function` (`FuncSafety`/`FuncKnowledge`), `Signal`/`SignalVote`, `Value`, `EventEmitter`,
  `Event`. Emits
  `GoalSelected`/`PlanBuilt`/`ActionStarted`/`ActionDone`/`Interacted`/`BeliefUpdated`/`CopingEntered`/`ReputationGossip`.
- `engine/stats` — `*Registry` (capability set for Intelligence; **Vindictiveness**/**Aggression**
  disposition ids resolved from `Config`, never a literal — D7); `Stats`.
- `engine/needs` — `*Registry` (`Kinds(Consumable)`/`IDs()`, consumable forward-roll for decay +
  Mood expected; **`Def.UpdateConditionalNeeds` for the §F Safety-intensity drive**; P5 resolves the
  **Safety** Dimension id into `Config.SafetyDim` via platform/config, not a literal).
- `engine/values` — appraisal helpers (`ComputeStanding`/`Salience`/`EffValue`/`Priority`); P5 also
  uses `DeriveReferentInput` + applies the per-agent bond multiplier to the base weight before
  `ComputePriority`.
- `engine/tom` — `ToM`: `Observe` (β + the §B-drift Aggression fold), `AdjustAffinity` (the persisted
  Resentment Affinity drop), `AdjustRelyOn`/`BestProviderFor`/`Influence`/`GossipUpdate` (P6
  reliance + Influence-weighted gossip), `Self`/`Subjects`/`SelfID`. `Belief`/`StatEvidence`.
- `engine/planner` — `*Planner`, `Plan`, `Trace`, `AgentSnapshot` (incl. the new optional
  `ApathyBudget *Budget`), `Budget`, the sentinel errors.
- `engine/perception` — the senses; `WorldView` embeds `WorldSnapshot`. (The §F threat scan reads
  perceived entities + their copied `Tags` from `Sensor.Sight`.)
- `engine/rng` — injected `*RNG` (D12); the coping branch, the §F threat override, and the
  budget-sizing are rng-free.
- `engine/actions` — `*Registry` (effort-tag resolution for Stamina drain; Rest/Sleep detection).
- **Contract — NOT imported**: `engine/world` implements `WorldView` (dependency inversion).
- **Contract**: `content/balance.yaml` — existing blocks **plus** the `gates:` thresholds,
  `adrenaline.crash_stamina_penalty`, `tag_levels.effort`, `resentment.{per_trigger,threshold}`, the
  `coping:` block (incl. `apathy_budget_penalty`), the `intelligence:` block, the `threats:` block
  (`hostile_tags` → `Config.ThreatTags`; **`per_threat_intensity` → `Config.ThreatPerThreatGain` and
  `safety_decay` → `Config.ThreatSafetyDecay`**; `safety_threat_threshold` →
  `Config.SafetyThreatThreshold`), the `politics:` block (`rely_cost_threshold`, `relyon_delta`,
  `vote_rely_threshold`, `urgency_threshold`, `vote_relyon_delta`, `influence_weight` →
  `Config.RelyCostThreshold`/`RelyOnDelta`/`VoteRelyThreshold`/`UrgencyThreshold`/`VoteRelyOnDelta`/
  `InfluenceWeight`), and the `trade:` block. `Config.SafetyDim` comes from `content/needs.yaml` and
  `Config.Functions` from `content/glossary` (the Reliance edges) via platform/config. Injected via
  `platform/config` and wired in `backend/main.go` (`agentConfigFromBalance`) — see BLOCKER-2. No
  constant hardcoded (D10).

## Owned Data

- The `Agent` value (Body incl. `Resentment` and the conditional Safety intensity in
  `NeedIntensities`; coping incl. `FailStreak`; `Goal`/`Plan`/`PlanIdx`/`Elapsed`; `Values`; `ToM`;
  `RealStats`). `Tick`/`ApplyOutcome` are the only mutators.
- The `Intent`/`Signal`/`ActionOutcome`/`CopingState`/`LatentGoal`/`FunctionSpec`/`Config`/
  `Services`/`WorldView`/`KnownObject` types and the loop/coping/calibration/dynamics logic.
- `RealStats` is held for the world to read at outcome resolution; the decision side never reads it
  (D8).

## Invariants

(All P1/P2 invariants hold unchanged. P3/P5/P6 additions:)

- **Orchestrator only (D5)**: the coping/Stamina/Adrenaline/Resentment logic and the P5
  referent-aware Urgency / defensive-goal logic sequence and thread state; the agent computes no
  Standing/Priority of its own (it calls `engine/values`), assembles no action sequence (it calls
  `engine/planner`), and **computes no need-intensity arithmetic of its own** — the §F Safety drive
  delegates the math to `needs.Def.UpdateConditionalNeeds`. The per-call `ApathyBudget` is sized by
  the agent but the search itself is the planner's. The §F override forces the Safety **Dimension**
  only — it never decides how to satisfy it.
- **Decisions read `ToM[self]`, never Real Stats (D8)**: rebind Intelligence threshold and the
  `ApathyBudget`-sizing Intelligence read `ToM[self]`; Resentment's `Vindictiveness`/`Aggression`
  read `ToM[self]`; reliance reads ToM beliefs (`BestProviderFor`/`Influence`), never Real Stats;
  the body scalars injected into the gate snapshot are objective Body facts, NOT beliefs. A guard
  confirms `Tick` never reads `a.RealStats`.
- **Coping branch, §F drive & budget-sizing are deterministic (D12)**: the cascade step and the
  `ApathyBudget` caps are pure functions of (failure cause, perceived Intelligence, FailStreak,
  Coping, Config). The §F threat scan iterates perceived entities in ObjectID-sorted order and
  produces a deduped, AgentID-sorted threat list; reliance candidate iteration + `allBeliefs`
  iterate sorted SubjectIDs; no rng draw selects the branch, the drive, the budget, or the provider.
- **Resentment / roles / defense are emergent, not hardcoded (D2)**: Resentment is a scalar drift;
  the Aggression drift is threshold-gated and time-continuous (owned by `updateResentment`, every
  tick — §B-drift); reliance is `RelyOn` accretion with no role/chief type; the §F defensive reaction
  is the ordinary Safety-need pursuit triggered by a perceived **content-authored** threat tag and a
  driven Safety intensity. Affinity drops persist on the actual `Belief`.
- **No hardcoded Function mapping (D7/D10)**: the goal→Function resolution reads `Config.Functions`,
  never the removed `goalToFunction` "Safety→FuncSafety else FuncKnowledge" literal; the capability
  stat-set passed to `BestProviderFor` is the matched `FunctionSpec.Stats`, not a literal.
- **All rates / ids injected (D10/D7)**: no literal for the rebind threshold, apathy
  streak/recover/budget, crash stamina penalty, effort levels, resentment per-trigger/threshold, the
  threat-tag set, the Safety Dimension id, the §F `per_threat_intensity` / `safety_decay` scalars,
  **or the politics ratios** (`RelyCostThreshold`/`RelyOnDelta`/`VoteRelyThreshold`/
  `UrgencyThreshold`/`VoteRelyOnDelta`/`InfluenceWeight`). A grep guard confirms no balance-constant
  literal and no stat/need/action/dimension/tag id literal in logic
  (`Intelligence`/`Vindictiveness`/`Aggression`/`Mood`/`Rest`/`Sleep`/`Safety`/`hostile`/`encroach`
  all registry-/config-resolved).
- **Bounds**: `Stamina ∈ [0, StaminaMax]`; `Adrenaline ∈ [0, AdrMax]`; `Resentment ≥ 0`;
  `FailStreak ≥ 0`; the driven `NeedIntensities[SafetyDim] ∈ [0, 1]` (clamped by
  `UpdateConditionalNeeds`); each `ApathyBudget` cap ≥ 1; `signalWeight ∈ [0, 1]` (clamp01).
- **Determinism (D12)**: `Tick`/`ApplyOutcome` are pure functions of (Agent state, world snapshot,
  rng state, Config, Services); all iteration uses sorted keys/slices.

## Acceptance Criteria (testable)

(P1/P2 ACs remain. P3/P5/P6 additions — each maps to a unit/golden/scenario:)

- [ ] **Low-Intelligence skips Rebinding to Latent (scenario F, golden)**: an agent with perceived
  `Intelligence = 0.3` (< `RebindMinIntelligence = 0.4`) on a plan fail emits **one**
  `CopingEntered(Latent)` with **no** `CopingEntered(Rebinding)` and no `Longing`. A
  perceived-`Intelligence = 0.7` agent emits `CopingEntered(Rebinding)` first and (on repeated fail)
  `Longing → Latent`. Golden over the event sequence.
- [ ] **Cascade order + FailStreak (design §3)**: a contrived `ErrUnreachable` sequence drives a
  high-Intelligence agent `Idle → Rebinding → Longing → Latent → Apathy`; `FailStreak` increments
  per fail and resets to 0 on a success; `Apathy` is entered only at `FailStreak ≥ ApathyFailStreak`.
- [ ] **Apathy reduces the planner budget via ApathyBudget (gap-closure)**: while `Coping ==
  Apathy`, the `AgentSnapshot` built by `replan` has a non-nil `ApathyBudget` whose `MaxNodes` equals
  `max(1, int(effectiveNodes × (1 − ApathyBudgetPenalty)))` (and likewise for depth/actions); while
  `Coping != Apathy`, `ApathyBudget == nil`. A planner-level companion AC (in `engine/planner`)
  asserts the override is honored for that call only. Table-driven over {Apathy, non-Apathy}.
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
  (c) with no latent goals (`latentFactor == 0`) no drift applies.
- [ ] **Apathy single-success recovery (golden)**: an `Apathy` agent receiving
  `ApplyOutcome{Status: Succeeded, Completed: true}` resets `Coping = Idle`, `FailStreak = 0`,
  `Mood += ApathyRecoverMood`, and emits `CopingEntered(Idle)`.
- [ ] **Apathy narrows cognition via the gate**: an `Apathy` agent whose `Mood ≤ -0.60` builds a
  snapshot with `Mood ≤ -0.60`; the planner does not select an `abstraction:med`+ action. Once Mood
  recovers above `-0.60`, the abstract action is visible again.
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
- [ ] **Conscience urgency relief end-to-end (scenario B, golden)**: a starving agent whose
  `Urgency > conscience_urgency_threshold (0.70)` injects `Urgency`; the `conscience` gate's relief
  branch makes `Take` (`norm:transgressive`) visible, and `PlanBuilt` steps include `Take`.

### §F — Threat perception ACTIVATED: Safety-intensity drive + defensive goal (P6 Blocker Group A)

- [ ] **BLOCKER-1: perceived threat raises Safety intensity; absence decays it
  (`bind_target_test.go` / a new `scenario_threat_test.go`)**:
  - A stub `WorldView` whose `Sensor.Sight` returns an entity tagged `["agent", X]` where
    `X ∈ Config.ThreatTags` (use a stub `ThreatTags = ["X"]` so no literal is needed) → after one
    `Tick`, `a.NeedIntensities[cfg.SafetyDim] == ThreatPerThreatGain` (rose from 0, `> 0`).
  - Two threat entities present → `a.NeedIntensities[cfg.SafetyDim] == 2 × ThreatPerThreatGain`
    (proportional to count), clamped to `1.0` if the product exceeds 1.
  - A `WorldView` with NO threat entity in radius, starting from `a.NeedIntensities[cfg.SafetyDim] =
    0.50` and `ThreatSafetyDecay = 0.10` → after one `Tick` the intensity is `0.40`; after
    `⌈0.50/0.10⌉ = 5` threat-free ticks it is `0.0` (decays to 0 over N ticks).
  - Starting at `0` with no threat → stays `0` (never negative).
  Table-driven; the drive runs every tick (threat present or not), BEFORE phase-3 appraise.
- [ ] **BLOCKER-1: driven Safety intensity feeds the appraisal chain**: after a Tick in which a
  threat was perceived (Safety intensity raised to `> 0`), `appraise` returns a `DimensionPriority`
  for `cfg.SafetyDim` with `Priority > 0` (Safety now competes in mediation), whereas with the
  intensity at 0 the Safety `Priority` is 0. Asserts the wiring closes the loop the diagnosis found
  open (Safety satisfaction can drop below 1.0).
- [ ] **Hostile/encroach perception forces a Safety goal (reflex, scenario)**: an agent that
  perceives a threat-tagged entity sets `a.Goal == cfg.SafetyDim`, clears `a.Plan`, and emits
  **one** immediate `GoalSelected{dimension: SafetyDim, priority: 1.0, eff_value: 1.0}` ahead of
  phase-4 mediation. An agent perceiving NO threat tag this tick proceeds through normal phase-4
  mediation (no forced Safety goal, no extra `GoalSelected`). Table-driven over (threat present /
  absent); the threat tag is resolved from `Config.ThreatTags` (stub `ThreatTags = ["X"]`).
- [ ] **Threat scan is count-stable & deterministic (D12)**: a `WorldView` returning the same two
  threat entities in either order yields the same `a.NeedIntensities[cfg.SafetyDim]` (the threat
  list is deduped + AgentID-sorted before `UpdateConditionalNeeds`); two identical Ticks produce
  byte-identical `NeedIntensities` and intents.

### BLOCKER-2 — main.go wires ThreatTags / SafetyDim / threat scalars

These ACs live in a `backend` (main package) test, since the gap is in `agentConfigFromBalance`,
not in engine code. They are testable without writing engine `.go` files:

- [ ] **`agentConfigFromBalance` populates the threat fields**: given the shipped `content/balance.yaml`,
  `agentConfigFromBalance(parsedBalance)` returns a `Config` with
  `ThreatTags == ["violent:high", "hostile", "encroach"]` (order as authored),
  `ThreatPerThreatGain == balance.threats.per_threat_intensity`,
  `ThreatSafetyDecay == balance.threats.safety_decay`, and a non-empty `SafetyDim` equal to the
  Safety dimension id resolved from the loaded `needs.Registry`. Asserts none of the four is the
  zero value.
- [ ] **`SafetyDim` resolves to the conditional `PreventBelow` dimension**: the resolved
  `Config.SafetyDim` is the single `needs.Kinds(needs.Conditional)` entry whose `Posture ==
  needs.PreventBelow` (the shipped content's `Safety`), proving main.go does not hardcode the
  `"Safety"` string but resolves it from the registry.
- [ ] **End-to-end: a wired agent fires the Safety drive**: an `Agent` constructed with
  `DefaultConfig()` (which mirrors the balance values) perceiving a threat-tagged entity raises
  `NeedIntensities[cfg.SafetyDim]` above 0 — confirming the full path (config → §F drive → need
  intensity) is connected, not just the field assignment.

- [ ] **Scenario D — Other-referent lowers conscience threshold (P5, golden)**: (unchanged — see
  the prior batch's detailed setup; Priority(Other.Safety, C) > Priority(Self.Safety, A), Take
  becomes visible, golden Priority at seed 404.)
- [ ] **Scenario H — low-Intel leaves without provisions, starves (P5, golden)**: (unchanged — L's
  Plan is `[Travel]`, H's is `[Forage, Travel]`; golden Satiety intensities at tick 15, seed 505.)
- [ ] **No constant / id hardcoded (D10/D7 guard)**: grep guard — no rebind/apathy/crash/effort/
  resentment/**threat-scalar**/**politics-ratio** literal and no
  `Intelligence`/`Vindictiveness`/`Aggression`/`Mood`/`Rest`/`Sleep`/`Safety`/`hostile`/`encroach`
  string literal in logic; no hardcoded `goalToFunction` mapping (Function resolved via
  `Config.Functions`).
- [ ] **Determinism: identical tick reproduces (D12)**: same (Agent state, `WorldView` snapshot,
  rng seed, Config, Services) twice → byte-identical `[]Intent` + identical receiver mutations
  (incl. `Resentment`/`FailStreak`/`Stamina`/`Adrenaline`/the driven Safety intensity/the
  forced-Safety override/the `RelyOn` shifts). Golden digest.

### Scenario G — Village Chief Emergence Prerequisite (P5, Collective Referent Wiring)

This is a prerequisite for the full Scenario G (village chief emergence). The collective referent
wiring (`appraiseCollective`) enables an agent to care about the Safety of the village as a whole.
With the §F Safety-intensity drive now ACTIVATED, member Safety intensities actually rise under
threat, so `MemberNeedIntensities()` reports non-trivial Safety pressure and collective Safety
satisfaction (`1 − mean member intensity`) can drop below the threat threshold — the precondition
the chief-emergence integration run (`testdata/scenarios/p6_chief_emergence.yaml`) was missing.

**Defensive goal → Patrol (implemented).** A held Collective value with a protective Posture
(`MaintainAbove`/`PreventBelow`) drives a phase-4 goal override: `defensiveCollectiveGoal` computes
the mean collective satisfaction for the value's dimension and, when it drops below
`Cfg.SafetyThreatThreshold` (`balance.yaml threats.safety_threat_threshold`), forces that dimension
(Safety) as the goal. The planner resolves the Safety goal to **Patrol** (`content/actions.yaml`
Patrol `produces: [public_safety, has_Safety]`, `requires: [at_target]`). Agents holding no
Collective value are unaffected. Verified by `TestScenarioG_DefensiveGoalSelectsPatrol`.

Assertions:
- [ ] **Collective referent computes priority from member data**: `appraiseCollective` returns > 0
  when at least one Collective value is held and the world has member need data.
- [ ] **Aggregation mode "min"**: the collective Safety priority is driven by the minimum
  CurrentIntensity across members.
- [ ] **Aggregation mode "mean"**: the collective Safety priority is the mean across members.
- [ ] **Collective-referent priority feeds the urgency proxy**: the combined urgency proxy in
  `Tick` includes `maxCollective`, so a low collective Safety raises urgency above the conscience
  threshold, making pro-social actions (Patrol, Report) visible.
- [ ] **Determinism (D12)**: `appraiseCollective` iterates member IDs in sorted (AgentID) order.
- [ ] **No member data → no contribution**: when `MemberNeedIntensities()` returns nil, the
  collective referent contributes 0 to the urgency proxy.

### Scenario G (P6) — Reliance convergence, Vote, and Influence

- [ ] **Function resolved from `Config.Functions` (gap-closure)**: with a stub `Config.Functions =
  [{ID: FuncSafety, Dim: SafetyDim, Stats: [StrengthID]}, {ID: FuncKnowledge, Dim: KnowledgeDim,
  Stats: [IntelligenceID]}]`, `handleRelianceTrigger` pursuing a `SafetyDim` goal resolves Function
  `FuncSafety` and passes `[StrengthID]` to `BestProviderFor`; pursuing `KnowledgeDim` resolves
  `FuncKnowledge` with `[IntelligenceID]`; pursuing a Dimension absent from the table resolves no
  Function and forms NO reliance. Asserts the removed `goalToFunction` fallback is gone.
- [ ] **Reliance forms on a self-unsolvable Function (P6)**: an agent pursuing a `Safety`-Function
  goal that the planner reports unreachable OR whose cheapest plan cost > `Config.RelyCostThreshold`
  calls `ToM.AdjustRelyOn(provider, FuncSafety, RelyOnDelta)` toward `BestProviderFor`, and emits one
  `BeliefUpdated{cause:"reliance"}` (via the `emit` now passed into `handleRelianceTrigger`). When
  the agent CAN self-solve cheaply, no reliance forms and no event is emitted.
- [ ] **Vote uses the phase-3 combined urgency (gap-closure)**: `emitVoteIfEligible(now, world,
  combinedPriority)` emits a `SignalVote` iff `bestRelyOnFor(f).strength > VoteRelyThreshold` AND
  `combinedPriority > Config.UrgencyThreshold`; it does NOT recompute a separate distributed urgency
  (the `distributedUrgency` helper is removed). Table-driven over {high, low} `combinedPriority` and
  {above, below} reliance strength.
- [ ] **Vote accelerates convergence under distributed urgency (P6, CONTRAST)**: two runs of the
  same fixture. (a) HIGH distributed urgency (all members' Safety low at once → high
  `combinedPriority`) → agents emit `Vote` and the guardian's received-RelyOn share for `Safety`
  crosses `role_convergence_threshold` in markedly fewer ticks than (b) LOW urgency (no votes).
  Assert `ticks_to_converge(high) < ticks_to_converge(low)`.
- [ ] **Influence-weighted gossip moves ToM more (P6, TABLE)**: a receiver folds the SAME claim
  about C from two sources with equal `Trust` but different Influence in `processIncomingSignals`,
  using `signalWeight = clamp01(trust·(1 + InfluenceWeight·Influence(source, f, observers)))` and
  then `GossipUpdate(C, sourceBeliefAboutC, signalWeight)`. Assert the per-stat mean delta is
  monotonically non-decreasing in Influence for each `InfluenceWeight > 0`, and the
  `InfluenceWeight = 0` row is flat (Influence has no effect). Asserts `processIncomingSignals`
  handles non-Vote gossip signals (not only votes).

### Scenario C Extension — Gossip Trust-Cluster Propagation (P4)

(unchanged from the prior batch — gossip propagates within the trust cluster, non-witness cluster
unchanged, cluster reputation divergence variance > 0, `ReputationGossip` events emitted, golden at
seed 303.)

> Structural JSON-schema validation of the `content/balance.yaml` blocks this module reads (incl.
> the new `threats.per_threat_intensity` / `threats.safety_decay` scalars and the `politics:` block)
> is a **platform/config** AC.

## Out of Scope

(Unchanged from P1/P2.) Additionally:
- **The four P3 gate predicates themselves** → `engine/gates`.
- **Applying the `CostMultiplier` to the tag-cost sum** → `engine/planner`.
- **Consuming the `ApathyBudget` override in the search** → `engine/planner` (the agent only sizes
  and sets the optional `AgentSnapshot.ApathyBudget` pointer).
- **Resource-conflict resolution and "rejection without Offer"** → `engine/world` (via
  `WorldView.ResentmentTriggers`).
- **The persisted-Affinity write mechanism on `Belief`** → `engine/tom` (`AdjustAffinity`).
- **The `Influence` aggregate derivation + the `GossipUpdate` fold math** → `engine/tom`; this
  module only computes the Influence-weighted `signalWeight` it passes.
- **The conditional Safety-intensity ARITHMETIC** (the rise/decay/clamp math) → `engine/needs`
  (`Def.UpdateConditionalNeeds`); this module only scans perception for the threat list, supplies
  the two injected constants, and stores the result.
- **The two `threats.*` scalar values, the `politics.*` ratios, the Safety dimension-id resolution,
  and the `Config.Functions` table population from file** → `platform/config` + `backend/main.go`
  (`agentConfigFromBalance`); this module consumes the resulting `Config` fields.
- **The Other-referent input-derivation math (P5)** → `engine/values` (`DeriveReferentInput`).
- **The Intelligence-gated lookahead hard-skip (P5)** → `engine/planner`.

## Open Questions

- **`engine/tom` Affinity write path (RESOLVED in code).** `accrueResentment` persists the Resentment
  Affinity drop via `a.ToM.AdjustAffinity(triggerID, delta)`; the prior "mutates a copy" concern no
  longer applies. (The §B-drift gap-closure moves only the Aggression-Drift threshold check to
  `updateResentment`; the Affinity-drop path is unchanged.)
- **Gossip-event emission threading (gap-closure follow-up).** `processIncomingSignals` does not
  currently take an `emit core.EventEmitter`; the Influence-weighted gossip fold (§I-fold) should
  emit one `ReputationGossip` event per changed stat (the P4 contract). If that emission is in scope
  this batch, add `emit` to `processIncomingSignals`' signature and thread it from `Tick`'s phase 0;
  otherwise fold silently and flag to the reviewer. Flag to architect.
- **Vote urgency threshold field name (gap-closure).** §H standardizes on `Config.UrgencyThreshold`.
  If the current code carries `Config.VoteUrgencyThreshold`, reconcile to the single
  `UrgencyThreshold` field (and the `politics.urgency_threshold` balance key) so there are not two
  near-duplicate thresholds. Non-blocking; document the chosen name in the Data Contract.
- **§F threat list — agents only, or any entity? (RESOLVED for P6 Blocker Group A.)** A perceived
  threat is restricted to entities tagged `agent` (matching the `near_other` test), so
  `UpdateConditionalNeeds` receives `[]core.AgentID`. A future non-agent threat (e.g. a wildfire
  object tagged `encroach`) would need a separate object-threat channel; out of scope here — the
  brief's signature is agent-keyed.
- **`threats.per_threat_intensity` / `threats.safety_decay` initial values (NOT blocking).** The
  needs SPEC suggests `0.20` / `0.05`. Confirm with balance tuning; both are injected so a retune is
  a content edit (D10).
- **`coping:` / `intelligence:` blocks in `balance.yaml` (NOT blocking).** Resolved by the prior
  batch; `coping.rebind_min_intelligence` is DEPRECATED in favour of `intelligence.rebind_threshold`.

## Notes

- **Why the body scalars are injected, not gate-side state.** Gates are stateless; the live
  Stamina/Mood/Adrenaline/Urgency are this module's Body state, written into the per-tick snapshot.
- **Why the per-call `ApathyBudget` and not a planner-config mutation.** The planner is shared and
  immutable-after-New (safe across goroutines during plan); mutating its config per agent would race
  and break determinism. The per-call snapshot field scopes the override to one `Plan` call.
- **Why the Safety drive lives in phase 1, before appraise.** The conditional Safety intensity is
  STATE the appraisal chain reads; driving it after appraise would delay the Safety Priority by a
  tick and break the reflex. Running the §F drive at the end of phase 1 (right after the sight scan)
  guarantees phase-3 appraise sees the fresh intensity the same tick.
- **The §F defensive goal is a reflex, not a system (D2).** The hostile/encroach scan forces the
  Safety **Dimension** and drives the Safety **intensity** — the same Safety need the appraisal
  chain already arbitrates. The trigger (a content-authored threat tag) is data; the reaction
  (pursue Safety) is the ordinary planner pursuit. There is no hardcoded fight/flight subsystem.
- **Why the Aggression drift moved to `updateResentment`.** The drift is a function of accumulated
  `Resentment` (a continuous scalar), not of this tick's triggers — so it must run every tick.
  Leaving it behind `accrueResentment`'s `len(triggers)==0` early return meant a steeped grudge
  stopped hardening on quiet ticks. The Affinity drop stays trigger-gated (it needs a specific
  target). See §B-drift.
- **CopingEntered event mode strings (data-contracts §4).** Emit `mode` as the canonical string
  `rebind|longing|latent|apathy` (plus `idle` on recovery).
- **Intelligence / Vindictiveness / Aggression / Mood / Safety / Function id resolution (D7).** All
  resolved from the registries / `Config` (`svc.Stats` for stats, `svc.Needs`/config for the Safety
  dimension, `Config.Functions` for the goal→Function mapping), never a string literal in logic.
- **P4 gossip propagation is one-hop per tick.**
