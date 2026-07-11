// Package integration defines the kernel's ports and holds the active
// adapters selected by config.
//
// This is the hexagonal boundary. The kernel (workspace views, pickers,
// statusline) owns the functionality and the presentation; when it needs a
// capability it cannot implement itself — an AI summary, a code-forge
// status — it calls a PORT defined here. An INTEGRATION is an adapter that
// satisfies a port; it is a bounded provider, never a driver. The kernel
// pulls; integrations do not push.
//
// Dependency rule: adapters (internal/adapters/*) import this package
// to implement its interfaces; the kernel imports this package to call
// them; NEITHER imports the other. The composition root (cmd/atelier) reads
// config, constructs the chosen adapters, and installs them via SetActive.
// A short-lived CLI process resolves the active set once at startup and the
// kernel reads it through Active().
//
// Predictable over dynamic: the set of ports is small and kernel-defined.
// Adding a capability means the kernel grows a port and wires it into a
// view — never a dynamic injection mechanism. When no adapter is installed
// for a port, the kernel degrades gracefully (the capability is simply
// absent).
package integration

// WorkspaceRef identifies a workspace window for a port query. The kernel
// populates it from tmux/statestore; adapters treat it as read-only input.
type WorkspaceRef struct {
	WindowID string // tmux window id, e.g. "@3"
	Cwd      string // worktree path (may be empty for non-git workspaces)
	RepoPath string // @repo_path (empty for non-git)
}

// ForgeState is the kernel's normalized code-forge state vocabulary. Every
// ForgeIntegration maps its native state onto one of these; the KERNEL owns
// the glyph, color, and picker sort order. Adapters classify; they never
// render.
type ForgeState string

const (
	ForgeNone   ForgeState = ""       // no associated forge item
	ForgeOpen   ForgeState = "open"   // open PR/MR
	ForgeDraft  ForgeState = "draft"  // draft PR/MR
	ForgeMerged ForgeState = "merged" // merged
	ForgeClosed ForgeState = "closed" // closed without merge
)

// ForgeStatus is what a ForgeIntegration reports for one workspace.
type ForgeStatus struct {
	State ForgeState
}

// ForgeIntegration is the port a code-forge adapter (GitHub, GitLab, …)
// satisfies to enrich the workspace picker with per-workspace forge status
// and an open-in-browser action. The kernel owns the badge slot, rendering,
// sort order, caching, and refresh cadence; the adapter only classifies and
// opens.
type ForgeIntegration interface {
	// Name is the adapter's identifier (e.g. "github"). Used in diagnostics.
	Name() string
	// Status classifies the workspace's forge item into a kernel ForgeState.
	// Returning ForgeNone (or an error) clears the badge.
	Status(WorkspaceRef) (ForgeStatus, error)
	// Open opens the workspace's forge item (e.g. its PR) in a browser.
	Open(WorkspaceRef) error
}

// Set is the collection of active adapters, resolved from config at the
// composition root. A nil field means that capability is disabled and the
// kernel degrades gracefully.
type Set struct {
	Forge ForgeIntegration
	AI    AIIntegration
}

var active Set

// SetActive installs the resolved adapter set. Called once by the
// composition root (cmd/atelier) after reading config, before dispatch.
func SetActive(s Set) { active = s }

// Active returns the installed adapter set. Kernel code reads it to reach a
// port; fields may be nil (capability disabled) — callers must nil-check.
func Active() Set { return active }
