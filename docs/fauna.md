# Fauna — 동물 (축소 반응 루프) — Subsystem Plan (DRAFT)

Concept & rationale: `docs/design.md §5`(연속좌표·동적지형), `§6`(Formula DSL — `engine/expr` 공유 평가기),
`§7`(생애주기 — 사망×번식, object-mortality 계열), `§9`(경제 — 무주물/소유 seam).
메뉴: `docs/world-roadmap.md` 🦌 동물 섹션 + 교차연결(fauna→물질사슬·decay·flora·climate).
형제 문서(양식·패턴 출처): `docs/materials.md`(FINAL recipe model — 산물이 *이미 설계된* craft 사슬로 합류),
`docs/flora.md`(순수-Step transform·다중주기·종=`objects.yaml` 블록·outcome-중립 staging),
`docs/climate.md`(체감온도 입력 계약·결정성 패턴), `docs/map-plan.md`(navmap/직렬화 양식).

> **이 문서는 게이트 산출물이다 — 결정 표면(Open questions 열거)일 뿐, module SPEC은 아직 없다.**
> §0 = 사람 확정(재논쟁 금지). §1 = **전부 `OPEN`**(사람만 flip). 어떤 phase도 그 phase에 태그된
> Open question이 `OPEN`인 동안 시작 금지(CLAUDE.md Open-Question gate). **메커니즘을 발명하면 결함이지 주도성이 아니다.**

관련(예정) 모듈: **신규** `engine/fauna`(축소 반응 컨트롤러), `engine/world`(소유·갱신주기·intent 수집·apply),
`engine/actions`(공유 원자행위 레지스트리 — Hunt/Eat/Forage… + 신규 Butcher/Flee/Graze 후보), `engine/expr`(§6),
`engine/perception`/`engine/spatial`(지각·근접), `engine/decay`(P_m2 — 사체 산물 lot 부패, **재사용**),
`content/objects.yaml`(종 = object_kind `fauna:` 블록 + 산물 item·material tag), 초기 분포 = world-gen/시나리오 픽스처.

---

## 0. Decisions locked (사람 확정 — 여기서 다시 결정하지 않음)

1. **축소 반응 루프, full agent 루프 아님.** 동물은 agent의 value/ToM/GOAP-planner 스택을 돌리지 **않는다**.
2. **사회적 동물(무리/herd/세력권)은 후행 phase로 연기.** Phase 1 fauna = 순수 축소 반응(단독 행동만).
3. **명시적 '계절' 없음.** climate가 기온/날씨/바람을 **주기**로 변동시키고, fauna는 **체감온도**(apparent
   temperature)에 훅한다 — 체감온도 = **per-entity §6 수식**(지역 climate 속성 + 동물 자신의 속성). '겨울'은
   체감온도 지속 저하로 **창발**(season enum/label 없음). 체감온도는 **입력 계약**으로 취급(climate 미구현).
4. **불변식 준수:** D2(메타제도 하드코딩 금지 — 사육/길들임은 *창발*, 제도로 저작 금지), D3(behavior tree/FSM
   손그림 금지), D4(tag/§6 파생, per-animal bespoke Go 함수 금지), D10(종 = content 데이터+스키마, 코드 아님),
   D12(결정성: seeded RNG, 고정 apply 순서, map-순회 로직 금지).
5. **산물은 *이미 설계된* 물질사슬로 합류(병렬 시스템 금지):** 고기(food/decay)·가죽·뼈(craft 입력 W7)·힘줄
   (binding W8)·젖/털 = `materials.md` FINAL recipe model의 **material-tag item**. 사체→재료는 `engine/decay`
   (owner-agnostic lot, Dm4/Dm5)와 Craft/extract를 **재사용**한다 — 평행 부패/추출 시스템을 발명하지 않는다.

> **현행 baseline(일반화 대상):** `content/objects.yaml`의 `prey`(`mobile:true`, `Hunt`→`raw_meat`,
> `depletes:true`, `balance.regen.prey_respawn` 타이머)는 "동물 = 떠도는 채집 객체"의 최소형이다. fauna는 이를
> **축소 반응 entity**로 일반화한다(사냥감=huntable, 포식자=움직이는 위협→Safety, 사체→재료). 위협→Safety는
> 이미 `balance.yaml threats.hostile_tags` + `needs.UpdateConditionalNeeds`(per_threat_intensity/safety_decay)
> 채널이 존재한다 — fauna는 그 채널을 *재사용*한다.

---

## 1. Open questions — **ALL `OPEN`** (옵션 + 추천; 사람만 resolve)

### [decision — 의사결정 기계 / D3·D4·planner 미빌드 회피]

- **F1 — 동물 의사결정 메커니즘.** `OPEN` — options:
  - **(a) horizon-1 utility arbitration** — 매 틱, **공유 원자행위 레지스트리**(agent가 쓰는 `engine/actions`)의
    후보 행위를 동물의 drives + 지각 문맥 위 §6 수식으로 점수화 → 최고-utility 단일 행위 선택(동률 = ID).
    multi-step plan 조립 없음 ⇒ **planner 의존 없음**. `engine/actions`/`expr`/perception/spatial 재사용.
  - (b) agent planner를 최소 method로 재사용 — **미빌드 planner에 하드블록**.
  - (c) per-species 저작 FSM/BT — **D3 위반(기각)**.
  - **rec: (a)** — planner/lifecycle 미빌드 제약을 회피하면서 D3/D4 준수(행위=공유 레지스트리, 점수=§6 데이터).
    `[P1 게이트]`

- **F2 — 게이트/가시성 + D8 2채널(동물은 ToM 없음).** `OPEN` — gate는 `ToM[self]`(D8)를 읽는데 동물엔
  자기신념이 없다. options:
  - **(a)** 동물은 gate/visibility 층을 건너뛰고 utility를 **실제-스탯 단일 채널**로 평가(시도(ToM)/판정(real)
    2채널이 자기신념 부재로 1채널로 collapse).
  - (b) degenerate `ToM[self]=real`로 agent 경로 재사용.
  - (c) gate 유지하되 gate Context에 실제-스탯 주입.
  - **rec: (a)** — 자기신념 없는 동물엔 과신/과소(D8) 의미가 없으므로 1채널이 정직. `[P1]`

### [state & substrate — 내부상태 / entity 표현 / 모듈경계]

- **F3 — 동물 내부상태(drive-set) + ToM 여부.** `OPEN` — options:
  - (a) drive-set만(hunger, fear/flee, thermal, fatigue, reproduction-readiness) — 최소.
  - **(b)** drive-set + agent `Body` 부분집합(`Stamina` + 단일 vital/health 스칼라 + `Pos`); Inventory/Value/ToM 없음.
  - (c) full Body − Value/ToM.
  - **ToM: rec NO** — D6(평판 분포)/D8(자기보정) 사회 기계를 동물에서 빼둔다.
  - **rec: (b) + ToM 없음** — 사냥/도주가 의미를 가지려면 Stamina/vital은 필요(아사·도주 실패→사망), 그러나
    Value/ToM/Inventory는 축소 루프 밖. `[P1]`
  - ⚠ **모델링 플래그:** drive-set은 agent `Value{Dimension,…}`와 **다른** 동기 기계다(축소 루프, §0-1 잠금).
    drift 방지를 위해 drive ≠ Value로 명확히 분리(D5 정신) — drive를 agent 가치계로 오인 금지.

- **F4 — 동물 표현 / entity substrate.** `OPEN` — options:
  - **(a)** 신규 경량 `Animal` entity(자체 struct, 자체/공유 ID space) + 축소 컨트롤러.
  - (b) `Agent` struct 재사용 + reduced-control 플래그/경로 — agent에 동물 분기 오염(D5 위험).
  - (c) 현행 `prey mobile:true` object_kind를 컨트롤러가 구동(객체-행 그대로).
  - **rec: (a)** — D5 관심사 분리 + 결정성(자체 컨트롤러), apply는 통합 ID 순서로 통합(F16). `[P1 게이트]`

- **F5 — 모듈 경계 + DAG 위치.** `OPEN` — flora는 순수 L1 transform이나, fauna 의사결정은 `engine/actions`(L3)·
  `perception`(L3)·`spatial`을 *재사용*하라는 제약이 있어 순수 L1로는 어색하다. options:
  - **(a)** 신규 `engine/fauna` 컨트롤러 모듈(`core`/`expr`/`actions`/`perception`/`spatial`/`rng` import),
    intent 방출 → world가 apply(agent와 평행, agent보다 앞 or 옆 stage).
  - (b) 순수 L1 transform(core+expr+rng만, flora-parity) — world가 지각 문맥 + per-action utility 입력을 *값으로* 선계산.
  - (c) `engine/agent`에 흡수.
  - **rec: (a)** — 레지스트리 재사용 제약과 정합하며 동물 로직을 agent 밖으로(D5). world가 유일 mutator 유지. `[P1 게이트]`

### [content & food chain — 종 데이터 / 먹이사슬 / 위협]

- **F6 — 종 = content(D10) + 스키마 home.** `OPEN` — options:
  - (a) 신규 `content/fauna.yaml` 종 카탈로그.
  - **(b)** `content/objects.yaml` object_kind에 `fauna:` 블록으로 합류(flora 1i 선례 — 신규 `content/flora.yaml`을
    만들지 않았듯).
  - (c) 분리(거동 §6=fauna.yaml, 산물/yield=objects.yaml).
  - **rec: (b)** — 종 = object_kind + `fauna:` 블록(스탯·diet tag·산물 yield 표·size·drive §6 수식·체감온도 내성
    §6). flora 선례 직접 복제. `[P1 게이트]`

- **F7 — 먹이사슬 메커니즘 + 개체군 동역학.** `OPEN` — 초식=flora를 Graze/Forage(edible tag·flora 객체 질의),
  포식=prey를 Hunt(prey/edible tag + 지각). 개체군: options:
  - **(a) 창발** — 먹으면 자원/사냥감 감소, 아사→사망, 번식→증가(eat·death·reproduce가 base에서).
  - (b) 개체군 방정식 모델 — agent-based 정신 위반(기각).
  - **rec: (a) 창발** — 표적팅은 **tag**로(`edible`/`prey`/diet tag, D4); 남획→붕괴→포식자 아사(공유지 L)도 창발. `[P1]`

- **F8 — 포식자 → agent Safety 창발.** `OPEN` — options:
  - **(a)** 기존 위협→Safety 채널 재사용 — 포식자가 hostile tag(`balance.yaml threats.hostile_tags`) 보유 →
    agent 지각이 이미 방어 Safety goal 삽입 + `needs.UpdateConditionalNeeds`(per_threat_intensity/safety_decay).
  - (b) 포식자-전용 Safety 지각 채널 신규.
  - **rec: (a)** — 새 기계 0. 포식자 = "hostile tag를 단 움직이는 entity". (사회 시나리오 threat→Safety와 정합.) `[P1]`
  - ⚠ **불변 플래그(정합):** 포식자→Safety는 agent가 동물에 대한 ToM를 형성하는 게 아니라 **지각 hostile tag**로
    흐른다(동물 ToM 없음, F3). D6/D8과 충돌 없음 — 단 이 경로를 SPEC에 명시할 것.

### [lifecycle & thermal — 번식 / 체감온도 / 사체]

- **F9 — 번식/개체군 사이클 + §7 의존.** `OPEN` — §7 lifecycle 미빌드. options:
  - (a) 최소 자족 fauna birth(drive-gated, 부모 근처 seeded spawn — flora 씨앗분산 동형) — §7 비의존.
  - (b) §7 lifecycle 대기 — **P1 하드블록**.
  - **(c) P1 부트스트랩** = 현행 타이머-respawn(`balance.regen.prey_respawn`) 유지; 창발 번식은 후행 phase.
  - **rec: (c) for P1**(타이머 respawn 부트스트랩, §7 비차단) → 후행 phase에서 (a) 창발 birth로 승격. `[P1 비차단 via (c);
    창발 birth = 후행 phase 게이트]`

- **F10 — 체감온도(apparent-temp) 거동.** `OPEN` — 체감온도 = per-entity §6(지역 climate `temperature`/`moisture`
  (+미래 `wind`) + 동물 속성 size/coat 내성). 효과 options:
  - (a) thermal **drive**가 이동을 견딜만한 체감온도(그늘/물가)로 bias.
  - (b) thermal stress → vital 손상 → 내성 초과 지속 시 사망.
  - (c) dormancy/torpor(활동 저하).
  - **rec: (a)+(b)** — drive가 이동 bias + 지속 초과가 vital 깎음('겨울'=체감온도 지속저하→die-off/이주 압력 창발).
    체감온도는 **입력 계약**(climate 미빌드): world가 climate 값을 주입, fauna가 per-entity §6 합성. `[climate 미빌드면
    후행 phase; P1은 thermal-OFF / 내성식은 존재하되 climate-중립 출하]`

- **F11 — 사체 + 사망 시 산물.** `OPEN` — options:
  - **(a)** 사망 시 동물 → `carcass` 객체(종의 material lot=meat/hide/bone/sinew를 **owner-agnostic decay lot**
    Dm4로 보유)로 전환, `engine/decay`로 부패; `Butcher` 추출 행위가 부패 전 lot을 inventory로 yield. 미추출
    사체는 `rotten_matter`로 부패(Q3 transform) → 청소부 niche + W10 정합.
  - (b) 즉시 yield(사체 객체 없음).
  - (c) 사체=decayable 객체 + butcher=recipe-mediated Craft.
  - **rec: (a)** — `engine/decay`(Dm4/Dm5 owner-agnostic lot) 재사용(병렬 시스템 금지) + extract(F12). `[P1 게이트 —
    engine/decay(P_m2 READY) + materials yield 의존]`

- **F12 — Butcher 행위 범위.** `OPEN` — options:
  - **(a)** 신규 `Butcher` 추출 행위(tag-gated `tool:cutting`, §6/Dexterity yield) — `Mine`(Xm4)/`Fell` 평행,
    NON-recipe-mediated.
  - (b) 사체를 input slot으로 하는 recipe-mediated Craft.
  - (c) `Hunt`를 일반화해 산물 직접 yield(butcher 단계 없음).
  - **rec: (a)** — Mine 추출 평행으로 recipe model 밖; 산출 hide/bone/sinew는 material-tag item으로 FINAL recipe
    사슬에 합류(W7 뼈·W8 힘줄). `[P1]`

### [husbandry — D2 창발]

- **F13 — 사육/길들임(D2 창발).** `OPEN` — options:
  - **(a)** 풍부한 husbandry를 후행 phase로 연기; P1 = 순수 야생 도주 거동.
  - (b) 창발 길들임: agent의 반복 `Feed`/`Contain` 행위가 동물 flee-drive 반응을 이동(먹이는 자에 대한 fear↓) →
    가축화 **창발**, 저작 'tame' 플래그 없음(D2).
  - (c) 저작 tame 상태 — **D2 위반(기각)**.
  - **rec: (a) 연기**; 열릴 때 (b) 창발(‘tame’ 플래그는 D2 위반). `[후행 phase 게이트]`
  - ⚠ **불변 플래그:** P1에 taming 손대지 말 것 — 'tame' 상태/플래그를 저작하면 D2 위반(가축화는 도주-drive
    shift에서 창발해야).

### [determinism, perf, serialization — 결정성/규모/직렬화]

- **F14 — 이동/locomotion substrate.** `OPEN` — 다수 동물. options:
  - (a) navmap/pathfind 재사용(지형 우회) — per-animal-per-tick 비쌈.
  - **(b)** 연속 Pos + spatial 위 cheap steering/gradient(pathfind 없음), terrain은 passability 샘플만.
  - (c) hybrid(기본 steering, 막힐 때만 pathfind).
  - **rec: (b) for P1**(규모; D11 연속·타일 스냅 금지) → 필요 시 (c)/(a) 승격. `[P1 — perf/결정성]`

- **F15 — 동물 지각.** `OPEN` — options:
  - (a) `engine/perception`(Sight LoS/Smell/Hearing) 재사용 — 규모에서 비쌈.
  - **(b)** cheap spatial-radius 질의(prey/predator/flora/shade를 §6 sense 반경 내 탐지) — full LoS 없음.
  - (c) hybrid.
  - **rec: (b) for P1**(규모, 종별 §6 sense 반경) → 후행 `perception` 재사용. `[P1 — perf]`

- **F16 — 갱신 주기 / 틱 통합 + 결정성.** `OPEN` — options:
  - **(a)** 매 틱, 통합 read→score→intent→apply에서 동물을 agent와 **고정 결합 ID 순서**로 interleave.
  - (b) bulk 다중주기(`tick % N`) — flora/climate 동형.
  - (c) hybrid(이동/도주=매 틱, 저긴급 drive=bulk).
  - **rec: (a)** 거동(도주/사냥은 틱-반응 필요) + apply는 **고정 결합 agent+animal ID 순서**(D12); 번식/thermal
    die-off는 bulk cadence. map-순회 금지, per-step seeded RNG fork. `[P1 게이트 — 결정성]`

- **F17 — 직렬화 / 스냅샷.** `OPEN` — options:
  - **(a)** 동물을 `animals[]`(또는 `objects[]`)에 periodic full + sparse delta(spawn/move/die) — flora/wear 동형
    (`data-contracts.md §6`).
  - (b) 매 틱 full.
  - **rec: (a)**. `[후행 phase — 직렬화]`

### [bootstrap & vocabulary]

- **F18 — world-gen 초기 개체군 + 청소부 niche.** `OPEN` — 초기 배치 = world-gen/픽스처(content 아님,
  objects.yaml 정신); P1 부트스트랩 = 시나리오 픽스처 + 타이머 respawn(F9c). open: spawn 밀도/biome 적합도.
  **rec:** 시나리오=픽스처, 라이브=적합도-가중(flora 1j 동형). `[P1 비차단]`

- **F19 — 신규 glossary 용어(coin 금지 — 확정 필요).** `OPEN` — 후보(사람 확정 시 glossary 등재):
  동물 entity 명칭(예: `Animal`/`Creature`), `carcass`, `drive`(+ 개별 drive 명), `Butcher`/`Graze`/`Flee`
  행위, 체감온도 §6 operand 명칭(예: `apparent_temp`), 포식자 hostile tag(예: `threat:predator`),
  `fauna:` content 블록, edible/prey 표적 tag(예: `edible`/`prey`/diet tag).
  **rec:** resolve 시 채택·등재. `[교차]`

---

## 2. Phases — (각 phase 독립 shippable + 테스트 + 결정성 골든; `flora.md §2`/`climate.md §2` 양식)
> **핵심 안전 레버:** P_fa1~P_fa2는 outcome-중립(종 미배치 → 거동 0 변화; 현행 `prey` 타이머-respawn legacy 유지)
> → 기존 world 골든 불변. **P_fa3에서만** 의도적 재기준(climate M-staging/flora P_f staging 동형).
> **공통 선행:** `engine/expr` 구현(§6 — drive·utility·체감온도 내성·yield 수식) + `engine/decay`(P_m2, 사체 lot 부패).

- **P_fa1 — `engine/fauna` 축소 반응 컨트롤러 + fauna-OFF 출하(outcome-중립)**
  horizon-1 utility arbitration(drives + 실제-스탯 §6, planner/ToM 없음), cheap steering 이동, cheap sense-radius
  지각, 공유 `engine/actions` 레지스트리에서 후보 점수화 → 단일 intent(동률 ID). **종 미배치 → intent 0 →
  거동 불변.** 테스트: utility 동률-ID 결정성, drive 통합, seeded steering 재현, 공유-레지스트리 점수화, fauna-OFF
  중립, import/literal 가드(actions/expr/perception/spatial만; world/agent import 금지).
  **게이트(`OPEN`이면 시작 금지): F1, F2, F3, F4, F5, F14, F15.**

- **P_fa2 — `world` 와이어링(cadence + intent 수집 통합 apply 순서 + spawn/move/die 델타) — 여전히 outcome-중립**
  world가 fauna 컨트롤러를 통합 read→score→intent→**apply(고정 결합 agent+animal ID 순서, D12)**에 합류; per-step
  rng fork + ID 발급. **종 여전히 미배치 → 거동 불변(중립 회귀 가드).** 와이어링·cadence·fork·apply-순서 결정성만 검증.
  **게이트: F4, F16.**

- **P_fa3 — 활성: 야생 단독 prey+predator + 사체→재료 + 골든 의도적 재기준**
  `content/objects.yaml`에 `fauna:` 종 활성(prey 1 + predator 1) + §6 수식(`platform/config`가 `engine/expr`로
  컴파일). 초식 prey가 flora를 Graze, predator가 prey를 Hunt(tag 표적팅), predator가 hostile tag 보유 →
  agent Safety(F8 채널 재사용). 사망 → `carcass` 객체 + decay lot(Dm4) → `Butcher` 추출(hide/bone/sinew →
  material-tag item, W7/W8 공급); 미추출 사체는 부패(W10 정합). **이 phase에서만** 영향 골든 재기준.
  시나리오: "포식자 접근 → agent Safety goal 창발", "사냥→사체→butcher→뼈/힘줄이 craft 입력", "사체 미추출 →
  부패(W10)". **게이트: F6, F7, F8, F11, F12.** 의존: `engine/decay`(P_m2) + `expr` + (초식용 edible flora —
  flora 활성 or placeholder edible 객체).

- **P_fa4 — 창발 번식/개체군 사이클(타이머 respawn 대체) + 체감온도 thermal 거동**
  drive-gated 창발 birth(F9a — flora 씨앗분산 동형, §7 비의존 최소 사이클) + 체감온도 thermal drive/vital(F10) —
  **climate 출하 시** 활성('겨울'=체감온도 지속저하→die-off/이주 압력 창발). 남획→붕괴→아사(공유지 L) 시나리오.
  **게이트: F9(창발), F10.** 의존: climate(thermal), (선택) §7 lifecycle 정합.

- **P_fa5 — 직렬화/스트림 + 렌더**
  `animals[]` periodic full + sparse delta(spawn/move/die) → `platform/persist` + `data-contracts.md §6`(wear/terrain/
  flora 정합). frontend: 동물 렌더(이동·종). **게이트: F17.**

- **park (frontier — P1 비차단):** 사회적 동물(무리/herd/세력권, **사람 연기**) · husbandry/길들임(F13 창발) ·
  계절 이주 richness · 작물/구조물 피해 · `engine/perception` full-LoS 동물 지각 · navmap/pathfind 동물 경로.

## 3. Notes / escalations (정직 플래그 — 덮지 않음)

- **의존 현실(차단 회피 명시):** ① **planner 미빌드** → F1(a) horizon-1로 회피(plan 조립 없음). ② **lifecycle(§7)
  미빌드** → F9(c) 타이머 respawn 부트스트랩으로 P1 비차단; 창발 번식은 P_fa4. ③ **climate 미빌드** → 체감온도는
  **입력 계약**(F10), P1은 thermal-OFF. ④ **`engine/expr` + `engine/decay`(P_m2)** 가 선행(둘 다 READY/다음 leaf).
- **불변 플래그(아래는 위반이 아니라 가드레일):**
  - **D3:** Hunt/Graze/Flee/Butcher가 horizon-1 utility로 선택되는 한 OK — 단 drive→utility는 **§6 데이터**(D4)여야
    하고 per-species behavior tree를 저작하면 위반. (F1/F6에서 수식=데이터로 못박을 것.)
  - **D2:** husbandry/길들임은 *창발*만 — 'tame' 상태/플래그 저작은 위반(F13).
  - **D5/D1:** 동물 drive-set은 agent `Value` 계와 **별개의** 동기 기계다(축소 루프, §0-1 잠금). 정직 플래그:
    코드베이스에 두 번째 동기 기계가 생기므로 drive≠Value로 명확 분리(F3) — 위반은 아니나 drift 위험.
  - **D6/D8:** 동물 ToM 없음(F3); 포식자→agent Safety는 지각 hostile tag로만 흐름(F8) — D6/D8과 정합.
  - **D7:** 동물 base 스탯은 §6 합성이고 mutable일 수 있으나 **stat-training(use로 단련)/노화는 cross-cutting
    stats/lifecycle 소관**(flora와 동일) — fauna는 §6로 *읽기만*. (F3 범위 밖으로 명시.)
- **신규 용어는 coin 금지(F19):** 사람 확정 후 `docs/glossary.md` 등재 — 동물 entity·`carcass`·`drive`·`Butcher`/
  `Graze`/`Flee`·체감온도 operand·포식자 hostile tag·`fauna:` 블록·edible/prey 표적 tag.
- **DAG 영향(F5 resolve 시):** `engine/fauna`가 `architecture.md §2/§4/§5`에 합류 — rec(a)면 actions/perception/
  spatial 뒤(L4~L5, agent 옆), world(stage 7)가 apply 와이어링. resolve 전엔 architecture 미수정(사람 확인 후).
- **시나리오 정합:** P_fa3가 W7(뼈 craft 입력)·W8(힘줄 binding/대체)·W10(사체/유품 부패) 공급측을 활성화; 포식자→
  Safety는 사회 시나리오 threat→Safety 계열과 연결.
