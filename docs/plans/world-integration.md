# World Integration — 통합 배선 (env·fauna·scent → world) — Subsystem Plan

Concept & rationale: `docs/core/design.md §5`(연속좌표·격자=인덱스·동적지형)·`§6`(공유 §6 평가기)·`§7`(object-mortality),
`docs/core/data-contracts.md`(직렬화/Redis/SSE), 그리고 잇는 모듈 SPEC들:
`backend/engine/world/SPEC.md`(+ sub-SPEC 표 — WI phase별 정본), `backend/engine/env/{climate,flora,decay}/SPEC.md`·
`backend/engine/fauna/SPEC.md`·`backend/engine/space/{spatial,navmap,scent}/SPEC.md`,
`docs/plans/{climate,flora,fauna,materials,resources,world-gen}.md`(전부 메커니즘 RESOLVED).
이 문서 = **모듈을 잇는 *통합* 결정 표면** — module SPEC 아님. W1-W14는 **새 메커니즘이 아니라 배선 선택**(경계·격자·cadence·apply순서·직렬화).

> **게이트 전부 완료: W1–W14 RESOLVED**(W1-9 2026-06-27 · W10-W14 + 작은 seam 2026-06-28; W10a만 DEFERRED).
> **이 문서 = 확정값(결정 기록) + phase 상태.** 옵션 전문·기각 근거 원문 = 커밋 `1f66cdc`(pre-diet).
> **배선의 구현 정본 = 각 phase의 SPEC**(§2 포인터). SPEC/구현이 §0·§1을 벗어나면 여기를 먼저 고치고 사람 승인.

관련 모듈: `engine/world`(orchestrator·sole mutator), `engine/env/{climate,flora,decay}`(pure Step), `engine/fauna`(pure Step),
`engine/space/{spatial,navmap,scent}`(인덱스), `platform/config`(content compile·bounds 주입), `platform/persist`/`platform/api`(직렬화·SSE),
`backend/tools/worldgen`(fixture 산출).

---

## 0. Decisions locked (상류에서 이미 확정 — 재논쟁 금지)

1. **world = 유일 mutator (D12).** climate/flora/decay/fauna는 전부 **pure Step → delta/intent 반환**, world가 apply. 의존성 역전으로 DAG 사이클 0 (env·fauna는 world를 import 안 함).
2. **틱 4단계 골격 유지.** `read(snapshot) → plan(parallel, read-only) → collect intents → apply(serial, 정렬 ObjectID)` (SPEC-tick.md). env-phase/scent 구동은 이 골격 *안에* 끼운다 — 골격 자체는 안 바꿈.
3. **climate→navmap 단방향.** climate는 `[]Transition` 반환만, world가 `navmap.SetTerrain` 호출(climate는 navmap import 안 함). scent도 동일(world가 deposit/spread/commit 구동).
4. **scent = world 소유 공유 인덱스**(F36 승격, `engine/space/scent`). emitter `scent:<channel>` 태그(flora/fauna/decay)에서 world가 침착, fauna(+후일 perception)가 read. **스칼라 강도**(F21 revised).
5. **연속좌표 불변(D11).** agent/animal `Pos`는 무한 연속 float; **격자는 인덱스**(스냅 금지). 단 climate/navmap/world-gen은 *유한 사각형*에 격자를 깐다 — agent는 그 안/밖 어디든 연속(W1이 이 경계를 정의).
6. **fixture = 런타임 생성 0 (D12).** world-gen은 author-time 생성기(`tools/worldgen`), engine은 fixture load만. 시나리오 손-fixture와 동일 형식.
7. **°C 전환 확정(CA3).** `temperature` operand = 실제 °C(클램프 없음), `moisture`=[0,1]. 소비자 §6 임계는 각 모듈 활성화 시 °C로 재기준(§3 FLAG).
8. **fauna/flora 활성화 = 단계적 re-baseline.** 도입은 OFF(중립)로 골든 유지, 활성화는 의도된 후속 phase. 통합 배선은 OFF 중립을 깨지 않는다.
9. **수치 튜닝 외부화 = `content/world.yaml`(사람 2026-06-27).** 모든 world 기하/격자/속도/크기/감각/렌더 값을 한 파일로(튜닝 표면, D10). bounds는 fixture가 override(W1). 기존 runtime knob(spatial_hash_cell·difficulty·backup)은 `balance.yaml world.*` 유지. schema = `content/schema/world.schema.json`. 값들은 **상호 제약**(특히 `scent_cell_size ≥ max_speed × scent_spread`) — 편집 시 인라인 불변식 유지.

---

## 1. Resolutions (W1–W14 — 전부 사람 확정; 옵션 전문 = 커밋 `1f66cdc`)

### W1–W9 (2026-06-27; 수치 → `content/world.yaml`)
- **W1 — 월드 경계(WorldMin/Max) 출처 · RESOLVED: fixture가 권위.** world-gen/시나리오 fixture가 bounds를 싣고 world가 climate·navmap에 같은 값 주입(한 소스, D12 재현); headless 기본값만 balance. agent `Pos`는 여전히 경계 밖 연속 가능(경계 = *격자 범위*일 뿐, 클램프는 셀 조회에서만).
- **W2 — 격자 해상도 스택 + climate↔navmap 매핑 소유 · RESOLVED: navmap=spatial 재사용, climate=coarse, 펼침 함수 = world 소유**(`climateCellToNavCells`, bounds+grid dims로 계산). *(후속: Q-M1이 navmap↔spatial을 다시 분리 — `docs/plans/shelter.md`.)*
- **W3 — 속도·DT·Ns·scent셀 동시값 · RESOLVED: 4값을 한 표로 함께 확정**(balance `fauna.*`+`scent.*`), cell-skip 불변식 `cellSize ≥ maxSpeed·Ns` 자동 만족; config가 로드 시 검증.
- **W4 — env Step + scent 구동 위치 · RESOLVED: apply 후 직렬 env-phase.** (tick%N) climate→SetTerrain 브리지→flora→decay; fauna.Step은 매 틱(F45 내부 cadence); scent = predator 매틱 deposit·bulk spread(tick%Ns)·틱말미 Commit(next-tick latency, F33). world=sole mutator와 일치, D12 최안전.
- **W5 — 합동 agent+animal apply 순서 · RESOLVED: 단일 정렬 ObjectID 스트림**(합쳐서 오름차순 1패스; 충돌=관련 stat·동률=ID; id 공간 공유, animal `an:` 접두).
- **W6 — 동물 물리 크기 · RESOLVED: 점 + action 거리 게이트**(충돌 없음); species `size`는 apparent_temp/speed §6 *속성*으로만 존재.
- **W7 — 레거시 prey 마이그레이션 · RESOLVED: fauna-OFF 동안 유지, P_fa3 활성화에서 신규 species로 전환**(+`regen.prey_respawn` 제거 → W11로 일반화; flora 마이그레이션 패턴 동일).
- **W8 — Snapshot/Redis/SSE 스키마 확장 · RESOLVED: 일괄 정의 + schema_version +1.** Snapshot `world.{flora[],animals[],climate{},terrain{}}`(periodic-full+sparse 델타) · Redis 라이브 키 · SSE `WorldFrame`(god-view 제외). 정본 = `docs/core/data-contracts.md §10`.
- **W9 — fixture 형식 통일 · RESOLVED: 단일 포맷** `{seed, bounds, terrain격자, objects[], agents[], animals[], flora[]}` — world-gen 산출과 손-시나리오가 동일 스키마, engine은 한 로더(`content/schema/fixture.schema.json`).

### W10–W14 (구현 직전 감사에서 surface, 2026-06-28; 감사 원문 = `docs/archive/world-readiness.md`)
- **W10a — agent 지형: 주관 cost 맵(메모리) · `DEFERRED`(남겨둠).** agent 이동 = 자기 `MoveTo`, navmap은 참고만; agent마다 **주관 cost 맵 = 메모리**(다녀본 길+본 셀, sparse) + desire-path wear 섞음(D8 주관성, `known[]` 확장). **design.md §5 개정 후속** — 그때까진 agent는 지형 무시 직선이동.
- **W10b — animal 지형: 종별 cost 맵 · RESOLVED·✅반영.** `fauna:` content `terrain_cost`(terrain→mult)+`impassable`; `TerrainSampler`=종-독립 사실, 컨트롤러가 `Rules.TerrainCost` 적용(수영종=river 낮음 등). 정본 = fauna SPEC + `objects.schema.json`.
- **W11 — fauna 개체수 유지 · RESOLVED: (b′) 숨김 respawn.** **번식(부모→자식) 안 함**(사용자: "번식 없음, respawn이 맞음"). 종별 개체수 목표 미달 시 시드 확률/cadence로 1마리 respawn — 위치 = **(1) 모든 agent sight 밖 (2) 미개발(footprint/정착지 밖) (3) 통과가능 야생**, 후보 셀 시드 결정 선택(D12). 스탯 = 종 `GenSpec` 샘플(상속 없음). 함의: FA6 재정의·P_fa4 birth 불필요·`repro_readiness` 제거 가능.
- **W12 — agent 시딩 밀도 · RESOLVED: 마을 클러스터 시딩**(반경 ~50-60, 국소 밀도=sight 수준) + 첫 fixture는 작은 집중 맵(bounds override). 균일 배치는 sight≪간격이라 사회 시나리오가 깨짐.
- **W13 — 계절 시간압축 · RESOLVED: 가속-연 config 노브**(`DaysPerYear` 축소/`YearFraction` 배속; 결정성 유지) — 테스트/데모용.
- **W14 — frontend 렌더 범위 · RESOLVED: 실데이터 전면 교체**(bounds+pixelsPerUnit 카메라 · fixture terrain · animals/flora/climate · 낮밤/날씨 앰비언트 · 목업 제거).

### 작은 엔진 seam (전부 RESOLVED, 2026-06-28)
- **animal base-stat 출처:** 종별 **`GenSpec` 샘플**(agent 패리티; `worldgen.Load`가 시드 샘플).
- **planner terrain-Mine 바인딩:** world apply가 **actor 셀 fallback**(노드 없이도 terrain `extract`로 Mine).
- **navmap 접근자(fauna TerrainSampler):** world가 terrainTypes join + navmap엔 **`FootprintBlocked` 하나만 신설**.
- **`world.WorldState` env 필드 + `RenderView()`:** world가 render 투영 빌드(god-view 필터 한 곳; persist는 쓰기만).
- **`world.EnvConfig` ↔ `config.EnvConfig` 네이밍:** 유지(다른 패키지, 한정명 충돌 없음).

---

## 2. Phases — 상태 + 구현 정본(SPEC) 포인터

> 각 phase의 배선 상세(시그니처·cadence·중립성 AC)는 **SPEC이 정본** — 여기선 scope와 상태만.

| Phase | Scope (W#) | 상태 | 정본 SPEC |
|---|---|---|---|
| **WI-P0** | config/bounds/grid/§6 컴파일·교차검증 (W1/W2/W3/W9) | ✅ SHIPPED | `backend/platform/config/SPEC-world.md` + `content/schema/{world,terrain,climate,objects,fixture}.schema.json` |
| **WI-P1** | env-phase 오케스트레이션: climate/flora/decay Step + SetTerrain 브리지 + env-OFF 중립 (W4) | ✅ SHIPPED | `backend/engine/world/SPEC-world-env.md` |
| **WI-P2** | scent+fauna 배선: 결합 apply·deposit/spread/commit·어댑터 (W4/W5/W6) | ✅ SHIPPED | `backend/engine/world/SPEC-world-fauna.md` |
| **WI-P3** | Mine terrain-driven 추출 경로 (resources R1) | ✅ SHIPPED | `backend/engine/world/SPEC-mine-terrain.md` |
| **WI-P4** | 직렬화/스트림/생성기: Snapshot·Redis·SSE + 통일 fixture 로더 (W8/W9) | ✅ SHIPPED (2026-07-03 배포) | `backend/platform/persist/SPEC-world.md` + `backend/tools/worldgen/SPEC.md` + `docs/core/data-contracts.md §10` |
| **WI-P5** | agent 주관 cost 맵 (W10a) | ⏸ DEFERRED | (design.md §5 개정 후속) |
| **WI-P6** | fauna 개체수 respawn (W11) | ✅ SHIPPED | `engine/world/respawn.go` (+ SPEC-world-fauna) |
| **WI-P7** | frontend 실데이터 렌더 (W14) | ✅ SHIPPED (game.dogring.kr 라이브) | `frontend/SPEC.md` + `docs/plans/frontend.md` |

- **W10b(종별 지형)**: fauna 구현에 포함 ✅. **콘텐츠(종 블록·climate 데이터·클러스터 시딩 fixture·가속-연 노브)**: 활성화와 함께 작성 완료 ✅ (activation-gate G1-G17).

## 3. Notes / flags

- **불변식:** D11(연속좌표·격자=인덱스; W1 경계는 격자범위일 뿐 agent 클램프 아님) · D12(seeded·정렬 apply·런타임 생성 0·env는 직렬 env-phase) · D2(정착/생태 = base+창발, 하드코딩 금지) · D4/D10(전이·자원·fauna §6=데이터).
- **°C 재기준(FLAG, climate SPEC서 인계):** temperature operand가 °C이므로 flora suitability·decay accel(Dm3)·fauna apparent_temp(F40)·`climate.yaml` `when:` 임계를 각 활성화 phase에서 °C로 재기준 + 골든 재기준. (진행 기록 = `docs/plans/activation-gate.md`.)
- **glossary:** 통합이 coin한 용어(`WorldFrame`·`bounds`·`wind.mag` 등)는 `docs/core/glossary.md` "World generation & integration" 섹션에 등재됨.
- **교차:** `engine/world`(orchestrator) · env/fauna(pure Step) · space/{navmap,scent}(인덱스) · `docs/core/data-contracts.md`(W8) · `docs/plans/world-gen.md`(W1/W9) · `docs/plans/resources.md` R1(WI-P3) · `docs/plans/shelter.md`(Q-M1 격자 분리·exposure 주입) · climate/flora/fauna M-staging(활성화 re-baseline).
- **상태: W1–W14 전부 RESOLVED, WI-P0~P4·P6·P7 SHIPPED.** 남은 것 = WI-P5(agent 주관맵, 이연)뿐. 수치 튜닝은 상시(balance/tuner).
