#!/usr/bin/env bash
# Compose labeled panels into one comparison image.
#
#   compare.sh <out.png> <h|v> "LABEL=path.png" "LABEL=path.png" [...]
#
# h: panels side by side (states, screens). v: panels stacked (logs, wide output).
# Needs ffmpeg and ffprobe, and freeze when ffmpeg has no drawtext (label.sh).
set -euo pipefail

if [[ $# -lt 4 ]]; then
  echo "usage: $0 <out.png> <h|v> \"LABEL=path.png\" \"LABEL=path.png\" [...]" >&2
  exit 1
fi
out=$1; dir=$2; shift 2
[[ $dir == h || $dir == v ]] || { echo "direction must be h or v, got '$dir'" >&2; exit 1; }
for t in ffmpeg ffprobe; do
  command -v "$t" >/dev/null || { echo "missing tool: $t" >&2; exit 1; }
done

source "$(dirname "$0")/label.sh"

tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
bg=0x2a2c37

inputs=(); labels=(); widths=(); heights=()
for spec in "$@"; do
  label=${spec%%=*}; path=${spec#*=}
  [[ -f $path ]] || { echo "no such image: $path" >&2; exit 1; }
  inputs+=("$path"); labels+=("$label")
  dims=$(ffprobe -v error -select_streams v:0 -show_entries stream=width,height -of csv=p=0:s=x "$path")
  widths+=("${dims%x*}"); heights+=("${dims#*x}")
done
n=${#inputs[@]}

maxw=0; maxh=0; minw=${widths[0]}
for i in $(seq 0 $((n-1))); do
  (( widths[i] > maxw )) && maxw=${widths[i]}
  (( widths[i] < minw )) && minw=${widths[i]}
  (( heights[i] > maxh )) && maxh=${heights[i]}
done

args=(); filter=""; joined=""
for i in $(seq 0 $((n-1))); do
  label_png "${labels[i]}" "$tmp/l$i.png"
  args+=(-i "${inputs[i]}")
done
for i in $(seq 0 $((n-1))); do args+=(-i "$tmp/l$i.png"); done

# Label band: about 1/9 of the narrowest panel, readable once the image is scaled
# down. One band for all panels: hstack needs equal heights.
if [[ $dir == h ]]; then band=$(( minw / 9 )); else band=$(( maxw / 9 )); fi
(( band < 80 )) && band=80; (( band > 160 )) && band=160
for i in $(seq 0 $((n-1))); do
  if [[ $dir == h ]]; then pw=${widths[i]}; ph=$maxh; else pw=$maxw; ph=${heights[i]}; fi
  filter+="[$i]pad=${pw}:${ph}:0:0:color=0x1d1d1d[i$i];"
  # A label wider than its panel shrinks to fit; pad cannot crop.
  filter+="[$((n+i))]scale=-1:${band},scale='min(iw,${pw})':-1,pad=${pw}:${band}:0:0:color=${bg}[b$i];"
  filter+="[b$i][i$i]vstack[p$i];"
  joined+="[p$i]"
done
if [[ $dir == h ]]; then filter+="${joined}hstack=${n}"; else filter+="${joined}vstack=${n}"; fi
filter+=",scale='min(3000,iw)':-2"

ffmpeg -loglevel error -y "${args[@]}" -filter_complex "$filter" "$out" </dev/null
echo "$out"
