# World Integration — W1~W14 심의 기록 (Why)

> **성격:** `docs/plans/world-integration.md`의 Open-Question **심의 전문**(W1~W14 + 작은 seam — 옵션·기각 근거·rec 논리).
> 확정값(What)은 plan §1이 권위, 배선 구현(How)은 각 WI phase의 SPEC(plan §2 표). 이 파일 = 2026-07-09
> 다이어트 때 잘라낸 **pre-diet 원문 스냅샷**(§1 전체; 이후 갱신되지 않음).

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
- **rec: (a)** — 한 번에 정의하고 bump(소비자 freeze 전이라 비용 낮음). animals/flora/decay 모두 periodic-full+델타 패턴 통일. `[docs/core/data-contracts.md + platform/persist + platform/api]`

### W9 — world-gen fixture 형식 = 시나리오 fixture 통일 · 비차단(WI-P0/P4) · `RESOLVED: rec (사람 2026-06-27); 수치→content/world.yaml`
- (a) **단일 포맷**: `{seed, bounds, terrain격자(base material+속성), objects[](자원 노드 포함), agents[], animals[], flora[]}` — world-gen 산출과 손-시나리오가 동일 스키마, engine은 한 로더.
- (b) world-gen 전용 포맷 + 별 로더.
- **rec: (a)** — world-gen.md §2가 이미 "시나리오 fixture와 통일" 명시. content/map.yaml 스키마 1개. `[platform/config 로더 + content/schema]`

### — 구현 직전 감사에서 surface된 신규 OPEN (W10–W14, `docs/archive/world-readiness.md`) —
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
