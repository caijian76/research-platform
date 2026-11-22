SHELL := /bin/bash
.DEFAULT_GOAL := help

ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
DIST := $(ROOT)/dist

GO ?= go
NPM ?= npm
GOPROXY ?= https://goproxy.cn,direct
GOSUMDB ?= off
TMPDIR ?= /var/tmp
export GOPROXY GOSUMDB TMPDIR

.PHONY: help frontend backend app dev build deploy release fmt vet clean

help: ## 显示可用命令
	@echo "科研云平台常用命令："
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z0-9_-]+:.*?## / {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

frontend: ## 构建前端到 backend/web
	cd $(ROOT)/frontend && $(NPM) ci --no-audit --no-fund && $(NPM) run build

backend: ## 编译后端静态二进制到 dist/
	@mkdir -p $(DIST)
	cd $(ROOT)/backend && CGO_ENABLED=0 GOOS=linux $(GO) build -trimpath -ldflags "-s -w" -o $(DIST)/research-platform-server ./cmd/server

app: frontend backend ## 仅构建前后端（不构建镜像）

dev: ## 启动前端开发服务器（Vite，/api 代理到 127.0.0.1:8080）
	cd $(ROOT)/frontend && $(NPM) run dev

build: ## 构建前后端、镜像并导入 containerd
	$(ROOT)/scripts/build.sh

deploy: ## 应用清单并等待 rollout
	$(ROOT)/scripts/deploy.sh

release: build deploy ## 构建并部署

fmt: ## 格式化 Go 代码
	cd $(ROOT)/backend && gofmt -w ./cmd ./internal

vet: ## Go 静态检查
	cd $(ROOT)/backend && $(GO) vet ./...

clean: ## 清理构建产物（dist、backend/web）
	rm -rf $(DIST) $(ROOT)/backend/web