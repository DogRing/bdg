# Formula Evaluator (§6 DSL) — Subsystem Plan

Concept & rationale: `docs/design.md §6` (수식 DSL — stat 계산식은 데이터다; 특히 line 89 **평가기 위치**
+ line 90 **평가 모델 / Context 채널**, 둘 다 binding). The module SPEC does NOT yet exist:
this Tier-2 plan opens the build decisions for the NEW foundational module `backend/engine/expr/SPEC.md`
(L0 leaf, `core`-only). It sits between `design.md §6` and every module that evaluates a `Formula`.

관련 모듈: **신규** `engine/expr`(한 공유 평가기 — `Program` 컴파일·`Context` 평가),
`engine/gates`(기존 boolean `GateExpr` 술어트리 평가기 — §6 boolean 부분집합으로 **통합 대상**),
`engine/climate`·`engine/flora`(이미 `expr.Program` + `expr.Context`를 SPEC에서 가정),
`engine/actions`·`engine/economy`(미래 소비자), `platform/config`(로드 시 parse/compile + 미정의 식별자 검증).
용어: `docs/glossary.md` `Formula`(데이터 수식 DSL, 출력 numeric|boolean) · `GateExpr`(그 boolean 부분집합).

## 0. Decisions locked (design.md §6 line 89–90 에서 확정 — 여기서 다시 결정하지 않음)
- **한 공유 평가기.** `engine/expr` = 전 모듈이 공유하는 **단 하나의** §6 평가기(L0 leaf, **`core` 타입만 의존**,
  `rng`도 안 의존). `gates`·`climate`·`flora`·`actions`·`economy`가 모두 이것을 쓴다 — **두 번째 평가기 금지**
  (glossary "one shared evaluator"). gates의 boolean `GateExpr`는 이 평가기의 boolean 부분집합.
- **시그니처 형태:** `eval(Program, Context) → number | bool`. expr은 **순수** 평가기다 — plan/apply 단계도,
  `ToM`/real 구분도, RNG도 **모른다**(멍청한 순수 평가기). 입력은 컴파일된 `Program` + 추상 `Context`뿐.
- **채널은 호출자가 갈아끼운다.** 계획 단계엔 호출자가 **`ToM` 기반 `Context`**(인지값 → boolean 시도-게이트, D8),
  apply 단계엔 **real 기반 `Context`**(실제값 → numeric chance/qty)를 넘긴다. **chance→성공 roll은 호출자의 것**
  (주입 RNG); expr은 **숫자만 반환**(결정성, D12). expr은 어느 채널인지 알지 못한다.
- **피연산자는 `Context`로 해석.** 주체 스탯(`STR`…)·대상 속성(`terrain.depth`·`door.lockStrength`·`plant.length`)·
  술어(`has`/`isOwner`/`paid`, boolean)·환경 변수(`moisture`). 연산자는 design §6: 산술 `+ - * /`(괄호)→수치,
  비교 `> < >= <= == !=`→불리언, 논리 `& | !`→불리언. **출력 타입 = 문맥/역할**(boolean 게이트 vs 수치).
- **로드 시 parse/compile, 런타임 evaluate.** `platform/config`가 `Program`을 컴파일; 미정의 식별자는
  **로드 시 검증 실패**(D10). eval 안에 RNG 없음; **고정 연산자 우선순위**(D12). 형식만 고정, 변수명은 자유.

## 1. Decisions — **ALL RESOLVED** (추천대로 채택)
> 사람이 7개 전부 각 줄의 `rec`로 확정(`[RESOLVED→rec]` = 그 줄 rec). #4 gates 통합 = **단계적**(expr 독립 → 동일성 검증 → 스왑, 기존 gates golden 보호). ⇒ module SPEC 작성 가능.

- [RESOLVED→rec] **`Context` 인터페이스 형태** — 피연산자/술어가 어떻게 해석되는가(메서드 셋, 호출자별 누가 구현하나)? —
  options: (a) 단일 메서드 `Lookup(ident string) (Value, bool)` (operand·predicate 모두 한 lookup으로,
  호출자가 `terrain.depth`/`has(key)` 모두 처리); (b) 타입별 메서드 셋 `Stat(StatID) float64` +
  `Attr(core.Tag) (float64,bool)` + `Pred(name string, arg core.Tag) bool` (역할 분리, expr이 산술/술어를
  형식으로 구분); (c) `core`에 작은 인터페이스 `expr.Context` 선언 + 호출자(gates `AgentSnapshot`,
  flora `SiteInput`+`Plant`, climate `CellState`)가 어댑터로 구현. — rec: **(b)+(c)** — 산술 operand와 boolean
  술어는 형식이 다르니(숫자 vs 불리언) 메서드를 나누되, 인터페이스는 expr이 선언하고 각 호출자가 어댑터로 구현
  (flora SPEC이 이미 "Context flora builds from SiteInput+Plant" 라고 가정 → (c)와 정합); why: 단일 string lookup은
  술어 인자(`has(itemID)`)·타입(숫자/불리언)을 잃어 컴파일 시 타입검사가 어려워짐.
- [RESOLVED→rec] **`Program` 표현** — AST vs 컴파일된 bytecode/closure? `platform/config`가 어떻게 생산하나? —
  options: (a) 불변 AST 노드 트리(`gates.GateExpr`와 동형 — 호환 쉬움, 평가 시 트리 워크); (b) 평탄화 bytecode/
  스택 VM(빠름, 캐시친화 — but L0에 VM은 과설계); (c) `func(Context)(Value,error)` 클로저로 컴파일
  (가장 단순, 트리 워크 없음 — but introspection/golden 직렬화·`Reads()`류 정적분석이 어려움). — rec: **(a)
  불변 AST** — gates의 기존 `GateExpr` 트리가 이미 (a)라 통합(Q4)이 자연스럽고, `Reads()`(참조 StatID union)·
  golden 직렬화·로드 시 식별자 검증이 트리 위에서 쉽다; `platform/config`가 YAML/문자열 → AST로 parse·validate;
  why: closure는 빠르지만 D10 로드검증·introspection을 잃고, bytecode VM은 L0 leaf엔 과함.
- [RESOLVED→rec] **술어 함수 셋 (`has`/`isOwner`/`paid`/…) — 확장 가능한가? `Context`에 어떻게 바인딩?** —
  options: (a) 코어에 고정 enum(`has`/`isOwner`/`paid`만; 새 술어 = 엔진 수정 — 단순하지만 D10 위반 소지);
  (b) `Context`가 `Pred(name, arg) (bool, bool)`로 임의 이름 해석(완전 확장 — 새 술어 = 호출자 구현만, 코어 무수정,
  D10 정신); (c) 등록된 술어 시그니처 테이블(이름→인자arity) + `Context`가 값 제공(컴파일 시 미등록 술어 검출). —
  rec: **(b)+(c)** — `Context.Pred`로 해석하되 컴파일 시 known-predicate 테이블로 미정의 술어를 로드 실패
  처리(D10); 새 술어는 호출자가 `Context`에 구현+테이블 등록(코어 무수정); why: (a)는 D10(코어 무수정 확장)을
  깨고, 순수 (b)는 컴파일 시 오타 검출을 잃는다 — (c)가 로드검증을 회복.
- [RESOLVED→rec] **gates 마이그레이션 — `gates.GateExpr`를 `expr` 위로 리팩터(한 평가기) vs 분리 유지?** —
  options: (a) gates를 expr 위로 통합: `GateExpr`/`Op`/leaf eval을 `expr`로 이관, gates는 expr를 import해
  boolean 부분집합만 사용(glossary "one shared evaluator" 충족 — but `gates_test.go`·`p3_gates.json` golden·
  `schema_version 3` 영향, 신중한 재기준); (b) 분리 유지: expr은 climate/flora/actions만, gates는 자기 트리 유지
  (블래스트 최소 — but DSL 드리프트 = glossary가 금지하는 바, 두 평가기); (c) 단계적: 먼저 expr를 leaf-eval
  semantics가 gates와 **바이트동일**하도록 만들고(병렬 검증), 활성화 phase에서만 gates를 expr로 스왑(climate
  M-staging 동형, golden 중립 보장 후 의도적 재기준). — rec: **(c) 단계적 통합** — 최종 목표는 (a)(한 평가기,
  드리프트 금지)지만, 기존 gates golden/스키마를 보호하려면 expr-first → 동일성 검증 → 스왑이 안전; why: (b)는
  glossary 위반(두 평가기), (a) 직행은 P3 gates golden을 즉시 위태롭게 함.
- [RESOLVED→rec] **numeric vs boolean 타이핑 — 컴파일 시 검사 vs eval 시?** —
  options: (a) 컴파일 시 정적 타입검사(각 노드 numeric|boolean 추론, 게이트 문맥=boolean·비용 문맥=numeric를
  컴파일 시 강제 → 로드 실패로 조기 검출, D10); (b) eval 시 동적 검사(런타임 타입 에러 — 단순하지만 결정성 경로에
  에러 분기); (c) 호출자가 `EvalBool`/`EvalNumber` 둘로 진입점 분리(반환 타입을 호출 부위에서 고정). — rec:
  **(a)+(c)** — 컴파일 시 노드 타입 추론으로 "게이트인데 산술 결과" 같은 형식오류를 **로드 시** 잡고(D10),
  런타임 진입점은 `EvalBool`/`EvalNumber`로 분리(호출자가 기대 타입 명시, climate `Rules.Eval`은 bool,
  flora `Suitability`는 number); why: 결정성 런타임 경로(D12)에 타입에러 분기를 두지 않으려면 검사를 로드로 당겨야 함.
- [RESOLVED→rec] **에러/엣지 처리 — 누락 operand, 타입 불일치, 0나눗셈, NaN/clamp, float 도메인** —
  options: (a) 컴파일 시 잡을 수 있는 것(미정의 식별자·타입 불일치)은 로드 실패, 런타임 산술 엣지(0나눗셈·NaN)는
  **결정적 정책**(0÷0→0 또는 정의된 sentinel, clamp 없음 — 클램프는 호출자 책임, climate/flora가 이미 [0,1]
  클램프); (b) 런타임에 모두 error 반환(호출자가 처리 — but 결정성 경로에 error 분기 증식); (c) panic on
  런타임 위반(navmap unknown-id 동형 — world-contract bug). — rec: **(a)** — 식별자/타입은 로드검증(D10),
  런타임 산술 엣지는 **고정·결정적 결과**(0나눗셈 → 0 또는 명시 sentinel, NaN 금지)로 D12 보존; 도메인 클램프는
  expr이 아니라 호출자(이미 climate/flora가 함); why: 런타임 error/panic 분기는 결정적 틱(D12)을 복잡하게 하고
  호출자가 이미 클램프를 소유.
- [RESOLVED→rec] **값 타입(`float64`만?) + boolean 강제(coercion) 규칙** —
  options: (a) 단일 `float64`(boolean = 0.0/1.0 또는 ≠0 → true; 단순·균일 — but 게이트 boolean 의미가 흐려짐);
  (b) 합 타입 `Value{Kind: Num|Bool; Num float64; Bool bool}`(타입 명확, coercion 명시적·금지 가능 — 약간의
  메모리/복잡도); (c) float64 + boolean을 **별개 평가 경로**로(산술 서브트리는 float, 논리/비교 서브트리는 bool,
  경계는 비교 연산자에서만 num→bool 단방향 — coercion을 연산자 문법으로 강제). — rec: **(b)+(c)** — 내부 `Value`
  합 타입으로 타입을 명시하고, num↔bool 암묵 coercion은 **금지**(boolean은 비교/논리에서만, 산술은 numeric만 —
  Q5 정적검사가 강제); why: (a) float-as-bool는 design §6의 "출력 타입=문맥"과 충돌하고 미묘한 버그(0.0이 false?)를
  부르며, 명시 합 타입이 D12 골든 직렬화·타입검사에 가장 안전.

## 2. Phases — placeholder
> **§1이 전부 `OPEN`인 동안 채우지 않는다.** 각 Open question이 `RESOLVED`로 닫힌 뒤에야
> `engine/expr` module SPEC을 작성하고, 그 다음 이 절을 (climate.md §2 / map-plan M1~M5 양식으로)
> shippable + 테스트 + 결정성 골든 단위로 채운다.
>
> 예상 골격(미확정, 참고용): Pxa expr 코어(Program/Context/eval, 산술+비교+논리, 컴파일 타입검사) →
> Pxb gates 통합(Q4 단계적, golden 동일성 검증 → 스왑) → Pxc climate/flora 소비(이미 SPEC이 `expr.Program`+
> `expr.Context` 가정 — 컴파일 경로를 `platform/config`에 연결) → Pxd 술어 함수(`has`/`isOwner`/`paid`,
> economy/portal seam). 빌드순서: expr은 stage 1(core·rng와 동급 L0)이므로 climate/flora(stage 2)보다
> 먼저 존재해야 한다(architecture §5 caveat).
