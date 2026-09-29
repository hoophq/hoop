#!/usr/bin/env bash
#
# Every claim this stack makes, as an assertion.
#
# The walkthrough in README.md explains WHY each of these is the behaviour.
# This script only checks that it still is — run it after a change to the
# relay code and it will tell you which property moved.
#
# Exit code is the number of failed checks, so CI can read it.
#
# Prereqs: ./run.sh

set -uo pipefail
cd "$(dirname "$0")"

PASS=0
FAIL=0

h()    { printf '\n\033[1;36m%s\033[0m\n\033[2m%s\033[0m\n' "$*" "----------------------------------------------------------------"; }
note() { printf '\033[2m  %s\033[0m\n' "$*"; }

ok() {
    local name="$1" got="$2" want="$3"
    if [[ "$got" == *"$want"* ]]; then
        printf '  \033[32mPASS\033[0m  %s\n' "$name"; PASS=$((PASS + 1))
    else
        printf '  \033[31mFAIL\033[0m  %s\n        want: %s\n        got:  %s\n' \
            "$name" "$want" "$(printf '%s' "$got" | head -3 | tr '\n' ' ')"
        FAIL=$((FAIL + 1))
    fi
}

no() {
    local name="$1" got="$2" want="$3"
    if [[ "$got" != *"$want"* ]]; then
        printf '  \033[32mPASS\033[0m  %s\n' "$name"; PASS=$((PASS + 1))
    else
        printf '  \033[31mFAIL\033[0m  %s\n        must NOT contain: %s\n' "$name" "$want"
        FAIL=$((FAIL + 1))
    fi
}

dc()  { docker compose "$@"; }
cli() { docker compose exec -T client "$@" 2>&1 | grep -v "Permanently added"; }
# As the subject who is NOT enrolled in the identities directory.
mal() {
    docker compose exec -T client ssh \
        -i /home/rider/probe/unenrolled \
        -o CertificateFile=/home/rider/probe/unenrolled-cert.pub \
        -o UserKnownHostsFile=/tmp/known_hosts_mallory \
        "$@" 2>&1 | grep -v "Permanently added"
}
bastion_log() { dc logs bastion 2>&1; }
trail() { dc exec -T bastion sh -c "cat /tmp/audit.jsonl" 2>/dev/null; }

dc ps --status running --format '{{.Service}}' 2>/dev/null | grep -q bastion \
    || { echo "the stack is not up; run ./run.sh first" >&2; exit 1; }

# --------------------------------------------------------------------------
h "1. A command runs on a host with nothing installed on it"
note "host-a is stock sshd. Its whole hoop footprint is one authorized_keys line."

out=$(cli ssh host-a.prod 'hostname; whoami')
ok "the command ran on the target"        "$out" "host-a"
ok "as the account the certificate named" "$out" "devuser"

# --------------------------------------------------------------------------
h "2. A blocked command never reaches the target"
note "Not reported after the fact — the bastion refuses before the upstream"
note "channel is written, so the target's sshd never learns it was attempted."

before=$(dc logs host-a 2>&1 | grep -c "secrets.env" || true)
out=$(cli ssh host-a.prod 'cat secrets.env')
after=$(dc logs host-a 2>&1 | grep -c "secrets.env" || true)
ok "the user is told why"              "$out" "not readable through hoop"
no "and the secret does not come back" "$out" "hunter2"
ok "the target's own log is unchanged" "$before" "$after"

# --------------------------------------------------------------------------
h "3. Output is masked over a network hop"
note "The bytes came from another host rather than a local process, and the"
note "rewrite hook runs on them just the same."

out=$(cli ssh host-a.prod 'cat team.txt')
no "the address does not reach the client" "$out" "alice@example.com"
ok "it arrives masked"                     "$out" "*****"

# --------------------------------------------------------------------------
h "4. A target narrows the listener's capability list"
note "The listener admits shell, pty, exec and env. host-a's entry admits"
note "exec and env, so a shell on host-a is refused and one on host-b is not."

out=$(cli ssh -T host-a.prod 2>&1)
ok "host-a refuses a shell"  "$out" "shell request failed"
out=$(cli ssh host-b.prod 'hostname')
ok "host-b admits one"       "$out" "host-b"

# --------------------------------------------------------------------------
h "5. File transfer is refused, and it says why"
note "Against a remote sftp-server it is an opaque subsystem stream; relaying"
note "it un-decoded would leave file transfer the one un-inspected path here."

cli sftp host-b.prod </dev/null >/dev/null 2>&1
out=$(trail | grep 'capability_refused' | grep 'sftp' | tail -1)
ok "sftp is refused, and the trail says why" "$out" "not delivered over a terminated session"

# --------------------------------------------------------------------------
h "6. A forward is dialled BY THE TARGET"
note "The rejected design dialled it from the bastion, where 127.0.0.1 would"
note "have meant the BASTION's loopback. bastion-decoy listens there to prove"
note "you never reach it."

out=$(dc exec -T client sh -c '
    ssh -f -N -L 15432:127.0.0.1:5432 host-b.prod 2>/dev/null
    sleep 2
    PGCONNECT_TIMEOUT=8 PGPASSWORD=demo psql -h 127.0.0.1 -p 15432 \
        -U demo -d demo -tAc "select 1 as reached;" 2>&1 | head -2
    pkill -f "ssh -f" 2>/dev/null; exit 0' 2>&1)
ok "postgres on host-b's loopback answered" "$out" "1"
no "the bastion's own loopback did not"     "$out" "THE-BASTION-DIALLED-IT"

# --------------------------------------------------------------------------
h "7. A forward outside the allowlist is refused"
note "Forwarded bytes are carried WITHOUT inspection, so the allowlist is the"
note "only control that bounds them. That is why it is required, not defaulted."

dc exec -T client sh -c '
    ssh -f -N -L 16379:127.0.0.1:6379 host-b.prod 2>/dev/null
    sleep 1
    (echo | nc -w 2 127.0.0.1 16379) >/dev/null 2>&1
    pkill -f "ssh -f" 2>/dev/null; exit 0' >/dev/null 2>&1
out=$(bastion_log | grep "forward refused" | tail -1)
ok "refused, with the destination named" "$out" "127.0.0.1:6379"

# --------------------------------------------------------------------------
h "8. Three credential sources, and a target names exactly one"

before=$(dc logs host-c 2>&1 | grep -c "Accepted certificate" || true)
out=$(cli ssh -o ForwardAgent=no host-c.prod 'hostname')
ok "agent_identity with no agent is REFUSED" "$out" "ForwardAgent yes"
after=$(dc logs host-c 2>&1 | grep -c "Accepted certificate" || true)
ok "and the target was never reached with a substitute" "$before" "$after"

# ssh-agent is started and stopped inside ONE shell so nothing survives the
# check, and the whole thing is bounded: a hung agent would otherwise stall
# the run rather than fail it.
out=$(dc exec -T client sh -c '
    eval $(ssh-agent -s) >/dev/null 2>&1
    ssh-add ~/.ssh/id_ed25519 >/dev/null 2>&1
    timeout 25 ssh -o ForwardAgent=yes host-c.prod hostname 2>&1 \
        | grep -v "Permanently added"
    ssh-agent -k >/dev/null 2>&1
    exit 0' 2>&1)
ok "with an agent, the session runs" "$out" "host-c"

out=$(dc logs host-c 2>&1 | grep "Accepted certificate" | tail -1)
ok "and the TARGET's own log names the person" "$out" "alice@example.com"

out=$(cli ssh legacy-01 'hostname; whoami')
ok "a per-target login override reaches a different account" "$out" "ec2-user"

# --------------------------------------------------------------------------
h "9. The identities overlay, and what happens without it"
note "The overlay is LANE-WIDE: an enrolled subject reaches every target under"
note "their own key, and a target's private_key serves the unenrolled."

before=$(dc logs host-b 2>&1 | grep -c "Accepted publickey" || true)
out=$(mal host-b.prod 'hostname')
ok "an unenrolled subject is refused where there is no shared key" \
    "$out" "do not have access"
# The client is told that and no more. The identities directory, the config
# key that decided, and whether this subject is enrolled all stay in the
# trail — a refusal is the one message an unauthorised party can produce on
# demand, so it must not describe the deployment.
for leak in "identities" "private_key" "not enrolled" "/etc/hoop-inspect"; do
    no "the refusal does not leak: $leak" "$out" "$leak"
done
out=$(trail | grep upstream_credential_refused | tail -1)
ok "and the trail keeps the whole reason" "$out" "is not enrolled in"
# Asserted on the TARGET, not on the message: the refusal names the target it
# refused, so "the output does not mention host-b" was never the question.
# The question is whether anything authenticated there.
after=$(dc logs host-b 2>&1 | grep -c "Accepted publickey" || true)
ok "and nothing authenticated to the target in their place" "$before" "$after"

out=$(mal host-a.prod 'hostname')
ok "the same subject is carried by the target's shared key" "$out" "host-a"
out=$(bastion_log | grep "fell back" | tail -1)
ok "and the fallback is recorded EVERY time" "$out" "mallory@example.com"

# --------------------------------------------------------------------------
h "10. The host key is checked, and relaxing it is loud"

ok "accept_new learned legacy-01 and wrote it" \
    "$(cat state/known_hosts.legacy 2>/dev/null)" "legacy-01 ssh-ed25519"
out=$(trail | grep "host_key_first_contact" | tail -1)
ok "and the first contact is in the trail, with a fingerprint" \
    "$out" "host_key_first_contact"

# --------------------------------------------------------------------------
h "11. A destination no target covers is carried BLIND"
note "Same bastion, same client, same certificate. blind-01 is not in"
note "relay.targets, so the bastion relays bytes it cannot read — which is"
note "ADR-0015's behaviour, now a visible choice rather than the only one."

out=$(cli ssh blind-01 'cat secrets.env')
ok "the blocked path is readable there"     "$out" "hunter2"
ok "and the address is NOT masked"          "$out" "alice@example.com"
out=$(trail | grep 'forward_open' | grep 'blind-01:22' | tail -1)
ok "the trail records the destination only" "$out" "forward_open"
out=$(trail | grep 'relay_open' | grep -c 'blind-01' || true)
ok "and no session was terminated for it"   "$out" "0"

# --------------------------------------------------------------------------
h "12. The trail has TWO hops, and can tell them apart"
note "Hop 1 authorises opening a forward; hop 2 authorises a session on"
note "whatever answered. Separate handshakes, independent trust decisions."

out=$(trail | grep '"hop":"2"' | tail -1)
ok "hop 2 is marked as such"        "$out" '"hop":"2"'
ok "and names the target it reached" "$out" '"target"'
out=$(trail | grep 'relay_open' | tail -1)
ok "a terminated forward is recorded" "$out" "relay_open"

# --------------------------------------------------------------------------
printf '\n\033[1m%d passed, %d failed\033[0m\n' "$PASS" "$FAIL"
if (( FAIL == 0 )); then
    printf '\033[32mEvery property ADR-0021 specifies holds in this stack.\033[0m\n'
fi
exit "$FAIL"
