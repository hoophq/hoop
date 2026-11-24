---
name: hoop-ui
description: Design rules for Hoop UI. Use BEFORE you write or change anything a person sees in the Hoop product - a page, form, list, modal, empty state, badge, toast or component in webapp_v2/ (React, Mantine), or the text and output of the hoop CLI in client/ (help, flags, errors, tables, prompts, TUI). Use it also for a small UI fix, a label or a copy change. Not for the website (hoophq/lp) or documents.
---

# Hoop UI

Goal: a screen or a command output that looks like Hoop on the first try,
with no designer in the loop.

The repo holds the code rules. This skill holds the design decisions that
the repo does not write down, and tells you where the rest is. When this
skill and the repo disagree, the repo wins. Say so in your report.

## Which surface

| You change | Read |
| --- | --- |
| `webapp_v2/` (a page, a component, UI text) | `references/webapp.md` |
| `client/` output a person reads (help, errors, tables, prompts, TUI) | `references/terminal.md` |
| Both | Both. Use the same product words in both. |

The legacy ClojureScript app (`webapp/`) gets no new UI. Do not copy its look
into React. A page it still renders (Sessions, Runbooks, Terminal) follows the
same shape rules, set in its own code: a field, or a filter button beside a
field, is square (Radix `radius "none"`, Tailwind `rounded-none`).

## Steps

1. Read the reference for your surface, and the repo files it names.
2. Find the nearest existing screen or command and match it. Do not invent
   a pattern the app already has.
3. Build it. Use only components, variants and tokens that exist in the repo.
   Do not invent a token, a color, a size or a component name. If you need
   one that does not exist, stop and say what is missing.
4. Do the checks at the end of the reference, before you say you are done.
5. In your report, list: a rule you could not follow and why, a new
   component, and each `[TBD]` label.

## Never

- A color, size or radius that is not from the theme (web) or the style
  file (terminal).
- A second filled primary button in one view.
- A status shown by color alone.
- Motion a page writes on its own, or anything that moves on scroll.
- A product word that is not in the copy table (`references/webapp.md`,
  "Copy").
- Em dashes, exclamation marks, buzzwords, real customer names or emails.
