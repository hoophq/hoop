#!/usr/bin/env bash
# Draw the terminal output of one issue before and after its fix.
#
#   term.sh <before.txt> <after.txt> <out.png> [h|v] [dark|light]
#
# The text files hold what the terminal prints, ANSI colors and stderr
# included (`CLICOLOR_FORCE=1 hoop ... > before.txt 2>&1`, or
# `script -q before.txt hoop ...`). Write the "after" file by hand from the
# fix, with the same data as the "before". h (default): side by side; v:
# stacked, for wide output. dark (default) or light: the terminal background,
# to check text that disappears on one of them. Lines wrap at 80 columns, as
# in a standard terminal. Needs freeze, ffmpeg and ffprobe.
set -euo pipefail

if [[ $# -lt 3 ]]; then
  echo "usage: $0 <before.txt> <after.txt> <out.png> [h|v] [dark|light]" >&2
  exit 1
fi
before=$1; after=$2; out=$3; dir=${4:-h}; theme=${5:-dark}
for f in "$before" "$after"; do [[ -f $f ]] || { echo "no such file: $f" >&2; exit 1; }; done
command -v freeze >/dev/null || { echo "missing tool: freeze (brew install charmbracelet/tap/freeze)" >&2; exit 1; }
case $theme in
  dark) bg="#1e1e2e"; fg=$'\e[0;38;2;205;214;244m' ;;
  light) bg="#ffffff"; fg=$'\e[0;38;2;28;32;36m' ;;
  *) echo "theme must be dark or light, not: $theme" >&2; exit 1 ;;
esac
here=$(cd "$(dirname "$0")" && pwd)

tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
# The label is the first line of the window. Red marks the issue (red is
# "broken" in terminal.md). The fix is gray, as success is gray there.
draw() {
  # Wrap at 80 here: freeze 0.2.2 --wrap panics on colored text that wraps.
  # Count visible characters only: an escape code has no width and is never
  # cut. freeze forgets the text color at each new line and each reset (\e[0m
  # or \e[m), so set it again there.
  { printf '%s\n\n' "$2"
    perl -CSD -ne 'chomp; my $n = 0; my $o = "";
      for my $t (split /(\e\[[0-9;?]*[A-Za-z])/) {
        if ($t =~ /^\e\[/) { $o .= $t; next }
        for my $c (split //, $t) { if ($n == 80) { $o .= "\n"; $n = 0 } $o .= $c; $n++ }
      }
      print "$o\n"' "$1" | FG="$fg" perl -pe 's/\e\[0?m/$&$ENV{FG}/g; s/^/$ENV{FG}/'
  } > "$tmp/in.txt"
  # freeze reads ANSI colors from the text; it waits on an open stdin.
  freeze "$tmp/in.txt" --language ansi --window --border.radius 8 --padding 24,30 --margin 0 \
    --font.size 26 --background "$bg" -o "$3" </dev/null >/dev/null
}
draw "$before" $'\e[1;38;2;196;6;10m✕ BEFORE\e[0m' "$tmp/before.png"
draw "$after" $'\e[1;38;2;128;131;141m✓ AFTER\e[0m' "$tmp/after.png"
"$here/pair.sh" "$tmp/before.png" "$tmp/after.png" "$out" "$dir" >/dev/null
echo "$out"
