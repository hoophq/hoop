# Terminal review

Scope: what the `hoop` CLI shows a person: command output, help text, flag
help, prompts, errors, tables, and the full-screen TUIs (`sidecartui/`).

## Sources in the repo

| What | Where |
| --- | --- |
| The designed palette, light and dark (DES-234) | `client/cmd/sidecartui/styles.go` |
| Shared helpers for every command | `client/cmd/styles/` (`RenderTable`, `ClientError`, `Fainted`) |
| Errors and JSON events | `client/cmd/output.go` (`fatalErr`, `emitJSONEvent`) |
| When the TUI turns off (pipe, NO_COLOR, TERM=dumb, coding agent) | `client/cmd/sidecartui/format.go` |

## Color

- **Three meanings only.** Gray is information. Blue `#3E63DD` is what the
  person acts on: the brand mark, a key to press, the active tab, live state,
  an approval. Red `#C4060A` is refused or broken: a denial, an error, high
  risk. Do not add another hue. Success stays gray, not green: a color
  anywhere else makes blue and red mean less.
- **Never color alone.** A word or a symbol carries the meaning, and color
  only adds to it: `✕ DENY`, not a red `DENY` next to a gray `ALLOW` with no
  mark. A person with NO_COLOR, a color-blind person, or a log file gets the
  same meaning.
- **Grays follow the terminal background.** A fixed light gray disappears on
  a white terminal. Use `lipgloss.LightDark` as `setTheme` does. Only blue,
  red, and white text on a blue or red fill can be fixed colors.
- **Colors are defined once.** A hex or a 256-color number goes in the
  package's style file (`styles/`, `sidecartui/styles.go`), never in a
  command file. A command uses a named style.
- **Plain when not a person at a terminal.** When stdout is a pipe, NO_COLOR
  is set or TERM=dumb, print no escape codes and open no TUI. lipgloss and
  termenv do this for you. A raw `\x1b[` or `\033[` code does not: it is a
  Must fix.
- **Known debt.** `styles.ClientError` (amber `#DBAB79` fill), `Keyword`
  (pink 204) and the amber table header predate this palette. Use the helpers,
  so every error looks the same. Do not add new amber or pink. Do not flag the
  old ones.

## Text

- Use the product words, with their capitals (`webapp.md`, Copy): Resource,
  Agent, Sidecar, Guardrail, Session, Runbook. Older commands say
  "connection": do not flag old text, but new text says Resource.
- **Help `Short`**: starts with a verb, sentence case, no final period, under
  60 characters. "List resources", "Show the output of a plan session".
- **Help `Long`**: what the command does and when to use it. Put a real
  command with invented names (`pg-prod`) in cobra's `Example` field, on
  every command a person runs, not only on the parent.
- **Flag help**: one line, what the flag changes, and the values it takes:
  "Output format. One of: (json|wide)".
- **Errors**: say what failed, then why when it is known, then the next step:
  "Failed to fetch roles: not logged in. Run `hoop login`." A bad value says
  what a good one looks like: "--api-url must start with https://, for
  example https://hoop.example.com". In package `cmd`, print it with
  `fatalErr` or `styles.PrintErrorAndExit`. In a subcommand package
  (`contexts/`, `versions/`), return the error from `RunE`; cobra prints
  `Error: ...`. Both are correct. No Go struct dumps (`%+v`), stack traces or
  raw HTTP bodies to the person: they go to the debug log. A body can be a
  whole HTML error page, so it is a Must fix.
- **Success**: past tense, names the item: "Context pg-prod saved."
- **Empty**: say what is missing and the command that fixes it: "No contexts
  yet. Try `hoop context create NAME --api-url URL`."
- **Hints**: give the exact command, in backticks, that the person can copy.
- No em dashes. No exclamation marks. No emoji. Symbols only from this set,
  with this meaning: `✓` allowed or done, `✕` denied, `!` error, `▲` risk,
  `◆` an action, `→` `←` open and close, `*` the active item in a list.
- **Secrets**: never print a token, password or key. Print `OMITTED`, as
  `hoop config view` and `hoop context show` do. A long value (a base64
  certificate) prints as `yes` or `no`; a separate command shows the raw value.

## Layout

- **Width**: fit 80 columns. A TUI reads the terminal width and adapts.
- **Tables**: `styles.RenderTable` for every list a person reads. Headers in
  UPPERCASE (`NAME`, `TYPE`, `STATUS`). No hand-aligned columns with spaces or
  `tabwriter` in new code.
- **Alignment**: text left, numbers right, with a thousands separator
  (`12,345`). One time format in one view.
- **Space**: one blank line between blocks, none inside a block. Indent
  nested lines by 2 spaces.
- **Machine output**: with `-o json`, stdout holds JSON only, one object per
  line. Messages for the person go to stderr or into the JSON `message`.

## Prompts

- Ask a question, and show the choices with the default in capitals:
  `Append it to ~/.zshrc now? [y/N]`. The default is the safe answer.
- An action that removes something the person cannot get back (data on the
  gateway, a key shown once) asks first and names the item: "Delete resource
  pg-prod? [y/N]", with `--yes` to skip the question. Removing local config
  the person can create again (a context, a login) needs no question, but the
  success line names what went: "Context dev and its login removed."
- Without a terminal (stdin is not a TTY), never wait for an answer. Take a
  flag (`--yes`), or fail and name that flag.

## TUI

Follow `sidecartui/`: panes with a rounded border in the border gray, the
active tab blue and underlined, key hints at the bottom in blue
(`q quit  ? help`), one selected row with the selection gray fill. A new TUI
builds its styles in a `setTheme` that runs again on
`tea.BackgroundColorMsg`.

## In a screenshot

Look for, in this order: text you cannot read on this background, a color
that is not gray, blue or red, red used for something that is not broken,
meaning carried by color only, misaligned columns, lines wider than the
window, and an error with no next step.
