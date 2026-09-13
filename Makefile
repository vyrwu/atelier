BIN := bin/atelier
DEV ?= $(HOME)/.atelier-dev

# Git also reads $XDG_CONFIG_HOME/git/config, which the dev XDG redirect below
# hides — so a workspace agent's commits would fall back to git's unattributed
# OS-default identity ("<Full Name> <user@host.local>"). Pin git's global config
# to wherever the real identity lives so agents commit as you.
GIT_GLOBAL := $(shell git config --show-origin --get user.email 2>/dev/null | sed -E 's/^file:([^[:space:]]+).*/\1/')

build:
	go build -o $(BIN) ./cmd/atelier

install: build
	@mkdir -p $(HOME)/.local/bin
	@ln -sf $(PWD)/$(BIN) $(HOME)/.local/bin/atelier
	@echo "linked $(HOME)/.local/bin/atelier -> $(PWD)/$(BIN)"

run: build
	./$(BIN)

# dev runs the freshly-built binary in a fully isolated instance: its own tmux
# socket, its own state/config/cache, its own workspace root, and the dev binary
# first on PATH. It shares only $HOME/.claude (Claude auth + the guarded hooks,
# which route to this binary via the dev PATH/env). Nothing here touches your
# real atelier server, state, config, or installed binary. Reset with `dev-clean`.
dev: build
	@ATELIER_DEV=1 \
	 ATELIER_SOCKET=atelier-dev \
	 ATELIER_ROOT=$(DEV)/ateliers \
	 XDG_CONFIG_HOME=$(DEV)/config \
	 XDG_STATE_HOME=$(DEV)/state \
	 XDG_CACHE_HOME=$(DEV)/cache \
	 GIT_CONFIG_GLOBAL=$(GIT_GLOBAL) \
	 PATH="$(PWD)/bin:$$PATH" \
	 ./$(BIN)

dev-clean:
	-tmux -L atelier-dev kill-server 2>/dev/null || true
	rm -rf $(DEV)
	@echo "dev instance removed ($(DEV) + atelier-dev socket)"

.PHONY: build install run dev dev-clean
