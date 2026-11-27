#!/usr/bin/env python3
"""Flag design rule breaks on the added lines of a unified diff.

The rules are in .claude/skills/hoop-ui/references/ (webapp.md, terminal.md).

Usage: gh pr diff 123 | python3 check_diff.py
       git diff origin/main...HEAD | python3 check_diff.py

It finds patterns, not designs: FAIL is a rule break, WARN means look again.
Exit code 1 when there is a FAIL.
"""
import os
import re
import subprocess
import sys

WEB = re.compile(r"^webapp_v2/src/.*\.(jsx?|css)$")
CLI = re.compile(r"^client/.*\.go$")
# Files that own colors: the theme and the style files.
WEB_OWNERS = re.compile(r"^webapp_v2/src/(components/|theme\.js$)")
CLI_OWNERS = re.compile(r"^client/cmd/(styles/|sidecartui/styles\.go$)")
# Text a person reads that lives outside webapp_v2/ and client/: the feature
# flag descriptions show at Settings, Experimental. Copy checks only.
COPY_ONLY = re.compile(r"^common/featureflag/featureflag\.go$")

EM_DASH = re.compile(r"\u2014")
BUZZ = re.compile(r"\b(seamless(ly)?|cutting-edge|revolutionary|next-generation|robust|holistic|leverage|utilize|supercharge|effortless(ly)?|empower|unlock the power)\b", re.I)
EMOJI = re.compile(r"[\U0001F300-\U0001FAFF\u2600-\u26FF\u2705\u274C]")
# A string literal, to limit text checks to what a person reads.
GO_STRING = re.compile(r'"((?:[^"\\]|\\.)*)"|`([^`]*)`')
# A label, title or button text: label: 'X', title="X", <Button>X</Button>.
TITLE_SLOT = re.compile(
    r"""\b(?:\w*[Ll]abel|\w*[Tt]itle|placeholder)\s*[:=]\s*["'`]([A-Za-z][^"'`{}]{2,40})["'`]"""
    r"""|<Button\b[^>]*>\s*([A-Za-z][^<{}]{2,40}?)\s*<"""
    # JSX text alone on its line, as in a Button that spans lines.
    r"""|^\s+([A-Z][a-z]+(?: [A-Z][A-Za-z]*){1,3})\s*$"""
)
# Product words keep their capitals, so a label built from them is fine.
PRODUCT = re.compile(
    r"Resource Role|Resource|Agent|Sidecar|Guardrail|Data Masking|AI Analyzer|Session|Runbook"
    r"|Approval|Pending Approvals|Access Control|Access Request|Attribute|Enterprise|Hoop|Slack|Teams|Jira|MCP|API|SSO|AWS|GCP"
    r"|Compliance Pack|Vault|Access Key|Optional|\bID\b|\b[A-Z]\b"
)
JS_STRING = re.compile(r"""(["'`])((?:(?!\1)[^\\]|\\.)*)\1|>([^<>{}]+)<""")


def repo_root():
    try:
        return subprocess.check_output(["git", "rev-parse", "--show-toplevel"], text=True).strip()
    except (OSError, subprocess.CalledProcessError):
        return "."


def wrapped_components(root):
    """Components with a wrapper: a folder with an index file.

    A folder with only theme.js (Paper, Divider, Input) extends the Mantine
    theme; the page imports that one from @mantine/core.
    """
    d = os.path.join(root, "webapp_v2", "src", "components")
    try:
        names = os.listdir(d)
    except OSError:
        return set()
    return {
        n for n in names
        if n[:1].isupper() and any(os.path.isfile(os.path.join(d, n, f)) for f in ("index.jsx", "index.js"))
    }


def sidebar_labels(root):
    """The sidebar labels: page names keep their capitals, like product words."""
    path = os.path.join(root, "webapp_v2", "src", "layout", "Sidebar", "gatewayNav.js")
    try:
        text = open(path, encoding="utf-8").read()
    except OSError:
        return set()
    return set(re.findall(r"""label:\s*['"]([^'"]+)['"]""", text))


def removed_texts(diff):
    """The stripped text of every removed line, to skip lines that only moved."""
    return {
        raw[1:].strip() for raw in diff.splitlines()
        if raw.startswith("-") and not raw.startswith("---") and raw[1:].strip()
    }


def added_lines(diff):
    """Yield (path, line number, text) for each added line."""
    path, num = None, 0
    for raw in diff.splitlines():
        if raw.startswith("+++ "):
            p = raw[4:].strip()
            path = p[2:] if p.startswith("b/") else (None if p == "/dev/null" else p)
        elif raw.startswith("@@"):
            m = re.search(r"\+(\d+)", raw)
            num = int(m.group(1)) if m else 0
        elif path is None:
            continue
        elif raw.startswith("+"):
            yield path, num, raw[1:]
            num += 1
        elif not raw.startswith("-") and not raw.startswith("\\"):
            num += 1


def strings(pattern, text):
    for m in pattern.finditer(text):
        yield next(g for g in m.groups()[::-1] if g is not None) if m.lastindex else m.group(0)


def text_checks(text, pattern, out):
    for s in strings(pattern, text):
        if EM_DASH.search(s):
            out.append(("FAIL", "em dash in user text: use a period, colon or comma"))
        if BUZZ.search(s):
            out.append(("FAIL", f"buzzword '{BUZZ.search(s).group(0)}'"))
        if EMOJI.search(s):
            out.append(("WARN", "emoji in user text"))
        if re.search(r"\w!\s*$", s) or re.search(r"\w!\s*\\n", s):
            out.append(("WARN", "exclamation mark in user text"))


MANTINE_IMPORT = re.compile(r"import\s*{([^}]*)}\s*from\s*['\"]@mantine/core['\"]")


def wrapped_imports(names_text, wrapped):
    names = {n.strip().split(" as ")[0].strip() for n in names_text.split(",")}
    return sorted(names & wrapped)


def multiline_imports(path, lines, wrapped, moved):
    """A Mantine import that spans added lines. check_web reads one line at a
    time, so it sees only the one-line form."""
    out = []
    if WEB_OWNERS.match(path):
        return out
    nums = [n for n, _ in lines]
    text = "\n".join(t for _, t in lines)
    starts = [0]
    for _, t in lines[:-1]:
        starts.append(starts[-1] + len(t) + 1)
    for m in MANTINE_IMPORT.finditer(text):
        if "\n" not in m.group(0):
            continue
        hit = wrapped_imports(m.group(1), wrapped)
        if not hit:
            continue
        i = max(k for k, st in enumerate(starts) if st <= m.start())
        span = [t.strip() for _, t in lines if t.strip() and t.strip() in m.group(0)]
        level = "MOVED" if span and all(t in moved for t in span) else "FAIL"
        out.append((nums[i], level, f"{', '.join(hit)} from @mantine/core: import from @/components/ (webapp_v2/CLAUDE.md, Components)"))
    return out


def check_web(path, text, wrapped, sidebar=frozenset()):
    out = []
    m = MANTINE_IMPORT.search(text)
    if m:
        hit = wrapped_imports(m.group(1), wrapped)
        if hit and not WEB_OWNERS.match(path):
            out.append(("FAIL", f"{', '.join(hit)} from @mantine/core: import from @/components/ (webapp_v2/CLAUDE.md, Components)"))
    if re.search(r"\bstyle=\{\{", text):
        out.append(("FAIL", "style={{}}: use a Mantine prop (webapp_v2/CLAUDE.md, Styling hierarchy)"))
    if re.search(r"\bstyles=\{\{", text):
        out.append(("WARN", "styles={{}} on an instance: move it to Component.extend() (webapp_v2/CLAUDE.md)"))
    if re.search(r"@tabler/icons|react-icons|@radix-ui/react-icons|@heroicons", text):
        out.append(("FAIL", "icon library other than lucide-react"))
    if re.search(r"@mantine/notifications|notifications\.show", text):
        out.append(("FAIL", "Mantine notifications: use showSnackbar (webapp_v2/CLAUDE.md, Snackbars)"))
    if re.search(r"""\b(c|color)=["'](dark|light|blue|yellow|orange)["']""", text):
        out.append(("FAIL", "palette not in the theme (hoop-ui webapp.md, 2. Color)"))
    if re.search(r"<Badge\b[^>]*\bcolor=\{[^}]*indigo", text):
        out.append(("WARN", "Badge color changes with the state and one of them is indigo: indigo is not a status (hoop-ui webapp.md, 2. Color)"))
    for m in TITLE_SLOT.finditer(text):
        label = next(g for g in m.groups() if g)
        words = label.split()
        # Product words keep their capitals; the other words must not.
        named = label
        for name in sorted(sidebar, key=len, reverse=True):
            named = named.replace(name, " ")
        rest = PRODUCT.sub(" ", named).split()
        if len(words) >= 2 and all(w[:1].isupper() for w in words) and len(rest) >= 1 and all(w[:1].isupper() for w in rest[1:] or rest) and not (len(rest) == 1 and words[0] == rest[0]):
            out.append(("WARN", f"'{label}' is in Title Case: use sentence case (hoop-ui webapp.md, 8. Copy)"))
    if re.search(r"""\bclassName=["'][^"']*\b(flex|p-\d|m-\d|text-|bg-|w-\d)""", text):
        out.append(("FAIL", "Tailwind class in webapp_v2"))
    if re.search(r">\s*(OK|Submit|Yes)\s*<", text):
        out.append(("FAIL", "button text 'OK', 'Submit' or 'Yes': start with a verb (hoop-ui webapp.md, 8. Copy)"))
    if re.search(r"Oops|Something went wrong", text):
        out.append(("FAIL", "error text that does not say what failed (hoop-ui webapp.md, 8. Copy)"))
    if not WEB_OWNERS.match(path):
        if re.search(r"""\bbdrs=["'{]""", text):
            out.append(("WARN", "bdrs (a radius by style prop): the theme sets every shape (hoop-ui webapp.md, 3. Shape)"))
        if re.search(r"""\b(bg|c|color|bd)=["']var\(--hoop-""", text):
            out.append(("WARN", "a --hoop-* role token passed as a prop: use it in a CSS Module, or a Paper variant (hoop-ui webapp.md, 2. Color)"))
        if re.search(r"""<Badge\b[^>]*\bcolor=["']indigo["']""", text):
            out.append(("WARN", "indigo Badge: if it shows a state or a count, use gray; indigo is the action (hoop-ui webapp.md, 2. Color)"))
        if re.search(r"""<(Text|Group|Box)\b[^>]*\bc=["']indigo["']""", text):
            out.append(("WARN", "indigo text: if this is a note, use Paper variant=\"info\" and no color (hoop-ui webapp.md, 2. Color)"))
        if re.search(r"""\bradius=["'{]""", text):
            out.append(("WARN", "radius on a component: the theme sets every shape (hoop-ui webapp.md, 3. Shape)"))
        if re.search(r"""\bsize=["']xl["']""", text):
            out.append(("WARN", 'size="xl" on a control: 40px (md) is the default (hoop-ui webapp.md, 9. Check)'))
        if re.search(r"""--mantine-color-[a-z]+-\d|\b(c|color|bg)=["'][a-z]+\.\d["']""", text):
            out.append(("FAIL", 'a palette shade in a page: use a component, c="dimmed" or a --hoop-* role token (hoop-ui webapp.md, 2. Color)'))
        if "--brand-navy" in text:
            out.append(("WARN", "dark navy in a page: only for the auth loader and upsell visuals (hoop-ui webapp.md, 2. Color)"))
        if path.endswith(".css") and re.search(r"^\s*(transition|transform|animation)\s*:", text):
            out.append(("WARN", "motion written in a page: the theme moves controls (hoop-ui webapp.md, 6. Motion)"))
        if path.endswith(".css") and re.search(r"^\s*box-shadow\s*:", text):
            out.append(("WARN", "shadow on page content: use a border or a Paper variant (hoop-ui webapp.md, 2. Color)"))
        if path.endswith(".css") and re.search(r"^\s*border-radius\s*:", text) and "var(--hoop-" not in text:
            out.append(("WARN", "border-radius not from a --hoop-*-radius token (hoop-ui webapp.md, 3. Shape)"))
    if re.search(r"\b(Learn more|Read more|Click here)\b", text):
        out.append(("WARN", "bare 'Learn more', 'Read more' or 'Click here': name the thing (hoop-ui webapp.md, 8. Copy)"))
    if re.search(r"\bsuccessfully\b", text, re.I):
        out.append(("WARN", "'successfully': the past tense says it (hoop-ui webapp.md, 8. Copy)"))
    if re.search(r"\blorem ipsum\b", text, re.I):
        out.append(("WARN", "placeholder text left in (hoop-ui webapp.md, 8. Copy)"))
    if "AI Session Analyzer" in text:
        out.append(("WARN", "'AI Session Analyzer': the product word is 'AI Analyzer' (hoop-ui webapp.md, 8. Copy)"))
    if path.endswith(".css") and re.search(r"#[0-9a-fA-F]{3,8}\b|\brgba?\(", text) and not WEB_OWNERS.match(path):
        out.append(("FAIL", "literal color in CSS: use var(--mantine-*) (themeColors.test.js)"))
    if not path.endswith(".css"):
        text_checks(text, JS_STRING, out)
    return out


def check_cli(path, text):
    out = []
    if re.search(r"\\x1b\[|\\033\[|\\u001b\[", text):
        out.append(("FAIL", "raw escape code: use lipgloss, so NO_COLOR and pipes get plain text (hoop-ui terminal.md, 2. Color)"))
    if not CLI_OWNERS.match(path) and re.search(r"""(lipgloss|termenv)?\.?Color\(\s*"(#[0-9a-fA-F]{3,8}|\d{1,3})"\s*\)|color\(\s*"(#|\d)""", text):
        out.append(("FAIL", "color defined in a command file: name it in the style file (hoop-ui terminal.md, 2. Color)"))
    if re.search(r"#DBAB79|\"204\"", text, re.I):
        out.append(("WARN", "new use of the legacy amber or pink: use gray, blue #3E63DD or red #C4060A (hoop-ui terminal.md, 2. Color, Old helpers)"))
    if re.search(r"tabwriter\.NewWriter", text):
        out.append(("WARN", "tabwriter table: use styles.RenderTable (hoop-ui terminal.md, 4. Layout)"))
    m = re.search(r'\bShort:\s*"([^"]*)"', text)
    if m:
        s = m.group(1)
        if s.endswith("."):
            out.append(("WARN", "help Short ends with a period (hoop-ui terminal.md, 3. Text)"))
        if len(s) > 60:
            out.append(("WARN", f"help Short is {len(s)} characters: keep it under 60 (hoop-ui terminal.md, 3. Text)"))
        if s[:1].islower():
            out.append(("WARN", "help Short starts in lower case: use sentence case (hoop-ui terminal.md, 3. Text)"))
    if re.search(r'Printf?\([^)]*%\+v', text):
        out.append(("WARN", "%+v printed to the person: put details in the debug log (hoop-ui terminal.md, 3. Text)"))
    # The body goes into a message: fmt.Sprintf("... body=%s", e.Body) in an
    # Error() method is the usual shape, as well as Errorf and Printf.
    if re.search(r"(?i)\b(sprintf|errorf|printf|println|print)\(.*(\bbody=%|string\((resp)?body\)|\.body\b)", text):
        out.append(("WARN", "HTTP body in a message: the person may see a whole HTML page; log it at debug (hoop-ui terminal.md, 3. Text)"))
    if re.search(r"(?i)print[^\n]*\b(tls_?ca|ca_?cert|cert(ificate)?|private_?key)\w*\b\s*[=:]\s*%[sv]", text):
        out.append(("WARN", "a long value (a certificate or key) may be printed: print yes or no (hoop-ui terminal.md, 3. Text)"))
    if re.search(r"\[(Y/N|y/n|yes/no)\]", text):
        out.append(("WARN", "prompt choices: show the default in capitals, [y/N] or [Y/n] (hoop-ui terminal.md, 5. Prompts)"))
    if re.search(r"(?i)print[^\n]*\b(token|password|secret|api_?key)\b\s*[=:]\s*%", text):
        out.append(("WARN", "a secret may be printed: print OMITTED (hoop-ui terminal.md, 3. Text)"))
    text_checks(text, GO_STRING, out)
    return out


def missing_examples(lines):
    """A new cobra command that runs something and has no Example field.

    lines: (line number, text) of the added lines of one Go file, in order.
    A command block runs from `cobra.Command{` to the next one.
    """
    out = []
    starts = [i for i, (_, t) in enumerate(lines) if "cobra.Command{" in t]
    for k, i in enumerate(starts):
        block = [t for _, t in lines[i:starts[k + 1] if k + 1 < len(starts) else len(lines)]]
        runs = any(re.search(r"\b(RunE?|PreRunE?)\s*:", t) for t in block)
        if runs and not any(re.search(r"\bExample\s*:", t) for t in block):
            out.append((lines[i][0], "new command with no Example: add a real command with invented names (hoop-ui terminal.md, 3. Text)"))
    return out


BADGE_COLOR = re.compile(r"""<Badge\b[^>]*\bcolor=["'](\w+)["']""")
STATUS_COLORS = {"green", "amber", "red", "gray"}


def badge_swaps(badges):
    """An indigo Badge near a status-colored Badge in one file: the same badge
    drawn in two colors by state (if/else). A lone indigo label is fine."""
    out = []
    for num, color in badges:
        if color != "indigo":
            continue
        near = [c for n, c in badges if c in STATUS_COLORS and abs(n - num) <= 12]
        if near:
            out.append((num, f"Badge is {near[0]} in one state and indigo in another: indigo is not a status, use gray (hoop-ui webapp.md, 2. Color)"))
    return out


def main():
    diff = sys.stdin.read()
    if not diff.strip():
        print("No diff on stdin. Example: gh pr diff 123 | python3 check_diff.py")
        return 2
    wrapped = wrapped_components(repo_root())
    moved = removed_texts(diff)
    fails = warns = moves = 0
    sidebar = sidebar_labels(repo_root())
    badges = {}
    go_lines = {}
    web_lines = {}
    report = []
    for path, num, text in added_lines(diff):
        if CLI.match(path) and not path.endswith("_test.go"):
            go_lines.setdefault(path, []).append((num, text))
        if WEB.match(path) and ".test." not in path:
            web_lines.setdefault(path, []).append((num, text))
        # Badges count even on moved lines: the pair can be one old, one new.
        m = BADGE_COLOR.search(text)
        if m and WEB.match(path):
            badges.setdefault(path, []).append((num, m.group(1)))
        if text.lstrip().startswith(("//", "*", "/*")):
            continue
        if WEB.match(path) and ".test." not in path:
            found = check_web(path, text, wrapped, sidebar)
        elif CLI.match(path) and not path.endswith("_test.go"):
            found = check_cli(path, text)
        elif COPY_ONLY.match(path):
            found = []
            text_checks(text, GO_STRING, found)
        else:
            continue
        # A moved line keeps its finding: the review flags it once, in its new place.
        was_moved = text.strip() in moved
        report += [(path, num, "MOVED" if was_moved else level, msg) for level, msg in found]
    for path, items in badges.items():
        report += [(path, num, "WARN", msg) for num, msg in badge_swaps(items)]
    for path, lines in web_lines.items():
        report += [(path, num, level, msg) for num, level, msg in multiline_imports(path, lines, wrapped, moved)]
    for path, lines in go_lines.items():
        report += [(path, num, "WARN", msg) for num, msg in missing_examples(lines)]
    for path, num, level, msg in sorted(report, key=lambda r: (r[0], r[1])):
        print(f"{level} {path}:{num}: {msg}")
        fails += level == "FAIL"
        warns += level == "WARN"
        moves += level == "MOVED"
    print(f"\n{fails} FAIL, {warns} WARN, {moves} MOVED (old code in a new place: flag once)")
    return 1 if fails else 0


if __name__ == "__main__":
    sys.exit(main())
