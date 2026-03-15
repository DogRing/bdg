# ── 빌드 스테이지 ──
FROM rust:latest AS builder

WORKDIR /app

# 의존성 캐시 레이어 (소스 변경 시 재빌드 방지)
COPY Cargo.toml Cargo.lock ./
RUN mkdir src && echo "fn main() {}" > src/main.rs
RUN cargo build --release
RUN rm src/main.rs

# 실제 소스 빌드
COPY src ./src
RUN touch src/main.rs && cargo build --release

# ── 실행 스테이지 ──
FROM debian:bookworm-slim

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=builder /app/target/release/chess-sse .

EXPOSE 3001
CMD ["./chess-sse"]