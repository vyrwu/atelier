BIN := bin/atelier
DEV ?= $(HOME)/.atelier-dev

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
	 PATH="$(PWD)/bin:$$PATH" \
	 ./$(BIN)

dev-clean:
	-tmux -L atelier-dev kill-server 2>/dev/null || true
	rm -rf $(DEV)
	@echo "dev instance removed ($(DEV) + atelier-dev socket)"

.PHONY: build install run dev dev-clean
