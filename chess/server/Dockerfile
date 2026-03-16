# ── 빌드 스테이지 ──
FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o chess-server .

# ── 실행 스테이지 ──
FROM scratch

COPY --from=builder /app/chess-server /chess-server

EXPOSE 8080
CMD ["/chess-server"]