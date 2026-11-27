#!/usr/bin/env bash
# Put two images side by side (or stacked) on one ground, tops aligned.
#
#   pair.sh <before.png> <after.png> <out.png> [h|v]
#
# The images carry their own labels (shot.mjs and term.sh draw them).
# Needs ffmpeg and ffprobe.
set -euo pipefail

if [[ $# -lt 3 ]]; then
  echo "usage: $0 <before.png> <after.png> <out.png> [h|v]" >&2
  exit 1
fi
a=$1; b=$2; out=$3; dir=${4:-h}
for f in "$a" "$b"; do [[ -f $f ]] || { echo "no such image: $f" >&2; exit 1; }; done
size() { ffprobe -v error -select_streams v:0 -show_entries stream=width,height -of csv=p=0:s=x "$1"; }
da=$(size "$a"); db=$(size "$b")
wa=${da%x*}; ha=${da#*x}; wb=${db%x*}; hb=${db#*x}
gap=32; ground=0xF1F3F5
if [[ $dir == h ]]; then
  h=$(( ha > hb ? ha : hb ))
  f="[0]pad=$(( wa + gap )):$h:0:0:color=$ground[a];[1]pad=$wb:$h:0:0:color=$ground[b];[a][b]hstack=2"
else
  w=$(( wa > wb ? wa : wb ))
  f="[0]pad=$w:$(( ha + gap )):0:0:color=$ground[a];[1]pad=$w:$hb:0:0:color=$ground[b];[a][b]vstack=2"
fi
ffmpeg -loglevel error -y -i "$a" -i "$b" \
  -filter_complex "$f,pad=iw+64:ih+64:32:32:color=$ground,scale='min(2400,iw)':-2" -frames:v 1 "$out" </dev/null
echo "$out"
