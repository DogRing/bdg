# engine/env — 살아있는 세계 (순수 Step 변환)

날씨·식생·부패. 셋 다 **동형(同形)**: `core`+`rng`+`expr`만 의존하는 **순수 결정성 Step 변환**으로,
입력을 *값으로* 받아 **델타를 반환**할 뿐 — `navmap`/`world`/서로를 import하지 않는다. 실제 쓰기
(navmap/objects/inventory mutation)는 전부 `world`가 수행(단일 mutator, D12 apply 단계).

| 모듈 | 역할 | 의존 | DAG |
|---|---|---|---|
| `climate` | 동적지형 구동: `(거친 Moisture/Temperature 격자 + 시간 forcing + 전이규칙) → (다음 상태, terrain 전이 셀)`. 강우(seeded)·기온 모델·`content/climate.yaml` 전이표(§6 boolean). `world`가 `navmap.SetTerrain` 수행 | core, rng, expr | L1 |
| `flora` | 식생 구동: `(식물 집합 + per-plant terrain/climate + flora Rules, rng) → (성장/전파/고사 델타)` + per-plant `Shade`. 연속 `Growth`, §6 suitability/성장/그늘/전파/고사/yield | core, rng, expr | L1 |
| `decay` | 부패 구동: `(부패 lot 집합 + per-lot env + decay Rules, elapsedTicks, rng) → (age/전이/변환/소멸 델타)`. 연속 `decayAge` accumulator, §6 `accel`(decay 소유), owner-agnostic lot. **Dm1–Dm5 RESOLVED — READY(expr 이후)** | core, rng, expr | L1 |

규칙: 셋 다 `world`/`navmap`/`worldtime`/`gates`/서로 import 금지 — 델타만 반환. flora의 pure-Step
형태를 decay가 그대로 미러. 상세는 각 `<m>/SPEC.md`, 전체 DAG는 `docs/architecture.md`.
