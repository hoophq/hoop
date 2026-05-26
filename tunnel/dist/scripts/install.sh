#!/usr/bin/env sh
# install.sh — thin user-facing wrapper around `hsh-tunneld install`.
#
# This script lives inside the release tarball that ships from
# hoophq/hsh GitHub Releases (RD-227 bundles it alongside `hsh` and
# `hsh-tunneld`). The expected tarball layout is:
#
#     hsh-linux-x64/
#       hsh                <- the unprivileged CLI (Bun binary)
#       hsh-tunneld        <- the privileged daemon (Go binary)
#       install.sh         <- this script
#       uninstall.sh
#       LICENSE
#       README.md
#
# Why this script exists rather than asking users to run
# `hsh-tunneld install` directly:
#
#   1. Discoverability — every UNIX install document users read at
#      some point includes a step that says "extract the tarball and
#      run ./install.sh". Conforming to that convention is cheaper
#      than fighting it.
#
#   2. Friendlier error messages — `hsh-tunneld install` is correct
#      but terse; this wrapper detects common foot-guns (missing
#      sudo, missing systemd, wrong arch) and prints a one-line fix
#      before letting the Go binary speak.
#
#   3. Auto-sudo — running it without sudo re-execs under sudo
#      rather than dying with "permission denied". That single nicety
#      cuts the "I ran the install and it errored" support volume
#      we'd otherwise see.
#
# This script does NOT replicate the install logic in shell. All the
# work (writing the unit file, creating the group, daemon-reload,
# systemctl enable + restart) happens inside the Go binary so we
# have one source of truth + the same test coverage as direct CLI
# invocations.

# /bin/sh because we want Alpine + busybox to work, not just bash.
set -eu

# Resolve the directory the script lives in (the extracted tarball
# root). `readlink -f` is GNU + macOS; we avoid it by using POSIX
# constructs only. `$0` may be relative, so we cd to its dirname and
# pwd back the absolute path. Putting this in a subshell keeps the
# user's cwd untouched.
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
DAEMON="$SCRIPT_DIR/hsh-tunneld"

# Pretty-printer helpers. We avoid `echo -e` because dash + busybox
# don't interpret the escapes; printf is portable.
red()   { printf '\033[31m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
bold()  { printf '\033[1m%s\033[0m\n' "$*"; }

# Group name we mention in the post-install next-steps. Must match the
# Go-side default in tunnel/service/service.go::PlatformDefaults; we
# do NOT pass --group here because doing so would be a chance for the
# value to drift between this script and the Go binary.
INSTALL_GROUP="hsh"

#
# Pre-flight checks
#

if [ ! -x "$DAEMON" ]; then
    red "error: hsh-tunneld not found next to this script (expected at $DAEMON)"
    red "       Make sure you ran install.sh from the extracted tarball directory."
    exit 1
fi

# Linux-only for this revision. macOS support lands when the LaunchDaemon
# backend in tunnel/service ships; that's a separate RD-217 follow-up.
# Refusing on darwin here (instead of letting hsh-tunneld print
# ErrUnsupportedPlatform) gives a clearer error AND avoids the user
# wondering whether `sudo` succeeded.
UNAME=$(uname -s)
case "$UNAME" in
    Linux) ;;
    Darwin)
        red "error: macOS install is not supported yet."
        red "       Use the Homebrew formula instead (coming soon)."
        exit 1
        ;;
    *)
        red "error: unsupported platform ($UNAME)."
        red "       This installer is Linux-only for now."
        exit 1
        ;;
esac

# Refuse early if systemd isn't running. A container with no /sbin/init
# or a non-systemd distro (Alpine, void, gentoo with OpenRC) would
# otherwise get an opaque systemctl error after we've already asked for
# sudo. We probe for /run/systemd/system because it's the canonical
# "systemd is PID 1" marker and works without needing dbus.
if [ ! -d /run/systemd/system ]; then
    red "error: systemd is not running on this host."
    red "       hsh-tunneld currently requires systemd. Open an issue if you"
    red "       need OpenRC / runit / launchd support and we'll prioritise it."
    exit 1
fi

# Architecture sanity. The daemon is built per-arch; if the user
# extracted the wrong tarball this is where we catch it. Both `uname
# -m` and `file` are widely available; we prefer `uname -m` and only
# use `file` for the friendly "you have an aarch64 system" message.
HOST_ARCH=$(uname -m)
# We do not enforce a match here (`uname -m` reports e.g. `x86_64`,
# the daemon's GOARCH-derived suffix is `amd64`) because the
# canonical mapping is annoying to keep in sync and `hsh-tunneld
# version` exits cleanly on a wrong-arch binary on most systems.
# The check below catches the most common mistake: an arm64 user
# downloaded the amd64 tarball.
if ! "$DAEMON" version >/dev/null 2>&1; then
    red "error: hsh-tunneld failed to execute on this host ($HOST_ARCH)."
    red "       This usually means you downloaded the wrong-architecture tarball."
    red "       Check 'uname -m' and re-download the matching archive."
    exit 1
fi

#
# Sudo elevation
#
# If we're not root, re-exec under sudo so the user only types their
# password once for the whole install (rather than three times across
# the various Go-side syscalls). `-E` is deliberately omitted: we do
# NOT want HOOP_* env vars to leak into the daemon's environment at
# install time; the daemon should be driven by the on-disk config.

if [ "$(id -u)" != "0" ]; then
    if ! command -v sudo >/dev/null 2>&1; then
        red "error: this script needs root, and sudo is not available."
        red "       Re-run as root: 'su -c \"./install.sh\"'"
        exit 1
    fi
    bold "==> re-executing under sudo"
    exec sudo -- "$0" "$@"
fi

#
# Hand off to the Go binary
#

bold "==> registering hsh-tunneld with systemd"
# Pass through any extra args the user supplied (so `./install.sh
# --no-enable` keeps the start-at-boot opt-out reachable without us
# having to mirror every flag). We deliberately do NOT split or
# transform $@ — the Go binary's flag parser owns that.
"$DAEMON" install "$@"

#
# Post-install next-steps
#

cat <<EOF

$(green "Installed.")
$(green "Service:") systemctl status hsh-tunneld
$(green "Logs:")    journalctl -u hsh-tunneld -f

$(bold "Next step:") add yourself to the '$INSTALL_GROUP' group so you can use
the CLI without sudo. The 'hsh' group is what gates access to the
daemon's IPC socket (/var/run/hsh/hsh.sock).

  sudo usermod -aG $INSTALL_GROUP \$USER

Then log out and log back in (or run 'newgrp $INSTALL_GROUP' in your shell)
for the new group to take effect.

Once you've done that, you can drive the daemon from your shell:

  hsh tunnel config set api-url https://gateway.example.com
  hsh tunnel login
  hsh tunnel connections

Documentation: https://hoop.dev/docs/tunnel

EOF
