# frontend (React)

> 이 폴더에서 작업할 때 자동 로드된다. React 영역의 스택·컨벤션을 정의한다.
> API 계약은 `../docs/api-contract.md`를 단일 소스로 따른다.
> backend 코드는 읽지 않는다.

## 스택 (예시 — 프로젝트에 맞게 조정)
- React 18+ + Vite + TypeScript
- 함수형 컴포넌트 + Hooks
- 서버 상태: TanStack Query / 클라이언트 상태: Zustand  (또는 Context)
- 라우팅: React Router
- HTTP: 계약에서 생성한 타입 클라이언트(openapi-typescript 등) + fetch / axios

## 명령어
- 개발: `npm run dev`
- 빌드: `npm run build`
- 테스트: `npm run test`  (vitest + @testing-library/react)
- 린트: `npm run lint`  (eslint + prettier)
- 타입체크: `npm run typecheck`  (`tsc --noEmit`)

## 컨벤션
- 컴포넌트는 작게, 책임 단위로 폴더 분리하고 각 폴더에 `SPEC.md`를 둔다.
- API 호출 / 타입은 `docs/api-contract.md`에서 파생한 것만 사용한다(임의 `any` 금지).
- 서버 데이터는 TanStack Query로, 전역 UI 상태는 스토어로, 표현은 컴포넌트로 분리.
- 단위 테스트는 컴포넌트 옆에 둔다.