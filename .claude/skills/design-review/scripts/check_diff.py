#!/usr/bin/env python3
"""Flag design rule breaks on the added lines of a unified diff.

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

EM_DASH = re.compile(r"\u2014")
BUZZ = re.compile(r"\b(seamless(ly)?|cutting-edge|revolutionary|next-generation|robust|holistic|leverage|utilize)\b", re.I)
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
    r"Resource Role|Resource|Agent|Sidecar|Guardrail|Data Masking|AI Analyzer|AI Session Analyzer|Session|Runbook"
    r"|Approval|Access Control|Access Request|Enterprise|Hoop|Slack|Teams|Jira|MCP|API|SSO|AWS|GCP"
    r"|Compliance Pack|Vault|Access Key|\bID\b|\b[A-Z]\b"
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


def check_web(path, text, wrapped):
    out = []
    m = re.search(r"import\s*{([^}]*)}\s*from\s*['\"]@mantine/core['\"]", text)
    if m:
        names = {n.strip().split(" as ")[0] for n in m.group(1).split(",")}
        hit = sorted(names & wrapped)
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
        out.append(("FAIL", "palette not in the theme (webapp.md, Color meaning)"))
    if re.search(r"<Badge\b[^>]*\bcolor=[\"'{`]*indigo", text):
        out.append(("WARN", "indigo Badge: fine for a label (Recommended, Next), not for a state or a count (webapp.md, Color meaning)"))
    for m in TITLE_SLOT.finditer(text):
        label = next(g for g in m.groups() if g)
        words = label.split()
        # Product words keep their capitals; the other words must not.
        rest = PRODUCT.sub(" ", label).split()
        if len(words) >= 2 and all(w[:1].isupper() for w in words) and len(rest) >= 1 and all(w[:1].isupper() for w in rest[1:] or rest) and not (len(rest) == 1 and words[0] == rest[0]):
            out.append(("WARN", f"'{label}' is in Title Case: use sentence case (webapp.md, Copy)"))
    if re.search(r"""\bclassName=["'][^"']*\b(flex|p-\d|m-\d|text-|bg-|w-\d)""", text):
        out.append(("FAIL", "Tailwind class in webapp_v2"))
    if re.search(r">\s*(OK|Submit|Yes)\s*<", text):
        out.append(("FAIL", "button text 'OK', 'Submit' or 'Yes': start with a verb (webapp.md, Copy)"))
    if re.search(r"Oops|Something went wrong", text):
        out.append(("FAIL", "error text that does not say what failed (webapp.md, Copy)"))
    if path.endswith(".css") and re.search(r"#[0-9a-fA-F]{3,8}\b|\brgba?\(", text) and not WEB_OWNERS.match(path):
        out.append(("FAIL", "literal color in CSS: use var(--mantine-*) (themeColors.test.js)"))
    if not path.endswith(".css"):
        text_checks(text, JS_STRING, out)
    return out


def check_cli(path, text):
    out = []
    if re.search(r"\\x1b\[|\\033\[|\\u001b\[", text):
        out.append(("FAIL", "raw escape code: use lipgloss, so NO_COLOR and pipes get plain text (terminal.md, Color)"))
    if not CLI_OWNERS.match(path) and re.search(r"""(lipgloss|termenv)?\.?Color\(\s*"(#[0-9a-fA-F]{3,8}|\d{1,3})"\s*\)|color\(\s*"(#|\d)""", text):
        out.append(("FAIL", "color defined in a command file: name it in the style file (terminal.md, Color)"))
    if re.search(r"#DBAB79|\"204\"", text, re.I):
        out.append(("WARN", "new use of the legacy amber or pink: use gray, blue #3E63DD or red #C4060A (terminal.md, Known debt)"))
    if re.search(r"tabwriter\.NewWriter", text):
        out.append(("WARN", "tabwriter table: use styles.RenderTable (terminal.md, Layout)"))
    m = re.search(r'\bShort:\s*"([^"]*)"', text)
    if m:
        s = m.group(1)
        if s.endswith("."):
            out.append(("WARN", "help Short ends with a period"))
        if len(s) > 60:
            out.append(("WARN", f"help Short is {len(s)} characters: keep it under 60"))
        if s[:1].islower():
            out.append(("WARN", "help Short starts in lower case: use sentence case"))
    if re.search(r'Printf?\([^)]*%\+v', text):
        out.append(("WARN", "%+v printed to the person: put details in the debug log (terminal.md, Text)"))
    if re.search(r"body=%|string\(body\)|string\(respBody\)", text) and re.search(r"Errorf|Printf|Println|Exit", text):
        out.append(("WARN", "HTTP body in a message: the person may see an HTML page; log it at debug (terminal.md, Text)"))
    if re.search(r"\[(Y/N|y/n|yes/no)\]", text):
        out.append(("WARN", "prompt choices: show the default in capitals, [y/N] or [Y/n] (terminal.md, Prompts)"))
    if re.search(r"(?i)print[^\n]*\b(token|password|secret|api_?key)\b\s*[=:]\s*%", text):
        out.append(("WARN", "a secret may be printed: print OMITTED (terminal.md, Text)"))
    text_checks(text, GO_STRING, out)
    return out


def main():
    diff = sys.stdin.read()
    if not diff.strip():
        print("No diff on stdin. Example: gh pr diff 123 | python3 check_diff.py")
        return 2
    wrapped = wrapped_components(repo_root())
    moved = removed_texts(diff)
    fails = warns = 0
    for path, num, text in added_lines(diff):
        if text.lstrip().startswith(("//", "*", "/*")) or text.strip() in moved:
            continue
        if WEB.match(path) and ".test." not in path:
            found = check_web(path, text, wrapped)
        elif CLI.match(path) and not path.endswith("_test.go"):
            found = check_cli(path, text)
        else:
            continue
        for level, msg in found:
            print(f"{level} {path}:{num}: {msg}")
            fails += level == "FAIL"
            warns += level == "WARN"
    print(f"\n{fails} FAIL, {warns} WARN")
    return 1 if fails else 0


if __name__ == "__main__":
    sys.exit(main())
