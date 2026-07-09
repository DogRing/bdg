# Economy — 돈 · 소유 · 사유자산 · 통행료 — Subsystem Plan (DRAFT)

Concept & rationale: `docs/core/design.md §9` (+ §5 문/길목, §7 상속). 이 문서는 **구현 로드맵/결정 표면**이고 SPEC은 아직 없다.
관련 모듈: `content/objects.yaml`(`currency` item_kind, 문 object_kind+access policy), `engine/mind/actions`(trade=양도·pay-to-pass),
`engine/world`(`owner` 관계·상속 적용), `engine/agent`/`pathfind`(문 통과 = Caps+§6), `engine/mind/tom`(수요 감지·평판).

## 0. Decisions locked (design.md §9)
- **돈 = `currency` item_kind**(데이터). 창발 상품화 안 함(가독성). 이동은 기존 trade 재사용.
- **소유 = object→`owner` 관계**(`owner`=집합 가능=**공동소유**). `Build`가 set. **양도·공동소유는 trade로.** **상속 없음** — 사망 시 이전은 §7 임종 가치 전이로 *창발*(거래 없이 죽으면 무주물→공유지). 무주물 소유 *주장* 분쟁 → 접근 cost↑ → §3 코핑(폭력/거래).
- 길은 `wear`(object 아님) 유지. 소유·과금되는 건 길목의 **문(포털 구조물)** — 잠긴 집 문 = 통행료 문(같은 primitive).
- 문 개폐/통과 = **§6 수식**: `has(key) | STR > door.lockStrength | paid(toll) | isOwner`. soft lock → 강제 침입·미지불 우회가 창발(D2). M2 Caps 위에 얹힘.
- 문/구조물은 `integrity`(HP)를 갖고 **파괴 가능**: force/attack = **§7 위험-outcome 행동**이 per-tick integrity를 깎음 → 0이면 제거 + footprint un-stamp(M3) → navmap 영구 리루트. 강제 침입·재산 파괴는 D2 창발 범죄. ("구조물판 사망", `lifecycle.md` 참조.)
- **공동소유 = 채택**(trade로 owner 집합에 추가). 단 **공유자산 거버넌스**(누가 toll/policy 정하나)는 open.

## 1. Open questions (사람이 결정 — 컨트롤 표면)
- **[RESOLVED] 사망 시 이전:** 상속 **없음**. 죽음-근접 가치 전이(§7)로 죽어가는 자가 양도/공동소유 trade를 자발적으로; 거래 없이 죽으면 **무주물→공유지**.
- **[NEW] 공동소유 거버넌스:** 공동 소유 자산의 policy/toll을 누가 정하나(만장일치·과반·지분)?
- **[NEW] 죽음-근접 인지 + '물질' dimension:** agent가 자기 임종을 어떻게 아나(나이·vital·`ToM[self]`)? value가 빠지는 '물질' dimension 집합은?
- **[NEW] 소유 '주장(claim)' 행동:** 무주물 주장 = 행동? 주장이 상대 plan cost↑로 가는 경로(접근 게이트화)?
- **양도 메커닉:** ownership 이전을 trade에 어떻게 얹나 — 아이템 거래와 같은 경로 vs 별도 deed?
- **§6 DSL 술어 확장:** `has(item)`·`isOwner`·`paid(toll)` 외 무엇까지? 평가기 구현 위치(gates와 공유).
- **접근정책 데이터 모양:** 문 object_kind에 `{owner, policy expr, toll price}` → objects.yaml/스키마 + `data-contracts`(`object.owner` 필드) 확장.
- **수요 감지 깊이:** "남이 이 문/길을 필요로 함"을 ToM가 어디까지 추론(타인 plan/provisioning)? (지능 게이트)
- **pay-to-pass 지점:** 문 도달 시 결정(지불/우회/항의)을 agent 실행 루프 어디에서?
- **integrity 모델:** HP 단일값 vs 부위별? `lockStrength`↔`integrity` 관계, 파괴 임계?
- **수리·재건:** `Build`로 복구 가능? build↔destroy 군비경쟁의 비용 균형?
- **파괴의 사회적 귀결:** 소유주 원한·보복·평판(D6) 전파 — 기존 deed/gossip 경로 재사용?
- **투자량 자동 결정(문 lockStrength·집 크기) = planner frontier [PARKED → 플래너 설계 때]:** "안전을 채울 만큼만 / Standing 위해 더" 투자하는 *양*은 author 금지(D8: 마을 평균은 god 지식 / D9·D2: 답을 author 금지). **stat 임계를 향한 D9 forward-provisioning**으로 창발해야 함 — 인지 위협 `ToM[남].Strength` 분포 × RiskAversion + 지불 역량. author하는 건 공급식 `lockStrength=g(cost)`뿐. **map-plan §6 Degree-2와 동일한 planner 확장**(현재-plan만 보는 GOAP를 지연·간접 보상으로 확장)이라 함께 푼다.

## 2. Phases — (Open questions 해소 후 작성)
> map-plan.md 양식: 각 phase 독립 shippable + 테스트 + 결정성 골든.
