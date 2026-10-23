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
| `skills/gcp-marketplace-publish/` | `/gcp-marketplace-publish` — a release of the sidecar to the GCP Marketplace listing, on demand |
| `skills/support-agent/` | `/support-agent` — one scheduled run of the support agent: one Linear support ticket per run |
| `hooks/ai-disclosure.sh` | With `HOOP_AGENT` set: refuses a Slack post or Linear comment that does not say an AI wrote it, and Slack posts outside the agent's channel |
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
- Go: `go test` on the changed packages before the first push; the full suite
  runs in the PR's CI. `webapp_v2`: `npm run lint` + build.
- The PR opens as a draft after those checks, so CI runs while the `qa` agent
  works. It leaves draft only after QA says PASS and CI is green.
- Every PR is born **draft**, its "Testing" section built from the evidence.
- Evidence lives in `~/evidencias/<TICKET-ID>/`, raw captures in
  `~/evidencias/.archive/<TICKET-ID>/`. PR images go to the `claude/evidence`
  branch through `skills/evidence/publish.sh`.
- Linear ticket gets the PR link and moves to In Review.

## Support agent

A Claude Code scheduled task (claude.ai/code/routines) runs `/support-agent`
every hour on one person's account. It takes one Linear ticket labeled
`Support` per run, and asks in Slack or Linear when blocked. A later run
resumes when someone answers. Its state lives in Linear labels: `agent:go`,
`agent:working`, `agent:needs-info`, `agent:needs-customer`, `agent:drafted`,
`agent:pr-open`, `agent:stuck`, `agent:skip`.

- Its cloud environment, used by nothing else, sets `HOOP_AGENT=support`,
  `HOOP_AGENT_OWNER`, `HOOP_AGENT_SLACK_CHANNEL` (channel ID),
  `HOOP_AGENT_MODE` (`spec`: answers and specs only; `build`: draft PRs) and
  `GOPRIVATE=github.com/hoophq/libhoop,github.com/hoophq/libhoop-ica`.
  Its setup script installs `freeze` for evidence, and a Maven Central mirror
  for the UI build (Maven Central rate-limits the sandbox's shared IP):

  ```bash
  GOBIN=/usr/local/bin go install github.com/charmbracelet/freeze@v0.2.2 || true
  mkdir -p ~/.shadow-cljs
  echo '{:maven {:repositories {"central" {:url "https://maven-central.storage-download.googleapis.com/maven2/"} "clojars" {:url "https://repo.clojars.org/"}}}}' > ~/.shadow-cljs/config.edn
  ```
- The task selects the repos `hoop`, `libhoop` and `libhoop-ica`, and only the
  Linear and Slack connectors. Hourly is the shortest schedule.
- A second task runs `/support-agent digest` at the end of the day.
- To stop it, pause the task. To ask for a ticket, label it `agent:go`.
