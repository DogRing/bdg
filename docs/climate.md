# Climate & Dynamic Terrain — Subsystem Plan (DRAFT)

Concept & rationale: `docs/design.md §5` (동적 지형). 이 문서는 **구현 로드맵/결정 표면**이고 SPEC은 아직 없다.
관련 모듈: `engine/navmap`(지형 *상태* 보유), **신규** `engine/climate`(기후 필드·전이) 또는 `world`/`navmap` 흡수,
`engine/world`(소유·갱신 주기), `content/terrain.yaml` + **신규** `content/climate.yaml`.

## 0. Decisions locked (design.md §5 에서 확정 — 여기서 다시 결정하지 않음)
- 지형은 **상태(`Moisture` 등)를 가진다**. 기후(강우·기온)가 상태를 밀고, 임계 넘으면 **타입 전이**(가뭄→늪 마름, 장마→숲이 늪).
- 전이 규칙은 **데이터**(D4/D10), bespoke Go 함수 금지. 가능하면 **§6 수식 DSL**(`when moisture > x → type y`)로 표현.
- **다중 주기 갱신:** 건물 footprint=즉시(이벤트), `wear`=매 틱, 지형 전이=느린 bulk(`tick % N`, 고정순서 1패스). 병렬은 *read* 또는 비겹침 파티션 *write*를 고정순서 merge만. wall-clock·map순회 금지(D12).
- 직렬화: 지형은 정적-1회가 아니라 `wear`처럼 **델타 스트림**(`data-contracts.md §6`).

## 1. Open questions (구현 전 사람이 결정 — 이게 컨트롤 표면)
- **상태 입도:** cell별 `Moisture` vs region별? (경로 충실도 ↔ 메모리·스트리밍 비용)
- **기후 모델 범위:** 강우·기온만? 계절·바람·고도·일조까지? (P1 최소 vs 확장)
- **전이 표현:** terrain.yaml의 per-type 전이 규칙을 §6 DSL로 둘지, 별도 전이표를 둘지.
- **모듈 경계:** `engine/climate` 신설 vs `world`/`navmap` 확장 흡수? (architecture DAG 영향)
- **bulk 주기 N**의 기본값과, 전이가 outcome을 바꾸는 시점의 **결정성 골든 재기준** 정책.
- 지형 상태가 `bindTarget`/pathfind cost에 들어가는 경로(navmap cost 레이어 합성식).

## 2. Phases — (Open questions 해소 후 작성; 지금은 비워 둠)
> 각 phase는 독립 shippable + 테스트 + 결정성 골든. map-plan.md M1~M5 양식을 따른다.
