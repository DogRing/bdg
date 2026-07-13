# Flora — 식물/초목 객체 — Subsystem Plan (DRAFT)

Concept & rationale: `docs/core/design.md §5` (초목=flora 객체, terrain 아님), `§6` (Formula DSL),
`§7` (lifecycle / object-mortality 계열). 이 문서는 **결정 표면(Tier-2 plan)**이고 module SPEC은 작성됨
(`backend/engine/env/flora/SPEC.md`, `backend/engine/mind/perception/SPEC.md` 그늘 확장).
형제 문서: `docs/plans/climate.md`(동적 지형·다중주기·결정성 패턴 — climate가 `moisture`/`temperature`를 굴린다),
`docs/plans/lifecycle.md`(object-mortality·구조물 파괴와 동형), `docs/plans/map.md`(navmap·objects·serialization).

관련 모듈: terrain 속성(`engine/space/navmap` `TerrainAt`) + climate 상태(`moisture`/`temperature`) 읽기(=world가 값으로 주입),
적합도/그늘/자원 = §6 수식(`engine/kernel/expr` 평가기, L0 — design §6에서 확정), 식물 객체 = `engine/world` objects,
그늘→LoS 감소 = `engine/mind/perception`, 베리/목재 supply `Effect`·수율표 = `content/objects.yaml`,
초기 분포 = world-gen / 시나리오 픽스처. **신규 `content/flora.yaml`은 만들지 않음** — flora 종은 `objects.yaml`
object_kind에 `flora:` 블록으로 합류(1i 결정: flora가 objects[]에 합류).

> **게이트:** §1이 전부 `RESOLVED`(추천대로) → module SPEC 작성됨. §0 = 사람 확정(재논쟁 금지).

## 0. Decisions locked (design.md §5/§7 에서 확정 — 여기서 다시 결정하지 않음)
- **식물(나무·풀·덤불) = 오브젝트**(terrain 아님). 지형 위 **연속좌표 `Pos`**(D11). terrain 속성 벡터에 들어가지 않는다.
- **'숲'·'어두운 숲'은 창발(D2/D3):** '숲' = 나무 객체 군집. '어두운 숲' = 개별 식물 그늘의 **중첩**으로 창발 — 별도 terrain 타입도, terrain `light` 속성도 없다.
- **그늘 = perception 효과:** 식물 객체가 주변에 그늘을 드리워 **시야(LoS) 감소**(perception). terrain에 `light`/`shade` 속성을 추가하지 않는다.
- **자원 = D9 supply `Effect`만:** 베리 = 식량 supply(소비 item의 `Effect`), 나무 = 목재(채집 yield). **객체엔 미래 수치 필드 금지(D9)** — "곧 익을 양/남은 수명" 같은 forward 필드 없음. 객체는 충족 Effect(공급)만 보유.
- **lifecycle = 생성·성장·고사:** **적합도(suitability) = terrain 속성 + climate(`moisture`/`temperature`)의 §6 수식**. 악조건 → 고사 = **객체 제거**(object-mortality 계열 — `lifecycle.md`의 사망/`economy.md`의 구조물 파괴와 **동형의 기계**).
- **모두 content 데이터 + §6 수식(D10/D4):** 종류·적합도·그늘·자원 재생은 `content/objects.yaml` `flora:` + 수식. **하드코딩 생태계 없음(D2/D3)** — 천이·군집·먹이사슬은 base 메커닉에서 창발해야 한다.

## 1. Decisions — **ALL RESOLVED** (추천대로 채택)
> 사람이 25개 전부 각 줄의 `rec`로 확정(`[RESOLVED]` = 그 줄의 rec). 명시 확정/정제:
> - **1f** → (a) 야생 flora = 무주물 + **심은 것만 `owner`**(economy 미출하 동안은 전부 무주로 동작).
> - **1e 수율표** → `objects.yaml` object_kind에 `yields: [{item, chance, qty:[min,max]}]`, **seeded RNG 롤**(D12), 액션은 generic(대상 표를 읽음). 수율·`regen`은 balance에서 빼서 **객체에**(D9 locality); `balance.yaml`은 전역 상수만.
> - **수율 스케일** → `chance = §6(Dexterity)`. 신규 capability 스탯 **`Dexterity`**(손재주). 스탯은 use로 단련(D7 명확화 — 행동별 스킬 없음).
> - 신규 명칭(채택 → glossary/stats 등재 필요): `suitability`·`growth`·`shade`·`Fell`·`Plant`·`Dexterity`.
> - **[정제] 형태 = `length`+`width`** (단일 `growth` 대신): 매 게임시간 적합도로 성장; **목재 yield ∝ `length`**, **그늘 ∝ `width`**(§6). 종 = 나무 여럿 + **풀·꽃**. ⇒ flora SPEC의 단일 `Growth`축을 `length`/`width` 2축으로 **개정 필요**(new Q: 성장률·`yield(length)`·`shade(width)` 수식 형태).

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
- [RESOLVED→rec] **그늘이 LoS에 들어가는 지점·방식** — perception이 그늘을 어떻게 읽나? options: (a) `perception`이 시선 경로 위 식물 그늘을 적분해 시야 반경/확률 감소(occluder 질의) (b) world가 셀별 누적 그늘 필드를 만들고 perception이 샘플 (c) 광선상 occluder 카운트로 단순 감산; rec: (a) — D11(연속·객체 질의, 타일 필드 회피), perception은 이미 spatial 객체 질의 구조. **[명칭 채택]** occlusion query = `ShadeOccluder` + `WorldSnapshot.ShadeOccluders`(perception SPEC; glossary 등재). **NEW Q (P_f3):** 합성 site(perception vs world)·boolean 임계 vs 연속 strength → §3/SPEC Open Questions.
- [RESOLVED→rec] **낮/밤 상호작용** — 그늘이 시간에 따라 변하나? options: (a) P1 시간 무관(그늘 = LoS 상수 감산) (b) 밤엔 기저 시야가 이미 낮으니 그늘 영향 비선형(`worldtime` 결합) (c) 그늘 = 낮에만 의미(밤엔 무시); rec: (a) for P1 — 최소; 밤 시야 자체는 perception/worldtime 소관, 그늘×낮밤 결합은 frontier(climate Temperature park와 동류).

### 1e. 자원 재생 (regeneration)
- [RESOLVED→rec] **베리 재생 주기/조건** — 채집 후 재생 규칙? options: (a) 단순 틱 타이머(현행 `balance.regen.berry_bush` 재사용, 적합도 무관) (b) 적합도/`growth` 가중 재생률(악조건이면 느리게) (c) 성장 단계 gating(mature만 베리 보유); rec: (b) — 적합도가 자원에도 일관되게 작용(D4), 가뭄이면 흉작이 창발; 단 **D9 준수**(재생 = rate 상수, 미래 수량 필드 금지). 현행 `berry_bush` regen을 flora 적합도로 일반화.
- [RESOLVED→rec] **목재 채집과 고사의 관계** — 나무에서 목재를 얼마나, 베면 죽나? options: (a) 채집 = 부분 yield, 나무 생존(재생) (b) 벌목 = 객체 제거(목재 대량 yield + 고사 트리거 = lifecycle object-mortality 재사용) (c) 둘 다 행동(Forage=가지치기 생존 / Fell=벌목 제거); rec: (c) — 벌목을 object-mortality(§7/구조물 파괴)와 동형으로 재사용, 채집은 비파괴 yield. **[명칭 채택]** `Fell` 벌목 행동(glossary 등재). **NEW Q (P_f4):** `Fell`/`Plant` actions.yaml 정의·고사 트리거 위치 → §3/SPEC Open Questions.

### 1f. 소유 가능성 (economy 연결)
- [RESOLVED→rec] **식물 객체에 `owner`를 붙일 수 있나** — economy `owner` primitive 적용 범위? options: (a) 야생 flora = 무주물(공유지), 심은 식물만 owner(`Plant` 행동) (b) flora 전부 무주물(P1, economy 미연결) (c) flora 전부 owner 가능(터·과수원 사유화); rec: (a) — 무주 야생 + 의도적 식재 사유화로 토지/과수원 드라마 훅(economy.md `owner`/상속과 결합), 단 **economy 미출하 동안은 (b)로 동작**(seam만 예약). `Plant` 행동·owner 연결은 economy phase에서.

### 1g. 갱신 주기 (cadence × D12)
- [RESOLVED→rec] **flora 갱신 주기 = 매 틱 vs N틱 bulk** — 성장/번식/고사/재생을 언제? options: (a) 전부 매 틱(정확·비쌈) (b) climate형 다중주기: 그늘 질의=lazy(perception 시), 성장/적합도 적분/고사=느린 bulk(`tick % N`), 자원 재생=타이머 (c) 단일 bulk 주기로 통합; rec: (b) — climate(`tick % 60`)와 정합·다중주기 패턴 재사용, 부하 분산, **고정순서 1패스·wall-clock 금지·map 순회 금지(D12)**. flora bulk 주기 N = climate N과 같게 둘지/오프셋 줄지 하위 결정 포함.
- [RESOLVED→rec] **bulk 결정성 적용 순서** — 식물 객체를 어떤 순서로 갱신? options: (a) `ObjectID` 정렬 1패스(D12 표준) (b) 공간 파티션 병렬 + 고정순서 merge; rec: (a) — climate/world apply와 동일, 단순·결정적; 병렬은 프로파일 후.

### 1h. 모듈 경계 + DAG 위치
- [RESOLVED→rec] **신규 `engine/env/flora` vs `world`/objects 흡수** — 어디에 살까? options: (a) 신규 `engine/env/flora`: 순수 transform `(식물 상태 + terrain/climate 입력 + Rules) → (성장/번식/고사 델타)`, climate와 동형(world가 적용) (b) `world` objects 로직에 흡수(별 모듈 없음) (c) 적합도/성장=`flora`, 그늘 occlusion=`perception` 확장; rec: (a)+(c) — climate 패턴 직접 복제(`core`(+`expr`)+`rng`만 의존, 순수·테스트 용이, D5 관심사 분리); 그늘은 perception 소관이라 flora는 그늘 *파라미터*만 노출.
- [RESOLVED→rec] **DAG 위치** — `engine/env/flora`의 leaf level·의존? options: (a) L1 leaf = `core`(+`expr`)+`rng`만(climate와 같은 stage 2; 입력은 navmap/climate 상태를 *값으로* 받음) (b) navmap/climate import(L2+); rec: (a) — climate "navmap/worldtime import 금지" 불변식과 동형, world가 입력(terrain/climate 상태)을 주입하고 출력 델타를 적용 → world가 유일 객체 변이자. **의존:** 적합도/그늘 수식 = §6 → `engine/kernel/expr`(L0, design §6에서 확정) — climate와 공유. **escalation 해소됨**(`engine/kernel/expr`가 L0 leaf로 확정 → core 승격 불필요).

### 1i. 직렬화 (snapshot / delta)
- [RESOLVED→rec] **식물 객체 직렬화 형태** — flora를 어떻게 스냅샷/스트림? options: (a) 일반 `objects[]`에 합류(현행 berry_bush처럼) + `growth`/그늘 상태 필드 (b) climate형 periodic full + sparse delta(생성/성장단계전이/고사 이벤트) (c) 정적 + 이벤트(spawn/grow/die); rec: (b)/(c) 혼합 — 식물은 동적이라 `data-contracts.md §6`(periodic full + sparse delta, wear/terrain와 정합); spawn/die = `objects[]` add/remove 이벤트, growth = 주기 full. data-contracts §1 objects 스키마 확장 필요(growth 필드).
- [RESOLVED→rec] **그늘은 직렬화하나** — LoS 영향을 스트림에 실을지? options: (a) 직렬화 안 함 — 그늘은 식물 상태에서 파생(perception이 재계산) (b) 렌더용 그늘 오버레이를 별도 스트림 (c) 파생만, 프런트가 식물 위치로 재구성; rec: (a)/(c) — D9정신(파생값 저장 안 함), 그늘은 식물 `Pos`/`growth`에서 결정적 재계산. 렌더 캐노피는 frontend가 합성(map-plan M5 렌더 확장).

### 1j. world-gen 초기 분포
- [RESOLVED→rec] **초기 식물 분포 생성** — 라이브/시나리오 초기 숲을 어떻게 깔까? options: (a) 적합도장 위 seeded-RNG 분포(climate/terrain 적합도가 높은 곳에 군집) (b) 절차적 군집(seed 포인트 + 분산 시뮬 N스텝 pre-warm) (c) 시나리오는 픽스처, 라이브는 절차적(둘 다 — map-plan layout source와 동형); rec: (c) — 골든/시나리오 결정성(픽스처) + 라이브 다양성(절차), `map.yaml`/world-gen layout 패턴 재사용. 분포는 **content 아님**(placement = world-gen, objects.yaml 정신).

### 1k. 번식 밀도 상한 (carrying capacity) — **RESOLVED 2026-07-12 (사람 확정)**
- **문제(라이브 관찰):** flora 활성 시 `grass`가 ~8만 개까지 폭증 → 맵을 카펫처럼 뒤덮음. 기존 density weight `1/(1+n)`은 **평형 밀도가 없다**: 포화 군집(이웃 n)에서 스텝당 총 spawn ≈ `(n+1)·chance·suit/(1+n) = chance·suit`(밀도 무관 **상수**) → 선형+frontier 확장 → 무한 증식. content 튜닝(chance 0.30→0.15 등)은 증식을 늦출 뿐 수렴시키지 못함(가중치의 **형태**에 고정점이 없어서).
- [RESOLVED→(A)] **밀도 상한 방식** — options: **(A)** carrying-capacity weight: `1/(1+n)` → `max(0, 1 − n/K)`, K = 종별 `carrying_capacity`(번식 반경 내 목표 이웃 수). n≥K인 기성 군집 내부의 국소 spawn이 정지하여 밀도 ≈K로 조절된다. n<K인 가장자리는 적합 서식지로 계속 확장할 수 있으며, K는 전역 개체 수 하드캡이 아니다. **(B)** 부모 무관 산포 spawn(목표 areal 밀도까지 여기저기) — 균일 초원 룩이나 §1a 재개방·world 후보 샘플링 필요. **(C)** 종별 전역 총량 하드캡 N — 8만은 막으나 공간적으로 부자연·N은 매직 상수. **채택: (A)** — 최소 변경, 동일 seed/config 재실행의 결정성 유지(각 eligible 부모의 3회 draw 유지), 부모-근접 군집 룩·D2 창발 보존, 국소 밀도가 종별 단일 content 노브(K). 근거·K 캘리브레이션 → `docs/decisions/flora-carrying-capacity.md`.
- **계약:** `carrying_capacity`는 종 `propagation:` 블록의 **선택적** 정수. `K>0` → logistic weight; `K=0`/생략 → 레거시 `1/(1+n)`(미설정 종·픽스처·flora-off 바이트 불변). n 범위 = 동종(SPEC `NeighborCount` scope 결정과 정합). D9 준수(K는 룰 상수, per-plant 필드 아님)·D4/D10(content 데이터)·D12(단일 Step의 eligible 부모당 3회 draw 유지, 동일 seed/config 재현).

## 2. Phases — (각 phase 독립 shippable + 테스트 + 결정성 골든; `climate.md §2` / `map-plan.md M1~M5` 양식)
> **핵심 안전 레버:** P_f1~P_f3는 outcome-중립(flora-off / `Rules` 비어있음 / 그늘 occluder 빈 슬라이스 → 거동 0 변화)
> → 기존 world/perception 골든 불변. **P_f4에서만** 의도적 재기준. climate M-staging과 동형(`docs/plans/climate.md §2`).

### P_f1 — `engine/env/flora` 순수 transform + flora-OFF 출하 (outcome-중립)  ✅ SPEC 작성됨
- `backend/engine/env/flora/SPEC.md` 구현: `New`/`Step`(성장 적분·씨앗분산 번식·이력 고사)/`ShadeOf`/`Suitability`/`Stage`/`Yield`.
- 의존: `core` + `engine/kernel/expr`(§6, L0) + `rng`만. navmap/climate/world/perception import 금지(grep 가드).
- **출하 시 flora-off:** `Rules` 비어있음 → `Step`은 spawn/die 0, `Growth` 불변; `ShadeOf`는 zero-radius. world/perception 미접촉 → 시뮬 거동 불변.
- 테스트: 성장 적분·파생 stage·§6 적합도·씨앗분산(시드 재현)·이력 고사(flicker 없음)·그늘 §6·수율표 seeded 롤(Dexterity 스케일)·owner seam inert·**flora-off 중립**·정렬 결정성·결정성 골든(flora-off 먼저)·resume·missing-input panic·import/literal 가드.

### P_f2 — `world` 와이어링 (cadence + 객체 add/remove 브리지) — 여전히 outcome-중립(Rules 비어있음)
- world가 flora cadence(`tick % N`, N=60 권고/오프셋 비차단)에서 `flora.Step` 호출; 식물마다 `navmap.TerrainAt`+terrain 속성+climate `Moisture`/`Temperature`를 `SiteInput`으로 샘플; `NeighborCount`는 spatial 질의(같은 종 권고); per-step rng fork + `idAlloc`(world가 ObjectID 발급).
- apply 단계: `StepDeltas.Spawned`를 objects[]+spatial에 add, `Died`를 remove, `Grown` 적용 — **world가 유일 객체 변이자**(D12 고정순서). flora `State` 스냅샷은 plan 단계와 alias 안 됨(climate/navmap와 동일).
- **Rules 여전히 비어있음** → spawn/die 0 → 기존 world 골든 불변(outcome-중립 회귀 가드). 와이어링·샘플링·cadence·fork·idAlloc 결정성만 검증.

### P_f3 — 그늘 → perception(LoS) 통합 + 골든 의도적 재기준  ✅ perception SPEC 확장됨
- world가 `flora.ShadeOf`를 perception `ShadeOccluder`로 어댑트(`WorldSnapshot.ShadeOccluders`); perception `Sight`가 곱셈 감쇠 ∏(1−opacity)를 `shade_sight_tau` 임계로 합성.
- **shade-off 중립:** occluder 빈 슬라이스면 `Sight`는 기존 boolean-opaque 거동과 바이트 동일(회귀 가드) → 식물 배치 전까지 기존 perception 골든 불변.
- **이 phase에서만** 그늘 활성 시 영향받는 perception 골든을 의도적 재기준. 시나리오: "나무 군집 뒤 타깃이 그늘 중첩으로 안 보임(어두운 숲)" + "그늘 1그루는 보임".
- **NEW Q (사람 결정 필요, P_f3 전):** occluder 인터페이스 명칭/합성 site, boolean 임계 vs 연속 strength → §3 / SPEC Open Questions.

### P_f4 — 활성 적합도/번식/자원 (`content/objects.yaml` `flora:`) + 골든 의도적 재기준
- `objects.yaml`에 flora 종 활성(예: `berry_shrub`/`oak`) + §6 수식(`platform/config`가 `engine/kernel/expr`로 컴파일 → `flora.Rules`); 스키마 검증·종/아이템/operand 교차검증.
- **berry_bush 마이그레이션:** 활성 시 `berry_bush` → `berry_shrub`(flora) 전환 + `balance.regen.berry_bush` 제거(라이브 hunger-loop 골든 의도적 재기준). `prey`/timer regen은 비-flora로 유지.
- **`Fell`/`Plant` actions.yaml 정의** + `Fell`이 object-mortality 트리거(world apply가 대상을 `flora.Died`에 추가) + `Yield`→inventory 와이어링. `Plant`(owner 설정)는 economy phase로 연기 권고.
- **이 phase에서만** 영향받는 world/perception 골든 재기준(climate M4 동형). 시나리오: "가뭄 N일 → 적합도<θ 지속 → 군집 고사(객체 제거)" + "성숙 군집이 씨앗분산으로 확장" + "Dexterity 높은 채집자가 더 많은 베리".
- **NEW Q (사람 결정 필요, P_f4 전):** `Fell`/`Plant` 행동 정의·고사 트리거 위치, berry_bush in-place 전환 vs 신규 종, `NeighborCount` 종 범위 → §3 / SPEC Open Questions.

### P_f5 — 직렬화/스트림 + 렌더 (`data-contracts.md §6` 합류)
- flora `Plants()` = periodic full 소스, `StepDeltas`(spawn/grow/die) = sparse delta 소스 → `platform/persist` 직렬화 + `data-contracts.md §6`(periodic full + sparse delta, wear/terrain와 정합). objects 스키마에 `growth` 필드 확장.
- frontend: 식물 렌더(성장단계별 스프라이트, 군집), 선택적 캐노피/그늘 오버레이(파생 — 식물 `Pos`/`growth`에서 재구성, 직렬화 안 함). 기존 objects 렌더 확장(map-plan M5와 합류).

### park (frontier — P1 비차단)
economy 소유(`Plant`→owner, 과수원 사유화·상속) · 밀도 경쟁(1c: 그늘→이웃 적합도 피드백 = 종간 경쟁) · 명시 천이(창발만) · 낮밤 그늘(1d) · climate 계절 성장 modulation.

## 3. Notes / escalations
- **§6 평가기 의존 (해소):** 적합도·그늘·자원 재생·고사 판정이 모두 §6 수식 → `engine/kernel/expr`(L0 leaf, `design.md §6` line 89에서 확정) 사용. climate와 공유. 이전 climate escalation("평가기 home")은 `engine/kernel/expr`가 L0로 확정되며 **해소**(core 승격 불필요). `architecture.md §2/§4` 반영.
- **현행 코드와의 정합:** `content/objects.yaml`의 `berry_bush`(이미 `depletes`+`balance.regen.berry_bush` 재생)와 `prey`(mobile)는 flora가 일반화/형식화할 대상. flora-OFF phases 동안 `berry_bush`는 레거시 유지(골든 churn 0), 활성(P_f4)에서 `berry_shrub`로 전환(의도적 재기준). 신규 `berry_shrub`/`oak` flora 종은 활성 전까지 dormant(placement 없음).
- **NEW open questions (사람 결정 필요, 구현 전):** `flora`/`perception` SPEC의 Open Questions에 옵션+추천과 함께 정리됨 —
  (1) `Fell`/`Plant` actions.yaml 추가 + 고사 트리거 위치(P_f4 차단), (2) `berry_bush` in-place 전환 vs 신규 종(P_f4 — 라이브 hunger-loop 영향), (3) perception occluder 인터페이스 명칭/합성 site(P_f3 차단), (4) 그늘 boolean 임계 vs 연속 visibility strength(P_f3), (5) flora cadence N vs climate N(P_f2 비차단), (6) `NeighborCount` 종 범위(P_f4 비차단). **stat 단련 메커니즘(Dexterity를 use로 올림)은 flora 범위 밖 — stats/lifecycle 소관**(flora는 §6로 읽기만).
- **신규 명칭(채택 → glossary/stats 등재 완료):** `suitability`/`growth`/`shade`(+`ShadeOccluder`)/`Fell`/`Plant`/`Dexterity`. `docs/core/glossary.md` 등재; `Dexterity` = `content/stats.yaml` capability.
- **불변 가드:** flora는 D2/D3(하드코딩 생태계 금지) — 숲·천이·먹이사슬은 base 메커닉 창발; D9(미래 수치 필드 금지) — 객체는 supply Effect만, regen=객체-local 수율표; D11(연속좌표, 그늘을 타일 필드로 굳히지 말 것); D12(틱 파생 주기, 고정순서, map 순회 금지).
