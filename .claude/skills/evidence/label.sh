# Sourced by compare.sh and compose-video.sh.
#
#   label_png <text> <out.png>   the label as light text on a dark box
#
# It uses ffmpeg drawtext when ffmpeg has it (Linux), else freeze: Homebrew
# ffmpeg has no drawtext, and freeze crashed (SIGSEGV) in a cloud sandbox.

label_font=""
# Not a pipe into grep -q: under pipefail, ffmpeg's SIGPIPE fails the test.
if [[ $(ffmpeg -hide_banner -filters 2>/dev/null </dev/null) == *' drawtext '* ]]; then
  for f in "$(fc-match -f '%{file}' 'monospace:spacing=mono' 2>/dev/null)" \
    /usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf; do
    [[ -f $f ]] && { label_font=$f; break; }
  done
fi
[[ -n $label_font ]] || command -v freeze >/dev/null ||
  { echo "missing tool: freeze (or an ffmpeg with drawtext)" >&2; exit 1; }

label_png() {
  local text=$1 out=$2
  printf '%s' "$text" > "$out.txt"
  if [[ -n $label_font ]]; then
    # A monospace glyph is 0.6 em wide; 0.62 keeps the last one inside the box.
    # ${#text} counts bytes outside a UTF-8 locale: the box only gets wider.
    # drawtext puts the baseline at y+max_glyph_a, of this text's glyphs: a fixed
    # baseline keeps every label on one line. Capitals are 0.73 em high.
    local fs=56 h=152
    ffmpeg -loglevel error -y -f lavfi -i "color=c=0x1f2937:s=$(( ${#text} * fs * 62 / 100 + 120 ))x$h" \
      -frames:v 1 -vf "drawtext=fontfile=$label_font:textfile=$out.txt:fontsize=$fs:fontcolor=0xf8f8f2:x=60:y=$(( h / 2 + fs * 73 / 200 ))-max_glyph_a" \
      "$out" </dev/null
  else
    # freeze waits on stdin when it is an open pipe, even with a file argument.
    freeze "$out.txt" --language text --window=false --border.radius 0 \
      --background "#1f2937" --padding 22,30 --margin 0 --font.size 28 \
      --theme dracula -o "$out" </dev/null >/dev/null 2>&1
  fi
}
