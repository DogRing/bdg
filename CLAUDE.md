# <프로젝트명> (슬라임 시티: backend=Go / frontend=React)

> 명세 우선(SPEC-first) · 계층 모듈 방식. 큰 파일을 통째로 읽지 않고
> 폴더별 `SPEC.md`로 의도를 관리하며, 작업은 서브에이전트로 분리해 진행한다.

## 요구사항 입력처
- **개발 요구사항·명세는 `docs/PRD.md`에 작성한다.** (사람이 쓰는 최상위 입력)
- backend ↔ frontend 경계(API 계약)는 `docs/api-contract.md`가 단일 소스다.
- 모듈별 상세 명세는 각 폴더의 `SPEC.md`에 두며, spec-architect가 PRD를 분해해 생성한다.

## 레이아웃
- `backend/` — Go. 스택·컨벤션은 `backend/CLAUDE.md` (해당 폴더 작업 시 자동 로드).
- `frontend/` — React. 스택·컨벤션은 `frontend/CLAUDE.md`.
- `docs/PRD.md` 요구사항 / `docs/api-contract.md` API 계약 / `docs/templates/SPEC.template.md` 양식.
- `.claude/agents/` 서브에이전트(분해 / 구현 / 검증).

## 작업 불변식 (권고)
1. 코드 작성·수정 전, 그 폴더의 `SPEC.md`를 먼저 확인/최신화한다.
2. 코드는 최신 `SPEC.md`에 맞춘다. 어긋나면 SPEC를 먼저 고친다.
3. 상위 `SPEC.md`는 추상 레벨 유지, 하위는 경로로 *참조만*(복붙 금지).
4. **backend / frontend는 서로의 구현을 읽지 않는다. 둘의 접점은 `docs/api-contract.md`뿐이다.**
5. 한 파일이 ~400줄 초과면 하위 모듈로 분해하고 각 폴더에 `SPEC.md`를 둔다.
6. 구현은 의존성이 없는 leaf 모듈부터 하나씩.

## 에이전트
- `spec-architect`: PRD/계약을 top-down 분해 → 폴더 + `SPEC.md` 작성 (코드 X).
- `implementer`: 단일 모듈을 SPEC대로 구현 (자기 폴더 + 부모가 준 인터페이스/계약만 참조).
- `reviewer`: SPEC 준수 검증 (read-only).

표준 흐름: 분해 → leaf부터 구현 → 검증 → `NEEDS_FIX` 환류. 메인 세션은 상위 SPEC와 요약만 본다.