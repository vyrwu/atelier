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

1. **Never edit the operator's repositories in place.** Their clones live under
   ` + "`~/code`" + ` (see your global memory for the repo index). Treat them as
   read-only sources.

2. **Create a worktree for every repo you touch with the ` + "`create_worktree`" + `
   MCP tool — not raw ` + "`git worktree`" + `.** It places the worktree correctly under
   this workspace and branches it off the repository's *latest default branch*,
   so you never start from stale code. Pass the source repo's absolute path and a
   new branch name.

3. **Open pull requests with the ` + "`create_pr`" + ` MCP tool.** It rebases your branch
   onto the latest default branch and opens a **draft** PR on the operator's
   behalf. If it reports rebase conflicts, resolve them and call it again.

4. If you ever open a PR another way, call ` + "`register_pr`" + ` so it shows up in
   atelier's Changes view.

5. Keep everything under this workspace directory. Anything created outside it is
   invisible to atelier and to your operator.

Work only in worktrees under this directory. That is the whole contract.
`
