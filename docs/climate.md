# Climate & Dynamic Terrain — Subsystem Plan

Concept & rationale: `docs/design.md §5` (동적 지형). SPECs now exist:
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
- 반환 `[]Transition`을 apply 단계에서 처리: `GridCell` → `[]navmap.Cell` 매핑(coarse→fine) 후 정렬셀로 `navmap.SetTerrain(cells, To)` 직렬 호출(world가 유일 변이자).
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
