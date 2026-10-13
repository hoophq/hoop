# Team Claude Code setup

This directory is versioned team infrastructure: every engineer's Claude Code
behaves the same way in this repo. Change it via PR like any code.

## What's here

| File | What it standardizes |
|---|---|
| `settings.json` | Pre-approved permissions (build/test/lint/git/gh read), `CGO_ENABLED=1`, deny rules for secrets, hooks |
| `hooks/gofmt.sh` | Auto-`gofmt` on every Go file Claude edits — deterministic, not a prose instruction |
| `skills/shape/` | `/shape` — an idea or a Linear project becomes vertical tickets with a `Definition of done` |
| `skills/build-ticket/` | `/build-ticket EVL-86` — one ticket to a draft PR: code, tests, QA loop, evidence |
| `agents/qa.md` | `qa` — independent QA; judges the branch against the ticket only, never edits |
| `skills/evidence/` | `/evidence` — the same flow on `origin/main` and the branch, as before/after images |
| `../.mcp.json` | Linear + Figma MCP servers for everyone (first run: approve + `/mcp` to authenticate) |

## First-time setup (each engineer, once)

1. Pull, open `claude` in the repo, accept the workspace trust prompt.
2. Run `/mcp` and authenticate `linear` (and `figma` if you do frontend work).
3. For `/evidence`: `brew install ffmpeg vhs charmbracelet/tap/freeze`.
4. That's it — skills, agents, permissions and hooks are active.

A personal skill of the same name in `~/.claude/skills/` shadows the team one.
Delete the personal copy.

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

- **libhoop needs nothing per worktree**: it resolves from the Go module
  proxy (`GOPRIVATE` and credentials are set once per machine). Only
  `make libhoop-dev`, for a local clone, is per checkout.
- **`webapp_v2` needs its own `npm install`** in each worktree (node_modules
  is untracked).
- **One dev stack per slot**: `make run-dev` binds :8009/:8010. To run a
  stack in another worktree, use `HOOPDEV_SLOT` (see "Run more than one
  stack" in `DEV.md`).
- Hygiene: worktree name = ticket ID; remove after merge
  (`git worktree remove <path>`, `git worktree prune` weekly).

Suggested daily loop: `/shape` the work into tickets, dispatch 3-4 in the
morning (one worktree + `/build-ticket` each), then rotate — read one
session's evidence while the others run. You review and unblock; the agents
wait, not you.

## Conventions the skills enforce

- Branch names come from Linear's `branchName` (auto-links the PR).
- Commits start with the ticket ID.
- Go: `make test-oss` green before PR. `webapp_v2`: `npm run lint` + build.
- No PR before the `qa` agent says PASS.
- Every PR is born **draft**, its "Testing" section built from the evidence.
- Evidence lives in `~/evidencias/<TICKET-ID>/`, raw captures in
  `~/evidencias/.archive/<TICKET-ID>/`.
- Linear ticket gets the PR link and moves to In Review.
