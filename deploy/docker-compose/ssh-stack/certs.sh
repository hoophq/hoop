#!/usr/bin/env bash
#
# One certificate per attribute, and what the end-hop does with it.
#
# demo.sh proves the stack works with ONE certificate. This script asks the
# opposite question: which FIELD of a certificate decided that, and what
# happens when each field says something else. Every probe below mints a
# certificate that differs from the working one in exactly one attribute, so
# a refusal has one possible cause.
#
# The four questions a certificate answers, and the field that answers each:
#
#   who signed it       the CA signature        admission
#   which names it may  -n, the principals      WHICH LOGIN, checked against
#     claim                                     the OS account database after
#   who the holder is   -I, the key id          the audit trail, and nothing
#                                               else
#   when               -V, the validity window  admission
#   what it may ask    extensions               pty and forwarding
#     for
#   where from / what  critical options         admission; an unknown one
#     it pins                                   REFUSES the certificate
#
# The last line is the asymmetry worth knowing: an unknown EXTENSION is
# ignored, an unknown CRITICAL OPTION refuses the whole certificate. That is
# the certificate format's own rule and both halves are probed below.
#
# Exit code is the number of failed checks, so CI can read it.
#
# Prereqs: ./run.sh
# Background: README.md, "Certificate attributes"

set -uo pipefail
cd "$(dirname "$0")"

PASS=0
FAIL=0

h()    { printf '\n\033[1;36m%s\033[0m\n\033[2m%s\033[0m\n' "$*" "----------------------------------------------------------------"; }
note() { printf '\033[2m  %s\033[0m\n' "$*"; }

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

if ! docker compose ps --status running --services 2>/dev/null | grep -q endhost; then
    echo "the stack is not up. Run ./run.sh first." >&2
    exit 1
fi
if ! docker compose exec -T client test -d /home/rider/probe 2>/dev/null; then
    echo "the client has no /home/rider/probe mount. This stack was brought up
before certs.sh existed -- ./run.sh down && ./run.sh to pick it up." >&2
    exit 1
fi

# ---------------------------------------------------------------- the family
#
# Every certificate signs the SAME public key. Only the attributes differ, so
# nothing else can explain a different answer.
#
# They are minted fresh on every run, because two of them are about time and
# a cached -V would stop meaning what it says.
PROBE=/home/rider/probe

mint_with() {   # mint_with CAKEY NAME [ssh-keygen args...]
    local ca="$1" name="$2"; shift 2
    cp keys/user.pub "keys/probe/$name.pub"
    ssh-keygen -q -s "$ca" "$@" "keys/probe/$name.pub"
}
mint() { mint_with keys/ca "$@"; }

# probe CERT LOGIN HOST PORT [ssh args...]
#
# -F /dev/null on purpose: client/ssh_config exists to make the walkthrough
# read as intent, and that is exactly wrong here. A probe has to show every
# option it is using, because the claim is that nothing but the certificate
# differs.
#
# An empty CERT offers the bare key with no certificate at all. That needs
# its own key file: ssh loads `<identity>-cert.pub` from beside the private
# key whether you asked it to or not, so probing with ~/.ssh/id_ed25519
# would silently present the stack's real certificate and pass.
probe() {
    local cert="$1" login="$2" host="$3" port="$4"; shift 4
    local certopt=()
    [[ -n "$cert" ]] && certopt=(-o "CertificateFile=$PROBE/$cert-cert.pub")
    # ${a[@]+"${a[@]}"} rather than "${a[@]}": bash 3.2 is what macOS ships,
    # and there an empty array under `set -u` is an unbound variable. This
    # expansion has to survive it, because the bare-key probe is the one
    # that passes no certificate at all.
    docker compose exec -T client ssh -F /dev/null \
        -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
        -o IdentitiesOnly=yes -o BatchMode=yes -o ConnectTimeout=5 \
        -o LogLevel=INFO -i "$PROBE/id" ${certopt[@]+"${certopt[@]}"} \
        -p "$port" "$login@$host" "$@" 2>&1
}

# refused CERT LOGIN HOST PORT SERVICE
#
# Runs a probe that MUST fail and echoes the reason SERVICE logged for it.
#
# A refused handshake tells the CLIENT nothing -- "Permission denied
# (publickey)" is all it ever gets, and deliberately: naming the field that
# failed would help an attacker more than a user. So the reason has to come
# from the operator's log.
#
# Which is why this counts the log lines rather than reading the last one.
# Reading the last one lets a probe that STOPPED refusing pass on the
# previous probe's reason -- several of these certificates are refused with
# the same words, so that failure mode is not hypothetical. Two checks make
# it impossible: the client must have been denied, and the log must have
# grown.
#
# The reason is a JSON string inside a JSON log line, so a needle carrying
# quotes has to carry them escaped -- \"devuser\", not "devuser".
refused() {
    local cert="$1" login="$2" host="$3" port="$4" svc="$5"
    local before after out
    before=$(docker compose logs "$svc" 2>&1 | grep -c 'handshake refused')
    out=$(probe "$cert" "$login" "$host" "$port" id)
    after=$(docker compose logs "$svc" 2>&1 | grep -c 'handshake refused')
    if [[ "$out" == *"uid="* ]]; then
        echo "THIS CERTIFICATE WAS ADMITTED: $out"
        return
    fi
    if (( after <= before )); then
        echo "REFUSED WITH NO HANDSHAKE REASON LOGGED: $out"
        return
    fi
    docker compose logs "$svc" 2>&1 | grep 'handshake refused' | tail -1
}

MULTI=172.31.77.11    # multiuser lane :2222, identity-ext lane :2224
ENDHOST=172.31.77.10  # admits pty, so the pty GRANT is the only variable
BASTION=172.31.77.20  # carries forwards to the end-hop, so the forwarding
                      # grant is the only variable

rm -f keys/probe/*.pub keys/probe/*-cert.pub
cp keys/user keys/probe/id
[[ -f keys/probe/rogue-ca ]] || ssh-keygen -t ed25519 -f keys/probe/rogue-ca -N '' -q -C rogue-ca

#         name          what it changes
mint      base          -I rider@example.com -n devuser -V +1h
mint_with keys/probe/rogue-ca \
          rogue         -I rider@example.com -n devuser -V +1h
mint      hostcert      -h -I rider@example.com -n devuser -V +1h
mint      wrongname     -I rider@example.com -n analyst -V +1h
mint      noprincipal   -I rider@example.com            -V +1h
mint      expired       -I rider@example.com -n devuser -V 20260101:20260102
mint      future        -I rider@example.com -n devuser -V +1h:+2h
mint      evilkeyid     -I root@evil.example -n devuser -V +1h
mint      serial        -I rider@example.com -n devuser -V +1h -z 4242
mint      nogrants      -I rider@example.com -n devuser -V +1h -O clear
mint      unknownext    -I rider@example.com -n devuser -V +1h -O extension:x-hoop-probe@hoop.dev=1
mint      unknowncrit   -I rider@example.com -n devuser -V +1h -O critical:x-hoop-probe@hoop.dev=1
mint      forcecmd      -I rider@example.com -n devuser -V +1h -O force-command=/bin/true
mint      srcok         -I rider@example.com -n devuser -V +1h -O source-address=172.31.77.40/32
mint      srcbad        -I rider@example.com -n devuser -V +1h -O source-address=10.99.99.0/24
mint      jumpgrant     -I rider@example.com -n jump    -V +1h
mint      jumpnogrant   -I rider@example.com -n jump    -V +1h -O clear
mint      identityext   -I ca-internal-ref-99 -n devuser -V +1h \
                        -O extension:login@hoop.dev=alice@corp.example \
                        -O extension:groups@hoop.dev=sre,oncall
# The two that name NOBODY. Both are certificates ssh-keygen signs without a
# complaint, and both used to be admitted as `anonymous`.
mint      noidentity    -I '' -n devuser -V +1h
mint      idextnoname   -I ca-internal-ref-99 -n devuser -V +1h \
                        -O extension:groups@hoop.dev=sre,oncall
chmod 600 keys/probe/id keys/probe/rogue-ca

# ================================================================ the signer
h "SIGNATURE / trusting a CA is the whole admission decision"
note "trusted_ca names one public key. Nothing else about a certificate can"
note "make up for the wrong signature, and no per-user state exists to hold"
note "an exception in."

ok "the stack's own CA is admitted" \
   "$(probe base devuser $MULTI 2222 id)" "uid=10001(devuser)"

ok "a certificate from another CA is refused" \
   "$(refused rogue devuser $MULTI 2222 endhost-multi)" "certificate signed by unrecognized authority"

ok "a bare public key is refused, certificate or nothing" \
   "$(refused "" devuser $MULTI 2222 endhost-multi)" "only certificate authentication is accepted"
note "there is no authorized_keys here and no place to put one. Trusting a"
note "key because someone holds it is the model this design replaced."

ok "a HOST certificate cannot be used to log in" \
   "$(refused hostcert devuser $MULTI 2222 endhost-multi)" "only certificate authentication is accepted"
note "two layers refuse this one. ssh will not even offer it ('not a user"
note "certificate'), so it falls back to the bare key and the server refuses"
note "THAT -- which is why the reason above is the bare-key one. The server's"
note "own type check is the backstop behind a client that tried anyway."

# ============================================================== which names
h "PRINCIPALS (-n) / the only field that decides a login name"
note "This is the field people expect -I to be. It is a list of names the"
note "holder may REQUEST, checked against the name in the userauth request."

ok "a name in the list logs in" \
   "$(probe base devuser $MULTI 2222 id)" "uid=10001(devuser)"

ok "a name NOT in the list is refused" \
   "$(refused wrongname devuser $MULTI 2222 endhost-multi)" 'principal \"devuser\" not in the set of valid principals'

ok "and the same certificate works for the name it does carry" \
   "$(probe wrongname analyst $MULTI 2222 id)" "uid=10004(analyst)"
note "one certificate, two logins, opposite answers. Nothing about the"
note "holder changed -- only the name they asked to become."

ok "a certificate with NO principals is refused outright" \
   "$(refused noprincipal devuser $MULTI 2222 endhost-multi)" "certificate carries no principals"
note "this is the one refusal that is not stock crypto/ssh. An empty list"
note "reads there as 'valid for every login name' -- correct for a HOST"
note "certificate, a hole for a user certificate at an endpoint whose job is"
note "deciding who may log in. ssh-keygen produces one whenever -n is"
note "omitted, so it is a plausible mistake and not a theoretical one."

# ================================================================== the human
h "KEY ID (-I) / names the human, decides nothing"
note "Free-form, and no part of authorization reads it. It is what the audit"
note "trail calls the session, which is the whole of its job."

ok "a certificate whose key id is a lie still logs in" \
   "$(probe evilkeyid devuser $ENDHOST 2222 'echo probe-keyid')" "probe-keyid"
ok "and the trail names the key id, not the account" \
   "$(docker compose exec -T endhost sh -c 'grep probe-keyid /tmp/audit.jsonl | tail -1')" \
   '"principal":"root@evil.example"'
note "the session still ran as devuser. -I is the answer to 'who ran this',"
note "and -n is the answer to 'as what' -- a CA that confuses the two writes"
note "an audit trail that cannot name anybody."

probe serial devuser $ENDHOST 2222 'echo probe-serial' >/dev/null 2>&1
ok "the serial is recorded, which is what a KRL revokes by" \
   "$(docker compose exec -T endhost sh -c 'grep connection_open /tmp/audit.jsonl | tail -1')" \
   '"serial":"4242"'

# ======================================================================= when
h "VALIDITY (-V) / checked at the handshake, both ends of the window"

ok "an expired certificate is refused" \
   "$(refused expired devuser $MULTI 2222 endhost-multi)" "cert has expired"

ok "so is one that is not valid yet" \
   "$(refused future devuser $MULTI 2222 endhost-multi)" "cert is not yet valid"
note "a clock that is wrong on either side breaks logins, and that is the"
note "trade a short -V buys: revocation you do not have to distribute."

# ================================================================== the grants
h "EXTENSIONS / what the holder may ASK for"
note "These are grants, not identity. ssh-keygen enables the standard set"
note "unless told otherwise, so their absence is the deliberate signal --"
note "-O clear is a CA saying no, a default certificate says nothing."

ok "a certificate with the standard grants gets a terminal" \
   "$(probe base devuser $ENDHOST 2222 -tt id)" "uid=10001(devuser)"
ok "-O clear does not, on the same lane" \
   "$(probe nogrants devuser $ENDHOST 2222 -tt id)" "PTY allocation request failed"
note "same listener, same account, same capabilities_allowed. Only the"
note "certificate differs, which is what makes this a certificate check"
note "rather than a lane check."

ok "the trail records which grants the certificate carried" \
   "$(docker compose exec -T endhost sh -c 'grep connection_open /tmp/audit.jsonl | tail -1')" \
   '"permit_pty":"false"'

FWD_OK="$(docker compose exec -T client sh -c "
    ssh -F /dev/null -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
        -o IdentitiesOnly=yes -o BatchMode=yes -o LogLevel=INFO \
        -i $PROBE/id -o CertificateFile=$PROBE/jumpgrant-cert.pub \
        -N -L 15581:$ENDHOST:2222 -p 2222 jump@$BASTION 2>/tmp/fwd-ok.err &
    sleep 2; nc -w 2 127.0.0.1 15581 </dev/null 2>&1 | head -1
    kill %1 2>/dev/null; cat /tmp/fwd-ok.err" 2>&1)"
ok "permit-port-forwarding carries a forward through the bastion" "$FWD_OK" "SSH-2.0-hoop"

FWD_NO="$(docker compose exec -T client sh -c "
    ssh -F /dev/null -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
        -o IdentitiesOnly=yes -o BatchMode=yes -o LogLevel=INFO \
        -i $PROBE/id -o CertificateFile=$PROBE/jumpnogrant-cert.pub \
        -N -L 15582:$ENDHOST:2222 -p 2222 jump@$BASTION 2>/tmp/fwd-no.err &
    sleep 2; nc -w 2 127.0.0.1 15582 </dev/null 2>&1 | head -1
    kill %1 2>/dev/null; cat /tmp/fwd-no.err" 2>&1)"
ok "-O clear does not, to the same destination" \
   "$FWD_NO" "this certificate does not permit port forwarding"
note "the bastion's destinations_allowed admits this address in both cases."
note "The certificate says what the holder may ASK for; the lane says what"
note "it will CARRY. Both have to agree, and here only the first changed."

ok "an UNKNOWN extension is ignored, not refused" \
   "$(probe unknownext devuser $MULTI 2222 id)" "uid=10001(devuser)"
note "which is what makes a certificate issuable to a mixed fleet: a host"
note "that does not read an extension must not reject the holder for it."

# ======================================================== the critical options
h "CRITICAL OPTIONS / the half that refuses what it does not understand"
note "Same certificate, same CA, one field in a different bucket -- and the"
note "answer inverts. That inversion is the format's whole point: an option"
note "the endpoint cannot honour must not be silently dropped."

ok "an unknown critical option refuses the certificate" \
   "$(refused unknowncrit devuser $MULTI 2222 endhost-multi)" 'unsupported critical option \"x-hoop-probe@hoop.dev\"'

ok "force-command is refused too, and this one is an interop fact" \
   "$(refused forcecmd devuser $MULTI 2222 endhost-multi)" 'unsupported critical option \"force-command\"'
note "sshd honours force-command; this endpoint does not implement it and"
note "therefore refuses the certificate rather than ignoring the pin. A CA"
note "already stamping it for an sshd fleet cannot issue to these listeners"
note "unchanged. Loud, which is the right failure, but it is a real edge."

ok "source-address matching the client is admitted" \
   "$(probe srcok devuser $MULTI 2222 id)" "uid=10001(devuser)"

ok "source-address that does not match is refused" \
   "$(refused srcbad devuser $MULTI 2222 endhost-multi)" "is not allowed because of source-address restriction"
note "this pair is worth more than it looks. source-address is enforced by"
note "the ssh library AFTER the public-key callback returns, off the"
note "Permissions the callback handed back -- so a callback that returns a"
note "fresh empty Permissions drops the pin with no error anywhere, and a"
note "certificate locked to one network works from everywhere. The refusal"
note "above is the evidence that the value is being carried back."

# ================================================================ the mapping
h "IDENTITY MAPPING / which field carries the human is a config choice"
note "A certificate has no field called email and none called groups, so"
note "identity: is a mapping. The identity-ext lane reads two namespaced"
note "extensions instead of the key id -- same binary, same certificate"
note "format, different answer to 'who was that'."

ok "the extension lane admits the certificate" \
   "$(probe identityext devuser $MULTI 2224 'echo probe-idext')" "probe-idext"

IDEXT="$(docker compose exec -T endhost-multi sh -c 'grep probe-idext /tmp/audit.jsonl | tail -1')"
ok "and the trail names the EXTENSION, not the key id" "$IDEXT" '"principal":"alice@corp.example"'
no "the key id is not the subject on this lane"          "$IDEXT" '"principal":"ca-internal-ref-99"'
ok "though the key id is still recorded beside it" \
   "$(docker compose exec -T endhost-multi sh -c 'grep connection_open /tmp/audit.jsonl | tail -1')" \
   '"key_id":"ca-internal-ref-99"'
note "so a CA can keep principals a list of LOGIN NAMES -- all this endpoint"
note "checks them for -- and carry identity somewhere the login check will"
note "never be tempted to read."

note ""
note "And a certificate that fills NEITHER field is REFUSED, not admitted as"
note "anonymous. The actor column is not the only thing at stake: the policy"
note "context omits subject, email and groups when they are empty, so a Rego"
note "rule reading input.context.subject sees an absent key. The rule does not"
note "fire, and a command a named certificate is denied would run for this one."

NOID="$(probe noidentity devuser $ENDHOST 2222 id)"
ok "an empty key id is refused"                  "$NOID" "carries no identity"
ok "and the refusal names the field to fill"     "$NOID" "key_id"
no "nothing ran under it"                        "$NOID" "uid="

IDNONAME="$(probe idextnoname devuser $MULTI 2224 id)"
ok "so is a certificate missing the extension its lane maps" \
                                                 "$IDNONAME" "extensions.login@hoop.dev"
no "groups alone are not an identity"            "$IDNONAME" "uid="
note "both certificates are otherwise valid: right CA, right principal, in"
note "date. Only the field that names the human is empty."

printf '\n\033[1m%d passed, %d failed\033[0m\n\n' "$PASS" "$FAIL"
exit "$FAIL"
