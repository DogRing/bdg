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

## 1. Open questions — 전부 RESOLVED (사람 확정)

### [taste — 세계 느낌]
- **Q1 부패 모델:** `RESOLVED: 이산 상태전이`(fresh→stale→rotten→gone). 가치·효과가 단계로 변함, 저작·결정성 쉬움.
- **Q2 부패 구동:** `RESOLVED: 환경결합`(기온·습도가 가속). climate hook 재사용, 저온·건조 저장이 창발(D2); 기본 rate는 데이터.
- **Q3 부패 산물:** `RESOLVED: 변환`(food→거름/썩은것). D9 locality, regen→object 결정과 동형.
- **Q4 원재료 추출:** `RESOLVED: terrain 변형`(depth/material → navmap `SetTerrain` 재사용). 영구 리루트·고갈 창발.
- **Q5 자원 고갈:** `RESOLVED: 혼합` — 광물=유한·희소(고갈 → economy 분쟁 시드 D2), 식생=전파 재생(flora 재사용).

### [eng — locked]
- 부패 적용범위: 바닥·인벤토리·저장 동일 틱, **저장 구조물이 rate 곱 감속**(cold-storage 창발).
- 배치: 부패=`engine/decay`(L1, 순수 Step) / 추출=actions+world / 레시피=`content/recipes.yaml`+스키마 / 재료=objects.yaml tag.
- 부패 틱 cadence(N틱)·상태전이 임계의 데이터 모양.

## 2. Phases
> map-plan.md 양식: 각 phase 독립 shippable + 테스트 + 결정성 골든. **공통 선행: `engine/expr` 구현**(§6 — Craft 품질·도구배수·tag질의 가속식).

- **P_m1 — 재료·레시피·부패상태 데이터 + 스키마** *(content, 엔진 無 — 첫 leaf)*
  objects.yaml: 재료 tag + 부패 state 필드(`states`/전이 임계/transform 산물/저장 rate-mult). `content/recipes.yaml`(신규): `inputs:[{tagQuery, qty}]`, `outputs:[{item, qty}]`, `tool?`(tag), `station?`. `content/schema/*` + `data-contracts` 확장. 검증 = 스키마 + 결정성 로드 테스트.
- **P_m2 — `engine/decay` (부패 Step)** *(L1 leaf)*
  순수 `Step(prev items, env{temp,moisture}, rules, rng) → next + StepDeltas`. 이산 상태전이, 환경결합 가속, transform 산물 emit, 저장 rate 곱, multi-rate cadence. 골든 스냅샷. `world`가 유일 mutator로 wire. (env는 climate 출력 모양을 **입력으로** 받음 — climate 구현에 비의존, flora와 동형.)
- **P_m3 — `Craft` 행위** *(actions, P_m1 + expr 의존)*
  레시피 매개 단일 원자행위(D3). tag-query input 매칭·소비/산출, 도구 = 소비 안 되는 `wear` 내구재 + **§6 보유도구 tag 배수**, 품질 = **§6(Dexterity)→chance/qty**(yield 모델 재사용). 테스트.
- **P_m4 — 추출(채굴)** *(actions + world + navmap)*
  terrain 변형 추출 행위(navmap `SetTerrain` 재사용), 광물 = 유한 노드(고갈), 식생 = 전파 재생(flora). depletion → navmap 영구 리루트 골든.

의존: **P_m1 → (P_m2, P_m3)**. P_m3·P_m2(가속식)·Craft 품질은 **expr 구현 선행**. P_m4는 world+navmap(M3) 위.
