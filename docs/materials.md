# Materials & Crafting — 물질 사슬 (재료·대체·레시피·부패) — Subsystem Plan (DRAFT)

Concept & rationale: `docs/design.md §9`(경제) + §5(자원) + §6(수식). 이 문서는 **결정 표면**이고 SPEC은 아직 없다.
관련 모듈: `content/objects.yaml`(item_kind + 재료 tag), `content/recipes.yaml`(신규, 변환), `content/schema/*`,
`engine/mind/actions`(Craft·채굴), `engine/env/decay`(신규 L1, 부패 Step), `engine/world`(유일 mutator), `engine/kernel/expr`(§6 품질·도구배수·tag질의).

**세계 완성 로드맵(사람 확정):** (지금) **물질사슬 → fauna → 불&빛**. 몸/needs는 **Agent 단계로 이연**. 모방(지식전수)은 추후.

## 0. Decisions locked
- 재료도 별도 타입 아님 — **item_kind + 재료 tag**(D10 최소 확장).
- **대체가능 재료 = tag 질의:** 레시피 input은 특정 item이 아니라 **tag 집합**(예: `shaft_stock`)을 qty로 요구 → "나무 or 뼈"가 같은 tag면 자동 호환. 대체가능성이 tag에서 *창발*(D4). (명시 클래스 목록 대신 tag — 새 item이 tag만 달면 자동 자격.)
- 레시피=데이터, **Craft = 레시피로 매개되는 단일 원자행위**(D3; yield-table와 동형). 품질/산출은 **§6(Dexterity 등)→chance/qty** (yield 모델 재사용, 신규 메커니즘 금지).
- 도구 = 소비 안 되는 **내구재(`wear`)**, 행위 역량 배수는 **§6 수식이 보유도구 tag 참조**.
- **부패 시간 필수.** 부패 = 순수 변환 Step(flora Step과 동형), **world가 유일 mutator**, multi-rate cadence, 결정성(D12).
- 몸/needs·fauna·불&빛은 이 plan **밖**(이연).

### Recipe model — FINAL (locked; supersedes the flat `consume:` input model + Cm4/Cm5/Cm6 specifics, refines Cm2/Cm7)
Craft has **NO** tool/station action tag — the gate is purely "inputs present". A recipe is:
- `id`
- `inputs[]` = ordered list of **SLOTS**; each slot `{ any: [alternative, …] }`, satisfied by exactly **ONE** alternative (OR).
  - alternative `{ tagQuery: [tags] (AND — matched item must carry ALL), amount: int≥1, mode: wear|consume }`.
    - `consume` — remove `amount` matching items/units (Cm1 **most-decayed-first**, ties by ObjectID); works on stackable lots **AND** a whole durable instance (a tool consumed as material → instance removed; "칼>더 좋은 칼").
    - `wear` — matched item **MUST** carry a `tool` durability block; decrement its current durability by `amount`; break (object-mortality remove) at 0; else **persists**.
- `ambient[]` = station tags (주변도구: bench/furnace…); actor must be in range of an object carrying each tag; substitutable; **NOT consumed**. Optional.
- `duration` — craft time (ticks), **per-recipe** (replaces Cm6 action-flat).
- `basis_stat` — StatID driving the outcome roll.
- `outputs[]` = `{ item, base_qty }`. Actual success/qty **and the produced instance's durability/quality** are **ROLLED from `basis_stat`** (skilled → sturdier/more). A produced tool's starting durability = roll·`wear_max`. Perishable output → fresh decay lot `{kind,qty,decayAge=0}` (Dm5).
- **Determinism (D12):** a slot takes the **FIRST satisfiable alternative in authored order**; `wear` picks the **most-worn** matching tool (ties by ID); `consume` takes lots **most-decayed-first** (ties by ID).
- **Craftable gate** (planner/world reads the BOUND recipe): every slot has a satisfiable alternative **AND** every `ambient` station in range. **No partial run.**
- `objects.yaml` `tool` block = `{ wear_max }` (durability ceiling); **`wear_per_use` REMOVED** (amount is per-recipe). Bootstrap: `craft_basic_tool` = consume-only slots → bare-handed.
- **2단 대체:** `any` = OR over explicit alternatives (서로 다른 amount/mode 가능); a single `tagQuery` = AND over its tags, matched by **any item carrying that tag set** (D4 — new item auto-qualifies).

## 1. Open questions

### [taste — 세계 느낌] (전부 RESOLVED — 사람 확정)
- **Q1 부패 모델:** `RESOLVED: 이산 상태전이`(fresh→stale→rotten→gone). 가치·효과가 단계로 변함, 저작·결정성 쉬움.
- **Q2 부패 구동:** `RESOLVED: 환경결합`(기온·습도가 가속). climate hook 재사용, 저온·건조 저장이 창발(D2); 기본 rate는 데이터.
- **Q3 부패 산물:** `RESOLVED: 변환`(food→거름/썩은것). D9 locality, regen→object 결정과 동형.
- **Q4 원재료 추출:** `RESOLVED: terrain 변형`(depth/material → navmap `SetTerrain` 재사용). 영구 리루트·고갈 창발.
- **Q5 자원 고갈:** `RESOLVED: 혼합` — 광물=유한·희소(고갈 → economy 분쟁 시드 D2), 식생=전파 재생(flora 재사용).

### [eng — locked]
- 부패 적용범위: 바닥·인벤토리·저장 동일 틱, **저장 구조물이 rate 곱 감속**(cold-storage 창발).
- 배치: 부패=`engine/env/decay`(L1, 순수 Step) / 추출=actions+world / 레시피=`content/recipes.yaml`+스키마 / 재료=objects.yaml tag.
- 부패 틱 cadence(N틱)·상태전이 임계의 데이터 모양.

### [eng — RESOLVED] mechanism choices the P_m1/P_m2 SPECs FORCED — all `RESOLVED: (a)` (사람 확정)
> Surfaced while writing `content/schema/*` + `backend/engine/env/decay/SPEC.md`. Each is now `RESOLVED: (a)`:
> **Dm1** continuous `decayAge` accumulator (thresholds in effective-time units). **Dm2** §6 `accel`
> multiplicative: `effRate = baseRate · accel(temp,moist) · storageMult`. **Dm3** decay owns the §6
> `Program` (imports `engine/kernel/expr`, flora parity). **Dm4** owner-agnostic flat decayable-item set
> (floor + inventory + storage; dead-agent items keep decaying). **Dm5** decay unit = *lot*
> `{kind, qty, decayAge}`, no auto-merge (inventory `{tag:int}` view = sum over lots). Detail + rejected
> options retained below for the record.

- **Dm1 — Decay-time accumulation model.** `RESOLVED: (a)` effective-decay-time accumulator — each item
  carries a continuous `decayAge` advancing by `elapsedTicks · effectiveRate`; a state transition fires
  when `decayAge` crosses the next state's threshold (thresholds in *effective-time units*,
  env-independent). (Rejected: raw-elapsed-ticks + per-step threshold scaling; per-state countdown.)
- **Dm2 — Env-acceleration combination form.** `RESOLVED: (a)` §6 `accel` Formula over `temperature`/
  `moisture` = a multiplier; `effectiveRate = baseRate · accel · storageRateMult`. (Rejected: additive
  terms; fixed engine shape with rates-only data.)
- **Dm3 — Where the `accel` §6 Formula is evaluated.** `RESOLVED: (a)` decay owns the compiled `accel`
  `Program` in its `Rules`, imports `engine/kernel/expr`, builds the `Context` from the env input (flora
  parity). (Rejected: world evaluates + passes a scalar.)
- **Dm4 — Decay of owner-less / dying-agent items.** `RESOLVED: (a)` owner-agnostic — `Step` takes a flat
  decayable-item set keyed by a stable instance id (floor + every inventory + storage); dead-agent items
  keep decaying with no special case. (Rejected: inventory+storage only, floor inert.)
- **Dm5 — Stacked / quantity item decay granularity.** `RESOLVED: (a)` per-lot `{kind, qty, decayAge}`,
  NO auto-merge in P_m2 — a forage/craft output creates a NEW lot `decayAge=0`; the `{Tag:int}` inventory
  view is the SUM over a kind's live lots. (Rejected: one lot per (owner,kind); per-unit.)

> **GATE STATUS:** Dm1–Dm5 `RESOLVED: (a)`. P_m1/P_m2 finalized (see §2). Full rejected-option detail was
> condensed above on the FINAL re-baseline; the original enumerations are in VCS history.

### [P_m3 — RESOLVED] Craft mechanisms — Cm1–Cm6 `RESOLVED: (rec)`; Cm7 `RESOLVED`, then SUPERSEDED by §0 FINAL
> Cm1 consume most-decayed lot first (ties by ObjectID). Cm2 tool durability: deterministic decrement,
> break = object-mortality removal at 0. Cm3 §6 reads held tool via the existing `Attr` method
> (`tool:<family>.quality`) — `expr` L0 UNCHANGED. Cm4 yield reuse. Cm5 station-tag match. Cm6 duration.
> **Cm7** (tool-gate bootstrap) `RESOLVED` as a unified-input model, then **SUPERSEDED by §0 "Recipe
> model — FINAL"**: the flat `consume: material|tool` input, the recipe `station` field, the Craft
> `target_tags`, the action-flat duration, and the per-item `wear_per_use` are ALL replaced by the FINAL
> slot/alternative (`wear`|`consume`) + `ambient` + per-recipe `duration` + `basis_stat` + `tool:{wear_max}`
> model. The bootstrap is closed by a consume-only `craft_basic_tool` (bare-handed); a `mode: wear`
> cutting-tool alternative on `craft_pickaxe` makes the toolmaking chain emerge (D2). **No remaining
> OPEN for P_m3.** The Cm1–Cm7 detail below is RETAINED AS HISTORY; the binding shape is §0 FINAL.

- **Cm1 — most-decayed lot first, ties by ObjectID** (deterministic + emergent thrift). `RESOLVED`.
  (Rejected: lowest-ObjectID-first; freshest-first.) Carried into FINAL `consume` mode.
- **Cm2 — tool durability: deterministic decrement, break = object-mortality at the cap.** `RESOLVED`.
  (Rejected: §7-style risky break roll; wear-lowers-quality-but-never-breaks.) FINAL refines it: the
  block is `{wear_max}` only (the wear AMOUNT is per-recipe `wear` alternative, not per-item); a tool's
  CURRENT durability counts DOWN from `basis_stat roll · wear_max` and breaks at 0.
- **Cm3 — §6 reads held tool via the existing `Attr` operand `tool:<family>.quality`.** `RESOLVED`;
  `expr` L0 UNCHANGED. (Rejected: boolean `has(tool)` via `Pred`; a new `Context.HeldTool` method.)
  FINAL: quality = current durability / `wear_max`; used by the Mine yield (Craft uses `basis_stat`).
- **Cm4 — output qty/quality via the roll; perishable output → fresh decay lot (Dm5).** `RESOLVED`.
  FINAL: the roll is `basis_stat` (was §6(Dexterity, tool-quality)); produced durability = roll·`wear_max`.
- **Cm5 — station-tag-matched + no partial run.** `RESOLVED`. FINAL: the station is the recipe `ambient`
  (in-range via spatial hash); the Craft action has NO `target_tags`/station field.
- **Cm6 — per-recipe duration.** `RESOLVED`. FINAL: `duration` is a recipe field; the Craft action has
  no duration. (The earlier action-flat option is superseded.)
- **Cm7 — tool-gate granularity / first-tool bootstrap.** `RESOLVED` → §0 FINAL: a tool is a recipe input
  alternative (`mode: wear`/`consume`), NOT an action tag; a tool-free recipe is bare-handed (bootstrap).
  (Rejected: action-tag-only with a world-gen seed tool; the intermediate flat-`consume:tool` model.)

### [P_m4 — RESOLVED] extraction mechanisms — all `RESOLVED: (rec)` (사람 확정)
> Xm1–Xm6 resolved to the recommended option. **Xm1** mineral = finite-qty `ore_node` object_kind
> (object-local `remaining`, D9; berry_bush parity). **Xm2** one `SetTerrain` on node exhaustion
> (remaining→0), apply phase — minimal navmap churn. **Xm3** mineral = remove node + SetTerrain at 0;
> water = infinite (`depletes:false`); flora = existing regen — Q5 "혼합" via three existing mechanisms,
> no new water-rate machinery (rate-limited water = later economy seam). **Xm4** new `Mine` action
> parallel to flora's `Fell` (shared yield mechanism). **Xm5** require `tool:digging` (pickaxe) +
> flora-yield reuse → emergent toolmaking dependency (D2); the held pickaxe WEARS per use via the same
> durability path (the per-use amount is a Mine world/balance rate, since Mine has no recipe — FINAL
> removed per-item `wear_per_use`). **Xm6** gate the SetTerrain step on `content/terrain.yaml` + a
> depleted/`bare_rock` type (natural M3 dep). Detail retained as history; the binding shape is §0 FINAL
> for any tool/durability aspect.

- **Xm1 — finite-qty `ore_node` object_kind; SetTerrain on depletion.** `RESOLVED: (a)`. (Rejected:
  depleting terrain attribute chain; hybrid.) → objects.yaml `source:{initial, depleted_terrain}`.
- **Xm2 — one `SetTerrain` on node exhaustion (remaining→0), apply phase.** `RESOLVED: (a)`. (Rejected:
  threshold ladder; every-extraction.)
- **Xm3 — mineral = remove node + SetTerrain at 0; water = infinite (`depletes:false`); flora = regen.**
  `RESOLVED`. (Rejected: rate-limited water — deferred to a later economy-contention seam.)
- **Xm4 — a new `Mine` action parallel to flora's `Fell`** (distinct tool-gated/high-effort tags, shared
  yield mechanism). `RESOLVED: (a)`. (Rejected: overload Forage `depletes:true`; one generic `Harvest`.)
- **Xm5 — require `tool:digging` (pickaxe) + flora-yield reuse (`§6(Dexterity, tool quality)`).**
  `RESOLVED: (a)`. (Rejected: bare-hands mining.) Depends on the tool/durability mechanism (Cm2/Cm3 →
  §0 FINAL). The held pickaxe wears via the durability path; the per-use amount is a Mine world rate.
- **Xm6 — `content/terrain.yaml` + a `bare_rock`/depleted type prerequisite for the SetTerrain reroute.**
  `RESOLVED: (a)` — gate P_m4's SetTerrain step on the terrain catalog (natural M3 dep). Non-blocking on
  the Xm mechanisms; a sequencing prerequisite.

## 2. Phases
> map-plan.md 양식: 각 phase 독립 shippable + 테스트 + 결정성 골든. **공통 선행: `engine/kernel/expr` 구현**(§6 — Craft basis_stat·도구 durability·tag질의 가속식).

- **P_m1 — 재료·레시피·부패상태 데이터 + 스키마** *(content, 엔진 無 — 첫 leaf)* — **READY**
  objects.yaml: 재료 tag + 부패 state 필드(`states`/전이 임계/transform 산물) + `tool:{wear_max}` 내구재 + `source:{initial,depleted_terrain}`. `content/recipes.yaml`(FINAL): `inputs:[{any:[{tagQuery, amount, mode:wear|consume}]}]`, `ambient:[tags]`, `duration`, `basis_stat`, `outputs:[{item, base_qty}]`. `content/schema/*` + `data-contracts` 확장. 검증 = 스키마 + 결정성 로드 테스트. ✓ Dm1/Dm2/Dm5 + FINAL recipe model RESOLVED → buildable.
- **P_m2 — `engine/env/decay` (부패 Step)** *(L1 leaf)* — **READY**
  순수 `Step(prev lots, env{temp,moisture}, rules, rng) → next + StepDeltas`. 이산 상태전이, 환경결합 가속, transform 산물 emit, 저장 rate 곱, multi-rate cadence. 골든 스냅샷. `world`가 유일 mutator로 wire. (env는 climate 출력 모양을 **입력으로** 받음 — climate 구현에 비의존, flora와 동형.) ✓ Dm1–Dm5 RESOLVED → buildable (expr 구현 선행).
- **P_m3 — `Craft` 행위** *(actions, P_m1 + expr 의존)* — **SPEC READY** (`backend/engine/mind/actions/SPEC.md`)
  레시피 매개 단일 원자행위(D3, `recipe_mediated`; action에 tool tag/target/duration 無 — §0 FINAL). World apply: slot마다 authored 순서 first-satisfiable alternative(D12), `consume`=most-decayed lot 제거(ties ObjectID)·`wear`=most-worn tool durability ↓ break@0, `ambient` in-range gate, `basis_stat` 롤(success/qty + 산출 durability=roll·wear_max), 부패 산출→fresh lot(Dm5), no partial run. ✓ Cm1–Cm7 RESOLVED + §0 FINAL → SPEC written, **NO remaining OPEN** (expr 구현 선행).
- **P_m4 — 추출(채굴)** *(actions + world + navmap)* — **SPEC READY** (`backend/engine/mind/actions/SPEC.md` + `ore_node`/`tool:digging`)
  terrain 변형 추출 행위 `Mine`(navmap `SetTerrain` 재사용, Xm4 Fell-parallel), 광물 = 유한 노드 `ore_node`(`source.remaining`, Xm1), depletion(remaining→0) → 노드 제거 + 1회 SetTerrain → `bare_rock`(Xm2/Xm3), `tool:digging`(pickaxe) 액션-tag gate + 보유 도구 durability wear(Mine world rate) + §6 yield reuse(Xm5). water=infinite, flora=기존 regen. ✓ Xm1–Xm6 RESOLVED → SPEC written. ⚠ **선행: world+navmap(M3) + `content/terrain.yaml`의 `bare_rock` 타입(Xm6)**; SetTerrain reroute 골든은 terrain.yaml 선행 필요.

의존: **P_m1 → (P_m2, P_m3)**. P_m3·P_m2(가속식)·Craft 산출은 **expr 구현 선행**. P_m4는 world+navmap(M3) + `terrain.yaml`(Xm6) 위.
