# Security policy

## Reporting a vulnerability

Please report vulnerabilities privately, through the repository's **Security**
tab → **Report a vulnerability**. Don't open a public issue.

Include what you found, how to reproduce it, and the version (`atelier version`).
You'll get a response as soon as possible, and credit in the release notes if you
want it.

## Supported versions

Fixes land on the latest release only.

## Scope

atelier runs locally and acts with your credentials. It writes hooks into Claude
Code's and Codex's settings, runs Codex without its sandbox or approval prompts,
runs git and `gh` as you, and runs shell commands in tmux. Issues
that let untrusted input — a branch name, a PR title, a repository's contents —
run commands or reach those credentials are in scope.

Handoff exports contain conversation text and are private to the local owner:
the handoff directory uses mode `0700` and new export files use `0600`. A swap
also tightens the directory permissions left by an older version.
