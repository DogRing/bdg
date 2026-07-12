# Fauna — 동물 (축소 반응 루프) — Subsystem Plan

Concept & rationale: `docs/core/design.md §5`(연속좌표·동적지형), `§6`(Formula DSL — `engine/kernel/expr` 공유 평가기),
`§7`(생애주기 — 사망×번식, object-mortality 계열), `§9`(경제 — 무주물/소유 seam).
메뉴: `docs/plans/world-roadmap.md` 🦌 동물 섹션 + 교차연결(fauna→물질사슬·decay·flora·climate).
형제 문서(양식·패턴 출처): `docs/plans/materials.md`(FINAL recipe model), `docs/plans/flora.md`(순수-Step transform·
다중주기·종=`objects.yaml` 블록·outcome-중립 staging), `docs/plans/climate.md`(체감온도·바람 입력 계약·결정성 패턴),
`docs/plans/map.md`(navmap/직렬화/보조-색인 양식).

> **게이트 전부 완료:** §0 잠금 + §1 F1~F46 + 클러스터 6~10 **전부 RESOLVED**(사람 확정 2026-06-26 ~ 2026-07-08).
> **이 문서 = 확정값(결정 기록) + phase 로드맵.** 옵션 전문·판단 근거 = **`docs/decisions/fauna-gates.md`**
> (F25~F44 클러스터 상세·FC/M SPEC-design 전문; F1~F24 pre-resolution 전문만 커밋 `bebc643`).
> **확정 메커니즘의 구현 정본은 module SPEC:** `backend/engine/fauna/SPEC.md`(코어; 페이즈 델타 sub-spec =
> `SPEC-combat.md` FC1~FC13 · `SPEC-cover.md` M3/M4-b/M5-b · `SPEC-steering.md` M6/M7) ·
> `backend/engine/space/scent/SPEC.md`(냄새 그리드) · `backend/engine/world/SPEC-world-fauna.md`(apply/사망/feed) ·
> `backend/platform/config/SPEC-world.md`(`fauna:` content 컴파일).
> **메커니즘을 발명하면 결함이지 주도성이 아니다** — SPEC/구현이 §0·§1을 벗어나면 여기를 먼저 고치고 사람 승인.

관련 모듈: `engine/fauna`(축소 반응 컨트롤러), `engine/space/scent`(냄새 그리드 — 클러스터 8에서 승격), `engine/world`
(intent 수집·apply·냄새 bulk 패스), `engine/mind/actions`(공유 원자행위 — Hunt/Graze/Flee/Wary/Attack/Feed + agent
`Butcher`), `engine/kernel/expr`(§6), `engine/space/spatial`(근접/시야), `engine/env/decay`(사체 산물 lot 부패, **재사용**),
`content/objects.yaml`(종 = object_kind `fauna:` 블록 + 산물 item·material tag), 초기 분포 = world-gen/시나리오 픽스처.

---

## 0. Decisions locked (사람 확정 — 여기서 다시 결정하지 않음)

1. **축소 반응 루프, full agent 루프 아님.** 동물은 agent의 value/ToM/GOAP-planner 스택을 돌리지 **않는다**.
2. **사회적 동물(무리/herd/세력권)은 후행 phase로 연기.** Phase 1 fauna = 순수 축소 반응(단독 행동만).
3. **명시적 '계절' 없음.** climate가 기온/날씨/바람을 **주기**로 변동시키고, fauna는 **체감온도**(apparent
   temperature)에 훅한다 — 체감온도 = **per-entity §6 수식**(지역 climate 속성 + 동물 자신의 속성). '겨울'은
   체감온도 지속 저하로 **창발**(season enum/label 없음). 체감온도·바람은 **입력 계약**으로 취급(climate 미구현).
4. **불변식 준수:** D2(메타제도 하드코딩 금지 — 사육/길들임은 *창발*, 제도로 저작 금지), D3(behavior tree/FSM
   손그림 금지), D4(tag/§6 파생, per-animal bespoke Go 함수 금지), D10(종 = content 데이터+스키마, 코드 아님),
   D11(연속좌표; 그리드는 *색인*일 뿐, 동물을 칸에 스냅 금지), D12(결정성: seeded RNG, 고정 apply 순서, map-순회 로직 금지).
5. **산물은 *이미 설계된* 물질사슬로 합류(병렬 시스템 금지):** 고기(food/decay)·가죽·뼈(craft 입력 W7)·힘줄
   (binding W8)·젖/털 = `materials.md` FINAL recipe model의 **material-tag item**. 사체→재료는 `engine/env/decay`
   (owner-agnostic lot, Dm4/Dm5)와 Craft/extract를 **재사용**한다 — 평행 부패/추출 시스템을 발명하지 않는다.

> **현행 baseline(일반화 대상):** `content/objects.yaml`의 `prey`(`mobile:true`, `Hunt`→`raw_meat`,
> `depletes:true`, `balance.regen.prey_respawn` 타이머)는 "동물 = 떠도는 채집 객체"의 최소형이다. fauna는 이를
> **축소 반응 entity**로 일반화한다(사냥감=huntable, 포식자=움직이는 위협→Safety, 사체→재료). 위협→Safety는
> 이미 `balance.yaml threats.hostile_tags` + `needs.UpdateConditionalNeeds`(per_threat_intensity/safety_decay)
> 채널이 존재한다 — fauna는 그 채널을 *재사용*한다.

---

## 1. Resolutions — 사람 확정 (2026-06-26)

### 1.0 Resolution table
| F# | 주제 | RESOLVED |
|---|---|---|
| F1 | 의사결정 기계 | **(a) horizon-1 utility arbitration** — 매 틱 공유 `engine/mind/actions` 후보를 drives+문맥 §6로 점수화 → 최고 1개(동률 ID). multi-step plan 없음 ⇒ **planner 무의존** |
| F2 | 게이트/2채널 | **(a) 실제-스탯 단일 채널** — 동물 ToM 없음 → 시도/판정 2채널이 1채널로 collapse |
| F3 | 내부상태 | **(b) drives + Stamina + 단일 vital + Pos**; **ToM/Value/Inventory 없음**. drive ≠ agent Value(별 동기 기계, D5 분리) |
| F4 | entity substrate | **(a) 신규 경량 `Animal`** + 축소 컨트롤러 (Agent 오염 없음, D5) |
| F5 | 모듈 경계 | **(a) 신규 `engine/fauna`** (core/expr/actions/spatial/rng import) → intent, world가 유일 mutator |
| F6 | 종 콘텐츠 | **(b) `objects.yaml` `fauna:` 블록** (스탯·diet tag·산물 yield·size·drive §6·체감온도 내성 §6) |
| F7 | 먹이사슬/개체군 | **(a) 창발** — eat·death·reproduce가 base에서; 표적팅 = tag(`edible`/`prey`/diet) |
| F8 | 포식자→Safety | **(a) 기존 threat→Safety hostile-tag 채널 재사용** (포식자 = `threat:predator` 단 움직이는 entity) |
| F9 | P1 번식 | **(c) 타이머-respawn 부트스트랩** (`prey_respawn`; §7 비차단) → 창발 birth는 P_fa4 |
| F10 | 체감온도 거동 | **(a)+(b)** thermal drive bias + 지속초과 vital 손상. **FA5 RESOLVED(2026-07-09):** thermal drive = `clamp01(\|apparent_temp − comfort_temp\| / thermal_band)` — **대칭 comfort 밴드**(한랭+고온 둘 다 stress↑), `apparent_temp`(§6)는 렌더용 **°C 유지**(CA3), 새 종-블록 스칼라 `comfort_temp`/`thermal_band`; `thermal_band ≤ 0` = 중립 레버. 근거 = `docs/decisions/fauna-gates.md`(FA5). ~~climate 전 thermal-OFF~~(구: clamp01(°C)라 1로 포화·부호 반대 → 인코딩 불가였음) |
| F11 | 사체/산물 | **(a) `carcass` 객체** + owner-agnostic decay lot(Dm4) + Butcher 추출; 미추출 → `rotten_matter`(W10) |
| F12 | Butcher | **(a) 신규 extract 행위** (`tool:cutting` gate, Dexterity §6 yield; Mine/Fell 평행, non-recipe) |
| F13 | husbandry | **(a) 이연** (P1 야생만; 'tame' 플래그/상태 = D2 위반 금지) |
| F14 | locomotion | **(b) 연속 Pos + cheap steering**(pathfind 없음; terrain passability 샘플만, D11) |
| F15 | 감지 | **SUPERSEDED → F20~F24 (단일 냄새 그리드)** |
| F16 | tick/결정성 | **(a)** 매 틱 interleave + **고정 결합 agent+animal ID apply 순서**(D12); per-step rng fork. bulk cadence는 F24 |
| F17 | 직렬화 | **(a) periodic full + sparse delta**(spawn/move/die) — flora/wear 패리티 (P_fa5) |
| F18 | 초기 개체군 | 시나리오 = 픽스처; 라이브 = 적합도-가중 spawn (비차단) |
| F19 | glossary 용어 | **채택·등재** (목록 §1.2) |
| F20 | 감지 그리드 | **단일 통합 균일 그리드** (보조 색인; 동물 연속좌표 유지·칸 스냅 없음, D11) |
| F21 | 필드 값 | **이진 존재 플래그 + 바람 방향 인지 → upwind homing** (스칼라 농도 gradient 아님) → **클러스터 8에서 스칼라 강도로 개정** |
| F22 | 채널 | **(a) 다채널 `{food, prey, predator}`** (셀당 채널별 플래그); 채널 확장은 후행 phase → **FC10이 `carrion` 추가** |
| F23 | 반응 | **(a) 냄새 따라가기** — 출처 객체 정체 안 캠 (upwind=접근 / predator는 반대=flee) |
| F24 | cadence | **(a) 계층** — 이동/flee=매 틱 · 냄새 확산=Ns틱 · 체감온도=Nt틱(더 느림) · 번식=bulk; N=`balance.yaml` |

### 1.1 감지 모델 — F15 대체 (F20~F24 통합; SPEC 지침)
**단일 균일 그리드 = world 소유 보조 색인** (spatial hash·navmap cost field와 동류; 소유 모듈 = `engine/space/scent`,
클러스터 8 승격). **동물은 연속좌표 유지, 자기 위치가 속한 칸만 읽는다(스냅 금지, D11).**

1. **침착(deposit):** 냄새나는 객체/동물이 자기 칸의 **채널**(`food`/`prey`/`predator`)에 침착.
2. **확산(spread):** **바람이 downwind 칸으로 운반**(범위 확장). **바람 없음(P1, climate 전) → 국소(출처 칸 + 인접) → 단거리 감지만.**
3. **반응(read):** 동물이 자기+이웃 칸 읽음 → 채널 있으면 **바람 방향 따라 upwind steering**(food/prey 접근,
   predator flee). 바람 없어도 *어느 이웃 칸이 켜졌나*로 coarse 방향 확보(F23 따라가기, 출처 정체 불요).
4. **포식자 = 구동자:** 포식자(소수)가 **매 틱 자기 칸에 `predator` 침착**(쌈) → 인접 초식이 즉시 읽고 flee.
   나머지 채널 확산은 **bulk**(Ns틱). → 임박 회피 responsive + O(N²) 스캔 없음(2계층 그리드 불필요).
- **결정성(D12):** 침착 = apply 단계 직렬·고정 순서; 확산 = 고정순서 stencil bulk 패스(`tick % Ns`), RNG 없음;
  읽기는 score 단계 pure. **map-순회로 로직 굴리지 않음**(필드 *데이터* 갱신일 뿐 — navmap wear/terrain 패스 동형).
- **입력 계약(바람):** P1은 바람 중립(단거리), climate 출하 시 원거리+upwind homing 활성 (thermal-OFF와 동일 패턴).

### 1.2 glossary 용어 (F19 — coin 확정, `docs/core/glossary.md` 등재 대상)
entity `Animal` · `carcass` · `drive`(+ 개별: `hunger`/`fear`(→Flee)/`thermal`/`fatigue`/`repro_readiness`) ·
행위 `Butcher`/`Graze`/`Flee` · 체감온도 §6 operand `apparent_temp` · 포식자 hostile tag `threat:predator` ·
`fauna:` content 블록 · 표적 tag `edible`/`prey` · **냄새 그리드**: scent grid, scent channel
(`scent:food`/`scent:prey`/`scent:predator`), 바람 operand `wind.dir`.
> + §1.3 F42 batch(`SpeciesID`·`DriveID`·`fauna.Rules`·`hunger`/`fear`/`scent.*`/`dist.*`/`apparent_temp`/`wind.*`·
> `Heading`·`cellSize`) + 클러스터 6(`Wary`·`sight.predator`/`dist.predator`·`smell`/`sight`·`fov_arc`) + 클러스터 7
> (`is_current`·`agent.disposition`) + 클러스터 8(발생원 태그 `scent:<channel>`, `scent:carrion`).

## 1.3 SPEC-design detail — Phase-1 (F25~F44)

> **2차 게이트(2026-06-26): 전부 RESOLVED — 추천대로 사람 확정.** 아래 표가 권위(재논쟁 금지). ★ foundational =
> F26(per-action utility 수식 형태)·F27(expr↔fauna 피연산자 브리지) — F31(스키마)이 이 둘에 종속.
> 선례(발명 아님, 차용): flora `SiteInput`/`Rules`, decay `accel` Program(Dm3), Cm3 `tool:<family>.quality` operand,
> needs `UpdateConditionalNeeds`(F8 채널), spatial `cellSize`(balance), climate `fork(tick)`, navmap `wear` deposit.

| F | RESOLVED (rec) |
|---|---|
| **F25** | (c) 하이브리드 — 누적형(hunger/fatigue/repro)=rate상수(D9), 문맥결합(fear←predator scent/sight via `UpdateConditionalNeeds`, thermal←apparent_temp §6)=set-from-context; thermal P1 OFF |
| **F26★** | (a) (종×행위)당 §6 `Program` 하나, 컨트롤러 max(동률 `actions.IDs()`); dot-product/EffValue 기각(D4/D5) |
| **F27★** | (a) drive/scent/dist/apparent_temp/wind을 **소문자·dotted Attr operand**로 노출, base stat=`Stat` 유지, **expr L0 무수정**; config가 `ReadsAttrs()` 교차검증 |
| **F28** | (a) 신규 공유 `Graze`/`Flee`(+`Wary`,F43), `Hunt`/`MoveTo`/`Rest` 재사용, `Butcher`=agent action; **P_fa3 추가(골든 재기준)** |
| **F29** | open `core.Stats` + `map[DriveID]float64` + Stamina/Vital/Pos/Heading/CurrentAction; stat 단련·노화=cross-cutting(D7 읽기만) |
| **F30** | (a) 매 틱 재점수, stickiness=§6 항(명시 FSM 금지, D3) |
| **F31** | (a) 종-블록 맵 `fauna:{stats,drives,utility:{<ActionID>:§6},diet,senses,products}`; config가 `expr.Parse`+operand 교차검증 (★F26/F27 종속) |
| **F32** | (a) `cellSize`∝sense반경(balance), 셀=uint8 채널 비트셋 |
| **F33** | (a) 고정순서 stencil bulk(`tick%Ns`) + **next-tick(1틱) 지연**; ⚠ **predator 채널은 매 틱 source 셀 침착**(확산만 bulk — §1.1 막판회피 일관) |
| **F34** | (c) 바람 있으면 upwind, 중립이면 이웃-on coarse — **F44로 scent-only 한정**(food/prey homing + predator 조기경보/Wary) |
| **F35** | (a)+(c) §6(base stat) 기본속도 + fear/fatigue **+ thermal** 변조; navmap 샘플만(pathfind 없음, D11) → **R2 개정: `TerrainSampler{Passable, Cost}`, 물=고비용 통과(수영)** |
| **F36** | (a) P1=`engine/fauna` 서브모듈 소유 → **클러스터 8 개정: `engine/space/scent` 승격(world 소유)** |
| **F37** | (a) 종 products→파라미터화 `carcass` kind + `raw_meat` decay lot(Dm4); 미식→`rotten_matter`(W10) |
| **F38** | (a) Mine/Fell 평행 non-recipe extract, `tool:cutting` gate, §6(Dexterity,`tool:cutting.quality`) yield; agent action |
| **F39** | (a) `balance.regen.prey_respawn` 타이머 재사용; 창발 birth=P_fa4 |
| **F40** | (a) 종-블록 §6(climate attr[**°C** `temperature`/`moisture`/`wind.*`] + 동물 attr); P1 climate-OFF 중립 |
| **F41** | (a) 단일 read→score→intent→apply, **결합 agent+animal ObjectID 정렬 apply 순서**, `fork(tick)` |
| **F42** | F19와 batch 등재(§1.2 참조) |

### 클러스터 1~5 — F25~F42 옵션 상세 (이관·압축)
> 클러스터 1(의사결정 코어 F25~F27) · 2(entity/스키마 F28~F31) · 3(냄새 그리드 F32~F34) · 4(산물/생애주기 F35~F40) ·
> 5(통합/어휘 F41~F42)의 **옵션 전문·근거·⚠가드 원문 = `docs/decisions/fauna-gates.md`**. 위 표의 RESOLVED가 권위이며, 확정 메커니즘은
> 전부 SPEC에 구현·반영됨: fauna `SPEC.md`(Public Interface 파이프라인 + Owned Data — utility·drive·steering·
> `Animal` 필드·`fauna.Rules`), scent `SPEC.md`(그리드 shape/deposit/spread/read), `SPEC-world-fauna.md`(와이어링/apply),
> config `SPEC-world.md`(`fauna:` 파싱·operand 교차검증). 불변식 가드 요약은 §3.

### 클러스터 6 — 2단계 포식자 반응(smell↔sight) + 방향성 시야 (F43~F44) — 사람 확정 (2026-06-26)

> 사람 Phase-1 의도: "포식자 *냄새* 칸에 들면 WARY, 포식자가 *가까이* 오면(heading 기반 전방 시야) FLEE."
> 옵션 전문 = `docs/decisions/fauna-gates.md`.

- **F43 = (a) 단일 fear drive + 2입력 채널** (scent → Wary 밴드 / sight → Flee 밴드) + **신규 공유 `Wary` 행위**
  (feed 인터럽트·경계·천천히 edge away). 순서 `Flee > Wary > Graze` 는 §6 fear-band utility(연속값이 임계 넘는 것일
  뿐, wary→flee FSM 금지 — D3). 골든 churn → P_fa3 활성 시 추가. F25(fear)/§1.1 은 이 2채널로 정련.
- **F44 = (c) 하이브리드 + (c-ii) continuous bearing** — smell = omni scent 그리드(조기경보 → Wary, F34),
  **sight = spatial-hash 포식자 엔티티 질의 → 상대 bearing 이 `Heading ± fov_arc` 안이면 → Flee**(D11-정합 연속
  시야, 채널 깨끗 분리). 신규 operand `sight.predator`(1/0) + `dist.predator`. **F34 의 omni 규칙은 scent-only 로
  좁아짐**(F34↔F44 동시 확정). `fov_arc` = balance 데이터.
- **후속(M7, RESOLVED 2026-07-08):** F44 는 sight 를 **단일 최근접 포식자**로 해소했다 — 다수 포식자 동시 가시 시
  회피 벡터는 **클러스터 10 M7**(거리가중 반발 합산)이 정련(≤1 포식자는 F44 경로 byte-identical).

### 클러스터 7 — 적응형 cadence · 수영 · 성향 반응 (F45~F46 + 개정) — 사람 확정 (2026-06-27)
> R1~R5 RESOLVED. `backend/engine/fauna/SPEC.md`에 반영됨. F16/F24/F29/F30/F35/F41 개정·§1.2 glossary 추가.

- **F45 — 적응형 per-animal cadence (R1; F16/F24/F41 개정).** 동물 = DORMANT/ACTIVE 2상태. **DORMANT**: `(tick + phase(ID)) % N == 0` 일 때만 풀 재중재(N≈100 balance, `phase`=ID 파생 부하분산), 사이 틱엔 `CurrentAction` 유지 + 싼 steering. **ACTIVE**: 매 틱. **포식자 항상 ACTIVE.** **깨우기**: 매 틱 모든 동물이 자기 칸 predator-scent 비트 **O(1)** 읽음 → 켜지면 그 틱 ACTIVE(+쿨다운=balance). 결정성 = ID-phase·순수 읽기·고정순서·map-순회 금지. cadence 로직은 fauna `Step` 안(world는 매 틱 호출, apply 순서는 F41).
- **F46 — 성향(disposition) 기반 agent 반응 (R4; #4 이진판 supersede; P_fa3).** 초식 sight 가 agent 감지 → **`agent.disposition` = 부호 §6(agent 실 base stat; rec `Sociability − Aggression − Vindictiveness`, 계수=balance/content, D4)** → 양수=stay(fear 0) / 음수=flee(fear↑) via F43 fear 채널. ToM 아님(실 스탯, F3), '사냥꾼' 창발(D2). F8(포식자→agent Safety) 방향 불변. 세부 §6·operand 확정 = P_fa3.
- **F35 개정 (R2):** `TerrainSampler{Passable, Cost}` (옵션 b). **물 = 벽 아님, 고비용 통과 = 수영**(steering 진입 가능; Passable=false 는 진짜 blocker[벽/footprint]만). 수영 Stamina 소모·익사 risk(W1 동물판)는 §7/lifecycle 로 이연.
- **F29 정렬 (R3):** `Animal.Stats` = `map[core.StatID]float64` (inline; fauna 는 `stats` import 안 함, D7 읽기전용).
- **F30 (R5):** `is_current` §6 Attr operand 채택(현재 행동=1.0 / else 0.0) — ACTIVE-모드 stickiness(anti-thrash; FSM 금지, D3).

### 클러스터 8 — scent 승격 + 스칼라 강도 + 발생원 태그 (F21/F22/F36 개정) — 사람 확정 (2026-06-27)
> scent를 fauna 전용에서 **공유 월드 색인**으로 올리고 이진→스칼라로 개정. SPEC: `backend/engine/space/scent/SPEC.md`.
- **① scent 승격(F36 개정):** `engine/fauna/scent` → **`engine/space/scent`**(spatial/navmap kin, `core`만 의존). **world 소유**(fauna 아님); world가 `scent:<channel>` 태그 단 발생원에서 침착, **fauna는 읽기만**.
- **② 발생원 태그 + perception 공존:** 냄새원 = `scent:<channel>` 태그(+magnitude) — flora=`scent:food`, prey=`scent:prey`, predator=`scent:predator`, (후속) carcass/rot=`scent:carrion`. **`perception.Smell`(agent per-entity gradient)와 공존** — 같은 발생원 태그 공유(무엇이 냄새나나 1회 author).
- **F21 개정(이진→스칼라 강도):** 셀당 채널별 `float64≥0` 농도(magnitude 비례·거리/바람 falloff). `Deposit`·`Spread`=농도 diffusion·`Read`=Intensity+gradient·`IntensityAt`(F45 wake). `scent.<ch>` operand=**스칼라**.

### 클러스터 9 — 포식 전투 & 사체(Combat & Predation) — 사람 확정 (2026-07-01)
> F7(창발 eat/death)·F11(carcass=decay lot)·F22(채널 확장)을 **구체 전투 메커니즘**으로 정련. 기존엔 "Hunt→즉사"로
> 추상화돼 있어 **공격 액션·공격력·교전(engaged) 상태가 없었다** — 이 클러스터가 그 갭을 메운다. **구현 = 빌드
> 순서 phase 6b.** D2/D3/D4/D7/D10/D12 가드는 §3 준용.
- **FC1 전투 액션 = 신규 공유 2개** `Attack`+`Feed`(접근=기존 `TagSteerPrey` steer, 신규 아님). `Attack`=engage+exchange 겸용(쿨다운 게이트), `Feed`=carcass 소비(hunger↓, **durative**). 둘 다 `actions.yaml` 공유 엔트리 → horizon-1 utility로 선택(D3, 손그림 FSM 금지). 골든 churn → P_fa3 활성 시(F28/F43 동형).
- **FC2 대상/포식자끼리** — 표적 = attacker `diet` 멤버(F7 tag). **포식자↔포식자는 hunger 극단일 때만**: 별도 코드 규칙이 아니라 `Attack` utility가 hunger를 **fight-cost/위험**과 저울질(D2/D4 창발). 신규 operand **`target.threat`**(표적 위험도, 반격 예상) 노출 → 평소 predator 표적은 utility 낮음, hunger↑가 극복.
- **FC3 타이밍** — engage **시도 쿨다운 [50,100]틱**(lock 전), 성공 시 engaged; **exchange 쿨다운 [10,20]틱**마다 데미지. 둘 다 seeded `envFork` 범위추출(balance min/max). 하드타이머 아님 — 쿨다운-게이트 utility.
- **FC4 공격력/명중 = §6(D4/D7)** — `attack_power = f(Strength…)`, `hit = f(공격 Agility vs 방어 Agility)`(스탯 조합, 개별 스킬 저장 금지 — 매 틱 base 합성). 데미지 → 표적 Vital↓. content §6(speed/apparent_temp 동형).
- **FC5 반격** — prey 반격 없음(자기 Vital만↓); 포식자↔포식자(FC2)는 양쪽 각자 `Attack`.
- **FC6 이탈(disengage)** — 포식자 **stamina 저하** OR 표적이 **`disengage_range`(~2 셀) 이상** 벌어지면 lock 해제. **"포식자 스태미나가 먼저 떨어지면 멈춤"이 여기서 창발**(utility/거리 파생, FSM 아님).
- **FC7 Vital 재생/흉터** — 느린 재생(balance `vital_regen`), **완전 회복 불가**: 누적 피해가 **max Vital에 영구 소량 페널티**. 신규 Animal 상태 `VitalCap`(≤1.0, 전투마다 소량↓); 재생은 `VitalCap`까지만.
- **FC8 Feed/포식** — 표적 Vital=0 → 사망 → carcass(FC9). 포식자 `Feed`(durative) = carcass supply에서 hunger↓, **회복량 = 먹이 체격 비례**(content/§6). F11의 agent-`Butcher`(재료)와 **공존**(같은 carcass: predator=Feed/hunger, agent=Butcher/재료).
- **FC9 사체 = decay lot (F11 풀버전 확정)** — 사망 → `carcass` 객체 + **owner-agnostic decay lot(Dm4)** **런타임 생성**. decay 상태 fresh→rotting→bones→gone, 각 supply(Feed 먹이값)+transform(bones/hide/sinew, W7/W8). 미소비분 부패(W10).
- **FC10 carrion 냄새 (F22 채널 확장 확정)** — carcass = `scent:carrion` 태그 → world가 `ChanCarrion` 침착. scavenger/포식자가 `scent.carrion` operand로 homing(선택 거동).
- **FC11 공포 연동(창발)** — kill 목격/carrion 근접(피 냄새) → 주변 prey `fear`↑(F43 채널). content §6.
- **FC12 상태/결정성(D12)** — 신규 `fauna.Animal` 필드 `EngagedWith`·`NextExchangeTick`·`EngageCooldownUntil`·`VitalCap` + 직렬화. 교전=양방향 관계 → 한 apply에서 두 동물 **일관 갱신·id순**, 같은표적 충돌은 기존 combined-conflict 재사용. 모든 랜덤=seeded `envFork`.
- **FC13 전투 중 이동** — engaged면 locomotion 억제(제자리 육박); prey는 fear로 **이탈(거리 벌리기)만** 시도.

> **기빌드 모듈 수정(2026-07-01 검증) — 전부 SPEC 반영 완료:** scent(`ChanCarrion`) · decay(런타임 lot 추가 API) ·
> world(`SPEC-world-fauna.md` §Combat/death/carcass apply) · config(`SPEC-world.md` §Fauna combat content) ·
> fauna(`SPEC-combat.md` FC1~FC13). 검증 원문 = `docs/decisions/fauna-gates.md`.

---

### 클러스터 10 — 포식자-피식자 리얼리즘 (M1~M7) — 사람 확정 (2026-07-02 ~ 07-08)
> **원칙(사람 확정):** 포식자 속도를 올려서 잡지 않는다. 도망자(prey)가 포식자와 **같거나 약간 빠르게** — prey는
> **속도·은신·지형**으로, predator는 **매복·스태미나·몰이**로 생존. 목표: 측정 가능한 **~15% 포식 성공률**로 함께
> 튜닝(balance.yaml + `tools/tuner`). D2/D3/D4/D7/D10/D11/D12 가드 §3 준용(은신/지형/매복은 **창발**이어야 하며
> per-species FSM/속도·비율 하드코딩 금지). 배경: `docs/plans/scenarios-world.md` FA1~FA3, memory `live-emergence-underseeded`.
> **전부 RESOLVED.** 게이트 원문·유도 SPEC-design 전문 = `docs/decisions/fauna-gates.md`; **구현 정본 = 각 항목의 SPEC 섹션 포인터.**

- **M1 — prey-경쟁 속도 baseline (커밋 3948b86):** 포식자 속도 상향 revert. §6 speed는 공통 baseline +
  Agility 파생(prey Agility↑ → 자연히 대등~우세). 개별 종 속도 하드코딩 없음(D4/D7).
- **M2 — 추격 피로 (커밋 3948b86):** `applyAnimalFatigue` — `effort:high`(추격) 시 fatigue↑, `effort:none/low` 시
  회복. speed §6가 fatigue를 감산 → 장기 추격 시 포식자 감속(스태미나 창발, FC6 동형). rate = balance cadence.
- **맵 저작 — cover flora (2026-07-02):** `starter_village.fixture.yaml`에 숲(oak 클러스터)·덤불(berry_shrub thicket)
  저작 + `oak`/`berry_shrub`에 **`cover` 태그**(glossary 등재). 숲은 지형 타입이 아니라 flora 클러스터로 창발(D11).
- **M3 — cover 은신.** "prey가 풀숲에 들어가면 일정 확률로 ~100틱 안 보이게."
  **SPEC: fauna `SPEC-cover.md` §Cover-hiding + `SPEC-world-fauna.md` §Cover-hiding apply.**
  - M3-a 발동: `flee`(선택 steer=`flee:predator`) 중일 때만 판정. M3-b: §6 `hide_chance`(종별, D4/D7) +
    `hide_duration`=balance(기본 100틱). M3-c: sight+`scent:prey` **둘 다 제외**(완전 은신); predator가 flush 반경
    (`hidden_flush_factor`×scent cell) 진입 시 즉시 발각. M3-d 해제: 만료 ∨ engaged ∨ flush(은신 중 crouch=제자리).
  - M3-e 상태: `Animal.HiddenUntil`(직렬화 FC12 동형); roll = world apply seeded `envFork`; cover 조회 =
    `nearCoverFlora`(연속좌표, D11). M3-f cover 종: `cover` 태그 = `oak`+`berry_shrub`(조정 = content, D10).
    OFF-neutral: `hide_chance` 미저작 → 은신 0 → 골든 불변.
- **M4 — 지형 차단/회피.** "나무·강·지형이 추격을 막게." 사실 확인: fauna 이동은 **이미** per-species
  `TerrainCost`/`Impassable`을 읽음 → 강/산 방벽 = **content 튜닝**(신규 메커니즘 아님).
  - M4-a 강 비대칭(content) — 완료·커밋 `42954fa`: wolf river 1.4→3.0, bear 2.6, deer 1.5(건너 도망).
    arena hunt-success ~11–12.5%(목표 범위 내).
  - M4-b cover flora 이동 저항 — RESOLVED (i): world가 이동량을 `1 + coverDensity(pos)×종 cover_cost`로 스케일
    (감속만·낙상 없음; peaky 밀도 → 이동 중 결정적 속도 변주, RNG 없음, D12). 비대칭 = 종별 `cover_cost`(D10,
    rabbit 0.3 < deer 0.6~0.7 < wolf 1.2 < bear 2.0). OFF-neutral(cover 없음/`cover_cost=0` → resistance=1).
    **SPEC: fauna `SPEC-cover.md` §Cover speed resistance + `SPEC-world-fauna.md`.**
- **M5 — 매복·감지.** predator가 cover 은신으로 prey 시야(Flee)를 늦춰 접근 → 매복 창발. 사실 확인: fauna 감지 =
  반경+FOV(`sightQuery`), shade/LoS 미반영(perception full-LoS 동물 지각 = park) → M5-b가 cover-근접 피탐지↓로 근사.
  - M5-a prey 조기감지 = content 튜닝(기존 `senses` 필드, 신규 메커니즘 아님, D10).
  - M5-b — RESOLVED (ii): predator `Concealment = coverDensity(pos)×conceal_factor`(파생 transient, 매틱 재계산) →
    prey 유효 시야 반경 `SightRadius/(1+Concealment)`. **scent(Wary)·hunt/`combatTarget` 불변 — 시야(Flee)만 늦춤**
    (prey는 냄새로는 알되 늦게 봄). OFF-neutral(`conceal_factor=0` → byte-identical).
    **SPEC: fauna `SPEC-cover.md` §Ambush concealment + `SPEC-world-fauna.md`.**
  - M5-c 몰이(cornering): 별도 코드 없음 — 강+bounds+M4 지형비용 상호작용으로 창발 유지(D2/D3).
- **M6 — 관성·저크(juke).** 현행 이동엔 관성 없음(매 틱 즉시 회전·즉시 180°) → **heading 변화율 제한**으로 prey
  juke·predator 오버슈트 동시 창발. ⚠ ~15% 목표는 M1~M5로 달성 — M6은 리얼리즘 향상.
  - M6-a — RESOLVED (i): fauna `steerFull`에서 `±turn_rate×DT` 클램프(기존 `Heading` 상태 재사용, world 무변경).
    M6-b — RESOLVED (i): `turn_rate` = §6 `Program`(base+Agility 파생, D7 — 'nimble' 창발). M6-c — RESOLVED (i):
    heading turn-rate만(speed `accel` 캡은 후행 증분). OFF-neutral(미저작 → 무제한 → 골든 불변).
    **SPEC: fauna `SPEC-steering.md` §Turn-rate inertia.**
- **M7 — 다중 포식자 회피 벡터 (RESOLVED 2026-07-08).** prey 시야에 포식자가 둘 이상이면 모두 반영해 도망.
  사실 확인: 시야 경로만 **최근접 1마리** 반발(냄새 경로는 이미 필드 합산) → 협공(pincer) 시 2번째 포식자 쪽으로
  직진하는 결함(실제 유제류는 둘 사이 측면으로 빠짐).
  - RESOLVED (b): 시야 내 全 가시 포식자 **거리가중 반발 합산** `fleeDir = normalize(Σ (Pos−predᵢ)/distᵢ²)`
    (p=2 = fauna 상수) — 기존 `sightQuery` 순회 재사용(신규 쿼리 0)·ObjectID-정렬 고정순서 합산(D12)·pincer 측면
    회피 **창발**(D2/D3, 코너링 FSM 없음). `distPred` = 최근접 유지(fear·flush·M3 hidden 불변). OFF-neutral:
    가시 ≤1 → 기존 단일-대상 경로 **byte-identical**(골든 불변); 합산 ~0(대칭 협공) → 최근접 fallback(결정적).
    F44(단일 최근접 시야)를 다중-대상으로 정련.
    **SPEC: fauna `SPEC-steering.md` §Multi-predator flee steering (+ 코어 `SPEC.md` SENSE/STEER의 M7 포인터).**

**균형(공통):** M3~M7 착지 후 balance.yaml + `tools/tuner`로 **~15% 포식 성공률** 타겟 튜닝. 측정 시나리오: 토끼-늑대,
사슴-늑대/곰(스모크 digest 확장 or scenario fixture). 종별 성공률·평균 추격시간·개체군 안정성 로그.

---

## 2. Phases — (각 phase 독립 shippable + 테스트 + 결정성 골든; `docs/plans/flora.md §2` 양식)
> **모든 게이트(§1·§1.3·클러스터 6~10) RESOLVED ✓.** 핵심 안전 레버: P_fa1~P_fa2는 outcome-중립(종 미배치 → 거동
> 0 변화) → 기존 world 골든 불변; **P_fa3에서만** 의도적 재기준(climate/flora staging 동형).

- **P_fa1 — `engine/fauna` 축소 반응 컨트롤러 + 냄새 그리드 + fauna-OFF 출하(outcome-중립)** ✅ SHIPPED
  horizon-1 utility arbitration(drives + 실제-스탯 §6; planner/ToM 없음), cheap steering 이동, 냄새 그리드 감지,
  공유 `engine/mind/actions` 후보 점수화 → 단일 intent(동률 ID). 종 미배치 → intent 0 → 거동 불변.
- **P_fa2 — `world` 와이어링(cadence + 통합 apply 순서 + 냄새 bulk 패스 + spawn/move/die 델타)** ✅ SHIPPED
  통합 read→score→intent→**apply(고정 결합 agent+animal ID 순서, D12)**; 확산 bulk 패스(`tick % Ns`) +
  per-step rng fork + ID 발급. 종 미배치 중립 회귀 가드.
- **P_fa3 — 활성: 야생 prey+predator + 사체→재료 + 골든 의도적 재기준** ✅ SHIPPED
  `fauna:` 종 활성(activation-gate G5: deer/rabbit/goat/wolf/bear/fish) + §6 컴파일. Graze(food 냄새)·Hunt(prey
  냄새+표적팅)·`threat:predator`→agent Safety(F8)+Wary/Flee(F43/F44)·사망→carcass+decay lot→Butcher(hide/bone/
  sinew, W7/W8)·미추출 부패(W10). 이 phase에서만 영향 골든 재기준.
- **P_fa4 — 창발 번식/개체군 사이클 + 체감온도 thermal 거동** ⬜ 부분
  drive-gated 창발 birth(F9 승격 — 현재는 activation-gate G5대로 **respawn-to-target 유지, birth 미착수**) +
  thermal drive/vital(F10 — **thermal drive는 FA5로 RESOLVED·SHIPPED**(대칭 comfort 밴드, `comfort_temp`/`thermal_band`,
  `TestFA5ClimateThermal`+`TestThermalStressComfortBand`); **잔여 = ① thermal 지속초과→vital 손상 미착수, ② '겨울' die-off/
  이주 압력 창발 미검증, ③ comfort/band 밸런스 튜닝**(현재 UNTUNED placeholder)).
  남획→붕괴→아사(공유지 L) 시나리오. 의존: (선택) §7 lifecycle 정합.
- **P_fa5 — 직렬화/스트림 + 렌더** ✅ SHIPPED
  `animals[]` periodic full + sparse delta(spawn/move/die) → `platform/persist` + `docs/core/data-contracts.md §6`;
  frontend 동물 렌더(이동·종).
- **park (frontier — 비차단):** 사회적 동물(무리/herd/세력권, **사람 연기**) · husbandry/길들임(F13 창발 flee-shift) ·
  계절 이주 richness · 작물/구조물 피해 · `engine/mind/perception` full-LoS 동물 지각 · navmap/pathfind 동물 경로.

## 3. Notes / escalations (정직 플래그 — 덮지 않음)

- **의존 현실(2026-07-09 갱신):** ① **planner 미빌드** → F1(a) horizon-1로 회피(plan 조립 없음, 유효). ② **lifecycle
  (§7) 미빌드** → F9(c) 타이머 respawn 유지(창발 번식 = P_fa4 잔여). ③ climate·expr·decay·scent = **빌드/출하 완료**
  (체감온도·바람 입력 계약은 실제 값으로 전환됨 — CA1~CA3 RESOLVED).
- **게이트 이력:** 2차 게이트(§1.3 F25~F42)·3차 게이트(F43/F44)·클러스터 7~10 전부 사람 확정 완료(2026-06-26 ~
  2026-07-08). 원문 = `docs/decisions/fauna-gates.md`(1차 F1~F24 전문만 커밋 `bebc643`).
- **불변 플래그(아래는 위반이 아니라 가드레일):**
  - **D3:** Hunt/Graze/Flee/Wary/Attack/Feed/Butcher가 horizon-1 utility로 선택되는 한 OK — drive→utility는 **§6
    데이터**(D4)여야 하고 per-species behavior tree를 저작하면 위반. utility는 순수 점수, Wary↔Flee 는 fear-밴드
    결과, stickiness는 §6 `is_current` 항 — 손그림 FSM 금지.
  - **D4:** drive 갱신·utility·apparent_temp·yield 전부 tag/§6 파생; 닷프로덕트(F26(b))는 drive↔action 매핑을
    엔진 코드화할 위험이라 기각됨.
  - **D5/D1:** 동물 drive-set은 agent `Value` 계와 **별개의** 동기 기계(축소 루프, §0-1). 코드베이스의 두 번째
    동기 기계이므로 drive≠Value 분리 유지 — drift 위험 상시 감시.
  - **D6/D8:** 동물 ToM 없음(F2/F3); 포식자→agent Safety는 지각 hostile tag로만 흐름(F8).
  - **D7:** 동물 base 스탯은 §6 합성; **stat-training/노화는 cross-cutting stats/lifecycle 소관** — fauna는 §6로
    *읽기만*(F29 가드).
  - **D10:** species = content `fauna:` 블록(F6); `Stats`/`Drives` = open(신규 stat/drive = 데이터).
  - **D11:** 냄새 그리드 = 보조 색인, 동물은 연속좌표 유지·칸 스냅 없음; FOV = 연속 bearing(F44).
  - **D12:** scent 확산 = 고정순서 stencil bulk(F33); apply = 결합 agent+animal 정렬 ID 1순서(F41); per-step rng
    fork; FOV bearing 테스트·M7 반발 합산 = 고정순서.
  - **expr L0 불변(F27):** drive/scent/sight/dist/apparent_temp/wind은 전부 소문자/점 **Attr 피연산자**(caller
    네임스페이스) — expr 메서드 추가 금지. `platform/config`가 `ReadsAttrs()` 교차검증.
- **glossary(F19/F42):** §1.2 목록(+ 클러스터 6~8 추가분) 채택 확정 → `docs/core/glossary.md` 동기화는 별도 단계.
- **DAG(F5/F41):** `engine/fauna` = stage 6(agent 옆)·world(stage 7) 와이어링 — `docs/core/architecture.md §2/§4/§5`
  반영 완료.
- **시나리오 정합:** P_fa3가 W7(뼈 craft 입력)·W8(힘줄 binding)·W10(사체/유품 부패) 공급측을 활성화; 포식자→Safety는
  사회 시나리오 threat→Safety 계열과 연결. 리얼리즘 회귀 = `docs/plans/scenarios-world.md` FA1~FA7 + arena fixture.
- **cross-doc(climate):** F40(apparent_temp)·F33(scent 바람 확산)·F41(world 와이어링)은 `docs/plans/climate.md §1c`
  의 **CA1(연주기)·CA2(바람 `wind.dir`/`wind.mag`)·CA3(단위 °C·operand 노출)** 와 결합 — operand 명칭·단위
  (`temperature`/`moisture`/`wind.dir`/`wind.mag`)는 두 문서에서 **반드시 동일**.

## 4. 이동·nav 장(場) 서브시스템 (FM · Open-Question 게이트 — 2026-07-09 개시)

동물 이동을 **potential-field 스티어**로 확장. 배경 심의: `docs/decisions/`(추후 이관). cross-ref: `engine/space/scent`
(다채널 냄새장, 바람확산, gradient 방향 — 이미 존재)·`engine/space/navmap`(hex, `Neighbors`/`StepCost`/`Passable`/`RequiredTags`)·
`engine/space/pathfind`(A*, 빌드됨·미사용)·`docs/plans/map.md`·agent 물 모델(`content/needs.yaml` **Hydration** + `content/actions.yaml`
**Drink** + terrain **salinity** 음용 게이트 — 재사용). 불변 프레임: 장 = 연속좌표 샘플하는 **보조 색인**(D11, scent/navmap 동류)이지
칸 스냅 아님; 목표(무엇을 원하나)=§6 / 방향(어디로)=장 = **D5 분리**; 장 갱신·샘플·블렌드·apply 전부 **결정적**(D12).

**핵심 관찰:** 사용자 "동물 방향은 거의 비슷하다 → 공유 장" = per-agent A* 회피. **F35 "per-agent pathfind 없음"은 유지**된다 —
우리는 개체별 경로가 아니라 **공유 navmap-flood 거리장**(보조 색인)을 추가하는 것(D11이 명시 허용). 개체 차등은 장이 공유돼도
**각 개체가 자기 위치에서 자기 drive로 가중**하므로 보존(D1/D6/D8).

### 4.0 Keystone — **RESOLVED (2026-07-09)**
**행동 선택 = §6 이산 utility-max(기존, 개체별) + 방향 해석에 얇은 blend.** 순수 potential-field 교체(b) 기각(F35 전면 재설계·
지역최소/진동 위험), 순수 단일채널(a) 기각(도주 중 절벽 돌진). 채택 = **a-backbone + bounded blend**: 선택된 행동의 주 pull 벡터에
**상시 hazard/포식자 반발 + (후행)무리 separation**을 §6 계수로 가중 합산. 지금도 flee+wander를 합산 중이라 **확장**임. 오해 교정 기록:
장 공유 ≠ 획일 행동(pull은 개체 drive 가중).

### 4.1 Open questions (해당 phase 착수 전 사람 RESOLVE — 게이트)
- **FM1 — 장 인벤토리 (신규 vs 재사용).** steer 입력장 집합. **장은 종별로 합성**(사용자 2026-07-09 "종별 navmap") —
  물리적 scent 채널(food/prey/predator)은 **공유**하되, navmap 비용/통과성(종별 `terrain_cost`/`Caps`/`impassable` — **이미 존재**)과
  채널 가중은 **종마다 다름**(deer=강 저비용 도하, wolf=강 고비용, fish=육지 hazard). options: (a) **하이브리드** — 동적 유인/포식자=scent
  (이미 바람확산), 정적 hazard·물=navmap-flood 거리장, (b) 전부 navmap-flood 통일, (c) 전부 scent. **rec: (a) + 종별 합성**. **RESOLVED (rec 채택 · 사람 승인 2026-07-09).**
- **FM2 — 정적 hazard 장(절벽/급경사/물가).** options: (a) navmap `StepCost`/wear에 hazard 비용만(반발벡터 약함),
  (b) **별도 정적 hazard 거리장**(hazard 셀에서 flood-fill, 1회·불변, gradient=명확한 away). hazard 소스 terrain은 content tag.
  **종별 flood**(각 종의 통과성/비용 뷰로 계산 → deer/wolf/fish가 서로 다른 hazard 장; N종 = N장, 정적이라 1회). **rec: (b) 종별.** **RESOLVED (rec 채택 · 사람 승인 2026-07-09).**
  - **P_move1 실현(축소, 사람 지시 2026-07-09 "재사용 하되 나중을 위해 곱할 e 값을 종마다 정해놔. 연속 회피 먼저"):**
    P_move1은 **단일 공유 hazard 장 1장** + **종별 스칼라 `e`(`hazard_avoidance`, §6)** 로 출하한다 — 개체 차등은 `e`
    가중으로 보존되므로 "연속 회피"에는 공유 장으로 충분. **종별 N장(위 (b))은 후행(P_move1b/P_move3와 통합)**,
    fish-inversion(물=집)도 그때 per-species 장으로 처리(현재는 `hazard_avoidance: 0.0`으로 임시 비활성). danger 소스는
    **navmap `BaseCost`/`Passable` 프록시**(의도된 §5 depth/slope는 terrainAttrs 미배선 — 별도 후행). 빌더=신규 leaf `engine/space/field`(FM-build).
- **FM3 — 동적 포식자 도주장.** options: (a) scent.predator gradient(직선반대·이미 cadence 갱신, 장애물 우회 X),
  (b) 포식자 셀 navmap-flood 거리장(우회 도주, **포식자 타일 변경/신규/사망 시에만** 재계산, 반경 제한 local flood),
  (c) **하이브리드** — 탐지·경계는 scent/FOV(F43/F44 유지), **도주 방향만** flood. **rec: (c).** **RESOLVED (rec 채택 · 사람 승인 2026-07-09).**
- **FM4 — fauna `thirst` drive + 물 유인 (별도 phase 가능).** fauna에 `thirst` drive 추가(D10, rate=balance).
  물장 = 음용가능(river·salinity 0, 재사용) 셀 flood 거리장; 근접 시 hunger 유사 회복. options: (a) 신규 fauna thirst+Drink 상당,
  (b) agent Hydration need 기계 재사용(fauna는 need 아닌 drive라 부적합). **rec: (a).** **RESOLVED (rec 채택 · 사람 승인 2026-07-09).**
  - **FM4-src — 물 유인장 소스 식별 (shape 하위게이트 · 2026-07-10, P_move2 착수 중 발견).** FM4 원문 "river·salinity 0 셀"은 개념만 정하고
    **런타임 소스 식별 기전**은 미정. 발견: (1) 에이전트 Drink는 **오브젝트 kind `water_source`**(objects.yaml, `provides: at_water`, 무한)를 쓰나
    현 fauna 월드엔 **미배치**(물=river/lake **지형** 셀); (2) salinity/moisture는 terrain.yaml **지형-TYPE별** attr이나 런타임 `terrainAttrs`는
    **미배선**(make만·flora도 빈 채 소비); (3) Go 지형명 하드코딩 금지. *options:*
    (a) **오브젝트 `water_source` 재사용** — 에이전트 Drink 모델과 통일(D1), 단 fauna 월드에 water_source 오브젝트 배치 선행 필요(물=지형인데 오브젝트로 이중화);
    (b) **config-time 지형-attr 유도(rec)** — config가 terrain.yaml attrs(`salinity ≤ ε ∧ moisture ≥ θ`)에서 **음용 지형 ID 집합** 유도 → world가 그 타입 navmap 셀을 `field.Build` 소스로(hazard 필드의 navmap-셀 소스와 동형; river/lake 자동 포함·sea 자동 제외). 지형명 하드코딩 0 + 런타임 terrainAttrs 불필요(속성이 TYPE-uniform ⇒ load-time 유도로 충분);
    (c) **런타임 terrainAttrs 배선** 후 셀별 salinity/moisture 판정 — 원문 최충실이나 terrainAttrs 배선(별도 게이트·flora도 영향)을 P_move2에 포함 = 스코프 확대;
    (d) 조합. **rec: (b).** **RESOLVED: (b) config-time 지형-attr 유도 (사람 승인 2026-07-10).** thirst drive 추가·§6 물-탐색·근접 회복은 데이터 정의(hunger/food/Graze 미러)·RESOLVED.
    > **빌드 순서(leaf-first):** ① `platform/config` — terrain attrs에서 **음용 지형 ID 집합** 유도(`salinity ≤ ε ∧ moisture ≥ θ`, ε/θ=world.yaml `water:` 블록) → `EnvConfig.DrinkableTerrains` → ② `engine/space/field` — 재사용(attraction=`Gradient` as-is, 신규 코드 0) → ③ `engine/world` — `buildWaterField()`(음용 셀=소스, 1회 캐시·hazard 동형) + fauna Snapshot에 `WaterField` 주입(신규 `AttractionSampler` 인터페이스, 의존성 역전) + 근접 시 thirst 회복 apply(hunger/Graze 미러) → ④ `engine/fauna` — `thirst` drive operand + `seek:water` 스티어 채널(WaterField `Gradient` 방향) → ⑤ content — 종별 `thirst` drive(rate) + §6 물-탐색 액션(utility=f(thirst)) + `seek:water` steer + world.yaml `water:` 임계. **중립성:** WaterField nil(음용 지형 0) 또는 thirst 미저작 ⇒ byte-identical.
- **FM5 — blend 식 = §6 vs 엔진고정 + 상시반발 범위.** 이동벡터 = 주 pull + Σ 상시반발, 각 항 가중치.
  options(계수 위치): (a) **§6/content**(D4/D10), (b) 엔진 상수. options(상시반발 범위): 절벽만 / 절벽+포식자 / +무리 separation.
  **rec: 계수=(a) §6; 범위=phase 점진(P_move1 절벽+포식자, 무리=P_move4).** **RESOLVED (rec 채택 · 사람 승인 2026-07-09).**
- **FM6 — 장 갱신 cadence & 트리거 (D12).** 정적장=1회 불변; 동적 포식자장=셀변경 이벤트; wind=scent 자체처리;
  "flat→wander/rest"(유효장 임계 이하면 seeded 국소 wander 또는 정지=에너지보존). options: (a) 순수 이벤트, (b) **이벤트+저빈도
  타이머 하이브리드**. 트리거는 스냅샷 파생, flood 정렬 결정적, 고정 animal-ID apply. **rec: (b).** **RESOLVED (rec 채택 · 사람 승인 2026-07-09).**
- **FM7 — 포식자 측 장(사냥).** predator 이동장 = navmap cost(지형) + prey 유인. options: (a) scent.prey gradient만(있음, 기존 Hunt),
  (b) + prey 위치 flood 유인장(원거리 사냥). **rec: (a) 우선, (b) 후행.** **RESOLVED (rec 채택 · 사람 승인 2026-07-09).**
- **FM8 — 무리(herd) 공유 (P_move4, 후행).** options: (i) 명시 herd 그룹 대표가 장 추종, (ii) **창발형** — 감지자만 장 추종,
  이웃은 boids **alignment**로 방향 승계(자기 flood 불필요). M7 반발합산=separation 재사용. **rec: (ii) D2.** **RESOLVED (rec 채택 · 사람 승인 2026-07-09).**
- **FM-build — 장-빌더(navmap flood-fill) 위치.** navmap에 Field/flood API 추가 vs 신규 leaf `engine/space/field`. **rec: 신규 leaf**
  (navmap은 순수 지형색인 유지). **RESOLVED (rec 채택 · 사람 승인 2026-07-09).**

#### 밤잠·기상 (FM11~FM12 · 2026-07-10 개시 — 사용자 "저녁에 자는 것도 있나? 주변에 이벤트가 생기면 일어나게")
> **사실 확인:** 세계엔 낮/밤이 있으나(`worldtime.Clock.HourOfDay` → climate 일교차·렌더 day_night) **fauna §6엔 시간대 operand가 없다**
> — 동물이 "밤이다"를 읽고 잘 수단이 없음(현행 밤 효과는 apparent_temp 저온뿐, 간접). `Rest` 액션·`fatigue` drive는 존재하나 시간대 미연결.
> "주변 이벤트에 기상"은 **F45 wake(`ActiveUntil`)가 포식자 냄새 도달 시 즉시 깨움**으로 이미 부분 존재(dormant 한정·포식자 한정).
- **FM11 — 밤잠 활성화 = 시간대 §6 operand.** 동물이 밤을 인지하도록 §6에 시간 채널 주입(EnvSample에 `w.clock` 파생 필드 추가 →
  `context.Attr` 노출, temperature와 동형 배선). 주행성/야행성은 content §6 부호로 창발((1−daylight) vs daylight = D2/D10, 하드코딩
  flag 금지). *options:* (a) **`daylight` ∈[0,1]**(정오=1·자정=0, HourOfDay 사인) — clamp01 친화·완만한 여명/황혼; (b) `hour_of_day`
  ∈[0,24) — 자정 wrap 불연속; (c) `is_night` 이진 — 하드 컷오프. **rec: (a).** **RESOLVED: (a) `daylight ∈[0,1]` (사람 승인 2026-07-10).**
- **FM11b — 밤잠 구현 형태.** *options:* (a) **기존 `Rest` 재사용** — 밤에 Rest utility가 이기는 창발, 신규 액션/상태 0(D3), fatigue
  자동 회복; (b) **신규 `Sleep` torpor 상태** — 별도 Sleep 액션 + 더 큰 fatigue 회복 + 높은 기상 임계(약한 자극엔 안 깸). **rec: (a) 최소표면.**
  **RESOLVED: (b) 신규 Sleep torpor (사람 승인 2026-07-10)** — 사람이 얕은 rest 아닌 **깊은 잠**(회복↑·기상 임계↑)을 선택 → 아래 SS1~SS3 하위 게이트 발생.
- **FM12 — 기상 트리거 범위.** 자는 동물을 cadence 밖에서 **즉시** 재중재시키는 자극. 깨면 fear가 Sleep/Rest를 이겨 도주 = 창발.
  *options:* (a) **F45 포식자 냄새 wake 그대로**(이미 존재; 최소 표면); (b) **확장**(포식자∨굶주린 포식자의 prey∨동종 경보); (c) 총 각성도 ε.
  **rec: (a) 우선(P_sleep1), (b)는 후행.** **RESOLVED: (a) F45 재사용 (사람 승인 2026-07-10)** — 단 torpor(FM11b-b)의 높은 임계는 SS3에서 정의.

##### SS1~SS3 — Sleep torpor 하위 shape (FM11b-b가 연 신규 게이트 · 2026-07-10)
- **SS1 — Sleep 상태 표현.** *options:* (a) **`CurrentAction=="Sleep"` 재사용** — 신규 직렬화 필드 0; 기상/회복 조건이 CurrentAction 판독;
  torpor=이진(자는 중 iff Sleep). (b) 신규 `Animal.Sleeping bool`/`SleepDepth float64` 지속 필드. **rec: (a).** **RESOLVED: (a) (사람 승인 2026-07-10).**
- **SS2 — 깊은 fatigue 회복.** *options:* (a) **balance 파라미터 `SleepFatigueRecoverPerTick`**(CurrentAction==Sleep 시 적용, 기존
  `FatigueRecoverPerTick`와 동형 계열); (b) 액션 tag(`torpor`) → recovery×K; (c) 자동(깊음 없음). **rec: (a).** **RESOLVED: (a) (사람 승인 2026-07-10).**
- **SS3 — 기상 임계(F45 during Sleep).** *options:* (a) **balance 파라미터 `SleepWakeScentThreshold`** — CurrentAction==Sleep이면 F45가
  포식자 냄새 intensity ≥ threshold일 때만 wake(현행: >0); (b) sleep depth 스케일(SS1-b 필드 필요). **rec: (a).** **RESOLVED: (a) (사람 승인 2026-07-10).**
> **게이트 상태(밤잠·기상):** FM11/FM11b/FM12 + SS1~SS3 **전부 RESOLVED (2026-07-10).** **P_sleep1 SPEC 착수 가능.**
> 빌드 순서(leaf-first): ① `engine/fauna` — `daylight` operand(EnvSample.Daylight + context.Attr) + Sleep 액션 인식(CurrentAction=="Sleep":
> torpor fatigue 회복 + F45 wake 임계) → ② `platform/config` — `sleep_fatigue_recover`/`sleep_wake_scent_threshold` balance 파싱 + daylight 배선 →
> ③ world — EnvSample.Daylight = `w.clock` 파생 주입 → ④ content — 종별 `Sleep` §6 액션(daylight-구동 utility) + balance 값.

### 4.2 Phases (각 독립 shippable + 결정성 골든)
- **P_move1** — 정적 hazard 장(FM2, **단일 공유 장 + 종별 `e`**) + a-backbone blend steer(FM5) + flat→wander(FM6). 단독 개체.
  **연속 hazard 회피 SHIPPED (2026-07-09):** `engine/space/field`(potential 장 leaf) + fauna steer blend(`e·Repulsion`) +
  world 단일 장 1회 빌드·주입. 종별 필드·thirst 유인·포식자 도주장·무리는 P_move1b~P_move4 후행.
- **P_move2** — `thirst` drive + 물 유인장(FM4); drive>cost 창발 음용.
- **P_move3** — 동적 포식자 flood 도주장 + 이벤트 캐시(FM3/FM6); 포식자 측 유인 개선(FM7).
- **P_move4** — 무리 alignment 공유(FM8); herd 창발.
- **P_sleep1** — 밤잠(FM11 `daylight` operand → 신규 `Sleep` torpor 창발) + 기상(FM12 F45 재사용, SS3 임계).
  **SHIPPED (2026-07-10):** worldtime `DayFraction` + fauna `daylight`/`TagSleep`/wake 임계 + world daylight 주입·torpor fatigue 회복 + content(5종 Sleep §6·`state:sleep`). fish·FM12 확장·튜닝은 후행.

### 4.3 현실성(realism) 기준 + 신규 Open items (2026-07-09 — "현실적으로 움직이나" 검증 산물)
장-블렌드 모델은 고전적 실패모드(지역최소 stall / 로봇틱 beeline / restless drift / 상시 등속 활주)가 있어, **현실적 이동을 만들려면
아래가 필수**다. 대부분 기존 레버(wander·is_current stickiness·turn-rate·fatigue/stamina) 확장이지만, 둘은 신규 메커니즘:
- **FM9 — 이동 deadband (sub-threshold → 정지/미세만).** 유효 blend 벡터 크기 < ε면 **정지 또는 미세 wander만**(에너지보존·
  "위협/필요 없으면 대체로 안 움직임"). options: (a) deadband, (b) 항상 gradient 추종(restless·비현실). **rec: (a)**, ε=balance 튜닝. **RESOLVED (rec 채택 · 사람 승인 2026-07-09).**
- **FM10 — 간헐 이동(move-pause 버스트) + 에너지-게이트 속도.** 기본 저속/정지, **fear/hunger 임계 초과 시에만** 버스트(§6 speed가
  이미 drive 읽음 + stamina 소모로 자기제한). options: (a) 상시 연속 활주(로봇틱), (b) **drive-게이트 버스트-휴지 리듬**. **rec: (b)** —
  사용자 "체력 아끼려 계속 안 뛴다"의 직접 인코딩. **RESOLVED (rec 채택 · 사람 승인 2026-07-09).**
  - **FM9a — deadband 신호원 (shape 하위게이트 · 2026-07-10, P_move-realism 빌드 착수 중 발견).** FM9 rec는 "유효 blend 벡터 크기 < ε"였으나
    shipped 아키텍처에서 `baseSteerDir`는 **단위 방향만** 반환(키스톤=§6 이산 pick + 얇은 blend; 드라이브 크기는 벡터가 아니라 **§6 `speed`에** 인코딩)
    → blend 벡터 크기는 드라이브 신호가 아님. *options:* (a) **§6 `speed` 임계** — `rules.Speed(ctx) < ε`면 정지/미세 wander(shipped 아키텍처·FM10과 정합,
    드라이브 이미 §6 speed 인코딩); (b) `baseSteerDir`를 드라이브-가중 크기 벡터로 개조(키스톤의 "순수 potential-field 교체" 기각과 충돌); (c) §6가 별도 `move_intent` 스칼라 저작. **rec: (a).** **OPEN.**
  - **FM10a — 버스트-휴지 에너지 게이트 shape (하위게이트 · 2026-07-10).** FM10 rec (b) "drive-게이트 버스트-휴지"에서 **에너지 자기제한이 붙는 형식**.
    현재 stamina 드레인/회복은 **전투 engaged 한정**(`nextStamina`) — 자유 이동(도주 포함)엔 stamina 소모 없음 ⇒ 무한 질주 가능. *options:*
    (a) **순수 §6-창발(최소면)** — 신규 stamina 배선 0; 버스트-휴지는 §6 speed가 fear/hunger를 읽어 창발(드라이브↑→버스트·도주 후 드라이브↓→휴지), stamina 자기제한 없음(지속 도주 시 무한 질주 잔존);
    (b) **stamina §6 operand + 이동 stamina 동역학** — `stamina`를 §6 operand 노출 + stamina 드레인을 이동 effort(speed)에 비례로 확장·rest서 회복 ⇒ content가 소진/회복 곡선 저작(D4/D10; 지속 도주도 버스트화);
    (c) 엔진 speed cap `f(stamina)`(D4/D7 위배 — 기각). **rec: (b)**(당시 rec). **RESOLVED-then-SUPERSEDED (2026-07-10):**
    빌드 착수 중 **버스트-휴지가 이미 M2 fatigue 축으로 라이브**임을 확인 — Flee/Hunt=`effort:high` → `applyAnimalFatigue`가 `fatigue` 누적 → **모든 종 §6 `speed`가 `- fatigue`** → 지쳐 감속(휴지)·정지 시 배출(회복). 별도 stamina-locomotion 축은 **중복 메커니즘**(D-rule "메커니즘 발명은 결함")이라 (b) 철회. **최종 RESOLVED: (M2 재사용) — 신규 에너지 메커니즘 0; FM10 잔여 = fatigue 계수/rate 튜닝(balance, P_fa4). (사람 승인 2026-07-10).**
  > **게이트 상태(FM9a/FM10a):** FM9a=**RESOLVED (a) §6 speed 임계**; FM10a=**M2 fatigue 재사용**(별도 stamina 축 없음). → **P_move-realism 신규 코드 = FM9 deadband 뿐.**
  > 빌드 순서(leaf-first): ① `engine/fauna` — steerFull/cheap deadband(§6 `speed < MoveDeadband` ⇒ 정지) → ② `platform/config` — `move_deadband` balance 파싱 → ③ world — `MoveDeadband`를 fauna Snapshot에 주입(ScentCellSize/DT 동형). **중립성:** `MoveDeadband ≤ 0` ⇒ 기존 `speed ≤ 0` 정지 로직만 = byte-identical(기존 골든 보존). FM10 = 코드 없음(fatigue 튜닝 후행).
- **realism 수용기준(각 phase 골든/시나리오로 검증, 신규 메커니즘 아님):** ① anti-stall — "강 건너 먹이 앞 무한 pacing" 없음
  (wander+stickiness로 이탈); ② non-beeline — 경로가 직선 로봇 아님(turn-rate+wander); ③ 기본 정지 — 무자극 시 대체로 rest/미세이동;
  ④ 위협 반응 등급 — 원거리=경계(Wary, 이동 적음), 근접=버스트 도주(F43/F44 유지).

> **게이트 상태:** FM1~FM10·FM-build **전부 RESOLVED(2026-07-09, rec+종별)**.
> **P_move1 연속 hazard 회피 = SHIPPED (2026-07-09, 단일 공유 장 + 종별 `e`; 종별 필드는 후행).** 배경 심의 → `docs/decisions/fauna-gates.md`.
> 빌드 순서(leaf-first, 완료): ① `engine/space/field`(potential 장 leaf) → ② `engine/fauna` steer delta(hazard blend) →
> ③ content(`hazard_avoidance` 종별 §6 계수) → ④ world 와이어링(단일 정적 hazard 장 1회 빌드·주입). deadband/버스트(FM9/FM10)·thirst(FM4)·도주장(FM3)·무리(FM8)=후행 phase.

### 4.4 분포·경계 (Distribution & Boundary — FM13~FM15 · Open-Question 게이트 · 2026-07-12 개시)

> **사용자 관찰(2026-07-12):** "동물들이 다들 결국 벽에 가서 벽을 비비고 있다" + "어떻게 비슷한 밀도로 분포되게 할까?"
> **진단(코드 확인):** 분포를 정하는 3단계에 각각 누수. ① **초기 스폰 = 이미 균등**(`worldgen/materialize.go` — 맵 전체 rejection-sample, `soil` 셀만). ② **이동/섞임 = 누수 시작** — cheap/DORMANT 경로(`fauna/cheap.go`)가 wander 없이 **탄도(직진)** 라 초식 다수가 안 섞이고 일직선으로 벽까지 감 + 경계가 **흡수벽**(`world/fauna_apply.go` `clampToBounds`는 위치만 고정, 바깥향 heading 유지 → 접선 비비기; `steerFull` blocked 시 heading **동결** → 회전 폐기). ③ **리스폰 = 몰림 되먹임** — `world/respawn.go`가 새 개체를 **생존자 근처**(near-live)에 놓아 벽에 몰린 생존자 주변을 계속 채움(cadence 200). 폭주 구조: 균등 시작 → 탄도로 벽 → 흡수 고정 → 리스폰이 벽 근처 증폭.
> **정직 플래그:** "완전 균등"은 목표 아님 — 물유인장(FM4)·먹이 scent는 자원 쪽으로 당김이 의도된 창발(현실적). 여기서 없애려는 건 **경계 인공물**이지 자원 경사가 아님. 반사 경계여도 지속성 워커는 얇은 가장자리층 잔존(활성물질 성질) — turn/wander 튜닝 레버.

#### 4.4 Open questions (착수 전 사람 RESOLVE — 게이트)
- **FM13 — 경계 흡수 제거 (boundary de-sink).** 맵 외곽이 동물을 벽에 고정하지 않게. **번들 버그픽스(옵션 무관 공통):** `steerFull`/cheap가 blocked일 때 옛 heading 동결 대신 **회전된(desired) heading을 커밋**(위치는 정지하되 다음 틱 방향은 살림 — 매 틱 회전 폐기 제거). 외곽 반발 기전 *options:*
  (a) **반사(reflect) — apply 단계** `clampToBounds`가 위치를 clamp할 때 **바깥향 속도 성분 부호 반전**(vx/vy flip) → heading도 반사각. 축정렬 외곽이라 법선 자명·cheap/full 양경로 공통(apply는 intent 후 1곳). 유한영역 반사경계 = 랜덤워크 정상분포 균등. **rec.**
  (b) **경계 hazard 소스** `world/hazard.go`가 외곽(OOB-인접) 셀을 danger 소스로 추가 → 상시 반발이 내부로 밀어냄(sea/cliff와 동형, 연속·graded). 단 **cheap 경로는 hazard blend 미적용**(baseSteerDir 안 부름)이라 DORMANT엔 안 통함 → FM14 승격과 세트.
  (c) **(a)+(b)** 반사(강경 복귀·양경로) + 경계 반발장(ACTIVE는 벽 전에 완만 감속·선회). 가장 자연스러우나 표면↑.
  **rec: (a) 반사 + 공통 heading-commit 버그픽스** (양경로 커버·최소·균등수렴). (b)는 ACTIVE 자연스러움용 선택 가산. **RESOLVED: (a) 반사 + heading-commit (사람 승인 2026-07-12).**
- **FM14 — 벌크 섞임 / idle drift (interior mixing).** 내부(가장자리 아닌) 균등성. 현재 `move_deadband=0.0`이라 무자극 동물도 idle 속도로 **탄도 drift**→벽. *options:*
  (a) **`move_deadband` 상향(튜닝, 신규 코드 0)** — §6 idle speed 위로 올려 무자극 동물이 **정지**(에너지보존·현실적) → 초기 균등이 유지됨. FM9(SHIPPED) 레버의 값 선택일 뿐(신규 메커니즘 아님·D12 골든 영향은 값 변경분).
  (b) **cheap 경로에 wander 추가** — DORMANT 매 틱 heading jitter로 능동 섞임. 단 **DORMANT 매 틱 RNG draw** ⇒ 현 SPEC의 "cheap off-boundary = RNG draw 없음(D12 byte-identity)" 계약 변경·골든 전면 재생성·성능↑.
  (c) **경계 근처 승격** — cheap 동물이 경계(또는 blocked) 근처면 그 틱 full pipeline 승격(wander+hazard blend 획득). 국소적·byte-identity 대부분 보존. FM13(b) 경계 반발장과 세트로만 의미.
  (d) **(a)+(c)**.
  **rec: (a)** — 최소·초기 균등 보존·에너지보존 정합(FM10 M2 fatigue와 동선). 벽에 도달하는 소수 mover는 FM13이 처리. **RESOLVED: (a) move_deadband 상향 (사람 승인 2026-07-12) — 단 아래 FM14a 형태 하위게이트 발생.**
  - **FM14a — deadband 형태 하위게이트 (구현 착수 중 발견 · 2026-07-12).** idle 속도 산정: `speed = Agility×k − …` 구조라 무자극 속도 = deer 0.21·rabbit 0.34·wolf 0.31(units/tick, Agility=70/85/82). **문제:** 전역 `move_deadband`는 단일 스칼라라 rabbit idle(0.34)을 멈추려면 >0.34여야 하는데, 그러면 **deer의 graze-탐색 속도(=idle 0.21, deer §6 speed에 hunger 항 없음)** 도 함께 막혀 굶주린 deer가 먹이로 못 감(채집 파탄). 즉 종별 idle이 다르고 §6 speed가 idle 이동(Agility 상수항)과 목적 이동(drive 항)을 **한 스칼라에 섞어** 전역 deadband로 분리 불가. *options:*
    (a) **종별 `move_deadband`(objects.yaml 스칼라, `hazard_avoidance` 동형)** — rabbit 0.4·deer 0.05 등 종마다. Snapshot 전역 → 종별 배선(작은 스키마 확장·per-species 필드=게이트). rabbit idle 정지+deer 채집 보존. **rec.**
    (b) **action-keyed hold** — deadband를 §6 speed가 아니라 **선택된 action이 MoveTo/wander일 때만** 적용(목적행동 Graze/Flee/Hunt는 항상 이동). FM9a("deadband=§6 speed 임계") 재-shape → 신규 하위게이트.
    (c) **§6 speed drive-gate 재설계** — Agility를 상수항이 아닌 **승수**로: `speed = Agility×k×(idle_floor + fear + hunger_seeking)`. 가장 근본적이나 6종 speed 프로그램 전면 개편(스코프↑).
    (d) **전역 deadband + FM13만으로 수용(defer)** — 전역 deadband는 낮게(효과 미미) 두거나 0 유지, 벽 몰림은 FM13 반사로 해결하고 벌크 정지는 P_fa4 speed 재설계까지 이연. **RESOLVED: (a) 종별 move_deadband (사람 승인 2026-07-12).**
  - **FM14b — deadband 값 설정의 잠재 블로커 (P_dist2 배선 후 발견 · 2026-07-12).** 종별 deadband **메커니즘은 배선·SHIPPED**(objects.yaml `fauna.move_deadband` → config → `SpeciesRule.MoveDeadband` → `rules.moveDeadband(sp, global)`: 양수면 종별, 아니면 전역 fallback; 테스트 `TestPerSpeciesDeadband*`). **그러나 값을 넣을 수 없음:** 6종 §6 `speed` 프로그램을 전수 확인한 결과 **어느 종도 speed가 hunger로 상승하지 않음**(fear만 deer/rabbit에서 상승; goat/bear/wolf/fish는 fear도 없음). 따라서 idle-wander 속도 = graze-탐색 속도 = hunt 속도 → `move_deadband ≥ idle`이면 idle 표류뿐 아니라 **채집/사냥 이동까지 정지**(굶어 죽음), `< idle`이면 효과 0. 즉 종별 스칼라는 **cross-species** 충돌(rabbit↔deer)은 풀지만 **within-species** 충돌(idle-wander↔graze-seek 동속)은 §6 speed 구조상 못 풂. *options:* (a) **§6 speed에 hunger/seeking 항 추가**(목적 이동 > idle wander; 예 herbivore `speed += hunger*scent.food*k`) → 그 사이에 deadband 설정 가능(content §6 편집, D10). **rec.** (b) **action-keyed hold**(FM14a-b 재부상: MoveTo/wander일 때만 deadband). (c) **defer** — 값 0 유지(메커니즘만), 벽 몰림은 FM13로 해결됨(FM14 없이도 사용자 불만 해소); 벌크 정지는 P_fa4. **RESOLVED: (a) §6 hunger 항 추가 (사람 승인 2026-07-12).**
    > **구현 (a) shape:** hunger *restlessness* `speed += hunger * k`(scent.food 아님) — 배고픈 개체는 먹이 냄새 없이도 roam(탐색), 포만 개체는 idle 속도로 deadband에 걸려 rest. 이로써 rest-when-sated + roam-when-hungry가 §6 부호로 창발(D2/D10). **herbivore(deer/rabbit/goat)만** 적용(분포 우려의 다수; predator/fish는 무변=deadband 0으로 상시 roam → arena hunt-success 밴드 교란 최소). deadband = idle 속도 바로 위(포만 rest), k = 중간 hunger(~0.3)에서 deadband 돌파. idle 속도=deer 0.21/rabbit 0.34/goat 0.174(Agility 70/85/62×coeff). **전부 UNTUNED placeholder**(밸런스=P_fa4); 수용판정=arena `TestPredationArenaBalance` 밴드(engage>0 ∧ success<50%) + 굶주림 대량사 없음.
- **FM15 — 리스폰 분포 정책 (respawn distribution).** `world/respawn.go respawnPos`. *options:*
  (a) **near-live 유지(현행)** — 생존자 근처 스폰. 군집·포식자↔피식자 조우율·무리(FM8) 창발엔 유리하나 벽 몰림을 되먹임 증폭.
  (b) **균등 rejection-sample** — 초기 스폰과 동일 기전(`materialize` 재사용 — 맵 전체 passable/soil 균등) → **정상상태 균등**(리스폰이 지배적 재분포력이라 단일 최대 레버). 조우율/군집은 하락.
  (c) **하이브리드** — 종 생존 시 near-live(단 spread 확대)·멸종 재도입만 균등; 또는 종별 정책 스칼라(`respawn_spread`).
  **rec: (b)** — 사용자가 "비슷한 밀도"를 명시. 단 조우율 저하는 [[live-emergence-underseeded]] 위협 부트스트랩과 상충 가능 → (c) 대안 병기. **RESOLVED: near-live 유지(현행, 변경 없음) (사람 승인 2026-07-12)** — 조우율·무리(FM8) 창발 우선. 벽 몰림은 FM13이 처리(생존자가 내부에 분산 → near-live 리스폰도 내부에 뿌려짐).
> **게이트 상태(분포·경계):** FM13=**RESOLVED (a) 반사+heading-commit**; FM15=**RESOLVED near-live 유지(no-op)**; FM14=**RESOLVED (a) deadband 상향**이나 **FM14a 형태 하위게이트 OPEN**(전역 스칼라 ↔ 종별 idle 충돌). **P_dist1 빌드 착수 = FM13 뿐**(FM14는 FM14a RESOLVE 후). 빌드 순서(leaf-first): ① `engine/fauna` — `steerFull` blocked 시 heading-commit(step.go, 옛 heading 동결 제거) → ② `engine/world` — `reflectAtBounds`(commitAnimalOwnState, 위치 clamp + 바깥향 heading 성분 반사). **중립성:** 반사·heading-commit 둘 다 **경계/불통과 지형에 실제 닿은 개체에만** 영향(내부 이동 무변) — 단 벽/지형에 닿는 기존 골든은 재생성 필요.
> ### 4.4 Phases
> - **P_dist1 — SHIPPED (2026-07-12, 미커밋).** FM13 반사 경계(world `reflectAtBounds`) + blocked-heading-commit(fauna `steerFull`). move_deadband 무변(0.0). 벽-비비기 제거·내부 반사 확산. 테스트 `fauna/blocked_heading_test.go`·`world/reflect_test.go`; 전체 1254 통과·골든 무변. SPEC: `fauna/SPEC-steering.md`·`fauna/SPEC.md`·`world/SPEC-world-fauna.md`.
> - **P_dist2 — SHIPPED (2026-07-12, 미커밋).** 종별 deadband(`SpeciesRule.MoveDeadband`, 전역 fallback) 배선 + **FM14b (a): herbivore §6 speed에 hunger-restlessness 항(`+ hunger·k`) + 종별 `move_deadband`**(deer 0.27/rabbit 0.42/goat 0.21, idle 바로 위; predator/fish 무변). rest-when-sated + roam-when-hungry가 §6 부호로 창발. objects.yaml + objects.schema.json(`move_deadband` 키) + config/fauna 배선. **검증:** arena hunt-success 12.8%(밴드 내), prey population 3000틱 안정(41→42, deadband 굶주림 없음), 전체 1254 통과. 값=UNTUNED placeholder(P_fa4).
