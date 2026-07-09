# engine/space — 공간 substrate & 길찾기

"무엇이 어디에 있고, 거기로 어떻게 가나". 연속좌표(D11)를 유지하면서 그 위에 **격자 인덱스**
(근접 해시·비용장)를 둔다 — 격자는 *색인*이지 세계가 아니다(에이전트는 칸에 스냅되지 않음).

| 모듈 | 역할 | 의존 | DAG |
|---|---|---|---|
| `spatial` | 자유좌표 + 스페이셜 해시, 반경 질의 | core | L1 |
| `navmap` | 내비 비용장(terrain base cost + sparse `wear` trail + 건물 footprint) over 격자 인덱스. `Cost`/`Passable`/`TerrainAt`/`Deposit`/`Decay`/`StampFootprint` + `SetTerrain`(climate 구동 동적지형, world 소유) + 스냅샷 | core | L1 |
| `pathfind` | navmap 스냅샷 위 결정성 A*/Theta* → 경유점 경로 + 총비용 (순수 질의, navmap 불변) | core, navmap | L2 |

규칙: `navmap → pathfind`는 단방향, pathfind는 navmap을 변경하지 않음. mutator는 `world`.
상세는 각 `<m>/SPEC.md`, 전체 DAG는 `docs/core/architecture.md`.
