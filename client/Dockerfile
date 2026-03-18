FROM node:24-alpine
 
WORKDIR /app
 
COPY package.json package-lock.json ./
RUN npm ci
 
COPY . .
 
ARG VITE_API_URL
ARG VITE_SSE_URL
 
RUN if [ -n "$VITE_API_URL" ]; then export VITE_API_URL=$VITE_API_URL; fi && \
    if [ -n "$VITE_SSE_URL" ]; then export VITE_SSE_URL=$VITE_SSE_URL; fi && \
    npm run build
 