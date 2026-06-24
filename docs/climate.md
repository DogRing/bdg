# Climate & Dynamic Terrain — Subsystem Plan

Concept & rationale: `docs/design.md §5` (동적 지형). SPECs now exist:
`backend/engine/climate/SPEC.md` (신규 L1 leaf), `backend/engine/navmap/SPEC.md` (`SetTerrain` 추가).
관련 모듈: `engine/navmap`(지형 *상태* 보유 + `SetTerrain` writer), **신규** `engine/climate`(기후 필드·전이),
`engine/world`(소유·갱신 주기·navmap 브리지), `engine/worldtime`(밤/낮·시각), `content/terrain.yaml` + **신규** `content/climate.yaml`.

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
- [RESOLVED: (a) 신규 `engine/climate`] 모듈 경계 — forcing→상태→전이 결정 = 순수 함수 모듈. `navmap`은 cost field 책임만, `world`가 *언제* 돌릴지(주기) 소유(D5형 관심사 분리).
- [RESOLVED: (a) `core`-only L1 leaf] DAG 위치 — `engine/climate`는 core(+rng)만 의존(입력: 상태+forcing+규칙 → 출력: 새 상태 + 전이 셀 목록). navmap 기록은 `world`가 apply 단계에서 수행(navmap "world가 유일 변이자" 불변식 보존).
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

## 2. Phases — (각 phase 독립 shippable + 테스트 + 결정성 골든; `map-plan.md M1~M5` 양식)
> 빌드순서: `engine/climate`는 L1 leaf(core+rng)라 `engine/navmap`과 같은 stage 2에서 독립적으로 만들 수 있다.
> 와이어링(주기·SetTerrain 브리지)은 `world`(stage 7)에서, 콘텐츠 로드는 `platform/config`(stage 8)에서 합류한다.
> **핵심 안전 레버:** M1~M3는 outcome-중립(climate-off, `Rules` 비어있음 → 전이 0) → 기존 골든 불변. M4에서만 의도적 재기준.

### M1 — navmap `SetTerrain` + SPEC (outcome-중립)  ✅ SPEC 갱신 완료
- `engine/navmap`: `SetTerrain(cells, TerrainID)` + `TerrainOverrides()` 구현(apply-phase, world-소유, 정렬셀, `StampFootprint` 동형). 미정의 type panic.
- **불변:** 누가 `SetTerrain`을 호출하기 전까지 base-cost 레이어 = `New` 레이아웃 → 기존 terrain/pathfind 골든 그대로. `TerrainOverrides()`는 빈 슬라이스.
- 테스트: 전이 셀만 cost/Passable/RequiredTags 변경(이웃 불변), footprint 독립, `TerrainOverrides` sparse+D12정렬, pre-activation 바이트동일 회귀, unknown-id panic, 결정성 골든.
- 출하 단위: navmap만. world/climate 와이어링 없음 → 시뮬 거동 불변.

### M2 — `engine/climate` 순수 transform (강우→Moisture→Temperature, 전이표) — climate-off 기본
- `engine/climate` 구현: `New` / `Step(prev, Forcing, Rules, rng) → (next, []Transition)` / `Rules.Eval` / `Cells()` / `Rain()`.
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
- Temperature → path cost(agent comfort/stamina), 연속 moisture-cost 가산, 계절·바람·해·고도(§6 피연산자 데이터 추가, D10). dirty-set/부하분산 cadence(프로파일 후). 모두 새 코드 아닌 데이터/계수 확장.
