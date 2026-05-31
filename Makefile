VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOBIN   ?= $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN := $(shell go env GOPATH)/bin
endif
SKILL_DIR ?= $(HOME)/.claude/skills/ask-gemini

LDFLAGS := -X main.version=$(VERSION)

.PHONY: build install test clean

build:
	go build -ldflags "$(LDFLAGS)" -o ask-gemini ./cmd/ask-gemini

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/ask-gemini
	@echo "Installed ask-gemini -> $(GOBIN)/ask-gemini"
	rm -rf "$(SKILL_DIR)"
	mkdir -p "$(SKILL_DIR)"
	cp -R skill/. "$(SKILL_DIR)/"
	@echo "Installed skill   -> $(SKILL_DIR)"

test:
	go test ./...

clean:
	rm -f ask-gemini
