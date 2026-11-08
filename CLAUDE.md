# CLAUDE.md — atelier

A workshop for parallel coding agents (Claude Code, Codex, and Antigravity CLI today; Grok Build
next): a terminal-based agentic development environment, where
reviewing a PR should be as easy as making the change by hand.
The design, its principles (`NFR-*`, cited in code), and the rules (§5) are in
DESIGN.md, imported below. User-facing behaviour is README.md; contributor
workflow is CONTRIBUTING.md — keep all three in step with the code.

@DESIGN.md

## Layout

- `internal/core` — domain types, the state file, config, paths, slug
- `internal/tmux` — tmux CLI wrapper (dedicated socket)
- `internal/git` — worktree derivation + git queries
- `internal/forge` — `gh` PR queries + open-in-browser
- `internal/agent` — the agents (Claude Code, Codex, Antigravity): launch/resume, finding
  and exporting their conversations for a handoff, per-session status, hooks,
  their config (trust + MCP registration), the per-workspace guide
- `internal/mcp` — stdio MCP server (`register_pr`, `create_worktree`, `create_pr`)
- `internal/ui` — the Bubble Tea overlay + the home splash (the only UI)
- `internal/gc` — deleting spaces (one, or all older than an age) and sweeping
  up what deleted spaces leave behind
- `cmd/atelier` — one binary: `up` / `open` / `home` / `create` / `swap` /
  `handoff` / `win` / `hook` / `usage` / `poll` / `mcp` / `install` / `ls` /
  `purge` / `gc` / `version`

## Model & bindings

- A workspace ("space") = a dir under `~/ateliers/<slug>/` + a tmux session whose
  `claude` window runs the agent — Claude Code, Codex, or Antigravity, `Workspace.Agent` (""
  = claude); the window name predates Codex (named windows: `claude`, `shell`,
  `<repo>-<branch>` per worktree — nav is keyed on names, so
  `automatic-rename`/`allow-rename` are off).
- Swap (`M-x`, or on a Spaces row): `atelier swap` respawns the agent window
  with `atelier handoff`, which exports the outgoing agent's conversation since
  `SwappedAt` to `.atelier/handoff/`, records the swap, then execs into the
  incoming agent at once — resuming its latest conversation in the space
  (derived from native transcripts; Antigravity uses the pointers described below), or starting one; a swap to
  the same agent (`Tab` cycles all choices in the confirm, `--to`) always starts one.
  A launch reservation covers the gap before the handoff owns the per-space
  lock; abandoned launches expire after 30 seconds. Exports are owner-only.
  Codex runs with `--dangerously-bypass-approvals-and-sandbox` and always a
  `-c` (without one it can attach to an app-server daemon lacking the space's
  env). Each agent's hooks are guarded on `$ATELIER_AGENT`.
- Allowances: `atelier usage` is tmux's asynchronous status job. It renders
  cached account quotas immediately and refetches stale readings
  (`UsageStale`: Codex 1 min, Claude 15 min — its `/usage` boots the CLI);
  a finished turn's hook refreshes that agent detached
  (`atelier usage refresh <agent>`), at most once a minute each (shared file
  locks; no model calls). Codex uses
  `account/rateLimits/read`; Claude uses native `claude -p /usage`, validating
  `local_command=usage` and zero turns/cost. Reset dates are parsed in UTC;
  unsupported output fails closed. Claude spaces also use local `statusLine`
  settings as a fallback to capture native JSON with
  `atelier usage claude`, chaining any existing custom command without editing
  global settings. Updating bindings backfills these settings so existing
  Claude sessions hot-reload the collector too. Cache files under
  `CacheDir()/usage/` hold quota metadata
  only, partitioned by agent config directory. Applicable native windows take
  precedence; captures can restore an expired window with a newer reset.
  `~` means last observed or stale; an
  expired window is unknown. Percentage spent uses green/amber/red based
  on spent quota; the icon retains its brand colour.
- Antigravity (`agy`, CLI 1.5.3): workspace `.agents/mcp_config.json` is merged
  without replacing other servers. `.atelier/antigravity-settings.json` is a
  `--settings` overlay for folder trust and a statusLine collector; global
  settings, model, effort, permissions, and an existing status command survive.
  The collector is specific to that launch, guarded by ATELIER_SESSION and
  ATELIER_AGENT, and maps native agent_state to working/idle/blocked. It captures
  quota.remaining_fraction and reset_time as last-observed usage, never token
  estimates. There is no background Google balance fetch. The TUI payload also
  provides conversation_id and transcript_path; `.atelier/antigravity/<id>.json`
  retains that pointer because native CLI history omits initial prompts and
  its summary database can omit workspace paths. Restore chooses the newest
  existing native transcript (preferring transcript_full.jsonl), and handoff
  exports explicit USER_INPUT / PLANNER_RESPONSE text and tool calls. No
  separate title generation: agy lacks a documented ephemeral, tool-free mode.
- Lifecycle: create → **active**; `M-d` delete → Trash (kill processes, keep on
  disk); Trash `↵` restore (resume); Trash `M-d` delete permanently (confirm).
  `Retired bool` + `RetiredAt` on the workspace (zero value = active, no migration).
  Both deletes go through `gc.Delete`. `atelier purge` deletes by idle age
  (`gc.LastActive`: created/swapped/retired/last hook), Trash only unless
  `--all`, skipping unsaved work and running sessions unless `--force`. Both
  caller and cwd spaces are excluded; the final re-read, safety checks, and
  deletion hold the state lock shared with restoration and agent launch.
  Restoring refreshes the status timestamp before changing the record.
  `atelier gc` drops records
  whose directory is gone (10-minute grace for a space being made), adopts
  record-less space dirs into Trash or active Spaces according to their session,
  recovers their agent from metadata/transcripts without a creation cutoff,
  kills sessions of retired spaces, and
  removes orphaned cache files, Claude project entries, and git worktree
  records; it refuses to run on an unreadable state and skips directory checks
  when the root is missing. Swap lock files are retained to preserve locking
  across concurrent openers. Unknown session-query results abort cleanup;
  failed stops are errors and leave the workspace on disk.
- Overlays: `M-s` spaces · `M-p` PRs · `M-w` worktrees · `M-t` trash · `M-n` new
  (background). `M-h` switches to the home splash (a session switch, not an
  overlay); the keymap, version, and dependency doctor live there — there is no
  Help screen. `M-p` and `M-w` are scoped to the current space (off one they
  toast and close). In-space: `M-a` agent · `M-x` swap agent · `M-c` shell.
  `M-n`: `Tab` picks the agent. `M-q` detaches. Leader is `M-`
  (Alt), hardcoded. New spaces build via a detached `atelier create`; feedback is
  a status-line spinner (`@atelier_spin`) that resolves into a check-mark.
- PR view: `↵` opens the diff as a window, `M-e` its checks, `M-b` the
  browser, `M-o` reopens or marks a draft ready, `M-c` closes (nothing converts
  back to draft), `M-y` copies a Markdown link. The diff is local `git diff <base>...HEAD` in the worktree on the PR's
  head branch in the PR's repo, after fetching the base (instant, offline, no
  size limit — GitHub's diff endpoint refuses past 20k lines); with no such
  worktree or no known base it falls back to `gh pr diff`. It is piped to
  whichever of `diffnav` · `delta` · `less` is installed, chosen by the shell that
  runs it rather than atelier's PATH; none is a dependency. Opening the diff
  (`↵` or `M-d`) when its window exists re-runs it there (`respawn-window`), so
  it never shows a stale changeset; the checks window is returned to as is.
  `M-e` opens the PR in
  gh-enhance in a window — as the `gh-enhance` binary if it's on PATH (a package
  manager install isn't registered with gh), else as `gh enhance` — falling back
  to `gh pr checks --watch`. The two windows swap in place: `M-e` in a `pr-*`
  window opens that PR's `ci-*` checks, `M-d` in a `ci-*` window its `pr-*` diff
  (`atelier win <session> checks|diff <window>`, which maps the window name back
  to its PR). Both bindings test the window name first and send the key through
  anywhere else (Alt-d is delete-word). `M-a` is the agent everywhere — never
  overload it, not even in a PR's windows.
- Agent status is event-driven: the per-session marker (`@atelier_status`) and the
  attention badge (`@atelier_attention`) are pushed by the agents' hooks (Codex's
  run only once the user trusts them). Entering
  a blocked space sets it idle, so the badge clears until the agent asks again.
  The hooks never refresh PRs. PR status
  is kept by tmux's status job `atelier poll '#{session_name}'` (status-interval
  5): one GraphQL query (worktree branches + registered PRs by number) for every
  due space — the focused one after 5s, the rest after a minute — stamped in
  `PRsRefreshed`. Sweeps take turns under `core.LockPRs`, reading state under
  it, and an `M-o`/`M-c` result is saved under it too, so an older answer never
  lands over a newer one. Merged PRs are never re-fetched: a branch whose
  cached PRs have all merged is settled and asked only for open PRs. The
  status jobs pin the state, config, cache, and root paths. The overlay never
  queries GitHub; it re-reads the cache every second, keeping the cursor on its
  PR. Worktree freshness is computed off the path that builds the model.
- PR rows name their base; stacked PRs (one's base is another's head) render as
  a contiguous run, base first, joined by a gutter line — derived per render,
  never stored. Rows are ordered open · draft · merged · closed, newest first; a
  stack takes its most active member's state. A PR in its base's merge queue
  (`isInMergeQueue`) swaps its state icon for the amber merge-queue Octicon; it
  still sorts as open.
- The generated tmux fragment advertises 24-bit colour (`terminal-features[100]
  "*:RGB"`) when `$COLORTERM` says the launching terminal has it.
  WezTerm also gets synchronized redraws (`sync`) for complete popup frames.
- Bindings are regenerated from the binary: `up`/`install` always write and
  source the fragment, and the popup and window keys (`open`, `win`) re-sync it
  whenever the server's `@atelier_bindings` marker (a hash the fragment sets
  when sourced) differs from the running binary's — so a rebuild or upgrade
  under a live server fixes itself on first use instead of leaving keys dead.
  Likewise `up` restarts an already-running splash (`respawn-pane`), since a
  running process keeps the binary it started with.

## Working here

- **Tests cover the headless core only** — the domain (state file, paths,
  config), agent status + the hook merges, finding and exporting agent
  conversations, worktree derivation, the PR query, and list rendering.
  Anything that shells out to tmux, `gh`, or an agent stays untested; don't add
  an abstraction to make it mockable.
  Run `mise run ci` before committing (the pinned suite includes race tests,
  vet, gosec, govulncheck, actionlint, zizmor and release checks). Run
  `mise run snapshot` and `mise run signing-test` when changing packaging.
  Keep `mise.lock` in sync with tool versions. Never execute project binaries,
  Go tests, mise tasks or GoReleaser hooks in the signing job. Keys belong only
  in their protected environments; see the guide before changing tag/App policy.
  Keep every action SHA-pinned,
  workflow tokens scoped, and scanner exceptions narrow with a reason beside
  them. Release setup and the security control inventory are in
  `docs/release-security.md`. Commits are Conventional Commits (release-please reads them).
- Delete dead code rather than keep it. Elegance and fitness-for-purpose over
  completeness or feature count.
