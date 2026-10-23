---
name: evidence
description: Produce the human evidence for a hoop ticket - the same real flow captured on origin/main and on the branch, composed into labeled before/after images (a side-by-side video for a UI or terminal flow), plus a one-page EVIDENCE.md. Use after QA passes and before a ticket goes to review, or when asked for evidence of a change.
---

# Evidence for a ticket

Evidence is what convinces a person in two minutes that the change works and
breaks nothing. A reader must be able to COMPARE: every image puts "before" and
"after" side by side, with a label on each panel. Model: "Testing" in PR #1937.

Deliverable, and nothing else, in `~/evidencias/<TICKET-ID>/`:

```
EVIDENCE.md
1-<question>.png      one comparison image per question, 2 to 4 in total
2-<question>.png
```

When the change has a flow, the comparison is a video of it instead: a UI
flow (model: ENG-596), or a terminal flow with each step explained on screen.
`<flow>.gif` for the PR and `<flow>.mp4` for Linear, 1 or 2 flows. A PNG stays
for a single state: one API response, one log line.

Everything else (raw captures, logs, snapshots, scripts, test output) goes to
`~/evidencias/.archive/<TICKET-ID>/`. Never inside the ticket folder.

## 1. Plan the captures (before you run anything)

For each `Definition of done` item, write one line:

| Question a reader asks | Surface a person meets | Capture |
|---|---|---|
| "Does the sidecar get the same rules?" | `GET /api/sidecars/configuration` | `curl \| jq` → PNG |
| "Does the admin screen change?" | rule edit page | browser screenshot |
| "Is the page fast, and does it show X?" | the page, used end to end | recorded flow → side-by-side GIF/MP4 |
| "Does the CLI tell the user why?" | `hoop exec`, then the session API | VHS tape → side-by-side GIF/MP4 |

Surfaces, best first: the UI; an API response; a sidecar, gateway or agent log
line; CLI output. Never an internal DB snapshot or `go test` output: a test is
proof for CI, not for a reader.

When the UI changes, the surface is the flow, recorded: a still hides what a
click does. When the ticket adds a view, show its layout to the human as soon
as it renders, before building its backend: the first look can remove it.

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
- No Docker: `scripts/dev/run-sandbox.sh start --bin <hoop>` for each side in
  turn, `stop` between them. Each start begins from an empty database, so run
  the seed script on both.
- QA's stacks, still running with QA's seed, are the cheapest place to record.
  Ask QA to leave them up. After you swap the UI assets under a running stack,
  restart its container: the gateway reads `index.html` once at boot.

## 3. Capture

- **Terminal / API / log:** record the flow with VHS: one tape (model:
  `examples/terminal.tape`), run on each side against that side's stack.
  - Before each command, type `# Step N: <what and why>`: it is the
    explanation the reader sees. Without it, a video of typing tells nothing.
  - Hide the setup (`Hide` … `Show`). End on the answer, held 4 s: the last
    frame must answer the question alone.
  - A single state with no steps (one response, one log line): a PNG
    (`freeze`, or the last frame of the tape).
- Keep only the lines that answer the question (`jq` filter, `grep`). The
  command and the commit on top of the panel say which version produced it.
- **UI:** record the flow. A screenshot only for a state that has no flow.
  - Write the steps once in a `<flow>.flow.mjs` (model:
    `.claude/skills/evidence/examples/access-control.flow.mjs`) and run it on each stack:
    `node .claude/skills/evidence/rec.mjs <baseURL> <tokenFile> <flow>.flow.mjs main|branch <outDir>`
  - Same seed, same steps, same start and end state. The middle differs only
    where the UI does (main scrolls a long list, the branch pages it).
  - Show what the page hides. `counters` in the flow adds a fixed "test
    overlay" with request counts; `ui.click` draws a ring where each click
    lands.
  - The two sides must differ on screen. When the UI action lands outside
    the browser (a CLI, a database client, an agent), run that client for
    real and mirror its output live in a "test overlay" panel (model:
    `examples/reject-live/`). Same clicks with one changed sentence is not
    a video worth recording.
  - Undo what the flow opened before its last step, so the last frame shows
    the answer.
  - Record on the final commit, then pull 3 or 4 frames
    (`ffmpeg -ss <s> -i x.mp4 -frames:v 1 f.png`) and look at them. A capture
    from an earlier QA round can show the bug you fixed after it.

## 4. Compose

```bash
.claude/skills/evidence/compare.sh <out.png> h \
  "BEFORE · origin/main=before.png" \
  "AFTER · branch=after.png" \
  "DURING DEPLOY · old gateway, new DB=old-replica.png"
```

`h` for screens and JSON (side by side), `v` for wide one-line logs (stacked).

A recorded flow (UI or terminal):

```bash
.claude/skills/evidence/compose-video.sh <out-basename> \
  "BEFORE · origin/main=main.mp4" "AFTER · branch=branch.mp4"
```

It writes `<out>.mp4` (full size, for Linear) and `<out>.gif` (1800 px, 8 fps,
about 2 MB for 30 s, for the PR). The shorter side holds its last frame.

Open every image, and the frames you pulled from a video, and check the labels
and the text are readable.

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

For a video, one section holds the GIF and a "What to look at" list. Each
bullet names a region of the screen (the counter bottom-left, the expanded
group, the filter) and what differs there.

## 6. Show, then publish

Show the EVIDENCE.md and the images to the user, in their language, before
publishing. In autonomous mode (`HOOP_AGENT` set) nobody is there to show:
publish, and the PR stays a draft until a person reads it. Then:
- Linear: attach `EVIDENCE.md` and the images to the ticket (the MP4 for a
  video).
- PR: the result line, the images (the GIF for a video) and "What it solves" under "Testing".
  Host the images in a secret gist and link the raw URLs. A cloud session
  cannot create gists (the GitHub API answers 403): there,
  `.claude/skills/evidence/publish.sh <TICKET-ID> <files>` pushes them to the
  `claude/evidence` branch and prints the links.
  The repo is public: no customer data, tokens, internal hosts or real emails
  in any image.

## Pitfalls already met

- Labels: `label.sh` draws them with ffmpeg `drawtext` when ffmpeg has it,
  else with `freeze`. Homebrew `ffmpeg` has no `drawtext`; `freeze` crashed
  (SIGSEGV) in a scheduled cloud run. `freeze` draws no emoji; render such
  output with Chromium instead.
- VHS in a cloud sandbox: it needs `ttyd` and a `chromium` in `PATH`, and runs
  as root only with `VHS_NO_SANDBOX=true`. The agent's setup script installs
  them (`.claude/README.md`); elsewhere, run the same lines.
- A UI flow in a cloud sandbox: `make build-dev-webapp` and `make run-dev`
  (or `scripts/dev/run-sandbox.sh start --ui`), then `rec.mjs` as usual: it
  finds Chromium. In our cloud runs Docker Hub answered 429, and Maven Central
  sent error pages that fail as "Checksum validation failed, expected Your".
  The agent's environment sets mirrors for both (`.claude/README.md`).
- `publish.sh` links the GIF for the PR. GitHub does not play an MP4 link:
  attach the MP4 to the Linear ticket.
- In zsh, `$VAR[...]` is an array subscript: write `${VAR}[...]` inside
  ffmpeg filters.
- Routes behind `EnterpriseLicenseOnly` (sidecar config) need the dev
  license: from `.env` locally, from `HOOPDEV_LICENSE` in a cloud session
  (`run-sandbox.sh` uploads it). Tests that call handlers directly skip it, a
  real stack does not.
- In control-plane mode the rule screen opens the sidecar form, which does not
  show connections; use gateway mode for that screen.
- `Page.captureScreenshot` of `about:blank` never answers. `rec.mjs` starts the
  frame loop at `ui.go(path, { record: true })` and times out each capture.
- `freeze`, `compare.sh` and `compose-video.sh` wait on stdin in a background
  shell: run them with `</dev/null`.
- This Homebrew `ffmpeg` has no `-vsync`: pull single frames with `-ss`.
  `ffprobe -of csv=p=0` can print a trailing comma: `| head -1 | tr -d ,`.
