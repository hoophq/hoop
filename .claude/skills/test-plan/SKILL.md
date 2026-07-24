---
name: test-plan
description: Generate the "How to test" section for the current branch's changes - exact commands, flows to exercise, expected results. Use before opening/updating a PR or when asked how to validate a change.
---

# Generate a test plan for the current changes

Goal: a teammate (or a QA agent) must be able to validate this change without
asking anything. This is the team's standard for every PR.

## Steps

1. Diff the branch against main: `git diff main...HEAD --stat` then read the
   changed files' diffs.
2. Identify what is observable from outside: which API response, CLI
   behavior (`hoop connect` / `hoop exec` / proxy flow), gateway/agent
   interaction, or `webapp_v2` screen changed.
3. Write a `## How to test` section in markdown:
   - **Setup**: exact commands — see `DEV.md` (`make run-dev`, Postgres via
     `make run-dev-postgres`, agent, `webapp_v2`: `npm run dev`). Only what
     this change needs.
   - **Steps**: numbered, copy-pasteable commands or precise UI actions.
   - **Expected**: the observable result per step — literal output where
     possible (API status + body fields, CLI output, what the screen shows).
   - **Regression check**: 1-2 quick checks that the closest existing
     behavior still works.
4. Cover the negative path too (permission denied, invalid input, guardrail
   still blocking after a fix).
5. If a step cannot be validated locally (needs enterprise libhoop, IDP,
   cloud resources), say so explicitly and state what environment is needed —
   never leave silent gaps.

Output the section ready to paste into the PR body (or update the open PR
with `gh pr edit --body-file` if asked).
