# autobot 的构建入口。
#
# 日常三条命令：
#   make build   编译出 bin/autobot（不带参数时 make 就是它）
#   make check   离线门禁：gofmt + go vet + 单测（提交前跑）
#   make e2e     实盘用例（需要服务端在跑，自动 source .env.local）
# 全部目标见 `make help`。
#
# 只用到 GNU Make 3.81 就有的写法（macOS 自带 3.81）：没有 !=、没有 .ONESHELL、
# 没有 $(file ...)，因此 macOS 与 Linux 上行为一致。
#
# 可覆盖的变量（例：make build VERSION=v1.0.0）：
#   BIN / BIN_DIR / DIST / PLATFORMS / VERSION / LDFLAGS / ARGS / E2E_*

GO       ?= go
CMD      ?= ./cmd/autobot
BIN_NAME ?= autobot
BIN_DIR  ?= bin
BIN      ?= $(BIN_DIR)/$(BIN_NAME)
DIST     ?= dist

# 版本戳：有 tag 时是 tag，否则是 commit 短 sha，不在 git 仓库里则用默认值。它经
# -X 注入 cmd/autobot/main.go 的 version（那里的 var 就是为这个留的）。
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null)
ifeq ($(strip $(VERSION)),)
VERSION := 0.1.0-dev
endif

# -s -w 去掉符号表与 DWARF（二进制小一半），-trimpath 去掉本机绝对路径。
LDFLAGS ?= -s -w -X main.version=$(VERSION)

# 交叉编译目标：服务端是 linux/amd64，本机是 darwin/arm64。
PLATFORMS ?= linux/amd64 darwin/arm64

# 实盘用例：默认从 .env.local 读环境变量（不存在就用现有环境）。
E2E_ENV      ?= .env.local
E2E_PKGS     ?= ./test/e2e/...
E2E_TIMEOUT  ?= 60m
E2E_PARALLEL ?= 1
E2E_RUN      ?=

.DEFAULT_GOAL := build

.PHONY: help build install run version test test-race cover vet build-all fmt fmt-check lint check e2e release release-linux clean

help: ## 显示所有目标（含每个目标的说明）
	@echo "autobot 构建目标（变量见 Makefile 顶部，例：make build VERSION=v1.0.0）"
	@echo
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk -F ':.*## ' '{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

build: ## 编译 CLI 到 $(BIN)（只编主包，依赖走构建缓存，增量很快）
	@mkdir -p $(BIN_DIR)
	@$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) $(CMD)
	@printf '  -> %s  version=%s  size=%s\n' "$(BIN)" "$(VERSION)" "$$(du -h $(BIN) | cut -f1)"

install: ## 编译并安装到 $GOBIN（未设置则 $GOPATH/bin），PATH 里可直接敲 autobot
	@$(GO) install -trimpath -ldflags "$(LDFLAGS)" $(CMD)
	@d="$$($(GO) env GOBIN)"; if [ -z "$$d" ]; then d="$$($(GO) env GOPATH)/bin"; fi; \
		echo "  -> $$d/$(BIN_NAME)"

run: ## 不产出二进制直接跑（例：make run ARGS="probe --account autobotprobe"）
	@$(GO) run $(CMD) $(ARGS)

version: ## 打印将要注入的版本戳与 ldflags
	@echo "version = $(VERSION)"
	@echo "ldflags = $(LDFLAGS)"

test: ## 离线单测（实盘用例有 build tag，自动跳过）
	@$(GO) test ./... -count=1

test-race: ## 离线单测 + 竞态检测（比 test 慢，提交前跑一次）
	@$(GO) test ./... -count=1 -race

cover: ## 各包覆盖率一览
	@$(GO) test ./... -count=1 -cover

vet: ## go vet（含实盘用例的 e2e tag）
	@$(GO) vet ./...
	@$(GO) vet -tags=e2e ./...
	@echo "vet: clean"

build-all: ## 编译所有包（= go build ./...，DoD 门禁的一部分，不产出二进制）
	@$(GO) build ./...
	@echo "build: ok"

fmt: ## 就地格式化（gofmt -w）
	@gofmt -w .

fmt-check: ## 只检查 gofmt 是否干净（CI 门禁，不修改文件）
	@out="$$(gofmt -l .)"; \
	if [ -n "$$out" ]; then printf 'gofmt 报告以下文件需要格式化:\n%s\n' "$$out"; exit 1; fi
	@echo "gofmt: clean"

lint: ## staticcheck（已安装才跑，未安装只提示）
	@if command -v staticcheck >/dev/null 2>&1; then \
		staticcheck ./...; \
	else \
		echo "staticcheck 未安装，跳过：go install honnef.co/go/tools/cmd/staticcheck@latest"; \
	fi

check: fmt-check vet build-all test ## 离线门禁：gofmt + go vet + go build ./... + 单测

e2e: ## 实盘用例（需要服务端；自动加载 $(E2E_ENV)，可加 E2E_RUN=TestFleetLifecycle）
	@set -a; if [ -f "$(E2E_ENV)" ]; then . "./$(E2E_ENV)"; fi; set +a; \
	$(GO) test -tags=e2e $(E2E_PKGS) -count=1 -v -timeout $(E2E_TIMEOUT) \
		-parallel $(E2E_PARALLEL) -p $(E2E_PARALLEL) $(if $(E2E_RUN),-run $(E2E_RUN),)

release: ## 交叉编译静态二进制到 $(DIST)/（CGO_ENABLED=0，可直接拷到服务端）
	@mkdir -p $(DIST)
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		out=$(DIST)/$(BIN_NAME)-$$os-$$arch; \
		if [ "$$os" = "windows" ]; then out=$$out.exe; fi; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o "$$out" $(CMD) || exit 1; \
		printf '  -> %-30s %s\n' "$$out" "$$(du -h $$out | cut -f1)"; \
	done

release-linux: ## 只交叉编译 linux/amd64（静态单文件，scp 到服务端即可跑）
	@$(MAKE) release PLATFORMS=linux/amd64

clean: ## 删除 $(BIN_DIR)/ 与 $(DIST)/
	@rm -rf $(BIN_DIR) $(DIST)
	@echo "已删除 $(BIN_DIR)/ $(DIST)/"
