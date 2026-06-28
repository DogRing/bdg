# World Readiness — 구현 직전 정합성·현실성 감사 (sizing · content · frontend · algorithms)

> 목적: 세계 통합 **SPEC은 완성**됐으나, 실제로 시나리오(동식물 이동·행동·날씨·온도·강/바다·경로·냄새·사냥·
> 번식)가 **돌아갈지** + **frontend에 보일지** + **수치/크기 비율이 현실적인지**를 구현 전에 점검한다.
> 결론: **SPEC은 준비됐지만, (1) 콘텐츠 다수 미작성 · (2) agent 내비게이션이 navmap을 안 씀 · (3) 번식 메커니즘
> 미설계 · (4) frontend가 하드코딩 목업 · (5) 월드/agent 밀도 비율 문제** 가 남아 있다. 아래가 그 목록.
> (시나리오 카탈로그: `docs/scenarios-world.md` FA1–FA7 + W1–W16. 설계 게이트: `docs/world-integration.md`.)

---

## 1. Sizing & 비율 (수치 정합성)

### 1a. 격자 ↔ 엔티티 ↔ 월드 — **비율 OK** ✅
| 축 | 값 | 비고 |
|---|---|---|
| world bounds | `512 × 512` | `content/world.yaml` |
| navmap cell / spatial cell | `8` | 512/8 = 64×64 셀 |
| scent cell | `10` | ≥ max_speed(1.4)×spread(6)=8.4 ✓ (cell-skip 안전) |
| climate cell | `32` (16×16) | = 4×4 navmap 셀 |
| animal speed | 1.0–1.4 units/tick | 틱당 이동 < 1.4 ≪ 셀 8 → 부드러움 |
| animal size | 1.0–1.2 (속성, 점) | 물리 충돌 없음(W6) |
| flora tree | L12 / W6 | W6 < 셀 8; 그늘 radius=§6(W) |
| sight / smell | 18 / 10 (agent), 14–20 / 10–12 (fauna) | 셀 8의 ~2배 반경 |
**판정:** 엔티티(점/소형) ≪ 셀(8–32) ≪ 월드(512) — 내부 비율은 일관. **크기 비율 자체는 문제 없음.**

### 1b. ⚠ 월드 ↔ agent **밀도** — 문제 (사회 창발 깨짐)
- 40 agent / 512² = agent당 6,553 sq → 균일 배치 시 최근접 간격 **≈ 81 units**.
- agent sight = **18** ≪ 81 → **agent들이 서로 거의 못 본다** → 사회 시나리오(A–L: 가십·평판·역할)가 작동 불가.
- **원인:** 큰 생태용 월드(512)에 agent를 균일 분산하면 국소 밀도가 너무 낮음.
- **해결 (택1, 권장=A):**
  - **(A) 마을 클러스터 시딩** — agent를 ~반경 50–60(≈110×110 구역)에 모아 시딩(world-gen WG5 "거주 적합지"
    + `live-emergence-underseeded`). 국소 간격 ≈ sight → 사회 작동, 큰 월드는 생태(강·포식 반경)에 사용. **fixture가
    agent를 클러스터로 배치해야 함** — 현재 이걸 강제하는 시딩 정책이 없음 → 명시 필요.
  - (B) 첫 시나리오는 작은 집중 맵(~160–200)으로 — fixture bounds override(W1=a)로 가능.
- **추가:** 동물 개체수가 어디에도 파라미터화 안 됨 — 512에서 포식-피식 안정엔 초식 ~20–40 + 포식 ~3–6 권장(fixture).

### 1c. ⚠ 시간 척도 — 연주기는 단기 런서 안 보임
- tick=1 game-min · day=**1440 tick** · year=120일=**172,800 tick**. real_scale 12 → 1년 = 연속 **10 real-day**.
- 일주기(낮밤·일교차)는 1440틱이라 단기 런서 보임; **연주기(−5↔30°C)는 172,800틱이라 안 보임.**
- **해결:** 계절 시나리오(FA5/W2/W9 일부)는 (a) 장기 배치 런 또는 (b) **가속-연(年) 테스트 config**(예 `DaysPerYear`
  축소 또는 `YearFraction` 배속) 필요. frontend 라이브 관전도 동일 — 시간압축/스냅샷 스크럽 필요.

---

## 2. 시나리오 실행가능성 매트릭스 (사용자가 나열한 항목별)

| 사용자 항목 | 현 설계 지원 | 차단 갭 |
|---|---|---|
| 동물 **이동** | 설계 ✅ (fauna steer, NextPos=Pos+dir·speed·DT) | fauna 종 콘텐츠 0개 |
| 동물 **행동** | 설계 ✅ (horizon-1 utility Graze/Flee/Wary/Hunt/Rest) | **Graze/Flee/Wary 액션 미작성**, 종 §6 미작성 |
| **날씨**(비·낮밤) | 설계 ✅ (climate rain + HourOfDay) | **climate.yaml 데이터 미작성** |
| **온도**(연·일·체감) | 설계 ✅ (CA1-3 + F40 apparent_temp) | climate.yaml + 종 apparent_temp §6 + °C 재기준 + 연주기 시간척도 |
| **강** | terrain ✅ (river=고비용 통과) | **W10 결정 대기**: 경로계획(a/b)에 따라 — (a)로컬이면 강은 *로컬 회피*만, (b)면 전역 우회. fauna는 이미 수영(로컬). |
| **바다** | terrain ✅ (sea=impassable) | 동상(W10). agent는 로컬 회피 or A* 우회 — 결정에 달림. |
| **경로**(desire-path) | navmap wear 설계 ✅ | **W10 (a)면 길 창발 삭제**(design §5 개정), (b)면 통행→wear→길 창발. |
| **냄새** | 설계 ✅ (scent grid + wind) | scent 구동 wiring(P2 구현 전) + 발생원 `scent:*` 태그 미작성 |
| **사냥** | 설계 ✅ (Hunt + scent + FOV) | 포식 종 미작성 + Hunt apply(P_fa2) + carcass/Butcher(P_fa3) |
| **번식·탄생** | ✅ **respawn으로 결정**(W11) | 번식 X — 개체수 목표 미달 시 **시야밖+미개발**에 respawn. 남은=placement 알고리즘(§3b). |

**요약:** 이동/행동/날씨/온도/냄새/사냥 = **설계됨, 콘텐츠+구현 대기**. 강/바다/경로(agent) = **W10 결정에 달림**
(로컬 회피 vs A* 우회; (a)면 design §5 길-창발 삭제)(3a). 번식·탄생 = **respawn 개체수조절로 결정**(W11) — 남은 건
숨김 placement 알고리즘(3b).

---

## 3. ⚠ 설계 갭 (구현 전 결정/설계 필요)

### 3a. 경로계획(navmap A*) — 할지 미정 (W10; 개념 정리 2026-06-28)
- **개념 구분(중요):** "주변 칸 물체만 계산하는 균일 그리드"는 **`spatial` hash(근접 질의)** — *이미 존재·작동*
  (perception/fauna sight). `navmap`은 **별개** 모듈로 design §5의 **이동-비용장 + A* 길찾기 + 창발 길(desire-path)**.
  둘 다 균일 그리드·D11 인덱스지만 목적이 다름(근접  vs 경로).
- 현재 agent `MoveTo`=유클리드 거리(`arrival_epsilon`) → navmap **경로계획 미사용**. fauna만 `TerrainSampler`로 *로컬*
  지형비용(수영). 즉 빠진 건 *근접*이 아니라 ***전역 경로계획***.
- **결정(W10) — 경로계획을 원하나?**
  - **(a) 로컬 지형만(경로계획 없음, 사용자 의도에 가까움):** agent도 fauna처럼 밟는 칸의 cost/passable만 로컬 샘플
    (못 가는 칸=슬라이드/정지), 직선 이동. A*·비용장·desire-path 없음. navmap=지형타입 격자. ⚠ **design §5 "길 창발"·
    경로비용 bindTarget 삭제 → design.md 개정 필요(인변식, 승인).** 강/바다/벽은 *로컬 회피*만(큰 호수 빙 도는 전역 우회 X).
  - **(b) design §5 그대로:** navmap 비용장 + A*/Theta* + `wear`→길 창발 + 경로비용 최근접 bindTarget(신규 WI-P5).
- W1/W2/W13/W16 + 경로/강/바다(agent)는 이 결정에 달림. **사람 RESOLVE 필요(+ (a)면 design.md §5 개정).**

### 3b. 개체수 = 숨겨진 respawn (W11 RESOLVED 2026-06-28 — 번식 안 함)
- **결정:** 부모→자식 번식 **없음.** 개체수는 **respawn으로 조절** — 단 "매번"이 아니라 **시야 밖 + 미개발 야생**에 등장.
- **메커니즘(작음):** world가 종 **개체수 목표** 유지, 미달 시 시드 확률/cadence로 1마리 respawn, 위치 = (1) 모든 agent
  `sight_radius` 밖 · (2) 미개발(건물 footprint/정착지 클러스터 밖) · (3) 통과가능 야생 terrain. 후보 중 시드 선택(D12).
  부모/상속 없음(스탯=종 GenSpec). 레거시 `balance.regen.prey_respawn`을 이 조건부 placement로 일반화.
- **함의:** P_fa4 birth 메커니즘 **삭제**, `repro_readiness` drive 불필요. 남은 작업 = **respawn placement 알고리즘**
  (시야밖∧미개발 후보 탐색) — world wiring. 새 메커니즘 거의 없음.

### 3c. 기타 미해결 seam (이미 flag됨)
- `world.WorldState`에 env 필드 + `RenderView()`(engine/world) · animal base-stat `GenSpec`(fauna: content) ·
  planner의 terrain-Mine 바인딩 · °C 임계 튜닝 데이터(flora suitability/decay accel/climate `when`/apparent_temp).
- day/night → perception 시야 감소(야행/주행) = perception OQ **parked** — FA5 야행성 행동엔 필요.

---

## 4. 콘텐츠 갭 (스키마는 있음, 데이터 미작성)

| 콘텐츠 | 상태 | 필요 |
|---|---|---|
| flora 종 | ✅ berry_shrub/oak/grass/wildflower | (°C 재기준 점검) |
| stats / terrain.yaml / world.yaml | ✅ | — |
| **fauna 종** | ❌ 0개 | **deer(초식)·wolf(포식)** fauna: 블록(utility/drives/apparent_temp/speed/senses/diet/threat) |
| **Graze/Flee/Wary 액션** | ❌ | `content/actions.yaml`에 추가(P_fa3) |
| **climate.yaml** | ❌ | transitions + balance(rain·annual·wind °C 데이터) |
| **scent 발생원 태그** | ❌ | 먹는 flora→`scent:food`, prey→`scent:prey`, predator→`scent:predator` + magnitude |
| **시작 fixture** | ❌ | 강+마을 클러스터+숲+산+동물 (집중 ~160–200 맵) — 첫 playable |
| 레거시 `prey` 마이그레이션 | (W7) | fauna 활성화 시 deer로 전환 |

---

## 5. Frontend 갭 (현재 = 하드코딩 목업)

`frontend/src/utils/canvasRenderer.ts` 현황 — **실제 월드 데이터를 안 그린다:**
- **고정 장식 맵**: FORESTS/FIELDS/BUILDINGS/강이 **canvas 픽셀 좌표에 하드코딩**(quadratic 가짜 강, 64/32px 격자선
  = 월드 격자 무관). 코너 라벨 **"(1000,1000)"** — 실제 bounds는 **512** (불일치).
- **카메라 = auto-fit**(`buildTransform`이 agent+object bbox에 맞춤) — 월드 `bounds`/`pixelsPerUnit`를 **안 씀**
  (내가 추가한 `RenderConfig`는 미사용). agent를 따라다니지만 월드 경계·지형은 안 보임.
- **env 미렌더**: WorldFrame의 `animals`/`flora`/`climate`/`terrain`이 state엔 들어오나 **canvas에 안 그려짐**.
- (frontend/SPEC.md 모듈 구조 설명도 stale: `main.ts/sse.ts/world.ts` ↔ 실제 React `main.tsx/hooks/components`.)

**필요(frontend render phase):**
1. **카메라 = 월드 bounds + pixelsPerUnit**(고정 월드뷰) 또는 auto-fit에 animals+월드 bbox 포함. `RenderConfig` 사용.
2. **실제 terrain 렌더** — fixture terrain 격자(navmap)를 타입별 색으로 — 하드코딩 장식 맵 제거(가짜 강·1000 라벨).
3. **env 엔티티** — `animals`(종·heading), `flora`(stage/width→크기) WorldFrame에서 그림.
4. **앰비언트** — `climate`로 낮밤 틴트 + 비 오버레이 + 바람 방향 표시.
5. (선택) 디버그 오버레이: scent heatmap · navmap wear(길) · FOV 콘.

---

## 6. 알고리즘 — 구현 전 모듈 SPEC에 못박을 것

| # | 알고리즘 | 현 상태 | 비고 |
|---|---|---|---|
| 1 | **agent 지형 처리** — W10 결정: (a)로컬 cost/passable 샘플(작음, fauna와 동형) vs (b)A*/Theta* pathfind+wear+cost-bindTarget(WI-P5) | W10 OPEN(3a) | (a)면 design §5 개정; (b)면 design §5 핵심 |
| 2 | pathfind 연결성(8/4-conn, Theta* any-angle) + StepCost √2 정합 | navmap OQ에 위임 | pathfind SPEC 확인/확정 |
| 3 | **scent 확산 스텐실** (downwind bias 함수·falloff 곡선·이웃 가중치) | scent SPEC "fixed weights(balance)"만 | 실제 stencil 수식 미정 |
| 4 | **fauna steer/wander** (채널 방향 + §6 jitter rng + !Passable 슬라이드/정지) | SPEC 계약만 | wander 알고리즘·장애물 회피 미정 |
| 5 | **FOV bearing test** (Heading±fov_arc, atan2 + 각도 wrap) | SPEC 명시 | wrap 처리 확정 |
| 6 | **flow-accumulation hydrology** (D8 흐름·누적·pit priority-flood·침식) | world-gen.md §1 개요 | D8/priority-flood 정밀화 |
| 7 | **respawn placement** (개체수 목표 → 시야밖∧미개발 야생 후보 탐색 + 시드 선택) | W11 RESOLVED(3b) | 번식 X; 작음(후보 필터 1패스) |
| 8 | **apparent_temp §6 + thermal→action** 결합 | F40 계약 | 종 §6 + TakeShelter 커플링 |
| 9 | day/night → perception 시야 감소 | parked | FA5 야행성 |

---

## 7. 우선순위 체크리스트 (구현 진입 전)

**P0 — 설계/결정 (코드 전):**
- [x] ~~번식 메커니즘~~ → **W11 RESOLVED: respawn 개체수조절(번식 X)** — 남은 = 시야밖+미개발 placement 알고리즘(world wiring).
- [ ] **W10 RESOLVE: 경로계획 할지** — (a) 로컬 지형샘플(작음, design §5 길-창발 삭제→design.md 개정) vs (b) A* pathfind(WI-P5). 강/바다/경로(agent)가 여기 달림(3a).
- [ ] **agent 시딩 밀도 정책** — 마을 클러스터 시딩(W12/1b) — 사회 창발 유지.
- [ ] **가속-연 테스트 config**(W13) — 계절 시간척도(1c).
- [ ] day/night→sight 결합 여부(FA5), °C 임계 재기준 owner.

**P1 — 콘텐츠 작성 (스키마 존재):**
- [ ] `content/objects.yaml` fauna 종 deer/wolf + `scent:*` 태그(먹는 flora/prey/predator).
- [ ] `content/actions.yaml` Graze/Flee/Wary.
- [ ] `content/climate.yaml` (transitions + rain/annual/wind °C).
- [ ] 시작 fixture (강+마을+숲+산+동물, 집중 맵).

**P2 — 알고리즘 SPEC 확정:** scent 스텐실 · steer/wander · FOV · pathfind 연결성 · flow-accumulation · apparent_temp §6.

**P3 — 구현 (빌드 순서):** env/fauna 모듈 → world wiring(P1/P2/P3 + **agent-nav P5**) → config(P0) → persist(P4) → worldgen(P4입력) → **frontend 렌더**(§5).

**P4 — frontend 렌더:** bounds 카메라 · 실제 terrain · env 엔티티 · 낮밤/날씨 앰비언트 · 1000→512 정리.

---

## 8. 한 줄 결론
세계 통합 **SPEC은 완성**이고 **크기 비율(셀/엔티티/월드)은 정합**하다. 막는 것은 *설계 SPEC*이 아니라 **(1) 미작성
콘텐츠**(fauna 종·액션·climate.yaml·fixture), **(2) 한 개의 미정 결정 W10**(경로계획 할지 — (a)로컬/(b)A*; (a)면 design §5
개정), **(3) 하드코딩 목업인 frontend 렌더**, **(4) agent 시딩 밀도/시간척도** 다.
**해소된 것:** 번식 = respawn 개체수조절(W11, 시야밖+미개발 placement; 부모/상속/birth 메커니즘 삭제) — 작은 world wiring만.
이들을 닫으면 FA1–FA6·W-세트가 돈다. (FA7 무리·야행성은 추가 설계 후.)
