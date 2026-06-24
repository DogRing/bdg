# Climate & Dynamic Terrain — Subsystem Plan (DRAFT)

Concept & rationale: `docs/design.md §5` (동적 지형). 이 문서는 **구현 로드맵/결정 표면**이고 SPEC은 아직 없다.
관련 모듈: `engine/navmap`(지형 *상태* 보유), **신규** `engine/climate`(기후 필드·전이),
`engine/world`(소유·갱신 주기), `engine/worldtime`(밤/낮·시각), `content/terrain.yaml` + **신규** `content/climate.yaml`.

## 0. Decisions locked (design.md §5 에서 확정 — 여기서 다시 결정하지 않음)
- 지형은 **상태(`Moisture` 등)를 가진다**. 기후(강우·기온)가 상태를 밀고, 임계 넘으면 **타입 전이**(가뭄→늪 마름, 장마→숲이 늪).
- 전이 규칙은 **데이터**(D4/D10), bespoke Go 함수 금지. 가능하면 **§6 수식 DSL**(`when moisture > x → type y`)로 표현.
- **다중 주기 갱신:** 건물 footprint=즉시(이벤트), `wear`=매 틱, 지형 전이=느린 bulk(`tick % N`, 고정순서 1패스). 병렬은 *read* 또는 비겹침 파티션 *write*를 고정순서 merge만. wall-clock·map순회 금지(D12).
- 직렬화: 지형은 정적-1회가 아니라 `wear`처럼 **델타 스트림**(`data-contracts.md §6`).

## 1. Open questions — **ALL RESOLVED** (사람 확정; module SPEC 작성 가능)
> 게이트 시운전(spec-architect 열거 → 사람 결정)으로 닫음. SPEC/구현은 이 항목들을 위반하면 안 됨.

- [RESOLVED: (b) coarse climate grid] 상태 입도/저장 — 넓은 단위 climate grid에 보관(navmap cell 여러 개를 묶음), 경로 cost로 내릴 때만 navmap cell로 매핑. 연속좌표·D11과 충돌 없음, bulk 스캔·델타 스트림이 가벼움.
- [RESOLVED: 추천 오버라이드 — Rain + Day/Night + Temperature] P1 forcing 범위 — **세 가지**: ① 강우(`Rainfall`, 아래 모델), ② 밤/낮(신규 forcing 아님 — `worldtime` 시각에서 파생), ③ 기온(`Temperature` 상태 = f(시각, 강우); 비 오면 ↓). **park(나중):** 계절, 바람, 해/일조, 고도. 추가 forcing/상태는 §6 수식 피연산자로 데이터 추가(D10)해 확장.
- [RESOLVED: (c) 틱 기반 함수 + fixture] forcing 생성 — 라이브는 `tick` 파생, 골든/시나리오는 fixture 주입(wall-clock 금지, D12). 단 **강우는 사인이 아니라 seeded 확률 과정**(아래 "강우 모델").
- [RESOLVED: (b) `climate.yaml` 전이표] 전이 규칙 위치 — from-type × 조건 → to-type 전이표를 `content/climate.yaml`에. 로드 시 미정의 type 검출(D10), 기후 데이터의 단일 소유처.
- [RESOLVED: (a) 신규 `engine/climate`] 모듈 경계 — forcing→상태→전이 결정 = 순수 함수 모듈. `navmap`은 cost field 책임만, `world`가 *언제* 돌릴지(주기) 소유(D5형 관심사 분리).
- [RESOLVED: (a) `core`-only L1 leaf] DAG 위치 — `engine/climate`는 core만 의존(입력: 상태+forcing+규칙 → 출력: 새 상태 + 전이 셀 목록). navmap 기록은 `world`가 apply 단계에서 수행(navmap "world가 유일 변이자" 불변식 보존).
- [RESOLVED: (a) `SetTerrain` 추가 — **사람 승인**] navmap 기록 IF — navmap SPEC의 "지형 base-cost 레이어 immutable"을 **의도적으로 완화**, `StampFootprint`와 동형의 world-소유·직렬 `SetTerrain(cells, type)` 추가. ⚠️ **다음 spec-architect 작업: navmap SPEC §Owned Data + §Public Interface 갱신.**
- [RESOLVED: (a) TerrainID 변경만] cost 합성 — 전이는 `TerrainID`만 바꾸고 base cost가 따라옴(D4 단순). Temperature는 P1에서 path cost에 직접 안 들어감(습도/전이, 이후 agent comfort 경로). 연속 moisture-cost 가산은 park.
- [RESOLVED: (a) 고정 전역 N = 60틱(1게임시간)] bulk 주기 — 강우 확률이 매 게임시간 갱신이라 정합. 전역 동기 1패스(고정순서, D12). 부하 분산·dirty-set은 프로파일 후.
- [RESOLVED: (c) 단계별] 골든 재기준 — 도입 phase는 climate-off로 outcome-중립(기존 골든 불변), 활성화 phase에서 영향 골든만 의도적 재기준(map M1→M2+ 동형).
- [RESOLVED: (b)+(c)] 직렬화 — coarse moisture grid는 주기적 full, 지형 type 변화는 전이 *이벤트*(`cell, from→to`)로. `data-contracts.md §6` "periodic full + sparse deltas"와 정합.

### 1a. 강우 모델 (RESOLVED — 매 1게임시간 평가, seeded RNG/D12, wall-clock 금지)
- 비 그친 직후 `p_rain = 0`.
- 매 게임시간 `p_rain += seeded-random 소량`(증가량 계수 = `balance.yaml`).
- 기대 첫 강우 ≈ **10일**(240게임시간) 이 되도록 보정; **30일(720게임시간) 경과 시 무조건 강우(hard cap).**
- 강우 시작 시 지속 = uniform **2~12시간**(seeded RNG).
- 강우 중 `Moisture↑`; 그친 뒤 증발로 `↓`, **기온 높을수록 증발↑**(Temperature → Moisture 피드백).
- 설계 의도(고정): 10일 기대 / 30일 강제 / 2~12시간 지속. **튜닝 상수**(시간당 증가량 등)는 `balance.yaml`(rate only, D9 정신).

### 1b. 기온 모델 (RESOLVED, P1 최소)
- `Temperature` = f(시각[밤↓/낮↑, `worldtime`], 강우[비 오면 ↓]). 계절 보정은 park.
- 용도: Moisture 증발률 변조(위). 이후 agent comfort/stamina로 확장 가능(§6 수식).

## 2. Phases — (이제 작성 가능; 다음 spec-architect 작업)
> 각 phase는 독립 shippable + 테스트 + 결정성 골든. map-plan.md M1~M5 양식.
> 1차 초안 후보: ① navmap `SetTerrain` + SPEC 갱신(outcome-중립) → ② `engine/climate`(강우→Moisture, 전이표) climate-off 기본 → ③ 활성화 + 골든 재기준 → ④ 직렬화/렌더.
