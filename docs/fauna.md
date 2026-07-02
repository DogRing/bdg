# Fauna — 동물 (축소 반응 루프) — Subsystem Plan

Concept & rationale: `docs/design.md §5`(연속좌표·동적지형), `§6`(Formula DSL — `engine/kernel/expr` 공유 평가기),
`§7`(생애주기 — 사망×번식, object-mortality 계열), `§9`(경제 — 무주물/소유 seam).
메뉴: `docs/world-roadmap.md` 🦌 동물 섹션 + 교차연결(fauna→물질사슬·decay·flora·climate).
형제 문서(양식·패턴 출처): `docs/materials.md`(FINAL recipe model — 산물이 *이미 설계된* craft 사슬로 합류),
`docs/flora.md`(순수-Step transform·다중주기·종=`objects.yaml` 블록·outcome-중립 staging),
`docs/climate.md`(체감온도·바람 입력 계약·결정성 패턴), `docs/map-plan.md`(navmap/직렬화/보조-색인 양식).

> **게이트 완료(2026-06-26):** §0 잠금 + §1 **전부 RESOLVED**(사람 확정). 옵션 전문·근거는 pre-resolution
> 커밋 `bebc643` 참조. 이 §1은 확정값 + 핵심 근거만 유지. SPEC은 이 확정 위에서 작성한다.
> **메커니즘을 발명하면 결함이지 주도성이 아니다** — SPEC/구현이 §0·§1을 벗어나면 여기를 먼저 고치고 사람 승인.

관련(예정) 모듈: **신규** `engine/fauna`(축소 반응 컨트롤러 + 냄새 그리드 보조-색인), `engine/world`(소유·갱신주기·
intent 수집·apply·냄새 bulk 패스), `engine/mind/actions`(공유 원자행위 레지스트리 — Hunt/Eat/Forage… + 신규
Butcher/Flee/Graze), `engine/kernel/expr`(§6), `engine/space/spatial`(근접), `engine/env/decay`(P_m2 — 사체 산물 lot 부패, **재사용**),
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
| F10 | 체감온도 거동 | **(a)+(b)** thermal drive bias + 지속초과 vital 손상; **climate 전 thermal-OFF**(내성식 존재·중립 출하) |
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
| F21 | 필드 값 | **이진 존재 플래그 + 바람 방향 인지 → upwind homing** (스칼라 농도 gradient 아님) |
| F22 | 채널 | **(a) 다채널 `{food, prey, predator}`** (셀당 채널별 플래그); 채널 확장은 후행 phase |
| F23 | 반응 | **(a) 냄새 따라가기** — 출처 객체 정체 안 캠 (upwind=접근 / predator는 반대=flee) |
| F24 | cadence | **(a) 계층** — 이동/flee=매 틱 · 냄새 확산=Ns틱 · 체감온도=Nt틱(더 느림) · 번식=bulk; N=`balance.yaml` |

### 1.1 감지 모델 — F15 대체 (F20~F24 통합; SPEC 지침)
**단일 균일 그리드 = world 소유 보조 색인** (spatial hash·navmap cost field와 동류; 모듈 배치 = `engine/space/spatial` 확장
vs `engine/fauna` 서브모듈은 SPEC 시 확정, F5 정신). **동물은 연속좌표 유지, 자기 위치가 속한 칸만 읽는다(스냅 금지, D11).**

1. **침착(deposit):** 냄새나는 객체/동물이 자기 칸의 **채널 플래그**(`food`/`prey`/`predator`) set. 이진(있다/없다).
2. **확산(spread):** **바람이 플래그를 downwind 칸으로 운반**(범위 확장). **바람 없음(P1, climate 전) → 플래그 국소
   (출처 칸 + 인접) → 단거리 감지만.**
3. **반응(read):** 동물이 자기+이웃 칸 읽음 → 채널 플래그 있으면 **바람 방향 따라 upwind steering**(food/prey 접근,
   predator flee). 바람 없어도 *어느 이웃 칸이 켜졌나*로 coarse 방향 확보(F23 따라가기, 출처 정체 불요).
4. **포식자 = 구동자:** 포식자(소수)가 **매 틱 자기 칸에 `predator` 플래그 침착**(쌈) → 인접 초식이 즉시 읽고 flee.
   나머지 채널 확산은 **bulk**(Ns틱). → 임박 회피 responsive + O(N²) 스캔 없음(2계층 그리드 불필요).
- **결정성(D12):** 침착 = apply 단계 직렬·고정 순서; 확산 = 고정순서 stencil bulk 패스(`tick % Ns`), RNG 없음;
  읽기는 score 단계 pure. **map-순회로 로직 굴리지 않음**(필드 *데이터* 갱신일 뿐 — navmap wear/terrain 패스 동형).
- **입력 계약(바람):** P1은 바람 중립(단거리), climate 출하 시 원거리+upwind homing 활성 (thermal-OFF와 동일 패턴).
- **정직 트레이드오프:** 이진은 *거리*를 모름(있다/upwind 방향만) — steering엔 충분, 정밀 거리판단 필요해지면 그때
  스칼라 농도로 승격(후행). P1엔 무관.

### 1.2 glossary 용어 (F19 — coin 확정, `docs/glossary.md` 등재 대상)
entity `Animal` · `carcass` · `drive`(+ 개별: `hunger`/`fear`(→Flee)/`thermal`/`fatigue`/`repro_readiness`) ·
행위 `Butcher`/`Graze`/`Flee` · 체감온도 §6 operand `apparent_temp` · 포식자 hostile tag `threat:predator` ·
`fauna:` content 블록 · 표적 tag `edible`/`prey` · **냄새 그리드**: scent grid, scent channel
(`scent:food`/`scent:prey`/`scent:predator`), 바람 operand `wind.dir`.
> 명칭은 추천 확정값. `glossary.md` 반영은 별도 단계(여기 등재 후 동기화).

## 1.3 SPEC-design open questions — Phase-1 detail (F25~F44)

> **2차 게이트(2026-06-26):** §1(F1–F24)은 메커니즘 *선택*을 확정했다. F25~F42는 그 아래 **"정확히 어떤 데이터 모양·수식
> 형태"** 층 — `engine/fauna` module SPEC 작성 전 **사람 확정 필요**. 전부 옵션+추천+`OPEN`. **아무것도 결정하지 않는다**
> (메커니즘 발명 = 결함, 주도성 아님). 옵션은 §0·§1을 벗어나지 않으며, 깨끗한 메커니즘이 §1에서 도출 불가하면 그게 바로 OPEN으로
> 열거할 대상이다(발명 금지).
> **★ foundational(아래 스키마 전부를 형성): F26(per-action utility 수식 형태) · F27(expr↔fauna 피연산자 브리지).**
> F31(fauna 블록 스키마)은 이 둘에 종속.
> 선례(발명 아님, 그대로 차용): flora `SiteInput`/`Rules`(§6 Program 소유 + Context 어댑트), decay `accel` Program(Dm3),
> Cm3 `tool:<family>.quality` Attr 피연산자, needs `UpdateConditionalNeeds`(rate + 조건부 set, F8 채널),
> spatial `cellSize`(balance), climate `fork(tick)` per-step RNG, navmap `wear` deposit(apply 직렬·고정순서).

### Resolutions (F25~F42) — 사람 확정 (2026-06-26): **전부 추천대로**
> 아래 표가 권위. 각 F의 옵션 상세·근거는 그대로 기록(재논쟁 금지). F43/F44는 클러스터 6에서 RESOLVED.

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
| **F35** | (a)+(c) §6(base stat) 기본속도 + fear/fatigue **+ thermal** 변조(체온 스트레스→감속, thermal=f(apparent_temp F40); speed §6가 읽는 drive는 열린 content D4/D10 — 고정 화이트리스트 아님); navmap `Passable`/`TerrainAt` 샘플만(pathfind 없음, D11) |
| **F36** | (a) P1=`engine/fauna` 서브모듈 소유 → 나중 `engine/space` 승격 |
| **F37** | (a) 종 products→파라미터화 `carcass` kind + `raw_meat` decay lot(Dm4); 미식→`rotten_matter`(W10) |
| **F38** | (a) Mine/Fell 평행 non-recipe extract, `tool:cutting` gate, §6(Dexterity,`tool:cutting.quality`) yield; agent action |
| **F39** | (a) `balance.regen.prey_respawn` 타이머 재사용; 창발 birth=P_fa4 |
| **F40** | (a) 종-블록 §6(climate attr[**°C** `temperature`/`moisture`/`wind.*`] + 동물 attr); P1 climate-OFF 중립 |
| **F41** | (a) 단일 read→score→intent→apply, **결합 agent+animal ObjectID 정렬 apply 순서**, `fork(tick)`; ⚠ architecture.md DAG 삽입=SPEC 직전 사인오프 |
| **F42** | F19와 batch 등재: `SpeciesID`·`DriveID`·`fauna.Rules`·Attr operand명(`hunger`/`fear`/`scent.*`/`dist.*`/`sight.predator`/`apparent_temp`/`wind.dir`/`wind.mag`)·`Heading`·`cellSize`·`fov_arc`·`Wary` |

### 클러스터 1 — 의사결정 코어

**F25 — drive set + per-tick 갱신 규칙.** drive 집합은 F19 수준(`hunger`/`fear`/`thermal`/`fatigue`/`repro_readiness`)으로
고정; **남은 것 = 각 drive의 갱신식·단위·range·cadence + P1 활성 여부.** options: (a) 전부 §6 갱신식(종 블록 `drives.<id>.update`)
(b) 고정 엔진 shape + rate-only 데이터(needs `decay_per_tick` 동형) (c) 하이브리드 — 누적형(hunger/fatigue/repro_readiness)
= rate 상수(D9 정신, 미래수치 필드 금지), 문맥결합형(fear ← predator scent intensity = needs `UpdateConditionalNeeds`
채널형 / thermal ← `apparent_temp` 편차 = §6) = set-from-context. range = [0,1] 정규화(needs 동형). **rec: (c)** — needs 기계와
동형으로 재사용·최소, 결합 있는 곳만 §6. **P1 활성:** hunger/fatigue/fear/repro_readiness 활성, **thermal = OFF**(F10 — climate
전 내성식 존재·중립), repro_readiness는 F9 타이머-respawn에 종속(자체 birth는 P_fa4). cadence는 F24 계층에 매핑(fear=매 틱,
thermal=Nt). **⚠ D5 가드:** drive ≠ agent Value(별 동기 기계 — 코드베이스 2번째 동기 기계, drift 위험). **OPEN.**
> **F43 정련 노트:** fear 의 set-from-context 입력은 단일 스칼라가 아니라 **2채널**(scent=원거리 약, sight=근거리 강) 일 수
> 있다 — F43 에서 확정. F25 의 "fear ← predator scent intensity" 는 F43 이 (scent → Wary 밴드, sight → Flee 밴드) 로 정련.

**F26 ★foundational — per-action utility 수식 형태.** F1 = 매 틱 후보 §6 점수화 → max(동률 ID). 남은 것 = 그 수식의 정확한
shape·피연산자·부착 단위. options: (a) **(종 × action)별 §6 utility `Program`** — 종 `fauna:` 블록의 `utility: {Graze:"<§6>",
Flee:"<§6>", Hunt:"<§6>"}` 맵, 각각 수치 반환, 매 틱 animal Context로 평가, 컨트롤러가 max(동률 = `actions.IDs()` 순) (b) 제네릭
`Σ drive_weight·action_drive_match` 닷프로덕트(종별 가중치=데이터, "어떤 action이 어떤 drive 충족"=엔진 로직) (c) action `Effect`·
drive 결핍 닷프로덕트(agent EffValue 기계 차용). **rec: (a)** — D3(평탄 점수 집합, 트리 아님)·D4(§6 데이터)·D10(content)·D5(agent
Value와 분리) 동시 충족; (b)는 drive↔action 매핑이 엔진 코드화 → D4 위험, (c)는 agent Value 기계 차용 → D5 위반. **후보 집합 =
utility 맵 키**(F28과 교차). **⚠ D3 가드:** utility는 *순수 점수*여야 함 — 수식이 서로 참조하거나 시퀀싱을 인코딩하면 손그림 트리 =
D3 위반. 동률·thrash는 §6 stickiness 항 또는 ID-타이브레이크로(F30). **OPEN.**

**F27 ★foundational — expr↔fauna 피연산자 브리지.** expr은 `Stat(StatID)`/`Attr(Tag)`/`Pred(name,Tag)`만 노출(대문자-bare
= Stat 검증, 소문자/점 = Attr 무검증 caller 네임스페이스). fauna utility/drive/apparent_temp 수식이 **drive 값·scent 셀
읽기·표적까지 거리**를 어떻게 참조? options: (a) **전부 소문자/점 Attr 피연산자**로 노출 — animal Context 어댑터의 `Attr(name)`이
`hunger`/`fear`/`scent.predator`/`scent.food`/`dist.food`/`dist.prey`/`apparent_temp`/`wind.dir`(F19) 등을 스위치; base 스탯은
`Stat(StatID)`(Strength/Agility…). **expr L0 불변**(Cm3 `tool:<family>.quality`·flora `moisture` 동형). (b) expr에 신규 메서드
(`Drive(id)`/`Scent(ch)`) — expr L0 변경, 단일 caller용, "callers adapt" 규칙 위반. (c) drive/scent/dist를 가짜 StatID로 Stat
채널에 밀어넣음 — Stat은 knownStats 검증·대문자 컨벤션이라 오염·D10 위반. **rec: (a)** — flora/decay/Cm3 선례 그대로, expr 불변.
**dist/scent 선결(닭-달걀):** `dist.*`/`scent.*`는 표적/방향이 *read 단계에서 먼저* 해소돼야 함(utility가 표적을 고르는데 dist는
표적을 요구) → 컨트롤러가 read 단계에서 채널별 최근접 scent 방향·거리를 미리 해소해 Context에 채움(expr 순수성 유지). **⚠ 누락
정책:** expr missing Attr → 0이라 오타 피연산자가 조용히 0 → `platform/config`가 fauna 수식 `ReadsAttrs()`를 fauna 피연산자
어휘와 로드시 교차검증(flora operand cross-check 동형). **OPEN — 이게 클러스터 2 스키마(F31)를 형성.**
> **F43/F44 추가 operand:** sight 채널·predator 근접이 도입하는 신규 Attr — `sight.predator`(전방 FOV 안 포식자 존재) ·
> `dist.predator`(포식자까지 거리, `dist.food`/`dist.prey` 평행) — 모두 같은 소문자/점 네임스페이스(F27(a)) 로 노출. F44 확정 시 확정.

### 클러스터 2 — entity + 종 콘텐츠 스키마

**F28 — fauna 원자행위 집합 + `actions.yaml` 신규 여부.** F1 = 공유 `engine/mind/actions` 후보; F19 = Graze/Flee/Butcher coin.
options: (a) fauna 후보집합 = **종 선언 ActionID 리스트(= utility 맵 키)**, 전부 **공유 `actions.yaml` 엔트리**(Graze/Flee 신규,
Hunt/MoveTo/Rest 기존; Butcher는 **agent** extract 행위로 동물 후보 아님) (b) fauna 전용 action 서브레지스트리 — F1/F5 공유 결정
위반, 기계 중복 (c) 기존만 재사용(Graze→Forage, Flee→MoveTo-away) — F19가 이미 distinct coin, tag 구분(D4) 상실. **rec: (a)** —
신규 공유 엔트리 `Graze`(초식 비파괴 flora 채집→hunger; `Forage` 평행·herbivore tag), `Flee`(predator scent 반대 이동; target
없음); `Hunt`(기존, predator→prey), `MoveTo`/`Rest`(기존). **Butcher = agent 행위**(carcass extract, F11/F12 — 동물 후보 아님).
**fauna는 gate/cost 기계 미사용**(F1 — planner 없음): fauna action `Tags`는 utility tag-match에만 쓰임(gates/planner 아님) — 확인
필요. **⚠ 골든 가드:** Graze/Flee가 공유 레지스트리에 들면 `actions.IDs()`/`Producers`/gate-match가 변해 **agent 골든 churn** →
P_fa1/P_fa2 outcome-중립 레버 보존 위해 **P_fa3 활성 시 추가**(의도적 재기준). **OPEN.**
> **F43 추가:** 신규 `Wary` 행위(저-commitment 경계: Graze 인터럽트·경계상승·feed에서 천천히 멀어짐)도 같은 공유 레지스트리
> 엔트리로 — F28 의 신규 추가 목록(Graze/Flee)에 `Wary` 합류, 동일한 P_fa3 골든 재기준 적용. 확정은 F43.

**F29 — `Animal` struct 필드.** F3 = drives + Stamina + 단일 vital + Pos; ToM/Value/Inventory 없음. sub-options: (i) base
stats = **full open `core.Stats` 벡터**(D7 — 역량 = §6 합성, 종이 값 선언) vs 축소 subset; **rec: full `core.Stats`**(D7/D10,
§6가 읽음). (ii) drives = **open `map[DriveID]float64`**(D10 — 신규 drive = 데이터, Stats 평행) vs 고정 struct; **rec: open
map**. (iii) 그 외 = `ID core.ObjectID`(spatial ObjectID 공간 공유) · `Species SpeciesID` · `Pos core.Vec2`(연속, D11) ·
`Stamina float64` · `Vital float64`(단일) · `Heading`(steering 방향) · `CurrentAction`(ActionID + 잔여, F30). **rec 종합:**
`Animal{ID, Species, Pos, Stats(open), Drives(open map), Stamina, Vital, Heading, CurrentAction}`. **⚠ D7 가드:** stat 단련/노화는
**cross-cutting stats/lifecycle 소관**(flora Dexterity 동일) — fauna는 §6로 *읽기만*, per-action 스킬 저장 금지. **OPEN.**
> **F44 노트:** `Heading`(F29(iii) 이미 포함)이 F44 의 전방 FOV 기준축이 된다 — sight 채널이 Heading-상대 전방 부채꼴을 본다(F44).

**F30 — durative 행위 commitment vs 매 틱 재중재.** F1 = 매 틱 horizon-1; 그러나 공유 action은 durative(Duration틱). 긴장.
options: (a) **매 틱 재점수** — 동기 drive가 여전히 높아 같은 action 재승리(자연 stickiness); 인터럽트(fear 급등) = max 전환;
thrash는 §6 stickiness 항/ID-타이브레이크; action Duration → `EffectPerMinute` rate 분모(commitment 아님) (b) Duration 동안
commit + 고우선 인터럽트 채널(fear>θ)만 선점 — durative 정합하나 commit+interrupt 상태기계(경미한 D3 냄새) (c) 동물 action =
틱당 micro-action(Duration=1) → 매 틱 재점수 정확. **rec: (a)** — F1 문자 그대로, durative 지속은 drive 지속에서 창발, 동률은
§6 stickiness(agent `Stickiness` 동형)/ID(D12). **⚠ D3 가드:** stickiness는 §6 항으로 — 손코딩 FSM 금지. **OPEN.**
> **F43 결합:** Wary↔Flee 의 "2단계"도 F30(a) 위에서 — fear 가 wary 밴드면 Wary 가 max, flee 밴드면 Flee 가 max(연속 fear 값이
> utility 임계 넘는 것일 뿐, 명시적 wary→flee 전이 FSM 아님 — D3 가드). F43 참조.

**F31 — `fauna:` 블록 스키마 + utility 부착(F26/F27 종속).** F6 = `objects.yaml` `fauna:` 블록. options(utility 부착): (a)
**종 블록 내 맵** `fauna: { stats:{…}, drives:{<id>:{…}}, utility:{<ActionID>:"<§6>"}, diet:[tags], senses:{radius, deposits:
[scent:*]}, products:[{item, base_qty}], size, apparent_temp:"<§6>" }`(flora `flora:` 블록 확장; 후보집합 = utility 키) (b)
utility를 `actions.yaml`에(종 무관) — 늑대≠사슴 Flee, utility는 본질상 종×action (c) 신규 `content/fauna.yaml` — F6 위반(flora가
flora.yaml 안 만든 것 동형). **rec: (a)**. `platform/config`가 각 수식 `expr.Parse`(utility/drive/apparent_temp = KindNum) +
피연산자(F27)·StatID·diet/product item-tag 교차검증; `fauna.Rules`로 컴파일(`flora.Rules` 동형). **⚠ 크기:** 블록이 크다 — module
SPEC이 ~400줄 넘으면 fauna 콘텐츠 스키마/`fauna.Rules` 컴파일을 별 concern으로 분리(CLAUDE.md split 규칙). **OPEN(F26/F27 해소 후
확정).**
> **F43/F44 종속 추가:** `senses` 블록이 **2채널 감지**로 확장 — `senses: { smell:{radius, …}, sight:{fov_arc, radius} }`
> (smell=omni scent 그리드 / sight=전방 FOV). utility 맵에 `Wary` 키 추가. apparent_temp 는 CA2/CA3(바람·단위) operand 에 종속.
> 정확한 스키마는 F43/F44 확정 후.

### 클러스터 3 — 냄새 그리드 구체화

**F32 — 그리드 해상도 + 셀 데이터 shape.** §1.1 = 단일 균일 보조 색인, 이진 채널 플래그. options(해상도): (a) `cellSize` =
**sense radius 비례**(balance 데이터, spatial `cellSize` 동형 — locality 최적) (b) animal max-speed×cadence 비례(틱당 ≤1셀 이동
보장) (c) spatial hash cellSize 재사용(그리드 1개로 통합). **rec: (a)** + (b)를 하한 제약으로(≥ max-speed·Ns, upwind steer가
셀을 건너뛰지 않게). 셀 데이터: 채널별 이진 플래그 → **`uint8` bitset**(`{scent:food, scent:prey, scent:predator}` = 3비트; F22
채널 확장 = 비트 추가). **⚠ D11 가드:** 그리드 = 색인, 동물 연속좌표·자기 셀만 읽음(스냅 금지). **OPEN.**

**F33 — deposit 규칙 + 바람-확산 알고리즘(D12 스텐실) + cadence Ns + 침착→읽기 지연.** §1.1: predator = 매 틱 자기 셀 deposit,
그 외 채널 확산 = bulk(Ns). options(확산): (a) **고정순서 스텐실 bulk 패스**(`tick % Ns`, 셀 정렬 1패스, downwind 이웃으로 플래그
전파, RNG 없음 — navmap wear/terrain 패스 동형) (b) deposit 시 즉시 반경 stamp(확산 패스 없음) — 바람 downwind 표현 불가 (c) BFS
거리장 — 스칼라 농도라야 의미(F21 이진과 불일치). **rec: (a)**. **바람 입력 계약(F21):** P1 바람 중립 → 플래그 국소(출처+인접만,
단거리); climate 출하 시 downwind 전파 + 거리 확장. cadence Ns = balance(F24); predator 채널만 매 틱(임박 회피). **⚠ deposit→read
지연(D12):** predator deposit(apply 단계)과 herbivore read(score 단계)의 **동틱 vs 차틱** — §1.1 "즉시"가 same-tick이면 score 전
predator pre-pass 필요, next-tick이면 깨끗한 스냅샷(1틱 지연). **rec: next-tick(1틱 지연)** — read = 이전 틱 그리드 스냅샷(navmap
동형, 결정성 명료); 임박성은 predator 매-틱 deposit으로 충분. (F41 와이어링과 교차.) **OPEN.**
> **CA2 와이어링:** 바람-확산의 "downwind 이웃" 은 climate `Wind{dir,mag}`(CA2) 가 결정 — world 가 climate `Wind` 를 이 bulk
> 패스에 먹인다(`docs/climate.md §1c` 통합 seam). `wind.dir` 단위(라디안 등, CA2)·`wind.mag` 강도가 스텐실 전파 방향·범위를 정한다.

**F34 — read/upwind-steer 규칙.** §1.1: 동물이 자기+이웃 셀 읽음 → 채널 플래그면 바람 따라 upwind(food/prey 접근), predator
반대(flee). options: (a) **이웃 8셀 중 켜진 셀 방향 평균 → steer 벡터**(바람 없을 때 coarse 방향; F23 출처 정체 불요) (b)
`wind.dir` operand만으로 upwind(바람 필수 — P1 바람 중립이라 무력) (c) 둘 결합 — 바람 있으면 upwind 우선, 없으면 이웃-켜짐 방향.
**rec: (c)** — P1(바람 중립)은 이웃-켜짐 coarse 방향, climate 출하 시 upwind homing 활성(thermal-OFF 동일 패턴). 결과 = utility
수식의 `dist.*`/방향 피연산자로 노출(F27). predator = 벡터 반전(Flee). **OPEN.**
> **F44 분기:** F34 는 현재 **omni 8-이웃** 읽기다. F44 가 이를 **2채널로 분기** — smell(food/prey/predator scent) 은 omni 유지,
> **sight 채널(전방 FOV)은 별도**. 즉 F34 의 omni 규칙은 *scent* 에 한정되고, sight 는 F44 의 Heading-상대 전방 부채꼴이 담당.
> **F34↔F44 결합:** F44 의 hybrid(c) 확정이 F34 의 적용 범위(scent-only)를 좁힌다 — 둘은 함께 resolve.

### 클러스터 4 — 산물 & 생애주기 훅

**F35 — steering/locomotion 갱신(max-speed·그리드 추종).** F14 = 연속 Pos + cheap steering(pathfind 없음, terrain passability
샘플만). options(max-speed): (a) **§6(base stats) 수식**(`speed = §6(Agility,…)`, 종 블록; D7) (b) 종 상수(balance) (c) drive
변조(fear→speed↑, fatigue→↓). **rec: (a)+(c)** — base speed = §6(D4/D7), fear/fatigue 변조 = §6 항; terrain passability는 navmap
`Passable`/`TerrainAt` 샘플(D11, 못 가는 지형 회피만, pathfind 없음). 이동 = steer 벡터 방향으로 speed·dt 전진(연속). **⚠ D11
가드:** 칸 스냅 금지. **OPEN.**

**F36 — 그리드 소유/배치(`engine/space` 확장 vs `engine/fauna` 서브모듈).** §1.1/F5: SPEC 시 확정. options: (a) **`engine/fauna`
서브모듈**이 그리드 소유(scent는 fauna 전용 보조 색인; world가 deposit/spread 패스 구동, navmap 동형) (b) `engine/space`(spatial
옆 신규 `scent`) — 범용 필드지만 현 유일 소비자 = fauna, 조기 일반화 (c) navmap에 채널 추가 — navmap = terrain cost 전용, 의미
혼입. **rec: (a) for P1** — fauna 단독 소비자라 fauna 소유가 응집적·D5; 후일 다른 소비자 생기면 `engine/space`로 승격(스칼라 농도
승격과 함께, frontier). world가 bulk 확산 패스 구동(유일 mutator, D12). **OPEN.**

**F37 — `carcass` 객체 스키마 + decay lot 매핑.** F11 = `carcass` 객체 + owner-agnostic decay lot(Dm4) + Butcher; 미추출 →
`rotten_matter`(W10). options(yield 표 위치): (a) **종 블록 `products:[{item, base_qty}]`; 사망 시 world가 `carcass` 인스턴스에
종 product 표 복사**(carcass 자기서술 — Butcher가 live animal과 분리) + `raw_meat` decay lot 생성 (b) 제네릭 `carcass` 1종에
고정 yield — 늑대≠사슴 carcass (c) 종별 carcass object_kind — kind 폭발, D10은 1 carcass kind 매개변수화 선호. **rec: (a)**.
carcass = object_kind(`mobile:false`) + 런타임 product 표 + decay lot(Dm5 `{kind, qty, decayAge=0}`); 미Butcher → decay가
`rotten_matter`로 transform(W10, decay 재사용 — 평행 시스템 금지). **OPEN.**

**F38 — Butcher extract 메커니즘.** F12 = 신규 extract 행위(`tool:cutting` gate, Dexterity §6 yield; Mine/Fell 평행, non-recipe).
options: (a) **Mine/Fell 평행 non-recipe 행위** — action-level `tool:cutting` tag, `target_kind:carcass`, `§6(Dexterity,
tool:cutting.quality)` yield 롤(Cm3 operand 재사용), carcass `remaining`/product 표 소진, 소진 → carcass 제거(object-mortality)
(b) recipe-mediated Craft로 — Butcher는 추출이지 조합 아님, FINAL recipe 모델 부적합 (c) Hunt에 흡수 — 사냥(죽임)≠해체(추출),
tag/행위 분리(D4). **rec: (a)** — Mine 동형(Xm4/Xm5 그대로): `tool:cutting` gate, Dexterity 수율, 보유 도구 durability wear
(world rate), carcass product 소진. **agent 행위**(F28 — 동물 후보 아님). hide/bone/sinew → material-tag item(W7/W8 공급).
**OPEN.**

**F39 — 번식 부트스트랩(타이머-respawn) 와이어링.** F9 = 타이머-respawn(`prey_respawn`) 부트스트랩, 비차단; 창발 birth = P_fa4.
options: (a) **기존 `balance.regen.prey_respawn` 타이머 재사용** — 사냥된 동물 인스턴스가 N틱 후 적합도-가중 위치에 respawn(현행
prey legacy 그대로, F18) (b) drive-gated 창발 birth를 P1에 — F9가 P_fa4로 명시 연기 (c) 고정 개체수 유지(죽으면 즉시 1:1 spawn).
**rec: (a)** — F9 확정 그대로, P1 비차단; respawn 위치 = 적합도-가중(F18, 라이브) / 픽스처(시나리오). repro_readiness drive(F25)는
P1엔 타이머에 종속(자체 사이클 P_fa4). **OPEN.**

**F40 — `apparent_temp` 수식 형태(climate-OFF 출하).** F10 = thermal drive bias + 지속초과 vital 손상; climate 전 thermal-OFF
(내성식 존재·중립). options: (a) **종 블록 `apparent_temp:"<§6>"`** = local climate 속성(`temperature`/`moisture`/`wind`) +
동물 자기 속성(size·base stats)의 §6 → 수치; thermal drive가 이 값 편차로 bias(F25) (b) 엔진 고정 shape + 계수 데이터 (c) climate
미출하라 P1엔 아예 필드 없음. **rec: (a)** — §0-3 체감온도 = per-entity §6 정합(D4/D10); climate 입력은 **입력 계약**(P1 중립값 →
thermal 거동 0, decay/flora 동형); climate 출하 시 활성(P_fa4, '겨울' = 지속저하 창발). `platform/config`가 `expr.Parse`(KindNum).
**OPEN.**
> **CA2/CA3 결합(cross-doc):** apparent_temp 가 읽는 operand `temperature`/`moisture`/`wind.dir`/`wind.mag` 는 climate 가 **생성·
> 노출**(`docs/climate.md §1c` CA2 바람 생성·CA3 단위/노출). 특히 **CA3 단위 fork**(정규화 [0,1] vs °C) 는 이 수식이 무엇을 읽는지를
> 정하므로 **F40 과 CA3 는 한 번에 사람이 결정**해야 한다(operand 명칭 `temperature`·`wind.mag` 의 단위가 두 문서에서 동일해야 함).
> 바람 항이 apparent_temp 를 낮추는 "wind chill" = `apparent_temp = §6(temperature, wind.mag, size, …)` — 식은 fauna 소유, 값은 climate.

### 클러스터 5 — 통합 + 어휘

**F41 — 컨트롤러↔world 틱 와이어링(결합 ID apply 순서 + scent bulk cadence) 구체 shape.** F16/F24 = 매 틱 interleave + 고정
결합 agent+animal ID apply 순서, per-step rng fork, bulk cadence. options: (a) **world가 단일 read→score→intent→apply 루프에
animal 합류** — score(병렬 read-only, animal Context 빌드 + utility max) → intent 수집 → **apply(agent+animal 통합 ObjectID
정렬 1순서, D12)**; scent 확산 = `tick % Ns` bulk 패스(F33), per-step `fork(tick)` RNG(climate 동형), world가 ObjectID 발급 +
spatial Insert/Move/Remove (b) animal 별도 루프(2-패스) — 결합 순서(F16) 위반 (c) fauna 자체 apply — world 유일 mutator(F5)
위반. **rec: (a)**. apply 순서 = agent+animal **하나의 정렬된 ObjectID 시퀀스**(타이 ID, D12); scent deposit = apply 직렬(F33
next-tick 지연), 확산 = bulk; spawn/move/die 델타 = F17. **⚠ architecture.md:** `engine/fauna`가 stage6(agent 옆)·world(stage7)
와이어링 — **반영 미시행**, SPEC 착수 직전 사람 확인(§3 DAG 영향). **OPEN.**
> **CA2 와이어링:** 같은 world 루프가 climate `Wind`(CA2)를 ① scent 확산 패스(F33) ② animal Context 의 `wind.*` operand(F40)
> 양쪽에 주입한다 — `docs/climate.md §1c` 통합 seam 의 fauna 측. climate `Wind` 생성은 climate step cadence(CA2), 소비는 여기.

**F42 — 이번 라운드가 굳히는 신규 glossary 용어.** F19 채택 위 추가 식별자(SPEC가 도입): `SpeciesID`(fauna 종, flora `SpeciesID`
동형) · `DriveID`(open drive 키, StatID 평행) · `fauna.Rules`(컴파일된 §6, `flora.Rules` 동형) · utility 피연산자 `hunger`/`fear`/
`thermal`/`fatigue`/`repro_readiness`·`scent.food`/`scent.prey`/`scent.predator`·`dist.food`/`dist.prey`·`apparent_temp`·
`wind.dir`(F27 어댑터 네임스페이스) · `Heading`(steering) · scent 그리드 `cellSize`(balance). **rec:** F19 동기화 단계에서 일괄
`glossary.md` 등재(별도 단계 — 여기 coin 확정만). **OPEN(어휘 확정).**
> **F43/F44 추가 coin:** 행위 `Wary` · operand `sight.predator`/`dist.predator` · senses 하위 `smell`/`sight`(+`fov_arc`) ·
> (cross-doc, climate CA2) `wind.mag`. 같은 glossary 동기화 단계에 합류. **OPEN(어휘 확정).**

### 클러스터 6 — 2차 라운드: 2단계 포식자 반응(smell↔sight) + 방향성 시야 (F43~F44)

> **3차 게이트(2026-06-26).** 사람 Phase-1 의도: "포식자 *냄새* 칸에 들면 WARY, 포식자가 *가까이* 오면 FLEE; 포식자가 동물의
> **3-방향(heading 기반) 시야** 칸에 들면 FLEE." 이는 F25(fear)/F28(action set)/F34(읽기)/§1.1(감지)를 **정련**하는 2차 detail —
> SPEC 작성 전 사람 확정 필요. 전부 옵션+추천+`OPEN`. **결정 금지·발명 금지.** F25/F28/F30/F34 와 교차(위 인라인 노트 참조).

> **RESOLVED — 사람 확정 (2026-06-26):**
> - **F43 = `(a) 단일 fear drive + 2입력 채널`** (scent → Wary 밴드 / sight → Flee 밴드) + **신규 공유 `Wary` 행위**(feed 인터럽트·경계·천천히 edge away). 순서 `Flee > Wary > Graze` 는 §6 fear-band utility(연속값이 임계 넘는 것일 뿐, wary→flee FSM 금지 — D3). 골든 churn → P_fa3 활성 시 추가. F25(fear)/§1.1 은 이 2채널로 정련.
> - **F44 = `(c) 하이브리드` + `(c-ii) continuous bearing`** — smell = omni scent 그리드(조기경보 → Wary, F34), **sight = spatial-hash 포식자 엔티티 질의 → 상대 bearing 이 `Heading ± fov_arc` 안이면 → Flee**(D11-정합 연속 시야, 채널 깨끗 분리). 신규 operand `sight.predator`(1/0) + `dist.predator`. **F34 의 omni 규칙은 scent-only 로 좁아짐**(F34↔F44 동시 확정). `fov_arc` = balance 데이터.

**F43 — Wary↔Flee 2단계 포식자 반응(smell vs sight).** 사람의 2단계 = **2 감지 채널**에 깨끗이 매핑: **포식자 SCENT(omni 그리드,
중거리) → WARY**, **포식자 SIGHT(heading 전방 FOV, 근거리) → FLEE**. 남은 것 = (i) `fear` 가 단일 drive(근접도로 스케일)인가 2신호인가
(ii) 신규 `Wary` 행위 정의 + F28 집합/utility 순서 (iii) F25/§1.1 정련.
- (i) fear 구조 options: (a) **단일 `fear` drive + 2입력 채널** — scent 존재 → fear 가 *wary 밴드*(낮음)로, sight/근접 → *flee 밴드*
  (높음)로 set-from-context(F25(c) 채널형). utility 임계: `fear ∈ [waryθ, fleeθ)` → Wary 가 max, `fear ≥ fleeθ` → Flee 가 max.
  drive 1개·입력 2개로 최소·needs `UpdateConditionalNeeds` 채널 동형. **rec.** (b) 2개 분리 drive(`wary`+`fear`) — 분리는 깨끗하나
  drive↑·이중계산 위험·스키마↑. (c) drive 없이 utility 가 scent/sight operand 직접 읽음 — F25 가 fear 를 drive 로 이미 확정, bypass 비정합.
  **rec: (a) 단일 fear + 2밴드.**
- (ii) `Wary` 행위 options: (a) **신규 공유 `actions.yaml` 엔트리**(F28 Graze/Flee 와 함께) — §6 utility 가 `scent.predator` 로 점수;
  거동 = feed 인터럽트 + scent 반대로 *천천히* edge away/경계(저 commitment, full flee 아님). **rec.** (b) fear-bias 된 Graze(신규 행위
  없음) — 사람이 원한 distinct WARY 상태 상실. **rec: (a) 신규 Wary 행위.**
- utility 순서(F26 위 §6 점수, FSM 아님): predator scent 만 → `Flee < Wary`, `Wary > Graze` (feed 인터럽트하되 도망은 아직). predator
  sight(근접) → `Flee > Wary > Graze` (전면 도주가 선점). 이 순서는 **연속 fear 값이 utility 임계를 넘는 결과**일 뿐 — 명시적 wary→flee
  전이 코드 금지(F30(a)/D3 가드). 동률·thrash = §6 stickiness 항/ID(F30).
- (iii) F25/§1.1 정련: F25 의 "fear ← predator scent intensity" 를 **2채널**로(scent=원거리 약/Wary, sight=근거리 강/Flee); §1.1-3 의
  "predator → 즉시 flee" 를 **scent → Wary, sight-근접 → Flee** 로 정련(F44 의 채널 분기와 일관).
- **⚠ D3 가드:** Wary/Flee 는 horizon-1 utility *순수 점수*(fear-밴드)로 선택 — wary→flee 손그림 상태기계 금지(F26/F30 정신). **⚠ 골든:**
  Wary 공유 레지스트리 추가 = agent 골든 churn → P_fa3 활성 시 추가(F28 동형). **OPEN.**

**F44 — 방향성(heading 기반) 시야 vs F34 omni 읽기.** F34 는 현재 omni 8-이웃 셀을 읽는다. 사람은 **전방 3-방향 FOV**(뒤 사각지대)를
원한다. 남은 것 = sight 채널이 어떤 기하로 포식자를 감지하나.
- options: (a) **omni 8-이웃 유지**(현 F34) — 최단순이나 사각지대 없음·사람 의도 불충족. (b) **전방 3-셀 FOV 로 *모든* 감지**(Heading-상대
  셀 선택, F29 `Heading`) — 통일되나 *냄새는 본디 무방향*(후각은 facing 무관)이라 scent 를 방향화하면 틀림. (c) **하이브리드 — smell = omni
  scent 그리드(조기경보/Wary), sight = 전방 3-셀(근접/Flee)** — F43 의 2채널과 자연 정합. **rec: (c) 하이브리드.**
- **(c) 하위 OPEN — "전방 FOV" 의 정확한 기하:** 동물은 연속좌표·연속 `Heading`(D11) 인데 사람은 "칸" 으로 말함. 두 구현:
  - **(c-i) cell-based:** 8-이웃 중 Heading 전방 부채꼴(예: Heading 방향 셀 + 좌우 대각 2 = 3셀; 뒤 5셀 = 사각)의 `scent.predator` 를 읽음.
    그리드 재사용·새 쿼리 없음; 단 sight 가 scent 그리드를 빌려 씀(omni 조기경보 scent 와 같은 필드를 방향만 달리 읽음 — 채널 의미 약간 겹침).
  - **(c-ii) continuous bearing:** spatial hash 로 `sightRadius` 안 포식자 *엔티티* 질의 → 각 포식자의 **상대 bearing 이 Heading±arc 안**
    인 것만 → Flee. D11-정합(연속 시야, 칸 무관)·channel 깨끗 분리(sight≠scent 그리드); 단 spatial 쿼리 1개 추가. **rec(약): (c-ii)**
    (정직한 시야·채널 분리) — 단 추가 쿼리 비용은 사람 확인. `fov_arc`(반각, 예 90°=전방 3셀 상당) = balance 데이터.
- 결과 노출(F27): sight 채널 = operand `sight.predator`(전방 FOV 안 존재 1/0) + `dist.predator`(근접도) → Flee utility(F43) 가 읽음.
  scent 채널은 F34 omni 그대로(food/prey homing + predator 조기경보/Wary).
- **⚠ D11 가드:** cell-based 든 continuous 든 동물은 연속 Pos+Heading 유지·칸 스냅 없음. **⚠ D12:** FOV 셀 선택/bearing 테스트는
  고정순서·결정적(map-순회 로직 금지). **⚠ F34 결합:** F44(c) 확정이 F34 의 omni 규칙을 *scent-only* 로 좁힌다 — **F34/F44 함께 resolve.**
  **(RESOLVED — 클러스터 6 상단 블록.)**

### 클러스터 7 — 적응형 cadence · 수영 · 성향 반응 (F45~F46 + 개정) — 사람 확정 (2026-06-27)
> R1~R5 RESOLVED. `backend/engine/fauna/SPEC.md`에 반영됨. F16/F24/F29/F30/F35/F41 개정·§1.2 glossary 추가.

- **F45 — 적응형 per-animal cadence (R1; F16/F24/F41 개정).** 동물 = DORMANT/ACTIVE 2상태. **DORMANT**: `(tick + phase(ID)) % N == 0` 일 때만 풀 재중재(N≈100 balance, `phase`=ID 파생 부하분산), 사이 틱엔 `CurrentAction` 유지 + 싼 steering. **ACTIVE**: 매 틱. **포식자 항상 ACTIVE.** **깨우기**: 매 틱 모든 동물이 자기 칸 predator-scent 비트 **O(1)** 읽음 → 켜지면 그 틱 ACTIVE(+쿨다운=balance). 결정성 = ID-phase·순수 읽기·고정순서·map-순회 금지. cadence 로직은 fauna `Step` 안(world는 매 틱 호출, apply 순서는 F41).
- **F46 — 성향(disposition) 기반 agent 반응 (R4; #4 이진판 supersede; P_fa3).** 초식 sight 가 agent 감지 → **`agent.disposition` = 부호 §6(agent 실 base stat; rec `Sociability − Aggression − Vindictiveness`, 계수=balance/content, D4)** → 양수=stay(fear 0) / 음수=flee(fear↑) via F43 fear 채널. ToM 아님(실 스탯, F3), '사냥꾼' 창발(D2). F8(포식자→agent Safety) 방향 불변. 세부 §6·operand 확정 = P_fa3.
- **F35 개정 (R2):** `TerrainSampler{Passable, Cost}` (옵션 b). **물 = 벽 아님, 고비용 통과 = 수영**(steering 진입 가능; Passable=false 는 진짜 blocker[벽/footprint]만). 수영 Stamina 소모·익사 risk(W1 동물판)는 §7/lifecycle 로 이연.
- **F29 정렬 (R3):** `Animal.Stats` = `map[core.StatID]float64` (inline; fauna 는 `stats` import 안 함, D7 읽기전용).
- **F30 (R5):** `is_current` §6 Attr operand 채택(현재 행동=1.0 / else 0.0) — ACTIVE-모드 stickiness(anti-thrash; FSM 금지, D3). `AttrOperands()` + glossary.
- **glossary(§1.2/F42) 추가:** `is_current` · `agent.disposition`.

### 클러스터 8 — scent 승격 + 스칼라 강도 + 발생원 태그 (F21/F22/F36 개정) — 사람 확정 (2026-06-27)
> scent를 fauna 전용에서 **공유 월드 색인**으로 올리고 이진→스칼라로 개정. SPEC: `backend/engine/space/scent/SPEC.md`.
- **① scent 승격(F36 개정):** `engine/fauna/scent` → **`engine/space/scent`**(spatial/navmap kin, `core`만 의존). **world 소유**(fauna 아님); world가 `scent:<channel>` 태그 단 발생원에서 침착, **fauna는 읽기만**. fauna import에 `engine/space/scent` 추가.
- **② 발생원 태그 + perception 공존:** 냄새원 = `scent:<channel>` 태그(+magnitude) — flora=`scent:food`, prey=`scent:prey`, predator=`scent:predator`, (후속) carcass/rot=`scent:carrion`. **`perception.Smell`(agent per-entity gradient)와 공존** — 같은 발생원 태그 공유(무엇이 냄새나나 1회 author). (perception을 그리드로 통합은 frontier.)
- **F21 개정(이진→스칼라 강도):** 셀당 채널별 `float64≥0` 농도(magnitude 비례·거리/바람 falloff). `Deposit(ch,pos,intensity)`·`Spread`=농도 diffusion·`Read`=Intensity+gradient·`IntensityAt`(F45 wake=intensity>threshold). `scent.<ch>` operand=**스칼라**(이진 아님). perception.Smell strength와 개념 통일.
- **glossary 추가:** `scent:food`/`scent:prey`/`scent:predator`(/`scent:carrion`) 발생원 태그.

### 클러스터 9 — 포식 전투 & 사체(Combat & Predation) — 사람 확정 (2026-07-01)
> F7(창발 eat/death)·F11(carcass=decay lot)·F22(채널 확장)을 **구체 전투 메커니즘**으로 정련. 기존엔 "Hunt→즉사"로
> 추상화돼 있어 **공격 액션·공격력·교전(engaged) 상태가 없었다** — 이 클러스터가 그 갭을 메운다. 대상 SPEC:
> `engine/fauna/SPEC.md`(코어) + `SPEC-world-fauna.md`(apply/사망/feed) + scent/decay SPEC(소폭). **구현 = 우리 빌드
> 순서 phase 6b**(활성화 phase 6과 독립). D2/D3/D4/D7/D10/D12 가드는 §3 준용.
- **FC1 전투 액션 = 신규 공유 2개** `Attack`+`Feed`(접근=기존 `TagSteerPrey` steer, 신규 아님). `Attack`=engage+exchange 겸용(쿨다운 게이트), `Feed`=carcass 소비(hunger↓, **durative**). 둘 다 `actions.yaml` 공유 엔트리 → horizon-1 utility로 선택(D3, 손그림 FSM 금지). 골든 churn → P_fa3 활성 시(F28/F43 동형).
- **FC2 대상/포식자끼리** — 표적 = attacker `diet` 멤버(F7 tag). **포식자↔포식자는 hunger 극단일 때만**: 별도 코드 규칙이 아니라 `Attack` utility가 hunger를 **fight-cost/위험**과 저울질(D2/D4 창발). 신규 operand **`target.threat`**(표적 위험도, 반격 예상) 노출 → 평소 predator 표적은 utility 낮음, hunger↑가 극복.
- **FC3 타이밍** — engage **시도 쿨다운 [50,100]틱**(lock 전), 성공 시 engaged; **exchange 쿨다운 [10,20]틱**마다 데미지. 둘 다 seeded `envFork` 범위추출(balance min/max). 하드타이머 아님 — 쿨다운-게이트 utility.
- **FC4 공격력/명중 = §6(D4/D7)** — `attack_power = f(Strength…)`, `hit = f(공격 Agility vs 방어 Agility)`(스탯 조합, 개별 스킬 저장 금지 — 매 틱 base 합성). 데미지 → 표적 Vital↓. content §6(speed/apparent_temp 동형).
- **FC5 반격** — prey 반격 없음(자기 Vital만↓); 포식자↔포식자(FC2)는 양쪽 각자 `Attack`.
- **FC6 이탈(disengage)** — 포식자 **stamina 저하** OR 표적이 **`disengage_range`(~2 셀 = 2×navmap cellSize) 이상** 벌어지면 lock 해제. **시나리오 #8("포식자 스태미나가 먼저 떨어지면 멈춤")이 여기서 창발**(utility/거리 파생, FSM 아님).
- **FC7 Vital 재생/흉터** — 느린 재생(balance `vital_regen`), **완전 회복 불가**: 누적 피해가 **max Vital에 영구 소량 페널티**. 신규 Animal 상태 `VitalCap`(≤1.0, 전투마다 소량↓); 재생은 `VitalCap`까지만.
- **FC8 Feed/포식** — 표적 Vital=0 → 사망 → carcass(FC9). 포식자 `Feed`(durative) = carcass supply에서 hunger↓, **회복량 = 먹이 체격 비례**(content/§6). F11의 agent-`Butcher`(재료)와 **공존**(같은 carcass: predator=Feed/hunger, agent=Butcher/재료).
- **FC9 사체 = decay lot (F11 풀버전 확정)** — 사망 → `carcass` 객체 + **owner-agnostic decay lot(Dm4)** **런타임 생성**. decay 상태 fresh→rotting→bones→gone, 각 supply(Feed 먹이값)+transform(bones/hide/sinew, W7/W8). 미소비분 부패(W10). ⚠ **decay 모듈에 런타임 lot 추가 API 필요**(현 `New(lots)` init 전용 — §modification).
- **FC10 carrion 냄새 (F22 채널 확장 확정)** — carcass = `scent:carrion` 태그 → world가 `ChanCarrion` 침착(**tag-driven 경로가 그대로 처리**; scent 모듈에 `ChanCarrion`+`Reading.Carrion` 추가). scavenger/포식자가 `scent.carrion` operand로 homing(선택 거동).
- **FC11 공포 연동(창발)** — kill 목격/carrion 근접(피 냄새) → 주변 prey `fear`↑(F43 채널). content §6.
- **FC12 상태/결정성(D12)** — 신규 `fauna.Animal` 필드 `EngagedWith`·`NextExchangeTick`·`EngageCooldownUntil`·`VitalCap` + 직렬화(`state.go`). 교전=양방향 관계 → 한 apply에서 두 동물 **일관 갱신·id순**, 같은표적 충돌은 기존 combined-conflict 재사용. 모든 랜덤=seeded `envFork`.
- **FC13 전투 중 이동** — engaged면 locomotion 억제(제자리 육박); prey는 fear로 **이탈(거리 벌리기)만** 시도.

> **⚠ 이 클러스터가 요구하는 phase 1~5(빌드 완료 모듈) 수정 — 검증 결과 (2026-07-01):**
> - **scent**(`engine/space/scent`): `ChanCarrion` 추가(enum + `Reading.Carrion` + `Sense` 매핑). 현 `NumChannels=3`→4.
> - **decay**(`engine/env/decay`): **런타임 lot 추가 API**(현재 `New(lots)` init 전용, `Step`도 신규-lot 입력 없음) — 사망 시 carcass lot 주입 경로. carcass item kind decay states(content).
> - **world**(`engine/world`): `applyAnimalIntent`에 **cross-animal 데미지 apply**(현재는 행위 주체만 변경); 사망→carcass(decay lot+object) 생성; `Feed`→hunger; engaged 양방향 갱신; `scentChannelFromTag`에 `carrion` case 추가; carcass를 `decayEnvInputs`에 편입.
> - **config**(`platform/config`): `fauna:` 블록 전투 §6 필드(attack/hit/engage/feed) + `target.threat`를 `AttrOperands`에; carcass decay states 파싱. (`ScentEmitters`는 `scent:carrion`을 자동 처리 — 무변경.)
> - **fauna**(`engine/fauna`): 위 FC1~FC13 코어(신규 액션·engaged 상태·operand·disengage·§6) — 최대 변경.

---

### 클러스터 10 — 포식자-피식자 리얼리즘 (M1~M6) — 진행 중 (2026-07-02)
> **원칙(사람 확정):** 포식자 속도를 올려서 잡지 않는다. 도망자(prey)가 포식자와 **같거나 약간 빠르게** — prey는
> **속도·은신·지형**으로, predator는 **매복·스태미나·몰이**로 생존. 목표: 측정 가능한 **~15% 포식 성공률**로 함께
> 튜닝(balance.yaml + `tools/tuner`). D2/D3/D4/D7/D10/D11/D12 가드 §3 준용(은신/지형/매복은 **창발**이어야 하며
> per-species FSM/속도·비율 하드코딩 금지). 배경: `docs/scenarios-world.md` FA1~FA3, memory `live-emergence-underseeded`.

**확정(Decisions locked) — 구현 완료:**
- **M1 — prey-경쟁 속도 baseline (RESOLVED, 커밋 3948b86):** 포식자 속도 상향 revert. §6 speed는 공통 baseline +
  Agility 파생(prey Agility↑ → 자연히 대등~우세). 개별 종 속도 하드코딩 없음(D4/D7).
- **M2 — 추격 피로 (RESOLVED, 커밋 3948b86):** `applyAnimalFatigue` — `effort:high`(추격) 시 fatigue↑,
  `effort:none/low` 시 회복. speed §6가 fatigue를 감산 → 장기 추격 시 포식자 감속(스태미나 창발, FC6 동형). rate = balance cadence.
- **맵 저작 — cover flora (RESOLVED, 2026-07-02):** `starter_village.fixture.yaml`에 **숲(oak 클러스터)·덤불(berry_shrub
  thicket)** 저작 + `oak`/`berry_shrub`에 **`cover` 태그**(glossary 등재). 숲은 지형 타입이 아니라 flora 클러스터로
  창발(D11). WEST/EAST WOODS + riparian gallery + 토끼밭 thicket. **`cover` 태그는 M3 전까지 INERT**(거동 0 변화; 스모크·전체 스위트 GREEN).

**Open questions (사람 확정 필요 — resolve 전 SPEC/구현 금지, 발명=결함):**

*M3 — cover 은신(cover-hiding). 사람 의도: "prey가 풀숲에 들어가면 일정 확률로 ~100틱 안 보이게". **RESOLVED 2026-07-02 (추천대로) → SPEC 착수·빌드 중.***
- **M3-a 발동 조건 — RESOLVED: (ii) `flee`(선택 steer=`flee:predator`) 중일 때만** 판정.
- **M3-b 확률·지속 — RESOLVED: (ii) §6 파생 확률**(prey 종별 `hide_chance` §6, 예 `0.5 + Agility*0.005`, D4/D7) + 지속 N=balance `hide_duration`(기본 100).
- **M3-c hidden 의미 — RESOLVED: (ii) sight+`scent:prey` 둘 다 제외**(완전 은신). **flush 반경**(`hidden_flush_factor`×scent cell) 안으로 predator가 들어오면 즉시 발각/교전 가능(코앞 은신 방지).
- **M3-d 해제 조건 — RESOLVED:** N틱 만료 ∨ engaged(교전 시 clear) ∨ predator flush 반경 진입. (crouch = hidden 중 제자리 웅크림 → 스스로 cover 이탈 안 함.)
- **M3-e 상태/결정성 — RESOLVED:** 신규 `fauna.Animal.HiddenUntil`(tick, 직렬화 FC12 동형); 진입 roll = world apply의 seeded `envFork(tick,"fauna")`; cover flora 조회 = world 객체 근접(`nearCoverFlora`, `nearForageFlora` 동형; 연속좌표·칸 스냅 없음, D11).
- **M3-f cover 종 집합 — RESOLVED:** `cover` 태그 = `oak`+`berry_shrub`(grass 제외). 태그로 조정 = content(D10).

> **SPEC design (2026-07-02, 게이트 resolve로부터 유도 — 발명 아님).** **단일 writer = world**(이미 flora 근접·per-species §6 eval·seeded roll·per-animal scent deposit을 apply에서 수행 — `applyAnimalGraze`/`nearForageFlora`/`Rules.Graze`/`depositAnimalScent` 동형), **fauna는 `HiddenUntil`을 읽기만**. ① world `applyAnimalHiding`: prey∧선택 steer=`flee:predator`∧非engaged∧非hidden∧`nearCoverFlora` → `Rules.HideChance` §6 roll(fauna fork) → `HiddenUntil=tick+hide_duration`; engage 시 clear. ② world scent deposit: `HiddenUntil≥tick`인 prey는 `ChanPrey` deposit skip(냄새로도 안 보임). ③ fauna `combatTarget`: `other.HiddenUntil≥Tick`인 후보 skip(단 `dist≤flush`면 발각). ④ fauna 조향: 자신이 hidden이면 crouch(NextPos=Pos), predator가 flush 반경 안이면 bolt. **신규 operand 없음·Snapshot seam 없음·Intent 필드 없음**; `cover` 태그·`hide_chance` §6·`hide_*` balance 키가 OFF-neutral 레버(prey에 hide_chance 없으면 은신 0 → 기존 골든 불변).

*M4 — 지형 차단/회피(terrain-block). 사람 의도: "나무·강·지형이 추격을 막게". [M4-a는 M3와 병행 가능]*
- **사실 확인(2026-07-02):** fauna 이동은 **이미** per-species `TerrainCost`/`Impassable`을 읽는다(`fauna/step.go`·`cheap.go`: 유효비용=BaseCost×종mult, impassable=스텝 거부). ⇒ **강/산/바다 방벽 = 신규 메커니즘이 아니라 content terrain_cost 튜닝**.
- **M4-a 강 비대칭(content) — 완료·커밋 `42954fa`(2026-07-02):** wolf river 1.4→3.0, bear 1.5→2.6, deer 1.8→1.5(건너 도망), rabbit/goat/fish 유지. 순수 content(W10b). arena 측정 hunt-success ~11–12.5%(목표 범위 내).
- **M4-b 숲/풀숲이 추격을 늦춤 — RESOLVED (i), 사람 확정 2026-07-02: cover flora 이동 저항(감속+변주, 넘어짐 없음).** 사람 의도: "숲·풀숲 장애물에서 계속 변속, 넘어지진 않되 속도 변주". **설계(M3와 동일 world-side 패턴, 발명 아님):** world가 매 틱 이동량을 cover 저항으로 스케일 — `resistance = 1 + coverDensity(pos)×종 cover_cost`, 실제 이동 = `Pos + (NextPos−Pos)/resistance`(≥1이므로 감속만, 정지/낙상 없음). `coverDensity(p)` = 주변 `cover` flora 각 겹침(중심→가장자리 선형감쇠, 반경=`cover_radius_factor×Width`) 합 → **공간적으로 peaky → 이동 중 저항 오르내림 → 속도 변주(RNG 없이 결정적, D12)**. 비대칭 = 종별 `cover_cost`(D10 content, rabbit 0.3 < deer/goat 0.6~0.7 < wolf 1.2 < bear 2.0). **단일 writer=world**(flora는 world 소유; `depositFloraScent`/`nearCoverFlora`/`kindIsCover` 동형), fauna는 `Rules.CoverCost(species)`만 노출. OFF-neutral: cover flora 없거나 cover_cost=0 → resistance=1 → 골든 불변. (ii)"은신만으로 처리"는 측정 결과 M3가 dilute fixture에서 잘 안 터져 기각; (iii) 물리 장애물=park.

*M5 — 매복·감지(ambush/detection). M3/M4로 prey가 어려워지면 predator 성공 레버. [M3/M4 착지 후]*
- **사실 확인(2026-07-02):** fauna 감지는 **반경+FOV**(`sightQuery`: SightRadius/fovArc + spatial NearbyEntities)이며 **shade/LoS 미반영**(perception full-LoS 동물 지각 = park). ⇒ shade(어두운 숲)로 매복이 **자동 창발하지 않는다**.
- **M5-a prey 조기감지 튜닝(content):** prey `senses`(sight 반경/FOV) vs predator 은밀성 = content senses 튜닝. 신규 메커니즘 아님. 추천 적용.
- **M5-b 매복 메커니즘:** (i) **fauna 감지에 shade occluder 반영**(park 승격 — 숲 안 predator가 prey sight에서 감쇠 → 매복 창발; 정합적이나 큼) / **(ii) cover-근접 피탐지 반경 축소**(M3 hidden과 통합한 근사 — predator가 cover 안이면 상대 sight 반경↓). 추천 **(ii)** — 저비용, M3와 한 메커니즘. (i)는 후행.
- **M5-c 몰이(cornering):** prey를 강/바다/경계로 몰면 실패 — 위치+지형 창발(강+bounds+M4). **별도 코드 불필요**(창발 유지). 추천.

*M6 — 관성·저크(inertia/momentum). [후행 phase — M3~M5 착지 후]*
- **M6-a:** 이동에 관성/급회전 페널티 — prey turn_rate↑(juke), predator 관성↑(오버슈트). content §6 `turn_rate`/`accel` per-species. 추천 content §6.

**균형(공통):** M3~M6 착지 후 balance.yaml + `tools/tuner`로 **~15% 포식 성공률** 타겟 튜닝. 측정 시나리오: 토끼-늑대,
사슴-늑대/곰(스모크 digest 확장 or scenario fixture). 종별 성공률·평균 추격시간·개체군 안정성 로그.

---

## 2. Phases — (각 phase 독립 shippable + 테스트 + 결정성 골든; `flora.md §2`/`climate.md §2` 양식)
> **모든 §1 게이트 RESOLVED ✓** → phase의 남은 실제 선행은 **Open question이 아니라 선행 leaf 빌드**다:
> `engine/kernel/expr`(§6 — drive·utility·체감온도 내성·yield 수식) + `engine/env/decay`(P_m2 — 사체 lot 부패). 둘 다 READY/다음 leaf.
> **⚠ 2차 게이트(§1.3 F25~F44):** SPEC-design 세부는 **OPEN** — module SPEC 작성 전 사람 확정 필요(특히 ★F26/F27, 그리고 F43/F44).
> **핵심 안전 레버:** P_fa1~P_fa2는 outcome-중립(종 미배치 → 거동 0 변화; 현행 `prey` 타이머-respawn legacy 유지)
> → 기존 world 골든 불변. **P_fa3에서만** 의도적 재기준(climate/flora staging 동형).

- **P_fa1 — `engine/fauna` 축소 반응 컨트롤러 + 단일 냄새 그리드 + fauna-OFF 출하(outcome-중립)**
  horizon-1 utility arbitration(drives + 실제-스탯 §6; planner/ToM 없음, F1/F2/F3), cheap steering 이동(F14),
  **단일 균일 냄새 그리드 감지**(F20~F24 — 이진 채널 플래그·근접 읽기; 바람=중립), 공유 `engine/mind/actions` 후보 점수화
  → 단일 intent(동률 ID). **종 미배치 → intent 0 → 거동 불변.** 테스트: utility 동률-ID 결정성, drive 통합, seeded
  steering 재현, 냄새 그리드 침착/읽기 결정성·중립, 공유-레지스트리 점수화, fauna-OFF 중립, import/literal 가드
  (actions/expr/spatial만; world/agent import 금지).
  **선행(RESOLVED ✓): F1·F2·F3·F4·F5·F14·F20·F21·F22·F23.** 빌드 선행: `engine/kernel/expr`.
  **⚠ SPEC-design 선행(OPEN, §1.3): F25·★F26·★F27·F28·F29·F30·F31·F32·F33·F34·F35·F36·F42 + F44(sight 채널 분기·FOV 기하).**

- **P_fa2 — `world` 와이어링(cadence + 통합 apply 순서 + 냄새 bulk 패스 + spawn/move/die 델타) — 여전히 outcome-중립**
  world가 fauna 컨트롤러를 통합 read→score→intent→**apply(고정 결합 agent+animal ID 순서, D12)**에 합류; 냄새
  확산 bulk 패스(`tick % Ns`, F24) + per-step rng fork + ID 발급. **종 여전히 미배치 → 거동 불변(중립 회귀 가드).**
  와이어링·cadence·fork·apply-순서·냄새 패스 결정성만 검증. **선행(RESOLVED ✓): F4·F16·F24.**
  **⚠ SPEC-design 선행(OPEN, §1.3): F33·F41.**

- **P_fa3 — 활성: 야생 단독 prey+predator + 사체→재료 + 골든 의도적 재기준**
  `content/objects.yaml`에 `fauna:` 종 활성(prey 1 + predator 1) + §6 수식(`platform/config`가 `engine/kernel/expr`로
  컴파일). 초식 prey가 flora를 Graze(`food` 냄새 따라), predator가 prey를 Hunt(`prey` 냄새 + 표적팅), predator가
  `threat:predator` 보유 → agent Safety(F8 채널 재사용) + 인접 초식 Wary/Flee(`predator` 냄새=Wary, 전방 sight=Flee, F43/F44). 사망 → `carcass` 객체 +
  decay lot(Dm4) → `Butcher` 추출(hide/bone/sinew → material-tag item, W7/W8 공급); 미추출 사체는 부패(W10 정합).
  **이 phase에서만** 영향 골든 재기준. 시나리오: "포식자 접근 → agent Safety goal 창발", "사냥→사체→butcher→
  뼈/힘줄이 craft 입력", "사체 미추출 → 부패(W10)", "포식자 냄새칸=Wary→전방 시야 진입=Flee(F43/F44)". **선행(RESOLVED ✓): F6·F7·F8·F11·F12.**
  빌드 선행: `engine/env/decay`(P_m2) + `expr` + (초식용 edible flora — flora 활성 or placeholder edible 객체).
  **⚠ SPEC-design 선행(OPEN, §1.3): F31·F37·F38·F39·F40 + F43(Wary 행위·2밴드 fear) + (Graze/Flee/Wary 공유-레지스트리 추가, F28 골든 재기준).**

- **P_fa4 — 창발 번식/개체군 사이클(타이머 respawn 대체) + 체감온도 thermal 거동**
  drive-gated 창발 birth(F9 승격 — flora 씨앗분산 동형, §7 비의존 최소 사이클) + 체감온도 thermal drive/vital(F10) —
  **climate 출하 시** 활성('겨울'=체감온도 지속저하→die-off/이주 압력 창발; 바람 → 냄새 원거리/upwind 활성). 남획→
  붕괴→아사(공유지 L) 시나리오. 의존: climate(thermal/wind, **CA1 연주기·CA2 바람·CA3 단위/operand 노출**), (선택) §7 lifecycle 정합.

- **P_fa5 — 직렬화/스트림 + 렌더**
  `animals[]` periodic full + sparse delta(spawn/move/die, F17) → `platform/persist` + `data-contracts.md §6`
  (wear/terrain/flora 정합). frontend: 동물 렌더(이동·종).

- **park (frontier — P1 비차단):** 사회적 동물(무리/herd/세력권, **사람 연기**) · husbandry/길들임(F13 창발 flee-shift) ·
  계절 이주 richness · 작물/구조물 피해 · 스칼라 농도 냄새 승격 · `engine/mind/perception` full-LoS 동물 지각 · navmap/pathfind 동물 경로.

## 3. Notes / escalations (정직 플래그 — 덮지 않음)

- **의존 현실(차단 회피 — 확정):** ① **planner 미빌드** → F1(a) horizon-1로 회피(plan 조립 없음). ② **lifecycle(§7)
  미빌드** → F9(c) 타이머 respawn 부트스트랩으로 P1 비차단; 창발 번식은 P_fa4. ③ **climate 미빌드** → 체감온도·
  바람은 **입력 계약**(F10/F21), P1은 thermal-OFF + 냄새 단거리(바람 중립). ④ **`engine/kernel/expr` + `engine/env/decay`
  (P_m2)** 가 선행 leaf(둘 다 READY/다음 빌드).
- **2차 게이트(§1.3 F25~F44, 2026-06-26):** module SPEC 착수 전 **사람 확정 필요**한 SPEC-design 세부. ★foundational = F26
  (per-action utility 수식 형태)·F27(expr↔fauna 피연산자 브리지) — 이 둘이 나머지 스키마(F31 fauna 블록)를 형성하므로 가장 먼저
  resolve. **3차 게이트(F43/F44, 2026-06-26):** 2단계 포식자 반응(Wary↔Flee, smell↔sight) + 방향성 시야 — F25/F28/F34 를 정련하며
  F40(apparent_temp)·CA1~CA3(climate)와 cross-doc 결합. **resolve 전 SPEC 작성 금지(발명 = 결함).**
- **불변 플래그(아래는 위반이 아니라 가드레일):**
  - **D3:** Hunt/Graze/Flee/Wary/Butcher가 horizon-1 utility로 선택되는 한 OK — 단 drive→utility는 **§6 데이터**(D4)여야
    하고 per-species behavior tree를 저작하면 위반. (§1.3 F26/F30/F43: utility는 순수 점수, Wary↔Flee 는 fear-밴드 결과, stickiness는 §6 항 — 손그림 FSM 금지.)
  - **D4:** drive 갱신·utility·apparent_temp·yield 전부 tag/§6 파생(§1.3 F25/F26/F40); F26(b) 닷프로덕트는 drive↔action
    매핑을 엔진 코드화할 위험이라 reject 권고.
  - **D5/D1:** 동물 drive-set은 agent `Value` 계와 **별개의** 동기 기계다(축소 루프, §0-1). 코드베이스에 두 번째
    동기 기계가 생기므로 drive≠Value로 명확 분리(F3; §1.3 F26(c) reject 근거) — 위반은 아니나 drift 위험.
  - **D6/D8:** 동물 ToM 없음(F2/F3); 포식자→agent Safety는 지각 hostile tag로만 흐름(F8) — D6/D8과 정합.
  - **D7:** 동물 base 스탯은 §6 합성·mutable일 수 있으나 **stat-training/노화는 cross-cutting stats/lifecycle 소관**
    (flora 동일) — fauna는 §6로 *읽기만*(§1.3 F29 가드).
  - **D10:** species = content `fauna:` 블록(F6); Animal `Stats`/`Drives` = open(§1.3 F29 — 신규 stat/drive = 데이터).
  - **D11:** 냄새 그리드 = 보조 색인. 동물은 연속좌표 유지·칸 스냅 없음, 필드는 *읽기만*(F20/§1.1; §1.3 F32/F35/F44 가드) — 위반 아님.
  - **D12:** scent 확산 = 고정순서 stencil bulk(§1.3 F33); apply = 결합 agent+animal 정렬 ID 1순서(F41); per-step rng fork; FOV 셀/bearing 테스트 고정순서(F44).
  - **expr L0 불변(§1.3 F27):** drive/scent/sight/dist/apparent_temp/wind은 전부 소문자/점 **Attr 피연산자**(caller 네임스페이스,
    flora `moisture`·Cm3 `tool:<family>.quality` 동형) — expr 메서드 추가 금지. `platform/config`가 `ReadsAttrs()` 교차검증.
- **glossary(F19/F42):** §1.2 + §1.3 F42 용어(+ F43/F44 의 `Wary`/`sight.predator`/`dist.predator`/`smell`/`sight`/`fov_arc`, cross-doc `wind.mag`) 채택 확정 → `docs/glossary.md` 동기화는 별도 단계.
- **DAG 영향(F5):** `engine/fauna`가 `architecture.md §2/§4/§5`에 합류 — actions/spatial 뒤(agent 옆), world(stage 7)가
  apply + 냄새 bulk 패스 와이어링. **architecture.md 수정은 아직 미시행** — SPEC 착수 직전 사람 확인 후 반영.
- **시나리오 정합:** P_fa3가 W7(뼈 craft 입력)·W8(힘줄 binding/대체)·W10(사체/유품 부패) 공급측을 활성화; 포식자→
  Safety는 사회 시나리오 threat→Safety 계열과 연결.
- **cross-doc(climate, 2026-06-26):** F40(apparent_temp)·F33(scent 바람 확산)·F41(world 와이어링)이 `docs/climate.md §1c`
  의 **CA1(연주기)·CA2(바람 생성·`wind.dir`/`wind.mag` operand)·CA3(단위·operand 노출)** 에 종속 — operand 명칭·단위
  (`temperature`/`moisture`/`wind.dir`/`wind.mag`)는 두 문서에서 **반드시 동일**. CA3 단위 fork 와 F40 은 한 번에 사람이 결정.
