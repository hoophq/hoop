#!/usr/bin/env bash
# usage: compose-video.sh <out-basename> "LABEL=left.mp4" "LABEL=right.mp4"
# Writes <out-basename>.mp4 (full size, for Linear) and <out-basename>.gif (1800 px,
# 8 fps, for the PR body). Needs ffmpeg, ffprobe and python3, and freeze when ffmpeg
# has no drawtext (label.sh).
# Side by side, a label band on top of each panel; the shorter side holds its last frame.
set -euo pipefail
out=$1; l=${2%%=*}; lp=${2#*=}; r=${3%%=*}; rp=${3#*=}
source "$(dirname "$0")/label.sh"
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
label_png "$l" "$tmp/l0.png"; label_png "$r" "$tmp/l1.png"
dur() { ffprobe -v error -show_entries format=duration -of csv=p=0 "$1"; }
d=$(python3 -c "print(max($(dur "$lp"), $(dur "$rp")) + 1.5)")
w=$(ffprobe -v error -select_streams v:0 -show_entries stream=width -of csv=p=0 "$lp" | head -1 | tr -d ,)
h=$(ffprobe -v error -select_streams v:0 -show_entries stream=height -of csv=p=0 "$lp" | head -1 | tr -d ,)
band=120
f="color=c=0x2a2c37:s=$((w*2))x$((h+band)):d=${d}:r=15[bg];"
f+="[2]scale=-1:${band}[l0];[3]scale=-1:${band}[l1];"
f+="[bg][l0]overlay=0:0[a];[a][l1]overlay=${w}:0[b];"
f+="[b][0:v]overlay=0:${band}:eof_action=repeat[c];[c][1:v]overlay=${w}:${band}:eof_action=repeat,format=yuv420p[v]"
ffmpeg -loglevel error -y -i "$lp" -i "$rp" -i "$tmp/l0.png" -i "$tmp/l1.png" -filter_complex "$f" -map '[v]' \
  -t "$d" -c:v libx264 -crf 23 -preset slow -movflags +faststart "$out.mp4"
ffmpeg -loglevel error -y -i "$out.mp4" -vf "fps=8,scale=1800:-1:flags=lanczos,split[s0][s1];[s0]palettegen=max_colors=128[p];[s1][p]paletteuse=dither=bayer:bayer_scale=4" "$out.gif"
ls -la "$out.mp4" "$out.gif"
