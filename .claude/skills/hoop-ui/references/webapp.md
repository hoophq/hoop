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
| A list page | `pages/Guardrails/index.jsx`, `pages/Agents/index.jsx` for the page; rows from `List` or `Table` (section 4), not their row code |
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

Square is the default: `defaultRadius` is 0, so a component with no shape
rule of its own (a dropdown, a menu, a tooltip, inline `Code`, a skeleton)
is square. Only what a theme makes round is round: `Button`, `ActionIcon`,
`Pagination` numbers (`--hoop-button-radius`), `Badge` and `Pill`
(`--hoop-badge-radius`), and Mantine's own round parts (`Avatar`, `Radio`,
`Switch`, `Stepper` icons).

Buttons are pills. The theme does it (`components/Button/theme.js`), so a
call site sets nothing. Do not draw a pill by hand on another component.

A button next to a field is square. In a filter bar, a search or any row
where a button sits beside an input, use `Button variant="field"`, or
`ActionIcon variant="field"` for an icon alone: it takes the field corners
(`--hoop-field-radius`, 0), fill, border and height.

Lists are square. `List` and `Table` take their corners and lines from
`--hoop-list-radius` (0) and `--hoop-list-border`. A page never sets them.

Fields are always square. Every input, select and textarea takes its corners
from `--hoop-field-radius` (0), and so do `SourcedInput` and the `field`
Button. Never round a field. Checkboxes are square too, from the same token.
With square cards and fields, buttons are the only round thing: the action
stands out.

A link to the docs is `DocsBtnCallOut`: a `size="sm"` button with an arrow
(`variant="indigo"` for the accented one). Never a hand-made link box.

A selected option (`SelectionCard`) is indigo: a light tint and an indigo
border. Never navy, which is for login and upsell visuals only.

Callouts are square. A note on the page (`Alert`, or a `Paper` with
`data-callout`) takes its corners from `--hoop-callout-radius` (0).

Cards are square. Every surface (`Paper`, `Card`, `MethodCard`,
`SelectionCard`, `EnterpriseBanner`, `CodeSnippet`, `Accordion`, a panel or a viewer)
takes its corners from `--hoop-card-radius` (0) and its white fill from
`--hoop-card-bg`, so a card stands off the page. `Paper`, `Accordion`, `Alert` and `ThemeIcon` ignore
a `radius` prop, so a page never passes one. Icon tiles (`ThemeIcon`) are
square too, from `--hoop-icon-radius` (0). Icon buttons (`ActionIcon`) are round, from `--hoop-button-radius`, like
`Button`. Badges are pills, from
`--hoop-badge-radius`; a `radius` prop on a Badge does nothing. A status badge is 11px
and its text reads at 4.5:1 or more: filled and outline badges use step 7 of
their color, a light badge writes its text in step 9 (`components/Badge`).
Never pass a lighter `c` on a badge. Tabs and `SegmentedControl` are square, from
`--hoop-tab-radius`: the hover fill, the focus ring and the moving indicator.
Page numbers (`Pagination`) are circles, from `--hoop-button-radius`. Modals, menus and
toasts are square like the rest.


**Change a shape in the theme, never on a page.** A radius, a border, a
motion or a size is a token in `theme.js` or a default in a component's
`theme.js`. If a page needs a value no token has, add the token, then use
it.

## 4. Page structure

- **Header** (as `pages/Guardrails/index.jsx`):
  `Stack gap="xl"` > `Group justify="space-between" align="flex-start"` >
  left `Stack gap="sm"` with `Title order={1}` and one line of
  `Text size="md" c="dimmed"`; right, the primary `Button`.
  The title is the sidebar label. `PageLayout` already pads the page: add no
  outer padding.
- **One line per level.** The header line says what the page does. A line
  about one choice goes in that section's `SectionRow` description. Never a
  loose `Text` between the header and the first section.
- **One primary button per view.** Filled indigo, no variant. Top right of
  the page header, or bottom right of a modal. Other buttons:
  `variant="default"` (secondary), `variant="subtle"` (quiet, Cancel),
  `color="red"` (destructive).
- **Forms** sit on the page: `Stack gap="xxlAlt"` of `SectionRow`, fields in
  `Stack gap="md"`. No card or border around the fields. Long form:
  `FormFooter`.
- **Lists:** one `Table`, or one `List` (`@/components/List`). Use `List`
  when each item has a name, a description and one action. Use `Table` when
  items have many columns. A row draws no border, corner or first/last flag:
  `List` does. Row actions: `ActionMenu` (right), or `size="sm"` buttons.
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
| Empty, no data | `<EmptyState title description action />`. `action` only when the user can create. One create button per view: in the empty state (center) while the list is empty, in the header (top right) once it has items. Never both, never neither. A create menu (`NewRuleButton`) goes in `action` as an element. Same shape on every page: title "No <things> yet", one `description` line on what to do, then `docsUrl` and `docsLabel="<Feature> documentation"` (it renders "Need more information? Check out our ..."). Not `compact`: that is for filters that match nothing. |
| Empty, filters match nothing | `<EmptyState compact title="No Guardrails match your filters" description="Try clearing the filter above." />` |
| Error | `<PageLoader error h={300} message="Failed to load Guardrails." />`, or `Alert color="red"` with a retry button |

## 6. Motion and feedback

Every control answers the pointer, and nothing else moves. The theme does
the work: a page writes no `transition` or `transform` on a control.

- **Buttons** step up 1px on hover and sink on press
  (`components/Button/Button.module.css`). `subtle`, `transparent` and
  `field` only change color.
- **Fields** take the action border and a soft 3px ring on focus
  (`components/Input/Input.module.css`).
- **Focus** shows at once, as Mantine draws it. Never remove the ring.
- **Tokens** in `theme.js`: `--hoop-ease`, `--hoop-motion-in` (150ms),
  `-out` (120ms), `-press` (90ms), `-field` (140ms). A new motion uses them.
- Move only `transform`, `opacity` and colors. Never animate `height`,
  `width`, `margin` or `padding`. No bounce, no tilt, no glow.
- A clickable row or card may lift 2px. A row that is not a link does not
  move.
- Nothing moves because the page scrolls: no fade-in on scroll, no
  parallax, no carousel, no tab that changes by itself.
- Under reduced motion everything rests (`respectReducedMotion` and the
  `prefers-reduced-motion` rules). Nothing stays hidden.

## 7. Touch and keyboard

- **Touch target 44px** (`--hoop-touch-target`). `Button` and `ActionIcon`
  grow an invisible hit area on a touch screen. A new clickable thing
  that is not one of them does the same.
- **Labels you can see.** Every field has a `label`. A placeholder is
  never the label. An `aria-label` alone is not one.
- **Every control has a name.** An icon-only button has a `Tooltip` and
  an `aria-label`. A picture has `alt`.
- **Headings go one level at a time.** No `tabindex` above 0.
- **The first error takes the focus** on a form that fails, and the error
  sits under its own field.
- The skip link (`layout/SkipLink.jsx`) stays the first stop of Tab.

## 8. Copy

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
  "Submit" or "Yes". Never a bare "Learn more", "Read more", "Explore",
  "More" or "Click here": name the thing ("Read the Agents docs").
- Page description: one line, what the person does here. `SectionRow`
  description: one line, why the person sets it.
- Empty state: what is missing, and what to do next.
- Error: what failed. "Failed to save Guardrail." The server message goes in
  `description`. Never "Oops" or "Something went wrong" alone.
- Success toast: past tense, names the item. `Agent "pg-prod" removed.`
  No "successfully": the past tense says it.
- A form label says what goes in ("Name", "Expires in"), never a question.
  A field error names the field and what to type.
- A card title names a thing or says what it does ("Blocks DROP TABLE"),
  never a slogan.
- No fake data shown as real: no made-up counts, users or quotes. No
  placeholder text left in (TODO, lorem, "sample").
- Confirm dialog: a question, and the result. "Delete Agent?" /
  "This action cannot be undone."
- Optional field: "Description (Optional)". Placeholder: an example value,
  not the label again.
- No em dashes. No exclamation marks. No buzzwords (seamless, robust,
  leverage, cutting-edge, next-generation, supercharge, effortless,
  empower, "unlock the power of"). "Unlock" only for a feature a plan
  really locks.
- Sample data is invented: `pg-prod`, `jane@example.com`.
- You do not know a label? Write `[TBD: what]` and list it in your report.

## 9. Check before you finish

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
- [ ] Words match the copy table. Buttons start with a verb. No "Learn
      more", "successfully" or exclamation mark.
- [ ] Hover a button and press it: it steps and sinks. Tab through the
      view: every control shows its focus ring.
- [ ] No `transition` or `transform` written on a control in a page. Nothing
      moves on scroll.
- [ ] Every field has a visible label. Every clickable thing is 44px to a
      finger.
- [ ] No text under 12px. Nothing scrolls sideways at 390px wide.
