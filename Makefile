.PHONY: build test test-e2e fmt lint clean help tidy install uninstall init-snapshot list-binaries release-check release-snapshot test-tmux test-tmux-clean test-plugin test-plugin-config

BIN_DIR    := bin
PREFIX     ?= $(HOME)/.local
INSTALL_DIR := $(PREFIX)/bin

# Auto-discover every cmd/* directory.
CMDS       := $(notdir $(wildcard cmd/*))
BINARIES   := $(addprefix $(BIN_DIR)/,$(CMDS))

# Inject the version into the `atelier` binary so `atelier version`
# reports the actual build identity instead of the placeholder "dev".
# Resolves to a clean tag when on one (e.g. `v0.1.0`), a `-dirty`
# suffix when the working tree has uncommitted changes, and a short
# SHA otherwise. Goreleaser releases override this with the tag.
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS    := -s -w -X main.version=$(VERSION)

help:
	@echo "atelier — Makefile targets"
	@echo ""
	@echo "  build           build all binaries into $(BIN_DIR)/"
	@echo "  list-binaries   print the names of binaries that would be built"
	@echo "  test            run unit tests (no tmux required)"
	@echo "  test-e2e        run e2e tests (spins up isolated tmux servers)"
	@echo "  install         copy every binary to $(INSTALL_DIR)"
	@echo "  uninstall       remove installed binaries"
	@echo "  init-snapshot   write 'atelier init' output to ./atelier.tmux"
	@echo "  release-check   validate .goreleaser.yaml"
	@echo "  release-snapshot dry-run goreleaser locally (writes to ./dist)"
	@echo "  test-tmux       rebuild + launch the bundled atelier runtime (default mode)"
	@echo "  test-tmux-clean test-tmux + wipe persistence cache (fresh-slate dev iteration)"
	@echo "  test-plugin     rebuild + launch atelier in PLUGIN mode (sources your own tmux.conf)"
	@echo "  test-plugin-config  (re)generate the plugin-mode test tmux.conf"
	@echo "  fmt             format code"
	@echo "  lint            run golangci-lint"
	@echo "  tidy            go mod tidy"
	@echo "  clean           remove build artifacts"
	@echo ""
	@echo "Built binaries:"
	@for b in $(CMDS); do echo "  $$b"; done
	@echo ""
	@echo "Reproducible dev/test environment: nix develop"

build: $(BINARIES)

list-binaries:
	@for b in $(CMDS); do echo "$$b"; done

# Depend on every .go file in the module so edits in internal/... force
# a rebuild. Without this, the rule only watched cmd/<tool>/ and stale
# binaries shipped silently after internal/tools/<tool>/ changes.
GO_SOURCES := $(shell find cmd internal -type f -name '*.go' 2>/dev/null)

$(BIN_DIR)/%: cmd/% $(GO_SOURCES) go.mod go.sum
	@mkdir -p $(BIN_DIR)
	go build -ldflags '$(LDFLAGS)' -o $@ ./cmd/$*

test:
	go test ./...

test-e2e: build
	@# Put $(BIN_DIR) on PATH so plugin discovery finds the freshly-built tools.
	PATH="$(PWD)/$(BIN_DIR):$$PATH" go test -tags=e2e ./...

fmt:
	go fmt ./...

lint:
	golangci-lint run

tidy:
	go mod tidy

install: build
	@mkdir -p $(INSTALL_DIR)
	@# Use `install` instead of `cp`: it rm-then-writes via a temp file,
	@# preserving inode atomicity. On macOS, overwriting an in-place
	@# binary while it may still be memory-mapped triggers a
	@# signature-mismatch SIGKILL on the next run. `install` avoids that
	@# by giving the new file a fresh inode.
	@#
	@# Followed by ad-hoc codesign on macOS so the binary carries a
	@# signature the kernel can verify. `codesign -` is ad-hoc (no
	@# identity), accepted by Gatekeeper for locally-built tools.
	@for b in $(CMDS); do \
		install -m 0755 $(BIN_DIR)/$$b $(INSTALL_DIR)/$$b; \
		if [ "$$(uname -s)" = "Darwin" ]; then \
			codesign --force --sign - $(INSTALL_DIR)/$$b 2>/dev/null || true; \
		fi; \
		echo "  installed: $(INSTALL_DIR)/$$b"; \
	done
	@echo ""
	@echo "Next steps:"
	@echo "  1. Ensure $(INSTALL_DIR) is on your PATH"
	@echo "  2. Verify: atelier doctor"
	@echo "  3. Launch the bundled runtime: atelier"
	@echo "     (or for embedding into your existing tmux:"
	@echo "      run-shell 'atelier init --bare | tmux source-file -')"

uninstall:
	@for b in $(CMDS); do \
		rm -f $(INSTALL_DIR)/$$b; \
		echo "  removed: $(INSTALL_DIR)/$$b"; \
	done

init-snapshot: build
	@./$(BIN_DIR)/atelier init > atelier.tmux
	@echo "Wrote atelier.tmux ($$(wc -l < atelier.tmux) lines)"

clean:
	rm -rf $(BIN_DIR)/ atelier.tmux dist/

release-check:
	goreleaser check

release-snapshot:
	goreleaser release --snapshot --clean --skip=publish

# ============================================================================
# Development launchers
# ============================================================================
#
# Atelier ships TWO ways to run:
#
#   1. Bundled runtime (`atelier` with no subcommand). The canonical user
#      entry point: spawns a dedicated tmux server on its own socket with
#      the curated bundled config. Everything atelier owns — bindings,
#      hooks, statusline, theme, persistence — is wired automatically.
#      This is what most users will run.
#
#   2. Plugin embed (`atelier init --bare`). For users who already have a
#      complex tmux config and want atelier's BEHAVIOR (bindings, hooks,
#      statusline emitters) without atelier owning the visual layer.
#
# The Makefile gives you fast iteration loops for both:
#
#   make test-tmux         rebuild + reinstall + launch bundled runtime
#   make test-tmux-clean   ... PLUS wipe persistence cache (fresh slate)
#   make test-plugin       rebuild + reinstall + launch plugin-mode (sources
#                          your own ~/.config/tmux/tmux.conf and layers
#                          atelier init on top)
#   make test-plugin-config (re)generate the plugin-mode tmux.conf used by
#                          test-plugin
#
# Both runners use the SAME `-L atelier` socket so they don't collide with
# your daily-driver tmux server.

# test-tmux launches the BUNDLED atelier runtime — the default user
# entry point. Rebuilds every binary, reinstalls them to $(INSTALL_DIR),
# clears the debug log, then invokes `atelier` (no subcommand). atelier
# itself handles socket selection, config writing, recovery from a
# wedged prior server, and attach.
#
# This is the iteration loop you want during development of the bundled
# experience.
test-tmux:
	@echo "→ killing prior atelier tmux server (-L atelier)"
	@# Force fresh launch. Without this, `atelier` would attach to the
	@# previous server (still alive) — meaning live in-memory state
	@# survives and `make test-tmux-clean` (which wipes only the cache
	@# file) appears to leave workspaces "uncleaned" because the
	@# server retained them. Killing first guarantees the launch is
	@# a true cold start: cache → restore → new tmux state.
	@tmux -L atelier kill-server 2>/dev/null || true
	@echo "→ clean rebuild + reinstall"
	@rm -rf $(BIN_DIR)
	@$(MAKE) --no-print-directory install >/dev/null
	@echo "→ clearing debug log"
	@rm -f $${XDG_CACHE_HOME:-$$HOME/.cache}/atelier/debug.log
	@command -v atelier >/dev/null 2>&1 || { echo "atelier not on PATH after install."; exit 1; }
	@echo "→ launching bundled atelier runtime"
	@# `atelier` (no subcommand) writes its own config to
	@# $(XDG_CACHE_HOME)/atelier/tmux.conf, probes/recovers any wedged
	@# prior server on the -L atelier socket, and attaches.
	atelier

# test-tmux-clean: `test-tmux` PLUS wiping persistence cache. Use when
# iterating on the persistence layer itself or for a truly empty-slate
# launch. NORMAL development should prefer `test-tmux` — workspaces
# persist across restarts (FR-5.2), which is the whole point.
test-tmux-clean:
	@echo "→ wiping persistence cache"
	@rm -f $${XDG_CACHE_HOME:-$$HOME/.cache}/atelier/state-*.json
	@$(MAKE) --no-print-directory test-tmux

# ----------------------------------------------------------------------------
# Plugin-mode test loop (secondary path)
# ----------------------------------------------------------------------------

TEST_PLUGIN_CONF := $(HOME)/.config/atelier/test.tmux.conf
TEST_PLUGIN_SOCKET := atelier

# test-plugin-config writes a tmux config that sources YOUR
# ~/.config/tmux/tmux.conf (theme, statusline, TPM plugins, etc.) and
# then layers atelier's engine wiring (--bare) on top. Use this when
# iterating on the plugin-embed path so you can verify atelier's
# behavior on top of a real-world host tmux configuration.
test-plugin-config:
	@mkdir -p $(dir $(TEST_PLUGIN_CONF))
	@printf '%s\n' \
	  '# Generated by `make test-plugin-config` — plugin-mode test config.' \
	  '# Sources your real tmux.conf so we test atelier embedded in a real setup.' \
	  '# Run via `make test-plugin`.' \
	  '' \
	  '# Source your main tmux.conf for cosmetics (theme, statusline, TPM plugins).' \
	  'if-shell "[ -f \"$$XDG_CONFIG_HOME/tmux/tmux.conf\" ]" {' \
	  '  source-file -F "$$XDG_CONFIG_HOME/tmux/tmux.conf"' \
	  '}' \
	  'if-shell "[ -f \"$$HOME/.config/tmux/tmux.conf\" ] && [ -z \"$$XDG_CONFIG_HOME\" ]" {' \
	  '  source-file "$$HOME/.config/tmux/tmux.conf"' \
	  '}' \
	  '' \
	  '# Reload this test config' \
	  'bind r source-file $(TEST_PLUGIN_CONF) \; display-message "atelier plugin test config reloaded"' \
	  '' \
	  '# Atelier in --bare mode: bindings + hooks + statusline injection,' \
	  '# no atelier-owned theme. Your host tmux.conf above keeps full' \
	  '# control of the visual layer.' \
	  "run-shell 'atelier init --bare | tmux source-file -'" \
	  > $(TEST_PLUGIN_CONF)
	@echo "Wrote: $(TEST_PLUGIN_CONF)"
	@echo ""
	@echo "Next: make test-plugin"

# test-plugin launches a fresh isolated tmux server with the plugin-mode
# config (your tmux.conf + atelier init --bare). Lets you verify the
# plugin-embed path against a realistic host config.
test-plugin:
	@if [ ! -f $(TEST_PLUGIN_CONF) ]; then \
		echo "Plugin test config missing — run 'make test-plugin-config' first."; \
		exit 1; \
	fi
	@echo "→ killing prior plugin-test server (-L $(TEST_PLUGIN_SOCKET))"
	@tmux -L $(TEST_PLUGIN_SOCKET) kill-server 2>/dev/null || true
	@echo "→ clean rebuild + reinstall"
	@rm -rf $(BIN_DIR)
	@$(MAKE) --no-print-directory install >/dev/null
	@echo "→ clearing debug log"
	@rm -f $${XDG_CACHE_HOME:-$$HOME/.cache}/atelier/debug.log
	@command -v atelier >/dev/null 2>&1 || { echo "atelier not on PATH after install."; exit 1; }
	@echo "→ launching tmux -L $(TEST_PLUGIN_SOCKET) (plugin mode)"
	tmux -L $(TEST_PLUGIN_SOCKET) -f $(TEST_PLUGIN_CONF) new-session -A -s default
