---
name: build-ticket
description: Run a hoop Linear ticket end-to-end with independent QA - context, design note, code, tests, QA loop until PASS, draft PR with evidence. Use when given a ticket ID like EVL-86 or DEP-123. Handles bug, feature and spike tickets.
---

# Build a ticket

Input: a Linear ticket ID. Output: a draft PR that passed independent QA, with
evidence.

Write every Linear and GitHub field in English, whatever the conversation
language.

## 1. Context

Fetch the ticket through the Linear MCP server: title, description, comments,
`branchName`, and the template blocks. If Linear is not connected, ask the human
to run `/mcp` — never guess the ticket content.

Read its `Type`:

- **bug** — go to step 3.
- **feature** or **spike** — if there is no `Definition of done`, stop and say
  so. Do not invent the contract. Run `/shape` on it instead.
- **no `Type`** (not written by `/shape`) — a defect report is a `bug`. Anything
  else: run `/shape` on it first.
- A support ticket (`[SUP]`, label `Support`) is shaped in place: write the
  template into that ticket and build it as one PR. Never split it into new
  tickets.

Identify the modules. For a frontend ticket with a Figma link, pull the design
context before writing UI code.

## 2. Investigate (feature and spike only)

Answer every `Open question` from the code, not from memory. Then post ONE
comment on the Linear ticket with the design note:

- the approach, in a few lines,
- the files it touches,
- gateway↔agent compatibility: what an old agent does with this,
- feature flag, migration, env var, analytics event: what changes,
- what you considered and dropped.

A feature that adds a view: show the human its layout (a screenshot of the
first render; fake data is fine) before building its backend.

If `Checkpoint` is `approve design before code`, stop here and wait for the
human. Otherwise continue.

## 3. Branch

One worktree per ticket. Branch from up-to-date `main`, using Linear's
`branchName` so Linear links the PR. A `claude --worktree` session starts on a
branch of its own: switch to `branchName` before the first commit. Commits start
with the ticket ID: `EVL-86: <summary>`.

## 4. Implement

The smallest correct change that satisfies the ticket. Follow the repo
`CLAUDE.md`: route registration and middleware order for the gateway API,
`processPacket()` dispatch for the agent, packet type constants never raw
strings, `common/log` never `fmt.Println`, models take `*gorm.DB` and propagate
`gorm.ErrRecordNotFound`.

Honour the ticket's `Constraints`. Do not ask about the feature flag: the ticket
answered it.

Do not widen the change. If the correct fix needs something the ticket did not
name, stop and report what is missing.

## 5. Validate

- Go: `go vet` and `go test` on the packages you changed, and on the packages
  that import a changed exported API. Add `-race` when the change touches
  goroutines, locks or channels. Do not run `make test-oss` here: the PR's CI
  runs the full suite (unit, sidecar modules, integration, smoke).
- `webapp_v2/`: `npm run lint` and `npm run build`
- New endpoint or schema: `make generate-openapi-docs`
- Migration: test `down 1`

Then push the branch and open the PR as a draft, title `<ID>: <summary>`
(`gh pr create --draft`), so CI runs while QA works. Step 7 fills it.

## 6. Independent QA

Call the `qa` subagent. Give it only the ticket ID and the branch. **Never pass
it your plan, your reasoning, or what you believe is done** — its value is that
it judges against the ticket, not against you.

On `FAIL`: fix each finding, commit, then resume the SAME qa agent and ask it to
re-check, so it remembers what it already raised.

After 3 `FAIL` rounds: stop. Leave the PR as a draft. Report the open findings
and what you think the real blocker is.

On `PASS`: run the `evidence` skill from QA's last-round captures (steps 4 and 5:
compose, write `EVIDENCE.md`), and show it to the user. Capture what is missing. For a UI ticket, record the flow on the final commit,
against QA's stacks (evidence skill, step 3).
Continue only after the user has seen it. Step 7 publishes it.

For a `spike`, there is no PR. Deliver the evidence and a proposed cut into
vertical tickets, then stop.

## 7. Fill the PR

Fill every section of `.github/PULL_REQUEST_TEMPLATE.md` in the draft from
step 5. Put:

- **Description**: what and why, 2-6 lines.
- **Related Issue**: the ticket ID only, never a Linear URL.
- **Testing**: the result line, the comparison images and "What it solves" from
  `EVIDENCE.md` (no customer data, tokens or internal hosts: the repo is
  public), the `Definition of done` as a checklist, the tests that guard it,
  and exact commands so a teammate can re-run it without asking anything.
- **Additional Notes**: the risks, or "low".

Then write the PR number into `EVIDENCE.md` and attach it to the ticket
(`evidence` step 6).

Call out explicitly, when true: a change an old agent cannot process (`major`), a
removed or renamed analytics event with its dashboard and successor, a new env
var with the helm chart change.

Add exactly one release label: `major`, `minor`, `patch` or `skip-release`.

CI must be green before step 8: a red check goes back to step 4.

## 8. Close the loop

- Comment the PR URL on the Linear ticket and move it to **In Review**.
- Report back here: PR link, QA verdict, evidence paths, anything risky.

## Notes

- Evidence: `~/evidencias/<TICKET-ID>/` holds only `EVIDENCE.md` and the
  comparison images; raw captures live in `~/evidencias/.archive/<TICKET-ID>/`.
  See the `evidence` skill.
- Parallel stacks: give each worktree a slot, `HOOPDEV_SLOT=N make run-dev`, with
  that slot's `API_URL`, `GRPC_URL` and its own database in the worktree `.env`.
  See "Run more than one stack" in `DEV.md`.
- `webapp_v2` needs its own `npm install` per worktree.
