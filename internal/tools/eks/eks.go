// Package eks is atelier's "EKS Assume role" tool: pick a context, assume its
// admin role via granted, point kubectl at the matching cluster, and drop into
// an interactive SHELL in a popup — the kubectl equivalent of the k9s tool.
//
// It mirrors internal/tools/k8s (same contexts.yaml shape: name / context /
// authCmd / initCmd; same per-context kubeconfig cache + granted-assume auth +
// respawn-on-change singleton popup) with one difference: the popup runs an
// interactive $SHELL (so you can run kubectl/helm/etc.) instead of launching
// k9s. Because it's a near-clone, a shared `kubectx` primitive is a sensible
// future extraction; kept separate here to avoid refactoring the load-bearing
// k8s tool.
package eks

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

	"github.com/vyrwu/atelier/internal/awsassume"
	"github.com/vyrwu/atelier/internal/dispatch"
	"github.com/vyrwu/atelier/internal/fzf"
	"github.com/vyrwu/atelier/internal/fzfstyle"
	hostpopup "github.com/vyrwu/atelier/internal/host/popup"
	"github.com/vyrwu/atelier/internal/manifest"
	"github.com/vyrwu/atelier/internal/popup"
	"github.com/vyrwu/atelier/internal/tmuxhost"
	"github.com/vyrwu/atelier/internal/workspace"
)

// OptActiveContext tracks which context the singleton EKS shell is currently
// authed to, so a re-pick of the SAME context skips the respawn (preserves the
// shell) and a DIFFERENT context respawns.
const OptActiveContext = "@atelier_eks_active"

// Spec is the singleton backing session for the EKS shell popup.
var Spec = &popup.SessionGlobal{
	Tool:        "eks",
	DefaultCmd:  awsassume.DefaultShell(),
	Description: "Singleton EKS assume-role shell popup",
}

// Context is one EKS target: an admin role to assume (AuthCmd) and a cluster to
// point kubectl at (InitCmd + KubeContext). Same shape as the k8s tool's.
type Context struct {
	Name        string `yaml:"name"`
	KubeContext string `yaml:"context,omitempty"`
	// AuthCmd is the granted auth prefix, e.g. "assume <admin-profile> --exec".
	// Because `assume` is a sourced shell function it runs inside an interactive
	// shell with the launch passed as its single --exec command (awsassume).
	// Empty = no AWS auth.
	AuthCmd string `yaml:"authCmd,omitempty"`
	// InitCmd builds the per-context kubeconfig on first open, e.g.
	// "aws eks update-kubeconfig --name <cluster> --region <r> --kubeconfig $KUBECONFIG".
	InitCmd string `yaml:"initCmd,omitempty"`
}

type contextsFile struct {
	Contexts []Context `yaml:"contexts"`
}

// LoadContexts reads the EKS contexts.yaml.
func LoadContexts(path string) ([]Context, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no eks contexts file at %s — create one (see docs)", path)
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cf contextsFile
	if err := yaml.Unmarshal(data, &cf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return cf.Contexts, nil
}

// OpenCommand: pick a context (small picker popup) → respawn the shell if the
// context changed → open the full-size shell popup. Unlike k9s there is no
// fast-path/skip-picker: the picker always shows, so re-choosing is the way to
// switch clusters (respawn-per-context). Re-picking the SAME context is a
// no-op respawn, so the existing shell + its scrollback survive.
func OpenCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "open",
		Short: "Pick an EKS context (small popup); an authed kubectl shell opens after",
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
			ctx := findContext(contexts, picked)
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
			queueFullShellPopup(h)
			return nil
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// AttachCommand is the deferred full-size entry queued by OpenCommand — context
// selection + setup already happened in the picker popup.
func AttachCommand() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:    "_attach",
		Short:  "internal: attach to the EKS shell singleton (post-picker deferred entry)",
		Hidden: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return Spec.EnsureAndAttach(tmuxhost.New(socket))
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// ContextsCommand lists configured EKS contexts (CLI).
func ContextsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "contexts",
		Short: "List configured EKS contexts",
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

// LaunchCommand runs INSIDE the popup pane (wrapped by granted assume). It
// lazily builds the kubeconfig (initCmd) on first open, selects the cluster
// context, then execs an interactive shell so the user can run kubectl with
// the assumed creds + the right cluster already in context. This is the ONE
// place the EKS tool diverges from k9s (which execs k9s here).
func LaunchCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "_launch",
		Short:  "internal: lazy-init + set context + exec an interactive shell (runs inside popup pane)",
		Hidden: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			name := os.Getenv("EKS_CONTEXT_NAME")
			if name == "" {
				return fmt.Errorf("missing EKS_CONTEXT_NAME")
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
			ctx := findContext(contexts, name)
			if ctx == nil {
				return fmt.Errorf("context %q not in %s", name, cfg.Contexts)
			}
			kubeContext := ctx.KubeContext
			if kubeContext == "" {
				kubeContext = ctx.Name
			}
			// Lazy init if the per-context kubeconfig is still empty.
			if info, err := os.Stat(kubeconfig); err != nil || info.Size() == 0 {
				if ctx.InitCmd != "" {
					fmt.Printf("Initializing EKS context %q...\n", name)
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
			// Point kubectl at the matching cluster (best-effort — the kubeconfig
			// initCmd usually already sets current-context).
			_ = exec.Command("kubectl", "config", "use-context", kubeContext).Run()

			// Drop into an interactive shell WITH the assumed creds + KUBECONFIG.
			shell := awsassume.DefaultShell()
			fmt.Printf("EKS context %q ready — kubectl is pointed at %s\n", name, kubeContext)
			return syscall.Exec(shell, []string{shell, "-i"}, os.Environ())
		},
	}
}

func findContext(contexts []Context, name string) *Context {
	for i := range contexts {
		if contexts[i].Name == name {
			return &contexts[i]
		}
	}
	return nil
}

// queueFullShellPopup opens the full-style EKS shell popup on the outer client
// (same deferred-popup dance k9s uses so it never nests on a sibling popup).
func queueFullShellPopup(h *tmuxhost.Client) {
	styleArgs := hostpopup.PopupStyleArgs(&manifest.Binding{Style: manifest.StyleFull, Title: "EKS"})
	_ = hostpopup.OpenOnOuter(h, styleArgs, dispatch.ToolCmd("eks", "_attach"))
}

// pickContext shows the EKS context fzf picker (cloud glyph, orange accent).
func pickContext(contexts []Context) (string, error) {
	names := make([]string, 0, len(contexts))
	for _, c := range contexts {
		names = append(names, c.Name)
	}
	args := fzfstyle.Args("雲 ", "EKS Contexts", "208",
		fzfstyle.WithCustomColor("prompt:208:bold,pointer:208,query:208,hl:208,hl+:208:bold,label:103,border:103,footer:103"),
	)
	return fzf.Pick(names, args...)
}

// setup resolves the per-context kubeconfig, hydrates it from the cache, and
// respawns (or creates) the singleton popup pane with the granted-assume-wrapped
// launch for this context. Mirrors k8s setup, with the eks _launch + env prefix.
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
				if out, err := yaml.Marshal(v); err == nil {
					_ = os.WriteFile(kubeconfig, out, 0o600)
				}
			}
		}
	}
	if _, err := os.Stat(kubeconfig); err != nil {
		_ = os.WriteFile(kubeconfig, nil, 0o600) // touch so launch detects "needs init"
	}

	atelierBin, err := exec.LookPath("atelier")
	if err != nil {
		atelierBin = "atelier"
	}
	launch := fmt.Sprintf("%s tools eks _launch", atelierBin)
	runCmd := awsassume.WrapAuth(ctx.AuthCmd, launch, awsassume.DefaultShell())

	session := Spec.SessionName()
	has, err := h.HasSession(session)
	if err != nil {
		return err
	}
	if has {
		if _, err := h.Run("respawn-pane", "-k",
			"-e", "KUBECONFIG="+kubeconfig,
			"-e", "EKS_CONTEXT_NAME="+ctx.Name,
			"-t", session+":1.1",
			runCmd); err != nil {
			return err
		}
	} else {
		home, _ := os.UserHomeDir()
		if _, err := h.Run("new-session", "-d", "-s", session, "-c", home,
			"-e", "KUBECONFIG="+kubeconfig,
			"-e", "EKS_CONTEXT_NAME="+ctx.Name,
			runCmd); err != nil {
			return err
		}
		if err := popup.ApplyStyle(h, session); err != nil {
			return err
		}
	}
	if _, err := h.Run("set-option", "-t", session, "@eks_context", ctx.Name); err != nil {
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
	return filepath.Join(cache, "atelier", "eks", safeFilename(name)), nil
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
