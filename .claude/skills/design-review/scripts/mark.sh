#!/usr/bin/env bash
# Mark the spot of one issue on a screenshot: crop around it, draw a red box.
#
#   mark.sh <in.png> <x> <y> <w> <h> <out.png> [pad]
#
# x, y, w, h: the box, in the pixels of in.png. pad: the space kept around the
# box (default 120). The crop stays inside the image. Needs ffmpeg and ffprobe.
set -euo pipefail

if [[ $# -lt 6 ]]; then
  echo "usage: $0 <in.png> <x> <y> <w> <h> <out.png> [pad]" >&2
  exit 1
fi
in=$1; x=$2; y=$3; w=$4; h=$5; out=$6; pad=${7:-120}
[[ -f $in ]] || { echo "no such image: $in" >&2; exit 1; }
for t in ffmpeg ffprobe; do
  command -v "$t" >/dev/null || { echo "missing tool: $t" >&2; exit 1; }
done

dims=$(ffprobe -v error -select_streams v:0 -show_entries stream=width,height -of csv=p=0:s=x "$in")
iw=${dims%x*}; ih=${dims#*x}
(( x >= 0 && y >= 0 && w > 0 && h > 0 && x + w <= iw && y + h <= ih )) ||
  { echo "box ${x},${y} ${w}x${h} is outside the ${iw}x${ih} image" >&2; exit 1; }

cx=$(( x - pad < 0 ? 0 : x - pad )); cy=$(( y - pad < 0 ? 0 : y - pad ))
ex=$(( x + w + pad > iw ? iw : x + w + pad )); ey=$(( y + h + pad > ih ? ih : y + h + pad ))
cw=$(( ex - cx )); ch=$(( ey - cy ))
# The box is drawn 6px outside the spot, so it does not cover what it marks.
bx=$(( x - cx - 6 < 0 ? 0 : x - cx - 6 )); by=$(( y - cy - 6 < 0 ? 0 : y - cy - 6 ))

ffmpeg -loglevel error -y -i "$in" \
  -vf "crop=${cw}:${ch}:${cx}:${cy},drawbox=x=${bx}:y=${by}:w=$(( w + 12 )):h=$(( h + 12 )):color=0xE5484D:t=4" \
  -frames:v 1 "$out" </dev/null
echo "$out"
