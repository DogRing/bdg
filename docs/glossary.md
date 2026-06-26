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
- **`Dexterity`** (KR: 손재주): fine-manipulation capability. Read by the flora yield roll (`chance = §6(Dexterity)`, `docs/flora.md` 1e), the Craft outcome roll (`basis_stat`, `docs/materials.md §0`), the Mine yield roll (`§6(Dexterity, tool:digging.quality)`, Xm5), and later by any fine-manipulation action's `Formula`. D7: NOT a per-action skill — a base capability composed via §6. Mutable (train with use = general conditioning) — but the **training mechanism is a cross-cutting stats/lifecycle concern**, NOT owned by `engine/flora`/`engine/actions` (they only *read* it).
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
| Tag | `Tag` | `uses:Strength, effort:high, violent, noise:high, tool:cutting, tool:digging, station:bench, norm…` |
| Predicate | `Pred` | State key. `hasFood, atForest…` |
| Supply (satisfaction) | `Effect` | How much of which need an object satisfies. |
| Demand | `need-rate × predicted-time` | Computed, never authored. |
| Provisioning | forward-sim subgoal | Emergent, never authored. |
| Reverse index | `Producers map[Pred][]Action` | GOAP backward chaining. |
| Forage | `Forage` | NON-destructive harvest of a flora/source object (keeps the plant alive). `content/actions.yaml`. |
| Fell | `Fell` | DESTRUCTIVE vegetation removal (a tree); triggers object-mortality (`design.md §7`) + a wood yield. `docs/flora.md` 1e. *(action def pending — flora SPEC Open Questions.)* |
| Plant | `Plant` | Plants a flora object and sets its `owner` (the economy seam, inert until economy ships). `docs/flora.md` 1f. *(action def pending — flora SPEC Open Questions.)* |
| Craft | `Craft` | Single atomic action (D3) `recipe_mediated` by a `recipe` bound at plan time (Materials §0 FINAL). Applies the recipe's ordered SLOTS (each first-satisfiable alternative): `consume` removes `amount` (most-decayed lots first) / `wear` decrements the matched tool's durability (most-worn first, break at 0). Gated by the recipe's `ambient` stations (in-range). Outcome (success, qty, produced durability) rolled from the recipe's `basis_stat`; perishable output → fresh decay lot (Dm5). Carries NO tool tag, NO target, NO duration (the recipe supplies them). `content/actions.yaml` + `content/recipes.yaml`; `docs/materials.md` P_m3. |
| Mine | `Mine` | DESTRUCTIVE-on-depletion extraction of a finite `ore_node` (Materials P_m4/Xm4 — parallel to flora `Fell`); NOT recipe-mediated, so tool-gated by an action-level `tool:digging` tag (the held pickaxe is WORN per use — durability decremented by a world/balance rate, break at 0); yields via §6(Dexterity, tool quality) (flora-yield reuse, Xm5). On the node's `remaining→0` the world removes it + fires one `navmap.SetTerrain` reroute (Xm2/Xm3). `content/actions.yaml`; `docs/materials.md` P_m4. |

## Gates & cost
| Concept | Canonical | Notes |
|---------|-----------|-------|
| Gate | `Gate{id, tags []Tag, expr GateExpr}` | Tag-matched (D4) boolean visibility predicate over `ToM[self]` stats + action tags. Registered in a registry. |
| GateExpr | recursive predicate tree | leaf `{stat,op,value}` (reads `ToM[self]`, D8) or `{tag}`; composite `{and}`/`{or}`/`{not}`. |
| Stat formula | `Formula` | Data expression DSL (`design.md §6`): arithmetic `+ - * /` + comparison + logical `& | !` over StatIDs / context vars. Output numeric (capability·cost·crossable-width·suitability·shade·yield-chance·decay-accel·tool-quality) or boolean. `GateExpr` is its boolean subset; one shared evaluator (`engine/expr`, L0). |
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
| Desire-path wear | `wear` (trail) | Sparse per-cell trail field; traffic↑→cost↓, decay→소멸. `cost = base × f(wear)`. (Distinct from TOOL `durability` + item DECAY below — overlapping KR word 마모/부패, different mechanisms.) |
| Flora | flora object | Plant (tree/bush/grass) **object** on continuous coords (D11), NOT terrain. `engine/flora`; `docs/flora.md`, `design.md §5`. Forests/succession EMERGE (D2). |
| Length | `Length` | Continuous plant **height** morphology axis ≥ 0 (world units, `engine/flora`). Integrates `Length += lenRate·Suitability` per flora step (own per-species §6 rate). Maturity proxy: discrete `Stage` is DERIVED from `Length` via species thresholds (never stored, D9). **Wood/resource yield ∝ `Length`** (taller tree → more wood). One of the two morphology axes that replaced the single `Growth` scalar (`docs/flora.md` §1 refinement, 1b). |
| Width | `Width` | Continuous plant **girth/canopy-spread** morphology axis ≥ 0 (world units, `engine/flora`). Integrates `Width += widRate·Suitability` per flora step (own per-species §6 rate, independent of `Length` — pine grows tall faster than wide; oak the reverse). **Shade `Radius`/`Opacity` ∝ `Width`** (`ShadeOf` derives from `Width` + species §6). The second of the two morphology axes (`docs/flora.md` §1 refinement, 1b). |
| Suitability | `Suitability` | §6 formula over terrain attrs + climate (`Moisture`/`Temperature`) → [0,1]; drives both flora growth axes (`Length`/`Width`) + (below θ, with hysteresis) death. `docs/flora.md` 1b. |
| Shade | `Shade` | Per-plant occlusion PARAMETER (`Radius`/`Opacity` = §6(`Width`)) `engine/flora` emits; `engine/perception` composes overlapping shade ∏(1−opacity) into LoS attenuation. "Dark forest" EMERGES from overlap (D2). NOT a terrain `light` attribute, NOT the binary `[opaque]` tag. `docs/flora.md` 1d. |
| Shade occluder | `ShadeOccluder` | The perception-facing projection of a `Shade` caster (`{ID, Pos, Radius, Opacity}`) on `WorldSnapshot`; `world` adapts `flora.ShadeOf` into it (perception never imports flora). `backend/engine/perception/SPEC.md`. |

## Economy & ownership
| Concept | Canonical | Notes |
|---------|-----------|-------|
| Money | `currency` (item_kind) | Data item (D10), **not** emergent. Held in `Body.Inventory`; moves via trade. `design.md §9`. |
| Ownership | `owner` (object→`AgentID`) | Builder-owned; **transferable** (sale) + **inheritable** (on `Death`, §7). Co-ownership parked. Wild flora is unowned; only PLANTED flora (`Plant`) carries `owner` (`docs/flora.md` 1f). |
| Portal access | access `Formula` | Owner-controlled door/gate (map-plan M3). Pass/open = `has(key) | STR > lockStrength | paid(toll) | isOwner`. Soft lock = stat-contestable → emergent burglary (D2). |

## Materials & crafting (`docs/materials.md`)
| Concept | Canonical | Notes |
|---------|-----------|-------|
| Material | `material tag` (on an item_kind) | A material is NOT a type — it is an item_kind carrying a MATERIAL `Tag` (D10 minimal extension). `content/objects.yaml` `tags:`. |
| Substitutable material | `tagQuery` | A recipe alternative queries a `tagQuery` (a `Tag` set, AND), NOT an item id; any kind whose `tags` ⊇ the query satisfies it → substitutability EMERGES from tags (D4). `docs/materials.md §0`. |
| Recipe | `recipe` | DATA mediating the single `Craft` action (D3; Materials §0 FINAL): `{ id, inputs[], ambient[]?, duration, basis_stat, outputs[]{item, base_qty} }`. `inputs[]` = ordered SLOTS. `content/recipes.yaml`. |
| Recipe slot | input `slot` | One ordered input `{ any: [alternative,…] }`, satisfied by EXACTLY ONE alternative (OR); the world takes the FIRST satisfiable alternative in authored order (D12). `docs/materials.md §0`. |
| Recipe alternative | `alternative` (`{tagQuery, amount, mode}`) | One way to satisfy a slot. `mode: consume` = remove `amount` matching items/units (most-decayed lots first, ties by ObjectID; works on a stackable lot OR a whole durable instance). `mode: wear` = the matched item MUST have a `tool` block; decrement its CURRENT durability by `amount`, break at 0, else persist (most-worn matching tool, ties by ID). `docs/materials.md §0`. |
| Ambient station | `ambient` (station tags) | Recipe station tags the actor must be IN RANGE of (substitutable — any object carrying the tag; NOT consumed; optional). Bound in-range via the spatial hash by the world — there is NO Craft action target/station field (FINAL). `docs/materials.md §0`. |
| Basis stat | `basis_stat` (StatID) | The recipe StatID whose roll drives the Craft outcome: success, qty, AND the produced instance's durability/quality (skilled → sturdier/more). A produced tool's start durability = roll·`wear_max`. `docs/materials.md §0`. |
| Tool | `tool` (durable) | A durable item_kind carrying a `tool:<family>` `Tag` + a `tool:` block (`{ wear_max }` ONLY — the durability ceiling). Used by a recipe `mode: wear` alternative (Craft) or a non-recipe action tool tag (Mine); WEARS (current durability ↓) per use, breaks at 0; can also be `mode: consume`d as material. `docs/materials.md §0`. |
| Tool durability | tool `durability` (instance) | Per-tool-INSTANCE CURRENT durability (FINAL; distinct from desire-path `wear`). Created at `basis_stat roll · wear_max`; a use decrements it by the recipe alternative's `amount` (Craft) or the action's world/balance rate (Mine); at 0 the tool BREAKS = object-mortality removal (§7). Runtime per-instance state (data-contracts §9), counts DOWN. No per-item `wear_per_use` (the amount is per-recipe / a world rate). `docs/materials.md §0`. |
| Tool quality | `tool:<family>.quality` (§6 operand) | The §6 multiplier for the Mine yield reads the held tool's quality (= current `durability` / `wear_max`) via the world's `expr.Context.Attr("tool:<family>.quality")` operand (Cm3) — the `expr` L0 interface is UNCHANGED (a world-Context operand, like `terrain.depth`). A worn tool mines worse but works until break. (Craft uses `basis_stat`, not this operand.) `docs/materials.md §0/Cm3`. |
| Station | `station` (object tag) | An object_kind carrying a `station:*` `Tag` referenced by a recipe's `ambient` list (e.g. `station:bench`, `station:forge` — NOT the literal `tool_bench`/`forge` kind). `docs/materials.md §0`. |
| Finite source | `source` (object_kind) | An object_kind with a `source:` block carrying object-local `initial` → runtime per-instance `remaining` (D9 locality, Xm1; e.g. `ore_node`). Finite + scarce (Q5). `docs/materials.md` Xm1. |
| Ore node | `ore_node` | The finite mineral `source` object_kind (Xm1) the `Mine` action targets; yields `stone`/ore via §6. On `remaining→0` → object-mortality removal + one `navmap.SetTerrain` → `depleted_terrain` (Xm2/Xm3). `content/objects.yaml`; `docs/materials.md` P_m4. |
| Depletion reroute | `depleted_terrain` | The terrain type an `ore_node`'s cells become on exhaustion (Xm3, e.g. `bare_rock`); a permanent navmap reroute (Q4). Must exist in `content/terrain.yaml` (Xm6 prerequisite). `docs/materials.md` Xm3. |
| Decay | item `decay` | Perishability as ordered discrete STATES (`fresh→stale→rotten→gone`) over an accumulated measure (`DecayAge`, derived `State` — never stored, mirrors flora `Stage`/D9). `engine/decay` (L1, pure Step); `docs/materials.md` Q1/Dm1(a). Distinct from `wear` (trail) + tool `durability` — overlapping KR word, different mechanism. |
| Decay acceleration | `accel` (§6) | Env-coupled decay multiplier (§6 `Formula` over the climate output `temperature`/`moisture`, evaluated by `engine/decay` via `engine/expr`, Dm3(a)); cold+dry slows, warm+wet speeds → cold-storage EMERGES (D2). Composes MULTIPLICATIVELY: `effectiveRate = baseRate · accel · storageRateMult` (Dm2(a)). `docs/materials.md` Q2/Dm2/Dm3. |
| Decay transform | `transform` | A decay transition PRODUCES an item (e.g. food → `rotten_matter`), not vanish — D9 locality, mirrors flora regen→object. `docs/materials.md` Q3. |
| Storage rate multiplier | `StorageRateMult` | A storage structure's multiplier on the decay rate (world-injected value; `< 1` preserves). Cold-storage is emergent, not a hardcoded "fridge" (D2). `docs/materials.md §0 eng/Dm2`. |
