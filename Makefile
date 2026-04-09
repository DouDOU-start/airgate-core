# AirGate Core Makefile

# 变量
BACKEND_DIR := backend
WEB_DIR := web
SDK_FRONTEND := ../airgate-sdk/frontend
OPENAI_PLUGIN := ../airgate-openai/web
OPENAI_ASSETS := $(BACKEND_DIR)/data/plugins/gateway-openai/assets
EPAY_PLUGIN := ../airgate-epay/web
EPAY_ASSETS := $(BACKEND_DIR)/data/plugins/payment-epay/assets
BINARY := $(BACKEND_DIR)/server
WEBDIST := $(BACKEND_DIR)/internal/web/webdist
GO := GOTOOLCHAIN=local go

# 版本号：默认从 git 派生（dirty 检测），release workflow 通过 -ldflags 注入。
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.Version=$(VERSION)

.PHONY: help dev dev-backend dev-frontend dev-sdk dev-plugins dev-plugin-openai dev-plugin-epay \
        build build-backend build-frontend \
        build-plugins sync-plugins \
        ent lint fmt test clean install ci pre-commit setup-hooks \
        docker-build docker-rebuild docker-up docker-down docker-restart docker-dev

help: ## 显示帮助信息
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

# ===================== 开发 =====================

dev: ## 同时启动 SDK watch + 插件 watch + 前后端开发服务器
	@echo "启动开发环境..."
	@$(MAKE) sync-plugins
	@$(MAKE) dev-sdk &
	@$(MAKE) dev-plugins &
	@$(MAKE) dev-backend &
	@$(MAKE) dev-frontend
	@wait

dev-sdk: ## 启动 SDK 主题 watch 模式（修改 token 自动编译）
	@cd $(SDK_FRONTEND) && npm run dev

dev-plugins: ## 启动所有插件前端 watch 模式（修改后自动构建并同步到 core）
	@echo "启动插件前端 watch："
	@echo "  - openai → $(OPENAI_ASSETS)/"
	@echo "  - epay   → $(EPAY_ASSETS)/"
	@$(MAKE) dev-plugin-openai &
	@$(MAKE) dev-plugin-epay &
	@wait

dev-plugin-openai: ## 单独 watch openai 插件前端
	@cd $(OPENAI_PLUGIN) && npx vite build --watch --outDir ../../airgate-core/$(OPENAI_ASSETS) 2>&1 | grep -v 'not inside project root'

dev-plugin-epay: ## 单独 watch epay 插件前端
	@cd $(EPAY_PLUGIN) && npx vite build --watch --outDir ../../airgate-core/$(EPAY_ASSETS) 2>&1 | grep -v 'not inside project root'

dev-backend: ## 启动后端（带热重载，需要 air）
	@cd $(BACKEND_DIR) && \
	if command -v air > /dev/null 2>&1; then \
		air; \
	else \
		echo "未安装 air，使用普通模式启动（无热重载）"; \
		echo "安装 air: go install github.com/air-verse/air@latest"; \
		$(GO) run ./cmd/server; \
	fi

dev-frontend: ## 启动前端开发服务器
	@cd $(WEB_DIR) && npm run dev

# ===================== 构建 =====================

build: build-frontend build-backend build-plugins ## 构建前后端及插件（顺序：前端 → 嵌入 → 后端）

ensure-webdist: ## 把 web/dist 同步到 backend/internal/web/webdist 供 go:embed 使用
	@if [ -d $(WEB_DIR)/dist ] && [ "$$(ls -A $(WEB_DIR)/dist 2>/dev/null)" ]; then \
		mkdir -p $(WEBDIST); \
		find $(WEBDIST) -mindepth 1 ! -name '.gitkeep' -exec rm -rf {} +; \
		cp -r $(WEB_DIR)/dist/. $(WEBDIST)/; \
		echo "前端产物已同步到 $(WEBDIST)"; \
	else \
		echo "[ensure-webdist] $(WEB_DIR)/dist 为空，将使用占位 .gitkeep（go build 仍可通过，但运行时会报缺失前端）"; \
		mkdir -p $(WEBDIST); \
		[ -f $(WEBDIST)/.gitkeep ] || touch $(WEBDIST)/.gitkeep; \
	fi

build-backend: ensure-webdist ## 编译后端二进制（自动嵌入最新前端）
	@cd $(BACKEND_DIR) && $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o server ./cmd/server
	@echo "后端编译完成: $(BINARY) (version: $(VERSION))"

build-frontend: ## 构建前端产物
	@cd $(WEB_DIR) && npm run build
	@echo "前端构建完成: $(WEB_DIR)/dist/"

build-plugins: sync-plugins ## 构建插件前端并同步到 core
	@echo "插件前端构建完成"

sync-plugins: ## 构建所有插件前端并同步到 data/plugins/
	@echo "构建并同步 openai 插件前端..."
	@cd $(OPENAI_PLUGIN) && npm run build
	@mkdir -p $(OPENAI_ASSETS)
	@cp $(OPENAI_PLUGIN)/dist/index.js $(OPENAI_ASSETS)/index.js
	@echo "openai 插件前端已同步到 $(OPENAI_ASSETS)/"
	@echo "构建并同步 epay 插件前端..."
	@cd $(EPAY_PLUGIN) && npm run build
	@mkdir -p $(EPAY_ASSETS)
	@cp $(EPAY_PLUGIN)/dist/index.js $(EPAY_ASSETS)/index.js
	@echo "epay 插件前端已同步到 $(EPAY_ASSETS)/"

# ===================== 代码生成 =====================

ent: ## 生成 Ent ORM 代码
	@cd $(BACKEND_DIR) && $(GO) generate ./ent
	@echo "Ent 代码生成完成"

# ===================== 质量检查 =====================

lint: ## 代码检查（需要安装 golangci-lint）
	@if ! command -v golangci-lint > /dev/null 2>&1; then \
		echo "错误: 未安装 golangci-lint，请执行: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"; \
		exit 1; \
	fi
	@cd $(BACKEND_DIR) && golangci-lint run ./...
	@cd $(WEB_DIR) && npx tsc -b --noEmit
	@cd $(WEB_DIR) && npm run lint
	@echo "代码检查通过"

fmt: ## 格式化代码
	@cd $(BACKEND_DIR) && \
	if command -v goimports > /dev/null 2>&1; then \
		goimports -w -local github.com/DouDOU-start .; \
	else \
		$(GO) fmt ./...; \
	fi
	@echo "代码格式化完成"

test: ## 运行测试
	@cd $(BACKEND_DIR) && $(GO) test ./...
	@echo "后端测试完成"


# ===================== CI =====================

ci: lint test vet verify-ent build-backend ## 本地运行与 CI 完全一致的检查

pre-commit: lint vet build-backend ## pre-commit hook 调用（跳过耗时的测试）

vet: ## 静态分析
	@cd $(BACKEND_DIR) && $(GO) vet ./...

verify-ent: ## 验证 Ent 生成代码是否最新
	@cd $(BACKEND_DIR) && GOWORK=off go run entgo.io/ent/cmd/ent generate ./ent/schema
	@cd $(BACKEND_DIR) && \
	if ! git diff --quiet ent/; then \
		echo "❌ Ent 生成代码不一致，请运行: make ent"; \
		git diff --stat ent/; \
		exit 1; \
	fi
	@echo "Ent 生成代码一致"

setup-hooks: ## 安装 Git pre-commit hook
	@echo '#!/bin/sh' > .git/hooks/pre-commit
	@echo 'make pre-commit' >> .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo "pre-commit hook 已安装"

# ===================== 依赖安装 =====================

install: setup-hooks ## 安装全部依赖（含 SDK 前端构建）
	@cd $(SDK_FRONTEND) && npm install && npm run build && echo "SDK 前端构建完成"
	@cd $(BACKEND_DIR) && $(GO) mod download
	@rm -rf $(WEB_DIR)/node_modules/.vite
	@cd $(WEB_DIR) && npm install
	@command -v air > /dev/null 2>&1 || (echo "安装 air（热重载工具）..."; $(GO) install github.com/air-verse/air@latest)
	@echo "依赖安装完成"

# ===================== Docker =====================

docker-build: ## 构建 Docker 镜像（使用缓存）
	@docker build -f deploy/Dockerfile -t airgate-core:latest ..

docker-rebuild: ## 构建 Docker 镜像（无缓存，强制全量重建）
	@docker build -f deploy/Dockerfile -t airgate-core:latest --no-cache ..

docker-up: ## 启动生产环境（后台运行）
	@docker compose -f deploy/docker-compose.yml up -d

docker-down: ## 停止生产环境
	@docker compose -f deploy/docker-compose.yml down

docker-restart: ## 重启生产环境
	@docker compose -f deploy/docker-compose.yml restart

docker-dev: ## 启动开发环境（源码编译模式）
	@docker compose -f deploy/docker-compose.dev.yml up

# ===================== 清理 =====================

clean: ## 清理构建产物
	@rm -f $(BINARY)
	@rm -rf $(WEB_DIR)/dist
	@echo "清理完成"
