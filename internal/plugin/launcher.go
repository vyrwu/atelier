package plugin

import (
	"fmt"
	"os"
	"sort"
	"syscall"

	"github.com/vyrwu/atelier/internal/config"
	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/popup"
	"github.com/vyrwu/atelier/internal/tmuxhost"
)

// LauncherSpec is a `[tools.<name>]` block in config.toml. It registers
// ANY command as an atelier tool without writing Go: atelier binds a key
// to it, opens it in a popup of the declared shape, and owns the window
// state. The command doesn't have to be an atelier binary.
//
//	[tools.k9s-aws]
//	launch      = "aws-vault-k9s"   # any executable on PATH
//	popup       = "global"          # workspace | global | none
//	key         = "K"               # optional tmux binding
//	requires    = ["aws-vault-k9s"] # doctor checks these are present
//	icon        = "胡"
//	accent_color = "110"
//	title       = "K9s (AWS)"
//	description = "k9s with AWS SSO auth"
type LauncherSpec struct {
	Launch      string   `toml:"launch"`
	Popup       string   `toml:"popup"`
	Key         string   `toml:"key"`
	KeyTable    string   `toml:"key_table"`
	Requires    []string `toml:"requires"`
	Icon        string   `toml:"icon"`
	AccentColor string   `toml:"accent_color"`
	Title       string   `toml:"title"`
	Description string   `toml:"description"`
	Invoke      string   `toml:"invoke"`
	StartCwd    *bool    `toml:"start_cwd"`
}

// loadLaunchers reads the `[tools]` table from config.toml and turns each
// subtable into a launcher Plugin. Malformed entries are returned in the
// skipped map (keyed by config section) rather than failing the whole
// discovery — one bad launcher must not hide every good tool.
func loadLaunchers() (plugins []Plugin, skipped map[string]error) {
	skipped = map[string]error{}
	raw := map[string]LauncherSpec{}
	if err := config.LoadSection("tools", &raw); err != nil {
		skipped["[tools]"] = err
		return nil, skipped
	}
	names := make([]string, 0, len(raw))
	for name := range raw {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		spec := raw[name]
		if spec.Launch == "" {
			skipped["[tools."+name+"]"] = fmt.Errorf("missing `launch` command")
			continue
		}
		m, err := spec.toManifest(name)
		if err != nil {
			skipped["[tools."+name+"]"] = err
			continue
		}
		plugins = append(plugins, Plugin{Name: name, Manifest: m, launch: spec.Launch})
	}
	return plugins, skipped
}

// toManifest synthesizes a manifest from a launcher spec so launchers flow
// through the exact same binding-generation, selector, and doctor paths as
// built-in tools.
func (s LauncherSpec) toManifest(name string) (*manifest.Manifest, error) {
	popupKind := manifest.Kind(s.Popup)
	if s.Popup == "" {
		popupKind = manifest.KindNone
	}
	startCwd := popupKind == manifest.KindWorkspace
	if s.StartCwd != nil {
		startCwd = *s.StartCwd
	}
	invoke := s.Invoke
	if invoke == "" {
		invoke = "open"
	}
	m := &manifest.Manifest{
		Name:          name,
		Description:   s.Description,
		Tool:          true,
		Popup:         popupKind,
		PrimaryInvoke: invoke,
		Requires:      s.Requires,
		Binding: &manifest.Binding{
			Key:      s.Key,
			KeyTable: s.KeyTable,
			Style:    manifest.StyleFull,
			StartCwd: startCwd,
			Invoke:   invoke,
			Title:    s.Title,
		},
		UI: &manifest.UI{
			Icon:        s.Icon,
			AccentColor: s.AccentColor,
			PopupTitle:  s.Title,
		},
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return m, nil
}

// runLauncher opens the launcher's command with its declared popup
// lifecycle. It runs from inside the popup pty the binding already opened
// (display-popup -E 'atelier tools <name> open'), so:
//
//   - workspace: a per-window backing session running the command
//   - global:    a shared singleton backing session running the command
//   - none:      exec the command directly in this pty (no backing session)
func (p *Plugin) runLauncher(_ []string) error {
	h := tmuxhost.New("")
	switch p.Manifest.Popup {
	case manifest.KindWorkspace:
		return popup.OpenWorkspaceScoped(h, &popup.WorkspaceScoped{
			Tool:        p.Name,
			DefaultCmd:  p.launch,
			Description: p.Manifest.Description,
		})
	case manifest.KindGlobal:
		return (&popup.SessionGlobal{
			Tool:        p.Name,
			DefaultCmd:  p.launch,
			Description: p.Manifest.Description,
		}).EnsureAndAttach(h)
	default:
		return execInPopup(p.launch)
	}
}

// execInPopup replaces the current process with the launcher command via a
// login-ish shell so quoting/args/PATH resolution behave as the user
// expects. syscall.Exec keeps the popup pty; the command simply takes over
// this PID.
func execInPopup(cmdline string) error {
	shell, argv := shellExecArgs(cmdline, os.Getenv("SHELL"))
	return syscall.Exec(shell, argv, os.Environ())
}

// shellExecArgs resolves the shell and argv for exec'ing cmdline. Pure —
// extracted so the shell fallback + `-c` argv assembly is unit-testable
// without actually exec'ing. shellEnv is the value of $SHELL ("" → /bin/sh).
func shellExecArgs(cmdline, shellEnv string) (shell string, argv []string) {
	shell = shellEnv
	if shell == "" {
		shell = "/bin/sh"
	}
	return shell, []string{shell, "-c", cmdline}
}
