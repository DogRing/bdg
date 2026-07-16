# Scaling — 대형 맵 연산 검증 + 종별 집단화 — Subsystem Plan

개념·근거: `docs/core/design.md §5`(연속좌표·격자 인덱스, D11), `CLAUDE.md` D11/D12.
관련 플랜: `docs/plans/world-gen.md`(생성기·fixture·WG5 배치), `docs/plans/climate.md`(coarse 그리드),
`docs/plans/flora.md`(suitability), `docs/plans/fauna.md`(F18 적합도), `docs/plans/world-integration.md`(env/fauna 설치).
메모리: `deploy-pipeline-state`, `rabbit-meadow-test-world`, `live-emergence-underseeded`.

> **목표(사용자 확정):** 맵을 아주 크게(궁극적으로 마인크래프트式 준-무한) 키우고 싶다. **그 전에 에이전트가
> 아직 없는 지금**, 생태(flora+fauna+climate+scent)만으로 연산이 어디까지 버티는지 **실측**하고, 그 부하를
> 현실적으로 만들기 위해 **종별로 대량 배치**한다. 무한(청크 스트리밍)은 이 데이터를 보고 **그 다음에** 결정.

---

## 0. Decisions locked (사람 확정 2026-07-15)

1. **목표 = 경계 대형 whole-map 3000², 청크 미채택 (SC1).** 대상은 **하나의 중세 마을 + 주변 생태계**.
   비용 실사(§1) 결과 이 규모에선 지형 메모리·climate 어느 것도 벽이 아니고(첫 벽은 flora 개체수+스트리밍,
   4k²~8k²부터), 청크의 유일한 이득("먼 죽은 배후지 스킵")이 **단일 마을엔 적용될 데가 없음** — 주변
   생태계가 바로 관심영역이라 스킵할 배후지가 없다. 청크 런타임 생성은 D12를 깨야 하므로 **미채택**.
   **D12("런타임 생성 0") 유지.** 청크의 실전 이득은 더 싼 레버로(§2 P2 레버 (b)(c): 비균일 밀도 LOD +
   뷰포트 스트리밍) 취한다. 준-무한 재검토는 §3 OQ-INF(8k²+ & 큰 배후지 생길 때만).
2. **벤치 = 파라메트릭 스윕 (SC2).** 단일 타깃이 아니라 월드 크기를 500²→1k²→2k²→4k²→8k²(+메모리 여유 시 상향)
   스윕하며 **틱시간·힙메모리·개체수·직렬화 크기**를 뽑아 곡선/knee를 찾는다. "얼마까지 커지나"에 직접 답.
3. **climate = 셀 크기 32u 고정 (SC3).** 맵이 커지면 climate 그리드 차원이 면적 비례로 커진다
   (`GridCols = round((max.X−min.X)/32)`, rows 동일). 날씨 해상도를 유지하는 대신 climate가 **O(면적)/틱**이
   됨을 감수 — 벤치가 이게 실제 병목인지 밝힌다. (현 `world.yaml grids.climate_grid_cols/rows=16`은
   512²에서 32u였음 → 고정 상수에서 **bounds 파생**으로 승격; navmap `CellAt`/climate 정합 유지.)
4. **종별 배치 = 밀도+적합도 절차배치 (SC4).** 명시 리스트 대신, fixture가 **종별 목표 밀도(개체/면적)**를
   주면 materialize-time에 flora=suitability(`flora.md`)·fauna=F18 적합도(`fauna.md`) 가중으로 **결정적**
   배치(seed+고정순서, D12). WG5 approach는 이미 RESOLVED — 신설은 fixture schema의 `density` 블록뿐(§2 P1).

---

## 1. 현재 baseline — 무엇이 무엇에 비례하나 (코드 실사 2026-07-15)

| 시스템 | 비용 성격 | 스케일 |
|---|---|---|
| `space/scent` | sparse map(활성 셀만) + 고정순서 Spread | ✅ 개체 근처만, 면적 무관 |
| `space/navmap` | sparse(`wear`/`footprint`/override) + `terrainAt` 함수 샘플러 | ✅ 베이스 지형 미저장 |
| `env/climate` | **dense** `[][]CellState`; 틱마다 그리드 확산 | ⚠️ SC3로 O(면적)/틱 (32u 고정) |
| `env/flora` | `[]Plant` + spatial-hash 이웃질의 + 번식(캐리잉캐패시티) | ⚠️ O(개체수)=O(밀도×면적) |
| `fauna` | `[]Animal` + spatial+scent 구동 | ⚠️ O(개체수) |
| `worldgen.GenerateTerrain` | **dense 배열** `n=cols*rows`(elev/moisture/flow/dist…) | 🔴 로드시 O(면적) 메모리·시간, **전체 맵 일괄** |

**실질 천장 후보 (벤치가 검증):**
- **로드 타임 / 메모리** — `GenerateTerrain`+`materialize`가 맵 전체를 dense로 한 번에 생성. 이게 시작시간·힙의
  벽이자, 준-무한과 정면충돌하는 지점(무한은 청크 지연생성 필요 → §3 OQ-INF).
- **틱당 개체수** — flora 번식이 캐리잉캐패시티까지 차면 고정 밀도에서 개체수 ∝ 면적. flora/fauna step +
  scent deposit + **스냅샷/SSE 직렬화**가 전부 여기 비례. (현 라이브 `rabbit_meadow`는 500²에 grass 4 +
  rabbit 1 초미니 생태 → 부하 미관측. SC4가 이걸 현실화.)
- **climate O(면적)/틱** — SC3 결과. 벤치가 flora/fauna 대비 상대 비중을 드러냄.

## 2. Phases (각 독립 shippable; 결정적·golden 유지)

### P0 — 생태 스윕 벤치 하니스 ✅ SHIPPED (스윕 포함)
- **에이전트 0**, env(climate/flora/decay)+fauna만 설치한 `world.World`를 fixture에서 만들고 N틱 돌려 계측.
- **구현**: `backend/tools/scalebench`(dev 툴, worldgen.Load+world.Tick). 단일 모드=단계별 stderr 라이브 타이밍+
  `-cpuprofile`; **스윕 모드 `-sweep 500,1000,…`**=크기별 bounds 오버라이드→CSV(size,flora,animals,load_ms,
  tick_avg/max_ms,heap/sys_mb). `go -C backend run ./tools/scalebench -content <dir> -sweep <list> -ticks N`.

### P1 — 종별 대량 배치 (SC4) ✅ SHIPPED
- **fixture schema**(`content/schema/fixture.schema.json`): `flora_density`/`animal_density`(map 종→개체/면적).
- **배치**(`backend/tools/worldgen/density.go`, materialize에서 호출): count = round(density·bounds면적),
  결정적(seed 고정순서, D12) — **flora=§6 carrying-capacity 가중**(accept ∝ clamp(K/KRef,0,1); K는 6종 모두
  온도-free → climate 이전 평가 가능, `flora.Rules.CarryingCapacity` 신설), **fauna=지형 passability allow-list**
  (F18; fish→물, 육생→비-심해; 기존 `TerrainCost` passable 재사용, 새 content 스키마 0). draw 순서 = terrain→
  listed→density flora→density animals(고정, D12).
- **fixture**: `backend/tools/worldgen/testdata/village_ecosystem.fixture.yaml`(3000², flora 6종·fauna 6종).
- 테스트: `worldgen/density_test.go`(불변식+결정성), `verify_ecosystem_test.go`(3000² 종별 census+타이밍).
- 밀도 계수 = 빌드-시 데이터(§3 OQ-DENS, 비차단, UNTUNED 1차값).

### P2 — 천장 리포트 + 레버 🔬 5개 O(N²) 수정, 스윕 완료
**발견 = 스케일 천장은 전부 고칠 수 있는 O(N²) hot spot이고, 메모리는 절대 벽이 아님.** 프로파일로 5개 지목→
공간 인덱스/지연정렬로 수정(전부 결과-보존; golden 통과; combatTarget은 순차·read-only라 무레이스):
- 틱 3개(fauna apply, 동물×식물 전체 스캔): `coverDensity`(→cover 전용 spatial 인덱스, floraState 포인터 키잉) ·
  `nearForageFlora` · `nearestCoverFloraID`(→`w.spatial.NearbyEntities`). **3000² 틱 7000→57ms(~123×)**.
- 로드 1개: `PlaceObject`가 삽입마다 objectIDs 전체 재정렬 → O(N²log N). **지연정렬**(dirty+`orderedObjectIDs()`,
  bulk load O(N log N)). **Load 3000² 16.3s→0.28s(57×), 6000² 284s→1.4s(205×), 8000² ~15분→2.8s**.
- fauna combat 1개: `combatTarget`이 **호출마다 전체 동물 정렬·복사**(O(N²logN), GC 주범) + 전체 스캔. 정렬 제거
  (argmin은 순서 무관) + `snap.Spatial` 근처 질의(byID 해소, engaged=byID). **8000² 틱 1030→491ms(2.1×)**.

**스윕 결과(scalebench, ticks=15, cell 5, 5개 수정 후):**

| size | flora | animals | load | tick_avg | tick_max | heap |
|---|---|---|---|---|---|---|
| 2000² | 34k | 1458 | 0.13s | 26ms | 287ms | 30MB |
| 3000² | 76k | 1458 | 0.29s | 31ms | 670ms | 60MB |
| 4000² | 136k | 2312 | 0.56s | 64ms | 1.3s | 115MB |
| 6000² | 306k | 4752 | 1.45s | 204ms | 3.7s | 237MB |
| 8000² | 544k | 8168 | 2.78s | 491ms | 7.8s | 456MB |

- **메모리·Load = 벽 아님**(8000²도 heap 456MB, load 2.8s).
- **남은 천장 = 초대형 틱 비용**: ~4000²까지 쾌적(≤64ms), ~6000² 여유(204ms), 8000²=~0.5s/틱.
- **남은 레버**(비차단, 수확 체감): tick_max 스파이크=cover 인덱스 O(plants) 전체 재빌드(flora step마다)→증분화;
  `depositFloraScent`/`depositObjectScent` + scent.Spread(grass가 scent field 포화 → O(면적)/틱); GC 할당(fauna
  step 컨텍스트); SSE 뷰포트 컬링(SC1 레버). 주의: `respawn_targets` 절대값(합 1458)→≤3000² 동물수 미스케일(픽스처).
- **결론**: **SC1(3000² whole-map) 완전 검증** — Load 0.29s·틱 31ms·heap 60MB, 청크 불필요. 여유는 ~6000²(204ms)까지.
- 남음: 상세 audit → `docs/decisions/`.

### P3 — 준-무한 아키텍처 (이연, 게이트)
- **§3 OQ-INF가 RESOLVED된 뒤에만 착수.** 청크 지연생성(seed+청크좌표→항상 동일 청크, 결정적이되
  런타임 생성) = **D12 재해석** → `design.md` 수정 + 사람 승인 선행. 미승인 상태로 착수 금지(불변식 위반=defect).

## 3. Open questions

- **OQ-INF (이연/낮춤, D-invariant 게이트)** — SC1로 **현재 미채택.** 청크 스트리밍은 (a) 월드가 8k²+ 이고
  (b) 실제로 큰 야생 배후지가 비어있거나 여러 마을이 넓은 빈 공간에 흩어질 때만 재검토. 그때 D12"런타임
  생성 0"을 "런타임 생성하되 seed+좌표로 결정적·재현가능"으로 재해석 → `design.md §5`/D12 수정 + 사람
  RESOLVE 필요. 3000² 단일 마을엔 해당 없음.
- **OQ-DENS (비차단, 빌드-시 데이터)** — flora/fauna 종별 목표 밀도 계수. 생태적으로 타당한 값
  (초식>육식, biome 적합도)은 P1 SPEC/튜닝 시 데이터로 확정. 새 메커니즘 아님(world-gen.md §2와 동일 성격).

## 4. 불변식 / 결정성 (매 페이즈 유지)
- **D11** — 연속좌표. 배치·계측 어디서도 셀 스냅 금지(격자는 인덱스).
- **D12** — 벤치·배치 RNG = injected seeded·고정순서; 런타임 생성 0(P3 전까지). map-range 로직 금지(정렬 키).
- **D2** — 종 배치는 *배치*지 정착/biome 하드코딩 아님(정착은 Agent 단계 `Build` 창발).
- **D10/D4** — 종·밀도·적합도 = `content`/fixture 데이터 + §6 수식, 코드 분기 아님.
- golden: P0는 계측이라 outcome-중립; P1(배치 변경)은 관련 fixture golden 재기준.

## 5. 교차 연결
- **worldgen ↔ scaling**: density 배치는 worldgen materialize에 얹힘(WG5 구현체).
- **climate ↔ scaling**: SC3(32u 파생)은 climate `New`가 bounds에서 GridCols/Rows 파생하도록 하는 소변경.
- **persist/SSE ↔ scaling**: 개체수↑ → 스냅샷/델타 직렬화가 실질 병목 후보(persist-consistency-upgrade 델타 검증).
- **live-emergence-underseeded**: 대량 배치는 나중에 Agent 창발 시딩과도 맞물림(지금은 생태 부하 목적만).
