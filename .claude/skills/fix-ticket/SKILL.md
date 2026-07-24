---
name: fix-ticket
description: Implement a Linear ticket end-to-end - fetch context, branch, code, tests, draft PR, update the ticket. Use when given a ticket ID like EVL-86 or DEP-123.
---

# Fix a Linear ticket

Input: a Linear ticket ID. Follow every step — this encodes the team's
standard flow.

## 1. Context

- Fetch the full ticket via the Linear MCP server (title, description,
  comments, `branchName`, acceptance criteria). If Linear MCP is not
  connected, ask the user to run `/mcp` to authenticate — do not guess the
  ticket content.
- Identify which module the change belongs to: `gateway/`, `agent/`,
  `client/`, `common/`, `tunnel/`, or `webapp_v2/` (the legacy `webapp/` is
  frozen — only touch it if the ticket says so explicitly).
- For frontend tickets with a Figma link, pull the design context via the
  Figma MCP before writing UI code.
- If the ticket has no clear acceptance criteria and the change is not
  obvious, post ONE clarifying comment on the ticket (or ask the user) before
  writing code.

## 2. Branch

- Prefer a dedicated worktree per ticket (`claude --worktree <ticket-id>` —
  see `.claude/README.md` for hoop-specific worktree notes).
- Create the branch using Linear's `branchName` so Linear auto-links the PR.
  Base it on up-to-date `main`.

## 3. Implement

- Smallest correct change that satisfies the ticket. Follow `CLAUDE.md`
  (route registration/middleware order for gateway API, packet dispatch
  patterns for agent, module conventions).
- Commit messages start with the ticket ID: `EVL-86: <summary>`.

## 4. Validate

- Go changes: `make test-oss` (it runs `libhoop-map` + `generate-wasm`
  itself). Fix failures caused by your change; never skip or weaken tests.
- `webapp_v2/` changes: `npm run lint` and `npm run build` must pass.
- New gateway endpoints/migrations: check `migration-check` /
  `breaking-change-check` CI expectations before pushing.

## 5. Draft PR

Open with `gh pr create --draft`. Title: `<ID>: <summary>`.

**Body: fill the repository template** — read `.github/PULL_REQUEST_TEMPLATE.md`
and produce the body with its exact sections (`gh pr create --body` bypasses
GitHub's auto-template, so you must fill it yourself). Map content like this:

- **📝 Description** — what changed and why, plus the Linear ticket URL.
- **📣 User-facing impact** — one plain-language sentence, or "None".
- **🔗 Related Issue** — the Linear URL (leave `Fixes #` alone unless there is
  a GitHub issue).
- **🚀 Type of Change** — mark exactly the ones that apply.
- **📋 Changes Made** — bullet list of the main changes.
- **🧪 Testing** — exact commands + steps + expected results, written so a
  teammate can validate the flow without asking anything. Fill Test
  Configuration and check the boxes that are true (never check what you did
  not run).
- **✅ Checklist / 📄 Additional Notes** — check honestly; put risks and
  review-focus points in Additional Notes.

**Release label (merge is blocked without exactly one):** add it on create,
e.g. `gh pr create --draft --label patch …`. Choose: `patch` = bug fix,
`minor` = new feature/UI addition, `major` = breaking change,
`skip-release` = docs/CI/refactor/chore. If unsure, say so in the PR and pick
the more conservative option.

## 6. Close the loop

- Comment the PR URL on the Linear ticket and move it to **In Review** via
  the Linear MCP.
- Report back: PR link, test results, and anything you flagged as risky.
