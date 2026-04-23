#!/bin/bash
set -euo pipefail

TARGET=1000
TMP_FILE="$(mktemp)"

adjectives=(amber arctic atomic azure brisk calm clever cosmic crimson dusty eager electric fancy foggy golden happy icy jazzy keen lucid magic mellow minty nimble noble oceanic olive pearl quick radiant royal rustic sandy silver solar spicy stable stellar stone stormy sunny sweet swift tiny velvet vivid warm wild windy young zesty)
nouns=(acacia aurora canyon cedar comet coral creek dahlia dawn delta desert dune ember equinox fjord flora forest galaxy glade harbor horizon iris lagoon lichen meadow mesa meteor mist moon moss nebula oasis orchid pebble pine prism quartz ravine ridge river savanna shadow sky spring star summit tide valley willow zenith)

# Gera 1000 nomes únicos
while [ "$(wc -l < "$TMP_FILE")" -lt "$TARGET" ]; do
  adj=${adjectives[$((RANDOM % ${#adjectives[@]}))]}
  noun=${nouns[$((RANDOM % ${#nouns[@]}))]}
  num=$(printf "%04d" $((RANDOM % 10000)))
  name="${adj}-${noun}-${num}"
  grep -qxF "$name" "$TMP_FILE" 2>/dev/null || echo "$name" >> "$TMP_FILE"
done

# Cria as conexões
while IFS= read -r name; do
  hoop admin create connection "$name" -a default \
    --type custom \
    --schema=enabled \
    --skip-validation \
    -- bash
done < "$TMP_FILE"

rm -f "$TMP_FILE"
