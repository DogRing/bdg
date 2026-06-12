# backend (Go)

> 이 폴더에서 작업할 때 자동 로드된다. Go 영역의 스택·컨벤션을 정의한다.
> API 계약은 `../docs/api-contract.md`를 단일 소스로 따른다.
> frontend 코드는 읽지 않는다.

## 스택 (예시 — 프로젝트에 맞게 조정)
- Go 1.22+, go modules
- 라우팅: net/http (stdlib) + chi  (또는 gin / echo)
- DB: pgx + sqlc  (또는 database/sql)
- 설정: env 기반, `context.Context` 전파

## 명령어
- 빌드: `go build ./...`
- 테스트: `go test ./...`
- 벳: `go vet ./...`
- 린트: `golangci-lint run`
- 포맷: `gofmt -w .`  (또는 `goimports -w .`)

## 컨벤션
- 함수는 `(T, error)`를 반환하고 에러는 `fmt.Errorf("...: %w", err)`로 감싼다. 정상 흐름에서 `panic` 금지.
- 모듈은 책임 단위로 폴더 분리하고 각 폴더에 `SPEC.md`를 둔다. 패키지명은 폴더명과 일치.
- 표준 레이아웃 권장: 엔트리포인트 `cmd/`, 비공개 코드 `internal/`.
- 핸들러는 얇게: 입출력 검증 + 도메인 호출까지. 비즈니스 로직은 도메인 패키지에.
- 공개 API에는 `context.Context`를 첫 인자로 전파한다.
- 요청 / 응답 타입은 `docs/api-contract.md`와 1:1로 맞춘다.
- 테이블 주도(table-driven) 테스트를 기본으로, 파일은 `_test.go`.