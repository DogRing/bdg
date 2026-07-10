# Glossary — Single Source of Vocabulary

All code, SPECs, and docs use the **canonical identifier** in this table. No synonyms.
The `(KR: …)` hints cross-reference the Korean input docs (`docs/core/PRD.md`, `docs/core/design.md`).

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
- **`Dexterity`** (KR: 손재주): fine-manipulation capability. Read by the flora yield roll (`chance = §6(Dexterity)`, `docs/plans/flora.md` 1e), the Craft outcome roll (`basis_stat`, `docs/plans/materials.md §0`), the Mine yield roll (`§6(Dexterity, tool:digging.quality)`, Xm5), and later by any fine-manipulation action's `Formula`. D7: NOT a per-action skill — a base capability composed via §6. Mutable (train with use = general conditioning) — but the **training mechanism is a cross-cutting stats/lifecycle concern**, NOT owned by `engine/env/flora`/`engine/mind/actions` (they only *read* it).
- **No individual skills.** Action competence = composition of base attributes via a per-action **data formula** (`Formula`, `docs/core/design.md §6`), recomputed each time. The stat set is open content (D10); a new `kind` is a deliberate schema+engine extension. Base attributes are **mutable** (train with use = general conditioning, not a skill; drift with age, §7) — still no per-activity stored skills.

## Values & goals
| Concept | Canonical | Notes |
|---------|-----------|-------|
| Value | `Value{Dimension, Ref, Posture, Setpoint}` | Root of every goal. |
| Dimension | `Dimension` | `Satiety, Hydration, Rest, Safety, Standing, Openness…` (a `core.Dimension`; `engine/mind/needs` aliases it `NeedID`). |
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
| Fell | `Fell` | DESTRUCTIVE vegetation removal (a tree); triggers object-mortality (`docs/core/design.md §7`) + a wood yield. `docs/plans/flora.md` 1e. *(action def pending — flora SPEC Open Questions.)* |
| Plant | `Plant` | Plants a flora object and sets its `owner` (the economy seam, inert until economy ships). `docs/plans/flora.md` 1f. *(action def pending — flora SPEC Open Questions.)* |
| Craft | `Craft` | Single atomic action (D3) `recipe_mediated` by a `recipe` bound at plan time (Materials §0 FINAL). Applies the recipe's ordered SLOTS (each first-satisfiable alternative): `consume` removes `amount` (most-decayed lots first) / `wear` decrements the matched tool's durability (most-worn first, break at 0). Gated by the recipe's `ambient` stations (in-range). Outcome (success, qty, produced durability) rolled from the recipe's `basis_stat`; perishable output → fresh decay lot (Dm5). Carries NO tool tag, NO target, NO duration (the recipe supplies them). `content/actions.yaml` + `content/recipes.yaml`; `docs/plans/materials.md` P_m3. |
| Mine | `Mine` | DESTRUCTIVE-on-depletion extraction of a finite `ore_node` (Materials P_m4/Xm4 — parallel to flora `Fell`); NOT recipe-mediated, so tool-gated by an action-level `tool:digging` tag (the held pickaxe is WORN per use — durability decremented by a world/balance rate, break at 0); yields via §6(Dexterity, tool quality) (flora-yield reuse, Xm5). On the node's `remaining→0` the world removes it + fires one `navmap.SetTerrain` reroute (Xm2/Xm3). `content/actions.yaml`; `docs/plans/materials.md` P_m4. |
| Graze | `Graze` | FAUNA feeding action (F28): a herbivore seeks + crops vegetation; the controller steers toward the `scent.food` gradient, the world applies `hunger`↓ at a `forage`-tagged source (P_fa2). Scored by §6 utility (NOT GOAP); `produces: grazed` is fauna-internal (no agent goal chains to it). `content/actions.yaml`; `docs/plans/fauna.md`. |
| Flee | `Flee` | FAUNA full-speed flight from a SEEN predator (`sight.predator` → `fear` in the Flee §6 band, F43); steers directly away. Not a wary→flee FSM transition — the single `fear` value crossing utility bands (D3). `produces: fled`. `content/actions.yaml`. |
| Wary | `Wary` | FAUNA low-commitment vigilance from predator SCENT (`scent.predator` → `fear` in the Wary §6 band, F43); interrupts grazing + drifts slowly away. Distinct from `Flee` ONLY by the fear band (emergent). `produces: evaded`. `content/actions.yaml`. |

## Gates & cost
| Concept | Canonical | Notes |
|---------|-----------|-------|
| Gate | `Gate{id, tags []Tag, expr GateExpr}` | Tag-matched (D4) boolean visibility predicate over `ToM[self]` stats + action tags. Registered in a registry. |
| GateExpr | recursive predicate tree | leaf `{stat,op,value}` (reads `ToM[self]`, D8) or `{tag}`; composite `{and}`/`{or}`/`{not}`. |
| Stat formula | `Formula` | Data expression DSL (`docs/core/design.md §6`): arithmetic `+ - * /` + comparison + logical `& | !` over StatIDs / context vars. Output numeric (capability·cost·crossable-width·suitability·shade·yield-chance·decay-accel·tool-quality) or boolean. `GateExpr` is its boolean subset; one shared evaluator (`engine/kernel/expr`, L0). |
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
| Death | death event | Agent removed when a vital depletes (e.g. a failed risky river crossing). `docs/core/design.md §7`. |
| Reproduction | birth | New agent; with `Death` forms the population life-cycle. `docs/core/design.md §7`. |

## World & time
| Concept | Canonical | Notes |
|---------|-----------|-------|
| Coordinate | `Vec2` | Free coordinate (not tiled). |
| Spatial index | `SpatialHash` | Proximity / perception radius queries. |
| Time | `GameMinutes` / `Tick` | 24 game-h = 2 real-h (12×). Default tick = 1 game-minute. |
| Sense | `Sense` | `Sight(LoS) | Smell(gradient) | Hearing` |
| Terrain | `Terrain` | Data-defined type: base cost + traversal tags + **state** (`Moisture`…). Dynamic (transitions). `docs/core/design.md §5`. |
| Moisture | `Moisture` | Terrain wetness attribute; climate drives it; threshold → terrain **transition** (dry soil ⇄ wet ⇄ submerged). Vegetation is flora objects, not terrain. |
| Desire-path wear | `wear` (trail) | Sparse per-cell trail field; traffic↑→cost↓, decay→소멸. `cost = base × f(wear)`. (Distinct from TOOL `durability` + item DECAY below — overlapping KR word 마모/부패, different mechanisms.) |
| Flora | flora object | Plant (tree/bush/grass) **object** on continuous coords (D11), NOT terrain. `engine/env/flora`; `docs/plans/flora.md`, `docs/core/design.md §5`. Forests/succession EMERGE (D2). |
| Length | `Length` | Continuous plant **height** morphology axis ≥ 0 (world units, `engine/env/flora`). Integrates `Length += lenRate·Suitability` per flora step (own per-species §6 rate). Maturity proxy: discrete `Stage` is DERIVED from `Length` via species thresholds (never stored, D9). **Wood/resource yield ∝ `Length`** (taller tree → more wood). One of the two morphology axes that replaced the single `Growth` scalar (`docs/plans/flora.md` §1 refinement, 1b). |
| Width | `Width` | Continuous plant **girth/canopy-spread** morphology axis ≥ 0 (world units, `engine/env/flora`). Integrates `Width += widRate·Suitability` per flora step (own per-species §6 rate, independent of `Length` — pine grows tall faster than wide; oak the reverse). **Shade `Radius`/`Opacity` ∝ `Width`** (`ShadeOf` derives from `Width` + species §6). The second of the two morphology axes (`docs/plans/flora.md` §1 refinement, 1b). |
| Suitability | `Suitability` | §6 formula over terrain attrs + climate (`Moisture`/`Temperature`) → [0,1]; drives both flora growth axes (`Length`/`Width`) + (below θ, with hysteresis) death. `docs/plans/flora.md` 1b. |
| Shade | `Shade` | Per-plant occlusion PARAMETER (`Radius`/`Opacity` = §6(`Width`)) `engine/env/flora` emits; `engine/mind/perception` composes overlapping shade ∏(1−opacity) into LoS attenuation. "Dark forest" EMERGES from overlap (D2). NOT a terrain `light` attribute, NOT the binary `[opaque]` tag. `docs/plans/flora.md` 1d. |
| Shade occluder | `ShadeOccluder` | The perception-facing projection of a `Shade` caster (`{ID, Pos, Radius, Opacity}`) on `WorldSnapshot`; `world` adapts `flora.ShadeOf` into it (perception never imports flora). `backend/engine/mind/perception/SPEC.md`. |
| Cover tag | `cover` | A flora kind's `tags` marker (`content/objects.yaml`) declaring it a HIDING SPOT for prey: reaching cover-tagged flora lets a fleeing herbivore break a predator's line-of-sight (the fauna-realism M3 mechanic). Data-driven (D4/D10) — "what hides you" authored once; SEPARATE from `Shade` (LoS attenuation ∝ `Width`), a discrete affordance not an occlusion gradient. INERT until the cover-hiding mechanic is built (OFF-neutral). `docs/plans/fauna.md` (M3). |
| Exposure | `exposure` / `epsilon` | Local shelter factor ε∈[0,1] from `engine/env/exposure`: `local wind.mag = global wind.mag · epsilon`. Derived from `blocks_wind` blockers and forced interiors; data/tag-driven, never kind-hardcoded. `docs/plans/shelter.md`, `backend/engine/env/exposure/SPEC.md`. |
| Wind blocker | `blocks_wind` | Content tag/data declaring that an object/terrain footprint casts a directional wind shadow. world converts it to `exposure.Blocker`; exposure does not know wall/house/mountain names (D4/D10). |
| Shelter portal | `shelters` | Content tag/data declaring a portal object that can move an actor between exterior space and an interior region. Cave entrances are `shelters` portals, not privileged engine kinds. |
| Active space | `SpaceRef` | World-owned location context for a movable entity: exterior world or a specific continuous interior region. Missing/default is exterior. Positions remain continuous in both spaces (D11). |
| Cave interior | `InteriorRegion` | A separately generated small continuous region reached through a shelter portal. It has its own render/nav context; SH2 forces wind=0, rain blocked, temperature buffered. No z-layer/voxel/tile world. |

## Climate & weather (`docs/plans/climate.md`)
| Concept | Canonical | Notes |
|---------|-----------|-------|
| Climate field | `climate.State` | Coarse-grid `Moisture`+`Temperature` + world-uniform `Wind` + `RainProcess`; pure `Step(prev,Forcing,Rules,rng)→(next,transitions)`, world applies (`engine/env/climate`). |
| Temperature | `Temperature` (°C) | ACTUAL °C, NOT clamped (CA3): `AnnualMid + AnnualAmp·sin(2π·YearFraction+φ) + dailyDelta(HourOfDay) − TempRainDrop·raining`. §6 operand `temperature` (°C — consumer thresholds re-based at activation). |
| Annual cycle | `YearFraction` | [0,1) from `worldtime` (120-day calendar); drives the annual °C sinusoid (CA1). The one deterministic curve (not a process). |
| Diurnal phase | `DayFraction` | [0,1) from `worldtime` (`MinuteOfDay`/`DayMinutes`), the intra-day twin of `YearFraction`; 0 at midnight, 0.5 at solar-noon. The fauna `daylight` cue derives from it: `½(1−cos(2π·DayFraction))` (P_sleep1/FM11). |
| Wind | `Wind{Dir,Mag}` | World-uniform prevailing wind (CA2): `Dir` radians [0,2π) seeded directional random-walk mean-reverting to `WindPrevailingDir`; `Mag` [0,1]. §6 operands `wind.dir`/`wind.mag`. Drives scent spread + apparent temp. |
| Rain process | `RainProcess` | Seeded stochastic rain accumulator (1a): E[first rain]≈10d, 30d hard cap, 2–12h duration. Raises `Moisture`; lowers `Temperature` while raining. |
| Apparent temperature | `apparent_temp` (§6) | Per-ANIMAL felt temperature (fauna F40): §6 over `temperature`/`moisture`/`wind.mag` + the animal's size/stats. "Winter" EMERGES from sustained low `apparent_temp` (no season enum, D2). |
| Terrain transition | `climate.Transition` | A climate cell crossing a §6 threshold (`{Cell,From,To}`); world maps GridCell→[]navmap.Cell + calls `navmap.SetTerrain` (one-way: climate emits, world writes). `content/climate.yaml` `transitions`. |

## Fauna (`docs/plans/fauna.md`)
| Concept | Canonical | Notes |
|---------|-----------|-------|
| Animal | `fauna.Animal` | Reduced-reactive creature: `{Pos, Stats(open map), Drives, Stamina, Vital, Heading, CurrentAction, ActiveUntil}`. NO Value/ToM/planner (F1–F3); `world` applies its intents (combined agent+animal ID order, F41). `engine/fauna`. |
| Animal decision | horizon-1 utility | Per-tick: score each candidate (species×action) §6 `utility`, pick max (ties by ActionID). NOT a planner, NOT a behaviour tree (D3). F1/F26. |
| Drive | `DriveID` (open) | Animal motivation scalar ∈[0,1] = its lowercase §6 Attr operand (F27). Base vocab: `hunger`/`fatigue`/`repro_readiness` (accumulators by rate, D9) · `fear` (SET: `wary_level` on scent.predator / `flee_level` on sight.predator, F43) · `thermal` (from `apparent_temp`). Separate from agent `Value` (D5). |
| Wary vs Flee | one `fear` value | No FSM transition: `scent.predator`→fear into the Wary §6 band, `sight.predator`→the Flee band (F43/D3). |
| Senses | `Senses{smell,sight,fov_arc}` | Per-species radii: `smell_radius` (scent read), `sight_radius` (spatial query), `fov_arc` (HALF-angle radians of the Heading-relative forward sight cone — prey wide, predator narrow). F31/F44. |
| Adaptive cadence | DORMANT / ACTIVE | F45 frequency gate (NOT a behaviour state): dormant animals re-arbitrate every N ticks (ID-phase) + hold action; wake ACTIVE the tick predator scent reaches their cell (`IntensityAt`). Predators always ACTIVE. |
| Daylight | `daylight` (§6) | Diurnal light operand ∈[0,1]: 1 at solar-noon, 0 at midnight; `world` injects it from `DayFraction` (clock-derived, not weather). Drives the `Sleep` action; diurnal vs nocturnal EMERGE from the §6 sign (`(1−daylight)` vs `daylight`, D2/D10), no per-species flag. P_sleep1/FM11. |
| Sleep / torpor | `Sleep` / `state:sleep` / `TagSleep` | Night rest EMERGES: the shared `Sleep` action wins §6 utility at night (D3, no FSM; not a stored `Animal` field — sleeping ⇔ `CurrentAction`'s steer channel is `TagSleep`). The `state:sleep` tag ⇒ `TagSleep` steer (no-loco, `NextPos==Pos`) + deep fatigue recovery (`SleepFatigueRecoverPerTick`, else ordinary rest) + high F45 wake threshold (`SleepWakeScentThreshold` — a deep sleeper ignores a faint/distant predator, wakes to a near one → flee EMERGES). P_sleep1/FM11b/FM12/SS1–3. |
| Hazard avoidance | `hazard_avoidance` (§6 `e`) / hazard field | Continuous terrain-danger steering: `world` builds ONE shared danger potential field (`engine/space/field`), fauna adds `e·Repulsion(Pos)` to the heading (P_move1/FM5). A cost, NOT a block — a strong flee/drive overcomes it (deep water/cliff push harder & farther). Per-species scalar `e`; per-species fields deferred. |

## Scent (`engine/space/scent`)
| Concept | Canonical | Notes |
|---------|-----------|-------|
| Scent field | `scent.Grid` | Shared uniform multi-channel auxiliary index (spatial/navmap kin, `engine/space`), world-owned; per-cell per-channel SCALAR intensity (F21 revised). Derived state (rebuilt from emitters on resume, NOT serialized). |
| Scent channel | `Channel` | `ChanFood`/`ChanPrey`/`ChanPredator`/`ChanCarrion` (fixed order, append-only). §6 operands `scent.food`/`scent.prey`/`scent.predator`/`scent.carrion` (scalar). |
| Scent emitter tag | `scent:<channel>` | The tag a flora/fauna/decay kind carries so `world` deposits its magnitude into that channel (shared with `perception.Smell`, D4/D10 — "what is smelly" authored once). |
| Scent ops | `Deposit`/`Spread`/`Commit`/`Read`/`IntensityAt` | world drives deposit (predator every tick, food/prey bulk) + `Spread(Wind)` (tick%Ns diffusion) + `Commit` (next-tick latency). `Read`→per-channel intensity+gradient; `IntensityAt`=O(1) own-cell wake probe (F45). |

## World generation & integration (`docs/plans/world-gen.md` / `docs/plans/world-integration.md`)
| Concept | Canonical | Notes |
|---------|-----------|-------|
| World bounds | `bounds` (`WorldMin/Max`) | The finite rectangle the env grids (navmap/climate/scent) span; agents/animals keep CONTINUOUS unbounded `Pos` (D11) — bounds clamp only grid lookups. `content/world.yaml` (fixture may override). |
| Terrain type | base material | `soil`/`sand`/`river`/`lake`/`mountain`/`sea`(+`bare_rock`): a `navmap.TerrainType` (`BaseCost`/`Passable`/`RequiredTags`) + the §5 attribute preset. `content/terrain.yaml`. (Water = high-`BaseCost` traversable for fauna, not `!Passable`, R2; only `sea` blocks.) |
| Lake | `lake` | Still fresh water: an **outflow-less basin** the world-gen hydrology (WG2-a flow accumulation) marks; swimmable at river-like cost (never traps land fauna). `content/terrain.yaml`; `docs/plans/world-gen.md §1`. |
| Terrain attribute | §5 attr | `grain_size`/`moisture`/`temperature`/`depth`/`slope`/`salinity` — the `terrain.<attr>` §6 operand vector (flora suitability, traversal gates, climate transitions). `docs/core/design.md §5`. |
| Elevation | `elevation` | Per-cell relief ∈[0,1] the terrain generator emits (WG1-a stage 1, post-erosion). **Render-only** (3D hex height; `/api/terrain elevation[]`) — engine behavior (costs/gates/climate) never reads it. `backend/tools/worldgen`; data-contracts §6. |
| Nav cost field | `navmap.NavMap` | **Flat-top hex** (axial `q,r`) grid-indexed traversal cost + footprints + `wear` + dynamic terrain; `SetTerrain` + `TerrainOverrides` (sparse delta) + 6-neighbor `Neighbors`. The hex-convention authority (`docs/plans/hex-grid.md`). `engine/space/navmap`. |
| Hex cell | `navmap.Cell{Q,R}` | The internal **flat-top hexagonal** index (axial) navmap/pathfind compute over; NEVER an agent `Pos` (D11 — a cell *shape*, not a world tiling). Layout/wire use an **offset(col,row)** rectangular array (`i=row·cols+col`); offset↔axial conversion is engine-internal. `spatial`/`scent`/`climate` stay SQUARE. `docs/plans/hex-grid.md`. |
| World fixture | `Fixture` | The unified per-RUN layout (world-gen OR scenario, W9): `{seed, bounds, terrain grid, objects/agents/animals/flora/lots}`; absent block ⇒ subsystem OFF. `content/schema/fixture.schema.json`. Per-run, NOT content. |
| World-gen | `worldgen.Generate` | Author-time seeded WG1-a pipeline (elevation→hydrology→base material→distribution) → `Fixture`; runtime generation 0 (D12). `worldgen.Load` builds states → `InstallEnv`/`InstallFauna`. `backend/tools/worldgen`. |
| Env install | `InstallEnv` / `InstallFauna` | Opt-in world setup installing navmap/climate/flora/decay (+ animals/scent); NOT called ⇒ env OFF (outcome-neutral). `engine/world` SPEC-world-env/fauna. |
| Env adapters | `EnvSample` / `TerrainSampler` | Dependency-inversion adapters fauna declares + world fills: `EnvSample`=climate `CellAt`+`Wind` per animal; `TerrainSampler`={footprint-only `Passable`, terrain `Cost`} over navmap. |
| Render frame | `WorldFrame` | The SSE graphics frame (data-contracts §4): positions + flora/terrain deltas + ambient weather (hour/day-night, temperature, rain, wind). God-view EXCLUDED. Frontend `WorldFramePayload`. |

## Economy & ownership
| Concept | Canonical | Notes |
|---------|-----------|-------|
| Money | `currency` (item_kind) | Data item (D10), **not** emergent. Held in `Body.Inventory`; moves via trade. `docs/core/design.md §9`. |
| Ownership | `owner` (object→`AgentID`) | Builder-owned; **transferable** (sale) + **inheritable** (on `Death`, §7). Co-ownership parked. Wild flora is unowned; only PLANTED flora (`Plant`) carries `owner` (`docs/plans/flora.md` 1f). |
| Portal access | access `Formula` | Owner-controlled door/gate (map-plan M3). Pass/open = `has(key) | STR > lockStrength | paid(toll) | isOwner`. Soft lock = stat-contestable → emergent burglary (D2). |

## Materials & crafting (`docs/plans/materials.md`)
| Concept | Canonical | Notes |
|---------|-----------|-------|
| Material | `material tag` (on an item_kind) | A material is NOT a type — it is an item_kind carrying a MATERIAL `Tag` (D10 minimal extension). `content/objects.yaml` `tags:`. |
| Substitutable material | `tagQuery` | A recipe alternative queries a `tagQuery` (a `Tag` set, AND), NOT an item id; any kind whose `tags` ⊇ the query satisfies it → substitutability EMERGES from tags (D4). `docs/plans/materials.md §0`. |
| Recipe | `recipe` | DATA mediating the single `Craft` action (D3; Materials §0 FINAL): `{ id, inputs[], ambient[]?, duration, basis_stat, outputs[]{item, base_qty} }`. `inputs[]` = ordered SLOTS. `content/recipes.yaml`. |
| Recipe slot | input `slot` | One ordered input `{ any: [alternative,…] }`, satisfied by EXACTLY ONE alternative (OR); the world takes the FIRST satisfiable alternative in authored order (D12). `docs/plans/materials.md §0`. |
| Recipe alternative | `alternative` (`{tagQuery, amount, mode}`) | One way to satisfy a slot. `mode: consume` = remove `amount` matching items/units (most-decayed lots first, ties by ObjectID; works on a stackable lot OR a whole durable instance). `mode: wear` = the matched item MUST have a `tool` block; decrement its CURRENT durability by `amount`, break at 0, else persist (most-worn matching tool, ties by ID). `docs/plans/materials.md §0`. |
| Ambient station | `ambient` (station tags) | Recipe station tags the actor must be IN RANGE of (substitutable — any object carrying the tag; NOT consumed; optional). Bound in-range via the spatial hash by the world — there is NO Craft action target/station field (FINAL). `docs/plans/materials.md §0`. |
| Basis stat | `basis_stat` (StatID) | The recipe StatID whose roll drives the Craft outcome: success, qty, AND the produced instance's durability/quality (skilled → sturdier/more). A produced tool's start durability = roll·`wear_max`. `docs/plans/materials.md §0`. |
| Tool | `tool` (durable) | A durable item_kind carrying a `tool:<family>` `Tag` + a `tool:` block (`{ wear_max }` ONLY — the durability ceiling). Used by a recipe `mode: wear` alternative (Craft) or a non-recipe action tool tag (Mine); WEARS (current durability ↓) per use, breaks at 0; can also be `mode: consume`d as material. `docs/plans/materials.md §0`. |
| Tool durability | tool `durability` (instance) | Per-tool-INSTANCE CURRENT durability (FINAL; distinct from desire-path `wear`). Created at `basis_stat roll · wear_max`; a use decrements it by the recipe alternative's `amount` (Craft) or the action's world/balance rate (Mine); at 0 the tool BREAKS = object-mortality removal (§7). Runtime per-instance state (data-contracts §9), counts DOWN. No per-item `wear_per_use` (the amount is per-recipe / a world rate). `docs/plans/materials.md §0`. |
| Tool quality | `tool:<family>.quality` (§6 operand) | The §6 multiplier for the Mine yield reads the held tool's quality (= current `durability` / `wear_max`) via the world's `expr.Context.Attr("tool:<family>.quality")` operand (Cm3) — the `expr` L0 interface is UNCHANGED (a world-Context operand, like `terrain.depth`). A worn tool mines worse but works until break. (Craft uses `basis_stat`, not this operand.) `docs/plans/materials.md §0/Cm3`. |
| Station | `station` (object tag) | An object_kind carrying a `station:*` `Tag` referenced by a recipe's `ambient` list (e.g. `station:bench`, `station:forge` — NOT the literal `tool_bench`/`forge` kind). `docs/plans/materials.md §0`. |
| Finite source | `source` (object_kind) | An object_kind with a `source:` block carrying object-local `initial` → runtime per-instance `remaining` (D9 locality, Xm1; e.g. `ore_node`). Finite + scarce (Q5). `docs/plans/materials.md` Xm1. |
| Ore node | `ore_node` | The finite mineral `source` object_kind (Xm1) the `Mine` action targets; yields `stone`/ore via §6. On `remaining→0` → object-mortality removal + one `navmap.SetTerrain` → `depleted_terrain` (Xm2/Xm3). `content/objects.yaml`; `docs/plans/materials.md` P_m4. |
| Depletion reroute | `depleted_terrain` | The terrain type an `ore_node`'s cells become on exhaustion (Xm3, e.g. `bare_rock`); a permanent navmap reroute (Q4). Must exist in `content/terrain.yaml` (Xm6 prerequisite). `docs/plans/materials.md` Xm3. |
| Terrain-driven Mine | terrain `extract` | `Mine` over a terrain CELL (no ore_node) yields that terrain's `extract` table (stone anywhere, clay on soil) via §6(Dexterity, tool quality) — abundant, NO depletion (R1). Coexists with the ore_node node path (same `Mine`/`tool:digging`). `content/terrain.yaml`; `engine/world` SPEC-mine-terrain. |
| Ore kind | `iron_ore`/`copper_ore`/`coal_seam`/`gold_vein` | Per-mineral finite `source` object_kinds (specialized `ore_node`, R3) on `mountain`. `docs/plans/resources.md`. |
| Resource material tag | `stone_stock`/`clay_stock`/`fuel`/`ore:*`/`metal:*`/`precious` | Material tags (R4): `stone`→`stone_stock`, `clay`→`clay_stock`, `coal`→`fuel` (smelt consume), copper/iron ore→`ore:copper`/`ore:iron`→smelt `metal:copper`/`metal:iron`, `gold`→`precious`/`metal:gold`. Tier EMERGES from recipe input availability (D2). `docs/plans/resources.md` R4. |
| Smelting station | `station:furnace`/`station:kiln` | `Build`-built structures (a `station:*` object tag) a smelt/fire recipe's `ambient` references; metal/pottery emerge from recipes (no fire&light subsystem). `docs/plans/resources.md` R6. |
| Decay | item `decay` | Perishability as ordered discrete STATES (`fresh→stale→rotten→gone`) over an accumulated measure (`DecayAge`, derived `State` — never stored, mirrors flora `Stage`/D9). `engine/env/decay` (L1, pure Step); `docs/plans/materials.md` Q1/Dm1(a). Distinct from `wear` (trail) + tool `durability` — overlapping KR word, different mechanism. |
| Decay acceleration | `accel` (§6) | Env-coupled decay multiplier (§6 `Formula` over the climate output `temperature`/`moisture`, evaluated by `engine/env/decay` via `engine/kernel/expr`, Dm3(a)); cold+dry slows, warm+wet speeds → cold-storage EMERGES (D2). Composes MULTIPLICATIVELY: `effectiveRate = baseRate · accel · storageRateMult` (Dm2(a)). `docs/plans/materials.md` Q2/Dm2/Dm3. |
| Decay transform | `transform` | A decay transition PRODUCES an item (e.g. food → `rotten_matter`), not vanish — D9 locality, mirrors flora regen→object. `docs/plans/materials.md` Q3. |
| Storage rate multiplier | `StorageRateMult` | A storage structure's multiplier on the decay rate (world-injected value; `< 1` preserves). Cold-storage is emergent, not a hardcoded "fridge" (D2). `docs/plans/materials.md §0 eng/Dm2`. |
