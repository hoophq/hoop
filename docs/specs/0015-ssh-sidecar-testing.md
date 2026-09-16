# Testing the SSH lane (DEP-168 / ADR-0015)

How to verify the SSH lane, from the unit tests up to a real `ssh` client
against a running sidecar. Behaviour is specified by
[ADR-0015](../adr/0015-ssh-terminates-at-the-sidecar.md); the branch plan is
[0015-ssh-sidecar-impl.md](0015-ssh-sidecar-impl.md).

Every command below was run on branch `sandro/dep-168-ssh-sidecar-lane`
against libhoop `feat/v2-codec-ssh`, and the outputs quoted are the real ones.
Where something could not be verified from a non-interactive shell, it says so.

## Before you start

**The libhoop pin is not updated yet.** `sidecar/go.mod` still points at a
version with no `v2/codec/ssh`, so this branch does not build against the
published module. Until branch 1 merges, work against a local clone:

```bash
make libhoop-dev     # go work edit -replace github.com/hoophq/libhoop=./libhoop
```

Never commit that replace. `go.work` with a replace in it is a build that
ignores the pin CI tests.

You need an `ssh` client, `ssh-keygen` and `sftp` — the OpenSSH ones every
machine already has. Nothing else.

## Three layers, cheapest first

| Layer | Proves | Cost |
|---|---|---|
| 1. Unit tests | the decisions: admission, statements, refusals, masking, audit | seconds |
| 2. `-validate` | that a config is accepted or refused, and why | seconds |
| 3. A real client | that the wire actually works end to end | ~10 minutes |

Layer 1 is where the branch's own coverage lives. Layers 2 and 3 are what a
reviewer should run by hand at least once, because they exercise the seam
between this module and libhoop, which no unit test on either side crosses.

---

## Layer 1 — the automated tests

```bash
make test-sidecar        # every module under sidecar/, nested ones included
make test-oss            # the whole repo; test-sidecar is a dependency
```

Both must be clean. `make test-oss` takes several minutes, mostly
`gateway/models`.

To run only what this branch added:

```bash
cd sidecar
go test ./daemon/  -run TestSSH -v
go test ./policy/  -run 'TestOperationsScopes|TestUnscopedRule' -v
go test ./analyzer/ -run TestSSH -v
go test ./audit/   -run TestRedactStatementsCoversContentMetadata -v
cd config/yaml && go test ./... -run TestSSH -v
```

**What these cover:** the capability tri-state, every startup refusal, the
identity mapping, the statement shapes, both ends of a rename, the
destination check, masking with its fail-closed length rule, and the audit
records with their absence of content.

**What they deliberately do not cover:** the handshake, channel dispatch and
process spawn. Those are libhoop's mechanics and are tested in that repo.
Reaching them from here would need an SSH client, and that means
`golang.org/x/crypto` — a second direct dependency for a module whose whole
shape is one (`sidecar/CLAUDE.md`). Layer 3 covers them instead, by hand.

---

## Layer 2 — config refusals

Every refusal the design defines is reported at load, and all of them in one
run. This is the fastest way to see the surface.

```bash
WORK=$(mktemp -d)
ssh-keygen -t ed25519 -f "$WORK/hostkey" -N '' -q -C host
ssh-keygen -t ed25519 -f "$WORK/ca"      -N '' -q -C ca
```

### A config that should load

```bash
cat > "$WORK/good.yaml" <<YAML
listeners:
  - name: prod-endpoint
    protocol: ssh
    listen: 0.0.0.0:2222
    ssh:
      host_key: $WORK/hostkey
      trusted_ca: $WORK/ca.pub
      capabilities_allowed: [shell, pty, exec, env]
      identity: {subject: key_id, groups: principals}

  - name: prod-bastion
    protocol: ssh
    listen: 0.0.0.0:2223
    ssh:
      host_key: $WORK/hostkey
      trusted_ca: $WORK/ca.pub
      capabilities_allowed: []          # empty, not omitted: no session here
      destinations_allowed: ["10.0.0.0/8:2222"]
YAML

(cd sidecar/cmd && go run . -validate -config "$WORK/good.yaml")
```

Expect `config OK: 2 listener(s)` and, per lane, notes saying what it admits,
where it carries a forward and which account it runs as:

```
  prod-endpoint    ssh       enforcing 1 rule(s)
                               note: ssh: destinations_allowed is empty, so every client-opened forward is denied
                               note: ssh: every session runs as the login name the certificate admitted, ...
                               note: ssh: admits env, exec, pty, shell
  prod-bastion     ssh       no rules to enforce
                               note: ssh: carries forwards to 10.0.0.0/8:2222, and nowhere else
                               note: ssh: admits no session capability, so this listener has no shell, ...
```

Those notes are the point of the validate pass: the config file does not show
what a lane resolved to, and an empty destination list looks like a complete
bastion right up until the first forward is refused.

### A config that should be refused

```bash
cat > "$WORK/bad.yaml" <<YAML
listeners:
  - name: broken
    protocol: ssh
    listen: 0.0.0.0:2222
    upstream: db:5432
    ssh:
      host_key: $WORK/hostkey
      trusted_ca: $WORK/ca.pub
      capabilities_allowed: [shell, x11, shel]
      destinations_allowed: ["10.0.0.1"]
      identity: {subject: common_name}
    guardrails:
      rules:
        - {name: t, type: table, tables: [users]}
    mask:
      rules:
        - {entities: [EMAIL_ADDRESS]}
YAML

(cd sidecar/cmd && go run . -validate -config "$WORK/bad.yaml")
```

One run must report all seven problems. Anything reported one-per-restart is a
regression:

| Written | Refused because |
|---|---|
| `upstream` on an ssh lane | no fixed backend; an end-hop spawns locally, a bastion carries what the client picks |
| `x11` | the design names it, this version delivers no handler |
| `shel` | not a capability the design defines |
| `10.0.0.1` | not a network — write `10.0.0.5/32` for one host |
| `identity.subject: common_name` | not a certificate field this lane can read |
| a `table` rule | SSH has no relations; match a path with `pattern_match` |
| a mask rule with no strategy | the default is `redact`, which changes length |

Also worth trying by hand, each on its own:

- `capabilities_allowed:` with no value after it → refused. Absent means "the
  delivered set" and `[]` means "none"; a key with no value is asking for one
  of two opposite readings.
- `run_as: someone` anywhere in an `ssh` block → refused as an unknown key.
  It was a key in an earlier draft of ADR-0015; a config that still carries
  it must fail rather than have it silently ignored.
- `ssh:` block on a `postgres` lane, or a missing `ssh:` block on an `ssh`
  lane → both refused.
- `host_key` pointing at a file that does not exist → refused at load, not at
  the first login.

---

## Layer 3 — a real SSH client, end to end

This is the part worth doing before approving. It exercises the handshake, the
certificate check, the spawn and the audit trail together.

### Set up a CA and sign a user certificate

```bash
ssh-keygen -t ed25519 -f "$WORK/user" -N '' -q -C alice

# -I is the key id (which becomes the subject), -n the principals list.
# The login name you connect as MUST appear in -n.
ssh-keygen -s "$WORK/ca" -I alice@example.com -n "$USER" -V +1h "$WORK/user.pub"

ssh-keygen -L -f "$WORK/user-cert.pub"      # read it back
```

### Run the lane

```bash
cat > "$WORK/e2e.yaml" <<YAML
listeners:
  - name: prod-endpoint
    protocol: ssh
    listen: 127.0.0.1:2222
    ssh:
      host_key: $WORK/hostkey
      trusted_ca: $WORK/ca.pub
      capabilities_allowed: [shell, pty, exec, env, sftp]
      destinations_allowed: ["127.0.0.0/8:9999"]
      identity:
        subject: key_id
        groups: principals
        attributes: [permit-pty]
    guardrails:
      rules:
        - name: no-credential-reads
          type: pattern_match
          pattern_regex: '(cat|less|head|tail)\s+[^|;&]*(/etc/shadow|\.aws/credentials)'
          operations: [exec_line]
          message: reading credential material is not permitted
    mask:
      rules:
        - entities: [EMAIL_ADDRESS]
          strategy: mask
          mask_char: 42        # '*' — see the gotcha about mask_char below
audit:
  file: $WORK/audit.jsonl
log_level: info
YAML

(cd sidecar/cmd && go build -o "$WORK/hoop-inspect" .)
"$WORK/hoop-inspect" -config "$WORK/e2e.yaml" &
```

Note the config has exactly **one** guardrail rule and **one** mask rule. The
free tier caps both at one; see the gotchas.

Set the client options once:

```bash
SSHOPTS="-i $WORK/user -o CertificateFile=$WORK/user-cert.pub \
  -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
  -o IdentitiesOnly=yes"
```

`ssh` takes `-p 2222`; `sftp` takes `-P 2222`. They are not interchangeable.

### exec — allowed, and denied

```bash
ssh $SSHOPTS -p 2222 $USER@127.0.0.1 "id; uname -s"
```

Runs, and `id` shows your full group list — the lane resolves supplementary
groups the way `initgroups()` does, so a session is not silently missing
`docker` or `wheel`.

```bash
ssh $SSHOPTS -p 2222 $USER@127.0.0.1 "cat /etc/shadow"
```

```
hoop: reading credential material is not permitted
```

The rule's own message reaches the client. A generic "denied" would be a
regression: it leaves someone guessing which rule fired.

### env — the name is what a rule matches

```bash
ssh $SSHOPTS -p 2222 -o SetEnv="LD_PRELOAD=/tmp/evil.so" $USER@127.0.0.1 "echo ok"
```

Check the trail: the statement's text is `LD_PRELOAD`, and `/tmp/evil.so` is
beside it in `metadata["ssh.env_value"]`. A rule fencing `LD_PRELOAD` is
written against the variable, not against whatever an attacker puts in it.

### Masking — length is preserved, or the stream dies

```bash
ssh $SSHOPTS -p 2222 $USER@127.0.0.1 "echo 'contact: alice@example.com here'"
```

```
contact: ***************** here
```

Count the asterisks: seventeen, exactly the length of the address. That is the
whole safety property — a replacement of a different size shifts every byte
after it and corrupts a full-screen program.

### sftp — downloads masked, uploads refused

```bash
printf 'id,email\n1,bob@example.com\n' > "$WORK/emails.csv"

sftp $SSHOPTS -P 2222 -b - $USER@127.0.0.1 <<EOF
get $WORK/emails.csv $WORK/emails.got
EOF
cat "$WORK/emails.got"
```

```
id,email
1,***************
```

Now upload the same file:

```bash
sftp $SSHOPTS -P 2222 -b - $USER@127.0.0.1 <<EOF
put $WORK/emails.csv $WORK/uploaded.csv
EOF
ls "$WORK/uploaded.csv"
```

```
close remote: Permission denied
ls: .../uploaded.csv: No such file or directory
```

Refused at close, with **nothing written**. A download is masked because the
user should not see the value; an upload is refused rather than masked,
because silently altering a file the user believes they uploaded is worse than
refusing it. A file with no match uploads normally — check that too, or you
have only proved that uploads are broken.

### Forwarding — the destination list is the whole control

```bash
(echo "upstream-reached" | nc -l 9999 &)          # inside 127.0.0.0/8:9999

ssh $SSHOPTS -p 2222 -N -L 19999:127.0.0.1:9999 $USER@127.0.0.1 &
nc -w 2 127.0.0.1 19999                            # -> upstream-reached

ssh $SSHOPTS -p 2222 -N -L 18080:127.0.0.1:8080 $USER@127.0.0.1 &
nc -w 2 127.0.0.1 18080 </dev/null
```

```
channel 2: open failed: administratively prohibited:
  this listener does not carry forwards to 127.0.0.1:8080
```

The address checked is the address dialled — the endpoint resolves once and
connects to that exact value. A test that only proves "a hostname outside the
list is refused" has not tested the property that matters.

### Certificate trust

Four cases. The first three must all be refused.

```bash
# 1. A certificate signed by a CA this listener does not trust
ssh-keygen -t ed25519 -f "$WORK/rogueca" -N '' -q
ssh-keygen -t ed25519 -f "$WORK/rogue"   -N '' -q
ssh-keygen -s "$WORK/rogueca" -I mallory -n "$USER" -V +1h "$WORK/rogue.pub"
ssh -i "$WORK/rogue" -o CertificateFile="$WORK/rogue-cert.pub" \
    -o IdentitiesOnly=yes -o BatchMode=yes -p 2222 $USER@127.0.0.1 id

# 2. No certificate at all — see the gotcha, the key must be somewhere
#    with no <key>-cert.pub beside it
mkdir -p "$WORK/bare" && cp "$WORK/user" "$WORK/bare/key" && chmod 600 "$WORK/bare/key"
ssh -i "$WORK/bare/key" -o CertificateFile=none -o IdentitiesOnly=yes \
    -o BatchMode=yes -p 2222 $USER@127.0.0.1 id

# 3. A login name that is not in the certificate's principals
ssh $SSHOPTS -o BatchMode=yes -p 2222 nobody@127.0.0.1 id

# 4. An expired certificate: re-sign with -V -2h:-1h and retry
```

All four give `Permission denied (publickey)`. There is no password method and
no `authorized_keys` fallback to reach.

---

## What the audit trail must and must not hold

```bash
python3 - <<'PY'
import json, collections
c = collections.Counter()
for line in open('audit.jsonl'):
    e = json.loads(line)
    k = e['kind']
    if k == 'activity':
        k += ':' + e.get('metadata', {}).get('activity', '?')
    c[k] += 1
for k, v in sorted(c.items()):
    print(f'{v:4d}  {k}')
PY
```

```
   7  activity:connection_close      7  session_end
   7  activity:connection_open       7  session_start
   1  activity:forward_close         5  statement
   1  activity:forward_denied        1  violation
   1  activity:forward_open
   1  activity:forward_refused
   3  activity:session_close
   1  activity:sftp_transfer
```

Per capability:

| Capability | Expect to find |
|---|---|
| `exec` | a `statement` whose text is the command in full, with its verdict. A denial also writes a `violation` naming the rule |
| `env` | a `statement` whose text is the variable name, value in `metadata["ssh.env_value"]` |
| `sftp` | one `statement` per operation per path — **two for a rename**, marked `source` and `target` — plus an `sftp_transfer` activity with path, direction and byte count |
| `shell` | `session_start`, `activity:session_close` (geometry, duration, byte counts), `activity:connection_close`, `session_end`. **No statements** |
| forwards | `forward_open`/`forward_close`, or `forward_denied` and `forward_refused` |

### The assertion that matters most

```bash
ssh $SSHOPTS -p 2222 -tt $USER@127.0.0.1 <<'EOF'
echo SECRET-MARKER-12345
exit
EOF

grep -c "SECRET-MARKER-12345" "$WORK/audit.jsonl"       # must print 0
```

The marker appears on your terminal and **nowhere in the trail**. v1 records no
session content: no keystrokes, no output, no file bytes, and no setting that
would add them. If that grep ever returns non-zero, something has grown a
capture path and the change needs to go back to ADR-0015 first.

The same check for a download:

```bash
grep -c "bob@example.com" "$WORK/audit.jsonl"           # must print 0
```

### Redaction

With `audit.redact_statements` on, `statement` becomes a `sha256:` fingerprint
— and so does `metadata["ssh.env_value"]`, because an `env_set` matches on the
name and its value is statement content travelling in metadata. Paths are
**not** redacted: a trail that cannot say what was touched answers nothing.
That line matches how HTTP resources are already treated.

---

## Gotchas

Each of these cost time to find. None is a bug.

**The free tier caps rules at one of each.** Two guardrail rules gives
`2 guardrail rules are configured ... and this process enforces at most 1`.
Test one rule at a time, or run with a license.

**OpenSSH silently loads `<key>-cert.pub`.** Passing `-i key` also offers
`key-cert.pub` if it sits beside it, so the obvious way to test "no
certificate" actually tests "with certificate" and appears to show the lane
admitting a bare key. Copy the key somewhere with no certificate next to it,
and pass `-o CertificateFile=none`.

**`mask_char` is a number in JSON, not a character.** The field is a Go rune,
so YAML `mask_char: '*'` fails to decode; write `mask_char: 42`. On an ssh lane
it must be a single byte — `mask` preserves the rune count, and this lane needs
the byte count.

**`ssh -p`, `sftp -P`.** Putting `-p 2222` in a shared options variable makes
every `sftp` invocation print a usage message that says nothing about the port.

**pty geometry comes from the client.** An `ssh -tt` whose stdin is not a real
terminal sends `term: ""` and `0x0`, and the trail faithfully records that.
libhoop has a test pinning a real `120x40` request, so the plumbing is proven —
but the end-to-end geometry check is the one thing here that could not be
verified from a non-interactive shell. Run it from a real terminal and confirm
`cols`/`rows` are your window's.

**`"allowed": false` appears on every non-statement record.** The field is only
meaningful on `statement` and `violation`; it is unconditionally serialized, as
it already is for `session_start` and `session_end`. Filter denials on
`kind == "violation"`, never on `allowed`.

**Accounts and NSS.** The sidecar builds with `CGO_ENABLED=0`, so `os/user`
reads `/etc/passwd` directly and does not consult NSS. An account that exists
only in LDAP or SSSD will not resolve, and the session is refused. The login
shell is read from the same file, so an account resolving through NSS and
absent from `/etc/passwd` is refused too — its shell cannot be read, and that
field is how a host disables an account.

**Login shells.** A session runs the account's own shell. `usermod -s
/sbin/nologin alice` disables her here exactly as it does everywhere else on
the host, because the shell is what refuses. A shell that does not exist or
is not executable refuses the session at resolution, with a message naming
the file.

**sftp and the account.** File transfer runs in-process rather than in a
child, so it is served only when the sidecar already IS the account the login
name resolved to. `-validate` reports the uid and gid it can serve whenever
`sftp` is admitted; a session for any other account gets the subsystem
refused with the same comparison.

---

## Not covered yet

- **No e2e suite case.** `sidecar/e2e` boots a container per protocol; an SSH
  case needs a client in the image. Open decision 2 in the impl spec.
- **`remote_forward`, `agent_forward`, `x11`, other subsystems.** No handler
  exists. They are refused at config load, so there is nothing to test at
  runtime beyond that refusal.
- **Confining a transfer to a directory.** ADR-0015 names it a non-goal. An
  end-hop that admits `sftp` reaches the whole filesystem the account can, so
  do not deploy one without reading that section.

## Cleanup

```bash
kill %1                 # the sidecar
rm -rf "$WORK"
```
