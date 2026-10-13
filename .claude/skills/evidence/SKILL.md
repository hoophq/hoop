---
name: evidence
description: Produce the human evidence for a hoop ticket - the same real flow captured on origin/main and on the branch, composed into labeled before/after images, plus a one-page EVIDENCE.md. Use after QA passes and before a ticket goes to review, or when asked for evidence of a change.
---

# Evidence for a ticket

Evidence is what convinces a person in two minutes that the change works and
breaks nothing. A reader must be able to COMPARE: every image puts "before" and
"after" side by side, with a label on each panel. Model: "How to test" in PR
#1937.

Deliverable, and nothing else, in `~/evidencias/<TICKET-ID>/`:

```
EVIDENCE.md
1-<question>.png      one comparison image per question, 2 to 4 in total
2-<question>.png
```

Everything else (raw captures, logs, snapshots, scripts, test output) goes to
`~/evidencias/.archive/<TICKET-ID>/`. Never inside the ticket folder.

## 1. Plan the captures (before you run anything)

For each `Definition of done` item, write one line:

| Question a reader asks | Surface a person meets | Capture |
|---|---|---|
| "Does the sidecar get the same rules?" | `GET /api/sidecars/configuration` | `curl \| jq` → PNG |
| "Does the admin screen change?" | rule edit page | browser screenshot |

Surfaces, best first: the UI; an API response; a sidecar, gateway or agent log
line; CLI output. Never an internal DB snapshot or `go test` output: a test is
proof for CI, not for a reader.

Add a third column, **DURING DEPLOY** (`origin/main` code reading the database
the branch migrated), when the ticket has a migration, a wire/packet change, or
a sidecar config change. Gateways auto-deploy and roll; that is where things
break.

## 2. Run the same flow twice

- `origin/main` in one stack, the branch in another (`HOOPDEV_SLOT`, see
  `DEV.md`), the SAME seed data, created through the API the way an admin
  would. Save the seed script to the archive.
- Same steps, same viewport, same zoom, same `jq` filter on both sides.
- One process per port and per database at a time: stop the old process and
  wait for it to exit before starting the next. A health check can answer
  from the previous process. If a run overlapped, discard it and redo it from
  an empty database.

## 3. Capture

- **Terminal / API / log:** render the final state as a PNG (`freeze`, or the
  last frame of a VHS tape). A GIF of someone typing proves nothing; use a GIF
  only when the steps themselves are the evidence (a UI flow), and then compose
  before/after GIFs side by side too.
- Keep only the lines that answer the question (`jq` filter, `grep`). The
  command and the commit on top of the panel say which version produced it.
- **UI:** a screenshot of the state, both versions at the same size.

## 4. Compose

```bash
.claude/skills/evidence/compare.sh <out.png> h \
  "BEFORE · origin/main=before.png" \
  "AFTER · branch=after.png" \
  "DURING DEPLOY · old gateway, new DB=old-replica.png"
```

`h` for screens and JSON (side by side), `v` for wide one-line logs (stacked).
Open every image and check the labels and the text are readable.

## 5. Write EVIDENCE.md (English)

```markdown
# <TICKET-ID> · <what changes, in words>

PR #<n> · tested on `<sha>` against `origin/main` `<sha>` · <date>

**Result: <one sentence a manager understands>.**

## What changes, and why it is risky
<2-3 lines. Name the failure a customer would see if this went wrong.>

## 1. <question>
![...](1-<question>.png)
<1-2 lines: what to look at in the image.>

## 2. <question> ...

## What QA caught on the way
<the bugs found and fixed; the test that now guards each one>

## What it solves
<1-2 lines, for whom. If "nothing visible changes" is the goal, say so and
name the ticket where the user-visible part arrives.>

## Open items
<every gap, plainly: what was not exercised, commits after the last QA round,
flaky checks. Never hide one.>
```

Rules: the result comes first; one screen of text; each image answers one
question; no file the reader has to open by hand.

## 6. Show, then publish

Show the EVIDENCE.md and the images to the user, in their language, before
publishing. Then:
- Linear: attach `EVIDENCE.md` and the images to the ticket.
- PR: the result line, the images and "What it solves" under "How to test".
  Host the images in a secret gist and link the raw URLs.
  The repo is public: no customer data, tokens, internal hosts or real emails
  in any image.

## Pitfalls already met

- Homebrew `ffmpeg` has no `drawtext`; `compare.sh` renders labels with
  `freeze` instead.
- In zsh, `$VAR[...]` is an array subscript: write `${VAR}[...]` inside
  ffmpeg filters.
- Routes behind `EnterpriseLicenseOnly` (sidecar config) need the dev
  license from `.env`; tests that call handlers directly skip it, a real stack
  does not.
- In control-plane mode the rule screen opens the sidecar form, which does not
  show connections; use gateway mode for that screen.
