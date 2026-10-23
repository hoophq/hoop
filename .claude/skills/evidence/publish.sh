#!/usr/bin/env bash
# Publish evidence images for a PR. Cloud sessions cannot create gists, so the
# files go to the orphan branch claude/evidence, one folder per ticket, and
# the PR links them by commit SHA (a later push never changes a link).
#
#   publish.sh <TICKET-ID> <file>...
#
# Prints one Markdown image line per file, ready for the PR's "Testing".
# The repo is public: no customer data, tokens, internal hosts or real emails.
set -euo pipefail

[[ $# -ge 2 ]] || { echo "usage: $0 <TICKET-ID> <file>..." >&2; exit 1; }
folder=$1; shift
for f in "$@"; do [[ -f $f ]] || { echo "no such file: $f" >&2; exit 1; }; done

branch=claude/evidence
slug=$(git remote get-url origin | sed -E 's#^.*github\.com[:/]##; s#\.git$##')
tmp=$(mktemp -d); trap 'git worktree remove --force "$tmp/wt" >/dev/null 2>&1; rm -rf "$tmp"' EXIT

if git fetch -q origin "$branch" 2>/dev/null; then
  git worktree add -q --detach "$tmp/wt" FETCH_HEAD
else
  git worktree add -q --detach "$tmp/wt"
  git -C "$tmp/wt" checkout -q --orphan "$branch"
  git -C "$tmp/wt" rm -rq --cached . && git -C "$tmp/wt" clean -fdxq
fi

mkdir -p "$tmp/wt/$folder"
cp "$@" "$tmp/wt/$folder/"
git -C "$tmp/wt" add "$folder"
git -C "$tmp/wt" diff --cached --quiet || git -C "$tmp/wt" commit -q -m "Evidence for $folder"

# Another run can push first: each run writes its own folder, so a rebase
# never conflicts.
for try in 1 2 3; do
  git -C "$tmp/wt" push -q origin "HEAD:refs/heads/$branch" 2>/dev/null && break
  [[ $try == 3 ]] && { echo "push to $branch failed 3 times" >&2; exit 1; }
  git -C "$tmp/wt" fetch -q origin "$branch" && git -C "$tmp/wt" rebase -q FETCH_HEAD
done

sha=$(git -C "$tmp/wt" rev-parse HEAD)
for f in "$@"; do
  name=$(basename "$f")
  echo "![${name%.*}](https://raw.githubusercontent.com/$slug/$sha/$folder/$name)"
done
