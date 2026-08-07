# ============================================================
# AirGate Core — 多阶段构建
# 构建上下文为仓库根:
#   docker build -f deploy/Dockerfile -t airgate-core .
# ============================================================

# ---- 阶段 1: 构建前端 ----
FROM node:22-alpine AS frontend
WORKDIR /build
RUN corepack enable && corepack prepare pnpm@11.1.1 --activate

COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./web/
RUN cd web && pnpm install --frozen-lockfile
COPY web ./web
RUN cd web && pnpm build

# ---- 阶段 2: 构建后端 ----
FROM golang:1.26-alpine AS backend
RUN apk add --no-cache git
WORKDIR /build

COPY backend/go.mod backend/go.sum ./backend/
RUN cd backend && go mod download -x

COPY backend ./backend

# 把上一个阶段的前端产物拷进 //go:embed 目录，让 backend 把 SPA 打进二进制
RUN rm -rf backend/internal/web/webdist && mkdir -p backend/internal/web/webdist
COPY --from=frontend /build/web/dist/. backend/internal/web/webdist/

# 镜像内的 Version 由 build-arg 注入，与 release workflow 的 git tag 一致
ARG VERSION=docker
RUN cd backend && CGO_ENABLED=0 go build -buildvcs=false -trimpath \
    -ldflags "-X github.com/DouDOU-start/airgate-core/internal/version.Version=${VERSION}" \
    -o /server ./cmd/server

# ---- 阶段 3: 运行时 ----
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app

# 二进制完全自包含：前端 SPA 与翻译文件均已 //go:embed 进 binary
COPY --from=backend /server ./server
COPY deploy/config.docker.yaml ./config.yaml

RUN mkdir -p data/uploads data/plugins

EXPOSE 9517

ENV CONFIG_PATH=/app/config.yaml

CMD ["./server"]
