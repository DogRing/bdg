# engine/kernel — 바탕 primitives

엔진 전체가 의존하는 최하위 빌딩블록. **의존이 거의 없고**(`core`는 의존 0), 다른 모든 그룹이
여기를 import한다. import 경로 예: `github.com/dogring/bdg/engine/kernel/core`.

| 모듈 | 역할 | 의존 | DAG |
|---|---|---|---|
| `core` | 공유 타입: `StatID`·`Dimension`·`Tag`·`Pred`·`Referent`·`Vec2`·`GameMinutes` + 교차 인터페이스 | — | L0 |
| `rng` | 결정성 seeded RNG 래퍼 (주입형; 전역 rand 금지, D12) | — | L0 |
| `expr` | 공유 **§6 `Formula` 평가기** — 산술/비교/논리 + 술어, 컴파일된 불변 `Program`을 추상 `Context`에 평가. `gates`/`climate`/`flora`/`decay`/`actions`/`economy`가 공유(glossary "one shared evaluator"). `core`만 의존(→ L1 leaf가 DAG 깨짐 없이 재사용) | core | L0 |
| `worldtime` | tick ↔ game-minute 변환(12× 스케일) | core | L1 |

규칙: 여기 모듈은 IO·wall-clock·전역 상태 없음. 상세는 각 `<m>/SPEC.md`, 전체 DAG는 `docs/architecture.md`.
