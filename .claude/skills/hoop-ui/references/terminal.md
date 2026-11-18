# Terminal (`client/`)

Scope: what the `hoop` CLI shows a person: command output, help text, flag
help, prompts, errors, tables, and the full-screen TUIs (`sidecartui/`).

## 1. Read the repo first

| What | Where |
| --- | --- |
| The designed palette, light and dark | `client/cmd/sidecartui/styles.go` |
| Shared helpers for every command | `client/cmd/styles/` (`RenderTable`, `ClientError`, `Fainted`, `PrintErrorAndExit`) |
| Errors and JSON events | `client/cmd/output.go` (`fatalErr`, `emitJSONEvent`) |
| When the TUI turns off (pipe, NO_COLOR, TERM=dumb, coding agent) | `client/cmd/sidecartui/format.go` |

Find the nearest command in `client/cmd/` and match its shape.

## 2. Color

- **Three meanings only.** Gray is information. Blue `#3E63DD` is what the
  person acts on: the brand mark, a key to press, the active tab, live state,
  an approval. Red `#C4060A` is refused or broken: a denial, an error, high
  risk. Do not add another hue. Success stays gray, not green.
- **Never color alone.** A word or a symbol carries the meaning: `✕ DENY`,
  not a red `DENY` next to a gray `ALLOW`. NO_COLOR, a color-blind person or
  a log file get the same meaning.
- **Grays follow the terminal background.** Use `lipgloss.LightDark` as
  `setTheme` in `sidecartui/styles.go` does. Only blue, red, and white text on
  a blue or red fill can be fixed colors.
- **Colors are defined once,** in the package style file (`styles/`,
  `sidecartui/styles.go`). A command file uses a named style, never a hex or a
  256-color number.
- **Plain when not a person at a terminal.** Pipe, NO_COLOR or TERM=dumb: no
  escape codes, no TUI. lipgloss and termenv do this. A raw `\x1b[` or
  `\033[` code does not: never write one.
- **Old helpers.** `styles.ClientError` (amber fill), `Keyword` (pink) and the
  amber table header predate this palette. Use the helpers so every error
  looks the same. Do not add new amber or pink.

## 3. Text

- Product words, with their capitals, as in `webapp.md` "Copy": Resource,
  Agent, Sidecar, Guardrail, Session, Runbook. Older commands say
  "connection". New text says Resource. A new feature name follows
  `webapp.md` "Copy": the same word, with the same capitals, in the web app
  and in the CLI text.
- **Help `Short`:** starts with a verb, sentence case, no final period, under
  60 characters. "List resources".
- **Help `Long`:** what the command does and when to use it.
- **`Example`:** a real command with invented names (`pg-prod`), on every
  command a person runs.
- **Flag help:** one line, what the flag changes, and its values:
  "Output format. One of: (json|wide)".
- **Errors:** what failed, then why, then the next step:
  "Failed to fetch roles: not logged in. Run `hoop login`." A bad value shows
  a good one: "--api-url must start with https://, for example
  https://hoop.example.com". In package `cmd`, print with `fatalErr` or
  `styles.PrintErrorAndExit`. In a subcommand package, return the error from
  `RunE`. Never print a Go struct (`%+v`), a stack trace or a raw HTTP body:
  they go to the debug log.
- **Success:** past tense, names the item: "Context pg-prod saved."
- **Empty:** what is missing and the command that fixes it:
  "No contexts yet. Try `hoop context create NAME --api-url URL`."
- **Hints:** the exact command, in backticks, ready to copy.
- No em dashes. No exclamation marks. No emoji. Symbols only from this set:
  `✓` allowed or done, `✕` denied, `!` error, `▲` risk, `◆` an action,
  `→` `←` open and close, `*` the active item in a list.
- **Secrets:** never print a token, password or key. Print `OMITTED`, as
  `hoop config view` does. A long value (a certificate) prints as `yes` or
  `no`.

## 4. Layout

- **Width:** fit 80 columns. A TUI reads the terminal width.
- **Tables:** `styles.RenderTable` for every list a person reads. Headers in
  UPPERCASE (`NAME`, `TYPE`, `STATUS`). No hand-aligned columns or
  `tabwriter` in new code.
- **Alignment:** text left, numbers right, with a thousands separator
  (`12,345`). One time format in one view.
- **Space:** one blank line between blocks, none inside a block. Indent
  nested lines by 2 spaces.
- **Machine output:** with `-o json`, stdout holds JSON only, one object per
  line. Messages for the person go to stderr.

## 5. Prompts

- Ask a question with the default in capitals: `Append it to ~/.zshrc now? [y/N]`.
  The default is the safe answer.
- An action that removes what the person cannot get back asks first and
  names the item: "Delete resource pg-prod? [y/N]", with `--yes` to skip.
  Removing local config the person can create again needs no question, but
  the success line names what went.
- Without a terminal (stdin is not a TTY), never wait for an answer. Take
  `--yes`, or fail and name that flag.

## 6. TUI

Follow `sidecartui/`: panes with a rounded border in the border gray, the
active tab blue and underlined, key hints at the bottom in blue
(`q quit  ? help`), one selected row with the selection gray fill. A new TUI
builds its styles in a `setTheme` that runs again on `tea.BackgroundColorMsg`.

## 7. Check before you finish

- [ ] `go test` passes on the changed packages.
- [ ] Run the command. Read the output on a dark and on a light terminal, and
      piped (`| cat`): no escape codes when piped.
- [ ] Colors: only gray, blue, red. No hex in the command file.
- [ ] Every status has a word or a symbol, not only a color.
- [ ] `Short` starts with a verb, under 60 characters. `Example` is set.
- [ ] Every error has a next step. No struct dumps or raw bodies.
- [ ] Lists use `styles.RenderTable` with UPPERCASE headers. Fits 80 columns.
- [ ] `-o json` prints JSON only on stdout.
- [ ] No secret printed. No em dash, emoji or exclamation mark.
- [ ] A destructive action asks first, and has `--yes`.
