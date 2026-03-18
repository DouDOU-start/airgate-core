# AirGate Core Makefile

# 变量
BACKEND_DIR := backend
WEB_DIR := web
BINARY := $(BACKEND_DIR)/server
GO := GOTOOLCHAIN=local go

.PHONY: help dev dev-backend dev-frontend build build-backend build-frontend \
        ent lint fmt test clean install ci pre-commit setup-hooks \
        docker-build docker-rebuild docker-up docker-down docker-dev

help: ## 显示帮助信息
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

# ===================== 开发 =====================

dev: ## 同时启动前后端开发服务器
	@echo "启动开发环境..."
	@$(MAKE) dev-backend &
	@$(MAKE) dev-frontend
	@wait

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

build: build-backend build-frontend ## 构建前后端

build-backend: ## 编译后端二进制
	@cd $(BACKEND_DIR) && $(GO) build -o server ./cmd/server
	@echo "后端编译完成: $(BINARY)"

build-frontend: ## 构建前端产物
	@cd $(WEB_DIR) && npm run build
	@echo "前端构建完成: $(WEB_DIR)/dist/"

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

ci: lint test build-backend ## 本地运行与 CI 完全一致的检查

pre-commit: lint build-backend ## pre-commit hook 调用（跳过耗时的测试）

setup-hooks: ## 安装 Git pre-commit hook
	@echo '#!/bin/sh' > .git/hooks/pre-commit
	@echo 'make pre-commit' >> .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo "pre-commit hook 已安装"

# ===================== 依赖安装 =====================

SDK_FRONTEND := ../airgate-sdk/frontend

install: setup-hooks ## 安装全部依赖（含 SDK 前端构建）
	@cd $(SDK_FRONTEND) && npm install && npm run build && echo "SDK 前端构建完成"
	@cd $(BACKEND_DIR) && $(GO) mod download
	@rm -rf $(WEB_DIR)/node_modules/.vite
	@cd $(WEB_DIR) && npm install
	@command -v air > /dev/null 2>&1 || (echo "安装 air（热重载工具）..."; $(GO) install github.com/air-verse/air@latest)
	@echo "依赖安装完成"

# ===================== Docker =====================

docker-build: ## 构建 Docker 镜像（使用缓存）
	@docker build -f Dockerfile -t airgate-core:latest ..

docker-rebuild: ## 构建 Docker 镜像（无缓存，强制全量重建）
	@docker build -f Dockerfile -t airgate-core:latest --no-cache ..

docker-up: ## 启动生产环境（后台运行）
	@docker compose up -d

docker-down: ## 停止生产环境
	@docker compose down

docker-dev: ## 启动开发环境（源码编译模式）
	@docker compose -f docker-compose.dev.yml up

# ===================== 清理 =====================

clean: ## 清理构建产物
	@rm -f $(BINARY)
	@rm -rf $(WEB_DIR)/dist
	@echo "清理完成"
