# Web app review

Scope: `webapp_v2/src/`. The legacy ClojureScript app (`webapp/`) is out of
scope: do not review it, and do not ask a React change to copy it.

## Sources in the repo

The code rules live in the repo. Read them there, do not restate them in a
review. Cite the file and section in each issue.

| What | Where |
| --- | --- |
| Styling order, `style={{}}`, CSS Modules, text color | `webapp_v2/CLAUDE.md`: "Styling hierarchy", "CSS Modules", "Text color" |
| Wrapped components, where a component lives | `webapp_v2/CLAUDE.md`: "Components" |
| Toasts | `webapp_v2/CLAUDE.md`: "Snackbars / Toasts" (`showSnackbar`) |
| The components that exist, with props | `webapp_v2/COMPONENTS.md` |
| Colors, palettes, radius, sizes | `webapp_v2/src/theme.js` |
| No hand-picked colors in pages | `webapp_v2/test/themeColors.test.js` (ENG-994) |

The theme in `theme.js` wins over any picture or design file. Some design
files show pill buttons and square cards; the app is not there yet. Do not
flag a corner radius that comes from the theme.

## Design decisions not written in the repo

Check each one. The section names are the names to cite.

A role token or a variant in the repo wins over these rules. Examples: a
note uses `Paper variant="info"` (indigo-0); the header of an open settings
card uses `--hoop-tint-green`. Read the comments in `theme.js` and
`components/Paper/theme.js` before you flag a color.

### Color meaning

- Indigo (the primary color) is the action: primary button, link, focus,
  a selected option. It is never a status: a badge that changes color between
  indigo and green to show a state is a Should fix. A count is gray.
- Status: green is success, online, active. Amber is warning, pending,
  waiting for review. Red is error, offline, delete. Gray is neutral or off.
- Pink is the AI Analyzer feature only. Violet is Data Masking only.
- A status is never color alone: a badge or an icon has a word with it, and
  a status dot or icon has an `aria-label`.
- Dark navy (`--brand-navy`) is for the auth screens and upsell visuals only.
  The app is light: light page, light sidebar.

### Actions

- One primary (filled indigo) button per view: top right of the page header,
  or bottom right of a modal. Other buttons use `variant="default"`,
  `"light"` or `"subtle"`.
- A delete or other action that cannot be undone uses red and opens
  `ConfirmDialog` first.
- An icon-only button has a `Tooltip` that says what it does.

### Page structure

- A page uses `PageLayout` (`src/layout/PageLayout.jsx`): one title, one
  dimmed description line under it, then the content.
- Forms sit on the page with `SectionRow`, one row per setting. No card
  around form fields.
- A list is one `Table` or one bordered block. No card inside a card, with
  one exception: the settings card pattern from Figma. A stack of
  `SettingsCard`s (`CardStack`), each with its open panel, is designed that
  way: do not flag its borders. Flag a border that a page adds around or
  inside that pattern, and say how many borders the person sees in a row.
- Control size: 40px by default (`size="md"`), row actions 32px
  (`size="sm"`).

### Four states

A view that loads data shows all four: loading (`PageLoader` or a skeleton),
empty (`EmptyState`: what is missing and what to do next), error (what
failed, and a retry when it makes sense), and data. A missing empty or error
state is a Should fix. A blank page on error is a Must fix.

### Copy

UI text is English. Use the product words, with these capitals. When the
repo uses two names for one thing, the sidebar label
(`src/layout/Sidebar/gatewayNav.js`) decides. One page uses one name.

| Write | Not |
| --- | --- |
| Resource, Resource Role | connection, database, target |
| Agent | runner, worker |
| Sidecar, listener | proxy, gateway node |
| Guardrail | policy, filter |
| Live Data Masking, Data Masking | redaction, DLP rule |
| AI Analyzer | AI reviewer |
| Session | connection log, activity |
| Runbook | script, playbook |
| Approval, Pending Approvals | review request |
| Access Control | permissions page |

- Sentence case for titles, labels and buttons. Product words keep their
  capitals: "Create a new Guardrail", "Select all", "Candidate groups".
- A button starts with a verb: "Create rule", "Save", "Delete". Never "OK",
  "Submit" or "Yes".
- An error says what failed: "Failed to save Guardrail." The server message
  goes in the description. Never "Oops" or "Something went wrong" alone.
- A success toast is past tense and names the item: `Agent "pg-prod" removed.`
- A confirm dialog asks a question and says the result: "Delete Agent?" /
  "This action cannot be undone."
- An optional field says so: "Description (Optional)".
- A placeholder shows an example value, not the label again.
- No em dashes. No exclamation marks. No buzzwords (seamless, robust,
  leverage, cutting-edge, next-generation).
- Sample data is invented: `pg-prod`, `jane@example.com`. Never a real
  customer, email or host.

### Readable

- No text under 12px. Secondary text is `c="dimmed"`, never a lighter gray.
- At 390px wide, nothing scrolls the page sideways.

## In a screenshot

Look for, in this order: more than one filled indigo button, indigo or color
alone used for a status, a missing title or description, a card around a
form, text that is hard to read, words that are not the product words, and an
empty or error state that is blank.
