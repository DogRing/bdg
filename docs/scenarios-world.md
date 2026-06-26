# World-Layer Scenario Fixtures (W1–W16)

**물리/세계 층 통합-테스트 카탈로그.** `docs/testing.md §4`의 사회 시나리오(A–L)를 보완한다 — 이쪽은
지형·기후·식생·물질사슬·부패·경제·생애 *세계 완성* 층의 창발을 검증한다. 각 fixture =
`{초기상태(initial), seed, 기대-창발 단언(assertion)}` (D12: seed 주입, 결정성 골든).

> **원칙(중요):** 단언은 *메커니즘*이 아니라 *창발*을 본다. 결과가 **하드코딩 게이트가 아니라
> 기반 메커니즘(스탯·ToM·§6·태그·플래너)에서 나오는지**를 검증한다(D1/D2/D3). 같은 초기상태에서
> 스탯/ToM만 바꿨을 때 결과가 갈리면 통과; 시나리오-전용 분기가 있으면 실패.

> **상태 범례:** `READY`=의존 subsystem 구현됨 · `BLOCKED:<X>`=X 구현 대기. 대부분은
> 재출발 로드맵(expr → climate/flora/decay → materials → economy/lifecycle) 이후 활성.

---

## 지형·기후 (terrain / climate)

### W1 — 도하/수영 (River Crossing by *Perceived* Capability)  `BLOCKED: expr·perception(terrain)·lifecycle(stamina/death)·swim action`
- **초기:** 강(폭 가변)을 사이에 둔 자원. 역량/자신감이 다른 agent 2~3.
- **단언:** 도하 *시도* 여부는 **`ToM[self]` 역량**으로 결정되고(실제 능력 아님), 강폭이 지능·자신감에 따라
  다르게 *지각*된다(D8). 실제 능력+체력이 부족하면 강 중간에서 **사망**, 충분하면 도하 성공. 결과 분기가
  **swim §6 수식 + 실제 스탯**(시도 vs 판정 2채널)에서만 나오고 per-agent 분기 없음.
- **본다:** D1/D5/D8, Stamina→사망(§7).

### W2 — 늪지 건조 (Wetland Dries Without Rain)  `BLOCKED: climate`
- **초기:** 높은 moisture의 늪 셀들. 비를 0으로 강제하는 seed.
- **단언:** 장기 무강수 → 셀 `moisture` 하락 → terrain 속성 임계 통과 시 **`SetTerrain`으로 통행 가능/마른
  지형**으로 전이, navmap `BaseCost` 변동 → 경로 재계산. 비를 주면 역전. 전이가 **속성-임계(데이터)**에서만.
- **본다:** D11(grid=index), 동적 terrain delta.

### W3 — 비 사이클 (Rain Probability Cadence)  `BLOCKED: climate`
- **초기:** 맑음에서 시작, 고정 seed.
- **단언:** 비 확률이 0에서 **시간마다 상승**, ~10일 기대주기로 강수, **30일이면 강제** 발생, 1회 **2–12h**
  지속, 강수 후 0으로 리셋. 강수 동안 `moisture`↑·`temperature` 반응. (메커니즘+결정성 골든.)
- **본다:** 결정성(seed 동일 → 동일 강수열).

---

## 식생 (flora)

### W4 — 그늘→시야 감소 (Shade Occludes Line-of-Sight)  `BLOCKED: flora(active)·perception(ShadeOccluder)`
- **초기:** 폭(`Width`) 큰 식물 1, 그 그늘 안/밖에 관측 대상.
- **단언:** 넓은 식물이 만든 그늘이 LoS를 가려(불투명도 ∏(1-opacity)) 그늘 *너머/안*의 perception이 감소.
  식물이 자라 `Width`↑ → 차폐 면적↑. 좁은 식물은 영향 미미. (Width→Shade→LoS가 §6/데이터에서.)
- **본다:** flora morphology(Width=shade), perception 확장.

---

## 물질사슬 (materials / craft / decay)

### W5 — 도구 의존사슬 (Toolmaking Dependency Chain)  `BLOCKED: expr·materials(Craft P_m3, Mine P_m4)`
- **초기:** 광맥(`ore_node`)과 원재료(shaft/blade stock)만. 도구 없음.
- **단언:** 플래너가 **맨손 `craft_basic_tool` → `craft_pickaxe`(자르는 도구 wear 슬롯) → `Mine`** 사슬을
  *스스로 조립*(D3, 손으로 안 그림). 곡괭이 없이는 채굴 게이트 미충족 → 먼저 제작. 의존이 **레시피 input
  가용성**에서만 창발(D2).
- **본다:** D2/D3, 레시피=데이터.

### W6 — 도구 마모·파손 (Tool Wear → Break → Re-craft)  `BLOCKED: materials(Craft P_m3)`
- **초기:** 내구도 낮은 도구 1, 반복 제작 필요한 목표.
- **단언:** 제작마다 `wear` 슬롯 도구의 durability `amount`만큼 감소, **0에서 파손(object-mortality 제거)**.
  파손 후 재료가 희소해지면 재제작 사슬 재발동. 마모량이 **레시피 per-입력**에서.
- **본다:** Cm2, 희소성 창발.

### W7 — 도구를 재료로 (Tool-as-Material Upgrade, 칼>더 좋은 칼)  `BLOCKED: materials(Craft P_m3)`
- **초기:** 헌 칼 1 + 추가 재료, 더 좋은 칼 레시피.
- **단언:** 헌 칼이 `mode: consume` 슬롯에서 **통째로 소모**(도구≠고정분류) → 더 좋은 칼 산출. 같은 칼이
  다른 레시피에선 `wear`로 쓰일 수 있음. 산출 칼 내구도는 **basis_stat roll**.
- **본다:** 비-이산 도구/재료, 결과=확률 roll.

### W8 — 대체 재료 (Substitutable Inputs, 2단)  `BLOCKED: materials(Craft P_m3)`
- **초기:** 같은 결과를 내는 한 레시피. 칼날감을 나무/돌/뼈 중 일부만 보유한 변형 케이스들.
- **단언:** 슬롯이 **`any`(명시 대체) + `tagQuery`(태그 대체)** 양쪽으로 충족 — 칼날석이 없으면 고철로,
  칼이 없으면 도끼로. 새 아이템이 태그만 달면 자동 자격(레시피 무수정, D4). 대체 선택은 **authored-order
  first-satisfiable**(결정성).
- **본다:** D4/D10, 결정성.

### W9 — 저온저장 창발 (Cold-Storage Emerges)  `BLOCKED: decay(P_m2)·climate`
- **초기:** 같은 부패 음식을 더운/습한 곳, 차갑/건조한 곳, 저장 구조물 안에 각각.
- **단언:** `effRate = baseRate·accel(temp,moist)·storageMult`로 **더운/습→빨리, 차갑/건조·저장구조물→느리게**
  상태전이(신선→…→소멸). agent가 비축 시 저장 위치를 선호하는 행동이 창발(하드코딩 저장규칙 없음).
- **본다:** Dm2, 저장 전략 창발(D2).

### W10 — 유품 부패 (Dead Agent's Items Keep Rotting)  `BLOCKED: decay(P_m2)·lifecycle(death)`
- **초기:** 인벤토리에 부패물을 든 agent, 곧 사망.
- **단언:** 사망 후 떨어진/무주 아이템도 **소유 무관 동일 틱**으로 계속 부패(Dm4). 죽음-특례 분기 없음.
- **본다:** Dm4(owner-agnostic), D2.

### W11 — 채굴 고갈→지형변화 (Ore Depletion Reroutes the Map)  `BLOCKED: materials(Mine P_m4)·navmap·terrain.yaml`
- **초기:** 유한 `ore_node` 1, 그 위를 지나는 경로.
- **단언:** 반복 `Mine`으로 `remaining→0` → **노드 제거 + `bare_rock` SetTerrain(1회)** → navmap 영구 리루트.
  고갈이 공유지 분쟁 시드가 됨(연결 시).
- **본다:** Q4/Xm2, 동적 terrain.

### W12 — 숙련 제작 품질 (Skill → Sturdier Product)  `BLOCKED: materials(Craft P_m3)·stat-training(D7)`
- **초기:** basis_stat(Dexterity) 차이가 큰 제작자 2.
- **단언:** 같은 레시피라도 **basis_stat 높은 쪽이 더 높은 내구도/수량**(확률 roll). 제작 반복 시 해당
  스탯이 자라 점점 좋아짐(D7, 스킬 아님). '장인' 역할이 **창발**(per-activity 스킬 트랙 없음).
- **본다:** D7, 결과=basis_stat roll.

---

## 경제·소유·생애 (economy / lifecycle)

### W13 — 통행료 문 (Toll Door / Portal)  `BLOCKED: economy`
- **초기:** 좁은 길목에 owner가 toll 건 문(포털 구조물). 통과하려는 agent들.
- **단언:** 문 도달 시 §6 접근식 `has(key) | STR>lockStrength | paid(toll) | isOwner`로 **지불/우회/강제침입**이
  갈림. 강제 시 `integrity` 깎임 → 0이면 **un-stamp + navmap 영구 리루트**. 통행료·재산·범죄가 base에서 창발.
- **본다:** D2/D4, soft-lock 창발.

### W14 — 임종 가치 전이 (Deathbed Value-Shift)  `BLOCKED: lifecycle·economy`
- **초기:** 사망 근접 agent + 소유 자산 + 주변인.
- **단언:** 죽음-근접 인지 시 **물질 dimension value↓ · 타인 인정 욕구↑** → 자발적 **양도/공동소유 거래**.
  거래 없이 사망 → **무주물→공유지**(상속 없음). 전이가 value 분포 변화에서 *창발*(author한 상속 규칙 아님).
- **본다:** D1/D9, 상속 제거→창발.

### W15 — 무주물 주장 분쟁 (Unowned-Claim Conflict)  `BLOCKED: economy`
- **초기:** 무주물 1, 그것을 쓰려던 agent A, 뒤늦게 소유 주장하는 B.
- **단언:** B의 *주장*이 A의 접근 cost↑ → A가 **코핑(폭력/거래/양보)** 중 §6·value로 선택. 분쟁이 base
  메커니즘(cost·ToM)에서만, '범죄' 제도 하드코딩 없음(D2).
- **본다:** D2, 무주물→공유지 경계.

### W16 — 구조물→navcost (Build Changes the Map)  `READY?(world Build·navmap)`
- **초기:** 빈 터, Build 가능한 agent.
- **단언:** 구조물 건설 → footprint **stamp → navmap `BaseCost`/`Passable` 변동 → 경로 재계산**. 철거/파괴 시
  un-stamp 역전. (이미 일부 구현 가능성 — 새 세션에서 가장 먼저 검증해볼 후보.)
- **본다:** D11, 동적 navmap.

---

## 의존 → 활성 순서 (재출발 로드맵과 정렬)
1. `engine/kernel/expr` → W5·W7·W8·W12(§6 roll), W1(swim 식).
2. climate → W2·W3·W9. flora(active)+perception → W4. decay → W9·W10.
3. materials Craft/Mine → W5·W6·W7·W8·W11·W12.
4. economy·lifecycle → W13·W14·W15. (+ W16은 world/navmap 현존분으로 선검증 가능.)

> 새 세션 작업 지침: 각 W#를 `{초기상태, seed, 창발 단언}` 픽스처로 인코딩하되 **단언은 '스탯/ToM만 바꿔
> 결과가 갈리는가'로** 작성(메커니즘 검증이 아니라 창발 검증). 의존 subsystem이 `BLOCKED`인 동안은
> skip/pending 표식으로 두고, 해당 phase가 GREEN될 때 활성화.
