# engine/mind — 에이전트 인지·결정 기구

에이전트가 *무엇을 알고/믿고/원하고/어떻게 행동을 고르는가*. `agent`(결정 루프)가 이 faculty들을
조합한다 — 즉 `agent`/`world`는 여기 밖(상위 오케스트레이터)이고, 여기 모듈은 그 재료다.

| 모듈 | 역할 | 의존 | DAG |
|---|---|---|---|
| `stats` | `Stats` 벡터 + `StatRegistry` | core | L1 |
| `needs` | need dimension 카탈로그 + rate(balance.yaml), decay, forward-roll | core, stats | L2 |
| `gates` | `Gate` 레지스트리·eval(boolean 가시성 술어 트리; §6 boolean 부분집합을 `expr`로) | core, stats, expr | L2 |
| `tom` | `Belief`(self 포함)·평판 분포·가십·초기 추정 | core, stats, rng | L2 |
| `actions` | 카탈로그·tag·effect·Duration·`Producers`; 레시피 매개 `Craft` + 지형변경 `Mine` (Materials P_m3/P_m4) | core, stats, gates | L3 |
| `values` | Value 맵·Standing·Salience·appraisal | core, stats, needs, tom | L3 |
| `perception` | 3감각(Sight LoS/Smell/Hearing) + flora-shade LoS 감쇠 (`flora` import 안 함; world가 `Shade` 주입) | core, spatial | L3 |
| `planner` | HTN + GOAP, forward-sim, budget, gate 적용, **tag 파생 cost**; `recipe_mediated` 행위에 recipe 바인딩 | core, actions, gates, needs, values, (navmap/pathfind cost) | L4 |

규칙: `values → tom` 단방향(tom은 values 모름). 미래 `fauna`는 이 스택을 **안** 돌리고 일부만
재사용(축소 루프, `docs/plans/fauna.md`). 상세는 각 `<m>/SPEC.md`, 전체 DAG는 `docs/core/architecture.md`.
