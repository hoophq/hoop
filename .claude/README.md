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
  `~/evidencias/.archive/<TICKET-ID>/`. From a cloud session, where gists are
  blocked (the GitHub API answers 403), PR images go to the `claude/evidence`
  branch through `skills/evidence/publish.sh`.
- Linear ticket gets the PR link and moves to In Review.

## Support agent

To run it, set up a Claude Code scheduled task (claude.ai/code/routines) on one
person's account that runs `/support-agent` hourly. Each run takes one Linear
ticket labeled `Support`, and asks in Slack or Linear when blocked. A later run
resumes when someone answers. Its state lives in Linear labels: `agent:go`,
`agent:working`, `agent:needs-info`, `agent:needs-customer`, `agent:drafted`,
`agent:pr-open`, `agent:released`, `agent:stuck`, `agent:skip`. When the
version that ships its PR is out, the agent comments it on the ticket.

- Give the task its own cloud environment and use it for nothing else:
  `HOOP_AGENT` turns on the `ai-disclosure` hook in every session there. It sets
  `HOOP_AGENT=support`,
  `HOOP_AGENT_OWNER`, `HOOP_AGENT_SLACK_CHANNEL` (channel ID),
  `HOOP_AGENT_MODE` (`spec`: answers and specs only; `build`: PRs),
  `GOPRIVATE=github.com/hoophq/libhoop,github.com/hoophq/libhoop-ica`,
  `HOOP_RS_BUILD=0`, `HOOPDEV_CA_CERT=/root/.ccr/ca-bundle.crt` (the
  sandbox re-signs TLS; the dev image must trust it) and `HOOPDEV_LICENSE`:
  an Enterprise license as one line of JSON (`jq -c`), for the sidecar
  routes. Not `HOOP_LICENSE`: every sidecar reads that one by itself, so a
  standalone run would never meet the free-tier caps. The gateway accepts any
  license host on localhost. Sign it with
  `POST /api/orgs/license/sign`; `"expire_at": "17520h"` is 730 days.
  `GIT_AUTHOR_NAME`, `GIT_AUTHOR_EMAIL`, `GIT_COMMITTER_NAME` and
  `GIT_COMMITTER_EMAIL` hold the owner's name and the email of their GitHub
  account: the CLA check reads the commit author, and a cloud session
  otherwise commits as `Claude`, which the CLA refuses.
  Its setup script installs the evidence tools (`freeze`, VHS with `ttyd`, and
  Chromium as `chromium` in `PATH`), sets mirrors (in our cloud
  runs Docker Hub answered 429, and Maven Central sent error pages), and pulls
  the dev images into the environment cache. The pulls only fill the cache, so
  they may fail: a setup script that exits non-zero stops the session, and
  `make run-dev` pulls what is missing and fails loudly there.

  ```bash
  GOBIN=/usr/local/bin go install github.com/charmbracelet/freeze@v0.2.2 || true
  GOBIN=/usr/local/bin go install github.com/charmbracelet/vhs@v0.10.0 || true
  curl -fsSL -o /usr/local/bin/ttyd https://github.com/tsl0922/ttyd/releases/download/1.7.7/ttyd.x86_64 &&
    chmod +x /usr/local/bin/ttyd || true
  ln -sf /opt/pw-browsers/chromium /usr/local/bin/chromium
  mkdir -p ~/.shadow-cljs /etc/docker
  echo '{:maven {:repositories {"central" {:url "https://maven-central.storage-download.googleapis.com/maven2/"} "clojars" {:url "https://repo.clojars.org/"}}}}' > ~/.shadow-cljs/config.edn
  echo '{"registry-mirrors": ["https://mirror.gcr.io"]}' > /etc/docker/daemon.json
  (dockerd >/tmp/dockerd.log 2>&1 &)
  for i in $(seq 1 30); do docker info >/dev/null 2>&1 && break; sleep 1; done
  docker pull postgres:16 || true
  docker pull hoophq/agent-tools:noble-20260709-6890906 || true
  pkill dockerd || true
  ```
- The task selects the repos `hoop`, `libhoop` and `libhoop-ica`: pushes and
  PRs use that GitHub access. Its only connectors are Linear and Slack. Hourly
  is the shortest schedule.
- A second task runs `/support-agent digest` at the end of the day.
- To stop it, pause the task. To ask for a ticket, label it `agent:go`.
