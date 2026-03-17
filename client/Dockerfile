# ── 빌드 스테이지 ──
FROM node:24-alpine AS builder

WORKDIR /app

COPY package.json package-lock.json ./
RUN npm ci

COPY . .

# 환경변수로 API/SSE 주소 주입
ARG VITE_API_URL=http://localhost:8080
ARG VITE_SSE_URL=http://localhost:3001
ENV VITE_API_URL=$VITE_API_URL
ENV VITE_SSE_URL=$VITE_SSE_URL

RUN npm run build

# ── 실행 스테이지 (nginx) ──
FROM nginx:alpine

COPY --from=builder /app/dist /usr/share/nginx/html

# SPA 라우팅 설정
COPY nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]