# World Integration — 통합 배선 (env·fauna·scent → world) — Subsystem Plan

Concept & rationale: `docs/design.md §5`(연속좌표·격자=인덱스·동적지형)·`§6`(공유 §6 평가기)·`§7`(object-mortality),
`docs/data-contracts.md`(직렬화/Redis/SSE), 그리고 잇는 모듈 SPEC들:
`backend/engine/world/SPEC.md`(+`SPEC-tick.md`/`SPEC-emergent.md`, 이미 READY),
`backend/engine/env/{climate,flora,decay}/SPEC.md`·`backend/engine/fauna/SPEC.md`·`backend/engine/space/{spatial,navmap,scent}/SPEC.md`,
`docs/{climate,flora,fauna,materials,resources,world-gen}.md`(전부 메커니즘 RESOLVED).
이 문서 = **모듈을 잇는 *통합* 결정 표면** — module SPEC 아님. 아래 W1-W9는 **새 메커니즘이 아니라 배선 선택**(경계·격자·cadence·apply순서·직렬화).
게이트: spec-architect/implementer는 자기에게 태그된 OPEN이 남아 있으면 그 phase를 **시작 거부**하고 OPEN을 main session에 반환한다(추측 금지).

관련 모듈: `engine/world`(orchestrator·sole mutator), `engine/env/{climate,flora,decay}`(pure Step), `engine/fauna`(pure Step),
`engine/space/{spatial,navmap,scent}`(인덱스), `platform/config`(content compile·bounds 주입), `platform/persist`/`platform/api`(직렬화·SSE),
`backend/tools/worldgen`(fixture 산출).

---

## 0. Decisions locked (상류에서 이미 확정 — 재논쟁 금지)

1. **world = 유일 mutator (D12).** climate/flora/decay/fauna는 전부 **pure Step → delta/intent 반환**, world가 apply. 의존성 역전으로 DAG 사이클 0 (env·fauna는 world를 import 안 함).
2. **틱 4단계 골격 유지.** `read(snapshot) → plan(parallel, read-only) → collect intents → apply(serial, 정렬 ObjectID)` (SPEC-tick.md). env-phase/scent 구동은 이 골격 *안에* 끼운다 — 골격 자체는 안 바꿈.
3. **climate→navmap 단방향.** climate는 `[]Transition` 반환만, world가 `navmap.SetTerrain` 호출(climate는 navmap import 안 함, RESOLVED #6). scent도 동일(world가 deposit/spread/commit 구동).
4. **scent = world 소유 공유 인덱스**(F36 승격, `engine/space/scent`). emitter `scent:<channel>` 태그(flora/fauna/decay)에서 world가 침착, fauna(+후일 perception)가 read. **스칼라 강도**(F21 revised).
5. **연속좌표 불변(D11).** agent/animal `Pos`는 무한 연속 float; **격자는 인덱스**(스냅 금지). 단 climate/navmap/world-gen은 *유한 사각형*에 격자를 깐다 — agent는 그 안/밖 어디든 연속(아래 W1이 이 경계를 정의).
6. **fixture = 런타임 생성 0 (D12).** world-gen은 author-time 생성기(`tools/worldgen`), engine은 fixture load만. 시나리오 손-fixture와 동일 형식.
7. **°C 전환 확정(CA3).** `temperature` operand = 실제 °C(클램프 없음), `moisture`=[0,1]. 소비자 §6 임계는 각 모듈 활성화 시 °C로 재기준(아래 §3 FLAG).
8. **fauna/flora 활성화 = 단계적 re-baseline.** 도입은 OFF(중립)로 골든 유지, 활성화는 의도된 후속 phase(climate/flora/fauna M-staging). 통합 배선은 OFF 중립을 깨지 않는다.
9. **수치 튜닝 외부화 = `content/world.yaml`(신규, 사람 2026-06-27).** 모든 world 기하/격자/속도/크기/감각/렌더 값을 한 파일로 모음(튜닝 표면, D10; 미세조정은 후속). bounds는 fixture가 override(W1). 기존 runtime knob(spatial_hash_cell·difficulty·backup)은 `balance.yaml world.*` 유지; `world.yaml grids.navmap_cell_size`는 그 `spatial_hash_cell`을 미러(동기 유지). schema = `content/schema/world.schema.json`(WI-P0). 값들은 **상호 제약**(특히 `scent_cell_size ≥ max_speed × scent_spread`) — 편집 시 인라인 불변식 유지.

---

## 1. Open questions (W1-W9 — 사람만 RESOLVE)

각 항목: 옵션 + 근거 + **rec** + 게이트(차단 여부·해당 phase). 상태는 `OPEN` 또는 `RESOLVED: <답>`.

### W1 — 월드 경계(WorldMin/Max) 출처 · **🔴 차단(WI-P0)** · `RESOLVED: rec (사람 2026-06-27); 수치→content/world.yaml`
climate `Config.WorldMin/Max`+`GridCols/Rows`, navmap `Config.Min/Max`, world-gen 출력이 **공유할 유한 사각형**이 현재 어디에도 없다(agent 세계는 무한, spatial hash 경계 없음). 어디서 정의하나?
- (a) **fixture가 권위** — world-gen/시나리오 fixture가 bounds를 싣고, world가 climate·navmap에 같은 값을 주입. headless 테스트용 기본값만 `balance.yaml world.bounds`.
- (b) balance.yaml 단일 상수(`world.min/max`)에서 전적으로.
- (c) 첫 틱에 배치된 entity AABB에서 동적 산출.
- **rec: (a)** — world-gen이 지형격자를 만드는 주체이므로 bounds도 거기서 나오는 게 자연스럽고, climate/navmap/생성기가 한 소스로 묶임(D12 재현). agent `Pos`는 여전히 경계 밖 연속 가능(경계는 *격자 범위*일 뿐, 클램프는 `CellAt`/navmap 셀 조회에서만). `[platform/config·world.New 주입]`

### W2 — 격자 해상도 스택 + climate↔navmap 매핑 소유 · **🔴 차단(WI-P0)** · `RESOLVED: rec (사람 2026-06-27); 수치→content/world.yaml`
navmap/scent/climate 셀 크기와, "한 climate 셀 = 여러 navmap 셀" 펼침 함수의 소유.
- (a) **navmap = spatial = 8.0**(이동 충실도 = 근접격자), **scent = W3에서**(maxSpeed·Ns 종속), **climate coarse**(예 grid 8×8 또는 16×16 over bounds); climate GridCell→[]navmap.Cell 펼침 = **world 소유**(climate/navmap 둘 다 "world가 한다"고만 명시됨).
- (b) navmap을 spatial보다 미세(경로 충실도↑, 메모리/스트림↑).
- (c) climate를 navmap과 동일 격자(coarse 포기 — 전이 비용↑).
- **rec: (a)** — navmap=spatial 재사용이 가장 단순(navmap OQ 기본값), climate는 coarse 유지(전이 bulk 비용↓, RESOLVED #1), 펼침은 world가 `Config.WorldMin/Max`+grid dims로 계산(통합 SPEC에 매핑 함수 명시). `[engine/world 매핑 함수 신설]`

### W3 — 속도·DT·Ns·scent셀 동시값 (cell-skip 제약) · **🔴 차단(WI-P0)** · `RESOLVED: rec (사람 2026-06-27); 수치→content/world.yaml`
scent 불변식 **cellSize ≥ maxSpeed·Ns**(바람 거슬러 한 spread 사이 셀 건너뜀 방지). 동물 `Speed`(units/DT)·`DT`·scent `Ns`(spread 주기)·scent `cellSize` 4개가 전부 미정 — **함께** 정해야 함. tick=1 game-min, day=1440틱, 1년=120일=172,800틱, climate step=60틱.
- (a) **네 값을 함께 잡아 balance `fauna.*`+`scent.*` 블록 신설**(예 maxSpeed·DT·Ns 곱으로 scent cell 하한 자동 만족하게 cell 선택). smell_radius(10)와 독립 — scent cell이 그보다 클 수 있음(read는 이웃 스캔이라 무방).
- (b) Ns=1(매 틱 spread)로 두어 cell-skip 부담 최소화(연산↑).
- (c) scent cell = smell_radius로 고정하고 maxSpeed·Ns ≤ 그 값이 되게 속도/Ns 역산.
- **rec: (a)** — 4값을 한 표로 함께 명시(예: maxSpeed≈2, DT=1, Ns=10 → cell ≥ 20). 미세조정은 balance 자동튜닝 대상(design §4). `[content/balance.yaml fauna/scent 블록 + scent.New/fauna.Cadence 주입]`

### W4 — env Step + scent 구동의 틱-루프 위치/cadence · 비차단(WI-P1/P2) · `RESOLVED: rec (사람 2026-06-27); 수치→content/world.yaml`
climate/flora/decay/fauna Step과 scent deposit/spread/commit를 4단계 어디에 끼우나. (SPEC-tick.md엔 agent 4단계만 존재.)
- (a) **apply 후 env-phase**: agent apply 완료 → (tick%60) climate.Step → climate→navmap SetTerrain → (tick%Nf) flora.Step → (tick%Nd) decay.Step → fauna는 매 틱 Step(F45 내부 cadence) → scent: predator는 매틱 deposit·food/prey는 bulk·(tick%Ns) Spread·틱말미 Commit.
- (b) env를 plan-phase 병렬에 섞음(읽기전용이라 가능하나 순서 복잡).
- (c) 별 고루틴 env 루프(D12 위험).
- **rec: (a)** — 직렬 env-phase가 D12에 가장 안전하고 "world=sole mutator"와 일치. scent Commit는 매 틱 말미(next-tick latency, F33). **fauna.Step은 매 틱 호출**(active/dormant는 fauna 내부). `[engine/world/SPEC-tick.md 확장 또는 신규 SPEC-world-env.md]`

### W5 — 합동 agent+animal apply 순서 (F41) · 비차단(WI-P2) · `RESOLVED: rec (사람 2026-06-27); 수치→content/world.yaml`
agent intent와 animal intent를 어떻게 한 순서로 apply하나(둘이 ObjectID 공간 공유 전제).
- (a) **단일 정렬 ObjectID 스트림** — agent+animal intent를 합쳐 ObjectID 오름차순 1패스 apply, 충돌=관련 stat·동률=ObjectID(D12). spatial hash는 이미 agent/object/animal 공유.
- (b) agent 먼저, animal 나중(2패스 — 결정적이나 상호작용 비대칭).
- **rec: (a)** — fauna SPEC가 이미 "combined sorted-ObjectID"로 명시(F41). agent AgentID와 animal ObjectID가 같은 문자열 공간인지만 통합 SPEC에서 못박기(스폰 시 id 발급 규약). `[engine/world apply 확장]`

### W6 — 동물 물리 크기 모델 · 비차단(WI-P2) · `RESOLVED: rec (사람 2026-06-27); 수치→content/world.yaml`
`Animal`엔 크기 필드 없음(순수 점). 상호작용(사냥 포획·풀뜯기 도달·충돌)을 어떻게?
- (a) **점 + 상호작용=action 거리/`arrival_epsilon`**(P1) — 충돌 없음, 사냥/섭식은 target 거리 게이트.
- (b) `Animal`에 body radius 추가(충돌·포획 반경).
- (c) species size를 §6 apparent_temp/speed에만 쓰고 물리 충돌은 없음(현 상태 + 명문화).
- **rec: (a)+(c)** — P1은 점 + action 거리(가장 단순, flora만 크기 보유). size는 apparent_temp(F40)·speed 변조용 *속성*으로만 존재(content `fauna:` 필드), 물리 충돌 반경은 도입 안 함. `[fauna SPEC P_fa1 주석 + content]`

### W7 — 레거시 prey 마이그레이션 시점 · 비차단(WI-P2/활성화) · `RESOLVED: rec (사람 2026-06-27); 수치→content/world.yaml`
`regen.prey_respawn=720` 타이머 객체가 신규 fauna `Animal`과 공존. 언제 전환?
- (a) **fauna-OFF 동안 레거시 prey 유지**(골든 무변), **fauna 활성화(P_fa3)에서 신규 species로 전환** + `regen.prey_respawn` 제거(berry_bush→berry_shrub와 동일 의도된 re-baseline).
- (b) 즉시 신규 fauna로 교체(현 hunger/scenario 골든 재기준 필요).
- **rec: (a)** — flora 마이그레이션 패턴과 동일(zero churn → 활성화서 일괄). `[content/balance.yaml + fauna 활성화 phase]`

### W8 — Snapshot/Redis/SSE 스키마 확장 · 비차단(WI-P4) · `RESOLVED: rec (사람 2026-06-27); 수치→content/world.yaml`
신규 월드 요소(flora·animals·climate·wind·terrain-override)가 data-contracts에 없음. (scent는 파생→비직렬화.)
- (a) **§B/C안 채택 + schema_version +1**: Snapshot에 `world.{flora[],animals[],climate{grid,rain,wind},terrain{overrides,wear}}`(periodic-full + sparse 델타); Redis 라이브 키 `flora/animal:{id}/climate/terrain/frame`; SSE `WorldFrame`(hour_of_day·day_night·temperature·apparent_temp·raining·wind·positions·deltas, god-view 제외).
- (b) 최소만(animals만 추가, climate/flora는 후속).
- **rec: (a)** — 한 번에 정의하고 bump(소비자 freeze 전이라 비용 낮음). animals/flora/decay 모두 periodic-full+델타 패턴 통일. `[docs/data-contracts.md + platform/persist + platform/api]`

### W9 — world-gen fixture 형식 = 시나리오 fixture 통일 · 비차단(WI-P0/P4) · `RESOLVED: rec (사람 2026-06-27); 수치→content/world.yaml`
- (a) **단일 포맷**: `{seed, bounds, terrain격자(base material+속성), objects[](자원 노드 포함), agents[], animals[], flora[]}` — world-gen 산출과 손-시나리오가 동일 스키마, engine은 한 로더.
- (b) world-gen 전용 포맷 + 별 로더.
- **rec: (a)** — world-gen.md §2가 이미 "시나리오 fixture와 통일" 명시. content/map.yaml 스키마 1개. `[platform/config 로더 + content/schema]`

### — 구현 직전 감사에서 surface된 신규 OPEN (W10–W14, `docs/world-readiness.md`) —
> 세계 SPEC 완성 후 "시나리오가 실제로 도는가/보이는가" 점검에서 드러난 **미설계 메커니즘·정책**. 게이트:
> 사람만 RESOLVE. (수치 비율·콘텐츠 미작성은 차단이 아니라 작업 — readiness §1/§4; 아래는 *결정*이 필요한 것.)

### W10 — 지형 비용 모델 (사용자 결정 2026-06-28) — agent=이연 / animal=`RESOLVED`
**개념 정리:** "주변 칸 물체만 계산하는 균일 그리드"는 **`spatial` hash(근접)** — *이미 존재·작동*. `navmap`은 별개(이동 비용·길). 결정을 둘로 나눔:

#### W10a — agent 지형: 주관 cost 맵(메모리) · `DEFERRED (남겨둠)`
- agent 이동 = **agent 자신의 `MoveTo`** 로; **navmap은 참고만**(전역 A* 강제 아님).
- agent마다 **주관 cost 맵 = 메모리** — 본인이 **다녀본 길 + 시야로 본 셀**의 cost를 저장(매 틱 재계산 X — cost 계산이 드무니 메모리가 낫다). **길 창발(desire-path wear)도 이 맵에 섞어 사용.** (D8 주관성: agent는 아는 곳 cost로만 판단 — 기존 `known[]` 확장.)
- **가능: 예**(sparse, 가본/본 셀만; 이동/지각 시 갱신). **단 설계 이연** — design.md §5를 "공유 navmap(참고+wear) + per-agent 주관 cost 오버레이(메모리) + agent-driven MoveTo"로 개정 필요(후속). `[engine/agent memory cost map + design.md §5 개정 — DEFERRED]`

#### W10b — animal 지형: 종별 cost 맵 · `RESOLVED: 종별 terrain_cost`
- **종마다 terrain cost 맵** — "수영 잘하는 동물 / 등산 잘하는 동물"; 같은 지형도 종별 비용 다름.
- **메커니즘:** `fauna:` 콘텐츠 `terrain_cost`(terrain→mult, 부재=1.0) + `impassable`(종이 못 드는 terrain). `TerrainSampler`=종-독립 사실(`FootprintBlocked`/`TerrainAt`/`BaseCost`); 컨트롤러가 `Rules.TerrainCost(species,terrain)` 적용 → 유효비용=`BaseCost×mult`, 차단=`FootprintBlocked ∨ !passable`. 수영종=river/sea 낮음·등산종=mountain 낮음·물고기=육지 impassable.
- ✅반영: fauna SPEC(`TerrainSampler`·`Rules.TerrainCost`·SpeciesRule) + `objects.schema.json fauna:`(`terrain_cost`/`impassable`). `[fauna+content; A* 불필요]`

### W11 — fauna 개체수 유지 = 숨겨진 respawn · `RESOLVED: (b′) 숨김 respawn (사람 2026-06-28)`
**번식(부모→자식) 안 함.** 개체수는 **respawn으로 조절** — 단 "매번"이 아니라 **시야 밖 + 미개발 야생**에서 나타남.
- **RESOLVED 메커니즘:** world가 종별 **개체수 목표**를 두고, 목표 미달 시 시드 확률/cadence로 **1마리 respawn**.
  spawn 위치 = **(1) 모든 agent의 `sight_radius` 밖(미관측) · (2) 미개발(건물 footprint/정착지 클러스터 밖) · (3)
  통과가능 야생 terrain.** 후보 셀 중 시드 결정 선택(D12). **부모·상속 없음** — 스탯 = 종 `GenSpec` 샘플(W?=animal
  GenSpec seam). 레거시 `balance.regen.prey_respawn`을 이 *조건부 placement*로 일반화.
- (기각) drive-gated 출산(부모 근접+상속)·로지스틱 자기조절 — 사용자: "번식 없음, respawn이 맞음."
- **함의:** FA6 = "개체수-조절 respawn(숨김 배치)"로 재정의; P_fa4 birth 메커니즘 **불필요(삭제)**. `repro_readiness`
  drive도 불필요(제거 가능). 남은 작업 = **respawn placement 알고리즘**(시야밖 + 미개발 후보 탐색) — world wiring.
  `[engine/world respawn; balance.regen 일반화; 새 메커니즘 거의 없음]`

### W12 — agent 시딩 밀도 정책 · `RESOLVED: (a) 클러스터 + 첫 fixture (b) (사람 2026-06-28)`
512 월드에 40 agent 균일 배치 시 간격 ≈81 ≫ sight 18 → 서로 못 봄 → 사회 시나리오 깨짐(readiness §1b).
- (a) **마을 클러스터 시딩** — agent를 ~반경 50–60 거주 구역에 모음(world-gen WG5 + `live-emergence-underseeded`); 국소 밀도 = sight 수준. (b) 첫 시나리오는 작은 집중 맵(~160–200, bounds override). (c) 균일(현재) — 깨짐.
- **rec: (a)** (+ 첫 fixture는 (b)) — fixture 시딩이 클러스터를 강제. `[fixture/world-gen WG5; 시딩 정책 명시]`

### W13 — 계절 시간압축 config · `RESOLVED: (a) 가속-연 config 노브 (사람 2026-06-28)`
연주기 = 172,800틱(10 real-day) → 계절 시나리오/라이브 관전서 안 보임(readiness §1c).
- (a) **가속-연 테스트 config** (`DaysPerYear` 축소 또는 `YearFraction` 배속 노브). (b) 장기 배치 런만.
- **rec: (a)** — 테스트/데모용 시간압축 노브(결정성 유지). `[worldtime/balance config 노브]`

### W14 — frontend 렌더 범위 · `RESOLVED: (a) 실데이터 전면 교체 (사람 2026-06-28)`
현 canvas = 하드코딩 목업(가짜 강·"1000" 라벨·env 미렌더·auto-fit 카메라)(readiness §5).
- (a) **실제 데이터로 교체**: bounds+pixelsPerUnit 카메라 · fixture terrain 렌더 · animals/flora/climate 그림 · 낮밤/날씨 앰비언트. (b) 점진(agent만 유지, env 후속).
- **rec: (a)** — bounds 카메라 + 실제 terrain + env 엔티티부터. `[frontend render phase WI-P7; RenderConfig 사용]`

### — 작은 엔진 seam (전부 `RESOLVED: rec`, 사람 2026-06-28) —
- **animal base-stat 출처:** 종별 **`GenSpec` 샘플**(agent 패리티) — `fauna:` 콘텐츠에 per-stat gen 분포 추가; `worldgen.Load`가 시드로 샘플(world.Spawn 동형). `[fauna SPEC SpeciesRule + objects.schema fauna.gen — 종 작성 시]`
- **planner terrain-Mine 바인딩:** **world apply가 actor 셀 fallback** + planner는 terrain `extract` 있는 곳서 Mine 제공(노드 없이도 `has_materials` 가능). `[SPEC-mine-terrain OQ RESOLVED]`
- **navmap 접근자(fauna TerrainSampler):** **world가 `terrainTypes` join + navmap에 `FootprintBlocked` 하나만 신설**(BaseCost/TerrainAt는 join). `[navmap + SPEC-world-fauna OQ RESOLVED]`
- **`world.WorldState` env 필드 + `RenderView()`:** **world가 render 투영 빌드**(god-view 필터 한 곳; persist는 쓰기만). `[engine/world + persist SPEC-world OQ RESOLVED]`
- **`world.EnvConfig` ↔ `config.EnvConfig` 네이밍:** **유지**(다른 패키지, 한정명 충돌 없음).

---

## 2. Phases (W RESOLVE 후 진행 — 모듈 phase 교차참조)

- **WI-P0 (config/bounds/grid — W1/W2/W3/W9): ✅SPEC+스키마 작성됨.** `content/world.yaml`(✅) + `content/schema/world.schema.json`(✅ valid) + **`backend/platform/config/SPEC-world.md`**(✅, config SPEC.md sub-spec 등재): world.yaml→`world.EnvConfig`+climate/navmap Config 조립 · fauna/flora §6 `expr.Parse` 컴파일 · climate.yaml 전이표 · **교차검증**(fauna ReadsAttrs⊆AttrOperands∪drives · scent-cell floor≥max_speed×spread · grid sync · terrain ids) · ConfigHash 확장 · optional-file 중립(없으면 env OFF). **스키마 SHAPE 전부 작성됨(2026-06-27):** `content/schema/world.schema.json`✅ · `content/schema/terrain.schema.json`✅ + `content/terrain.yaml`✅(base material soil/sand/river/mountain/sea/bare_rock + §5 attrs) · `content/schema/climate.schema.json` **CA1-3 갱신**✅(°C clamp 제거·annual_mid/amp/phase·wind_* 추가·grid geometry는 world.yaml로 이관·init 블록) · `content/schema/objects.schema.json` **`fauna:` 블록 추가**✅(actions+utility·drives·apparent_temp·speed·senses·diet + faunaAction/driveSpec/senses $defs). **남은 = 튜닝 DATA만**(fauna 종 블록·climate.yaml °C 임계 데이터=활성화 P_fa3/climate M). **seam:** 엔진모듈 `NewRules`(컴파일된 expr.Program 주입). 구현 전.
- **WI-P1 (env-phase 오케스트레이션 — W4): ✅SPEC 작성됨 `backend/engine/world/SPEC-world-env.md`.** Phase 4 뒤 직렬 env 서브페이즈: climate.Step(tick%ClimateStep)→climate→navmap `SetTerrain` 브리지(`climateCellToNavCells` world 소유 매핑)→flora.Step(tick%FloraStep, `SiteInput`=navmap+climate 샘플)→decay.Step(tick%DecayStep, env 샘플) + `WorldSnapshot.ShadeOccluders`(flora→perception) + env-OFF 중립(`InstallEnv` 미호출 시 no-op). 구현 전. (스캔/scent/fauna는 WI-P2.)
- **WI-P2 (scent+fauna 배선 — W4/W5/W6): ✅SPEC 작성됨 `backend/engine/world/SPEC-world-fauna.md`.** `InstallFauna`(opt-in, 미호출=fauna-OFF 중립); fauna.Step=**plan 단계** 매틱(snapshot=animals+committed scent+spatial+TerrainSampler+EnvSample+Cadence+DT); **결합 agent+animal apply**=단일 lexicographic id 스트림(F41/W5, 공유 id공간 `an:` 접두); animal apply=move/commit drives·stamina·vital/death(§7); scent=deposit(predator 매틱·food/prey bulk)·Spread(tick%Ns, climate.Wind)·Commit(틱말미, post-move 위치). 어댑터: EnvSample=climate.CellAt+Wind, TerrainSampler=navmap{footprint-only Passable, BaseCost}(물=고비용 통과). 구현 전. **2 plumbing seam flagged**(navmap FootprintBlocked/BaseCost 접근자, animal id 접두). carcass/Butcher/종활성=P_fa3.
- **WI-P3 (Mine terrain-driven — resources R1): ✅SPEC 작성됨 `backend/engine/world/SPEC-mine-terrain.md`**(world SPEC.md 표 등재). terrain-cell 경로 추가(노드 path와 병존, 같은 `Mine`/`tool:digging`/§6 yield): apply 타겟 해석(ore_node 있으면 노드, 없으면 actor 셀 terrain `extract`) · `content/terrain.yaml` **`extract` yield 블록**✅+terrain.schema✅(soil→clay+stone·mountain/sand/bare_rock→stone·river/sea 無) · stone 고갈≈0·clay=soil-gated · §6(Dexterity,tool quality)+per-agent fork. **seam:** planner가 노드 없이 terrain Mine 선택(rec=actor 셀 fallback). 자원/제련=content/recipe(R3-R6). 구현 전.
- **WI-P4 (직렬화/스트림/생성기 — W8/W9): ✅출력 측 SPEC 작성됨.** `docs/data-contracts.md` 확장✅(§1 flora[]/animals[]/climate{} · §2 Redis animal/flora/climate/terrain/frame 키 · §4 WorldFrame + AnimalBorn/Died·PlantSpawned/Died 이벤트 · **신규 §10 env-state 직렬화 shapes** periodic-full+델타) + **`backend/platform/persist/SPEC-world.md`**✅(persist SPEC.md sub-spec 등재): env 블록=`world.WorldState`에 탑승 · Redis 렌더키(god-view 제외) · WorldFrame SSE 투영 · scent=파생 비직렬화 · **env-OFF 바이트동일**(omitempty, 기존 골든 무변). **입력 측(W9): ✅SPEC+스키마 작성됨.** `content/schema/fixture.schema.json`(통일 fixture: seed+bounds+terrain 격자+objects/agents/animals/flora/lots; absent=OFF) + **`backend/tools/worldgen/SPEC.md`**: `Generate`(author-time seeded WG1-a→Fixture, 런타임 생성 0)·`Parse/Encode`·**`Load`(run-time: fixture→navmap/climate/flora/decay.New[같은 terrainAt→t=0 격자 정합]→`world.InstallEnv/InstallFauna`+Spawn/PlaceObject, config Rules join)**. absent-block 중립. **seam:** `world.WorldState` env 필드+`RenderView()`(engine/world) · animal base-stat source(fauna GenSpec, rec=agent 패리티) · 로더 home(main이 worldgen.Load 호출). 구현 전.
  - **프론트 SSE 계약 wire 완료(2026-06-28):** `frontend/src/types.ts`(AnimalState/PlantState/ClimateState/WorldFramePayload/RenderConfig + WorldState env 필드) + `useWorld.ts` reducer **WorldFrame 핸들러**(env 머지, god-view 없음) + `frontend/SPEC.md` 계약. **tsc 통과.** 렌더(canvas 그리기)는 미구현=데이터만 입력.

### 신규 phase (구현 직전 감사, `docs/world-readiness.md` — W10–W14 모두 RESOLVED 2026-06-28)
- **WI-P5 (agent 주관 cost 맵 — W10a): `DEFERRED`(남겨둠).** agent 이동=자기 `MoveTo`, navmap=참고만; agent마다 **주관 cost 맵=메모리**(다녀본 길+본 셀, sparse)+desire-path wear 섞음(D8, `known[]` 확장). **design.md §5 개정 후속**(공유 navmap+per-agent 주관 오버레이+agent-driven MoveTo). 그때까진 agent는 지형 무시 직선이동.
- **WI-P6 (fauna 개체수 respawn — W11 RESOLVED): ⬜ world wiring.** 번식 X. 종 개체수 목표 미달 시 **시야 밖 + 미개발 야생**에 시드 확률로 1마리 respawn(부모/상속 없음, 스탯=종 GenSpec). 레거시 `prey_respawn` 일반화. P_fa4 birth=삭제. readiness §3b.
- **WI-P7 (frontend 렌더 — W14 RESOLVED): ⬜.** bounds+pixelsPerUnit 카메라 · fixture terrain · animals/flora/climate · 낮밤/날씨 앰비언트 · 하드코딩 목업 제거. readiness §5.
- **animal 종별 지형(W10b RESOLVED): ✅반영됨** — fauna `TerrainSampler`+`Rules.TerrainCost`+objects.schema `terrain_cost`/`impassable`(수영/등산). (별도 phase 아님 — fauna 구현에 포함.)
- **콘텐츠 작성(P1, 작업):** fauna 종 deer/wolf(+`terrain_cost`/`impassable`/`gen`) · Graze/Flee/Wary · `climate.yaml` · scent 발생원 태그 · 시작 fixture(클러스터 시딩 W12·작은맵) · 가속-연 config(W13) — readiness §4/§7.

> **W1–W14 전부 RESOLVED**(W10a DEFER). P0–P4 SPEC 완료 + W10b/W11/W12/W13/W14 결정. 남은=콘텐츠 작성·활성화(°C 재기준)·WI-P5(agent 주관맵, 이연)·WI-P6/P7 구현. 수치 untuned.

## 3. Notes / flags

- **불변식:** D11(연속좌표·격자=인덱스; W1 경계는 격자범위일 뿐 agent 클램프 아님) · D12(seeded·정렬 apply·런타임 생성 0·env는 직렬 env-phase) · D2(정착/생태 = base+창발, 하드코딩 금지) · D4/D10(전이·자원·fauna §6=데이터).
- **°C 재기준(FLAG, climate SPEC서 인계):** temperature operand가 °C이므로 flora suitability·decay accel(Dm3)·fauna apparent_temp(F40)·`climate.yaml` `when:` 임계를 각 활성화 phase에서 °C로 재기준 + 골든 재기준. (통합이 아니라 각 모듈 활성화 숙제 — 여기선 추적만.)
- **glossary 신규 등재 대상:** `wind.mag`(CA2) · `apparent_temp`(F40) · `scent:<channel>`/`scent.{food,prey,predator}`(F22) · fauna drive ids(hunger/fear/thermal/fatigue/repro_readiness) · `WorldFrame`(SSE) · world `bounds`. 별도 glossary 단계.
- **문서 결함 수정 완료(2026-06-27, scent 승격/flora 2축 잔재):** scent SPEC 이전경로 노트·fauna SPEC 소유권/import-guard·perception `§6(growth)→§6(width)`.
- **교차:** `engine/world`(orchestrator) · env/fauna(pure Step) · space/{navmap,scent}(인덱스) · `data-contracts.md`(W8) · `world-gen.md`(W1/W9) · `resources.md` R1(WI-P3) · `live-emergence-underseeded`(WI-P0 시딩 레버, world-gen §6) · climate/flora/fauna M-staging(활성화 re-baseline).
- **상태:** **W1–W14 전부 `RESOLVED`**(W1-9 2026-06-27 · W10–W14 + 작은 seam 2026-06-28). W10a(agent 주관 cost 맵)만 **DEFERRED**(설계 이연, design.md §5 개정 후속). → 게이트에 OPEN 없음. 남은 = **콘텐츠 작성 + 구현**(WI-P6 respawn·P7 frontend·env/fauna 모듈·W10b 포함) + 활성화 °C 재기준. RESOLVE는 사람만(완료).
