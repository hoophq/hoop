#!/usr/bin/env bash
# Tests for git-signing.sh. Run: bash .claude/hooks/git-signing_test.sh
set -u
hook="$(cd "$(dirname "$0")" && pwd)/git-signing.sh"
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
fail=0

# fresh_home: an empty HOME and GNUPGHOME, with git set up for SSH signing
# like the cloud sandbox, so nothing leaks from the caller.
fresh_home() {
  export HOME="$tmp/$1" GNUPGHOME="$tmp/$1/gnupg"
  mkdir -p "$GNUPGHOME"; chmod 700 "$GNUPGHOME"
  git config --global gpg.format ssh
}

check() {
  if [[ $2 == "$3" ]]; then echo "ok   $1"; else echo "FAIL $1: got '$2', want '$3'"; fail=1; fi
}

# A throwaway key, made and exported the way .claude/README.md says.
fresh_home maker
gpg --batch --passphrase '' --quick-generate-key "Test <test@example.com>" ed25519 sign 1d 2>/dev/null
want=$(gpg --with-colons --list-secret-keys test@example.com 2>/dev/null | awk -F: '$1 == "fpr" { print $10; exit }')
good=$(gpg --armor --export-secret-keys "$want" | base64 | tr -d '\n')

fresh_home unset
out=$(env -u GIT_SIGNING_KEY bash "$hook"); check "unset: no output" "$out" ""
check "unset: git config untouched" "$(git config --global --get gpg.format)" "ssh"

fresh_home bad
out=$(GIT_SIGNING_KEY=not-a-key bash "$hook"); check "bad key: exit 0" "$?" "0"
check "bad key: says not signed" "${out#*NOT signed}" " with it."
check "bad key: git config untouched" "$(git config --global --get gpg.format)" "ssh"

fresh_home good
out=$(GIT_SIGNING_KEY=$good bash "$hook"); check "good key: message" "$out" "Commits are signed with GPG key $want."
check "good key: format" "$(git config --global --get gpg.format)" "openpgp"
check "good key: signing key" "$(git config --global --get user.signingkey)" "$want"
git init -q "$tmp/repo"
git -C "$tmp/repo" -c user.name=Test -c user.email=test@example.com commit -q --allow-empty -m t
git -C "$tmp/repo" verify-commit HEAD 2>/dev/null; check "good key: commit verifies" "$?" "0"

# Repo and user settings both register the hook: two runs start at once.
fresh_home twice
for i in 1 2 3 4; do GIT_SIGNING_KEY=$good bash "$hook" >"$tmp/twice.$i" & done; wait
out=$(sort -u "$tmp"/twice.*); check "four at once: all signed" "$out" "Commits are signed with GPG key $want."
check "four at once: signing key" "$(git config --global --get user.signingkey)" "$want"

exit $fail
