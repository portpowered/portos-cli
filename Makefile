# CLI Makefile. The root Makefile delegates here with the root vocabulary: build, generate, test.
.PHONY: default build generate test
.SILENT:
# Export PATH to include Go bin directory for wire and other tools
export PATH := $(PATH):$(HOME)/go/bin

CLI_QUIET_COMMAND ?= go run ./cmd/cli-quiet-command

# Version is derived from git tags; falls back to "dev" if git is unavailable
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

# Detect OS for cross-platform support
# Primary check: OS variable set by Make on Windows
IS_WINDOWS :=
ifeq ($(OS),Windows_NT)
	IS_WINDOWS := 1
endif

default: build

build:
ifdef IS_WINDOWS
	$(CLI_QUIET_COMMAND) --label cli:build --success "CLI binary built." -- go build -ldflags "$(LDFLAGS)" -o port.exe cmd/port/main.go
else
	$(CLI_QUIET_COMMAND) --label cli:build --success "CLI binary built." -- go build -ldflags "$(LDFLAGS)" -o port cmd/port/main.go
endif

# API models, command docs and Wire injectors.
generate:
	$(CLI_QUIET_COMMAND) --label cli:interfaces --success "CLI API models generated." -- go generate -tags=interfaces ./internal/api/...
	$(CLI_QUIET_COMMAND) --label cli:docs --success "CLI command docs generated." --cwd ../api/scripts/cli-generator -- go run ./cmd/generate-cli-docs
	$(CLI_QUIET_COMMAND) --label cli:wire-generate --success "CLI Wire injectors generated." -- go generate -tags=wireinject ./internal/...
	$(CLI_QUIET_COMMAND) --label cli:wire --success "CLI Wire graph generated." -- wire ./internal/...

test:
	$(CLI_QUIET_COMMAND) --label cli:test --success "CLI tests passed." -- go test ./...
