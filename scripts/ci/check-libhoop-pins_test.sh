#!/usr/bin/env bash
# Fixture tests for check-libhoop-pins.sh. A stub gh answers the GitHub API
# from the table below, so no token or network is needed.
#
# Usage: scripts/ci/check-libhoop-pins_test.sh   (from the repo root)

set -euo pipefail

CHECK=$(cd "$(dirname "$0")" && pwd)/check-libhoop-pins.sh
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

ON_MAIN=fb73babb25df   # compare status: identical
BEHIND=20617fb21eb0    # compare status: ahead (main is ahead of it)
OFF_MAIN=1497bdcb0715  # compare status: diverged
API_DOWN=aaaaaaaaaaaa  # compare fails with HTTP 500
TAG=v1.2.3
PRE_TAG=v0.1.0-preview.20261021183309-0123456789ab
V2_TAG=v2.0.0

mkdir "$WORK/bin"
cat >"$WORK/bin/gh" <<EOF
#!/usr/bin/env bash
case "\$2" in
  repos/hoophq/libhoop/compare/$ON_MAIN...main) echo identical ;;
  repos/hoophq/libhoop/compare/$BEHIND...main) echo ahead ;;
  repos/hoophq/libhoop/compare/$OFF_MAIN...main) echo diverged ;;
  repos/hoophq/libhoop/compare/$API_DOWN...main) echo "gh: Server Error (HTTP 500)" >&2; exit 1 ;;
  repos/hoophq/libhoop/git/ref/tags/$TAG | repos/hoophq/libhoop/git/ref/tags/$PRE_TAG | repos/hoophq/libhoop/git/ref/tags/$V2_TAG) echo "refs/tags/\${2##*/}" ;;
  *) echo "gh: Not Found (HTTP 404)" >&2; exit 1 ;;
esac
EOF
chmod +x "$WORK/bin/gh"

pseudo() { echo "v0.0.0-20261021183309-$1"; }
gomod() { # $1=module $2=extra lines
  printf 'module example.com/%s\n\ngo 1.26\n\n%s\n' "$1" "$2"
}
req() { echo "require github.com/hoophq/libhoop $1"; }

passed=0 failed=0
# run <name> <want exit> <want output, or ""> <file> <content> [<file> <content>...]
run() {
  local name=$1 want=$2 grep_for=$3 dir out code line
  shift 3
  dir=$(mktemp -d "$WORK/case.XXXX")
  git -C "$dir" init -q
  while [ $# -gt 0 ]; do
    mkdir -p "$dir/$(dirname "$1")"
    printf '%s\n' "$2" >"$dir/$1"
    shift 2
  done
  git -C "$dir" add -A
  code=0
  out=$(cd "$dir" && PATH="$WORK/bin:$PATH" "$CHECK" 2>&1) || code=$?
  if [ "$code" -ne "$want" ] || { [ -n "$grep_for" ] && ! grep -qF -- "$grep_for" <<<"$out"; }; then
    echo "FAIL: $name: exit $code, want $want; output:"
    while IFS= read -r line; do echo "    $line"; done <<<"$out"
    failed=$((failed + 1))
  else
    echo "ok: $name"
    passed=$((passed + 1))
  fi
}

run "pin on main" 0 "" \
  a/go.mod "$(gomod a "$(req "$(pseudo $ON_MAIN)")")"
run "pin behind main" 0 "" \
  a/go.mod "$(gomod a "$(req "$(pseudo $BEHIND)")")"
run "pseudo-version after a tag" 0 "" \
  a/go.mod "$(gomod a "$(req "v1.150.1-0.20261021183309-$ON_MAIN")")"
run "pseudo-version after a prerelease" 0 "" \
  a/go.mod "$(gomod a "$(req "v1.2.3-rc.1.0.20261021183309-$ON_MAIN")")"
run "pin off main" 1 "a/go.mod::github.com/hoophq/libhoop $(pseudo $OFF_MAIN): commit $OFF_MAIN is not on hoophq/libhoop main (diverged)" \
  a/go.mod "$(gomod a "$(req "$(pseudo $OFF_MAIN)")")"
run "pin off main, every file named" 1 "b/go.mod::" \
  a/go.mod "$(gomod a "$(req "$(pseudo $OFF_MAIN)")")" \
  b/go.mod "$(gomod b "$(req "$(pseudo $OFF_MAIN)")")"
run "unknown commit" 1 "commit deadbeef0000 is not in hoophq/libhoop" \
  a/go.mod "$(gomod a "$(req "$(pseudo deadbeef0000)")")"
run "API error is not a missing commit" 1 "could not compare $API_DOWN" \
  a/go.mod "$(gomod a "$(req "$(pseudo $API_DOWN)")")"
run "existing tag" 0 "" \
  a/go.mod "$(gomod a "$(req $TAG)")"
run "missing tag" 1 "tag v1.9.9 does not exist" \
  a/go.mod "$(gomod a "$(req v1.9.9)")"
run "prerelease tag ending in a timestamp and hash is a tag" 0 "" \
  a/go.mod "$(gomod a "$(req $PRE_TAG)")"
run "+incompatible version reads the bare tag" 0 "" \
  a/go.mod "$(gomod a "$(req "$V2_TAG+incompatible")")"
run "+incompatible version with no tag" 1 "tag v3.0.0 does not exist" \
  a/go.mod "$(gomod a "$(req "v3.0.0+incompatible")")"
run "local replace in go.mod" 1 "local:../libhoop: replaced with a local path" \
  a/go.mod "$(gomod a "$(req "$(pseudo $ON_MAIN)")
replace github.com/hoophq/libhoop => ../libhoop")"
run "replace to a version off main" 1 "commit $OFF_MAIN is not on hoophq/libhoop main" \
  a/go.mod "$(gomod a "$(req "$(pseudo $ON_MAIN)")
replace github.com/hoophq/libhoop => github.com/hoophq/libhoop $(pseudo $OFF_MAIN)")"
run "replace with a fork" 1 "fork:github.com/someone/libhoop@$(pseudo $ON_MAIN): replaced with another module" \
  a/go.mod "$(gomod a "$(req "$(pseudo $ON_MAIN)")
replace github.com/hoophq/libhoop => github.com/someone/libhoop $(pseudo $ON_MAIN)")"
run "go.work local replace" 1 "go.work::github.com/hoophq/libhoop local:./libhoop" \
  a/go.mod "$(gomod a "$(req "$(pseudo $ON_MAIN)")")" \
  go.work "$(printf 'go 1.26\n\nuse ./a\n\nreplace github.com/hoophq/libhoop => ./libhoop')"
run "go.work replace to a version off main" 1 "go.work::github.com/hoophq/libhoop $(pseudo $OFF_MAIN)" \
  a/go.mod "$(gomod a "$(req "$(pseudo $ON_MAIN)")")" \
  go.work "$(printf 'go 1.26\n\nuse ./a\n\nreplace github.com/hoophq/libhoop => github.com/hoophq/libhoop %s' "$(pseudo $OFF_MAIN)")"
run "other hoophq modules are not checked" 0 "" \
  a/go.mod "$(gomod a "$(req "$(pseudo $ON_MAIN)")
require github.com/hoophq/libhoop-ica $(pseudo $OFF_MAIN)")"
run "path with a space" 1 "d e/go.mod::" \
  "d e/go.mod" "$(gomod d "$(req "$(pseudo $OFF_MAIN)")")"
run "go.mod that does not parse" 1 "cannot read a/go.mod" \
  a/go.mod "$(gomod a "require github.com/hoophq/libhoop v9.9.9")"
run "no libhoop reference" 1 "no go.mod requires" \
  a/go.mod "$(gomod a "")"

echo "$passed passed, $failed failed"
[ "$failed" -eq 0 ]
