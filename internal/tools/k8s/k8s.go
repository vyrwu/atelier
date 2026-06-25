// Package k8s is atelier's singleton k9s popup with context switching —
// the bash-exact port of tmux_k8s_picker + tmux_k8s_setup + tmux_k8s_launch
// + show_k8s_popup.
//
// Behavior:
//
//   - Picker: fzf with prompt `胡 ` (blue), label ` Contexts `, blue palette.
//     Reads from $XDG_CONFIG_HOME/atelier/k8s/contexts.yaml (Config.Contexts).
//   - Setup: hydrates per-context kubeconfig from configs.yaml cache when
//     present, then respawns the popup pane with the new auth+launch
//     command (respawn-pane -k preserves session, drops k9s state).
//   - Launch: runs initCmd if kubeconfig still empty, caches the kubeconfig
//     into configs.yaml for next time, then execs k9s.
//   - Popup style: key-table popup, status off, prefix None, prefix2 None,
//     aggressive-resize on (matches bash).
//   - Same-context re-open: skip respawn, just attach (preserves k9s state).
package k8s

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/vyrwu/atelier/internal/fzf"
	"github.com/vyrwu/atelier/internal/fzfstyle"
	"github.com/vyrwu/atelier/internal/popup"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

const OptActiveContext = "@atelier_k8s_active"

var Spec = &popup.SessionGlobal{
	Tool:        "k8s",
	DefaultCmd:  "k9s",
	Description: "Singleton k9s popup (bash-exact)",
}

type Context struct {
	Name        string `yaml:"name"`
	KubeContext string `yaml:"context,omitempty"`
	AuthCmd     string `yaml:"authCmd,omitempty"`
	InitCmd     string `yaml:"initCmd,omitempty"`
}

type contextsFile struct {
	Contexts []Context `yaml:"contexts"`
}

func LoadContexts(path string) ([]Context, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no k8s contexts file at %s — create one (see docs)", path)
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cf contextsFile
	if err := yaml.Unmarshal(data, &cf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return cf.Contexts, nil
}

// OpenCommand: picker → setup (respawn-if-changed) → attach.
// Bash equivalents: show_k8s_popup → tmux_k8s_picker → tmux_k8s_setup.
func OpenCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "open",
		Short: "Pick a context, set up the k9s singleton, attach (bash-exact)",
		RunE: func(_ *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			contexts, err := LoadContexts(cfg.Contexts)
			if err != nil {
				return err
			}
			if len(contexts) == 0 {
				return fmt.Errorf("no contexts in %s", cfg.Contexts)
			}

			active, _ := h.ShowGlobalOption(OptActiveContext)
			has, _ := h.HasSession(Spec.SessionName())

			// If session is already up on the active context, attach directly
			// (matches bash: re-open without picker when session exists).
			if has && active != "" {
				return Spec.EnsureAndAttach(h)
			}

			picked, err := pickContext(contexts)
			if err != nil {
				if errors.Is(err, fzf.ErrCancelled) {
					return nil
				}
				return err
			}

			var ctx *Context
			for i := range contexts {
				if contexts[i].Name == picked {
					ctx = &contexts[i]
					break
				}
			}
			if ctx == nil {
				return fmt.Errorf("picked context %q not found", picked)
			}

			if err := setup(h, *ctx); err != nil {
				return err
			}
			if err := workspace.SetPersistedGlobal(h, OptActiveContext, ctx.Name); err != nil {
				return err
			}
			return Spec.EnsureAndAttach(h)
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// SwitchCommand: force-picker re-open (M-; → k8s when already running).
// Always shows the picker; if a different context is picked, respawns the
// popup pane. Same-context picks become a no-op + attach.
func SwitchCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "switch",
		Short: "Force-pick a k8s context (respawn if changed, no-op if same)",
		RunE: func(_ *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			contexts, err := LoadContexts(cfg.Contexts)
			if err != nil {
				return err
			}
			if len(contexts) == 0 {
				return fmt.Errorf("no contexts in %s", cfg.Contexts)
			}
			picked, err := pickContext(contexts)
			if err != nil {
				if errors.Is(err, fzf.ErrCancelled) {
					return nil
				}
				return err
			}
			var ctx *Context
			for i := range contexts {
				if contexts[i].Name == picked {
					ctx = &contexts[i]
					break
				}
			}
			if ctx == nil {
				return fmt.Errorf("picked context %q not found", picked)
			}
			active, _ := h.ShowGlobalOption(OptActiveContext)
			if active != ctx.Name {
				if err := setup(h, *ctx); err != nil {
					return err
				}
				if err := workspace.SetPersistedGlobal(h, OptActiveContext, ctx.Name); err != nil {
					return err
				}
			}
			// When invoked from inside the K9s popup itself (M-c chord)
			// the calling client is already attached to _atelier_k8s;
			// setup's respawn-pane swapped its process in place, so the
			// user is now looking at the new context. Calling Attach
			// here would syscall.Exec tmux into the switch picker's
			// popup pty, opening a SECOND popup-client on the same
			// session — visible to the user as a duplicated K9s stack.
			// Skip the attach when we're already inside.
			curSession, _ := h.DisplayMessage("#{session_name}")
			if strings.TrimSpace(curSession) == Spec.SessionName() {
				return nil
			}
			return Spec.EnsureAndAttach(h)
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

func ContextsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "contexts",
		Short: "List configured k8s contexts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			contexts, err := LoadContexts(cfg.Contexts)
			if err != nil {
				return err
			}
			for _, c := range contexts {
				fmt.Fprintln(cmd.OutOrStdout(), c.Name)
			}
			return nil
		},
	}
}

// LaunchCommand is invoked INSIDE the popup pane (as the pane's command).
// It does the lazy initCmd-when-empty + kubeconfig caching + exec k9s,
// matching bash's tmux_k8s_launch.
func LaunchCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "_launch",
		Short:  "internal: lazy-init + exec k9s (runs inside popup pane)",
		Hidden: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			name := os.Getenv("K8S_CONTEXT_NAME")
			if name == "" {
				return fmt.Errorf("missing K8S_CONTEXT_NAME")
			}
			kubeconfig := os.Getenv("KUBECONFIG")
			if kubeconfig == "" {
				return fmt.Errorf("missing KUBECONFIG")
			}
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			contexts, err := LoadContexts(cfg.Contexts)
			if err != nil {
				return err
			}
			var ctx *Context
			for i := range contexts {
				if contexts[i].Name == name {
					ctx = &contexts[i]
					break
				}
			}
			if ctx == nil {
				return fmt.Errorf("context %q not in %s", name, cfg.Contexts)
			}
			kubeContext := ctx.KubeContext
			if kubeContext == "" {
				kubeContext = ctx.Name
			}
			// Lazy init if kubeconfig empty.
			if info, err := os.Stat(kubeconfig); err != nil || info.Size() == 0 {
				if ctx.InitCmd != "" {
					fmt.Printf("Initializing kube context %q...\n", name)
					initCmd := exec.Command("sh", "-c", ctx.InitCmd)
					initCmd.Stdout = os.Stdout
					initCmd.Stderr = os.Stderr
					initCmd.Env = os.Environ()
					if err := initCmd.Run(); err != nil {
						return fmt.Errorf("initCmd: %w", err)
					}
					if err := cacheKubeconfig(cfg.Configs, name, kubeconfig); err != nil {
						fmt.Fprintf(os.Stderr, "warning: cache kubeconfig: %v\n", err)
					} else {
						fmt.Printf("Cached kubeconfig for %q in %s\n", name, cfg.Configs)
					}
				}
			}
			bin, err := exec.LookPath("k9s")
			if err != nil {
				return err
			}
			return syscall.Exec(bin, []string{"k9s", "--headless", "--context", kubeContext}, os.Environ())
		},
	}
}

// pickContext is the bash-exact tmux_k8s_picker port (prompt 胡 blue, label
// ` Contexts `, blue hl).
func pickContext(contexts []Context) (string, error) {
	names := make([]string, 0, len(contexts))
	for _, c := range contexts {
		names = append(names, c.Name)
	}
	args := fzfstyle.Args("胡 ", "Contexts", "blue",
		fzfstyle.WithCustomColor("prompt:blue:bold,pointer:blue,query:blue,hl:blue,hl+:blue:bold,label:103,border:103,footer:103"),
	)
	return fzf.Pick(names, args...)
}

// setup is the bash-exact tmux_k8s_setup port:
//
//   - resolve per-context kubeconfig path under XDG_CACHE_HOME/atelier/k8s/
//   - hydrate from configs.yaml cache if present
//   - respawn-pane -k on the existing _atelier_k8s session, OR new-session
//     with the popup style options applied
func setup(h *tmuxhost.Client, ctx Context) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}

	kubeconfig, err := kubeconfigPathFor(ctx.Name)
	if err != nil {
		return err
	}

	// Hydrate from configs.yaml cache if present.
	_ = os.MkdirAll(filepath.Dir(kubeconfig), 0o755)
	if data, err := os.ReadFile(cfg.Configs); err == nil {
		var all map[string]any
		if yaml.Unmarshal(data, &all) == nil {
			if v, ok := all[ctx.Name]; ok && v != nil {
				out, err := yaml.Marshal(v)
				if err == nil {
					_ = os.WriteFile(kubeconfig, out, 0o600)
				}
			}
		}
	}
	if _, err := os.Stat(kubeconfig); err != nil {
		// Touch empty file so launch detects "needs init".
		_ = os.WriteFile(kubeconfig, nil, 0o600)
	}

	atelierBin, err := exec.LookPath("atelier")
	if err != nil {
		atelierBin = "atelier"
	}

	authCmd := ctx.AuthCmd
	if strings.TrimSpace(authCmd) != "" && !strings.HasSuffix(strings.TrimSpace(authCmd), ";") {
		authCmd = strings.TrimSpace(authCmd) + " "
	}
	runCmd := fmt.Sprintf("%s%s tools k8s _launch", authCmd, atelierBin)

	session := Spec.SessionName()
	has, err := h.HasSession(session)
	if err != nil {
		return err
	}
	if has {
		// respawn-pane -k preserves the session (so options stick) but
		// replaces the running k9s with a fresh auth+launch for the new
		// context.
		_, err := h.Run("respawn-pane", "-k",
			"-e", "KUBECONFIG="+kubeconfig,
			"-e", "K8S_CONTEXT_NAME="+ctx.Name,
			"-t", session+":1.1",
			runCmd)
		if err != nil {
			return err
		}
	} else {
		home, _ := os.UserHomeDir()
		_, err := h.Run("new-session", "-d", "-s", session, "-c", home,
			"-e", "KUBECONFIG="+kubeconfig,
			"-e", "K8S_CONTEXT_NAME="+ctx.Name,
			runCmd)
		if err != nil {
			return err
		}
		if err := popup.ApplyStyle(h, session); err != nil {
			return err
		}
	}
	if _, err := h.Run("set-option", "-t", session, "@k8s_context", ctx.Name); err != nil {
		return err
	}
	return nil
}

func cacheKubeconfig(configsFile, name, kubeconfigPath string) error {
	if err := os.MkdirAll(filepath.Dir(configsFile), 0o755); err != nil {
		return err
	}
	data, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		return err
	}
	var kc any
	if err := yaml.Unmarshal(data, &kc); err != nil {
		return err
	}

	all := map[string]any{}
	if existing, err := os.ReadFile(configsFile); err == nil {
		_ = yaml.Unmarshal(existing, &all)
	}
	if all == nil {
		all = map[string]any{}
	}
	all[name] = kc
	out, err := yaml.Marshal(all)
	if err != nil {
		return err
	}
	return os.WriteFile(configsFile, out, 0o600)
}

func kubeconfigPathFor(name string) (string, error) {
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		cache = filepath.Join(home, ".cache")
	}
	safe := safeFilename(name)
	return filepath.Join(cache, "atelier", "k8s", safe), nil
}

func safeFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
