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

import "time"

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

// CIStatus is the kernel's normalized continuous-integration verdict for a PR.
// The KERNEL owns the glyph/color (ciGlyphs); adapters classify.
type CIStatus string

const (
	CINone    CIStatus = ""        // no CI configured / no runs
	CIPass    CIStatus = "pass"    // all required checks green
	CIFail    CIStatus = "fail"    // at least one required check failing
	CIPending CIStatus = "pending" // checks still running
)

// ReviewDecision is the kernel's normalized code-review verdict for a PR.
// The KERNEL owns the glyph/color (reviewGlyphs); adapters classify.
type ReviewDecision string

const (
	ReviewNone             ReviewDecision = ""                  // no review activity
	ReviewApproved         ReviewDecision = "approved"          // approved
	ReviewChangesRequested ReviewDecision = "changes_requested" // changes requested
	ReviewRequired         ReviewDecision = "review_required"   // review requested, pending
)

// PullRequest is the rich per-PR record a ForgeIntegration reports. It replaces
// the single-enum ForgeStatus: the M-c "List Changes" view renders every field,
// and the M-s rollup counts PRs + derives per-workspace attention from CI/review
// state. Adapters populate it; the KERNEL owns all rendering (glyphs, columns).
type PullRequest struct {
	Number         int
	Repo           string // "owner/repo"
	Title          string
	State          ForgeState
	CI             CIStatus
	ReviewDecision ReviewDecision
	Comments       int
	URL            string
	Branch         string
	UpdatedAt      time.Time
}

// NeedsAttention reports whether a PR wants the user's eyes — failing CI or a
// changes-requested review on an open PR. This is the PR half of the M-s
// attention rollup (the drawing's <N-ATTENTION>). Pure.
func (p PullRequest) NeedsAttention() bool {
	if p.State != ForgeOpen && p.State != ForgeDraft {
		return false
	}
	return p.CI == CIFail || p.ReviewDecision == ReviewChangesRequested
}

// forgeGlyphs is the kernel-owned glyph + 256-color palette index for each
// renderable forge state. Single source of truth so the picker badge (ANSI)
// and the status-line segment (tmux #[fg=colourN]) render identically. Uses
// the Codicon git-pull-request family — centered on the monospace baseline
// (Octicons render low in some fonts) — with a DISTINCT glyph AND color per
// state: open=pull-request/green, draft=pull-request-draft/grey,
// merged=git-merge/purple, closed=pull-request-closed/red. ForgeNone/unknown
// has no entry — the slot is simply absent.
var forgeGlyphs = map[ForgeState]struct{ Glyph, Color string }{
	ForgeOpen:   {"\uea64", "35"},  // codicon git-pull-request, green
	ForgeDraft:  {"\uebdb", "244"}, // codicon git-pull-request-draft, grey
	ForgeMerged: {"\ueafe", "141"}, // codicon git-merge, purple
	ForgeClosed: {"\uebda", "203"}, // codicon git-pull-request-closed, red
}

// ForgeGlyph returns the Nerd Font glyph and 256-color palette index for a
// forge state. ok is false for ForgeNone or any unknown state, which callers
// render as an absent badge. Pure; the KERNEL owns this mapping (adapters
// classify state, they never render).
func ForgeGlyph(state ForgeState) (glyph, color string, ok bool) {
	spec, ok := forgeGlyphs[state]
	return spec.Glyph, spec.Color, ok
}

// ciGlyphs is the kernel-owned glyph + color for each CI verdict, using the
// Codicon check/x/sync family: pass=check/green, fail=x/red, pending=sync/yellow.
var ciGlyphs = map[CIStatus]struct{ Glyph, Color string }{
	CIPass:    {"", "35"},  // codicon check, green
	CIFail:    {"", "203"}, // codicon error, red
	CIPending: {"", "221"}, // codicon sync, yellow
}

// CIGlyph returns the glyph + 256-color index for a CI verdict. ok=false for
// CINone/unknown (absent column). Pure; KERNEL-owned.
func CIGlyph(ci CIStatus) (glyph, color string, ok bool) {
	spec, ok := ciGlyphs[ci]
	return spec.Glyph, spec.Color, ok
}

// reviewGlyphs is the kernel-owned glyph + color for each review verdict:
// approved=check-all/green, changes_requested=request-changes/red,
// review_required=comment/grey.
var reviewGlyphs = map[ReviewDecision]struct{ Glyph, Color string }{
	ReviewApproved:         {"", "35"},  // codicon verified, green
	ReviewChangesRequested: {"", "203"}, // codicon request-changes, red
	ReviewRequired:         {"", "244"}, // codicon comment, grey
}

// ReviewGlyph returns the glyph + 256-color index for a review verdict.
// ok=false for ReviewNone/unknown (absent column). Pure; KERNEL-owned.
func ReviewGlyph(r ReviewDecision) (glyph, color string, ok bool) {
	spec, ok := reviewGlyphs[r]
	return spec.Glyph, spec.Color, ok
}

// ForgeIntegration is the port a code-forge adapter (GitHub, GitLab, …)
// satisfies to feed the workspace pickers with pull-request data and act on it.
// The kernel owns all presentation (glyphs, columns, sort order), caching, and
// refresh cadence; the adapter only lists, opens, and (opt-in) closes.
//
// The port is repo-oriented, not window-oriented: List is batched per repo (one
// query, not one-per-window) so it survives workspace × repos × PRs without
// hitting the forge's rate limits. The kernel associates the returned PRs with
// workspaces by matching worktree branches.
type ForgeIntegration interface {
	// Name is the adapter's identifier (e.g. "github"). Used in diagnostics.
	Name() string
	// List returns the pull requests for the repo checked out at repoPath, in
	// ONE batched query. Best-effort: any absence (no gh, network failure,
	// unparseable output) returns (nil, nil) — the Changes view degrades to
	// "no PRs" rather than breaking.
	List(repoPath string) ([]PullRequest, error)
	// Open opens the given pull request in a browser.
	Open(pr PullRequest) error
	// Close closes a pull request WITHOUT merging — the first mutating forge
	// operation. Call sites gate it behind [forge] allow_write + a confirm
	// step. An adapter that doesn't support writes returns an error.
	Close(pr PullRequest) error
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
