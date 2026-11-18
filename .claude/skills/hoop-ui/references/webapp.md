# Web app (`webapp_v2/`)

## 1. Read the repo first

The code rules live in the repo. Read them there. This file does not copy them.

| What | Where |
| --- | --- |
| Styling order, no `style={{}}`, CSS Modules, text color | `webapp_v2/CLAUDE.md`: "Styling hierarchy", "CSS Modules", "Text color" |
| Wrapped components, where a new file goes | `webapp_v2/CLAUDE.md`: "Components" |
| Toasts | `webapp_v2/CLAUDE.md`: "Snackbars / Toasts" (`showSnackbar`) |
| Every component that exists, with props and examples | `webapp_v2/COMPONENTS.md` |
| Control sizes (40px default, no `radius`, no `xl`) | `webapp_v2/COMPONENTS.md`: "Control size scale" |
| Colors, palettes, spacing, radius, type, role tokens | `webapp_v2/src/theme.js` |
| Surfaces by role (`Paper variant`) | `webapp_v2/COMPONENTS.md`: "Paper" |
| No hand-picked colors in pages | `webapp_v2/test/themeColors.test.js` |
| Sidebar labels (the names of pages) | `webapp_v2/src/layout/Sidebar/gatewayNav.js` |

Reference pages to copy from:

| You build | Open |
| --- | --- |
| A list page | `pages/Guardrails/index.jsx`, `pages/Agents/index.jsx` |
| A create or edit form | `pages/Guardrails/Create/GuardrailForm.jsx`, `pages/Agents/Create/index.jsx` |
| A details page | `pages/Sidecars/Details/index.jsx` |

## 2. Color

A page never names a shade (`gray.6`, `--mantine-color-gray-6`), a hex or an
`rgb()`. The test fails on them. Pick, in this order:

1. A component that has the color already (`Badge variant`, `Alert`,
   `Paper variant`, `Divider`, `CodeSnippet`).
2. A semantic name: `c="dimmed"`, no prop for body text, `color="red"` on a
   component.
3. A role token from `theme.js` (`--hoop-surface-*`, `--hoop-border-*`,
   `--hoop-text-*`, `--hoop-tint-*`), in a CSS Module only.

What each color means. These rules are not in the repo:

- **Indigo is the action.** Primary button, link, focus, a selected option.
  Never a status. A count is gray, not indigo.
- **Status:** green is success, online, active. Amber is warning, pending,
  waiting for review. Red is error, offline, delete. Gray is neutral or off.
- **Never color alone.** A status has a word (`Badge`) or an icon with an
  `aria-label`.
- **Feature hues:** pink is the AI Analyzer only. Violet is Data Masking and
  AI features (`Paper variant="accent"`) only.
- **A note on the page** is `Paper variant="info"` (indigo tint) or
  `variant="warning"` (amber tint). Not `sky`: `sky` is in the theme, but no
  page uses it for a note.
- **The app is light.** Light page, light sidebar. Dark navy
  (`--brand-navy`) is for the auth loader and upsell visuals only. A dark
  block in a page is a terminal or code (`CodeSnippet`, `Code variant="terminal"`).
- **No shadows on page content.** Separate areas with a border
  (`withBorder`) or a surface step (`Paper variant`).

## 3. Shape

Use the radius the theme gives. Never pass `radius` on a control.

Future direction, not for now: the brand draws pill buttons and square
cards. `webapp_v2` does not have them. Do not build them until `theme.js`
does.

## 4. Page structure

- **Header** (as `pages/Guardrails/index.jsx`):
  `Stack gap="xl"` > `Group justify="space-between" align="flex-start"` >
  left `Stack gap="sm"` with `Title order={1}` and one line of
  `Text size="md" c="dimmed"`; right, the primary `Button`.
  The title is the sidebar label. `PageLayout` already pads the page: add no
  outer padding.
- **One primary button per view.** Filled indigo, no variant. Top right of
  the page header, or bottom right of a modal. Other buttons:
  `variant="default"` (secondary), `variant="subtle"` (quiet, Cancel),
  `color="red"` (destructive).
- **Forms** sit on the page: `Stack gap="xxlAlt"` of `SectionRow`, fields in
  `Stack gap="md"`. No card or border around the fields. Long form:
  `FormFooter`.
- **Lists:** one `Table`, or one bordered block of rows. Use rows when each
  item has a name, a description and one action. Use `Table` when items have
  many columns. Row actions: `ActionMenu` (right), or `size="sm"` buttons.
  Filters (`ValueFilter`, `AsyncValueFilter`) in a `Group gap="sm"` above the
  list.
- **No card inside a card.** One exception: `SettingsCard` in
  `pages/Features/AiSessionAnalyzer/` nests on purpose. Do not add a border
  around or inside it.
- **Back link** on a form or details page: `Button variant="transparent"
  color="gray"` with `leftSection={<ArrowLeft size={16} />}`, above the title.
- **Details page:** back link, `Title order={1}` with the item name, sections with
  `Title order={4}`. Facts in a key/value `Table`. More than three sections:
  `Tabs`.
- **Modals:** `Modal` from `@/components/Modal`. A delete or an action that
  cannot be undone: `ConfirmDialog` with `danger`.
- **Icon-only button:** `ActionIcon` in a `Tooltip` that says what it does.
- **Plan gates:** `FreeLicenseCallout` under the header, `EnterpriseBanner`
  on a form, `FeaturePromotion` in `FullBleed` for a feature that is off.

## 5. Four states

A view that loads data shows all four. Each is real code, reached by the
data, not a preview switch.

| State | Use |
| --- | --- |
| Loading | `<PageLoader h={300} />` (a part of a page: `Skeleton`) |
| Empty, no data | `<EmptyState title description action />`. `action` only when the user can create. Hide the header button while the list is empty. |
| Empty, filters match nothing | `<EmptyState compact title="No Guardrails match your filters" description="Try clearing the filter above." />` |
| Error | `<PageLoader error h={300} message="Failed to load guardrails." />`, or `Alert color="red"` with a retry button |

## 6. Copy

UI text is English. When the repo uses two names for one thing, the sidebar
label in `gatewayNav.js` decides. One page uses one name.

| Write | Not |
| --- | --- |
| Resource, Resource Role | connection, database, target |
| Agent | runner, worker |
| Sidecar, listener | proxy, gateway node |
| Guardrail | policy, filter |
| Live Data Masking, Data Masking | redaction, DLP rule |
| AI Analyzer | AI Session Analyzer, AI reviewer |
| Session | connection log, activity |
| Runbook | script, playbook |
| Approval, Pending Approvals, Approval rules | review request |
| Access Control | permissions page |
| Attribute | label, group |

- Sentence case for titles, labels and buttons. Product words keep their
  capitals: "Create a new Guardrail".
- **A new feature name is a product word.** Pick it once: the sidebar label,
  in the singular, each word capitalized ("Session Export"). Then write it
  the same way everywhere: title, buttons, empty state, dialogs, toasts,
  errors, and the CLI. Not "Session export" in one place and "session
  export" in another. Add it to the table above in your report.
- A button starts with a verb: "Create rule", "Save", "Delete". Never "OK",
  "Submit" or "Yes".
- Page description: one line, what the person does here. `SectionRow`
  description: one line, why the person sets it.
- Empty state: what is missing, and what to do next.
- Error: what failed. "Failed to save Guardrail." The server message goes in
  `description`. Never "Oops" or "Something went wrong" alone.
- Success toast: past tense, names the item. `Agent "pg-prod" removed.`
- Confirm dialog: a question, and the result. "Delete Agent?" /
  "This action cannot be undone."
- Optional field: "Description (Optional)". Placeholder: an example value,
  not the label again.
- No em dashes. No exclamation marks. No buzzwords (seamless, robust,
  leverage, cutting-edge, next-generation).
- Sample data is invented: `pg-prod`, `jane@example.com`.
- You do not know a label? Write `[TBD: what]` and list it in your report.

## 7. Check before you finish

- [ ] `npm run lint` and `npm test` pass (`themeColors.test.js` is in it).
- [ ] No import of a wrapped primitive from `@mantine/core`
      (`COMPONENTS.md` lists the wrappers). Icons from `lucide-react` only.
- [ ] No `style={{}}`, no `styles={{}}` on an instance, no hex, no shade, no
      `radius` or `size="xl"` on a control.
- [ ] Every token you use exists in `theme.js` (`grep` it).
- [ ] One filled indigo button in the view.
- [ ] Indigo is not used for a status. No status by color alone.
- [ ] Title from the sidebar label, one dimmed description line.
- [ ] Form fields sit on the page, in `SectionRow`. No card in a card.
- [ ] Loading, empty, error and data states exist.
- [ ] Every icon-only button has a `Tooltip`.
- [ ] Words match the copy table. Buttons start with a verb.
- [ ] No text under 12px. Nothing scrolls sideways at 390px wide.
