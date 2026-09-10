# AirGate Core Makefile

# 子 make 均在本目录运行，关闭 Entering/Leaving directory 提示（dev 并行拉起
# dev-backend / dev-frontend 两个子 make 时会连打两行，纯噪音）
MAKEFLAGS += --no-print-directory

# 变量
BACKEND_DIR := backend
WEB_DIR := web
BINARY := $(BACKEND_DIR)/server
WEBDIST := $(BACKEND_DIR)/internal/web/webdist
# 钉死 go.mod 的 1.26：GOTOOLCHAIN=auto 会跟着 golangci-lint 的 go.mod 升到 1.27，
# 而 v2.12.2 的 staticcheck 在 1.27 上分析 stdlib poll 会 panic（CI 只看到 make exit 2）。
GO := GOTOOLCHAIN=go1.26.0 go
GOLANGCI_LINT_VERSION := v2.12.2

# 版本号：默认从 git 派生（dirty 检测），release workflow 通过 -ldflags 注入。
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/DouDOU-start/airgate-core/internal/version.Version=$(VERSION)

.PHONY: help dev dev-backend dev-frontend \
        build build-backend build-frontend ensure-webdist \
        ent lint fmt test clean install ci pre-commit setup-hooks verify-ent verify-ent-changed \
        docker-build docker-rebuild docker-up docker-down docker-restart docker-dev

help: ## 显示帮助信息
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

# ===================== 开发 =====================

dev: ## 同时启动前后端开发服务器
	@echo "启动开发环境..."
	@cleanup() { \
		pids="$$(jobs -p)"; \
		if [ -n "$$pids" ]; then \
			echo "停止开发子进程..."; \
			kill $$pids 2>/dev/null || true; \
		fi; \
		wait 2>/dev/null || true; \
	}; \
	trap cleanup INT TERM EXIT; \
	$(MAKE) dev-backend & \
	$(MAKE) dev-frontend

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
	@cd $(WEB_DIR) && pnpm dev

# ===================== 构建 =====================

build: build-frontend build-backend ## 构建前后端（顺序：前端 → 嵌入 → 后端）

ensure-webdist: ## 把 web/dist 同步到 backend/internal/web/webdist 供 go:embed 使用
	@mkdir -p $(WEBDIST)
	@# .gitkeep 是仓库跟踪的占位符（.gitignore 已用 ! 保留）：任何分支都先补齐，
	@# 保证它始终躺在磁盘上，避免被 git add -A 当作删除误提交。
	@touch $(WEBDIST)/.gitkeep
	@if [ -d $(WEB_DIR)/dist ] && [ "$$(ls -A $(WEB_DIR)/dist 2>/dev/null)" ]; then \
		find $(WEBDIST) -mindepth 1 ! -name '.gitkeep' -exec rm -rf {} +; \
		cp -r $(WEB_DIR)/dist/. $(WEBDIST)/; \
		echo "前端产物已同步到 $(WEBDIST)"; \
	else \
		echo "[ensure-webdist] $(WEB_DIR)/dist 为空，将使用占位 .gitkeep（go build 仍可通过，但运行时会报缺失前端）"; \
	fi

build-backend: ensure-webdist ## 编译后端二进制（自动嵌入最新前端）
	@cd $(BACKEND_DIR) && $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o server ./cmd/server
	@echo "后端编译完成: $(BINARY) (version: $(VERSION))"

build-frontend: ## 构建前端产物
	@cd $(WEB_DIR) && pnpm build
	@echo "前端构建完成: $(WEB_DIR)/dist/"

# ===================== 代码生成 =====================

ent: ## 生成 Ent ORM 代码
	@cd $(BACKEND_DIR) && GOWORK=off $(GO) generate ./ent
	@echo "Ent 代码生成完成"

# ===================== 质量检查 =====================

lint: ## 代码检查（使用固定版本 golangci-lint）
	@cd $(BACKEND_DIR) && $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...
	@cd $(WEB_DIR) && pnpm exec tsc -b --noEmit
	@cd $(WEB_DIR) && pnpm lint
	@echo "代码检查通过"

fmt: ## 格式化代码
	@cd $(BACKEND_DIR) && \
	if command -v goimports > /dev/null 2>&1; then \
		goimports -w -local github.com/DouDOU-start .; \
	else \
		$(GO) fmt ./...; \
	fi
	@echo "代码格式化完成"

test: ensure-webdist ## 运行测试（先同步嵌入前端，避免 internal/server 因空 webdist 直接 os.Exit）
	@cd $(BACKEND_DIR) && $(GO) test ./...
	@echo "后端测试完成"


# ===================== CI =====================

ci: lint test verify-ent build-backend ## 本地运行与 CI 完全一致的检查

pre-commit: lint verify-ent-changed build-backend ## pre-commit hook 调用（跳过耗时的测试；ent/schema 本次未改动时跳过重新生成）

verify-ent: ## 验证 Ent 生成代码是否最新（与 make ent 使用同一 go:generate 指令，含 feature flags）
	@cd $(BACKEND_DIR) && GOWORK=off $(GO) generate ./ent
	@cd $(BACKEND_DIR) && \
	if ! git diff --quiet ent/; then \
		echo "❌ Ent 生成代码不一致，请运行: make ent"; \
		git diff --stat ent/; \
		exit 1; \
	fi
	@echo "Ent 生成代码一致"

verify-ent-changed: ## pre-commit 专用：本次提交未改动 ent/schema 时跳过重新生成校验
	@if git diff --cached --quiet -- $(BACKEND_DIR)/ent/schema/; then echo "ent/schema unchanged, skip verify-ent"; else $(MAKE) verify-ent; fi

setup-hooks: ## 安装 Git hooks（pre-commit + commit-msg）
	@echo '#!/bin/sh' > .git/hooks/pre-commit
	@echo 'make pre-commit' >> .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@cp scripts/commit-msg .git/hooks/commit-msg
	@chmod +x .git/hooks/commit-msg
	@echo "Git hooks 已安装（pre-commit + commit-msg）"

# ===================== 依赖安装 =====================

install: setup-hooks ## 安装全部依赖（含首次 webdist 构建）
	@cd $(BACKEND_DIR) && $(GO) mod download
	@rm -rf $(WEB_DIR)/node_modules/.vite
	@cd $(WEB_DIR) && pnpm install
	@command -v air > /dev/null 2>&1 || (echo "安装 air（热重载工具）..."; $(GO) install github.com/air-verse/air@latest)
	@$(MAKE) build-frontend ensure-webdist
	@echo "依赖安装完成"

# ===================== Docker =====================

docker-build: ## 构建 Docker 镜像（使用缓存）
	@docker build -f deploy/Dockerfile -t airgate-core:latest .

docker-rebuild: ## 构建 Docker 镜像（无缓存，强制全量重建）
	@docker build -f deploy/Dockerfile -t airgate-core:latest --no-cache .

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
	@rm -rf $(WEB_DIR)/dist $(BACKEND_DIR)/tmp $(BACKEND_DIR)/bin
	@find $(WEBDIST) -mindepth 1 ! -name '.gitkeep' -exec rm -rf {} + 2>/dev/null || true
	@echo "清理完成"
