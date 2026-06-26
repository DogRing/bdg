# Fauna — 동물 (축소 반응 루프) — Subsystem Plan

Concept & rationale: `docs/design.md §5`(연속좌표·동적지형), `§6`(Formula DSL — `engine/expr` 공유 평가기),
`§7`(생애주기 — 사망×번식, object-mortality 계열), `§9`(경제 — 무주물/소유 seam).
메뉴: `docs/world-roadmap.md` 🦌 동물 섹션 + 교차연결(fauna→물질사슬·decay·flora·climate).
형제 문서(양식·패턴 출처): `docs/materials.md`(FINAL recipe model — 산물이 *이미 설계된* craft 사슬로 합류),
`docs/flora.md`(순수-Step transform·다중주기·종=`objects.yaml` 블록·outcome-중립 staging),
`docs/climate.md`(체감온도·바람 입력 계약·결정성 패턴), `docs/map-plan.md`(navmap/직렬화/보조-색인 양식).

> **게이트 완료(2026-06-26):** §0 잠금 + §1 **전부 RESOLVED**(사람 확정). 옵션 전문·근거는 pre-resolution
> 커밋 `bebc643` 참조. 이 §1은 확정값 + 핵심 근거만 유지. SPEC은 이 확정 위에서 작성한다.
> **메커니즘을 발명하면 결함이지 주도성이 아니다** — SPEC/구현이 §0·§1을 벗어나면 여기를 먼저 고치고 사람 승인.

관련(예정) 모듈: **신규** `engine/fauna`(축소 반응 컨트롤러 + 냄새 그리드 보조-색인), `engine/world`(소유·갱신주기·
intent 수집·apply·냄새 bulk 패스), `engine/actions`(공유 원자행위 레지스트리 — Hunt/Eat/Forage… + 신규
Butcher/Flee/Graze), `engine/expr`(§6), `engine/spatial`(근접), `engine/decay`(P_m2 — 사체 산물 lot 부패, **재사용**),
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
   (binding W8)·젖/털 = `materials.md` FINAL recipe model의 **material-tag item**. 사체→재료는 `engine/decay`
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
| F1 | 의사결정 기계 | **(a) horizon-1 utility arbitration** — 매 틱 공유 `engine/actions` 후보를 drives+문맥 §6로 점수화 → 최고 1개(동률 ID). multi-step plan 없음 ⇒ **planner 무의존** |
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
**단일 균일 그리드 = world 소유 보조 색인** (spatial hash·navmap cost field와 동류; 모듈 배치 = `engine/spatial` 확장
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

---

## 2. Phases — (각 phase 독립 shippable + 테스트 + 결정성 골든; `flora.md §2`/`climate.md §2` 양식)
> **모든 §1 게이트 RESOLVED ✓** → phase의 남은 실제 선행은 **Open question이 아니라 선행 leaf 빌드**다:
> `engine/expr`(§6 — drive·utility·체감온도 내성·yield 수식) + `engine/decay`(P_m2 — 사체 lot 부패). 둘 다 READY/다음 leaf.
> **핵심 안전 레버:** P_fa1~P_fa2는 outcome-중립(종 미배치 → 거동 0 변화; 현행 `prey` 타이머-respawn legacy 유지)
> → 기존 world 골든 불변. **P_fa3에서만** 의도적 재기준(climate/flora staging 동형).

- **P_fa1 — `engine/fauna` 축소 반응 컨트롤러 + 단일 냄새 그리드 + fauna-OFF 출하(outcome-중립)**
  horizon-1 utility arbitration(drives + 실제-스탯 §6; planner/ToM 없음, F1/F2/F3), cheap steering 이동(F14),
  **단일 균일 냄새 그리드 감지**(F20~F24 — 이진 채널 플래그·근접 읽기; 바람=중립), 공유 `engine/actions` 후보 점수화
  → 단일 intent(동률 ID). **종 미배치 → intent 0 → 거동 불변.** 테스트: utility 동률-ID 결정성, drive 통합, seeded
  steering 재현, 냄새 그리드 침착/읽기 결정성·중립, 공유-레지스트리 점수화, fauna-OFF 중립, import/literal 가드
  (actions/expr/spatial만; world/agent import 금지).
  **선행(RESOLVED ✓): F1·F2·F3·F4·F5·F14·F20·F21·F22·F23.** 빌드 선행: `engine/expr`.

- **P_fa2 — `world` 와이어링(cadence + 통합 apply 순서 + 냄새 bulk 패스 + spawn/move/die 델타) — 여전히 outcome-중립**
  world가 fauna 컨트롤러를 통합 read→score→intent→**apply(고정 결합 agent+animal ID 순서, D12)**에 합류; 냄새
  확산 bulk 패스(`tick % Ns`, F24) + per-step rng fork + ID 발급. **종 여전히 미배치 → 거동 불변(중립 회귀 가드).**
  와이어링·cadence·fork·apply-순서·냄새 패스 결정성만 검증. **선행(RESOLVED ✓): F4·F16·F24.**

- **P_fa3 — 활성: 야생 단독 prey+predator + 사체→재료 + 골든 의도적 재기준**
  `content/objects.yaml`에 `fauna:` 종 활성(prey 1 + predator 1) + §6 수식(`platform/config`가 `engine/expr`로
  컴파일). 초식 prey가 flora를 Graze(`food` 냄새 따라), predator가 prey를 Hunt(`prey` 냄새 + 표적팅), predator가
  `threat:predator` 보유 → agent Safety(F8 채널 재사용) + 인접 초식 Flee(`predator` 냄새). 사망 → `carcass` 객체 +
  decay lot(Dm4) → `Butcher` 추출(hide/bone/sinew → material-tag item, W7/W8 공급); 미추출 사체는 부패(W10 정합).
  **이 phase에서만** 영향 골든 재기준. 시나리오: "포식자 접근 → agent Safety goal 창발", "사냥→사체→butcher→
  뼈/힘줄이 craft 입력", "사체 미추출 → 부패(W10)". **선행(RESOLVED ✓): F6·F7·F8·F11·F12.**
  빌드 선행: `engine/decay`(P_m2) + `expr` + (초식용 edible flora — flora 활성 or placeholder edible 객체).

- **P_fa4 — 창발 번식/개체군 사이클(타이머 respawn 대체) + 체감온도 thermal 거동**
  drive-gated 창발 birth(F9 승격 — flora 씨앗분산 동형, §7 비의존 최소 사이클) + 체감온도 thermal drive/vital(F10) —
  **climate 출하 시** 활성('겨울'=체감온도 지속저하→die-off/이주 압력 창발; 바람 → 냄새 원거리/upwind 활성). 남획→
  붕괴→아사(공유지 L) 시나리오. 의존: climate(thermal/wind), (선택) §7 lifecycle 정합.

- **P_fa5 — 직렬화/스트림 + 렌더**
  `animals[]` periodic full + sparse delta(spawn/move/die, F17) → `platform/persist` + `data-contracts.md §6`
  (wear/terrain/flora 정합). frontend: 동물 렌더(이동·종).

- **park (frontier — P1 비차단):** 사회적 동물(무리/herd/세력권, **사람 연기**) · husbandry/길들임(F13 창발 flee-shift) ·
  계절 이주 richness · 작물/구조물 피해 · 스칼라 농도 냄새 승격 · `engine/perception` full-LoS 동물 지각 · navmap/pathfind 동물 경로.

## 3. Notes / escalations (정직 플래그 — 덮지 않음)

- **의존 현실(차단 회피 — 확정):** ① **planner 미빌드** → F1(a) horizon-1로 회피(plan 조립 없음). ② **lifecycle(§7)
  미빌드** → F9(c) 타이머 respawn 부트스트랩으로 P1 비차단; 창발 번식은 P_fa4. ③ **climate 미빌드** → 체감온도·
  바람은 **입력 계약**(F10/F21), P1은 thermal-OFF + 냄새 단거리(바람 중립). ④ **`engine/expr` + `engine/decay`
  (P_m2)** 가 선행 leaf(둘 다 READY/다음 빌드).
- **불변 플래그(아래는 위반이 아니라 가드레일):**
  - **D3:** Hunt/Graze/Flee/Butcher가 horizon-1 utility로 선택되는 한 OK — 단 drive→utility는 **§6 데이터**(D4)여야
    하고 per-species behavior tree를 저작하면 위반.
  - **D2:** husbandry/길들임은 *창발*만 — 'tame' 상태/플래그 저작은 위반(F13 이연; 열리면 flee-drive shift로만).
  - **D5/D1:** 동물 drive-set은 agent `Value` 계와 **별개의** 동기 기계다(축소 루프, §0-1). 코드베이스에 두 번째
    동기 기계가 생기므로 drive≠Value로 명확 분리(F3) — 위반은 아니나 drift 위험.
  - **D6/D8:** 동물 ToM 없음(F2/F3); 포식자→agent Safety는 지각 hostile tag로만 흐름(F8) — D6/D8과 정합.
  - **D11:** 냄새 그리드 = 보조 색인. 동물은 연속좌표 유지·칸 스냅 없음, 필드는 *읽기만*(F20/§1.1) — 위반 아님.
  - **D7:** 동물 base 스탯은 §6 합성·mutable일 수 있으나 **stat-training/노화는 cross-cutting stats/lifecycle 소관**
    (flora 동일) — fauna는 §6로 *읽기만*.
- **glossary(F19):** §1.2 용어 채택 확정 → `docs/glossary.md` 동기화는 별도 단계.
- **DAG 영향(F5):** `engine/fauna`가 `architecture.md §2/§4/§5`에 합류 — actions/spatial 뒤(agent 옆), world(stage 7)가
  apply + 냄새 bulk 패스 와이어링. **architecture.md 수정은 아직 미시행** — SPEC 착수 직전 사람 확인 후 반영.
- **시나리오 정합:** P_fa3가 W7(뼈 craft 입력)·W8(힘줄 binding/대체)·W10(사체/유품 부패) 공급측을 활성화; 포식자→
  Safety는 사회 시나리오 threat→Safety 계열과 연결.
