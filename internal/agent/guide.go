package agent

// workspaceGuide is the default per-workspace CLAUDE.md. It is written once to
// the central WorkspaceGuidePath and symlinked into every workspace; the user
// owns it thereafter. Kept short and imperative so the agent actually follows it.
const workspaceGuide = "# Working inside an atelier workspace\n" + `
You are Claude Code running in an **atelier workspace**: a dedicated scratch
directory for ONE task. Your operator runs many of these in parallel and
switches between them. This directory is yours alone, it starts empty, and it is
NOT a repository — you do your work in git worktrees you create here.

## Rules (authoritative)

1. **Never base your work on the operator's ` + "`~/code`" + ` clones.** Those checkouts
   are stale — behind ` + "`origin`" + ` and often mid-edit — so reading them to learn
   how the code works builds your change on outdated code. Never read them for
   reference and never edit them in place.

2. **For every repo you touch OR consult, ` + "`create_worktree`" + ` first — not raw
   ` + "`git worktree`" + ` or ` + "`git clone`" + `.** It branches a fresh worktree off the
   repository's freshly-fetched *default branch* and places it under this
   workspace. Read and write only there — it is your one source of current truth,
   for the repo you're changing and for any repo you reference. Pass the source
   repo's absolute path (under ` + "`~/code`" + `) and a new branch name.

3. **Stay current through the task.** ` + "`origin`" + ` moves while you work. Before you
   commit to an approach or open a PR, re-fetch and confirm the default branch
   hasn't moved under you (` + "`git fetch origin <default>`" + `, then check
   ` + "`origin/<default>`" + `); if it has, re-verify your changes still hold.

4. **Open pull requests with the ` + "`create_pr`" + ` MCP tool.** It rebases your branch
   onto the latest default branch and opens a **draft** PR on the operator's
   behalf. If it reports rebase conflicts, resolve them and call it again; if it
   reports the base advanced, re-check your work against the new commits.

5. If you ever open a PR another way, call ` + "`register_pr`" + ` so it shows up in
   atelier's Changes view.

6. Keep everything under this workspace directory. Anything created outside it is
   invisible to atelier and to your operator.

Work only in fresh worktrees under this directory. That is the whole contract.
`
