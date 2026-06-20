// Package pg is atelier's pgcli / pgcenter popups — the bash-exact port
// of tmux_pg_picker, tmux_pg_setup, tmux_pg_launch, tmux_pg_switch,
// show_pgcli_popup, show_pgcenter_popup.
//
// Behavior:
//   - Two separate singleton sessions: _atelier_pgcli, _atelier_pgcenter
//   - Picker: fzf prompt `庫 ` color 110, label ` Postgres Contexts `, one
//     row per (context, endpoint) pair, endpoint suffix colored
//     green(108)=read / red(168)=write / grey(103)=other.
//   - First open shows picker; subsequent re-opens just attach
//   - Switch command always picks (M-; → pgcli when already running)
//   - SSM passwords cached in $XDG_CONFIG_HOME/atelier/pg/configs.yaml
//     under key `<context>:<endpoint>`
//   - libpq URI assembled with URL-encoded user+password
//   - Popup style options applied to new sessions
package pg

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
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

const (
	OptActivePgcli    = "@atelier_pgcli_active"
	OptActivePgcenter = "@atelier_pgcenter_active"
)

var (
	PgcliSpec = &popup.SessionGlobal{
		Tool:        "pgcli",
		DefaultCmd:  "pgcli",
		Description: "Singleton pgcli popup (bash-exact)",
	}
	PgcenterSpec = &popup.SessionGlobal{
		Tool:        "pgcenter",
		DefaultCmd:  "pgcenter top",
		Description: "Singleton pgcenter popup (bash-exact)",
	}
)

type Context struct {
	Name      string              `yaml:"name"`
	Database  string              `yaml:"database"`
	Port      int                 `yaml:"port"`
	Region    string              `yaml:"region,omitempty"`
	SsmRegion string              `yaml:"ssmRegion,omitempty"`
	AuthCmd   string              `yaml:"authCmd,omitempty"`
	Endpoints map[string]Endpoint `yaml:"endpoints"`
}

type Endpoint struct {
	Host            string `yaml:"host"`
	User            string `yaml:"user"`
	PasswordSsmPath string `yaml:"passwordSsmPath,omitempty"`
}

type contextsFile struct {
	Contexts []Context `yaml:"contexts"`
}

func LoadContexts(path string) ([]Context, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no pg contexts file at %s — create one (see docs)", path)
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cf contextsFile
	if err := yaml.Unmarshal(data, &cf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return cf.Contexts, nil
}

// PgcliCommand: show_pgcli_popup port — first call shows picker, subsequent
// re-attach.
func PgcliCommand() *cobra.Command {
	return openCommand("pgcli", PgcliSpec, OptActivePgcli)
}

// PgcenterCommand: show_pgcenter_popup port.
func PgcenterCommand() *cobra.Command {
	return openCommand("pgcenter", PgcenterSpec, OptActivePgcenter)
}

func openCommand(tool string, spec *popup.SessionGlobal, activeOpt string) *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   tool,
		Short: fmt.Sprintf("Open the %s singleton popup (picker on first call, attach on re-open)", tool),
		RunE: func(_ *cobra.Command, _ []string) error {
			h := tmuxhost.New(socket)
			has, _ := h.HasSession(spec.SessionName())
			if has {
				return spec.EnsureAndAttach(h)
			}
			ctx, role, err := pickEndpoint()
			if err != nil {
				if errors.Is(err, fzf.ErrCancelled) {
					return nil
				}
				return err
			}
			if ctx == nil {
				return nil
			}
			if err := setup(h, tool, *ctx, role); err != nil {
				return err
			}
			if err := workspace.SetPersistedGlobal(h, activeOpt, ctx.Name+":"+role); err != nil {
				return err
			}
			return spec.EnsureAndAttach(h)
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

// SwitchCommand: tmux_pg_switch port — always show picker, respawn if changed.
func SwitchCommand() *cobra.Command {
	var tool, socket string
	c := &cobra.Command{
		Use:   "switch",
		Short: "Force-pick a pg endpoint and respawn (default tool: pgcli)",
		RunE: func(_ *cobra.Command, _ []string) error {
			if tool == "" {
				tool = "pgcli"
			}
			spec := PgcliSpec
			activeOpt := OptActivePgcli
			if tool == "pgcenter" {
				spec = PgcenterSpec
				activeOpt = OptActivePgcenter
			}
			ctx, role, err := pickEndpoint()
			if err != nil {
				if errors.Is(err, fzf.ErrCancelled) {
					return nil
				}
				return err
			}
			if ctx == nil {
				return nil
			}
			h := tmuxhost.New(socket)
			active, _ := h.ShowGlobalOption(activeOpt)
			key := ctx.Name + ":" + role
			if active != key {
				if err := setup(h, tool, *ctx, role); err != nil {
					return err
				}
				if err := workspace.SetPersistedGlobal(h, activeOpt, key); err != nil {
					return err
				}
			}
			return spec.EnsureAndAttach(h)
		},
	}
	c.Flags().StringVar(&tool, "tool", "pgcli", "pgcli | pgcenter")
	c.Flags().StringVar(&socket, "socket", "", "tmux socket (tests only)")
	return c
}

func ContextsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "contexts",
		Short: "List configured pg contexts",
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
				for role := range c.Endpoints {
					fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", c.Name, role)
				}
			}
			return nil
		},
	}
}

// LaunchCommand runs INSIDE the popup pane — does SSM fetch + URI build + exec.
// Bash equivalent: tmux_pg_launch.
func LaunchCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "_launch",
		Short:  "internal: SSM fetch + URI build + exec pgcli/pgcenter (pane cmd)",
		Hidden: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			tool := os.Getenv("PG_TOOL")
			name := os.Getenv("PG_CONTEXT_NAME")
			role := os.Getenv("PG_ENDPOINT")
			if tool == "" || name == "" || role == "" {
				return fmt.Errorf("missing PG_TOOL / PG_CONTEXT_NAME / PG_ENDPOINT in env")
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
			ep, ok := ctx.Endpoints[role]
			if !ok {
				return fmt.Errorf("endpoint %q not defined for context %q", role, name)
			}
			port := ctx.Port
			if port == 0 {
				port = 5432
			}
			pw, err := fetchSSMPasswordCached(*ctx, role, ep)
			if err != nil {
				return waitOnError(err)
			}

			switch tool {
			case "pgcli":
				bin, err := exec.LookPath("pgcli")
				if err != nil {
					return waitOnError(fmt.Errorf("pgcli not on PATH"))
				}
				u := url.URL{
					Scheme: "postgresql",
					User:   url.UserPassword(ep.User, pw),
					Host:   fmt.Sprintf("%s:%d", ep.Host, port),
					Path:   "/" + ctx.Database,
				}
				return syscall.Exec(bin, []string{"pgcli", u.String()}, os.Environ())
			case "pgcenter":
				bin, err := exec.LookPath("pgcenter")
				if err != nil {
					return waitOnError(fmt.Errorf("pgcenter not on PATH"))
				}
				env := append(os.Environ(), "PGPASSWORD="+pw)
				return syscall.Exec(bin, []string{
					"pgcenter", "top",
					"-h", ep.Host,
					"-p", fmt.Sprintf("%d", port),
					"-U", ep.User,
					"-d", ctx.Database,
				}, env)
			default:
				return waitOnError(fmt.Errorf("unknown tool %q", tool))
			}
		},
	}
}

// pickEndpoint: bash-exact tmux_pg_picker port.
//
//	prompt: 庫 (color 110)
//	label:  Postgres Contexts
//	rows:   "<name> · <colored-endpoint>" with-nth=1 hides parseable suffix
//	endpoint color: read=108(green), write=168(red), other=103(grey)
//	output: <name>\t<endpoint>
func pickEndpoint() (*Context, string, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, "", err
	}
	contexts, err := LoadContexts(cfg.Contexts)
	if err != nil {
		return nil, "", err
	}
	lines, lookup := flattenEndpoints(contexts)
	if len(lines) == 0 {
		return nil, "", fmt.Errorf("no pg endpoints in %s", cfg.Contexts)
	}

	args := fzfstyle.Args("庫 ", "Postgres Contexts", "110",
		fzfstyle.WithCustomColor("prompt:110:bold,pointer:110,query:110,hl:-1,hl+:-1,label:103,border:103,footer:103"),
		fzfstyle.WithDelimiter("\t"),
		fzfstyle.WithNth("1"),
	)

	picked, err := fzf.Pick(lines, args...)
	if err != nil {
		return nil, "", err
	}
	entry, ok := lookup[picked]
	if !ok || entry.Ctx == nil {
		return nil, "", fmt.Errorf("picked entry %q not resolvable", picked)
	}
	return entry.Ctx, entry.Role, nil
}

type lookupEntry struct {
	Ctx  *Context
	Role string
}

// flattenEndpoints emits bash-style colored lines:
//
//	<name> · <colored endpoint>\t<name>\t<endpoint>
//
// with-nth=1 in the picker hides the trailing parseable fields.
func flattenEndpoints(contexts []Context) (lines []string, lookup map[string]lookupEntry) {
	lookup = make(map[string]lookupEntry)
	for i, ctx := range contexts {
		// Stable order: read, write, then any others alphabetically.
		ordered := orderedRoles(ctx.Endpoints)
		for _, role := range ordered {
			var color string
			switch role {
			case "read":
				color = "\033[38;5;108m"
			case "write":
				color = "\033[38;5;168m"
			default:
				color = "\033[38;5;103m"
			}
			line := fmt.Sprintf("%s · %s%s\033[0m\t%s\t%s", ctx.Name, color, role, ctx.Name, role)
			lines = append(lines, line)
			lookup[line] = lookupEntry{Ctx: &contexts[i], Role: role}
		}
	}
	return
}

func orderedRoles(eps map[string]Endpoint) []string {
	prio := []string{"read", "write"}
	var out []string
	seen := map[string]bool{}
	for _, p := range prio {
		if _, ok := eps[p]; ok {
			out = append(out, p)
			seen[p] = true
		}
	}
	var rest []string
	for k := range eps {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	// stable-ish: simple sort
	for i := 1; i < len(rest); i++ {
		for j := i; j > 0 && rest[j-1] > rest[j]; j-- {
			rest[j-1], rest[j] = rest[j], rest[j-1]
		}
	}
	return append(out, rest...)
}

// setup is the bash-exact tmux_pg_setup port. Spawns the singleton session
// or respawns-pane -k in the existing one, applying the popup style options
// on first creation. The pane runs `atelier tools pg _launch`, which reads
// PG_TOOL/PG_CONTEXT_NAME/PG_ENDPOINT from env to do its work.
func setup(h *tmuxhost.Client, tool string, ctx Context, role string) error {
	spec := PgcliSpec
	if tool == "pgcenter" {
		spec = PgcenterSpec
	}

	atelierBin, err := exec.LookPath("atelier")
	if err != nil {
		atelierBin = "atelier"
	}

	authCmd := strings.TrimSpace(ctx.AuthCmd)
	if authCmd != "" {
		authCmd += " "
	}
	runCmd := fmt.Sprintf("%s%s tools pg _launch", authCmd, atelierBin)

	session := spec.SessionName()
	has, err := h.HasSession(session)
	if err != nil {
		return err
	}
	if has {
		_, err := h.Run("respawn-pane", "-k",
			"-e", "PG_TOOL="+tool,
			"-e", "PG_CONTEXT_NAME="+ctx.Name,
			"-e", "PG_ENDPOINT="+role,
			"-t", session+":1.1",
			runCmd)
		if err != nil {
			return err
		}
	} else {
		home, _ := os.UserHomeDir()
		_, err := h.Run("new-session", "-d", "-s", session, "-c", home,
			"-e", "PG_TOOL="+tool,
			"-e", "PG_CONTEXT_NAME="+ctx.Name,
			"-e", "PG_ENDPOINT="+role,
			runCmd)
		if err != nil {
			return err
		}
		if err := popup.ApplyStyle(h, session); err != nil {
			return err
		}
	}
	if _, err := h.Run("set-option", "-t", session, "@pg_context", ctx.Name); err != nil {
		return err
	}
	if _, err := h.Run("set-option", "-t", session, "@pg_endpoint", role); err != nil {
		return err
	}
	return nil
}

// fetchSSMPasswordCached uses the configs.yaml SSM cache (atelier path).
func fetchSSMPasswordCached(ctx Context, role string, ep Endpoint) (string, error) {
	if ep.PasswordSsmPath == "" {
		return "", nil
	}
	cacheKey := ctx.Name + ":" + role
	if pw, ok := GetCachedByKey(cacheKey); ok {
		return pw, nil
	}
	region := ctx.SsmRegion
	if region == "" {
		region = ctx.Region
	}
	pw, err := awsSSMGetParameter(ep.PasswordSsmPath, region)
	if err != nil {
		return "", err
	}
	_ = SetCachedByKey(cacheKey, pw)
	return pw, nil
}

func awsSSMGetParameter(ssmPath, region string) (string, error) {
	if _, err := exec.LookPath("aws"); err != nil {
		return "", fmt.Errorf("aws CLI not on PATH: %w", err)
	}
	args := []string{"ssm", "get-parameter", "--name", ssmPath, "--with-decryption", "--query", "Parameter.Value", "--output", "text"}
	if region != "" {
		args = append([]string{"--region", region}, args...)
	}
	cmd := exec.Command("aws", args...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("aws ssm get-parameter: %w (%s)", err, strings.TrimSpace(errBuf.String()))
	}
	pw := strings.TrimSpace(out.String())
	if pw == "" || pw == "None" {
		return "", fmt.Errorf("SSM returned empty value for %s", ssmPath)
	}
	return pw, nil
}

// waitOnError prints err + waits for Enter (so the user can read the error
// before the popup closes). Bash equivalent: `read -r -p "press enter to dismiss "`.
func waitOnError(err error) error {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
	fmt.Fprint(os.Stderr, "press enter to dismiss ")
	var b [1]byte
	_, _ = os.Stdin.Read(b[:])
	return err
}

// buildLaunchCommand is retained for tests — it composes a shell command
// equivalent to what `tmux_pg_launch` used to produce (no SSM cache).
// New code paths use the _launch subcommand instead.
func buildLaunchCommand(ctx Context, role, tool string) (string, error) {
	ep, ok := ctx.Endpoints[role]
	if !ok {
		return "", fmt.Errorf("context %q has no endpoint %q", ctx.Name, role)
	}
	port := ctx.Port
	if port == 0 {
		port = 5432
	}
	password, err := fetchSSMPassword(ep.PasswordSsmPath, ctx.Region)
	if err != nil {
		return "", err
	}
	u := url.URL{
		Scheme: "postgresql",
		User:   url.UserPassword(ep.User, password),
		Host:   fmt.Sprintf("%s:%d", ep.Host, port),
		Path:   "/" + ctx.Database,
	}
	core := fmt.Sprintf(`%s %q`, tool, u.String())
	if ctx.AuthCmd != "" {
		return fmt.Sprintf(`%s sh -c %q`, ctx.AuthCmd, core), nil
	}
	return core, nil
}

func fetchSSMPassword(ssmPath, region string) (string, error) {
	if ssmPath == "" {
		return "", nil
	}
	if pw, ok := GetCachedPassword(ssmPath); ok {
		return pw, nil
	}
	if _, err := exec.LookPath("aws"); err != nil {
		return "", nil // tests run without aws; treat as empty pw
	}
	pw, err := awsSSMGetParameter(ssmPath, region)
	if err != nil {
		return "", err
	}
	_ = SetCachedPassword(ssmPath, pw)
	return pw, nil
}

var _ = filepath.Join // retained for future use
