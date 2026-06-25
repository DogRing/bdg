# Materials & Crafting — 물질 사슬 (재료·대체·레시피·부패) — Subsystem Plan (DRAFT)

Concept & rationale: `docs/design.md §9`(경제) + §5(자원) + §6(수식). 이 문서는 **결정 표면**이고 SPEC은 아직 없다.
관련 모듈: `content/objects.yaml`(item_kind + 재료 tag), `content/recipes.yaml`(신규, 변환), `content/schema/*`,
`engine/actions`(Craft·채굴), `engine/decay`(신규 L1, 부패 Step), `engine/world`(유일 mutator), `engine/expr`(§6 품질·도구배수·tag질의).

**세계 완성 로드맵(사람 확정):** (지금) **물질사슬 → fauna → 불&빛**. 몸/needs는 **Agent 단계로 이연**. 모방(지식전수)은 추후.

## 0. Decisions locked
- 재료도 별도 타입 아님 — **item_kind + 재료 tag**(D10 최소 확장).
- **대체가능 재료 = tag 질의:** 레시피 input은 특정 item이 아니라 **tag 집합**(예: `shaft_stock`)을 qty로 요구 → "나무 or 뼈"가 같은 tag면 자동 호환. 대체가능성이 tag에서 *창발*(D4). (명시 클래스 목록 대신 tag — 새 item이 tag만 달면 자동 자격.)
- 레시피=데이터, **Craft = 레시피로 매개되는 단일 원자행위**(D3; yield-table와 동형). 품질/산출은 **§6(Dexterity 등)→chance/qty** (yield 모델 재사용, 신규 메커니즘 금지).
- 도구 = 소비 안 되는 **내구재(`wear`)**, 행위 역량 배수는 **§6 수식이 보유도구 tag 참조**.
- **부패 시간 필수.** 부패 = 순수 변환 Step(flora Step과 동형), **world가 유일 mutator**, multi-rate cadence, 결정성(D12).
- 몸/needs·fauna·불&빛은 이 plan **밖**(이연).

## 1. Open questions

### [taste — 세계 느낌] (전부 RESOLVED — 사람 확정)
- **Q1 부패 모델:** `RESOLVED: 이산 상태전이`(fresh→stale→rotten→gone). 가치·효과가 단계로 변함, 저작·결정성 쉬움.
- **Q2 부패 구동:** `RESOLVED: 환경결합`(기온·습도가 가속). climate hook 재사용, 저온·건조 저장이 창발(D2); 기본 rate는 데이터.
- **Q3 부패 산물:** `RESOLVED: 변환`(food→거름/썩은것). D9 locality, regen→object 결정과 동형.
- **Q4 원재료 추출:** `RESOLVED: terrain 변형`(depth/material → navmap `SetTerrain` 재사용). 영구 리루트·고갈 창발.
- **Q5 자원 고갈:** `RESOLVED: 혼합` — 광물=유한·희소(고갈 → economy 분쟁 시드 D2), 식생=전파 재생(flora 재사용).

### [eng — locked]
- 부패 적용범위: 바닥·인벤토리·저장 동일 틱, **저장 구조물이 rate 곱 감속**(cold-storage 창발).
- 배치: 부패=`engine/decay`(L1, 순수 Step) / 추출=actions+world / 레시피=`content/recipes.yaml`+스키마 / 재료=objects.yaml tag.
- 부패 틱 cadence(N틱)·상태전이 임계의 데이터 모양.

### [eng — RESOLVED] mechanism choices the P_m1/P_m2 SPECs FORCED — all `RESOLVED: (a)` (사람 확정)
> Surfaced while writing `content/schema/*` + `backend/engine/decay/SPEC.md`. Each is now `RESOLVED: (a)`:
> **Dm1** continuous `decayAge` accumulator (thresholds in effective-time units). **Dm2** §6 `accel`
> multiplicative: `effRate = baseRate · accel(temp,moist) · storageMult`. **Dm3** decay owns the §6
> `Program` (imports `engine/expr`, flora parity). **Dm4** owner-agnostic flat decayable-item set
> (floor + inventory + storage; dead-agent items keep decaying). **Dm5** decay unit = *lot*
> `{kind, qty, decayAge}`, no auto-merge (inventory `{tag:int}` view = sum over lots). Detail + rejected
> options retained below for the record.

- **Dm1 — Decay-time accumulation model (BLOCKS P_m1 threshold units + P_m2 `Step`).** Q1 says "discrete
  transition on *accumulated time*" but not WHAT accumulates. Options: **(a) effective-decay-time
  accumulator** — each item carries a continuous `decayAge` that advances by `Δt_eff = elapsedTicks ·
  effectiveRate` per `Step`, where `effectiveRate` folds in env-acceleration + storage mult; a state
  transition fires when `decayAge` crosses the next state's threshold (thresholds are in *effective
  time units*, env-independent; acceleration shows up as faster aging). **(b) raw-elapsed-ticks +
  per-step threshold scaling** — item carries raw `ageTicks`; each state's threshold is divided by the
  current env/storage multiplier at compare time (thresholds are in *wall/tick units*, but the compare
  is env-dependent and non-monotone if env varies). **(c) per-state countdown** — item carries
  `ticksLeftInState`, decremented by `elapsedTicks · effectiveRate`; transition when ≤ 0, reset to the
  next state's threshold. Recommendation: **(a)** — a single monotone `decayAge` is resume-safe (one
  scalar to snapshot), order-free, and matches flora's "integrate a continuous axis, derive the
  discrete stage" shape (Length→Stage ≡ decayAge→State); env varying over time integrates correctly
  (each step adds that step's rate). **Decides:** schema threshold field is in effective-time units; the
  per-item decay state is one `decayAge float64` + derived `State`. **Return to human before P_m1.**

- **Dm2 — Env-acceleration combination form (BLOCKS P_m1 env hook + P_m2 `Step`).** Q2 says temperature
  & moisture *accelerate* decay over a data `baseRate`, but not HOW the two env operands + base combine.
  Options: **(a) §6 formula = the multiplier** — author one `accel:` §6 Formula over `temperature`/
  `moisture` (the climate output operands, identical names to climate/flora `Context`) → a scalar
  multiplier; `effectiveRate = baseRate · accel(temperature, moisture) · storageRateMult`. Fully
  multiplicative, data-defined, reuses `engine/expr` exactly like flora suitability (D4/D10); cold+dry
  (low temp, low moisture) → accel < 1 (slow), warm+wet → accel > 1 (fast) by authoring the formula.
  **(b) additive terms** — `effectiveRate = baseRate + k_t·temperature + k_m·moisture` (separate data
  coeffs). Loses the clean "storage multiplies" composition and needs a clamp ≥ 0. **(c) fixed
  engine multiplicative shape, rates-only data** — engine hardcodes `accel = f(temp,moist)` form,
  content supplies only coefficients (climate-style "shape is design, rates are data"). Recommendation:
  **(a)** — one §6 `accel` Formula keeps decay literal-free (D10), composes multiplicatively with the
  locked storage mult (eng-locked "저장 구조물이 rate 곱"), and reuses the shared evaluator (no second
  mechanism, plan §0). **Depends on Dm1** (the multiplier scales the `decayAge` increment under (a)).
  **Return to human before P_m1.**

- **Dm3 — Where the `accel` §6 Formula is evaluated (BLOCKS P_m2 dependency surface).** flora/climate
  carry compiled `Program`s INSIDE their own `Rules` and evaluate them (importing `engine/expr`).
  Options: **(a) decay owns it** — `decay.Rules` carries the compiled `accel` `Program` per item_kind;
  `decay` imports `engine/expr` and builds a `Context` from the env `{temperature, moisture}` input
  (mirrors flora exactly — L1, `core`+`expr`+`rng`). **(b) world/caller evaluates** — `world` computes
  the scalar `accel` per item and passes it into `Step` as a plain number (decay stays `core`+`rng`
  only, no `expr` import; the multiplier is "just a value" like `SiteInput.Moisture`). Recommendation:
  **(a)** — strict flora structural parity (the brief's mandate: "mirror flora's shape"); flora itself
  imports `expr` and evaluates its own §6, so decay doing the same is the conformant choice and keeps
  the env input a plain `{temperature, moisture}` value-shape (not a pre-baked rate). **Depends on
  Dm2** (only meaningful if (a)/§6 is chosen there). **Return to human/architect before P_m2.**

- **Dm4 — Decay of items not in a live `Body.Inventory` (owner-less / dying-agent items) (BLOCKS P_m2
  scope + world wiring).** eng-locked says decay applies to floor + inventory + storage "동일 틱". But
  when an agent dies (`design.md §7`) its inventory items become loose world objects mid-run; and
  ground items have no owner. Options: **(a) decay is owner-agnostic** — `Step` takes the full set of
  decayable item-instances (each with a position + an optional storage-structure handle), wherever they
  live; world enumerates floor items + every agent's inventory + storage contents into one input set
  each `Step`, so a dying agent's dropped items keep decaying with no special case (D2 — no bespoke
  death-decay rule). **(b) only inventory + storage decay; floor items are inert** — simpler input but
  contradicts eng-locked "바닥 동일 틱". Recommendation: **(a)** — matches the locked "floor included"
  scope and avoids a hardcoded lifecycle special-case (D2); the env `{temperature, moisture}` is sampled
  at each item's position regardless of owner (world's job, like flora's per-plant sampling). **Decides:**
  the `Step` input is a flat decayable-item set keyed by a stable instance id, NOT "per agent". **Return
  to human before P_m2** (it sets the `Step` input shape + the world enumeration contract).

- **Dm5 — Stacked / quantity item decay granularity (BLOCKS P_m1 decay-state placement + P_m2 `Step`).**
  `berries`/`raw_meat` are `stackable: true` (a `{Tag: int}` count in `Body.Inventory`, data-contracts
  §1). A decaying stack: does the whole count share one `decayAge`/`State`, or is each unit (or sub-lot)
  aged independently? Options: **(a) per-instance with a quantity** — promote a decaying stack to a
  distinct decayable *instance* carrying `{kind, qty, decayAge}`; the whole lot ages together and
  transitions together (transform emits `qty` of the product). Adding freshly-foraged berries to an
  existing lot either merges (averaging/ resetting age — needs a sub-rule) or stays a separate lot.
  **(b) age the whole inventory-count of a kind as one lot** — one `decayAge` per `(owner, kind)`;
  simplest, but mixing fresh+old berries silently ages the fresh ones. **(c) per-unit** — every unit is
  its own instance; exact but explodes instance count for a stack of 30 berries. Recommendation: **(a)
  separate lots, NO auto-merge in P_m2** — a forage/craft output creates a NEW decayable lot
  `{kind, qty, decayAge=0}`; lots never auto-merge (merge/averaging is a deliberate later rule), so the
  `{Tag:int}` inventory view is the SUM over a kind's live lots. Keeps each lot's age exact and
  deterministic, defers the merge policy. **Decides:** the decay-state unit is a *lot* (`{kind, qty,
  decayAge}`), not the bare `{Tag:int}` count — this changes the inventory data-contract (§1) and the
  `Step` input granularity. **Return to human before P_m1** (it touches data-contracts §1 inventory
  shape) **and P_m2** (Step input granularity).

> **GATE STATUS:** Dm1–Dm5 `RESOLVED: (a)` (사람 확정). P_m1 schema field units + P_m2 `decay.Step`
> Public Interface can now be finalized. SPEC-architect replaces every `[OPEN: Dm#]` hole in
> `content/schema/*` + `backend/engine/decay/SPEC.md` with the resolved (a) shape; then P_m1/P_m2 are
> buildable (after `engine/expr` impl).

## 2. Phases
> map-plan.md 양식: 각 phase 독립 shippable + 테스트 + 결정성 골든. **공통 선행: `engine/expr` 구현**(§6 — Craft 품질·도구배수·tag질의 가속식).

- **P_m1 — 재료·레시피·부패상태 데이터 + 스키마** *(content, 엔진 無 — 첫 leaf)*
  objects.yaml: 재료 tag + 부패 state 필드(`states`/전이 임계/transform 산물/저장 rate-mult). `content/recipes.yaml`(신규): `inputs:[{tagQuery, qty}]`, `outputs:[{item, qty}]`, `tool?`(tag), `station?`. `content/schema/*` + `data-contracts` 확장. 검증 = 스키마 + 결정성 로드 테스트. ✓ Dm1/Dm2/Dm5 RESOLVED → buildable.
- **P_m2 — `engine/decay` (부패 Step)** *(L1 leaf)*
  순수 `Step(prev items, env{temp,moisture}, rules, rng) → next + StepDeltas`. 이산 상태전이, 환경결합 가속, transform 산물 emit, 저장 rate 곱, multi-rate cadence. 골든 스냅샷. `world`가 유일 mutator로 wire. (env는 climate 출력 모양을 **입력으로** 받음 — climate 구현에 비의존, flora와 동형.) ✓ Dm1–Dm5 RESOLVED → buildable (expr 구현 선행).
- **P_m3 — `Craft` 행위** *(actions, P_m1 + expr 의존)*
  레시피 매개 단일 원자행위(D3). tag-query input 매칭·소비/산출, 도구 = 소비 안 되는 `wear` 내구재 + **§6 보유도구 tag 배수**, 품질 = **§6(Dexterity)→chance/qty**(yield 모델 재사용). 테스트.
- **P_m4 — 추출(채굴)** *(actions + world + navmap)*
  terrain 변형 추출 행위(navmap `SetTerrain` 재사용), 광물 = 유한 노드(고갈), 식생 = 전파 재생(flora). depletion → navmap 영구 리루트 골든.

의존: **P_m1 → (P_m2, P_m3)**. P_m3·P_m2(가속식)·Craft 품질은 **expr 구현 선행**. P_m4는 world+navmap(M3) 위.
