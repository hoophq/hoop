#!/usr/bin/env bash
# SessionStart hook: with GIT_SIGNING_KEY set (the support agent's cloud
# environment), sign every commit with that GPG key, so GitHub shows the
# owner's commits as Verified. Elsewhere it does nothing. The import is here
# because an environment's setup script cannot read the environment's
# variables. The cloud sandbox signs with its own SSH key (gpg.format=ssh);
# this switches git to OpenPGP.
#
# GIT_SIGNING_KEY: the armored secret key, base64 in one line.
[[ -n ${GIT_SIGNING_KEY:-} ]] || exit 0

key=$(printf '%s' "$GIT_SIGNING_KEY" | base64 -d 2>/dev/null)
fpr=$(gpg --batch --with-colons --import-options show-only --import <<<"$key" 2>/dev/null |
  awk -F: '$1 == "fpr" { print $10; exit }')
if [[ -n $fpr ]] && gpg --batch --import <<<"$key" 2>/dev/null &&
  git config --global gpg.format openpgp &&
  git config --global user.signingkey "$fpr" &&
  git config --global commit.gpgsign true; then
  echo "Commits are signed with GPG key $fpr."
else
  # stdout reaches the session: it must know its commits are not signed.
  echo "GIT_SIGNING_KEY is set but could not be imported: commits are NOT signed with it."
fi
exit 0
