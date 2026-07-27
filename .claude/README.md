# Team Claude Code setup

This directory is versioned team infrastructure: every engineer's Claude Code
behaves the same way in this repo. Change it via PR like any code.

## What's here

| File | What it standardizes |
|---|---|
| `settings.json` | Pre-approved permissions (build/test/lint/git/gh read), `CGO_ENABLED=1`, deny rules for secrets, hooks |
| `hooks/gofmt.sh` | Auto-`gofmt` on every Go file Claude edits — deterministic, not a prose instruction |
| `skills/fix-ticket/` | `/fix-ticket EVL-86` — the standard ticket→draft-PR flow |
| `skills/test-plan/` | `/test-plan` — generates the mandatory "How to test" PR section |
| `../.mcp.json` | Linear + Figma MCP servers for everyone (first run: approve + `/mcp` to authenticate) |

## First-time setup (each engineer, once)

1. Pull, open `claude` in the repo, accept the workspace trust prompt.
2. Run `/mcp` and authenticate `linear` (and `figma` if you do frontend work).
3. That's it — skills, permissions and hooks are active.

## Parallel sessions with worktrees (the standard)

One ticket = one worktree = one terminal tab. Never work on two tickets in
the same checkout.

```bash
# new session on a ticket (creates .claude/worktrees/<name> on its own branch)
claude --worktree evl-86

# see all your running sessions (fleet view)
claude agents
```

Or with plain git: `git worktree add .worktrees/evl-86 -b <branchName>` then
run `claude` inside it.

### hoop-specific worktree notes

- **`make libhoop-map` is per-checkout**: the `libhoop` symlink is untracked,
  so a fresh worktree doesn't have it. The `test-*` targets run it for you;
  only run it manually if you need `go build` before any test.
- **`webapp_v2` needs its own `npm install`** in each worktree (node_modules
  is untracked).
- **Only one dev stack at a time**: `make run-dev` binds :8009/:8010 —
  parallel worktrees can build and unit-test freely, but only one can run the
  full gateway+agent stack. Coordinate or use different ports.
- Hygiene: worktree name = ticket ID; remove after merge
  (`git worktree remove <path>`, `git worktree prune` weekly).

Suggested daily loop: dispatch 3-4 tickets in the morning (one worktree +
`/fix-ticket` each), then rotate — validate one session's output while the
others run. You review and unblock; the agents wait, not you.

## Conventions the skills enforce

- Branch names come from Linear's `branchName` (auto-links the PR).
- Commits start with the ticket ID.
- Go: `make test-oss` green before PR. `webapp_v2`: `npm run lint` + build.
- Every PR is born **draft** with a "How to test" section.
- Linear ticket gets the PR link and moves to In Review.
