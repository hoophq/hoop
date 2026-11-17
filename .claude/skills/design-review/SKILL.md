---
name: design-review
description: Review web app or terminal (CLI) work against Hoop's design decisions and return clear issues with fixes. Use when asked for a design review, a UI review, a CLI or terminal output review, "does this look right", or "check this against the design system" - on a PR number, a branch, a diff, or a screenshot. Covers webapp_v2 screens and components (React, Mantine) and the hoop CLI (help, prompts, errors, colors, tables, TUI). No design context needed.
---

# Design review

Input: a PR number, a branch, a diff, or a screenshot of the web app or the
terminal. Output: a list of issues. Each issue says where it is, which rule it
breaks, and the fix. A reader with no design background can apply every fix.

You review. You do not edit files, and you do not post comments on GitHub or
Linear unless the person asks.

## 1. Get the work

| Input | Do |
| --- | --- |
| PR number | `gh pr diff <N>` and `gh pr view <N> --json title,body,files` |
| Branch | `git diff origin/main...<branch>` |
| No input | `git diff origin/main...HEAD`, plus uncommitted changes |
| Screenshot | Read the image. Find the code it shows if the repo has it (search the visible text). |

Then find the scope:

- Files under `webapp_v2/src/` → web app. Read `references/webapp.md`.
- Files under `client/` that print to a person (`fmt.Print*`, `styles.`,
  `lipgloss`, cobra `Short`/`Long`/flag help, `sidecartui/`) → terminal.
  Read `references/terminal.md`.
- Both → read both. Neither (only Go logic, tests, docs) → say "No design
  surface in this change" and stop.

Only review what the change adds or edits. Old code next to the change is not
an issue. When new code copies, moves or reuses an old pattern that breaks a
rule (code moved to a new file, an old component shown in a new place), flag
it once and name the old place. When it follows a newer command that does the same
thing (for example `hoop versions` for a new subcommand), do not flag it: list
it once under the report, so design can decide.

## 2. Run the script

```bash
gh pr diff <N> | python3 .claude/skills/design-review/scripts/check_diff.py
git diff origin/main...HEAD | python3 .claude/skills/design-review/scripts/check_diff.py
```

It reads a unified diff and reports FAIL and WARN on added lines only. FAIL is
a rule break. WARN means look again: it can be correct. The script finds
patterns. It does not see the screen: step 3 is still necessary.

For a web app change, also run the color test that guards the theme:

```bash
cd webapp_v2 && npx vitest run test/themeColors.test.js
```

No `webapp_v2/node_modules`: run `npm ci` in `webapp_v2` first. If you
cannot, write "Color test: not run" in the report.

## 3. Read and look

Read the changed code against the reference file of its scope. When the
change has a screen or terminal output you can see, look at it:

- Web app: the PR's "Testing" images, or run it (`cd webapp_v2 && npm run dev`).
- Terminal: the PR's images, or build and run the command
  (`make build-dev-client`, then `$HOME/.hoop/bin/hoop <command>`). Look at it
  in a dark and a light terminal theme, and once with `NO_COLOR=1` and piped
  through `| cat`.

If you cannot run it (the build needs private module credentials, or takes
too long), use the PR's images and the code. Say so in the report's first
line: "Not run: reviewed from code and PR images." A GIF or video from the PR
reads better as frames:

```bash
ffmpeg -loglevel error -i in.gif -vf "fps=1/2,scale=1200:-1" frame-%02d.png
```

Some rules need the running screen (control size, 390px width). When you
could not see it, list those rules under "Not checked" in the report.

When the design file (Figma, the PR's description) and a reference disagree,
do not pick: write it as a question for design.

## 4. Report

Use this format. Most severe first. Each item names its file.

```markdown
## Design review: <PR or branch>

Scope: web app | terminal | both. Script: <n> FAIL, <n> WARN.

### Must fix
1. `webapp_v2/src/pages/Foo/index.jsx:42`: two filled indigo buttons in one view.
   Rule: one primary button per view (webapp.md, Actions).
   Fix: make "Cancel" `variant="default"`.

### Should fix
...

### Questions for design
- Where a reference, the repo and the design file disagree.

### Not checked
- Rules you could not see without running it.

### Looks good
- One line for each rule the change follows well. Two to four lines.
```

Severity:

- **Must fix**: the person cannot read it, it misleads them, or it breaks a
  rule the repo enforces (test, lint, `CLAUDE.md`). A screen reader that
  cannot read a status is a Should fix; text no one can read is a Must fix.
  Examples: text that
  disappears on a light terminal, indigo used for an error, a color only the
  theme may define, a secret printed to the terminal, a raw HTTP body in an
  error.
- **Should fix**: it works, but it does not look or read like the rest of the
  product. Examples: a word that is not the product word, a missing empty
  state, a table aligned by hand.
- **Nit**: taste. Add at most three. Leave them out when there are Must fix
  items.

Rules for each issue:

- One location (`path:line`), one rule, one fix. The fix is code or exact text
  the engineer can paste.
- Name the rule with its reference file and section, so the engineer can read
  why.
- Write for an engineer with no design background: say what the person using
  the product sees, not design terms. "The error is gray, so it looks like a
  hint" and not "low visual hierarchy".
- If a reference and the repo disagree, the repo wins. Say so in one line
  under the report, so design can update this skill.
- If you are not sure, write it as a question, not a rule.

No issues: say "No design issues found", list what you checked, and stop.

## Writing style

Simple English (ASD-STE100). Short sentences. No em dashes. No buzzwords.
