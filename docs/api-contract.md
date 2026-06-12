# API 계약 (backend ↔ frontend 단일 소스)

> backend와 frontend는 서로의 코드를 읽지 않는다. 이 문서가 유일한 접점이다.
> 변경 시 이 파일을 먼저 고치고, 양쪽 모듈 `SPEC.md`가 이를 참조한다.

## 전송 / 버전
- 프로토콜: REST(JSON) | gRPC | GraphQL  중 택1
- 베이스 경로: `/api/v1`

## 공통 규약
- 인증: (예) `Authorization: Bearer <jwt>`
- 에러 형식:
  ```json
  { "error": { "code": "STRING", "message": "STRING" } }
  ```
- 페이지네이션: (예) `?page=&size=`, 응답에 `total` 포함

## 엔드포인트 / 타입
### <리소스>
- `GET /api/v1/<...>` — 설명
  - 요청 파라미터:
  - 응답:
    ```json
    { }
    ```
- `POST /api/v1/<...>` — 설명
  - 요청 바디 / 응답:

> 규모가 커지면 OpenAPI(`openapi.yaml`)나 proto 파일로 옮기고 이 문서는 링크만 둔다.
> frontend는 이 계약에서 타입 / 클라이언트를 생성한다(예: openapi-typescript).
> backend의 DTO / 응답 타입은 이 계약과 1:1로 일치시킨다.