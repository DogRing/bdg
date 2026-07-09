# Lifecycle — 위험 행동 · 사망 · 번식 — Subsystem Plan (DRAFT)

Concept & rationale: `docs/core/design.md §7`. 이 문서는 **구현 로드맵/결정 표면**이고 SPEC은 아직 없다.
관련 모듈: `engine/mind/actions`(위험·단계 outcome), `engine/agent`(durative 재평가), `engine/world`(사망/번식 적용),
`engine/mind/needs`(Stamina↔Rest 결합), `engine/mind/perception`(주관적 magnitude), `content/actions.yaml`.

## 0. Decisions locked (design.md §7 에서 확정)
- 행동은 결정적 공급만이 아니라 **위험·단계적 outcome**을 가질 수 있다: 실제 스탯 + (인지/실제) 지형을 **§6 수식**으로 **durative 중 per-tick 재평가** → 진행 / 중단(회항) / 실패(사망).
- **시도(인지 `ToM[self]`) vs 판정(실제) 2단계**(D8) — 기존 plan(신념 읽기)/apply(실제 적용) 위에 얹힌다(새 기계 최소).
- **Stamina = 기존 `Body.Stamina`** (새 스탯·need 아님). 떨어지면 **Rest/Sleep need 강도↑** (기존 Rest 재사용).
- 사망 + **번식** = 인구 생애주기 사이클. 자식·세대는 §1.3 가치 대상(예: '아이 행복') — D1 충돌 없음.

## 1. Open questions (사람이 결정)
- **사망 조건식:** 어떤 vital이 어떤 임계에서 사망? "회항 가능 여부" 판정식(남은 Stamina vs 복귀 비용)?
- **위험 outcome의 액션 스키마:** per-tick 평가식 필드명, 중단/사망 임계 표현 — §6 DSL을 actions.yaml에 어떻게 얹나(actions.schema 확장 범위)?
- **번식 메커닉:** 짝짓기/자원/근접 조건? 상속률·세대 perturbation? (노화 곡선은 design §4 계수)
- **주관적 지형:** "강 폭이 지능·자신감에 따라 다르게 보임"을 `perception`이 어떻게 산출하나 — magnitude 주관화는 신규(D8 확장).
- **data-contracts:** 사망/번식 이벤트 타입 + 스냅샷 인구 변동 표현(`data-contracts.md §4` 추가).
- 인구 사이클이 **창발 제도/경제**(돈·소유)와 어떻게 맞물리나 — 별도 스레드(`map-plan.md §6` toll 노트 참조).
- **위험-outcome 대상 일반화:** 같은 per-tick outcome 기계가 *구조물*도 깎는다(문 부수기 = "사망의 객체판", `economy.md` 참조). 행동 outcome이 agent need만이 아니라 **대상 object의 `integrity`**도 바꾸도록 스키마를 일반화?

## 2. Phases — (Open questions 해소 후 작성)
> map-plan.md 양식: 각 phase 독립 shippable + 테스트 + 결정성 골든.
