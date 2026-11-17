#!/usr/bin/env bash
# Draw the terminal output of one issue before and after its fix, side by side.
#
#   term.sh <before.txt> <after.txt> <out.png> [h|v]
#
# The text files hold what the terminal prints, ANSI colors included
# (`CLICOLOR_FORCE=1 hoop ... > before.txt`, or `script -q before.txt hoop ...`).
# Write the "after" file by hand from the fix. h (default): side by side;
# v: stacked, for wide output. Needs freeze, ffmpeg and ffprobe.
set -euo pipefail

if [[ $# -lt 3 ]]; then
  echo "usage: $0 <before.txt> <after.txt> <out.png> [h|v]" >&2
  exit 1
fi
before=$1; after=$2; out=$3; dir=${4:-h}
for f in "$before" "$after"; do [[ -f $f ]] || { echo "no such file: $f" >&2; exit 1; }; done
command -v freeze >/dev/null || { echo "missing tool: freeze (brew install charmbracelet/tap/freeze)" >&2; exit 1; }
here=$(cd "$(dirname "$0")" && pwd)

tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
# The label is the first line of the window: red for the issue, green for the fix.
draw() {
  { printf '%s\n\n' "$2"; cat "$1"; } > "$tmp/in.txt"
  # freeze reads ANSI colors from the text; it waits on an open stdin.
  freeze "$tmp/in.txt" --language ansi --window --border.radius 8 --padding 24,30 --margin 0 \
    --font.size 26 --background "#1e1e2e" -o "$3" </dev/null >/dev/null
}
draw "$before" $'\e[1;38;2;229;72;77m✕ BEFORE\e[0m' "$tmp/before.png"
draw "$after" $'\e[1;38;2;48;164;108m✓ AFTER\e[0m' "$tmp/after.png"
"$here/pair.sh" "$tmp/before.png" "$tmp/after.png" "$out" "$dir" >/dev/null
echo "$out"
