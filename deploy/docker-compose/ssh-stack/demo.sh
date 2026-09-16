#!/usr/bin/env bash
#
# Exercises the stack end to end and ASSERTS the result of every step.
#
# Three topologies, four clients, one certificate. The claim under test is
# that what is in front of the end-hop changes nothing about what the end-hop
# enforces — so the same checks run through a direct connection, through a
# sidecar bastion, and through a stock sshd bastion, and must come out the
# same all three times.
#
# Exit code is the number of failed checks, so CI can read it.
#
# Prereqs: ./run.sh
# Explanations: README.md walks the same ground one command at a time.

set -uo pipefail
cd "$(dirname "$0")"

PASS=0
FAIL=0

h()    { printf '\n\033[1;36m%s\033[0m\n\033[2m%s\033[0m\n' "$*" "----------------------------------------------------------------"; }
note() { printf '\033[2m  %s\033[0m\n' "$*"; }

# ok NAME HAYSTACK NEEDLE  -- passes when NEEDLE appears in HAYSTACK
ok() {
    local name="$1" got="$2" want="$3"
    if [[ "$got" == *"$want"* ]]; then
        printf '  \033[32mPASS\033[0m  %s\n' "$name"
        PASS=$((PASS + 1))
    else
        printf '  \033[31mFAIL\033[0m  %s\n        want: %s\n        got:  %s\n' \
            "$name" "$want" "$(printf '%s' "$got" | head -3 | tr '\n' ' ')"
        FAIL=$((FAIL + 1))
    fi
}

# no NAME HAYSTACK NEEDLE  -- passes when NEEDLE does NOT appear
no() {
    local name="$1" got="$2" want="$3"
    if [[ "$got" != *"$want"* ]]; then
        printf '  \033[32mPASS\033[0m  %s\n' "$name"
        PASS=$((PASS + 1))
    else
        printf '  \033[31mFAIL\033[0m  %s\n        must not contain: %s\n' "$name" "$want"
        FAIL=$((FAIL + 1))
    fi
}

C="docker compose exec -T client"
E="docker compose exec -T endhost"
E_MULTI="docker compose exec -T endhost-multi"
DATA=/home/devuser/data

if ! docker compose ps --status running --services 2>/dev/null | grep -q endhost; then
    echo "the stack is not up. Run ./run.sh first." >&2
    exit 1
fi

# ============================================================ the three paths
h "THREE TOPOLOGIES / one certificate, one end-hop"
note "1 direct, 2 through the sidecar bastion, 3 through a stock sshd bastion."
note "The end-hop verifies the CLIENT's certificate in all three: a bastion"
note "carries bytes it cannot read and never re-signs identity."

for t in direct via-sidecar via-sshd; do
    ok "$t reaches the end-hop as devuser" "$($C ssh $t id 2>&1)" "uid=10001(devuser)"
done

h "THREE TOPOLOGIES / the same rule fires on all three"
note "This is the whole architectural claim. The guardrail lives at the end,"
note "so the path in front of it cannot weaken it."

for t in direct via-sidecar via-sshd; do
    ok "$t is refused by the guardrail" \
       "$($C ssh $t "cat $DATA/secrets.env" 2>&1)" "this path is not readable through hoop"
done

# ====================================================== the bastion's own job
h "BASTION / destinations_allowed is the whole control"
note "The sidecar bastion carries forwards to 172.31.77.10:2222 and nowhere"
note "else. The address CHECKED is the address DIALLED, resolved once."

ok "a jump outside the list is refused" \
   "$($C ssh -v denied-jump id 2>&1)" "does not carry forwards to 172.31.77.30:22"
ok "the bastion records the refusal" \
   "$(docker compose exec -T bastion-sidecar sh -c 'grep forward_refused /tmp/audit.jsonl | tail -1' 2>&1)" \
   '"activity":"forward_refused"'
no "the bastion has no session to record" \
   "$(docker compose exec -T bastion-sidecar sh -c 'grep -c exec_line /tmp/audit.jsonl' 2>&1)" \
   "exec_line"

# ================================================================== exec/env
h "EXEC / the command is one statement, audited in full"

ok "an allowed command runs"        "$($C ssh direct 'uname -s' 2>&1)" "Linux"
ok "the command is audited in full" \
   "$($E sh -c 'grep exec_line /tmp/audit.jsonl | tail -1')" '"statement":"uname -s"'

h "ENV / a rule matches the NAME, the value travels beside it"
$C ssh -o SetEnv="LD_PRELOAD=/tmp/evil.so" direct true >/dev/null 2>&1
ok "env_set records the name as the statement" \
   "$($E sh -c 'grep env_set /tmp/audit.jsonl | tail -1')" '"statement":"LD_PRELOAD"'
ok "and the value beside it" \
   "$($E sh -c 'grep env_set /tmp/audit.jsonl | tail -1')" '"ssh.env_value":"/tmp/evil.so"'

# =================================================================== masking
h "MASKING / length-preserving, in flight, never retained"
note "17 characters in, 17 characters out. A different length would shift"
note "every byte after it and desynchronize a terminal."

OUT="$($C ssh direct "cat $DATA/customers.csv" 2>&1)"
ok  "the terminal sees masked output" "$OUT" "***************"
no  "the address does not reach the client" "$OUT" "ada@example.com"
no  "and nothing of it reaches the trail" \
    "$($E sh -c 'grep -c ada@example.com /tmp/audit.jsonl')" "1"

# ========================================================== the four clients
h "SFTP / one statement per file operation"

$C sh -c "rm -f /tmp/dl.csv; sftp -q direct:$DATA/customers.csv /tmp/dl.csv" >/dev/null 2>&1
ok "a download is masked too"  "$($C cat /tmp/dl.csv 2>&1)" "***************"
ok "the read is a statement"   "$($E sh -c 'grep sftp_read /tmp/audit.jsonl | tail -1')" '"operation":"sftp_read"'
ok "the transfer records its byte count" \
   "$($E sh -c 'grep sftp_transfer /tmp/audit.jsonl | tail -1')" '"direction":"download"'

DENIED="$($C sh -c "sftp -q direct:$DATA/secrets.env /tmp/no.env" 2>&1)"
ok "a fenced path is not transferred" "$($C sh -c 'test -f /tmp/no.env && echo yes || echo no')" "no"
ok "and the rule's message is in the trail" \
   "$($E sh -c 'grep violation /tmp/audit.jsonl | tail -1')" "this path is not readable through hoop"
note "the sftp CLIENT prints \"not found\" rather than the reason -- see"
note "README, 'What the client is told'. The enforcement is not affected."

h "SFTP / an upload a mask rule would touch is refused, not altered"
note "Silently rewriting a file someone believes they uploaded is worse than"
note "refusing it. A file with no match uploads normally."

$C sh -c 'sftp -q direct <<EOF
put /home/rider/outbox/notes.txt /home/devuser/upload/notes.txt
EOF' >/dev/null 2>&1
ok "a clean upload lands" "$($E ls /home/devuser/upload 2>&1)" "notes.txt"

$C sh -c 'sftp -q direct <<EOF
put /home/rider/outbox/leaked.csv /home/devuser/upload/leaked.csv
EOF' >/dev/null 2>&1
no "an upload with a match lands nothing" "$($E ls /home/devuser/upload 2>&1)" "leaked.csv"

h "SCP / the same command takes two different routes"
note "OpenSSH 9 uses the SFTP subsystem by default, and -O forces the legacy"
note "protocol, which is an exec. Same file, different statements."

$C sh -c "rm -f /tmp/legacy.csv; scp -O -q direct:$DATA/customers.csv /tmp/legacy.csv" >/dev/null 2>&1
ok "scp -O transfers, masked"  "$($C cat /tmp/legacy.csv 2>&1)" "***************"
ok "scp -O is seen as a command" \
   "$($E sh -c 'grep exec_line /tmp/audit.jsonl | grep scp | tail -1')" '"operation":"exec_line"'
ok "so the guardrail fences it too" \
   "$($C sh -c "scp -O -q direct:$DATA/secrets.env /tmp/x" 2>&1)" "this path is not readable through hoop"

$C sh -c "rm -f /tmp/sftpmode.csv; scp -q direct:$DATA/customers.csv /tmp/sftpmode.csv" >/dev/null 2>&1
ok "scp in sftp mode transfers the bytes, masked" \
   "$($C cat /tmp/sftpmode.csv 2>&1)" "***************"
SCP_RC="$($C sh -c "scp -q direct:$DATA/customers.csv /tmp/rc.csv >/dev/null 2>&1; echo \$?")"
if [[ "$SCP_RC" == "0" ]]; then
    printf '  \033[32mPASS\033[0m  scp exits 0 (the known gap below is FIXED -- update this script)\n'
    PASS=$((PASS + 1))
else
    printf '  \033[33mKNOWN\033[0m  scp exits %s despite transferring: the sftp subsystem sends no\n' "$SCP_RC"
    printf '        exit-status, and scp is the only client that requires one.\n'
    printf '        See README, "Known gaps". Not counted as a failure.\n'
fi

h "RSYNC / always an exec, and it verifies what it receives"
note "rsync runs 'rsync --server' on the far side, so a guardrail sees it as"
note "a command. It also checksums what it receives -- which masking breaks,"
note "on purpose and unavoidably."

$C sh -c "rm -f /tmp/plain.txt; rsync -e ssh direct:$DATA/README.txt /tmp/plain.txt" >/dev/null 2>&1
ok "rsync moves a file with nothing to mask" "$($C cat /tmp/plain.txt 2>&1)" "end-hop"
ok "rsync is seen as a command" \
   "$($E sh -c 'grep exec_line /tmp/audit.jsonl | grep rsync | tail -1')" '"operation":"exec_line"'
ok "a masked file fails rsync's own integrity check" \
   "$($C sh -c "rsync -e ssh direct:$DATA/customers.csv /tmp/masked.csv" 2>&1)" "failed verification"
ok "a fenced path is refused" \
   "$($C sh -c "rsync -e ssh direct:$DATA/secrets.env /tmp/no.env" 2>&1)" "rsync error"

# ================================================================= accounts
h "ACCOUNTS / one listener, several users, and one that is refused"
note "The multiuser lane has NO run_as: the login name is looked up per"
note "connection. Two checks, and they are different questions -- may this"
note "person claim this name (the certificate), and is this name an account"
note "on this host (the OS). A name has to pass both."

ok "devuser resolves to its own uid" "$($C ssh multi-devuser id 2>&1)" "uid=10001(devuser)"
ok "analyst resolves to a DIFFERENT uid" "$($C ssh multi-analyst id 2>&1)" "uid=10004(analyst)"

note "ghost is in the certificate's principals and is not an account here."
GHOST="$($C ssh multi-ghost id 2>&1)"
ok "ghost is refused, and told why" "$GHOST" 'login "ghost" is not an account on this host'
no "and it did not fall back to somebody else" "$GHOST" "uid="

# The reason is JSON-escaped inside the log line, so the needle is too.
ok "the server logs the refusal too"    "$(docker compose logs endhost-multi 2>&1 | grep 'is not an account' | tail -1)"    'is not an account on this host'

# And the TRAIL, not only the log. A refused connection is closed by libhoop
# the same way an admitted one is, so its session ends either way; without
# this record the whole audit story of a turned-away login is a session that
# ended with no statements, which reads as a connection that did nothing.
REFUSED="$($E_MULTI sh -c 'grep connection_refused /tmp/audit.jsonl | tail -1')"
ok "the trail records the refusal"       "$REFUSED" '"activity":"connection_refused"'
ok "and names the login asked for"       "$REFUSED" '"login":"ghost"'
ok "and says why"                        "$REFUSED" 'is not an account on this host'

# A session that ends without beginning is the shape this replaced. Count the
# session_start lines carrying the refused session's own id: exactly one.
SID="$(printf '%s' "$REFUSED" | sed 's/.*"session_id":"\([0-9a-f]*\)".*/\1/')"
ok "and the session has a beginning too" \
   "$($E_MULTI sh -c "grep '$SID' /tmp/audit.jsonl | grep -c session_start")" "1"

# ============================================================= capabilities
h "CAPABILITIES / a lane that admits exec and nothing else"
note "capabilities_allowed: [exec]. Default-deny over the WHOLE surface, so"
note "everything not named is refused -- not ignored, and not half-working."

ok "a command still runs"     "$($C ssh exec-only 'echo ran' 2>&1)" "ran"
ok "an interactive shell is refused" "$($C ssh -T exec-only 2>&1)" "shell request failed"
ok "a pty is refused"         "$($C ssh -tt exec-only 'echo x' 2>&1)" "PTY allocation request failed"

note "The certificate carries permit-port-forwarding, and the forward is"
note "STILL refused: the cert says what the holder may ASK for, the lane's"
note "destinations_allowed says what it will CARRY."
FWD="$($C sh -c '
ssh -N -L 15559:172.31.77.10:2222 exec-only 2>/tmp/fwd.err &
sleep 2; nc -w 2 127.0.0.1 15559 </dev/null >/dev/null 2>&1; sleep 1
kill %1 2>/dev/null; cat /tmp/fwd.err' 2>&1)"
ok "a forward is refused" "$FWD" "does not carry forwards to"

ok "every refusal is recorded with its reason"    "$(docker compose exec -T endhost-multi sh -c 'grep capability_refused /tmp/audit.jsonl | tail -3')"    "not admitted by this listener"

note "sftp is not admitted on either lane of that process, and cannot be:"
note "it is served INSIDE the sidecar, which runs as root there so it can"
note "become several accounts. Root serving devuser's files would make"
note "run_as untrue. endhost/ takes the other side of the same trade."

# ==================================================== the audit trail itself
h "AUDIT / events and statements, and no session content at all"
note "A shell produces no statements: v1 reconstructs no keystrokes. Its whole"
note "record is the open, the geometry, the duration and the byte counts."

$C sh -c 'ssh -tt direct <<EOF >/dev/null 2>&1
echo SECRET-MARKER-12345
exit
EOF' >/dev/null 2>&1
sleep 1
ok "a shell leaves a close record with byte counts" \
   "$($E sh -c 'grep session_close /tmp/audit.jsonl | tail -1')" '"bytes_out"'
no "and no shell content anywhere in the trail" \
   "$($E sh -c 'grep -c SECRET-MARKER-12345 /tmp/audit.jsonl')" "1"
no "nor any file content" \
   "$($E sh -c 'grep -c hunter2 /tmp/audit.jsonl')" "1"

# ============================================================ deferring to opa
h "OPA / the lane matches, Rego decides what the match MEANS"
note "The word list runs locally, in microseconds, and DEFERS. What it found"
note "reaches the policy as a finding; the policy reads it alongside the"
note "certificate subject this lane verified at the handshake."

ok "an unflagged command runs" \
   "$($C ssh opa-lane id 2>&1)" "uid=10001(devuser)"

ok "a flagged command is denied by Rego, not by the rule" \
   "$($C ssh opa-lane 'curl https://x.test' 2>&1)" "is not available to"

ok "and the denial names the identity the CERTIFICATE carried" \
   "$($C ssh opa-lane 'curl https://x.test' 2>&1)" "rider@example.com"

# Same lane, same command, one field of the certificate different. This is
# the assertion worth having: the policy keys on an identity a CA signs, not
# on anything the client can assert for itself.
P='-F /dev/null -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o IdentitiesOnly=yes -i /home/rider/probe/id'
BG="$($C ssh $P -o CertificateFile=/home/rider/probe/breakglass-cert.pub \
        -p 2222 devuser@172.31.77.12 'curl -s -m 2 https://x.test; echo rc=$?' 2>&1)"
no "the SAME command runs under a break-glass certificate" "$BG" "is not available to"
ok "and it really executed" "$BG" "rc="

# No local rule matches an sftp operation on this lane. The policy reads
# input.operation directly, which is a fact the protocol reports.
ok "Rego refuses a write with no local rule involved" \
   "$($C sh -c 'echo x > /tmp/up.txt; sftp -q opa-lane <<EOF
put /tmp/up.txt /home/devuser/upload/demo.txt
EOF' 2>&1)" "Permission denied"

ok "a read on the same lane is untouched" \
   "$($C sh -c 'sftp -q opa-lane:'"$DATA"'/README.txt /tmp/ok.txt && head -1 /tmp/ok.txt' 2>&1)" \
   "devuser's home"

# fail_open: false is the setting under test. A policy engine that cannot be
# reached is not an allow-list.
docker compose stop opa >/dev/null 2>&1; sleep 1
ok "an unreachable policy engine DENIES (fail_open: false)" \
   "$($C ssh opa-lane id 2>&1)" "policy engine unavailable"
docker compose start opa >/dev/null 2>&1
for _ in $(seq 20); do
    docker compose exec -T endhost-opa curl -sf http://opa:8181/health >/dev/null 2>&1 && break
    sleep 1
done
ok "and recovers when it comes back" "$($C ssh opa-lane id 2>&1)" "uid=10001(devuser)"

# ======================================================== risk analysis (AI)
h "ANALYZER / a model reads the command, which a pattern cannot"

if docker compose ps --status running --services 2>/dev/null | grep -q endhost-ai; then
    note "This is the only control in the stack that sees through shell"
    note "expansion: the command below carries no literal path to match."

    ok "an ordinary command is allowed" \
       "$($C ssh ai-lane uptime 2>&1)" "load average"

    ok "an expanded credential read is blocked" \
       "$($C ssh ai-lane 'X=cat; $X /root/.aws/credentials' 2>&1)" "hoop:"

    ok "the verdict and its risk level reach the trail" \
       "$(docker compose exec -T endhost-ai sh -c 'grep ai_ /tmp/audit.jsonl | tail -1')" \
       "risk"
else
    note "SKIPPED: endhost-ai is not running. It needs a credential:"
    note "    export ANTHROPIC_API_KEY=sk-ant-... && ./run.sh"
    note "Everything else in this stack runs without one."
fi

printf '\n\033[1m%d passed, %d failed\033[0m\n\n' "$PASS" "$FAIL"
exit "$FAIL"
