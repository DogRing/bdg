# Flora — 식물/초목 객체 — Subsystem Plan (DRAFT)

Concept & rationale: `docs/design.md §5` (초목=flora 객체, terrain 아님), `§6` (Formula DSL),
`§7` (lifecycle / object-mortality 계열). 이 문서는 **결정 표면(Tier-2 plan)**이고 **module SPEC은 아직 없다**.
형제 문서: `docs/climate.md`(동적 지형·다중주기·결정성 패턴 — climate가 `moisture`/`temperature`를 굴린다),
`docs/lifecycle.md`(object-mortality·구조물 파괴와 동형), `docs/map-plan.md`(navmap·objects·serialization).

관련(예상) 모듈: terrain 속성(`engine/navmap` `TerrainAt`) + climate 상태(`moisture`/`temperature`) 읽기,
적합도/그늘/자원 = §6 수식(`engine/expr` 제안 평가기), 식물 객체 = `engine/world` objects,
그늘→LoS 감소 = `engine/perception`, 베리 supply `Effect` = `content/objects.yaml`,
초기 분포 = world-gen / 시나리오 픽스처, 신규 `content/flora.yaml`(종·적합도·그늘·자원 수식).

> **게이트 STEP 1 (이 문서):** §0 = 사람 확정(재논쟁 금지), §1 = **모든 항목 `[OPEN]`로 열거만**(여기서 결정 금지),
> §2 = placeholder. §1이 전부 `RESOLVED`가 되기 전엔 어떤 module SPEC도 쓰지 않는다(`CLAUDE.md` Open-Question gate, D2/D3).

## 0. Decisions locked (design.md §5/§7 에서 확정 — 여기서 다시 결정하지 않음)
- **식물(나무·풀·덤불) = 오브젝트**(terrain 아님). 지형 위 **연속좌표 `Pos`**(D11). terrain 속성 벡터에 들어가지 않는다.
- **'숲'·'어두운 숲'은 창발(D2/D3):** '숲' = 나무 객체 군집. '어두운 숲' = 개별 식물 그늘의 **중첩**으로 창발 — 별도 terrain 타입도, terrain `light` 속성도 없다.
- **그늘 = perception 효과:** 식물 객체가 주변에 그늘을 드리워 **시야(LoS) 감소**(perception). terrain에 `light`/`shade` 속성을 추가하지 않는다.
- **자원 = D9 supply `Effect`만:** 베리 = 식량 supply(소비 item의 `Effect`), 나무 = 목재(채집 yield). **객체엔 미래 수치 필드 금지(D9)** — "곧 익을 양/남은 수명" 같은 forward 필드 없음. 객체는 충족 Effect(공급)만 보유.
- **lifecycle = 생성·성장·고사:** **적합도(suitability) = terrain 속성 + climate(`moisture`/`temperature`)의 §6 수식**. 악조건 → 고사 = **객체 제거**(object-mortality 계열 — `lifecycle.md`의 사망/`economy.md`의 구조물 파괴와 **동형의 기계**).
- **모두 content 데이터 + §6 수식(D10/D4):** 종류·적합도·그늘·자원 재생은 `content/flora.yaml` + 수식. **하드코딩 생태계 없음(D2/D3)** — 천이·군집·먹이사슬은 base 메커닉에서 창발해야 한다.

## 1. Decisions — **ALL RESOLVED** (추천대로 채택)
> 사람이 25개 전부 각 줄의 `rec`로 확정(`[RESOLVED]` = 그 줄의 rec). 명시 확정/정제:
> - **1f** → (a) 야생 flora = 무주물 + **심은 것만 `owner`**(economy 미출하 동안은 전부 무주로 동작).
> - **1e 수율표** → `objects.yaml` object_kind에 `yields: [{item, chance, qty:[min,max]}]`, **seeded RNG 롤**(D12), 액션은 generic(대상 표를 읽음). 수율·`regen`은 balance에서 빼서 **객체에**(D9 locality); `balance.yaml`은 전역 상수만.
> - **수율 스케일** → `chance = §6(Dexterity)`. 신규 capability 스탯 **`Dexterity`**(손재주). 스탯은 use로 단련(D7 명확화 — 행동별 스킬 없음).
> - 신규 명칭(채택 → glossary/stats 등재 필요): `suitability`·`growth`·`shade`·`Fell`·`Plant`·`Dexterity`.

### 1a. 번식 / 확산 (propagation)
- [RESOLVED→rec] **번식 모델** — 모식물이 어떻게 새 식물 객체를 낳나? options: (a) 씨앗 분산 = 부모 `Pos` 주변 seeded-RNG 반경에 확률적 spawn(밀도·적합도 가중) (b) 단순 적합도 기반 spontaneous spawn(부모 무관, 적합 셀에 확률 등장) (c) 둘 다(개척=spontaneous, 군집 확장=분산); rec: (a) — 군집/숲의 공간적 창발(D2)이 부모 근접 분산에서 자연히 나옴, climate 적합도와 곱해 천이도 창발.
- [RESOLVED→rec] **확산 갱신 주기·예산** — 번식 평가를 언제 도나? options: (a) 매 틱 전 식물 스캔(비쌈) (b) climate형 느린 bulk(`tick % N`, 고정순서 1패스) (c) 성숙 식물의 이벤트 트리거(성장 단계 도달 시 1회); rec: (b) — climate(N=60) cadence와 정합, 다중주기·D12 패턴 재사용, map 순회 금지.
- [RESOLVED→rec] **번식 결정성/RNG** — 확률 spawn의 시드 분기 방식? options: (a) world per-step fork(climate처럼 `fork(tick)`) (b) 식물 객체별 결정성 시퀀스(`ObjectID` 파생 시드); rec: (a) — climate.Step과 동일 패턴, 고정순서 적용(D12), 객체별 시드 관리 부담 없음.

### 1b. 성장 (growth)
- [RESOLVED→rec] **성장 모델: 이산 단계 vs 연속** — 식물 성숙도 표현? options: (a) 이산 성장 단계(seedling→sapling→mature, 단계별 그늘/자원/footprint) (b) 연속 `growth ∈ [0,1]` 스칼라(파생 그늘/자원은 §6 수식) (c) 연속 내부값 + 렌더/게이트용 파생 단계; rec: (c) — 연속이 적합도 적분·고사와 매끄럽고(climate `moisture`형), 단계는 그늘 반경·채집 가용성의 데이터 임계로 §6에서 파생(D4).
- [RESOLVED→rec] **성장 구동식** — `growth` 증가율을 무엇이 정하나? options: (a) 적합도 §6 수식만(`growth += rate · suitability`) (b) 적합도 + 밀도 경쟁 패널티 (c) 적합도 + climate 계절(park); rec: (a) — 최소·D9정신(rate 상수), 밀도/계절은 별도 OPEN(1c·climate park)에서 다룸.
- [RESOLVED→rec] **고사 판정식** — 언제 객체 제거? options: (a) `suitability < θ`가 연속 M-bulk-주기 지속 시 고사(이력 카운터) (b) 즉시 임계(`suitability < θ` → 제거) (c) `growth`가 음의 적분으로 0 도달 시(연속 쇠퇴); rec: (a) — 일시적 악천후로 숲이 깜빡 사라지지 않게 이력 필요, lifecycle 사망 "회항/지속" 판정과 동형.

### 1c. 경쟁 (종내·종간 density effect)
- [RESOLVED→rec] **밀도 효과(경쟁)를 둘 것인가** — 군집 과밀 시 성장·번식 억제? options: (a) 둔다: 반경 내 식물 수가 적합도/성장률을 §6로 감산(자기조절 군집) (b) 두지 않는다(P1): 적합도+공간만으로 군집, 경쟁은 frontier (c) 자원(빛/물) 공유로 간접 경쟁(그늘이 이웃 적합도↓로 되먹임); rec: (b) for P1 — 최소로 출하, 경쟁을 빼도 적합도+분산으로 군집은 창발; (c)를 frontier seam으로 명기(그늘→이웃 적합도 피드백 = 자연스런 종간 경쟁).
- [RESOLVED→rec] **종간 천이(succession)** — 풀→덤불→나무 같은 천이를 명시할까? options: (a) 명시 안 함 — 종별 적합도 차이(예: 나무가 그늘 만들고 그늘이 음지종 적합도↑)로 **창발**(D2) (b) 천이 규칙표(`from-species → to-species`)를 content에; rec: (a) — D2/D3(하드코딩 생태계 금지), 천이는 그늘×적합도 상호작용의 부산물이어야 함.

### 1d. 그늘 메커닉 (shade → LoS)
- [RESOLVED→rec] **그늘 반경/세기 산출** — 식물별 그늘을 무엇으로? options: (a) 종+성장단계별 상수 반경 (b) `growth`·footprint에서 §6 수식 파생(`radius = f(growth, species)`) (c) 캐노피 footprint(navmap 셀 마킹); rec: (b) — D4/D10(데이터 수식), 연속 성장과 일관, 셀 마킹은 D11 타일화 위험.
- [RESOLVED→rec] **그늘 합성(중첩)** — '어두운 숲'을 어떻게 누적? options: (a) 가산 후 클램프(겹칠수록 어두움) (b) max(가장 진한 그늘) (c) 곱셈 감쇠(빛 투과 ∏(1−shade)); rec: (c) — 물리적 빛 투과에 가깝고 단조·결정적, 중첩으로 '어두운 숲' 창발(locked §0).
- [RESOLVED→rec] **그늘이 LoS에 들어가는 지점·방식** — perception이 그늘을 어떻게 읽나? options: (a) `perception`이 시선 경로 위 식물 그늘을 적분해 시야 반경/확률 감소(occluder 질의) (b) world가 셀별 누적 그늘 필드를 만들고 perception이 샘플 (c) 광선상 occluder 카운트로 단순 감산; rec: (a) — D11(연속·객체 질의, 타일 필드 회피), perception은 이미 spatial 객체 질의 구조. **[NEW NAME?]** "occlusion query" 인터페이스 명칭 결정 필요.
- [RESOLVED→rec] **낮/밤 상호작용** — 그늘이 시간에 따라 변하나? options: (a) P1 시간 무관(그늘 = LoS 상수 감산) (b) 밤엔 기저 시야가 이미 낮으니 그늘 영향 비선형(`worldtime` 결합) (c) 그늘 = 낮에만 의미(밤엔 무시); rec: (a) for P1 — 최소; 밤 시야 자체는 perception/worldtime 소관, 그늘×낮밤 결합은 frontier(climate Temperature park와 동류).

### 1e. 자원 재생 (regeneration)
- [RESOLVED→rec] **베리 재생 주기/조건** — 채집 후 재생 규칙? options: (a) 단순 틱 타이머(현행 `balance.regen.berry_bush` 재사용, 적합도 무관) (b) 적합도/`growth` 가중 재생률(악조건이면 느리게) (c) 성장 단계 gating(mature만 베리 보유); rec: (b) — 적합도가 자원에도 일관되게 작용(D4), 가뭄이면 흉작이 창발; 단 **D9 준수**(재생 = rate 상수, 미래 수량 필드 금지). 현행 `berry_bush` regen을 flora 적합도로 일반화.
- [RESOLVED→rec] **목재 채집과 고사의 관계** — 나무에서 목재를 얼마나, 베면 죽나? options: (a) 채집 = 부분 yield, 나무 생존(재생) (b) 벌목 = 객체 제거(목재 대량 yield + 고사 트리거 = lifecycle object-mortality 재사용) (c) 둘 다 행동(Forage=가지치기 생존 / Fell=벌목 제거); rec: (c) — 벌목을 object-mortality(§7/구조물 파괴)와 동형으로 재사용, 채집은 비파괴 yield. **[NEW NAME?]** `Fell` 행동명 확정 필요(actions.yaml).

### 1f. 소유 가능성 (economy 연결)
- [RESOLVED→rec] **식물 객체에 `owner`를 붙일 수 있나** — economy `owner` primitive 적용 범위? options: (a) 야생 flora = 무주물(공유지), 심은 식물만 owner(`Plant` 행동) (b) flora 전부 무주물(P1, economy 미연결) (c) flora 전부 owner 가능(터·과수원 사유화); rec: (a) — 무주 야생 + 의도적 식재 사유화로 토지/과수원 드라마 훅(economy.md `owner`/상속과 결합), 단 **economy 미출하 동안은 (b)로 동작**(seam만 예약). `Plant` 행동·owner 연결은 economy phase에서.

### 1g. 갱신 주기 (cadence × D12)
- [RESOLVED→rec] **flora 갱신 주기 = 매 틱 vs N틱 bulk** — 성장/번식/고사/재생을 언제? options: (a) 전부 매 틱(정확·비쌈) (b) climate형 다중주기: 그늘 질의=lazy(perception 시), 성장/적합도 적분/고사=느린 bulk(`tick % N`), 자원 재생=타이머 (c) 단일 bulk 주기로 통합; rec: (b) — climate(`tick % 60`)와 정합·다중주기 패턴 재사용, 부하 분산, **고정순서 1패스·wall-clock 금지·map 순회 금지(D12)**. flora bulk 주기 N = climate N과 같게 둘지/오프셋 줄지 하위 결정 포함.
- [RESOLVED→rec] **bulk 결정성 적용 순서** — 식물 객체를 어떤 순서로 갱신? options: (a) `ObjectID` 정렬 1패스(D12 표준) (b) 공간 파티션 병렬 + 고정순서 merge; rec: (a) — climate/world apply와 동일, 단순·결정적; 병렬은 프로파일 후.

### 1h. 모듈 경계 + DAG 위치
- [RESOLVED→rec] **신규 `engine/flora` vs `world`/objects 흡수** — 어디에 살까? options: (a) 신규 `engine/flora`: 순수 transform `(식물 상태 + terrain/climate 입력 + Rules) → (성장/번식/고사 델타)`, climate와 동형(world가 적용) (b) `world` objects 로직에 흡수(별 모듈 없음) (c) 적합도/성장=`flora`, 그늘 occlusion=`perception` 확장; rec: (a)+(c) — climate 패턴 직접 복제(`core`(+`expr`)+`rng`만 의존, 순수·테스트 용이, D5 관심사 분리); 그늘은 perception 소관이라 flora는 그늘 *파라미터*만 노출.
- [RESOLVED→rec] **DAG 위치** — `engine/flora`의 leaf level·의존? options: (a) L1 leaf = `core`(+`expr`)+`rng`만(climate와 같은 stage 2; 입력은 navmap/climate 상태를 *값으로* 받음) (b) navmap/climate import(L2+); rec: (a) — climate "navmap/worldtime import 금지" 불변식과 동형, world가 입력(terrain/climate 상태)을 주입하고 출력 델타를 적용 → world가 유일 객체 변이자. **의존 escalation:** 적합도/그늘 수식 = §6 → `engine/expr`(design §6 제안, 아직 미존재) 필요. climate의 "Formula 평가기 home" escalation과 **공유**(`architecture.md §4` 참조).

### 1i. 직렬화 (snapshot / delta)
- [RESOLVED→rec] **식물 객체 직렬화 형태** — flora를 어떻게 스냅샷/스트림? options: (a) 일반 `objects[]`에 합류(현행 berry_bush처럼) + `growth`/그늘 상태 필드 (b) climate형 periodic full + sparse delta(생성/성장단계전이/고사 이벤트) (c) 정적 + 이벤트(spawn/grow/die); rec: (b)/(c) 혼합 — 식물은 동적이라 `data-contracts.md §6`(periodic full + sparse delta, wear/terrain와 정합); spawn/die = `objects[]` add/remove 이벤트, growth = 주기 full. data-contracts §1 objects 스키마 확장 필요(growth 필드).
- [RESOLVED→rec] **그늘은 직렬화하나** — LoS 영향을 스트림에 실을지? options: (a) 직렬화 안 함 — 그늘은 식물 상태에서 파생(perception이 재계산) (b) 렌더용 그늘 오버레이를 별도 스트림 (c) 파생만, 프런트가 식물 위치로 재구성; rec: (a)/(c) — D9정신(파생값 저장 안 함), 그늘은 식물 `Pos`/`growth`에서 결정적 재계산. 렌더 캐노피는 frontend가 합성(map-plan M5 렌더 확장).

### 1j. world-gen 초기 분포
- [RESOLVED→rec] **초기 식물 분포 생성** — 라이브/시나리오 초기 숲을 어떻게 깔까? options: (a) 적합도장 위 seeded-RNG 분포(climate/terrain 적합도가 높은 곳에 군집) (b) 절차적 군집(seed 포인트 + 분산 시뮬 N스텝 pre-warm) (c) 시나리오는 픽스처, 라이브는 절차적(둘 다 — map-plan layout source와 동형); rec: (c) — 골든/시나리오 결정성(픽스처) + 라이브 다양성(절차), `map.yaml`/world-gen layout 패턴 재사용. 분포는 **content 아님**(placement = world-gen, objects.yaml 정신).

## 2. Phases — (placeholder; §1이 전부 RESOLVED 된 후 작성)
> `climate.md §2` / `map-plan.md M1~M5` 양식: 각 phase 독립 shippable + 테스트 + 결정성 골든.
> ✅ **§1 전부 RESOLVED (추천대로).** phase·module SPEC 작성 가능.
> 예상 phase 골격(미확정, 결정 후 채움): (P_f1) `engine/flora` 순수 transform + flora-off 출하(outcome-중립),
> (P_f2) `world` 와이어링(cadence + 객체 add/remove 브리지, 여전히 outcome-중립),
> (P_f3) 그늘→perception(LoS) 통합 + 골든 의도적 재기준, (P_f4) 활성 적합도/번식/자원(`content/flora.yaml`),
> (P_f5) 직렬화/스트림 + 렌더(`data-contracts.md §6` 합류), (park) economy 소유·천이·밀도경쟁·낮밤 그늘.

## 3. Notes / escalations
- **§6 평가기 의존:** 적합도·그늘·자원 재생·고사 판정이 모두 §6 수식이라, flora는 climate와 **같은 escalation**을 공유한다 — 공유 §6 boolean/arith 평가기의 home(`engine/expr` 제안, 현재 gates 안의 boolean 부분만 존재). `docs/climate.md`/`architecture.md §4`의 평가기-home 결정이 flora SPEC의 전제다.
- **현행 코드와의 정합:** `content/objects.yaml`의 `berry_bush`(이미 `depletes`+`balance.regen.berry_bush` 재생)와 `prey`(mobile)는 flora가 일반화/형식화할 대상. flora 도입 시 `berry_bush`를 flora 종으로 흡수할지(현행 object_kind 유지 + flora 상태 부여) = §1e/§1i 결정에 종속.
- **신규 명칭 후보(채택 시 glossary 등재):** `suitability`(적합도), `growth`(성숙도 연속값), `shade`/occlusion query(그늘→LoS), `Fell`(벌목 행동), `Plant`(식재 행동). 모두 §1에서 결정 전까지 미확정.
- **불변 가드:** flora는 D2/D3(하드코딩 생태계 금지) — 숲·천이·먹이사슬은 base 메커닉 창발; D9(미래 수치 필드 금지) — 객체는 supply Effect만; D11(연속좌표, 그늘을 타일 필드로 굳히지 말 것); D12(틱 파생 주기, 고정순서, map 순회 금지).
