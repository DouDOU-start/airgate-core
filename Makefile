# AirGate Core Makefile

# 变量
BACKEND_DIR := backend
WEB_DIR := web
BINARY := $(BACKEND_DIR)/server
GO := GOTOOLCHAIN=local go

.PHONY: help dev dev-backend dev-frontend build build-backend build-frontend \
        ent lint fmt test clean install docs-check

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

lint: ## 代码检查（后端 golangci-lint）
	@cd $(BACKEND_DIR) && \
	if command -v golangci-lint > /dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "未安装 golangci-lint，回退到 go vet"; \
		echo "安装: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
		$(GO) vet ./...; \
	fi
	@echo "后端代码检查通过"

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

# ===================== 文档检查 =====================

docs-check: ## 检查文档中引用的文件是否存在
	@echo "检查文档引用..."
	@errors=0; \
	for doc in docs/*.md; do \
		for ref in $$(grep -oP '`([a-zA-Z0-9_/-]+\.md)`' "$$doc" | tr -d '`'); do \
			if [ ! -f "docs/$$ref" ] && [ ! -f "$$ref" ]; then \
				echo "  ✗ $$doc 引用了不存在的文件: $$ref"; \
				errors=$$((errors + 1)); \
			fi; \
		done; \
	done; \
	if [ $$errors -eq 0 ]; then echo "文档引用检查通过"; else echo "发现 $$errors 个破损引用"; exit 1; fi

# ===================== 依赖安装 =====================

install: ## 安装前后端依赖
	@cd $(BACKEND_DIR) && $(GO) mod download
	@cd $(WEB_DIR) && npm install
	@echo "依赖安装完成"

# ===================== 清理 =====================

clean: ## 清理构建产物
	@rm -f $(BINARY)
	@rm -rf $(WEB_DIR)/dist
	@echo "清理完成"
