#!/usr/bin/env bash
# Fail when a go.mod pins github.com/hoophq/libhoop to a commit that is not on
# libhoop main (ENG-821).
#
# A pseudo-version names a commit, not a ref. A commit that only lives on a
# libhoop PR branch becomes unfetchable when that branch is squash-merged and
# deleted: every module in go.work then fails with "unknown revision", and so
# does every build of each release that carries the pin. #1986 did this on
# 2026-10-21. A commit on main stays fetchable.
#
# A tagged version is accepted when the tag exists. A replace of libhoop with
# a local path is refused: that is `make libhoop-dev`, which edits go.work.
#
# Usage: scripts/ci/check-libhoop-pins.sh   (from the repo root)
# Needs: go, jq, gh with a token that can read hoophq/libhoop (GH_TOKEN).

set -euo pipefail

MODULE=github.com/hoophq/libhoop
REPO=hoophq/libhoop
BRANCH=main

for dep in go jq gh git; do
  command -v "$dep" >/dev/null || { echo "FAIL: missing dependency: $dep" >&2; exit 1; }
done

# One "<go.mod><TAB><version>" line per libhoop reference. A local-path
# replace has no version and prints "-". A go.mod that does not parse stops
# the check.
refs=$(git ls-files -- 'go.mod' '**/go.mod' | while read -r mod; do
  go mod edit -json "$mod" | jq -r --arg m "$MODULE" --arg f "$mod" '
    ((.Require // [])[] | select(.Path == $m) | "\($f)\t\(.Version)"),
    ((.Replace // [])[] | select(.Old.Path == $m) | "\($f)\t\(.New.Version // "-")")' ||
    { echo "FAIL: cannot read $mod" >&2; exit 1; }
done)

if [ -z "$refs" ]; then
  echo "FAIL: no go.mod requires $MODULE; the module list is wrong" >&2
  exit 1
fi

err=$(mktemp)
trap 'rm -f "$err"' EXIT

failed=0
fail() { # $1=version $2=message
  local mod
  while IFS= read -r mod; do
    echo "::error file=$mod::$MODULE $1: $2"
  done < <(awk -F'\t' -v v="$1" '$2 == v { print $1 }' <<<"$refs")
  failed=1
}

while IFS= read -r version; do
  if [ "$version" = "-" ]; then
    fail "$version" "replaced with a local path; use make libhoop-dev (go.work) instead"
    continue
  fi
  # The three pseudo-version forms, as golang.org/x/mod/module/pseudo.go reads them:
  # vX.0.0-yyyymmddhhmmss-<sha>, vX.Y.Z-0.yyyymmddhhmmss-<sha>, vX.Y.Z-pre.0.yyyymmddhhmmss-<sha>.
  # Anything else is a tag, even one that ends in a timestamp and a hash.
  if [[ $version =~ ^v[0-9]+\.(0\.0-|[0-9]+\.[0-9]+-([^+]*\.)?0\.)[0-9]{14}-([0-9a-f]{12})(\+incompatible)?$ ]]; then
    sha=${BASH_REMATCH[3]}
    if ! status=$(gh api "repos/$REPO/compare/$sha...$BRANCH" --jq .status 2>"$err"); then
      if grep -q 'HTTP 404' "$err"; then
        fail "$version" "commit $sha is not in $REPO, or the token cannot read $REPO"
      else
        fail "$version" "could not compare $sha with $REPO $BRANCH: $(tr '\n' ' ' <"$err")"
      fi
      continue
    fi
    case "$status" in
      identical | ahead) echo "ok: $version is on $REPO $BRANCH" ;;
      *) fail "$version" "commit $sha is not on $REPO $BRANCH ($status); merge the libhoop PR and pin its $BRANCH commit" ;;
    esac
  else
    if gh api "repos/$REPO/git/ref/tags/$version" --jq .ref >/dev/null 2>"$err"; then
      echo "ok: $version is a $REPO tag"
    elif grep -q 'HTTP 404' "$err"; then
      fail "$version" "tag $version does not exist in $REPO"
    else
      fail "$version" "could not read tag $version in $REPO: $(tr '\n' ' ' <"$err")"
    fi
  fi
done < <(awk -F'\t' '{ print $2 }' <<<"$refs" | sort -u)

exit "$failed"
