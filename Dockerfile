# ============================================================
# AirGate Core — 多阶段构建
# 构建上下文必须为上级目录（包含 airgate-sdk）:
#   docker build -f airgate-core/Dockerfile -t airgate-core ..
# ============================================================

# ---- 阶段 1: 构建前端 ----
FROM node:22-alpine AS frontend
WORKDIR /build

# 构建 @airgate/theme（airgate-sdk 前端依赖）
COPY airgate-sdk/frontend/package*.json ./airgate-sdk/frontend/
RUN cd airgate-sdk/frontend && npm ci
COPY airgate-sdk/frontend ./airgate-sdk/frontend
RUN cd airgate-sdk/frontend && npm run build

# 构建 Core 前端
COPY airgate-core/web/package*.json ./airgate-core/web/
RUN cd airgate-core/web && npm ci
COPY airgate-core/web ./airgate-core/web
RUN cd airgate-core/web && npm run build

# ---- 阶段 2: 构建后端 ----
FROM golang:1.25-alpine AS backend
RUN apk add --no-cache git
WORKDIR /build

# 生成精简的 go.work（仅包含构建所需的模块，排除 airgate-openai 等）
COPY airgate-sdk/go.mod airgate-sdk/go.sum ./airgate-sdk/
COPY airgate-core/backend/go.mod airgate-core/backend/go.sum ./airgate-core/backend/
RUN go work init ./airgate-sdk ./airgate-core/backend && go mod download -x

# 拷贝源码并编译
COPY airgate-sdk ./airgate-sdk
COPY airgate-core/backend ./airgate-core/backend
RUN cd airgate-core/backend && CGO_ENABLED=0 go build -buildvcs=false -trimpath -o /server ./cmd/server

# ---- 阶段 3: 运行时 ----
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app

COPY --from=backend /server ./server
COPY --from=frontend /build/airgate-core/web/dist ./frontend
COPY airgate-core/backend/config.docker.yaml ./config.yaml
COPY airgate-core/backend/locales ./locales

RUN mkdir -p data/plugins

EXPOSE 9517

ENV CONFIG_PATH=/app/config.yaml

CMD ["./server"]
