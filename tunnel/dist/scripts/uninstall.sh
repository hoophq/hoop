#!/usr/bin/env sh
# uninstall.sh — companion to install.sh. Removes the systemd service
# registration; `--purge` also drops the config + group + binary.
#
# The wrapper shape mirrors install.sh: detect sibling daemon, sudo
# elevation, hand off to the Go binary. Everything destructive lives
# in `hsh-tunneld uninstall` so there's exactly one place that knows
# the install layout.

set -eu

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)

# After a successful install the daemon lives at /usr/local/bin (the
# Go binary copied itself there). Prefer that location on uninstall
# so a user who deleted the tarball directory can still remove the
# service — they only need this script in their shell history.
DAEMON_TARBALL="$SCRIPT_DIR/hsh-tunneld"
DAEMON_INSTALLED="/usr/local/bin/hsh-tunneld"

if [ -x "$DAEMON_INSTALLED" ]; then
    DAEMON="$DAEMON_INSTALLED"
elif [ -x "$DAEMON_TARBALL" ]; then
    DAEMON="$DAEMON_TARBALL"
else
    printf '\033[31m%s\033[0m\n' \
        "error: could not find hsh-tunneld."
    printf '       Looked at: %s\n' "$DAEMON_INSTALLED"
    printf '                  %s\n' "$DAEMON_TARBALL"
    printf '       If you have an installed daemon at a non-default path, run:\n'
    printf '         sudo <path-to-hsh-tunneld> uninstall %s\n' "$@"
    exit 1
fi

# Sudo elevation. Same rationale as install.sh.
if [ "$(id -u)" != "0" ]; then
    if ! command -v sudo >/dev/null 2>&1; then
        printf '\033[31m%s\033[0m\n' \
            "error: this script needs root, and sudo is not available."
        exit 1
    fi
    printf '\033[1m==> re-executing under sudo\033[0m\n'
    exec sudo -- "$0" "$@"
fi

printf '\033[1m==> uninstalling hsh-tunneld\033[0m\n'
"$DAEMON" uninstall "$@"

printf '\n\033[32mUninstalled.\033[0m\n'
if echo " $* " | grep -q ' --purge '; then
    printf 'Config, group, and binary removed (--purge).\n'
else
    printf 'To also remove config and group, re-run with --purge.\n'
fi
