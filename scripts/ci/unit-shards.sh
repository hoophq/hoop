#!/usr/bin/env bash
# Split the Go unit suite (go list github.com/hoophq/hoop/...) into shards
# that run on parallel CI runners.
#
# A test binary that boots the embedded PGlite database peaks at ~1 GB. The
# runner was killed when gateway/models and gateway/api/sidecar ran together
# (ENG-760). So the packages whose tests import pglitetest are dealt first, one
# shard each in turn, then the ones that import pglite, then all others. The
# rule reads the import graph: a new package gets a shard on the day it lands.
#
# Usage:
#   unit-shards.sh list <shard> <shards>  print the packages of shard 1..shards
#   unit-shards.sh check <dir>            fail unless the packages-<shard>.txt
#                                         files in <dir> hold each package once

set -euo pipefail

ROOT=github.com/hoophq/hoop
PGLITE=$ROOT/gateway/pglite
PGLITETEST=$PGLITE/pglitetest
# A shard runs at most this many pglitetest packages. Past it, add a leg to
# the test matrix in pullrequest.yml and release.yml.
MAX_PGLITETEST=3

usage() {
  echo "usage: $0 list <shard> <shards> | check <dir>" >&2
  exit 2
}

# One "<tier> <package>" line per package, in dealing order. Tier 1 imports
# pglitetest, tier 2 boots pglite directly, tier 3 is the rest. In tiers 1 and
# 2 the package with the most Test functions comes first: the count follows
# the measured time, so the two slowest packages never share a shard. Same CGO
# setting as make test-unit, and C collation, so every machine gets one order.
ordered() {
  env CGO_ENABLED=0 go list -f '{{.ImportPath}}|{{.Dir}}|{{join .TestGoFiles " "}} {{join .XTestGoFiles " "}}|{{join .TestImports " "}} {{join .XTestImports " "}}' "$ROOT/..." |
    awk -F'|' -v pt="$PGLITETEST" -v p="$PGLITE" '{
      tier = ($1 == p) ? 2 : 3
      n = split($4, imports, " ")
      for (i = 1; i <= n; i++) {
        if (imports[i] == pt) tier = 1
        else if (imports[i] == p && tier == 3) tier = 2
      }
      tests = 0
      if (tier < 3 && $3 ~ /[^ ]/) {
        cmd = "cd \"" $2 "\" && cat " $3 " </dev/null | grep -c \"^func Test\""
        cmd | getline tests
        close(cmd)
      }
      print tier, -tests, $1
    }' | LC_ALL=C sort -k1,1n -k2,2n -k3,3 | awk '{ print $1, $3 }'
}

list() {
  local shard=$1 shards=$2 tiers
  [[ $shard =~ ^[1-9][0-9]*$ && $shards =~ ^[1-9][0-9]*$ ]] || usage
  [ "$shard" -le "$shards" ] || usage
  tiers=$(ordered)
  awk -v s="$shard" -v n="$shards" '(NR - 1) % n == s - 1 { print $2 }' <<<"$tiers"
}

check() {
  local dir=$1 tiers
  local files=("$dir"/packages-*.txt)
  [ -e "${files[0]}" ] || { echo "no packages-<shard>.txt in $dir" >&2; exit 1; }
  tiers=$(ordered)
  awk -v max="$MAX_PGLITETEST" '
    FNR == NR { tier[$2] = $1; next }
    FNR == 1 { shard = FILENAME; sub(/.*packages-/, "", shard); sub(/\.txt$/, "", shard); order[++shards] = shard }
    {
      if ($1 in seen) { printf "%s ran in shard %s and in shard %s\n", $1, seen[$1], shard; bad = 1 }
      seen[$1] = shard
      if (!($1 in tier)) { printf "%s ran in shard %s but go list does not have it\n", $1, shard; bad = 1 }
      else if (tier[$1] == 1) heavy[shard]++
    }
    END {
      for (p in tier) if (!(p in seen)) { printf "%s ran in no shard\n", p; bad = 1 }
      for (i = 1; i <= shards; i++) {
        s = order[i]; per = per sprintf(" %s=%d", s, heavy[s])
        if (heavy[s] > max) { printf "shard %s ran %d pglitetest packages, more than %d: add a shard\n", s, heavy[s], max; bad = 1 }
      }
      if (bad) exit 1
      n = 0; for (p in tier) n++
      printf "%d packages, each in exactly one of %d shards; pglitetest packages per shard:%s\n", n, shards, per
    }' - "${files[@]}" <<<"$tiers"
}

case "${1:-}" in
  list) [ "$#" -eq 3 ] || usage; list "$2" "$3" ;;
  check) [ "$#" -eq 2 ] || usage; check "$2" ;;
  *) usage ;;
esac
