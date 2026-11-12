SHELL := /bin/sh

GO ?= go
BUN ?= bun
UI := web/ui

.PHONY: help web build dev-portal dev-admin web-dev-portal web-dev-admin check

help:
	@echo "web             bun 安装依赖并构建两个前端（web/ui -> web/portal/dist、web/admin/dist，Go 二进制会 embed）"
	@echo "build           make web 后编译 bin/portal、bin/admin"
	@echo "dev-portal      运行门户后端（:8081，需先 make web 生成 dist）"
	@echo "dev-admin       运行管理后台后端（:8082，需先 make web 生成 dist）"
	@echo "web-dev-portal  门户前端 rsbuild dev（:5181，/api 代理到 :8081）"
	@echo "web-dev-admin   管理后台前端 rsbuild dev（:5182，/api 代理到 :8082）"
	@echo "check           静态检查：gofmt、go vet、前端 typecheck（不含测试）"
	@echo "测试仅开发机手工执行：go test ./internal/..."

web:
	$(BUN) install --cwd $(UI) --frozen-lockfile
	cd $(UI) && $(BUN) run build:portal && $(BUN) run build:admin

build: web
	$(GO) build -o bin/portal ./cmd/portal
	$(GO) build -o bin/admin ./cmd/admin

dev-portal:
	$(GO) run ./cmd/portal

dev-admin:
	$(GO) run ./cmd/admin

web-dev-portal:
	cd $(UI) && $(BUN) run dev:portal

web-dev-admin:
	cd $(UI) && $(BUN) run dev:admin

check:
	@out="$$(gofmt -l cmd internal web/portal/embed.go web/admin/embed.go)"; \
		if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	$(GO) vet ./...
	cd $(UI) && $(BUN) run typecheck
