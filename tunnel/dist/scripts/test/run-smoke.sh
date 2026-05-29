#!/usr/bin/env bash
# run-smoke.sh — RD-226 end-to-end test for the install/uninstall
# wrapper scripts inside a real Ubuntu + systemd container.
#
# Distinct from tunnel/service's own smoke (which calls `hsh-tunneld
# install` directly): this one simulates the user-visible flow of
# extracting the release tarball and running `./install.sh`.
#
# Prerequisite: an Ubuntu container image with systemd as PID 1. We
# build it from /tmp/install-smoke/Dockerfile during the RD-217
# session; if you removed it, re-run that Dockerfile build first.
#
# Outside-the-container build steps are intentionally NOT in this
# script — the harness expects to receive a tarball-shaped /opt/tarball
# bind mount from the host (./install.sh, ./uninstall.sh, ./hsh-tunneld)
# so we can iterate on the binary or the scripts without rebuilding
# the container image.

set -euo pipefail

red()   { printf '\e[31m%s\e[0m\n' "$*"; }
green() { printf '\e[32m%s\e[0m\n' "$*"; }
blue()  { printf '\e[34m%s\e[0m\n' "$*"; }

fail() { red "FAIL: $*"; exit 1; }
pass() { green "PASS: $*"; }
step() { blue "==> $*"; }

# Wait for systemd inside the container.
step "waiting for systemd"
for _ in {1..30}; do
    state=$(systemctl is-system-running 2>/dev/null || true)
    case "$state" in running|degraded) break;; esac
    sleep 0.5
done
case "$state" in
    running|degraded) ;;
    *) fail "systemd is in state $state" ;;
esac

step "checking tarball layout"
[ -x /opt/tarball/install.sh   ] || fail "install.sh not executable"
[ -x /opt/tarball/uninstall.sh ] || fail "uninstall.sh not executable"
[ -x /opt/tarball/hsh-tunneld  ] || fail "hsh-tunneld missing"
pass "layout OK"

# Sanity: running install.sh from elsewhere should still find its
# sibling daemon via $(dirname "$0").
step "running install.sh as root (already root inside the container)"
cd /opt/tarball
./install.sh
pass "install.sh exited 0"

# Steady-state check (5s sample window — see RD-217 smoke for
# rationale). A daemon that's restart-looping would flicker between
# 'running' and 'stopped' here.
step "checking service is stably running"
unstable=0
for _ in $(seq 1 10); do
    st=$(/usr/local/bin/hsh-tunneld status 2>&1)
    [ "$st" = "running" ] || unstable=1
    sleep 0.5
done
[ "$unstable" = "0" ] || {
    journalctl -u hsh-tunneld --no-pager -n 30 || true
    fail "service not stably running"
}
pass "service stable"

# We should be able to invoke uninstall.sh from BOTH the tarball
# directory and from elsewhere on disk. The script falls back to
# /usr/local/bin/hsh-tunneld when its sibling is missing, which is
# the realistic "user deleted the tarball months ago and now wants
# to uninstall" case.
step "uninstalling from a non-tarball cwd"
cd /
/opt/tarball/uninstall.sh --purge
[ -f /etc/systemd/system/hsh-tunneld.service ] && fail "unit still present"
[ -f /etc/hsh/config.toml ]                    && fail "config still present"
[ -x /usr/local/bin/hsh-tunneld ]              && fail "binary still present"
grep -q '^hsh:' /etc/group                     && fail "group still present"
pass "uninstall --purge OK"

# uninstall.sh should also work without a tarball — exercise the
# "binary already removed, sibling fallback fails" path. Expect a
# graceful error message (not a stack trace).
step "uninstall.sh with no daemon available"
rm -f /opt/tarball/hsh-tunneld # remove the sibling as well
set +e
out=$(/opt/tarball/uninstall.sh 2>&1)
rc=$?
set -e
if [ $rc -eq 0 ]; then
    fail "expected uninstall.sh to fail when no daemon available, got rc=0"
fi
echo "$out" | grep -q "could not find hsh-tunneld" \
    || fail "no friendly error in uninstall.sh output: $out"
pass "uninstall.sh fails gracefully when both daemon paths are gone"

green ""
green "========================================"
green "  RD-226 wrapper-script smoke: PASSED"
green "========================================"
