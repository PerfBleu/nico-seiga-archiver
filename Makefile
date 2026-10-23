# nico-seiga-archiver
#
#   make            编译当前平台（默认构建）
#   make linux      Linux ELF（amd64）
#   make windows    Windows EXE（amd64）
#   make all        ELF + EXE 一起编译到 dist/
#
# 常用变量（可覆盖）：
#   make linux GOARCH=arm64
#   make windows GOARCH=386
#   make VERSION=1.2.3        # 手动指定版本（默认自动取 git commit）
#   make OUTDIR=build

BINARY ?= archiver
OUTDIR ?= dist
GO     ?= go

# 版本号默认自动注入为 git commit（短哈希），工作区有改动时追加 -dirty。
# 不在 git 仓库里（如源码压缩包）时退化为 dev。
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null)
GIT_DIRTY  := $(shell git status --porcelain 2>/dev/null | head -n 1)
ifeq ($(strip $(GIT_COMMIT)),)
VERSION ?= dev
else
ifeq ($(strip $(GIT_DIRTY)),)
VERSION ?= $(GIT_COMMIT)
else
VERSION ?= $(GIT_COMMIT)-dirty
endif
endif

# 目标平台（跨平台编译时可覆盖）
GOOS_LINUX   ?= linux
GOOS_WINDOWS ?= windows
GOARCH       ?= amd64

# 纯 Go 项目，关掉 cgo 才能真正做到跨平台交叉编译
export CGO_ENABLED ?= 0

# 通过 -X 注入版本号（commit 号）
APP        := nico-seiga-archiver
LDFLAGS    := -s -w -X '$(APP)/internal/cli.version=$(VERSION)'

# 输出文件名：Windows 需要 .exe 后缀
LINUX_BIN   := $(OUTDIR)/$(BINARY)-$(GOOS_LINUX)-$(GOARCH)
WINDOWS_BIN := $(OUTDIR)/$(BINARY)-$(GOOS_WINDOWS)-$(GOARCH).exe

HOST_OS   := $(shell $(GO) env GOOS)
HOST_ARCH := $(shell $(GO) env GOARCH)

ifeq ($(HOST_OS),windows)
HOST_BIN := $(OUTDIR)/$(BINARY)-$(HOST_OS)-$(HOST_ARCH).exe
else
HOST_BIN := $(OUTDIR)/$(BINARY)-$(HOST_OS)-$(HOST_ARCH)
endif

.DEFAULT_GOAL := build

.PHONY: help build native linux windows elf exe all run test vet fmt tidy clean check deps version

help: ## 显示帮助
	@echo "用法: make [目标]"
	@echo
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'
	@echo
	@echo "变量: BINARY=$(BINARY) OUTDIR=$(OUTDIR) GOARCH=$(GOARCH) VERSION=$(VERSION)"

version: ## 显示将要注入的版本号（git commit）
	@echo "$(VERSION)"

build: native ## 编译当前平台（默认）

native: ## 编译当前平台的可执行文件
	@mkdir -p $(OUTDIR)
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(HOST_BIN) .
	@echo "已生成: $(HOST_BIN)"

linux: elf ## 编译 Linux ELF

elf: ## 编译 Linux ELF（默认 amd64）
	@mkdir -p $(OUTDIR)
	GOOS=$(GOOS_LINUX) GOARCH=$(GOARCH) CGO_ENABLED=0 \
		$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(LINUX_BIN) .
	@echo "已生成: $(LINUX_BIN)"

windows: exe ## 编译 Windows EXE

exe: ## 编译 Windows EXE（默认 amd64）
	@mkdir -p $(OUTDIR)
	GOOS=$(GOOS_WINDOWS) GOARCH=$(GOARCH) CGO_ENABLED=0 \
		$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(WINDOWS_BIN) .
	@echo "已生成: $(WINDOWS_BIN)"

all: elf exe ## 同时编译 ELF 与 EXE
	@echo
	@ls -lh $(OUTDIR)

run: native ## 直接运行（不经过 archiver> 提示符时传入参数，如 make run ARGS=probe）
	$(HOST_BIN) $(ARGS)

test: ## 运行单元测试
	$(GO) test ./...

check: fmt vet test ## 格式化 + 静态检查 + 测试

vet: ## go vet
	$(GO) vet ./...

fmt: ## gofmt 全部源码
	$(GO) fmt ./...

tidy: ## 整理依赖
	$(GO) mod tidy

deps: ## 下载依赖
	$(GO) mod download

clean: ## 删除构建产物
	rm -rf $(OUTDIR)
	rm -f $(BINARY) $(BINARY).exe
	@echo "已清理"
