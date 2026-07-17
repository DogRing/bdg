# Climate & Dynamic Terrain — Subsystem Plan

Concept & rationale: `docs/core/design.md §5` (동적 지형). SPECs now exist:
`backend/engine/env/climate/SPEC.md` (신규 L1 leaf), `backend/engine/space/navmap/SPEC.md` (`SetTerrain` 추가).
관련 모듈: `engine/space/navmap`(지형 *상태* 보유 + `SetTerrain` writer), **신규** `engine/env/climate`(기후 필드·전이),
`engine/world`(소유·갱신 주기·navmap 브리지), `engine/kernel/worldtime`(밤/낮·시각), `content/terrain.yaml` + **신규** `content/climate.yaml`.

## 0. Decisions locked (design.md §5 에서 확정 — 여기서 다시 결정하지 않음)
- 지형은 **상태(`Moisture` 등)를 가진다**. 기후(강우·기온)가 상태를 밀고, 임계 넘으면 **상태/타입 전이**(가뭄→젖은 흙 마름, 장마→흙이 젖음/물에 잠김). 초목(나무·풀)은 terrain이 아니라 **flora 객체**라 전이 대상이 아니고, 악조건이면 객체로서 고사.
- 전이 규칙은 **데이터**(D4/D10), bespoke Go 함수 금지. 가능하면 **§6 수식 DSL**(`when moisture > x → type y`)로 표현.
- **다중 주기 갱신:** 건물 footprint=즉시(이벤트), `wear`=매 틱, 지형 전이=느린 bulk(`tick % N`, 고정순서 1패스). 병렬은 *read* 또는 비겹침 파티션 *write*를 고정순서 merge만. wall-clock·map순회 금지(D12).
- 직렬화: 지형은 정적-1회가 아니라 `wear`처럼 **델타 스트림**(`data-contracts.md §6`).

## 1. Open questions — **ALL RESOLVED** (사람 확정; module SPEC 작성됨)
> 게이트 시운전(spec-architect 열거 → 사람 결정)으로 닫음. SPEC/구현은 이 항목들을 위반하면 안 됨.

- [RESOLVED: (b) coarse climate grid] 상태 입도/저장 — 넓은 단위 climate grid에 보관(navmap cell 여러 개를 묶음), 경로 cost로 내릴 때만 navmap cell로 매핑. 연속좌표·D11과 충돌 없음, bulk 스캔·델타 스트림이 가벼움.
- [RESOLVED: 추천 오버라이드 — Rain + Day/Night + Temperature] P1 forcing 범위 — **세 가지**: ① 강우(`Rainfall`, 아래 모델), ② 밤/낮(신규 forcing 아님 — `worldtime` 시각에서 파생), ③ 기온(`Temperature` 상태 = f(시각, 강우); 비 오면 ↓). **park(나중):** 계절, 바람, 해/일조, 고도. 추가 forcing/상태는 §6 수식 피연산자로 데이터 추가(D10)해 확장.
- [RESOLVED: (c) 틱 기반 함수 + fixture] forcing 생성 — 라이브는 `tick` 파생, 골든/시나리오는 fixture 주입(wall-clock 금지, D12). 단 **강우는 사인이 아니라 seeded 확률 과정**(아래 "강우 모델").
- [RESOLVED: (b) `climate.yaml` 전이표] 전이 규칙 위치 — from-type × 조건 → to-type 전이표를 `content/climate.yaml`에. 로드 시 미정의 type 검출(D10), 기후 데이터의 단일 소유처.
- [RESOLVED: (a) 신규 `engine/env/climate`] 모듈 경계 — forcing→상태→전이 결정 = 순수 함수 모듈. `navmap`은 cost field 책임만, `world`가 *언제* 돌릴지(주기) 소유(D5형 관심사 분리).
- [RESOLVED: (a) `core`-only L1 leaf] DAG 위치 — `engine/env/climate`는 core(+rng)만 의존(입력: 상태+forcing+규칙 → 출력: 새 상태 + 전이 셀 목록). navmap 기록은 `world`가 apply 단계에서 수행(navmap "world가 유일 변이자" 불변식 보존).
- [RESOLVED: (a) `SetTerrain` 추가 — **사람 승인**] navmap 기록 IF — navmap SPEC의 "지형 base-cost 레이어 immutable"을 **의도적으로 완화**, `StampFootprint`와 동형의 world-소유·직렬 `SetTerrain(cells, type)` 추가. ✅ **완료: navmap SPEC §Owned Data + §Public Interface 갱신, `SetTerrain` + `TerrainOverrides` 추가.**
- [RESOLVED: (a) TerrainID 변경만] cost 합성 — 전이는 `TerrainID`만 바꾸고 base cost가 따라옴(D4 단순). Temperature는 P1에서 path cost에 직접 안 들어감(습도/전이, 이후 agent comfort 경로). 연속 moisture-cost 가산은 park.
- [RESOLVED: (a) 고정 전역 N = 60틱(1게임시간)] bulk 주기 — 강우 확률이 매 게임시간 갱신이라 정합. 전역 동기 1패스(고정순서, D12). 부하 분산·dirty-set은 프로파일 후.
- [RESOLVED: (c) 단계별] 골든 재기준 — 도입 phase는 climate-off로 outcome-중립(기존 골든 불변), 활성화 phase에서 영향 골든만 의도적 재기준(map M1→M2+ 동형).
- [RESOLVED: (b)+(c)] 직렬화 — coarse moisture grid는 주기적 full, 지형 type 변화는 전이 *이벤트*(`cell, from→to`)로. `data-contracts.md §6` "periodic full + sparse deltas"와 정합. navmap 측 델타 소스 = `TerrainOverrides()`.

### 1a. 강우 모델 (RESOLVED — 매 1게임시간 평가, seeded RNG/D12, wall-clock 금지)
- 비 그친 직후 `p_rain = 0`.
- 매 게임시간 `p_rain += seeded-random 소량`(증가량 계수 = `balance.yaml`/`climate.yaml`).
- 기대 첫 강우 ≈ **10일**(240게임시간) 이 되도록 보정; **30일(720게임시간) 경과 시 무조건 강우(hard cap).**
- 강우 시작 시 지속 = uniform **2~12시간**(seeded RNG).
- 강우 중 `Moisture↑`; 그친 뒤 증발로 `↓`, **기온 높을수록 증발↑**(Temperature → Moisture 피드백).
- 설계 의도(고정): 10일 기대 / 30일 강제 / 2~12시간 지속. **튜닝 상수**(시간당 증가량 등)는 `content/climate.yaml` balance 블록(rate only, D9 정신).

### 1b. 기온 모델 (RESOLVED, P1 최소)
- `Temperature` = f(시각[밤↓/낮↑, `worldtime`], 강우[비 오면 ↓]). 계절 보정은 park.
- 용도: Moisture 증발률 변조(위). 이후 agent comfort/stamina로 확장 가능(§6 수식).

## 1c. Phase-1 reopened — annual cycle / wind / apparent_temp (was parked, RESOLVED #2)

> **2차 게이트 (re-open) 2026-06-26.** §1 RESOLVED #2 가 **계절·바람·해·고도를 명시적으로 park** 했다. Phase-1 world 목표
> (연 1주기 기온 30°C→−5°C · 바람이 냄새를 그리드에 퍼뜨림 · 바람+기온 = 체감온도)가 그중 **연주기 기온·바람**을 다시
> 요구하므로, 그 둘을 여기서 **OPEN으로 재개방**한다(park 상태였으니 RESOLVED 아님 — 사람만 flip). **아무것도 결정하지
> 않는다(메커니즘 발명 = 결함).** 옵션은 §0·§1·design.md §5/§6 안에서만 고르며, 깨끗한 메커니즘이 도출 불가하면 그게
> OPEN 으로 열거할 대상이다. **체감온도 자체는 fauna 소관**(fauna F40) — 여기 climate 의 책임은 **operand 노출 + 단위 + 바람
> 생성**이지 apparent_temp 계산이 아니다. 교차참조는 fauna §1.3 F40/F33/F43/F44, **중복 금지**.
> **operand 명칭(두 문서 공유, 고정):** `temperature` · `moisture` · `wind.dir` · `wind.mag` · (fauna 측) `apparent_temp`.
>
> **바람의 지역 차폐(벽/건물/동굴 = shelter)는 climate 밖.** climate는 world-uniform `Wind()`만 생성하고, 그 값을 셀별 노출도 ε로 감쇠(local wind = global × ε)하는 것은 별도 서브시스템 `docs/plans/shelter.md`(Tier-2) 소관 — climate는 건드리지 않는다(shelter.md §0).

### Resolutions — 사람 확정 (2026-06-26)
> CA1·CA2·CA3 + worldtime 캘린더 RESOLVED. 아래 옵션 상세는 근거 기록(재논쟁 금지).
- **worldtime (선행):** `DaysPerSeason=30`·`SeasonsPerYear=4` ⇒ **`DaysPerYear=120`** (1게임년=120게임일). `YearFraction(t)`/`DayOfYear(t)` accessor 추가 + climate `Forcing`에 world-파생 `YearFraction` 필드. (worldtime SPEC Open Q "Calendar granularity" = RESOLVED 30/4.)
- **CA1 = `(a) day-of-year sinusoid` + `(a) additive offset`.** CA3=(ii)라 °C로 직접: `T(°C) = annualMid + annualAmp·sin(2π·yearFraction+φ) + dailyDelta(hourOfDay) − rainDrop·raining`; `annualMid`/`annualAmp`는 연 진동이 ~**−5…30°C**를 덮도록(balance 계수). season enum 없음(§0-3 정합).
- **CA2 = `(a) world-uniform 우세풍 + seeded directional random-walk`** (climate per-step `fork(tick)`), **climate-step cadence**(별도 Nw 없음). operand `wind.dir`(**라디안 [0,2π)**) + `wind.mag`(정규화 [0,1]). world-uniform `Wind`는 `State`에 보관(resume byte-동일). `wind.mag` = glossary 신규 coin.
- **CA3 = (ii) 전면 °C** (사람이 (i) 약-rec를 뒤집음). climate `Temperature` 상태 = **실제 °C**; operand `temperature` = °C; fauna `apparent_temp`가 °C 직독; `moisture`는 [0,1] 유지. climate가 `CellAt(pos core.Vec2) CellState` + `Wind()` 노출 → world가 동물 로컬 셀+바람을 fauna Context로 어댑트(apparent_temp 식은 fauna 소유).
  - **⚠ 결과 — 구현 시 °C 재기준 필요(M-phase staging, M4 동형):** 기존 [0,1] 가정 전부 °C로 — 1b(`TempDayPeak`/`TempNightLow`/`TempRainDrop`), 증발식(`EvapBaseRate + EvapTempScale·Temperature`), `content/climate.yaml` `when: temperature > …` 임계, climate SPEC `CellState`/`Init*`/`[0,1] clamp`, climate 골든. 이로써 **1b의 정규화-기온 + RESOLVED #2의 계절·바람 park를 supersede**.

**CA1 — 연주기 기온 (annual temperature cycle).** 1게임년 주기 곡선을 기존 일주기(1b) + 강우 강하와 합성해 평균기온이
30°C↔−5°C 로 진동. 남은 것 = (i) 주기 **shape** (ii) 일주기와의 **합성 방식** (iii) `worldtime` **의존**.
- shape options: (a) **day-of-year sinusoid** — 연평균을 `annualMid ± annualAmp·sin(2π·yearFraction + φ)` 로(파라미터 2개:
  중앙값·진폭, 또는 min/max −5..30), 매끈·주기 1개 의도와 정합, RNG 무관(결정성 무료). (b) season-index 구간선형 — `worldtime.Season`
  4 setpoint 보간; 거칠고 'season enum' 냄새(§0-3 fauna 정신 "계절 label 없음"과 충돌 위험). (c) seeded 연간 random-walk —
  강우식 차용; "1년 주기" 의도를 깨고 과함. **rec: (a) sinusoid.**
- 합성 options: (a) **additive offset** — 연주기 = 일주기 곡선의 *중앙값*을 이동, 일주기는 그 위 고정진폭 delta, 강우는 추가 차감
  (`T = annual(dayOfYear) + dailyDelta(hourOfDay) − rainDrop·raining`). 단순·1b 그대로 위에 얹힘. (b) amplitude modulation — 연주기가
  일교차 진폭을 변조(여름 일교차↑); 더 사실적이나 파라미터↑, 후행. (c) max/곱 — 의미 없음. **rec: (a) additive offset.**
- **⚠ worldtime 의존 (foundational flag):** 연주기는 **연속적 day-of-year(또는 year-fraction)** 가 필요. 현 `worldtime`은
  `Season(t)`/`Year(t)`/`DayOfRun(t)` + `DaysPerSeason`/`SeasonsPerYear` 는 있으나 **`DayOfYear(t)`/`YearFraction(t)` accessor 와
  `DaysPerYear`(=DaysPerSeason·SeasonsPerYear) 가 없다**. 또한 worldtime SPEC 자체 Open Question(§"Calendar granularity":
  `DaysPerSeason`/`SeasonsPerYear` 미확정, 제안 30/4)이 **선결**이어야 연 길이가 고정된다. 그리고 climate `Forcing` 는 현재
  `HourOfDay`+`AbsHour` 만 운반 — 연주기를 순수 transform 으로 유지하려면 **`Forcing` 에 `DayOfYear`(또는 `YearFraction`) 필드
  추가**(world 가 worldtime 에서 파생해 주입, climate 는 wall-clock 미접촉, D12). ⇒ **worldtime 재개방(작은 확장) + climate Forcing
  확장이 CA1 의 선행.** **OPEN.**

**CA2 — 바람 모델 (wind model).** 신규 forcing `Wind{dir, mag}`(world-uniform 스칼라; per-cell 필드는 후행). 남은 것 = (i) 시간에
따른 **결정적 생성**(seeded, D12) (ii) cadence (iii) §6 operand 노출 (iv) `dir` 표현 단위.
- 생성 options: (a) **prevailing 방향 + seeded directional random-walk** — `dir` 가 우세풍 각 둘레로 표류, `mag` 는 seeded noise;
  강우의 "seeded 확률 과정"(RESOLVED #3) 선례·climate per-step `fork(tick)` 그대로(D12). **rec.** (b) 결정적 sinusoid/회전 dir —
  byte 사소하나 기계적·주기적. (c) **per-climate-cell 바람장** — 공간변화 풍부하나 scent 확산 스텐실(F33)이 복잡해지고 무거움 → frontier;
  P1 = world-uniform 단일 `Wind`. **rec: (a) world-uniform directional random-walk(seeded fork).**
- cadence: climate step(`tick % 60`, 1게임시간)에서 같이 생성(별도 `Nw` 도입 안 함); scent 확산(F33 `tick % Ns`)·apparent_temp(fauna
  Nt)은 그 시점의 `Wind` 를 읽음. **rec: climate step cadence 재사용.**
- operand 노출: `wind.dir`(스칼라 방향) + `wind.mag`(스칼라 세기). scent 확산은 world 가 `(dir, mag)` 에서 downwind 이웃 offset 을 파생
  (F33), fauna utility/apparent_temp 는 `wind.dir`/`wind.mag` 를 §6 Attr 피연산자로 읽음(F27 어댑터, expr L0 불변). **rec: 둘 다 노출.**
- **⚠ `dir` 표현 단위(미결):** 라디안 `[0,2π)` vs turns `[0,1)` vs degrees — §6 operand 는 float 라 어느 쪽이든 동작하나 scent 스텐실의
  downwind 이웃 선택·fauna upwind steer(F34)와 **단위 합의**가 필요. **rec: 라디안 `[0,2π)`** (단, 사람 확인). `mag` 정규화 `[0,1]`
  vs world-units/step 도 동반 결정. **⚠ `wind.mag` 는 신규 coin** (fauna §1.2 에는 `wind.dir` 만 등재 — `wind.mag` glossary 추가 필요).
- 결정성: 생성 = climate 의 주입 per-step rng fork; world-uniform `Wind` 는 `State` 에 보관(resume byte-동일, D12). **OPEN.**

**CA3 — 단위 + apparent_temp operand 노출.** (i) **단위 결정** (ii) climate 가 `temperature`/`moisture`/`wind.*` 를 §6 Attr
operand 로 노출하는지 확인(apparent_temp 식 자체는 fauna F40 소관).
- (i) 단위 options: (a) climate 내부 `Temperature ∈ [0,1]` 유지 + **°C 매핑**(config `tempMinC=−5`, `tempMaxC=30`; `°C = lerp`) —
  기존 강우/증발/전이 수식(`EvapBase + EvapTempScale·Temperature`, `TempDayPeak/Low/RainDrop` 전부 [0,1] 가정)의 **byte-안정** 보존, °C 는
  display + operand 로만. (b) climate 를 **실제 °C(−5..30)** 로 이동 — 사람이 명시한 °C 와 직접 정합·개념 명료하나 위 수식·climate 골든
  전부 재기준(blast radius↑). **⚠ 진짜 fork:** 사람이 "°C" 라 했고 fauna apparent_temp 도 °C 로 읽는 게 자연스러움 → 그러면 operand
  `temperature` 가 °C 여야 한다. 그러나 operand 명칭은 **두 문서 공유 고정**(`temperature` 하나)이라 "정규화 [0,1] 인가 °C 인가" 가
  fauna 와 **동시에** 정해져야 함(CA3↔F40 결합). 절충안: 내부 [0,1] 유지 + 별도 °C operand(`temperature_c`) 노출 — 그러나 새 operand
  명칭 도입 = 명칭 일관성 제약과 충돌. **이 fork 가 정확히 OPEN** — climate·fauna 양쪽 operand 의미를 사람이 한 번에 결정. **rec(약):**
  내부 transform 은 [0,1] 정규화 유지(골든 안정), **operand `temperature` 의 노출 단위(정규화 vs °C)는 fauna apparent_temp 식과 함께
  사람이 확정** — climate 단독 결정 금지.
- (ii) operand 노출 seam: climate 는 **producer**, apparent_temp 는 fauna 의 animal Context 어댑터가 읽는다(F27). 따라서 world 가
  동물의 **로컬 climate 셀**(CellState: `Moisture`/`Temperature`) + `Wind` 를 샘플해 animal expr Context 의 `Attr("temperature")`/
  `Attr("moisture")`/`Attr("wind.dir")`/`Attr("wind.mag")` 로 주입해야 함. **현 climate 노출 API = `Cells()`(정렬 full) + `Rain()` 뿐** —
  위치-샘플 읽기 경로(`CellAt(pos core.Vec2) CellState` 또는 world 가 GridCell 매핑 후 `Cells()` 인덱싱)와 `Wind()` accessor 추가가
  필요. **rec: climate 가 `CellAt(pos)` + `Wind()` 노출; world 가 fauna Context 로 어댑트**(apparent_temp 식은 fauna 소유 — climate 책임
  = operand 값 + 단위). **OPEN.**

> **통합 seam(교차참조, 중복 금지):** `world` 가 climate `Wind` 를 ① fauna scent-spread(F33 downwind 전파) ② apparent_temp operand
> (F40) 양쪽에 먹인다 — 이는 fauna F41/F33 와이어링이다. climate 는 `Wind` 를 *생성·노출*만, 소비·확산 패스는 fauna/world.
> **불변 플래그:** D12(바람 생성 = seeded·고정순서; 연주기 = worldtime 파생, wall-clock 금지) · D10(신규 forcing/operand = content/data
> + §6) · D11(world-uniform 스칼라 바람 → 동물 칸 스냅 무관) — 직접 위반 없음. 단 **CA3 단위 fork 는 cross-doc operand 의미(climate↔fauna)
> 를 흔들므로** 조율 리스크로 표기(위반 아님): `temperature`/`wind.mag` 의 단위·정규화가 두 문서에서 **반드시 동일**해야 한다.

## 1d. Phase-1 reopened #3 — 겨울 눈 (강수 형태 + 적설/용해)  [ALL RESOLVED 2026-07-14]

> **Resolutions — 사람 확정 (2026-07-14):** **CS2 = (b) 백엔드 world-uniform 스칼라** `SnowCover∈[0,1]` (climate.State,
> 결정적 적분, WorldFrame/snapshot 스트림). **CS5 = (a) 낙하 눈 + 식생 스프라이트만** (지면/지형 백화 없음 — (b)(c) frontier;
> **(b)는 2026-07-17 사람 요청으로 추가 SHIPPED** — 3D tile-pass 백색 워시, 미니맵 제외. §CS5).
> **CS1 = (a) frontend 파생 + `snowFallC=2°C`.** **CS3 = 온도비례 선형 용해** (0°C 이하 축적 / 영상 (temp−0) 비례 용해;
> 율은 climate.yaml balance). **CS4 = (a) `snowCover` 로 season 구동** (CS2(b) 채택으로 함의 — `snow` = `snowCover ≥
> snowSpriteThresh`). 근거·기각안 상세 = `docs/decisions/climate-winter-snow.md` (재논쟁 금지). 활성화 staging = §CS-M.

> **3차 게이트 (re-open) 2026-07-14.** 사람이 명시한 겨울 눈 모델: **0~3°C 에선 눈이 내리되 녹고(적설 안 됨),
> 0°C 이하에서 눈이 쌓여 식생 스프라이트가 눈 변형으로 바뀐다.** 현 구현은 (a) 강수가 온도 무관 항상 '비'
> (`raining` bool; ambient.ts/atmosphere.ts 빗줄기만·눈 없음), (b) 적설 상태 자체가 climate·계약에 부재,
> (c) frontend `floraSeason(temperature)` 이 **순간 기온** <0°C 에 즉시 눈 스프라이트 → 일교차로 0°C 를 오르내리면
> 눈나무가 하루에 두 번 껌뻑임(사람이 원하는 "쌓여서" 축적 개념 없음). ⇒ **강수 형태(비↔눈)** 와 **적설 상태(축적↔용해)**
> 두 축을 여기서 OPEN 으로 연다. **아무것도 결정하지 않는다(메커니즘 발명 = 결함).** 옵션은 §0·§1·§1c·design.md §5
> 안에서만 고르며 **CA3 의 °C 재기준을 상속**(freezeC/snowFallC 는 °C).
>
> **필드 명칭(고정 후보):** `snow_cover`(적설, 정규화 [0,1]; world-uniform 스칼라) — glossary 신규 coin 후보.
> 강수 형태는 별도 필드 없이 `raining`+`temperature` 파생 가능(CS1).
> **불변 플래그:** D12(적설 적분 = climate step 결정적·고정순서; wall-clock 금지) · D10(신규 상태/임계 =
> content/climate.yaml balance + §6, 신규 Go 함수 최소) · 렌더 순수성(적설 렌더는 스트림된 `snow_cover` 만; frontend
> 모듈-mutable 금지). **교차참조(중복 금지):** `docs/plans/gl-atmosphere.md`(강설 파티클), `docs/plans/frontend.md` §8
> (스프라이트 season 드라이버 CS4·현 P6-Q2 supersede), fauna F40(apparent_temp 는 눈과 무관 — 건드리지 않음),
> `data-contracts.md` §2/§4/§6(스트림 필드).

### CS1 — 강수 형태 (내리는 비 ↔ 눈, 시각)  [RESOLVED: (a) frontend 파생, snowFallC=2°C]
낙하 강수의 **시각 형태**. `raining`+`temperature` 는 이미 스트림되므로 순수 파생 가능. 남은 것 = (i) 결정 위치 (ii) 임계.
- 위치 options: (a) **frontend 파생** — 렌더가 `raining && temperature < snowFallC` 면 빗줄기 대신 눈송이. 계약 무변경,
  ambient.ts(2D)/atmosphere.ts(3D) 분기만. (b) 백엔드 필드 `precip: 'rain'|'snow'|'none'` 추가 — authoritative 하나
  계약 확장(renderframe/types/persist). **rec: (a) frontend 파생** — 형태는 이미 스트림된 두 값의 순수 함수, sim 거동·직렬화 무영향.
- 임계 `snowFallC`: 사람 "0~3°C 눈" 과 정합하게 ≈ **2°C**(frontend 상수 or manifest). **rec: 2°C.**

### CS2 — 적설 상태 위치 (축적/용해 상태를 어디에)  [RESOLVED: (b) 백엔드 world-uniform 스칼라]
"쌓인 눈 / 바뀐 스프라이트" 를 낳는 지속 상태.
- (a) **frontend 적분** — useWorld 리듀서가 climate 프레임마다 `snowCover∈[0,1]` 적분(영하+강수 →↑, 영상 →↓). 빠르고 백엔드
  무변경. **✗ 클라이언트별 불일치**(스냅샷에 없음 → 새 접속자엔 눈 없음, 리로드=리셋), 결정성·persist 원칙([[persist-consistency-upgrade]])과 충돌.
- (b) **백엔드 world-uniform 스칼라** — climate.State 에 `SnowCover float64` 추가, climate step 에서 적분(CS3),
  WorldFrame/`sim:{run}:climate` 로 스트림, snapshot resume 바이트동일. 결정적·전 클라이언트 일관·지속. **신규 sim 메커니즘**
  (계약 확장: renderframe.go·types.ts·persist SPEC·data-contracts). §0 "periodic full + sparse delta" 패턴 그대로. **rec.**
- (c) **백엔드 per-climate-cell 적설** — coarse climate grid 셀별 depth(지형·고도별 패치 용해, 눈 덮인 지형 렌더 가능).
  §1 RESOLVED (b) coarse grid 재사용하나 무겁고 scent 스텐실↑ → **frontier(park).**
- **rec: (b) world-uniform 스칼라(P1); (c) frontier.**

### CS3 — 적축/용해 동역학 (식)  [RESOLVED: 온도비례 선형 용해]
사람 스펙 직역: 영하+강수 → 쌓이고, 영상 → 녹음(따뜻할수록 빨리). 0~3°C 강수는 눈으로 *보이지만*(CS1) temp>0 이라 안 쌓이고 녹음 = "눈이지만 녹고".
- 식(rec): 매 climate step(1게임시간, `tick%60`) —
  `if temperature <= freezeC && raining: snowCover += SnowAccumRate`
  `else if temperature > freezeC:      snowCover -= SnowMeltRate·max(0, temperature-freezeC)`
  `snowCover` clamp [0,1]. `freezeC=0`, 상수는 `content/climate.yaml` balance 블록(rate only, D9 정신). 용해 ∝ (temp−0)
  이 사람의 "0~3 녹음 / 저온일수록 유지" 를 자동 충족.
- 대안: 상수 용해율(온도 무관) — 단순하나 "따뜻할수록 빨리" 미충족. **rec: 온도비례 선형.**
- 적축률: 상수 `SnowAccumRate`(P1) vs 강수 세기(`MoistureRainRate`) 연동. **rec: 상수(P1); 세기 연동 park.** (상수값 = balance 튜닝).

### CS4 — 스프라이트 season 드라이버 전환 (frontend)  [RESOLVED: (a) snowCover 구동]
현 `floraSeason(temperature)` 는 **순간 기온** <0°C → 즉시 눈(껌뻑임). 사람 "쌓여서 스프라이트가 변하고" = 적설 기준이어야.
- (a) **`snow` season 을 `snowCover` 로 구동** — `snowCover ≥ snowSpriteThresh`(예 0.1) 면 'snow', 아니면 기존 온도 기반
  ('bare' <5°C / 'leaf'). 일교차 껌뻑임 해소, 축적/용해에 스프라이트가 따라옴. **CS2(b) 의 `snow_cover` 스트림 필드 필요**
  → types.ts `ClimateState.snowCover` + `floraSeason` 시그니처 확장(climate 전체 수신, snowCover 우선·temperature fallback). **rec.**
- (b) 현행 온도-only 유지 — 사람 요구 미충족. **✗.**
- **rec: (a).** ⚠ `docs/plans/frontend.md` §8 P6-Q2(현 `snowBelowC=0`) 를 이 결정이 **supersede** — frontend 플랜·`assets.test.ts`
  floraSeason 케이스 재기준 필요.

### CS5 — 지면 눈 레이어 (지형 백화)  [RESOLVED: (a) P1 → (b) 추가, 사람 요청 2026-07-17 SHIPPED]
쌓인 눈이 **지형/지면**도 하얗게 하나(식생 스프라이트뿐 아니라)?
- (a) **P1 = 지면 레이어 없음** — 낙하 눈(CS1) + 식생 스프라이트(CS4)만. 사람이 명시한 범위("스프라이트가 변하고")와 정합. **rec.**
- (b) world-uniform 백색 워시 ∝ `snowCover`(지형 위 반투명) — 저렴, "눈 세계" 가독성↑. 원하면 소규모 추가.
  → **사람 요청(2026-07-17)으로 추가 SHIPPED**: 3D tile pass 전용(메인 뷰), eased `snowCover` ∝ 알베도 백화
  (물 top 제외·벽면 감쇠, 조명 전 적용 — 메커니즘은 `frontend/src/gl/SPEC.md`). **2D 미니맵은 날씨/적설 무표시
  유지**(사람 지시: 미니맵엔 날씨 불필요).
- (c) per-cell 백화 — CS2(c) 필요. frontier.
- **rec: (a)(P1); (b)는 사람 원하면 추가; (c) frontier.**

### CS 통합 seam / phase (RESOLVE 후 staging)
- **CS-M(활성화):** CS2(b) `SnowCover` → climate.State + step 적분(CS3) + renderframe/persist/data-contracts 스트림 +
  types.ts + `floraSeason`(CS4) + ambient.ts/atmosphere.ts 강설(CS1). CA3 처럼 climate 골든 의도적 재기준(눈 상태 추가).
  frontend.md §8 P6-Q2 supersede. staging 양식은 M4/flora 동형.
- **불변 재확인:** D12(적분 결정적·per-step fork)·D10(임계/율 = climate.yaml)·렌더 순수성(스트림값만). CA3 °C 상속.

## 1e. Phase-1 reopened #4 — 얼음 (눈→얼음 크러스트 + 물→얼음 지형)  [RESOLVED 2026-07-14]

> **Resolutions — 사람 확정 (2026-07-14):** **ICE1 = (b) 물→얼음 지형**(path A 눈 크러스트·ICE2 는 park).
> **ICE3 = lake+river 결빙, per-cell `FrozenFrom` 원본 저장**(단일 `ice` 타입, 해빙 시 정확 복원), `ice` = passable·
> `base_cost≈1.2`·`Swim` 해제. **ICE3b = 표 sentinel `__origin__`**(결빙·해빙 규칙이 climate.yaml 표에 남고, 엔진은
> `to==IceType` origin 자동캡처 + `__origin__` resolve 만). **ICE4/ICE5(b)/ICE6(b) = rec 확정**(데이터 임계·terrain
> 색 렌더·기존 델타 스트림 + `FrozenFrom` per-cell 직렬화). 근거·기각안 = `docs/decisions/climate-ice.md`(재논쟁 금지).
> 활성화 staging = §ICE-M.

> **4차 게이트 (re-open) 2026-07-14.** CS(눈) 후속 질문: "눈이 얼음이 될 수 있나 / 어떻게 실제처럼."
> 현실의 **'얼음'은 두 갈래** — **(A) 눈이 굳어 얼음**(melt-refreeze 크러스트 / 압밀) · **(B) 물(호수·강·바다)이
> 얼어 얼음 지형**. 둘은 완전히 다른 메커니즘이다. **아무것도 결정 안 함(발명=결함).** 옵션은 §0·§1·§1c·§1d·
> design.md §5 안에서만 고르고 CA3 °C 를 상속. **불변 플래그:** D12(전이·적분 결정적·전역동기·wall-clock 금지) ·
> D10(임계/타입 = content) · D4(비용 = Tag/base-cost 파생). **교차참조:** §1 "river-freeze needs an `ice`
> terrain type"(이미 예견) · navmap `SetTerrain`(M1 완료)·`TerrainOverrides` 델타(M5, data-contracts §6) ·
> §1d CS(눈) 스칼라 방식.

### ICE1 — '얼음'이 모델에서 무엇인가  ⚠ 핵심 fork  [RESOLVED: (b) 물→얼음 지형 (P1); (a) 눈 크러스트는 후행]
> **사람 확정 (2026-07-14):** path **(b) 물→얼음 지형** — 기존 climate 전이표 + navmap `SetTerrain`/`TerrainOverrides`
> 재사용, 거의 content-only. path (a) 눈 크러스트 + (c) 는 후행(따라서 **ICE2·ICE5(a)·ICE6(a) 는 이번 phase 비적용**,
> park). 남은 OPEN = **ICE3**(대상·해빙 정체성·지형 속성) + ICE4/ICE5(b)/ICE6(b) 확정.
- (a) **눈 상태(snowpack)가 얼음화** — `SnowCover` 가 melt-refreeze/압밀로 '얼음/크러스트' 성질 획득. world-uniform
  스칼라 1개 추가(`IceCover`, `SnowCover`와 동형, CS2b 재현). 시각 = 지면/식생 스프라이트 변형; 이동(미끄러움)은
  후행. **"눈→얼음" 직역에 부합**하나 시각 임팩트 작고 CS5(지면 레이어 없음)와 상충 → 스프라이트 필요.
- (b) **물→얼음 지형** — `river`/`lake`/(`sea`)가 추울 때 `ice*` TerrainID 로 전이(**기존 §1d/§1 전이표 재사용**),
  따뜻하면 해빙. per-cell 공간적, `navmap.SetTerrain`(M1 완료) + `TerrainOverrides` 델타로 스트림 → **신규 계약 0**.
  얼어붙은 호수/강 위를 걷는 = **가장 상징적 '얼음'**. **거의 content-only**(terrain.yaml 신규 타입 + climate.yaml 규칙).
- (c) **둘 다** — 눈 크러스트(a) + 물 결빙(b). 최대 현실성, 두 메커니즘.
- **rec:** 아키텍처 적합도·임팩트로 **(b) 물→얼음 지형을 P1 우선**(전이표가 이미 이걸 예견했고 신규 엔진코드 거의 0);
  (a) 눈 크러스트는 후행 refinement. 단 사용자 직역이 (a)라 **최종 path 는 사람 선택**.

### ICE2 — (path A) 눈이 얼음이 되는 물리  [OPEN]
- (a) **melt-refreeze 크러스트** — `temperature>0` 로 부분 용해된 눈이 이후 `temperature<0`(신설 강수 없이) 재동결하면
  얼음화(용해분의 일부를 `SnowCover`→`IceCover` 로 이동). 우리 일주기 °C 곡선에 자연 발생 = **가장 사실적**("녹았다 얼면 얼음").
- (b) **압밀/노화** — 오래·두껍게 쌓인 눈이 느리게 얼음화(용해 무관). age 추적 or 지속 고-`SnowCover` 에 rate.
- (c) 둘 다.
- **rec: (a) melt-refreeze.**

### ICE3 — (path B) 어느 물이 어는가 + 해빙 정체성 + `ice` 지형 속성  [RESOLVED 2026-07-14]
> **사람 확정:** 대상 = **lake + river**(sea 제외). 해빙 정체성 = **per-cell 원본 타입 저장** — 단일 `ice`
> 타입 + `CellState` 에 신규 `FrozenFrom navTerrainID`(결빙 전 물타입; 비결빙=""); 해빙 시 `Terrain = FrozenFrom`
> 로 정확히 복원(언 강→강, 언 호수→호수). ⇒ **ICE6(b) 재평가:** "신규 계약 0" 아님 — `FrozenFrom` 이 per-cell
> 직렬화 필드로 추가됨(climate digest `cells[]` + data-contracts §10). `ice` 속성 = **passable**, `base_cost≈1.2`,
> 물의 `Swim` required_tag 제거, terrain.yaml 신규 `ice` 타입 + `TERRAIN_STYLE` 청백색. **⚠ 파생 OPEN = ICE3b.**

### ICE3b — (path B) 결빙 origin 캡처 + 해빙 복원의 표현  [RESOLVED: (b) 표 sentinel `__origin__`]
per-cell `FrozenFrom` 채택의 결과: 정적 `from×when→to` 표는 (1) 결빙 시 원본을 `FrozenFrom` 으로 **캡처**하는 것과
(2) 해빙 시 `to` 가 셀별 `FrozenFrom` 이라 **동적 복원**하는 것을 그대로 표현 못 함. 표현 방식 =
- (a) **엔진 special-case + config** — climate.Config 에 `IceType`+`ThawWhen`(§6 bool). Step: 어떤 전이든 `To==IceType`
  면 `FrozenFrom=From` 캡처; `Terrain==IceType && ThawWhen` 이면 `Terrain=FrozenFrom` 복원(표 밖 분기). 최소 content,
  단 해빙 로직이 엔진에(약한 D4 텐션 — 분기 1개).
- (b) **표 sentinel `to: __origin__`** — 결빙 규칙 `{from: lake/river, to: ice, when: "temperature < freezeC"}`(엔진이
  `to==ice` 시 origin 자동 캡처) + 해빙 규칙 `{from: ice, to: __origin__, when: "temperature > thawC"}` 에서 `__origin__`
  = 엔진이 `FrozenFrom` 으로 resolve 하는 예약 토큰. **freeze+thaw 가 climate.yaml 표에 그대로 남음(D10/D4 정신)**,
  엔진은 예약 토큰 1개 + origin 자동캡처만 학습.
- (c) **terrain attr 파생** — terrain.yaml 물타입에 `frozen_as: ice`/`freezable: true` 부여; 엔진이 freezable 셀을
  temp<freezeC 에 얼리고 되돌림. 가장 data-driven 이나 terrain 스키마 신규 필드 + 조건 위치가 표 밖.
- **rec: (b) sentinel `__origin__`** — 결빙·해빙 규칙이 데이터 표에 남고(비대칭 freezeC/thawC 히스테리시스도 표에서),
  엔진 변경은 예약 토큰 resolve + `to==IceType` origin 캡처로 국소.
- **대상 options:** (i) 정지수만(`lake`) (ii) `lake`+`river` (iii) +`sea` (iv) +포화 `soil`(빙판). sea 결빙은 느리고
  염분 영향 → **P1 제외 rec**. **rec: (ii) lake+river.**
- **⚠ 해빙 정체성 문제(진짜 fork):** 전이표는 stateless(from→to). 단일 `ice` 로 얼리면 해빙 시 **원래 물타입(강/호수)을
  잃음** → 언 강이 호수로 복원되는 오류. options: (i) **분리 결빙 타입** `ice_lake`/`ice_river`(해빙 `ice_lake→lake`
  명확, 타입 수↑, 기존 first-match 표와 정합) (ii) 단일 `ice` + 정규 물타입으로 lossy 복원 (iii) pre-freeze 타입을
  per-cell 상태로 저장(무거움, 신규 필드). **rec: (i) 분리 결빙 타입.**
- **해빙 히스테리시스:** freeze `temperature < 0` / thaw `temperature > 2` **비대칭 임계**(⚠ **non-negative 리터럴만** —
  §6 expr 에 단항 마이너스·음수 리터럴 없음, OQ-C RESOLVED; 0°C = 물의 어는점이라 문제없음)로
  깜빡임 방지(전이는 매스텝 즉시라 임계 gap 이 곧 히스테리시스; 신규 상태 불필요). **rec: 비대칭 °C 임계.**
- **`ice*` 지형 속성(D4):** `base_cost`·`passable`·`required_tags`·`attrs`. 언 호수/강 = **passable**(걸어감), 비용
  modest, 물의 `Swim` 게이트 제거; '미끄러움'은 tag 로 후행. **rec: passable, base_cost≈1.2, Swim 해제.**

### ICE4 — 임계/율 출처  [RESOLVED: 데이터(climate.yaml/terrain.yaml)]
- **freezeC/thawC 임계**는 별도 Config/balance 필드가 **아니라** 결빙·해빙 전이규칙 `when` 안의 **숫자 리터럴**
  (`temperature < 0` / `temperature > 2`)이다 — 기존 `moisture < 0.15` 전이와 동일(D10). °C(CA3 상속).
  **⚠ non-negative 리터럴만** 가능(§6 expr = 단항 마이너스·음수 리터럴 없음, OQ-C RESOLVED) → 결빙점은 `< 0`
  으로 표현(0°C = 물의 어는점). 음수 임계가 필요하면 `0 - N` 뺄셈형을 써야 하나 P1 은 불필요.
- **`ice_type`** 만 유일한 신규 `balance` 필드(→ `climate.Config.IceType`); **누락 시 "" = 얼음 비활성**(자동 default 없음).
- **`ice` 지형 속성**(passable·base_cost·attrs)은 `content/terrain.yaml`, 색은 frontend `TERRAIN_STYLE`.
- (IceAccumRate 등 율은 path-A 눈 크러스트 소관 = 후행 park.) **rec: 확정(데이터).**

### ICE5 — 렌더  [RESOLVED: (b) terrain 색 경로; (a) 눈 크러스트=park]
- **물→얼음(b):** `ice_lake`/`ice_river` = `TERRAIN_STYLE` 창백한 청백색 → **기존 terrain 색 경로로 렌더**(신규 렌더
  메커니즘 0; 3D hex 색도 자동). **rec.**
- **눈→얼음(a):** `IceCover` 용 스프라이트/워시 변형 필요(CS5 "지면 레이어 없음"과 상충 → 재검토). 후행.

### ICE6 — 결정성/직렬화  [RESOLVED: 기존 델타 + FrozenFrom per-cell 직렬화]
- **(b) 물결빙** = 기존 `Transition`/`TerrainOverrides` 델타 스트림(이미 구축, **신규 계약 0**); freeze/thaw 타이밍 결정적.
- **(a) `IceCover`** = `SnowCover` 와 동형 신규 스칼라(climate.State + renderframe + persist + types.ts, CS2b 방식 재현).

### ICE-M — 빌드 순서 (leaf-first; Codex 실행용. 각 단계 독립 테스트, SPEC 조항이 정본)
> **교차검증 완료(2026-07-14):** contract 체인 = climate.Step 이 sentinel 을 resolve → `Transition.To` 는 항상 실제
> id → world `env.go runClimateEnv` 의 **기존** `nav.SetTerrain(To)` + `pendingTerrainFrame`(terrain_delta) 경로가
> **무변경으로 동작**(단, To 가 sentinel 이면 `navmap.TerrainID("__origin__")` → SetTerrain **panic**, 그래서 Step resolve
> 가 필수·AC 로 가드). world 의 유일한 얼음 변경 = digest `FrozenFrom`. config 타입 정합: `TransitionRule.To`=`core.Tag`,
> `climate.OriginTerrain`=`core.Tag`(alias) → 비교 OK. frozen_from 은 **snapshot digest(§10) 전용** — `ClimateView`(§2 ambient
> hash)·`WorldFrame`(§4) 엔 안 나감(얼음 렌더는 기존 `terrain_delta` 의 terrain 타입 변화로).

1. **`engine/env/climate` (L1 leaf, 무의존) — 메커니즘.** `climate.go`: `CellState.FrozenFrom`·`Config.IceType`·
   `const OriginTerrain navTerrainID="__origin__"`(export). `step.go`: 전이 루프에 freeze/thaw 3분기(climate SPEC §Step).
   단위테스트 = climate SPEC AC(freeze 캡처 / thaw resolve / 빈 FrozenFrom 무발화 / 히스테리시스 / resume / sentinel 미저장).
   **climate 골든 재기준**(Cells digest 에 FrozenFrom). `go test ./engine/env/climate/`. **他모듈 불요.**
2. **content + schema (데이터).** `content/terrain.yaml`: `ice`(passable, base_cost≈1.2, `required_tags:[]`, attrs).
   `content/climate.yaml`: `{lake→ice, river→ice when "temperature < 0"}` + `{ice→__origin__ when "temperature > 2"}`
   (freeze < thaw 비대칭; **non-negative 리터럴만** — OQ-C) + `balance.ice_type: ice`. `content/schema/climate.schema.json`: `balance.ice_type` **optional**.
3. **`platform/config` — 로드·검증.** `world_content_types.go`: `Balance.IceType string yaml:"ice_type"`. `world_content.go`:
   `climate.Config{…, IceType: core.Tag(cd.Balance.IceType)}`. `world_content_rules.go buildClimateRules`: `to==climate.OriginTerrain`
   면 terrain 교차검증 **skip**; sentinel 사용 시 `ice_type` set + freeze(`to==ice`) 규칙 존재 검증. `go test ./platform/config/`.
   **의존: 1(OriginTerrain/IceType) + 2(content).**
4. **`engine/world` persist digest.** `state_env.go` `climateCellDigest`: `FrozenFrom navTerrainID json:"frozen_from"` +
   capture(`gcs.State.FrozenFrom`)/restore(`CellState{…, FrozenFrom}`) — **기존 Moisture/Temperature/Terrain 패턴 그대로 미러**.
   resume round-trip 테스트에 언 셀 포함. `go test ./engine/world/`. **의존: 1. runClimateEnv 브리지는 무변경.**
5. **frontend — 색 한 줄.** `manifest.ts TERRAIN_STYLE.ice`(청백색). `npx vitest run`+`npm run build`. **독립**(누락돼도 TERRAIN_DEFAULT).
6. **활성화 + 골든(통합).** 한랭기 fixture 로 lake/river 결빙→navmap 리루트→terrain_delta `ice`→해빙 origin 복원 검증.
   **의도적 골든 재기준**: 언 물=passable 로 바뀐 world/pathfind 골든(map M4 동형). full `go test ./...`+`npm run build`.
- **불변 재확인:** D12(전이 결정적·고정순서)·D10(규칙·타입=content)·D4(비용=base-cost). navmap `SetTerrain`(M1)·`TerrainOverrides`(M5)
  재사용, 신규 IO 0. (a) 눈 크러스트(ICE2/ICE5a/ICE6a) 는 별도 후행 phase.

## 2. Phases — (각 phase 독립 shippable + 테스트 + 결정성 골든; `map-plan.md M1~M5` 양식)
> 빌드순서: `engine/env/climate`는 L1 leaf(core+rng)라 `engine/space/navmap`과 같은 stage 2에서 독립적으로 만들 수 있다.
> 와이어링(주기·SetTerrain 브리지)은 `world`(stage 7)에서, 콘텐츠 로드는 `platform/config`(stage 8)에서 합류한다.
> **핵심 안전 레버:** M1~M3는 outcome-중립(climate-off, `Rules` 비어있음 → 전이 0) → 기존 골든 불변. M4에서만 의도적 재기준.

### M1 — navmap `SetTerrain` + SPEC (outcome-중립)  ✅ SPEC 갱신 완료
- `engine/space/navmap`: `SetTerrain(cells, TerrainID)` + `TerrainOverrides()` 구현(apply-phase, world-소유, 정렬셀, `StampFootprint` 동형). 미정의 type panic.
- **불변:** 누가 `SetTerrain`을 호출하기 전까지 base-cost 레이어 = `New` 레이아웃 → 기존 terrain/pathfind 골든 그대로. `TerrainOverrides()`는 빈 슬라이스.
- 테스트: 전이 셀만 cost/Passable/RequiredTags 변경(이웃 불변), footprint 독립, `TerrainOverrides` sparse+D12정렬, pre-activation 바이트동일 회귀, unknown-id panic, 결정성 골든.
- 출하 단위: navmap만. world/climate 와이어링 없음 → 시뮬 거동 불변.

### M2 — `engine/env/climate` 순수 transform (강우→Moisture→Temperature, 전이표) — climate-off 기본
- `engine/env/climate` 구현: `New` / `Step(prev, Forcing, Rules, rng) → (next, []Transition)` / `Rules.Eval` / `Cells()` / `Rain()`.
- 강우 모델(1a) 전체: `PRain` 누적, 10일 기대 보정, 30일 hard cap, 2~12h uniform 지속, `Moisture` 적분, 증발 = `EvapBase + EvapTempScale·Temperature`.
- 기온 모델(1b): 일주기 곡선(`worldtime` `HourOfDay` 파생 forcing) − 우천 강하, [0,1] 클램프.
- 전이표: `Rules.Eval`(from-type별 ordered, first-match, §6 Formula bool over `moisture`/`temperature`).
- **출하 시 climate-off:** `Rules` 비어있음 → `Step`은 전이 0 emit. 단독 단위·결정성 골든(seed→바이트동일 `Cells()`+`Rain()`+전이) + resume 불변. world/navmap 미접촉 → 시뮬 거동 불변.
- 의존: `core`(Formula), `rng`. **navmap·worldtime import 금지**(가드).

### M3 — `world` 와이어링 (cadence + navmap 브리지) — 여전히 outcome-중립(Rules 비어있음)
- `world`가 `climate.State` 소유. `tick % 60 == 0`에 `Forcing`(=`worldtime.Clock`에서 `HourOfDay`/`AbsHour`)를 만들어 `climate.Step(prev, f, rules, fork)` 호출(per-step seeded fork, D12).
- 반환 `[]Transition`을 apply 단계에서 처리: `GridCell` → `[]navmap.Cell` 매핑(coarse square→fine hex, hex-grid.md) 후 정렬셀로 `navmap.SetTerrain(cells, To)` 직렬 호출(world가 유일 변이자).
- climate State는 plan-phase snapshot에서 격리(navmap.Snapshot 동형) — `Step`은 `prev` 불변, `next`만 교체.
- **Rules 여전히 비어있음** → 전이 0 → 기존 world 골든 불변(outcome-중립 회귀 가드). 와이어링·매핑·cadence·fork 결정성만 검증.
- `platform/config`: `content/climate.yaml` 로드/검증(`climate.schema.json`) → `climate.Rules`+`climate.Config` 컴파일, `from`/`to`를 `terrain.yaml`과 교차검증, `when` 피연산자를 §6 Formula로 검증(D10). resume를 위해 `climate.State`를 snapshot에 직렬화(data-contracts §6).

### M4 — 활성화 (실 전이표) + 골든 의도적 재기준
- `content/climate.yaml`에 실제 전이 규칙 채움(예: `forest → swamp when moisture > 0.7`, `swamp → forest when moisture < 0.2`). 장마/가뭄 시나리오 픽스처.
- 비가 오래 오면 숲이 늪으로(통과비용↑/`Swim` 게이트), 가뭄이면 늪이 마름 → 길찾기가 동적으로 리루트(`pathfind`는 live snapshot 읽음 → 다음 틱 자동 반영).
- **이 phase에서만** 영향받는 world/navmap/pathfind 골든을 의도적 재기준(map M2 재기준 동형). 시나리오: "장마 N일 → 특정 corridor 통과불가 → 우회 경로 비용↑" end-to-end.
- 결정성 불변: 같은 seed+content → 전이 타이밍·셀·리루트까지 바이트동일.

### M5 — 직렬화/스트림 + 렌더
- `data-contracts.md §6`: climate 필드 = coarse Moisture/Temperature grid **periodic full** + 지형 type 변화 **sparse delta**(`TerrainOverrides()` = `cell, from→to`), `wear`와 동일 패턴. full-vs-delta cadence 결정(map-plan §6 open과 공유).
- `platform/persist`: climate.State(grid+RainProcess) + navmap TerrainOverrides 직렬화/Redis/Postgres.
- frontend: 동적 지형 재렌더(숲↔늪 색 전이), 선택적으로 강우/습도 오버레이. 기존 terrain region 렌더 확장(map-plan M5와 합류).

### (park, frontier) — design.md §5/§6 확장 seam
- Temperature → path cost(agent comfort/stamina), 연속 moisture-cost 가산, **per-cell 바람장**·해·고도(§6 피연산자 데이터 추가, D10). dirty-set/부하분산 cadence(프로파일 후). 모두 새 코드 아닌 데이터/계수 확장.
- **CA1~CA3 활성화 phase(연주기·바람·apparent_temp operand)** 는 사람 RESOLVE 후 별도 M(climate)·P_fa4(fauna thermal/wind)에서 의도적 재기준 — staging 양식은 M4/flora 동형.
