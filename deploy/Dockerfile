# ============================================================
# AirGate Core — 多阶段构建
# 构建上下文为上级目录:
#   docker build -f airgate-core/deploy/Dockerfile -t airgate-core ..
# ============================================================

# ---- 阶段 1: 构建前端 ----
FROM node:22-alpine AS frontend
WORKDIR /build
RUN corepack enable && corepack prepare pnpm@11.1.1 --activate

# 构建 Core 前端
COPY airgate-core/web/package.json airgate-core/web/pnpm-lock.yaml airgate-core/web/pnpm-workspace.yaml ./airgate-core/web/
RUN cd airgate-core/web && pnpm install --frozen-lockfile
COPY airgate-core/web ./airgate-core/web
RUN cd airgate-core/web && pnpm build

# ---- 阶段 2: 构建后端 ----
FROM golang:1.25-alpine AS backend
RUN apk add --no-cache git
WORKDIR /build
ENV GOPRIVATE=github.com/DouDOU-start/airgate-sdk \
    GONOPROXY=github.com/DouDOU-start/airgate-sdk \
    GONOSUMDB=github.com/DouDOU-start/airgate-sdk

COPY airgate-core/backend/go.mod airgate-core/backend/go.sum ./airgate-core/backend/
RUN cd airgate-core/backend && go mod download -x

# 拷贝源码
COPY airgate-core/backend ./airgate-core/backend

# 把上一个阶段的前端产物拷进 //go:embed 目录，让 backend 把 SPA 打进二进制
RUN rm -rf airgate-core/backend/internal/web/webdist && mkdir -p airgate-core/backend/internal/web/webdist
COPY --from=frontend /build/airgate-core/web/dist/. airgate-core/backend/internal/web/webdist/

# 镜像内的 Version 由 build-arg 注入，与 release workflow 的 git tag 一致
ARG VERSION=docker
RUN cd airgate-core/backend && CGO_ENABLED=0 go build -buildvcs=false -trimpath \
    -ldflags "-X github.com/DouDOU-start/airgate-core/internal/version.Version=${VERSION}" \
    -o /server ./cmd/server

# ---- 阶段 3: 运行时 ----
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app

# 二进制完全自包含：前端 SPA 与翻译文件均已 //go:embed 进 binary
COPY --from=backend /server ./server
COPY airgate-core/deploy/config.docker.yaml ./config.yaml

RUN mkdir -p data/uploads data/assets

EXPOSE 9517

ENV CONFIG_PATH=/app/config.yaml

CMD ["./server"]
