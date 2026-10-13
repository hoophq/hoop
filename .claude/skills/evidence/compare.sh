#!/usr/bin/env bash
# Compose labeled panels into one comparison image.
#
#   compare.sh <out.png> <h|v> "LABEL=path.png" "LABEL=path.png" [...]
#
# h: panels side by side (states, screens). v: panels stacked (logs, wide output).
# Needs ffmpeg, ffprobe and freeze. Labels are rendered with freeze because
# Homebrew ffmpeg has no drawtext filter.
set -euo pipefail

if [[ $# -lt 4 ]]; then
  echo "usage: $0 <out.png> <h|v> \"LABEL=path.png\" \"LABEL=path.png\" [...]" >&2
  exit 1
fi
out=$1; dir=$2; shift 2
[[ $dir == h || $dir == v ]] || { echo "direction must be h or v, got '$dir'" >&2; exit 1; }
for t in ffmpeg ffprobe freeze; do
  command -v "$t" >/dev/null || { echo "missing tool: $t" >&2; exit 1; }
done

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

maxw=0; maxh=0
for i in $(seq 0 $((n-1))); do
  (( widths[i] > maxw )) && maxw=${widths[i]}
  (( heights[i] > maxh )) && maxh=${heights[i]}
done

args=(); filter=""; joined=""
for i in $(seq 0 $((n-1))); do
  printf '%s' "${labels[i]}" > "$tmp/l$i.txt"
  freeze "$tmp/l$i.txt" --language text --window=false --border.radius 0 \
    --background "#1f2937" --padding 22,30 --margin 0 --font.size 28 \
    --theme dracula -o "$tmp/l$i.png" >/dev/null 2>&1
  args+=(-i "${inputs[i]}")
done
for i in $(seq 0 $((n-1))); do args+=(-i "$tmp/l$i.png"); done

# Label band: about 1/9 of the panel width, readable once the image is scaled down.
for i in $(seq 0 $((n-1))); do
  if [[ $dir == h ]]; then pw=${widths[i]}; ph=$maxh; else pw=$maxw; ph=${heights[i]}; fi
  band=$(( pw / 9 )); (( band < 80 )) && band=80; (( band > 160 )) && band=160
  filter+="[$i]pad=${pw}:${ph}:0:0:color=0x1d1d1d[i$i];"
  filter+="[$((n+i))]scale=-1:${band},pad=${pw}:${band}:0:0:color=${bg}[b$i];"
  filter+="[b$i][i$i]vstack[p$i];"
  joined+="[p$i]"
done
if [[ $dir == h ]]; then filter+="${joined}hstack=${n}"; else filter+="${joined}vstack=${n}"; fi
filter+=",scale='min(3000,iw)':-2"

ffmpeg -loglevel error -y "${args[@]}" -filter_complex "$filter" "$out"
echo "$out"
