# Claude Code 명세 우선·계층 모듈 시스템 — code-server 가이드

큰 파일을 통째로 읽지 않고, 폴더별 `SPEC.md`로 의도를 관리하며,
작업을 **분해 / 구현 / 검증** 세 서브에이전트로 나눠 진행하기 위한 구성이다.

## 1. 파일 구성

```
<repo>/
  CLAUDE.md                      # 운영 규약(권고). 메인 세션이 항상 로드
  .claudeignore                  # 컨텍스트 경량화 + 민감 파일 제외
  .claude/
    settings.json                # 권한 베이스라인(확장·CLI 공유)
    agents/
      spec-architect.md          # top-down 분해 + SPEC 작성 (코드 X)
      implementer.md             # 단일 모듈 구현
      reviewer.md                # SPEC 준수 검증 (read-only)
  docs/
    templates/
      SPEC.template.md           # SPEC 표준 양식
    claude-spec-system.md        # 이 문서
  src/
    <module>/
      SPEC.md                    # 모듈별 명세 (architect가 생성)
      ...                        # 구현 (implementer가 생성)
```

위 파일들은 전부 **저장소에 커밋되는 프로젝트 스코프**다.
컨테이너/Pod이 재시작돼도 살아남고 코드와 함께 버전 관리된다.

## 2. code-server에서 설치 / 실행

확장은 CLI를 감싼 형태이며, 패널 입력은 터미널 CLI와 동일한 엔진·인증·`CLAUDE.md`를 쓴다.
code-server는 기본 마켓플레이스가 Open VSX이므로, Extensions 뷰에서 "Claude Code"가
잡히면 그대로 설치해 GUI(diff 리뷰 등)를 쓸 수 있다.

다만 컨테이너 환경에서 가장 견고한 경로는 **통합 터미널에서 CLI를 직접 실행**하는 것이다.
서브에이전트·`CLAUDE.md`·설정은 전부 파일 기반이라 GUI든 CLI든 동작이 동일하다.

```bash
# 컨테이너에 Node 18+ 필요
npm install -g @anthropic-ai/claude-code
claude --version
claude                      # 저장소 루트에서 실행
```

## 3. 인증 (원격/컨테이너 권장 방식)

브라우저 기반 원격 환경에서는 OAuth 콜백이 번거롭다.
`ANTHROPIC_API_KEY`를 환경변수로 주입하는 방식이 깔끔하다.
k8s에 code-server를 배포한다면 Secret → 컨테이너 `env`로 넣으면 터미널·확장이 모두 상속받는다.

```yaml
# 예시: code-server Pod 컨테이너 스펙 일부
env:
  - name: ANTHROPIC_API_KEY
    valueFrom:
      secretKeyRef:
        name: claude-code
        key: api-key
```

## 4. 영속성 (k8s/컨테이너 핵심)

- **프로젝트 스코프**(`<repo>/.claude/`, `CLAUDE.md`, `SPEC.md`): 레포에 커밋 → 재시작에도 유지.
  → 에이전트·SPEC·설정은 전부 여기 둔다.
- **유저 스코프**(`~/.claude/`): 전역 설정과 인증 토큰(`~/.claude/.credentials.json`)이 여기 있다.
  컨테이너 홈은 휘발성이라 Pod 재시작 시 사라진다.
  매 재시작 재로그인을 피하려면 `~/.claude`를 PVC에 마운트하거나, 위 3번처럼 API 키를 쓴다.

## 5. 에이전트 호출

`claude` 실행 후:

- **자동 위임**: 작업 유형이 각 에이전트의 `description`과 맞으면 자동으로 위임된다.
- **명시 호출**: `@spec-architect ...`, `@implementer ...`, `@reviewer ...`
  또는 `claude --agent implementer`.
- **관리**: `/agents` 로 목록 확인·대화형 생성/편집.

### 표준 오케스트레이션 (메인 세션에 그대로 지시)

```
이 기능을 spec-architect로 모듈 분해하고 각 폴더에 SPEC.md를 작성해.
그다음 leaf 모듈부터 순서대로 implementer로 하나씩 구현하고, 각 모듈마다 reviewer로 검증해.
reviewer가 NEEDS_FIX면 그 권고를 implementer에 다시 넘겨.
너(메인)는 상위 SPEC와 각 요약만 보고 진행하고, 큰 코드 파일은 직접 읽지 마.
```

### 개별 호출 시 주의

서브에이전트는 부모의 대화 기록을 보지 못한다. `implementer`/`reviewer`를 개별로 부를 때는
**대상 모듈의 SPEC 경로와 의존 인터페이스를 프롬프트에 직접 넣어준다.**

```
implementer로 src/auth/token 모듈을 SPEC대로 구현해.
의존 인터페이스는 src/auth/SPEC.md 의 "인터페이스" 절을 따른다.
```

## 6. 동작 원리 (왜 토큰이 줄어드는가)

- 서브에이전트는 각자 **독립 컨텍스트**에서 돌고, 장황한 중간 출력은 그 안에 갇히며 **요약만** 메인에 돌아온다.
- 메인 세션은 추상화된 상위 SPEC와 요약만 보므로, 모듈이 늘어도 메인 컨텍스트가 거의 안 커진다.
- 상위 SPEC는 하위를 **경로로 참조만** 한다(`@import` 금지 — import는 시작 시 전부 로드되어 효과가 사라진다).

## 7. 튜닝 포인트

- `.claude/settings.json` 의 `permissions.allow` 에 프로젝트 테스트 명령을 추가한다(예: `Bash(make test:*)`).
  reviewer/implementer가 프롬프트 없이 테스트를 돌릴 수 있게 하기 위함.
- `.claudeignore` 에 빌드 산출물·대용량 파일을 추가해 컨텍스트를 더 가볍게 유지한다.
- 비용 관리: 분해는 `opus`(spec-architect), 구현/검증은 `sonnet`(implementer/reviewer)으로 분리돼 있다.
  세션 전체 모델을 강제하려면 `CLAUDE_CODE_SUBAGENT_MODEL` 환경변수를 쓴다.

## 주의

- 위 운영 규약은 **권고**다(강제 아님). 반드시 강제하려면 `.claude/settings.json` 의
  `PreToolUse` 훅으로 게이트를 걸 수 있으나, 본 구성에는 포함하지 않았다.
- 서브에이전트는 다른 서브에이전트를 부를 수 없다. 분해→구현→검증의 순서 제어는 메인 세션이 담당한다.
- 서브에이전트는 대화형 권한 프롬프트를 띄울 수 없다. `implementer`/`spec-architect`는
  `permissionMode: acceptEdits`로 쓰기 승인을 자동화했고, `reviewer`는 Write/Edit 권한 자체가 없어
  소스를 변경할 수 없다(테스트 실행은 allow 목록에 의존).
- 프레임워크/플래그는 버전에 민감하다. `claude --version` 기준으로 확인 후 조정한다.