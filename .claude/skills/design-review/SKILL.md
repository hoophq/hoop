---
name: design-review
description: Review web app or terminal (CLI) work AFTER it is built, against Hoop's design rules, and return clear issues with fixes. Use when asked for a design review, a UI review, a CLI or terminal output review, "does this look right", or "check this against the design system" - on a PR number, a branch, a diff, or a screenshot. Covers webapp_v2 screens and components (React, Mantine) and the hoop CLI (help, prompts, errors, colors, tables, TUI). No design context needed. To build UI, use hoop-ui instead.
---

# Design review

Two skills, one set of rules:

- `hoop-ui` is **before**: an agent loads it to build UI.
- `design-review` is **after**: it checks built work against the same rules.

The rules live in `hoop-ui` and in the brand skills (`design-webapp-md`,
`design-md`). This skill does not copy them. It says which one wins, how to
review, how severe each break is, and how to show it.

Input: a PR number, a branch, a diff, or a screenshot of the web app or the
terminal. Output: a list of issues. Each issue says where it is, which rule
it breaks, and the fix. A reader with no design background can apply every
fix.

You review. You do not edit files, and you do not post comments on GitHub or
Linear unless the person asks.

## 1. Get the work

| Input | Do |
| --- | --- |
| PR number | `gh pr diff <N>` and `gh pr view <N> --json title,body,files` |
| Branch | `git diff origin/main...<branch>` |
| No input | `git diff origin/main...HEAD`, plus uncommitted changes |
| Screenshot | Read the image, then follow "A screenshot" below. |

The rules must be there before you review. If
`.claude/skills/hoop-ui/references/webapp.md` or `terminal.md` does not
exist in this checkout, stop. Say "The hoop-ui rules are missing, so I
cannot review" and do not review from memory.

Then find the scope and read the rules:

| Files changed | Rules (read all of it) |
| --- | --- |
| `webapp_v2/src/` | `.claude/skills/hoop-ui/references/webapp.md`, and the repo files its section 1 names |
| `client/` output a person reads (`fmt.Print*`, `styles.`, `lipgloss`, cobra `Short`/`Long`/flag help, `sidecartui/`) | `.claude/skills/hoop-ui/references/terminal.md` |
| Text a person reads in another place: the feature flag `Description` in `common/featureflag/featureflag.go` (shown at Settings, Experimental) | `webapp.md`, 8. Copy: the same words as the web app and the CLI |
| Both | Both |
| Neither (only Go logic, tests, docs) | Say "No design surface in this change" and stop. |

A merged PR: its code may already be in this checkout. Check with
`git diff <merge commit> HEAD -- <its files>`. When that is empty, build and
run from here, with no separate folder.

Then load the brand skills, when this session has them (they come with the
Hoop account, not with the repo):

| Skill | Use it for |
| --- | --- |
| `design-webapp-md` | Web app screens: the screen types, the rules that never change, its checks |
| `design-md` | The brand: palette meaning, voice and copy, the bar and the pill |

For the terminal, use the brand skills for voice and copy only. They have
no terminal rules: `hoop-ui` terminal.md decides color, layout and
symbols.

Do not copy their rules into a report. Cite them like any other source. When
a session does not have them, write "Brand skills: not loaded" in the report
and review with `hoop-ui` only. To check, list the `references/`
folder in the base directory that the Skill tool names. When a brand skill
loads but its `references/` files are missing, use the rules in its `SKILL.md` only and
write "Brand skills: top level only".

Some rules disagree. Use this order, first wins:

1. The repo: `theme.js`, the tests, `CLAUDE.md`, `COMPONENTS.md`.
2. `hoop-ui`.
3. `design-webapp-md`.
4. `design-md`.

A break of a lower source that a higher source allows is not an issue. Put
it under "Questions for design" only when it is not in the "Known
differences" table of `references/review.md`.

Judge by today's rules: the `hoop-ui` of this checkout, also for an older or
merged PR. When the PR's own theme does not have a token that a rule names
yet, the issue and the fix stay. Add one line to the fix: "The theme does
this once <token> is on main."

Then read `references/review.md`: what not to flag, the known differences,
and how severe each break is.

Only review what the change adds or edits. Old code next to the change is
not an issue. When new code copies, moves or reuses an old pattern that
breaks a rule, flag it once and name the old place.

### A screenshot

A screenshot has no change, so review the whole screen, by today's rules.
If the person says which part is new, review that part only.

1. **Find the code.** Search a sentence the team wrote (a page
   description, a callout, a placeholder, a button), not the data
   (`pg-prod`, a date). Open the file it hits, then follow its imports until
   every part of the screen has a file. Write the route and the files in
   the report.
2. **No hit, or many.** Not in the repo: review from the image only, and
   say so. In many files (a component two pages share): name them all.
3. **Compare.** Check a few visible details against the code on main
   (corners, colors, a label). When they differ, the screenshot is from an
   older build. Judge by the code on main, do not flag what main already
   fixed, and say so under "Questions for design".
4. **Checks.** There is no diff. Run the script on the whole files, as if
   every line were new:

   ```bash
   for f in <files>; do git diff --no-index /dev/null "$f"; done \
     | python3 .claude/skills/design-review/scripts/check_diff.py
   ```

   Write "script on whole files" in the "Checks" line.
5. **Not app code** in the image (a browser bar, a chat button, an
   extension): do not review it. Name it once under "Not checked".
6. **States.** The image shows one state. List the others (loading,
   empty, error) under "Not checked", with the file that draws each one.

## 2. Run the checks

```bash
gh pr diff <N> | python3 .claude/skills/design-review/scripts/check_diff.py
git diff origin/main...HEAD | python3 .claude/skills/design-review/scripts/check_diff.py
```

It reads a unified diff and reports on added lines only. FAIL is a rule
break. WARN means look again: it can be correct. MOVED is old code that the
change moved to a new place: flag it once, as step 1 says. The script finds
patterns. It does not see the screen, and it does not see layout (a card in
a card, a field error shown only in a toast, a switch whose visible name is
not its label): step 3 is still necessary.

For a web app change, also run the tests that guard the theme. They read the
files on disk, not the diff. For a PR, put its code in a separate folder
first:

```bash
git fetch origin pull/<N>/head && git worktree add ../pr-<N> FETCH_HEAD
cd ../pr-<N>/webapp_v2 && npm ci && npx vitest run test/themeColors.test.js
```

Remove the folder after (`git worktree remove ../pr-<N>`). `npm ci` takes
minutes. If you cannot wait or cannot run it, write "Color test: not run" in
the report, and read the added lines for a hex, an `rgb()` or a
`--mantine-color-<name>-<n>` shade instead.

## 3. Read and look

Go through the "Check before you finish" list at the end of the `hoop-ui`
reference, one line at a time, on the changed code. Then read the other
sections for what the list does not cover.

When the change has a screen or terminal output you can see, look at it:

- Web app: the PR's "Testing" images, or run it (`cd webapp_v2 && npm run dev`).
- Terminal: the PR's images, or build and run the command. Build into a
  scratch folder and run it with a throwaway `HOME`, so the person's own
  `hoop` and its config stay as they are. Never `make build-dev-client`: it
  replaces `$HOME/.hoop/bin/hoop`.

  ```bash
  go build -o "$SCRATCH/hoop" ./client          # about 20 seconds
  HOME="$SCRATCH/home" "$SCRATCH/hoop" context list
  ```

  Look at it in a dark and a light terminal theme (`term.sh ... light`), and
  once with `NO_COLOR=1` and piped through `| cat`. When the command prints
  no color, one theme is enough: say so in "Not checked". To reach an error
  from the gateway with no gateway, point the command at a small local
  server that answers the error (for example a 502).

If you cannot run it, use the PR's images and the code. Say so in the
report's "Reviewed from" line. A GIF or video reads better as frames:

```bash
ffmpeg -loglevel error -i in.gif -vf "fps=1/2,scale=1200:-1" frame-%02d.png
```

That makes small frames to read. For `mark.sh`, take the frame you need again
at full size (drop `,scale=1200:-1`), so the small text stays readable.

Some rules need the running screen (hover, focus ring, 44px touch target,
390px width). When you could not see it, list those rules under "Not
checked".

When the design file (Figma, the PR's description) and `hoop-ui` disagree,
do not pick: write it as a question for design.

## 4. Show each issue

Each Must fix and Should fix item gets one image: the spot marked, and the
fix next to it when you can show it. Nits and questions get no image. An
issue with nothing to see (an error shown only in a toast, a missing
`aria-label`) gets no image: write "No image: nothing to see on the screen"
in its place. A code-only break on a visible element (a token passed as a
prop) also gets no image. Save the images in
`$HOME/design-review/<PR number, branch, or screenshot-<page>>/`, named
`<item number>-<short-name>.png`. In the report, write the full path
(`/Users/<you>/design-review/...`): `~` does not open in Markdown.

| Scope | You have | Do |
| --- | --- | --- |
| Terminal | anything | `term.sh` |
| Web app | the screen running | `shot.mjs`, then `pair.sh` |
| Web app | only the PR's images | `mark.sh` (the spot, no "after") |
| Web app | an issue that shows only across steps | a GIF with `.claude/skills/evidence/rec.mjs` |

The scripts are in `scripts/`. Each one prints the file it wrote.

**Terminal.** Save what the command prints, colors and errors included
(errors go to stderr, so add `2>&1`). Then write the "after" text by hand
from the fix, with the same data as the "before": the same names and URLs,
so only the fix changes. `term.sh` wraps lines at 80 columns:

```bash
CLICOLOR_FORCE=1 hoop context list > before.txt 2>&1   # or copy the PR's output
scripts/term.sh before.txt after.txt ~/design-review/<N>/02-table.png v        # dark
scripts/term.sh before.txt after.txt ~/design-review/<N>/02-table-light.png v light
```

If `CLICOLOR_FORCE` gives no colors, run the command in a real terminal
with `script -q before.txt <command>`.

**Web app, running.** `shot.mjs` opens the page, draws a red "BEFORE" box on
the element, applies the fix in the page (not in the code), and draws a
green "AFTER" box. `--steps` reaches a state first (open a card, a modal):

```bash
# A test hoop with a seeded admin, and its login in a file:
scripts/dev/run-sandbox.sh start
(source "${TMPDIR:-/tmp}/hoop-sandbox/env" && printf %s "$HOOP_TOKEN" > /tmp/hoop-dev-token)
# The web UI: `cd webapp_v2 && npm run dev:full` (http://localhost:5173).

node scripts/shot.mjs http://localhost:5173 /sessions \
  "[...document.querySelectorAll('button')].find(b => b.textContent.trim() === 'Clear Filters')" \
  "el.textContent = 'Clear filters'" ~/design-review/<N> --name 03-clear-filters --pad 160 \
  --token /tmp/hoop-dev-token
scripts/pair.sh ~/design-review/<N>/03-clear-filters-{before,after}.png ~/design-review/<N>/03-clear-filters.png
```

For a PR, run the stack from the PR's code (the `../pr-<N>` folder of step
2). A fix the page cannot show (a new empty state, an aria-label) gets the
"before" only.

**Web app, PR images or a screenshot.** Find the box of the spot in the
image's pixels: crop the area with ffmpeg and look at it, then read the box
from the crop and add the crop's corner. Mark it:

```bash
scripts/mark.sh shot.png <x> <y> <w> <h> ~/design-review/<N>/02-status-dot.png [pad]
```

Look at every image before you use it: the box must sit on the issue.

## 5. Report

Use this format. Most severe first. Each item names its file.

```markdown
## Design review: <PR, branch, or "screenshot of <page>">

- **Scope:** web app | terminal | both
- **Code:** (a screenshot only) the route and the files that draw it
- **Checks:** script <n> FAIL, <n> WARN, <n> MOVED (on the diff | on whole files) · color test passed | failed | not run | n/a (no web app change)
- **Rules from:** hoop-ui · design-webapp-md · design-md. Mark each brand skill "loaded", "top level only" or "not loaded".
- **Reviewed from:** the running screen | code and PR images (not run) | a screenshot and the code on main (not run)

### Must fix
None. (Write "None." when a section is empty. Keep the heading.)
1. `webapp_v2/src/pages/Foo/index.jsx:42`: two filled indigo buttons in one view.
   Rule: hoop-ui webapp.md, 4. Page structure ("One primary button per view").
   Fix: make "Cancel" `variant="subtle"`.
   ![1](/Users/<you>/design-review/<N>/01-two-primary.png)

### Should fix
...

### Nits
- At most three. Leave this section out when there are Must fix items.

### Questions for design
- Where hoop-ui, the repo and the design file disagree.

### Not checked
- Rules you could not see without running it, and the states the input does not show.

### Looks good
- One line for each rule the change follows well. Two to four lines.
```

Rules for each issue:

- One location (`path:line`), one rule, one fix. The fix is code or exact
  text the engineer can paste.
- The same break in many places is one item: give the first location, then
  list the others in the item ("Also at: `a.jsx:15`, `b.jsx:20`").
- Name the rule with its `hoop-ui` file and section, so the engineer can
  read why.
- Write for an engineer with no design background: say what the person
  using the product sees, not design terms. "The error is gray, so it looks
  like a hint" and not "low visual hierarchy".
- If `hoop-ui` and the repo disagree, the repo wins. Say so in one line
  under the report, so design can update `hoop-ui`.
- If you are not sure, write it as a question, not a rule.

An image path on your machine is for the person who asked. To post the
report on a PR, publish the images first
(`.claude/skills/evidence/publish.sh <TICKET-ID> <files>`) and use those
links. Only post when the person asks.

No issues: say "No design issues found", list what you checked, and stop.

## Writing style

Write the report in English, also when the person asks in another language:
it goes on PRs and tickets, and the repo is in English. Simple English
(ASD-STE100). Short sentences. No em dashes. No buzzwords.
