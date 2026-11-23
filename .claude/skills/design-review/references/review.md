# Review notes

The rules are in `hoop-ui` (`../hoop-ui/references/webapp.md` and
`terminal.md`). This file adds only what a reviewer needs on top: what not
to flag, how severe a break is, and what to look for first in a screenshot.

## Do not flag

- **Shapes from the theme.** Pill buttons, round icon buttons and page
  numbers, pill badges, square fields, cards, tabs and callouts all come
  from `theme.js` tokens. Flag a `radius` written on a page, not a corner
  the theme draws.
- **`SettingsCard` borders** in `pages/Features/AiSessionAnalyzer/`. The
  stack nests on purpose. Flag only a border a page adds around or inside
  it, and say how many borders the person sees in a row.
- **Role tokens and variants.** `Paper variant="info"` (indigo tint) and
  `--hoop-tint-green` on an open settings card are correct. Read the
  comments in `theme.js` and `components/Paper/theme.js` before you flag a
  color.
- **Old terminal helpers.** `styles.ClientError` (amber fill), `Keyword`
  (pink) and the amber table header. Flag a new use, not an old one.
- **Old words in old commands.** "connection" in a command the change does
  not touch.
- **A pattern from a newer command.** When new code follows a newer command
  that does the same thing (for example `hoop versions` for a new
  subcommand), do not flag it. List it once under the report, so design can
  decide.
- **The legacy app** (`webapp/`). It is out of scope.

## Severity

**Must fix**: the person cannot read it, it misleads them, or it breaks a
rule the repo enforces (test, lint, `CLAUDE.md`).

| Web app | Terminal |
| --- | --- |
| A blank page on error | Text that disappears on a light or a dark terminal |
| Indigo or green used for an error | Red used for something that is not broken |
| A hex, shade or `rgb()` in a page (the color test fails) | A raw escape code (`\x1b[`), so pipes get garbage |
| Text under 12px, or text no one can read | A secret printed |
| A delete with no `ConfirmDialog` | A raw HTTP body or `%+v` in an error |
| A wrapped primitive imported from `@mantine/core` | `-o json` with text on stdout |
| A field with no visible label | A prompt that waits when stdin is not a TTY |

**Should fix**: it works, but it does not look or read like the rest of
the product.

| Web app | Terminal |
| --- | --- |
| A second filled primary button | A color that is not gray, blue or red |
| Indigo used for a status, or a badge that swaps to indigo by state | A status carried by color only |
| A status by color alone, or no `aria-label` on a status dot | A hand-aligned or `tabwriter` table |
| A word not in the copy table, or one feature with two names | An error with no next step |
| Title Case on a label or a button | `Short` with a period, over 60 characters or in lower case |
| A missing empty or error state | No `Example` on a command a person runs |
| A card around form fields, or a card in a card | A line wider than 80 columns |
| `radius`, `style={{}}`, `transition` or `transform` written on a page | A new amber or pink |
| An icon-only button with no `Tooltip` | A destructive action with no `--yes` |
| "Learn more", "successfully", "OK", "Submit" | Emoji, or a symbol not in the set |

**Nit**: taste. Add at most three. Leave them out when there are Must fix
items.

## In a screenshot

Look for, in this order.

**Web app:** more than one filled indigo button; indigo or color alone used
for a status; text that is hard to read; a missing title or description
line; a card around a form or a card in a card; a shape that is not the
theme's (a round field, a square button); words that are not the product
words, or Title Case; an empty or error state that is blank.

**Terminal:** text you cannot read on this background; a color that is not
gray, blue or red; red used for something that is not broken; meaning
carried by color only; misaligned columns; lines wider than the window; an
error with no next step.
