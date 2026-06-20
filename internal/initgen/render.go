package initgen

import (
	"fmt"
	"io"

	"github.com/vyrwu/atelier/internal/plugin"
)

// RenderOptions controls how a tmux config block is emitted. Both
// the bundled-launcher path (atelier the bare command) and the
// plugin-mode path (atelier init) go through Render with different
// options — same emission order, same primitives, no drift.
//
// History: before this unification, init.go and run.go each
// hand-rolled the block ordering and forgot invariants (the
// ATELIER_TMUX_SOCKET env line was missing in the bundled launcher
// for weeks, silently breaking restore). Single source of truth
// closes that class of bug.
type RenderOptions struct {
	// Socket: tmux socket name this config is for. When non-empty,
	// Render emits `set-environment -g ATELIER_TMUX_SOCKET <socket>`
	// at the top so every run-shell child (restore, stamp-statusline,
	// stamp-last-seen, status emitters) reads it via tmuxhost.New("")
	// and routes commands back via -L <socket>.
	//
	// Bundled launcher passes the socket here. Plugin mode leaves it
	// empty — the user's host tmux already has TMUX set, which is
	// the routing mechanism in that context.
	Socket string

	// IncludeTheme: emit ThemeBlock() or not. true for the curated
	// bundled experience; false for power users who own the visual
	// layer themselves (`atelier init --bare`).
	IncludeTheme bool

	// Header: a comment line written at the top of the output. Used
	// by callers to identify which path generated this config
	// ("atelier init (engine + theme)" vs the bundled launcher).
	Header string
}

// Render writes the complete tmux config to w in the canonical
// order. Returns the plugin discovery result so callers can report
// skipped tools.
//
// Block order matters and is fixed here:
//
//  1. Optional header comment.
//  2. Socket-routing env (if Socket != "").
//  3. Popup key-table shim — MUST be first emission of any
//     -T popup binding so subsequent `unbind -T popup` calls don't
//     fail "table popup doesn't exist".
//  4. Theme (if IncludeTheme).
//  5. Per-plugin bindings (from manifest discovery).
//  6. Core bindings (M-?, M-q).
//  7. Hooks (cleanup, attention, last-seen).
//  8. Statusline (stamp-statusline injection).
//  9. Restore (workspace rehydration from cache).
//
// Adding new emission steps means adding them here ONCE — both the
// bundled launcher and plugin mode pick them up automatically.
func Render(w io.Writer, opts RenderOptions) (*plugin.DiscoveryResult, error) {
	if opts.Header != "" {
		fmt.Fprintln(w, opts.Header)
		fmt.Fprintln(w)
	}

	// Socket-routing env line. Must come BEFORE any run-shell call
	// so children spawned during config sourcing see the var. The
	// rest of the blocks all use run-shell, so this is the FIRST
	// substantive line of the config.
	if opts.Socket != "" {
		fmt.Fprintf(w, "set-environment -g ATELIER_TMUX_SOCKET %q\n\n", opts.Socket)
	}

	fmt.Fprint(w, PopupTableShim())
	fmt.Fprintln(w)

	if opts.IncludeTheme {
		fmt.Fprint(w, ThemeBlock())
		fmt.Fprintln(w)
	}

	res, err := plugin.Discover()
	if err != nil {
		return nil, err
	}
	for _, p := range res.Plugins {
		block := BindingBlock(p.Name, p.Manifest)
		if block == "" {
			continue
		}
		fmt.Fprint(w, block)
		fmt.Fprintln(w)
	}

	fmt.Fprint(w, CoreBindingsBlock())
	fmt.Fprintln(w)
	fmt.Fprint(w, HooksBlock())
	fmt.Fprintln(w)
	fmt.Fprint(w, StatuslineBlock())
	fmt.Fprintln(w)
	fmt.Fprint(w, RestoreBlock())

	return res, nil
}
