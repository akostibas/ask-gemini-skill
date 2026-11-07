VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BIN_DIR   ?= $(HOME)/bin
SKILL_DIR ?= $(HOME)/.claude/skills/ask-gemini

LDFLAGS := -X main.version=$(VERSION)

.PHONY: build install test clean

build:
	go build -ldflags "$(LDFLAGS)" -o ask-gemini ./cmd/ask-gemini

# Builds first, so a failed build leaves the old install. The binary is renamed
# into place: replaces an old symlink, and a running copy can't block it.
install: build
	mkdir -p "$(BIN_DIR)"
	cp ask-gemini "$(BIN_DIR)/.ask-gemini.new"
	mv -f "$(BIN_DIR)/.ask-gemini.new" "$(BIN_DIR)/ask-gemini"
	@echo "Installed ask-gemini -> $(BIN_DIR)/ask-gemini"
	rm -rf "$(SKILL_DIR)"
	mkdir -p "$(SKILL_DIR)"
	cp -R skill/. "$(SKILL_DIR)/"
	chmod +x "$(SKILL_DIR)"/*.sh
	@echo "Installed skill   -> $(SKILL_DIR)"

test:
	go test ./...

clean:
	rm -f ask-gemini
