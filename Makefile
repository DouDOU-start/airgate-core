# AirGate Core Makefile

# 变量
BACKEND_DIR := backend
WEB_DIR := web
SDK_FRONTEND := ../airgate-sdk/frontend
# 插件前端目录。所有插件 dev watch 都输出到自己的 web/dist；core 的
# servePluginAsset handler 在 dev 模式下从 <plugin>/web/dist 读，prod 模式从
# data/plugins/<id>/assets 读。这样三个插件的 dev 体验完全一致，没有特例。
OPENAI_PLUGIN := ../airgate-openai/web
CLAUDE_PLUGIN := ../airgate-claude/web
PLAYGROUND_PLUGIN := ../airgate-playground/web
EPAY_PLUGIN := ../airgate-epay/web
HEALTH_PLUGIN := ../airgate-health/web
# build-plugins 阶段（生产）同步各插件的 admin dist/index.js 到
# core 的 plugin assets dir；health 的公开状态页仍走 /status 反代，不经过这套 assets。
OPENAI_ASSETS := $(BACKEND_DIR)/data/plugins/gateway-openai/assets
CLAUDE_ASSETS := $(BACKEND_DIR)/data/plugins/gateway-anthropic/assets
PLAYGROUND_ASSETS := $(BACKEND_DIR)/data/plugins/airgate-playground/assets
EPAY_ASSETS := $(BACKEND_DIR)/data/plugins/payment-epay/assets
HEALTH_ASSETS := $(BACKEND_DIR)/data/plugins/airgate-health/assets
BINARY := $(BACKEND_DIR)/server
WEBDIST := $(BACKEND_DIR)/internal/web/webdist
GO := GOTOOLCHAIN=local go

# 版本号：默认从 git 派生（dirty 检测），release workflow 通过 -ldflags 注入。
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/DouDOU-start/airgate-core/internal/version.Version=$(VERSION)

.PHONY: help dev dev-backend dev-frontend dev-sdk dev-plugins dev-plugin-openai dev-plugin-claude dev-plugin-playground dev-plugin-epay dev-plugin-health \
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

dev-plugins: ## 启动所有插件前端 watch 模式
	@echo "启动插件前端 watch（统一输出到 <plugin>/web/dist，core 在 dev 模式下直读）："
	@echo "  - openai      → ../airgate-openai/web/dist/"
	@echo "  - claude      → ../airgate-claude/web/dist/"
	@echo "  - playground  → ../airgate-playground/web/dist/"
	@echo "  - epay        → ../airgate-epay/web/dist/"
	@echo "  - health      → ../airgate-health/web/dist/  （含 admin index.js + standalone status page）"
	@$(MAKE) dev-plugin-openai &
	@$(MAKE) dev-plugin-claude &
	@$(MAKE) dev-plugin-playground &
	@$(MAKE) dev-plugin-epay &
	@$(MAKE) dev-plugin-health &
	@wait

dev-plugin-openai: ## 单独 watch openai 插件前端（输出到 ../airgate-openai/web/dist）
	@if [ -d $(OPENAI_PLUGIN) ]; then \
		cd $(OPENAI_PLUGIN) && npx vite build --watch; \
	else \
		echo "跳过 openai 插件前端 watch：$(OPENAI_PLUGIN) 不存在"; \
	fi

dev-plugin-claude: ## 单独 watch claude 插件前端（输出到 ../airgate-claude/web/dist）
	@if [ -d $(CLAUDE_PLUGIN) ]; then \
		cd $(CLAUDE_PLUGIN) && npx vite build --watch; \
	else \
		echo "跳过 claude 插件前端 watch：$(CLAUDE_PLUGIN) 不存在"; \
	fi

dev-plugin-playground: ## 单独 watch playground 插件前端（输出到 ../airgate-playground/web/dist）
	@if [ -d $(PLAYGROUND_PLUGIN) ]; then \
		cd $(PLAYGROUND_PLUGIN) && npx vite build --watch; \
	else \
		echo "跳过 playground 插件前端 watch：$(PLAYGROUND_PLUGIN) 不存在"; \
	fi

dev-plugin-epay: ## 单独 watch epay 插件前端（输出到 ../airgate-epay/web/dist）
	@if [ -d $(EPAY_PLUGIN) ]; then \
		cd $(EPAY_PLUGIN) && npx vite build --watch; \
	else \
		echo "跳过 epay 插件前端 watch：$(EPAY_PLUGIN) 不存在"; \
	fi

dev-plugin-health: ## 单独 watch health 插件前端（同时 watch admin index.js + status standalone）
	@if [ -d $(HEALTH_PLUGIN) ]; then \
		cd $(HEALTH_PLUGIN) && npm run dev; \
	else \
		echo "跳过 health 插件前端 watch：$(HEALTH_PLUGIN) 不存在"; \
	fi

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

sync-plugins: ## 构建插件前端并同步 admin 资源到 data/plugins/
	@if [ -d $(OPENAI_PLUGIN) ]; then \
		echo "构建并同步 openai 插件前端..."; \
		(cd $(OPENAI_PLUGIN) && npm run build); \
		mkdir -p $(OPENAI_ASSETS); \
		cp $(OPENAI_PLUGIN)/dist/index.js $(OPENAI_ASSETS)/index.js; \
		echo "openai 插件前端已同步到 $(OPENAI_ASSETS)/"; \
	else \
		echo "跳过 openai 插件前端构建：$(OPENAI_PLUGIN) 不存在"; \
	fi
	@if [ -d $(CLAUDE_PLUGIN) ]; then \
		echo "构建并同步 claude 插件前端..."; \
		(cd $(CLAUDE_PLUGIN) && npm run build); \
		mkdir -p $(CLAUDE_ASSETS); \
		cp $(CLAUDE_PLUGIN)/dist/index.js $(CLAUDE_ASSETS)/index.js; \
		echo "claude 插件前端已同步到 $(CLAUDE_ASSETS)/"; \
	else \
		echo "跳过 claude 插件前端构建：$(CLAUDE_PLUGIN) 不存在"; \
	fi
	@if [ -d $(PLAYGROUND_PLUGIN) ]; then \
		echo "构建并同步 playground 插件前端..."; \
		(cd $(PLAYGROUND_PLUGIN) && npm run build); \
		mkdir -p $(PLAYGROUND_ASSETS); \
		cp $(PLAYGROUND_PLUGIN)/dist/index.js $(PLAYGROUND_ASSETS)/index.js; \
		echo "playground 插件前端已同步到 $(PLAYGROUND_ASSETS)/"; \
	else \
		echo "跳过 playground 插件前端构建：$(PLAYGROUND_PLUGIN) 不存在"; \
	fi
	@if [ -d $(EPAY_PLUGIN) ]; then \
		echo "构建并同步 epay 插件前端..."; \
		(cd $(EPAY_PLUGIN) && npm run build); \
		mkdir -p $(EPAY_ASSETS); \
		cp $(EPAY_PLUGIN)/dist/index.js $(EPAY_ASSETS)/index.js; \
		echo "epay 插件前端已同步到 $(EPAY_ASSETS)/"; \
	else \
		echo "跳过 epay 插件前端构建：$(EPAY_PLUGIN) 不存在"; \
	fi
	@if [ -d $(HEALTH_PLUGIN) ]; then \
		echo "构建并同步 health 插件前端..."; \
		(cd $(HEALTH_PLUGIN) && npm run build); \
		mkdir -p $(HEALTH_ASSETS); \
		cp $(HEALTH_PLUGIN)/dist/index.js $(HEALTH_ASSETS)/index.js; \
		echo "health 插件 admin 前端已同步到 $(HEALTH_ASSETS)/"; \
	else \
		echo "跳过 health 插件前端构建：$(HEALTH_PLUGIN) 不存在"; \
	fi

# ===================== 代码生成 =====================

ent: ## 生成 Ent ORM 代码
	@cd $(BACKEND_DIR) && GOWORK=off $(GO) generate ./ent
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

pre-commit: lint vet verify-ent build-backend ## pre-commit hook 调用（跳过耗时的测试）

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

install: setup-hooks ## 安装全部依赖（含 SDK 前端构建、插件前端依赖、首次 webdist 构建）
	@cd $(SDK_FRONTEND) && npm install && npm run build && echo "SDK 前端构建完成"
	@cd $(BACKEND_DIR) && $(GO) mod download
	@rm -rf $(WEB_DIR)/node_modules/.vite
	@cd $(WEB_DIR) && npm install
	@for p in $(OPENAI_PLUGIN) $(CLAUDE_PLUGIN) $(EPAY_PLUGIN) $(HEALTH_PLUGIN); do \
		if [ -d $$p ]; then \
			echo "安装插件前端依赖: $$p"; \
			cd $$p && npm install && cd - > /dev/null; \
		fi; \
	done
	@command -v air > /dev/null 2>&1 || (echo "安装 air（热重载工具）..."; $(GO) install github.com/air-verse/air@latest)
	@$(MAKE) build-frontend ensure-webdist
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
