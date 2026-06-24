# Glossary — Single Source of Vocabulary

All code, SPECs, and docs use the **canonical identifier** in this table. No synonyms.
The `(KR: …)` hints cross-reference the Korean input docs (`PRD.md`, `design.md`).

## State layers
| Concept | Canonical | Notes |
|---------|-----------|-------|
| Real stat (KR: 실제) | `Real Stats` | Objective value. Only god sees it accurately. Decides *outcomes* (success/failure). |
| Self-belief (KR: 자기 주관) | `ToM[self]` | Self-perception. *Decisions* (gates/cost) read this. Calibrated by action. |
| Other-belief (KR: 타인 주관) | `ToM[X]` | Observer X's belief. Differs per observer. |
| Open stat vector | `Stats` (`map[StatID]float64`) | Not a fixed struct. |
| Stat id | `StatID` | `Strength, Agility, Intelligence, Dexterity, Aggression, Impulsivity, Honesty, Greed, Sociability, Vindictiveness, RiskAversion` |
| Dynamic state | `Body` | `Inventory, Stamina, Mood, Goal, Plan, Pos` |

## Capability vs disposition
- **Capability**: Strength, Agility, Intelligence, Dexterity. Read by capability gates, outcomes, prediction.
- **Disposition** (value weights): Aggression, Impulsivity, Honesty, Greed, Sociability, Vindictiveness, RiskAversion. Raises goals.
- **`Intelligence`**: abstraction-ladder reach + ToM modeling depth + prediction (lookahead). A separate axis from Impulsivity.
- **`Dexterity`** (KR: 손재주): fine-manipulation capability. Read by the flora yield roll (`chance = §6(Dexterity)`, `docs/flora.md` 1e) and later by any fine-manipulation action's `Formula`. D7: NOT a per-action skill — a base capability composed via §6. Mutable (train with use = general conditioning) — but the **training mechanism is a cross-cutting stats/lifecycle concern**, NOT owned by `engine/flora` (flora only *reads* it).
- **No individual skills.** Action competence = composition of base attributes via a per-action **data formula** (`Formula`, `design.md §6`), recomputed each time. The stat set is open content (D10); a new `kind` is a deliberate schema+engine extension. Base attributes are **mutable** (train with use = general conditioning, not a skill; drift with age, §7) — still no per-activity stored skills.

## Values & goals
| Concept | Canonical | Notes |
|---------|-----------|-------|
| Value | `Value{Dimension, Ref, Posture, Setpoint}` | Root of every goal. |
| Dimension | `Dimension` | `Satiety, Hydration, Rest, Safety, Standing, Openness…` (a `core.Dimension`; `engine/needs` aliases it `NeedID`). |
| Referent | `Referent{Kind, ID}` | Pointer the evaluation reads. `Kind = Self|Other|Place|Collective` |
| Posture | `Posture` | `Maximize | MaintainAbove | PreventBelow` |
| Value map | `Known map[ObjectID]Valuation` | Per-agent standing value over known objects. |
| Standing value | `Standing` | Weighted sum of an object's dimension contributions. |
| Salience | `Salience` | Momentary boost from current needs + perceptual proximity. |
| Effective value | `EffValue = Standing · Salience` | Read by goal arbitration. |
| Need / Goal / Target | `Need ≠ Goal ≠ Target` | value / concrete goal-state / the object. |

## Actions & planning
| Concept | Canonical | Notes |
|---------|-----------|-------|
| Task | `Task` | Abstract; decomposes into `Method`s. |
| Method | `Method` | Tags + subtasks. Shared across parents (DAG). |
| Atomic action | `Action{Tags, Produces, Duration, Effect}` | Leaf. Durative. |
| Tag | `Tag` | `uses:Strength, effort:high, violent, noise:high, norm…` |
| Predicate | `Pred` | State key. `hasFood, atForest…` |
| Supply (satisfaction) | `Effect` | How much of which need an object satisfies. |
| Demand | `need-rate × predicted-time` | Computed, never authored. |
| Provisioning | forward-sim subgoal | Emergent, never authored. |
| Reverse index | `Producers map[Pred][]Action` | GOAP backward chaining. |
| Forage | `Forage` | NON-destructive harvest of a flora/source object (keeps the plant alive). `content/actions.yaml`. |
| Fell | `Fell` | DESTRUCTIVE vegetation removal (a tree); triggers object-mortality (`design.md §7`) + a wood yield. `docs/flora.md` 1e. *(action def pending — flora SPEC Open Questions.)* |
| Plant | `Plant` | Plants a flora object and sets its `owner` (the economy seam, inert until economy ships). `docs/flora.md` 1f. *(action def pending — flora SPEC Open Questions.)* |

## Gates & cost
| Concept | Canonical | Notes |
|---------|-----------|-------|
| Gate | `Gate{id, tags []Tag, expr GateExpr}` | Tag-matched (D4) boolean visibility predicate over `ToM[self]` stats + action tags. Registered in a registry. |
| GateExpr | recursive predicate tree | leaf `{stat,op,value}` (reads `ToM[self]`, D8) or `{tag}`; composite `{and}`/`{or}`/`{not}`. |
| Stat formula | `Formula` | Data expression DSL (`design.md §6`): arithmetic `+ - * /` + comparison + logical `& | !` over StatIDs / context vars. Output numeric (capability·cost·crossable-width·suitability·shade·yield-chance) or boolean. `GateExpr` is its boolean subset; one shared evaluator (`engine/expr`, L0). |
| Visibility (AND) | `visibility` | An action is visible iff **every** matching gate's `expr` is true (hard AND). Gates carry no cost. |
| Cost (tag-derived) | planner cost | Action cost = tag-derived terms (`effort, risk, moral, social…`) composed in the **planner**, not in gates. |
| Cost-term library | `effort, risk, moral, social…` | Reusable terms composing cost (`balance.yaml cost_terms × tag_levels`), read by the planner. |
| Deliberation budget | `Budget` | Search depth. |
| Stickiness | `Stickiness` | Bonus to keep the current goal (anti-thrash). |

## Social
| Concept | Canonical | Notes |
|---------|-----------|-------|
| Belief | `Belief` (= `ToM[X]`) | `EstStats, Trust, Affinity, Ledger, RelyOn, LastSeen` |
| Reputation | (distribution) | Distribution of `ToM_*[C]`. Not stored; mean is derived. |
| Gossip | gossip update | `ToM_A[C] += α·trust_A[B]·(claim_B[C] − ToM_A[C])` |
| Signal | `Signal{Kind, Intent, Valence, ClaimedValue, Truth, Intensity}` | `Truth` is hidden. |
| Reliance edge | `RelyOn map[Function]float64` | Who an agent relies on for what. |
| Influence | `Influence` | Social weight of a signal. |
| Emergent role | reliance cluster | Not a type. |

## Dynamics
| Concept | Canonical | Notes |
|---------|-----------|-------|
| Mood | `Mood` | `+= λ·(actual − expected progress)`, decays to baseline. |
| Adrenaline | `Adrenaline` | Urgency-triggered surge → loosens planner cost/visibility → crash. |
| Stamina | `Stamina` | Consumable budget; replenished by sleep/rest. |
| Urgency | `Urgency` | Acts on deliberation time, conscience threshold, adrenaline. |
| Coping | `Coping` | rebind / longing / latent / apathy. |
| Resentment | `Resentment` | Affinity↓, Aggression drift. |
| Self-calibration rate | `β` | Per-stat self-perception update. |
| Death | death event | Agent removed when a vital depletes (e.g. a failed risky river crossing). `design.md §7`. |
| Reproduction | birth | New agent; with `Death` forms the population life-cycle. `design.md §7`. |

## World & time
| Concept | Canonical | Notes |
|---------|-----------|-------|
| Coordinate | `Vec2` | Free coordinate (not tiled). |
| Spatial index | `SpatialHash` | Proximity / perception radius queries. |
| Time | `GameMinutes` / `Tick` | 24 game-h = 2 real-h (12×). Default tick = 1 game-minute. |
| Sense | `Sense` | `Sight(LoS) | Smell(gradient) | Hearing` |
| Terrain | `Terrain` | Data-defined type: base cost + traversal tags + **state** (`Moisture`…). Dynamic (transitions). `design.md §5`. |
| Moisture | `Moisture` | Terrain wetness attribute; climate drives it; threshold → terrain **transition** (dry soil ⇄ wet ⇄ submerged). Vegetation is flora objects, not terrain. |
| Desire-path wear | `wear` | Sparse per-cell trail field; traffic↑→cost↓, decay→소멸. `cost = base × f(wear)`. |
| Flora | flora object | Plant (tree/bush/grass) **object** on continuous coords (D11), NOT terrain. `engine/flora`; `docs/flora.md`, `design.md §5`. Forests/succession EMERGE (D2). |
| Growth | `Growth` | Continuous plant maturity ∈ [0,1] (`engine/flora`). Discrete stages are DERIVED via species thresholds — never stored (D9: no future field). `docs/flora.md` 1b. |
| Suitability | `Suitability` | §6 formula over terrain attrs + climate (`Moisture`/`Temperature`) → [0,1]; drives flora growth + (below θ, with hysteresis) death. `docs/flora.md` 1b. |
| Shade | `Shade` | Per-plant occlusion PARAMETER (`Radius`/`Opacity` = §6(Growth)) `engine/flora` emits; `engine/perception` composes overlapping shade ∏(1−opacity) into LoS attenuation. "Dark forest" EMERGES from overlap (D2). NOT a terrain `light` attribute, NOT the binary `[opaque]` tag. `docs/flora.md` 1d. |
| Shade occluder | `ShadeOccluder` | The perception-facing projection of a `Shade` caster (`{ID, Pos, Radius, Opacity}`) on `WorldSnapshot`; `world` adapts `flora.ShadeOf` into it (perception never imports flora). `backend/engine/perception/SPEC.md`. |

## Economy & ownership
| Concept | Canonical | Notes |
|---------|-----------|-------|
| Money | `currency` (item_kind) | Data item (D10), **not** emergent. Held in `Body.Inventory`; moves via trade. `design.md §9`. |
| Ownership | `owner` (object→`AgentID`) | Builder-owned; **transferable** (sale) + **inheritable** (on `Death`, §7). Co-ownership parked. Wild flora is unowned; only PLANTED flora (`Plant`) carries `owner` (`docs/flora.md` 1f). |
| Portal access | access `Formula` | Owner-controlled door/gate (map-plan M3). Pass/open = `has(key) | STR > lockStrength | paid(toll) | isOwner`. Soft lock = stat-contestable → emergent burglary (D2). |
