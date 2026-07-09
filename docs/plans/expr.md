# Formula Evaluator (§6 DSL) — Subsystem Plan

Concept & rationale: `docs/core/design.md §6` (수식 DSL — stat 계산식은 데이터다; 특히 line 89 **평가기 위치**
+ line 90 **평가 모델 / Context 채널**, 둘 다 binding). The module SPEC NOW EXISTS:
`backend/engine/kernel/expr/SPEC.md` (L0 leaf, `core`-only). It sits between `design.md §6` and every module
that evaluates a `Formula`. This Tier-2 plan keeps the locked decisions (§0), the resolved build
decisions (§1, all RESOLVED), and the shippable phases (§2).

관련 모듈: **신규** `engine/kernel/expr`(한 공유 평가기 — `Program` 컴파일·`Context` 평가),
`engine/mind/gates`(기존 boolean `GateExpr` 술어트리 평가기 — §6 boolean 부분집합으로 **통합 대상**),
`engine/env/climate`·`engine/env/flora`(이미 `expr.Program` + `expr.Context`를 SPEC에서 가정),
`engine/mind/actions`·`engine/economy`(미래 소비자), `platform/config`(로드 시 parse/compile + 미정의 식별자 검증).
용어: `docs/core/glossary.md` `Formula`(데이터 수식 DSL, 출력 numeric|boolean) · `GateExpr`(그 boolean 부분집합).

## 0. Decisions locked (design.md §6 line 89–90 에서 확정 — 여기서 다시 결정하지 않음)
- **한 공유 평가기.** `engine/kernel/expr` = 전 모듈이 공유하는 **단 하나의** §6 평가기(L0 leaf, **`core` 타입만 의존**,
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
> 사람이 7개 전부 각 줄의 `rec`로 확정(`[RESOLVED→rec]` = 그 줄 rec). #4 gates 통합 = **단계적**(expr 독립 → 동일성 검증 → 스왑, 기존 gates golden 보호). ⇒ module SPEC 작성 완료(`backend/engine/kernel/expr/SPEC.md`).

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

## 2. Phases — (각 phase 독립 shippable + 테스트 + 결정성 골든; `climate.md §2 / map-plan M1~M5` 양식)
> 빌드순서: expr은 build stage 1(`core`·`rng`와 동급 L0, architecture §5)이므로 그 소비자 climate/flora(stage 2)·
> gates(L2)·platform/config 보다 **먼저** 존재해야 한다. 모듈 SPEC = `backend/engine/kernel/expr/SPEC.md`.
>
> **핵심 안전 레버:** Pxa(코어)·Pxb(통합 준비)는 **gates·climate·flora 골든을 일절 건드리지 않는다** —
> expr는 독립 패키지로 출하되고, 기존 gates `p3_gates.json`/schema_version 3은 불변. gates 실제 스왑(Pxb 후반)·
> climate/flora 활성화(Pxc)에서만 의도적 재기준(climate M4 동형).

### Pxa — `engine/kernel/expr` 코어 (Program/Context/eval, 산술+비교+논리, 컴파일 타입검사)  ← 이 SPEC
- **출하물:** `engine/kernel/expr` 단독 패키지 — `Value`{Kind:Num|Bool}(#7), `Context`{Stat/Attr/Pred}(#1),
  불변 AST `Program`(#2) + `ResultKind()`/`Reads()`/`ReadsAttrs()`/`ReadsPreds()` introspection,
  `Parse(text, want, knownStats, knownPreds) (*Program, error)`(컴파일+정적 타입추론+식별자/타입검증, #2/#3/#5/#6),
  런타임 진입점 `EvalNumber`/`EvalBool`(#5), `BasePreds()`(`has`/`isOwner`/`paid`, #3).
- **불변:** 순수·결정성(D12) — RNG 0, wall-clock 0, map-iteration 0. 식별자/타입 오류 = **로드 실패**(D10),
  런타임 산술 엣지(0나눗셈·NaN) = **고정 정책 0**(#6) → eval은 error 분기 없는 `float64`/`bool` 반환. 도메인
  클램프는 호출자(#6). num↔bool 암묵 coercion 금지(#7). 고정 연산자 우선순위(D12).
- **테스트:** 산술/비교/논리 table-driven + §6 예제(`STR*0.5+AGI*0.3`, `(…>0.5)|(AGI>terrain.depth)`),
  술어 호출(`has(key)|STR>door.lockStrength|paid(toll)|isOwner`), 미정의 식별자/술어/arity → 로드 실패,
  타입 clash → 로드 실패, `want` 불일치 → 로드 실패, div-0/NaN→0, no-clamp, introspection 3종 정렬·중복제거,
  결정성 골든(formula+Context-stub 시퀀스 → 바이트동일 digest, 재-Parse 재현), read-only, `-race` 동시 eval,
  no-IO/no-rng/no-stats/no-gates import grep guard.
- **블로커:** 없음 — `core`만 의존, stage 1. (gates/climate/flora/config 무접촉.)

### Pxb — gates 통합 준비 → 단계적 스왑 (#4 단계적; golden 동일성 검증 후에만 스왑)
- **Pxb-1 (동일성 검증, gates 무수정):** expr leaf/composite/comparison semantics가 `gates.evalExpr`+`cmpOp`
  (`backend/engine/mind/gates/eval.go`)와 **바이트동일**함을 증명하는 parallel 테스트 — 각 shipped gate predicate
  (`capability_floor`/`knowledge`/base `conscience`/`stamina`/`apathy`/`adrenaline`)를 `expr.Program`로 재표현,
  battery of `AgentSnapshot`에서 `EvalBool` == `gates.evalExpr`. **expr는 gates를 import하지 않고, gates는
  수정하지 않는다**(테스트는 패키지 경계 밖). ⇒ 스왑이 golden-중립임을 보장.
- **Pxb-2 (실제 스왑, 의도적 재기준):** gates가 expr를 import → 중복 `GateExpr`/`Op`/`cmpOp` 제거, boolean
  부분집합만 사용(glossary "one shared evaluator" 충족). **결정 필요(Open Q, gates owner/human):** gates가
  자기 on-disk YAML leaf 형태를 유지(eval 엔진만 스왑, golden-중립)하느냐, expr 텍스트 수식 문법으로 이주
  (content+schema 마이그레이션, `gates.schema.json` 3→4 가능)하느냐. 권장: **엔진만 스왑 먼저**(golden-중립),
  문법 이주는 후속. 이 phase에서만 `p3_gates.json`/스키마 재기준 가능.
- **블로커:** Pxb-2는 gates owner 결정 대기(엔진-스왑 vs 문법-이주). Pxb-1·Pxa는 무블로커.

### Pxc — climate/flora 소비 (`platform/config`에 컴파일 경로 연결)
- **출하물:** `platform/config`가 `content/climate.yaml` `when:` 조건과 `content/objects.yaml` `flora:` 수식
  (suitability/length-rate/width-rate/shade(width)/yield-chance)을 `expr.Parse`로 컴파일 → 각 모듈의 compiled
  `Rules`(`climate.Rules`/`flora.Rules`)에 `expr.Program`로 저장. climate/flora는 자기 `Context` 어댑터
  (`CellState` / `SiteInput`+`Plant`)를 구현해 `EvalBool`/`EvalNumber` 호출.
- **불변:** climate/flora SPEC이 이미 가정한 `expr.Program`+`expr.Context` 형태 충족(SPEC 재작성 불필요).
  Attr-name 교차검증은 **각 소비자의 config 단계**가 `ReadsAttrs()`를 자기 operand 어휘에 대조(전역 attr
  레지스트리 없음, D10). 도입은 climate-off/flora-off(빈 `Rules`) → 기존 world/navmap/perception 골든 불변;
  활성화·재기준은 climate M4 / flora 활성화 phase 소관(여기 아님).
- **테스트:** config 컴파일 path(수식 문자열 → `Program`, 미정의 operand/술어 → 로드 실패), climate
  `Rules.Eval`(bool)·flora `Suitability`/rates(number)가 expr 통해 동작, 교차검증 골든. (climate/flora 단위
  골든 자체는 그 모듈 SPEC AC.)
- **블로커:** Pxa 완료. (climate/flora 모듈 골든 활성화는 별도 phase.)

### Pxd — 술어 구현 (`has`/`isOwner`/`paid`) + economy/portal seam
- **출하물:** §9 portal access 수식(`has(key) | STR > door.lockStrength | paid(toll) | isOwner`)을 평가하는
  실 술어 — 호출자(actions/economy/portal)가 자기 `Context.Pred` 어댑터를 실제 inventory/ownership/toll
  view 위에 구현 + 자기 `knownPreds`로 등록(코어·expr 무수정, D10). expr는 테이블 형태 + `BasePreds()`만 제공.
- **불변:** 새 술어 = 호출자 구현 + 테이블 등록(엔진 무수정, D10/D2 — 제도 하드코딩 금지). 술어는 boolean,
  비교/논리에서만 사용(#7). arity-1-Tag 시그니처 유지(§9 portal 충분).
- **테스트:** portal 수식이 inventory/ownership/toll 변화에 따라 가부 변화(soft lock = stat 경합 → 강제 침입
  창발, D2), 미등록 술어 = 로드 실패.
- **블로커:** Pxa 완료 + economy/portal `Context` view. (predicate-arg richness — 다중/타입 인자는 향후 별도
  결정, §1 #3 비차단 Open.)
