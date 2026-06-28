# World Generation — 월드 생성기 (terrain + 분포) — Subsystem Plan

Concept & rationale: `docs/design.md §5`(바탕재료·연속좌표·navmap 인덱스), `docs/resources.md` §0/R7(자원↔terrain affinity),
`docs/fauna.md` F18(적합도 spawn), `docs/flora.md`(suitability), 메모리 `live-emergence-underseeded`(초기 시딩 레버).
**결정 표면**이고 module SPEC 아님. **빌드는 메커니즘 뒤로**(world-roadmap 게이팅 결정 3 — 그 전엔 시나리오 = authored fixture).

> **위치(핵심):** world-gen = **author-time/init 생성기**(engine 밖, `backend/tools/worldgen`) → 주입 seed로 deterministic하게
> terrain 격자 + 자원/entity 배치를 담은 **fixture**를 산출 → **engine은 그 fixture를 load만**(런타임 생성 0, D12).
> 시나리오 손-fixture와 **동일 형식** → world-gen은 메커니즘 뒤에 와도 무방.

관련: `backend/tools/worldgen`(생성기), fixture/`content/map.yaml`(산출), `engine/world`(load·spawn), `engine/space/navmap`(terrain 격자),
`engine/kernel/rng`(seed), `resources`/`flora`/`fauna` affinity, `climate`(초기 moisture/temperature 시드 + `WorldMin/Max`).

---

## 0. Decisions locked (사람 확정 2026-06-27)

1. **접근 = 절차 물중심 파이프라인 (WG1-a).** 고도→경사→물흐름(강/호수/바다)→물근접 습도→base material→biome→자원 분포.
   (타일 조립(b)·혼합(c) 기각 — 지리 일관성·창발 구동·seed 재현 위해 절차.)
2. **hydrology = flow-accumulation (WG2-a).** 고도 위 물이 최저 이웃으로 내리흐름→누적; 누적>임계=강(river, 침식으로 고도↓),
   유출 없는 분지=호수, 고도<해수면=바다. dendritic(나뭇가지) 강 자연 발생, seed 결정성. (authored 경로·노이즈임계 기각.)
3. **생성기→fixture (WG6).** engine 밖 생성기가 fixture 산출, engine은 load만(런타임 생성 0, D12). 모듈 = `backend/tools/worldgen` (WG7, engine 아님 — config류 IO/init).
4. **자원 분포 = resources R7 affinity 가중 (WG4).** `ore_node`(coal/copper/iron/gold)∝산 terrain(유한·희소 밀도), clay∝soil, stone=base에서(도처, terrain-driven이라 노드 불필요).
5. **base material = §6/임계 규칙 (WG3).** 고도/습도/경사/해수면 위 데이터 규칙(D4/D10) → sand/soil/river/mountain/sea + 속성 벡터(design §5).
6. **초기 entity + 창발 시딩 (WG5).** agent(+Value 설정)·fauna(F18 적합도)·flora(suitability) seeded 배치. **정착·biome는 *배치*가 아니라 창발**(agent 배치만; 정착은 `Build` 창발, '도시계획' 하드코딩 금지 D2). ⚠ `live-emergence-underseeded` 교훈: Value 미설정·위협 부트스트랩 없으면 사회 창발 빈약 → **시딩 레버 명시**.
7. **결정성 (D12).** 주입 seed → 동일 월드. 생성 RNG = seeded·고정순서; 런타임 생성 0.

## 1. Pipeline (WG1-a 물중심 — 산출 단계)
1. **고도장:** seeded 노이즈(다중옥타브, 결정성) → 연속 고도 [0,1].
2. **경사:** 고도 기울기 파생.
3. **물 흐름 (WG2-a):** flow-accumulation — 셀별 물이 최저 이웃으로 흐름, 누적량 계산. 누적>임계=강(river base material, 침식 고도↓), 유출 없는 저점=호수, 고도<해수면=바다.
4. **습도:** 물(강/호수/바다) 근접 → `moisture`↑ → climate 초기 moisture 시드로 연결.
5. **base material 할당 (WG3):** §6/임계(고도·경사·습도·해수면) → sand/soil/river/mountain/sea + 속성(grainSize/moisture/temperature/depth/slope/salinity, design §5).
6. **자원 분포 (WG4):** affinity 가중(resources R7) — mountain 셀에 광물 `ore_node`(유한·희소 밀도), soil은 clay terrain-driven 자격, stone은 base에서 도처.
7. **flora/fauna/agent 초기 (WG5):** flora=suitability 가중, fauna=F18 적합도 가중, agent=거주 적합지 + **Value/위협 시딩**(live-emergence).
8. **navmap bake:** base material → navmap `BaseCost`/`Passable` 초기값(engine load 시 또는 fixture에 포함).

## 2. Remaining (빌드-시 계수 — 비차단, 새 메커니즘 아님)
메커니즘(WG1~WG7)은 전부 RESOLVED. 빌드 시 정밀화할 **데이터 계수**만 남음(OPEN 메커니즘 아님):
- 노이즈 종류/옥타브, 해수면 임계, 강 누적 임계, 침식 강도 = 생성 파라미터(데이터).
- **fixture 형식** = 시나리오 fixture와 통일 — ✅**작성됨** `content/schema/fixture.schema.json`(seed+bounds+terrain 격자+objects/agents/animals/flora/lots; 블록 absent=서브시스템 OFF) + 생성기·로더 SPEC `backend/tools/worldgen/SPEC.md`(`Generate`(author-time, WG1-a)·`Parse`·`Load`(run-time→navmap/climate/flora/decay.New→`world.InstallEnv/InstallFauna`+Spawn/PlaceObject)). 맵 크기/경계 `WorldMin/Max`는 climate `CellAt`/navmap과 정합(world.yaml bounds; fixture override).

## 3. Notes / flags
- **빌드 순서:** 게이팅 결정 3대로 **메커니즘 뒤** — expr✓/climate/flora/fauna/materials/world 후. 그 전엔 authored fixture.
- **불변식:** D11(연속좌표·격자 인덱스, 셀 스냅 금지) · D12(seeded·고정순서·런타임 생성 0) · D2(정착/biome = base material + 창발, 도시계획 하드코딩 금지) · D4/D10(base material·분포 = 데이터 규칙).
- **교차:** resources(affinity)·flora(suitability)·fauna(F18)·climate(초기 moisture/temperature 시드 + `WorldMin/Max`)·navmap(terrain bake)·`live-emergence-underseeded`(시딩 레버).
