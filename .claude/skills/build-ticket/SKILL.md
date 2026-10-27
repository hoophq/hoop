---
name: build-ticket
description: Run a hoop Linear ticket end-to-end with independent QA - context, design note, code, tests, QA loop until PASS, draft PR with evidence. Use when given a ticket ID like EVL-86 or DEP-123. Handles bug, feature and spike tickets.
---

# Build a ticket

Input: a Linear ticket ID. Output: a draft PR that passed independent QA, with
evidence.

Write every Linear and GitHub field in English, whatever the conversation
language.

## Autonomous mode

When `HOOP_AGENT` is set, the `support-agent` skill started this run from a
schedule and no person reads the session. Then:

- A step that says show the human a result: attach it to the ticket and
  continue.
- A step that says stop, wait for or ask the human: ask as `support-agent`
  step 5 says, and end the run. A later run resumes from the ticket.
- A support ticket is shaped as a comment, never in its description. The
  `ai-disclosure` hook refuses description edits.
- The stack: the usual `make` targets, in this order in a cloud session:
  1. Start Docker: `pgrep dockerd || (dockerd >/tmp/dockerd.log 2>&1 &)`.
  2. Make the `.env`: `cp .env.sample .env`, `sed -i '/^AUTH_METHOD=/d' .env`,
     `echo AUTH_METHOD=local >> .env` (the first value of a key wins).
  3. `make run-dev-postgres`, then `make run-dev` in the background; wait for
     `/api/healthz`. The environment sets `HOOP_RS_BUILD=0` and `HOOPDEV_CA_CERT`.
  4. Register the admin and upload the Enterprise license, or the sidecar
     routes answer 403. The token goes to a file: shell variables do not
     survive between commands, and `rec.mjs` reads the same file.

     ```bash
     curl -s -D - -o /dev/null -H 'content-type: application/json' \
       -d '{"email":"admin@example.com","password":"Dev-Passw0rd","name":"Admin"}' \
       http://127.0.0.1:8009/api/localauth/register |
       sed -nE 's/^[Tt]oken: (.*)\r?$/\1/p' | tr -d '\r' > /tmp/hoop-dev-token
     curl -sf -X PUT http://127.0.0.1:8009/api/orgs/license -H 'content-type: application/json' \
       -H "authorization: Bearer $(cat /tmp/hoop-dev-token)" -d "$HOOPDEV_LICENSE"
     ```

  After `make build-dev-webapp`, `git checkout` the two `package-lock.json`
  unless the ticket changes them. Without Docker, `scripts/dev/run-sandbox.sh`
  (`--ui` for the UI) runs gateway and agent on PGlite, with no database for a
  connection to test against. It uploads the license itself.
- A sidecar: `make test-sidecar-e2e` runs here. By hand,
  `hoop start sidecar --config <file>` in front of the dev Postgres
  (`127.0.0.1:5449`); unlicensed it takes one guardrail and one mask rule,
  and `HOOP_LICENSE="$HOOPDEV_LICENSE"` lifts that. Managed by the gateway:
  `POST /api/sidecars` returns its `token`, then
  `HOOP_CONTROL_PLANE_URL=http://127.0.0.1:8009 hoop start sidecar --token <token>`.
  When a test fails here, read the same test in the PR's CI job before you
  chase it: a cloud sandbox can differ from the CI runner.
- Commits go in the owner's name (the environment sets `GIT_AUTHOR_*` and
  `GIT_COMMITTER_*`) and end with the `Co-Authored-By: Claude` and
  `Claude-Session:` lines: the CLA passes, and each commit still says an AI
  wrote it.
- The sandbox is discarded after the run: push the branch after each commit.
  The evidence is how a person knows the work is right: a PR without its
  comparison images in "Testing" is not done. Run the `evidence` skill (Skill
  tool): a flow, in the UI or in a terminal, is a video with its steps
  explained on screen. QA's captures are raw material:
  compose them (`evidence` step 4), then publish them with
  `.claude/skills/evidence/publish.sh` (`evidence` step 6). That is the upload
  route of a cloud session; a capture left in the sandbox is lost with it.
- `gh` can be missing: use the GitHub MCP tools.
- The PR description starts with `Opened by the support agent (AI) from <ID>.`
  It opens as a draft, so nobody reviews it before QA. When QA passed, its
  evidence is in "Testing" and CI is green, mark it ready for review
  (`update_pull_request`, `draft: false`): Qodo reviews it while this session
  listens. A person merges it.
- Once the draft PR exists, `subscribe_pr_activity` on it. Its CI results and
  review comments (Qodo once it is ready, reviewers) wake this session after
  the run ends. On each wake: fix a red check this PR caused and push; verify
  each review finding, then fix and push it or reply why not; reply on each
  thread you addressed and resolve it. Each reply starts with
  `Support agent (AI):`. Never merge it. Unsubscribe when it is merged or
  closed.

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
compose, write `EVIDENCE.md`), and show it to the user. Capture what is missing. For a UI or terminal flow, record it on the final
commit, against QA's stacks (evidence skill, step 3).
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
