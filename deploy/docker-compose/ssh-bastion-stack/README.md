# The terminating bastion: inspecting hosts that run nothing

Every other SSH topology hoop ships puts the inspecting endpoint on the
**target**. That is ADR-0015, and it rests on a precondition it states
plainly: a hoop sidecar runs on every host you want to inspect.

It does not hold everywhere. Appliances take configuration but not binaries.
Managed instances run an image you do not control. Hosts sit under
change-freeze. And "inspect these twelve hosts by Friday" cannot mean twelve
installs. For those the choice today is no inspection at all.

This stack is the fourth topology — ADR-0027 — where the **bastion is the
endpoint and the target runs nothing**:

```
  client ──hop 1──> bastion ──hop 2 inside hop 1's channel──> bastion
                       │                                        │
                       └──────── upstream, a connection ────────┘
                                 the client never makes
                                          │
                                          v
                                    stock sshd
```

The client still makes exactly two hops, the same two as every ADR-0015 mode.
What differs is where hop 2 lands: on the target's sidecar there, on the
bastion here. That is why the bastion never sees hop 2's plaintext in
ADR-0015 and sees all of it in this one.

**The cost is real and this document does not bury it.** The bastion holds
the plaintext of every session for every host behind it, and a credential
that reaches most of them. A blind bastion holds neither. Blast radius
changes shape: ADR-0015's modes degrade per host, this one takes the fleet.

## Contents

1. [Before you start](#before-you-start)
2. [The client types a real hostname](#1-the-client-types-a-real-hostname)
3. [The gates are the same gates](#2-the-gates-are-the-same-gates)
4. [A ceiling per target](#3-a-ceiling-per-target)
5. [Three credential sources](#4-three-credential-sources)
6. [The host key, and how strictly it is checked](#5-the-host-key-and-how-strictly-it-is-checked)
7. [A forward the target dials](#6-a-forward-the-target-dials)
8. [What is refused, and why](#7-what-is-refused-and-why)
9. [The host that is not inspected](#8-the-host-that-is-not-inspected)
10. [The trail has two hops](#9-the-trail-has-two-hops)
11. [Configuration that fails at load](#10-configuration-that-fails-at-load)
12. [What this costs](#what-this-costs)
13. [Files](#files)

## Before you start

```bash
./run.sh          # build, generate keys, start, and print what -validate says
./demo.sh         # every claim below, as an assertion
```

`./run.sh` needs a `libhoop` checkout, which is a **private** module — so this
stack is runnable by hoop engineers alone until ADR-0027 ships in a release.
There is no published-image mode, deliberately: a released image carries no
relay code, and a stack whose whole subject was missing would fail in a way
that looks like a configuration mistake.

### Where each command runs

Two places, and every block below says which:

```
# host    — from this directory, on your machine
# client  — inside the client container
```

The client is where a user's `ssh` lives; the host is where you read the
bastion's trail, a target's own log, and the files `run.sh` generated. To get
a client shell:

```bash
# host
docker compose exec client sh
```

Every `# client` block also runs from the host by prefixing it with
`docker compose exec client`, which is how `demo.sh` drives all of them.

### What is where

| Host | Address | Matched by | Credential | Admits |
|---|---|---|---|---|
| bastion | `172.31.78.10:2222` | — | — | no session of its own |
| `host-a.prod` | `172.31.78.20` | the `*.prod` **glob** | shared key, or an enrolled subject's own | `exec`, `env` |
| `host-b.prod` | `172.31.78.21` | its **name** | enrolled subjects only | `exec`, `env`, `pty`, `shell`, `local_forward` |
| `host-c.prod` | `172.31.78.22` | its **name** | the client's own agent | `exec`, `env`, `pty`, `shell` |
| `legacy-01` | `172.31.78.23` | its **name** | shared key, login `ec2-user` | `exec`, `env` |
| `blind-01` | `172.31.78.24` | **nothing** | none — carried blind | everything, uninspected |

Four of those are inspected and one is not, and the difference is four lines
in [`bastion/config.yaml`](bastion/config.yaml). That contrast is the point of
the last host.

**Every target is a stock `sshd`.** Their entire hoop footprint is one
`authorized_keys` line, or in two cases one `TrustedUserCAKeys` line. Nothing
is installed on them. You can read the whole of it in
[`targets/`](targets/) — it is twenty lines of Dockerfile and two sshd_configs.

### The client cost, in full

[`client/ssh_config`](client/ssh_config) is the whole of it:

```
Host *.prod legacy-01
    ProxyJump jump@172.31.78.10:2222
    HostKeyAlias hoop-bastion
    User devuser

Host host-c.prod
    ForwardAgent yes
```

Two stanzas, neither naming a host. Adding a target to the fleet changes
nothing here — the property that makes an evaluation across twelve hosts a
config change on one box.

## 1. The client types a real hostname

```bash
# client
ssh host-a.prod 'hostname; whoami'
```
```
host-a
devuser
```

A command ran on a host with no hoop component on it. Nothing about the
command line says "bastion".

### What actually happened

**There is no SNI in SSH.** Nothing in the transport names the host the user
meant — which is the constraint that decides the whole client experience.
`ProxyJump` does not log in to the bastion and run `ssh` there. It opens a
`direct-tcpip` channel, and the literal string you typed travels in that
channel-open request:

```
  1. client -> bastion    hop 1: certificate verified, permit-port-forwarding
                          checked, destinations_allowed checked on the
                          RESOLVED address
  2. bastion              "host-a.prod" matches relay.targets -> TERMINATE
                          (no match, and the forward is carried blind)
  3. client -> bastion    hop 2: the SAME certificate, verified AGAIN, inside
                          hop 1's channel
  4. bastion -> host-a    the upstream. The client is not party to it.
```

**The certificate is verified twice, and that is deliberate.** Hop 1
authorises opening a forward; hop 2 authorises a session on whatever
answered. They are separate handshakes with independent trust decisions, and
collapsing them would mean the inner server trusting a claim the outer one
made about a connection it no longer controls.

**Reachability is decided before termination, not after.** Step 1's
`destinations_allowed` check runs first, on the resolved address. A host named
in `relay.targets` that the destinations list does not cover can never be
reached — so `relay.targets` cannot become a second, quieter way to widen
reach.

### What `HostKeyAlias` is doing

The bastion terminates hop 2, so it presents **its own** host key where the
target's would be. Without `HostKeyAlias` every connection is a host-key
mismatch.

Two lines of client config point every target's host-key lookup at one alias,
so one `known_hosts` entry covers a fleet. The cost is named in the ADR and
does not go away: a user who bypasses the bastion gets a first-contact prompt
rather than a mismatch warning, so network policy has to carry what host-key
checking no longer does.

A host CA would be better — one `@cert-authority` line client-side, the
bastion minting a host certificate per target, and per-target host identity
stays meaningful. It is deferred, not rejected, and moving later is a
client-config change.

### There is no shell on the bastion

```bash
# client
ssh -p 2222 -o HostKeyAlias=hoop-bastion jump@172.31.78.10
```
```
channel 0: open failed: administratively prohibited:
  no session capability is permitted on this connection
```

The listener's `capabilities_allowed` is `[shell, pty, exec, env]` — but that
list is the **ceiling each target narrows**, not what runs here. A terminating
bastion serves no session of its own: it has no accounts, and a host that
could run one would be worth attacking for more than its credentials. The
container has no `devuser` at all.

## 2. The gates are the same gates

The whole chain applies — to a host that has never heard of hoop.

```bash
# client
ssh host-a.prod 'cat secrets.env'
```
```
hoop: this path is not readable through hoop
```

**The target never learned the command was attempted.** Check for yourself:

```bash
# host
docker compose logs host-a | grep secrets.env    # nothing
```

A blocked command is refused **before the upstream channel is written**. That
is strictly more than an end-hop offers, where the command reaches the host
and is refused there.

### Masking, over a network hop

```bash
# client
ssh host-a.prod 'cat team.txt'
```
```
contact: *****************
```

The bytes came from another host rather than a local process, and the rewrite
hook ran on them just the same. An ssh lane masks a byte stream **in place**,
so `mask` is the only strategy it accepts: a replacement of a different length
would shift every byte after it and corrupt a full-screen program.

### No guardrails on an interactive shell

ADR-0015 decided this and terminating in the middle changes nothing about it.
Nothing makes a keystroke stream into statements: `bash` holds the terminal in
raw mode at its own prompt because it does its own line editing, so keystrokes
reassemble into text the user never typed. A control that fires
inconsistently, beside controls that hold, will be read as equivalent to them.

A shell is admitted by capability, masked on the way out, and recorded as
events.

## 3. A ceiling per target

```bash
# client
ssh -T host-a.prod          # shell request failed on channel 0
ssh host-b.prod 'hostname'  # host-b
```

The listener admits `shell`. `host-a.prod` is matched by the `*.prod` glob,
whose entry admits `exec` and `env`, so a shell there is refused.
`host-b.prod` is matched by **name**, and its entry admits a shell.

**Precedence is total and fixed**: exact name, then glob, then literal
address, then longest prefix. Without that, map iteration would decide which
rule applied — per connection, differently each time. A name key is matched
*before* resolution and an address key *after*, which is what keeps a name key
from widening reachability.

A target's list is tri-state, with the same three readings the listener's own
list has:

| `capabilities_allowed` on a target | admits |
|---|---|
| absent | whatever the listener admits |
| `[]` | nothing — a target reached only by a forward |
| populated | those, and refuses the rest |

It can only ever **narrow**: naming a capability the listener does not admit
fails at load, with the conflict named. `sftp` on the listener is dropped
rather than inherited, because a terminated session cannot carry it.

## 4. Three credential sources

The problem this topology has and ADR-0015's does not: a bastion that
terminates hop 1 holds the user's **certificate** but not the private key that
signs for it. Certificate pass-through is arithmetic, not an oversight.

### A shared key — the floor every target has

`host-a.prod` takes `private_key` from its `*.prod` entry. The target's
records name an account rather than a person:

```bash
# host
docker compose logs host-a | grep Accepted
```
```
Accepted publickey for devuser ... ED25519 SHA256:cpqiXe+...
```

hoop's trail is the only place the human appears.

### A key per person — `identities`

`ssh.relay.identities` is a directory whose **filenames are certificate
subjects**, read from disk **per session**. Enrolling or removing somebody
takes effect on their next connection, with no restart.

That does mean a subject the certificate carries becomes a filesystem path,
so the lookup refuses any subject that is not a plain filename — a separator,
a `..`, an absolute path or a leading dot is rejected before the path is
built, and the result is checked to be inside the directory. A shorter way to
say it: the directory is the boundary, and it is enforced rather than assumed.

A file that is present but **unusable** — the wrong permissions, or a public
key where the private half belongs — refuses the session instead of falling
through to the shared key. Somebody deliberately enrolled that person, and
quietly serving them as the account that names nobody is the silent downgrade
this whole design refuses.

`host-b.prod` carries **no** `private_key`, so only enrolled subjects reach
it. `mallory@example.com` is not enrolled:

```bash
# client
ssh -i probe/unenrolled -o CertificateFile=probe/unenrolled-cert.pub \
    host-b.prod 'hostname'
```
```
hoop: you do not have access to this host
```

**Refused, not downgraded.** Every fallback considered was rejected for one
reason: the downgrade is silent and always runs toward the credential that
names nobody.

### The client is told less than the trail, deliberately

That message is short on purpose. A refusal is the one thing an unauthorised
party can make this listener produce on demand, so it carries nothing that
describes the deployment — not the identities directory, not which config key
decided, and not whether `mallory@example.com` is enrolled, which would turn
every refusal into an enrolment oracle for anyone holding any certificate.

The operator loses nothing. The same refusal, in the trail:

```bash
# host
docker compose exec bastion grep upstream_credential_refused /tmp/audit.jsonl
```
```
"target":  "host-b.prod"
"subject": "mallory@example.com"
"reason":  "target \"host-b.prod\" admits enrolled subjects only and
            \"mallory@example.com\" is not enrolled in
            /etc/hoop-inspect/identities; the target carries no private_key,
            so there is nothing shared to fall back to ..."
```

Every refusal in this design is written twice like that. A client asking for
a forward outside the allowlist is told `127.0.0.1:6379 is not a destination
this host may reach` — the destination it named, so it can act, and not the
list or the target key. A host-key mismatch is told the key did not match,
and not the address, the fingerprint or the file. A target that cannot be
reached is told exactly that, and not where it is.

#### The overlay is LANE-WIDE, and this surprises people

`identities` is not per target. An **enrolled subject reaches every target
under their own key**, and a target's `private_key` is the credential for
people who are *not* enrolled. That is why `host-a`'s `authorized_keys` in
this stack holds both the shared key and alice's.

The same unenrolled subject *is* carried on `host-a.prod`, because that target
does have a shared key:

```bash
# client
ssh -i probe/unenrolled -o CertificateFile=probe/unenrolled-cert.pub \
    host-a.prod 'hostname'                       # host-a
```
```bash
# host
docker compose logs bastion | grep "fell back"
```
```
WARN ssh upstream credential fell back to the target's shared key
  target=*.prod subject=mallory@example.com
```

**Logged every time**, because the failure mode of an overlay is silent
degradation: a subject format changed at the IdP, or a filename that no longer
matches, keeps working on the shared key and quietly stops naming anyone. A
target meant to be per-user should omit `private_key` once its people are
enrolled; carrying one under `identities` is a migration position, not a
steady state.

### The user's own certificate — `agent_identity`

`host-c.prod` sets `agent_identity: true` and the bastion holds **no**
credential for it at all.

```bash
# client
ssh -o ForwardAgent=no host-c.prod 'hostname'
```
```
hoop: this host authenticates with your own forwarded agent, and this session
offered none. Add `ForwardAgent yes` to its ssh_config stanza, beside the
ProxyJump it already carries
```

That one stays long, and it is the exception that shows the rule: every word
of it is about the CLIENT's own configuration. Telling someone to add a line
to their own `ssh_config` reveals nothing about the server, and withholding it
would turn a one-line fix into a support ticket. The host is called "this
host" rather than named by its config key, which may be a glob covering a
fleet the client never saw.

With an agent:

```bash
# client
eval $(ssh-agent -s); ssh-add ~/.ssh/id_ed25519
ssh host-c.prod 'hostname'          # host-c
```

And now look at what **the target itself** recorded:

```bash
# host
docker compose logs host-c | grep "Accepted certificate"
```
```
Accepted certificate ID "alice@example.com" (serial 1) signed by ED25519 CA
  ... via /etc/ssh/hoop_ca.pub
```

**A person's name, in a stock sshd's own log, on a host configured with one
line.** No key is stored anywhere, and nothing is provisioned or deprovisioned
on the target. Its operational cost is *lower* than a stored key's and its
identity fidelity higher — an unusual pairing, and the reason it is taken
despite costing a signing oracle at the bastion for the life of the session.

**It arrives at hop 2, not hop 1.** `ProxyJump` opens no session channel on
the bastion, and `auth-agent-req@openssh.com` is a session request. That is a
property of the topology, and it means the upstream dial moves to the first
session channel — so an upstream failure surfaces at first use rather than at
connect.

**The bastion consumes the agent; it never forwards it.** The channel is
opened to sign and closed with the session. Proxying it to the target would
hand the host the session exists to watch an identical ability to sign as the
user — the hazard `ssh -A` is known for, pointed at the worst possible place.
The two are one config line apart, which is why `agent_forward` stays
undelivered while the agent is accepted as a credential.

A user CA on the bastion would remove the client requirement, and the POC
implemented it. **It is rejected on custody**: a user CA's private half mints
a session as any principal on every host that trusts it, and this would put it
on the box with the largest pre-authentication attack surface in the
deployment — one already holding the plaintext of every session behind it.
Compromise of a bastion should cost the sessions it carries, not the fleet's
authentication.

### A different account on the target

```bash
# client
ssh legacy-01 'hostname; whoami'
```
```
legacy-01
ec2-user
```

The client typed no account. `login: ec2-user` on that target overrides the
lane's, and the lane's default is the name the client asked for — which the
certificate's principals vouched for.

## 5. The host key, and how strictly it is checked

The bastion is the client here, and the only component positioned to notice
that the host behind it was replaced. `HostKeyAlias` already moved host
identity off the client, so **this check is what remains of host identity in
the whole path**.

| `host_key_check` | Host not in `known_hosts` | Key present but changed |
|---|---|---|
| `strict` (default) | refuse | refuse |
| `accept_new` | learn it, write it, continue | **refuse** |
| `off` | connect | connect |

**`accept_new` refusing a changed key is the whole of its value.** It gives up
detection at first contact and keeps it for every session after, which is why
it is not trust-on-first-use renamed: a rebuilt host still fails.

`legacy-01` uses it, and its file starts empty:

`state/` is a directory on the HOST, mounted into the bastion, so all three
of these run on the host rather than in the client container:

```bash
# host
cat state/known_hosts.legacy                        # empty: run.sh truncates it

docker compose exec client ssh legacy-01 hostname   # first contact

cat state/known_hosts.legacy
```
```
legacy-01 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5...
```

**Nobody saw what it learned.** A client's prompt puts a fingerprint in front
of a person who could compare it; the bastion runs unattended and has no one
to ask. So the host and its fingerprint go to the trail as a first-contact
event — reviewable afterwards, not before:

```bash
# host
docker compose exec bastion grep host_key_first_contact /tmp/audit.jsonl
```

**`accept_new` makes `known_hosts` a state file**, and that has a concrete
deployment consequence this stack learned the hard way: a key is learned by
**renaming** a new file over the old one, and a single file bind-mounted into
a container refuses that rename with `EBUSY` however writable it looks. So
`legacy-01`'s file lives in a mounted **directory**, not as a mounted file.
The load-time check performs the real rename with the file's own contents, so
a deployment that could not learn is refused at startup rather than at three
in the morning.

**`off` is loud**, and it costs more than `StrictHostKeyChecking=no` usually
does. A client disabling the check gives up its own detection and nothing
else; here the client already gave that up to `HostKeyAlias` and could not
notice a target being replaced if it wanted to. So naming it warns at load
with the listener named, and every session under it carries an audit event
recording that the host key was not verified.

## 6. A forward the target dials

Much of what people reach a host for does not listen on the network at all.
`host-b.prod` runs Postgres on its **own loopback**:

```bash
# client
ssh -f -N -L 15432:127.0.0.1:5432 host-b.prod
PGPASSWORD=demo psql -h 127.0.0.1 -p 15432 -U demo -d demo \
    -tAc 'select inet_server_addr();'
```
```
127.0.0.1
```

**The TARGET made that TCP connection**, on its own host and in its own
network. The bastion opened a `direct-tcpip` channel on its **upstream**
connection and relayed the two together.

### The rejected design was a security bug, and you can see it

Dialling the destination from the bastion is the obvious implementation — the
outer forward handler already resolves and dials. It is rejected, and not as a
limitation: `localhost` would resolve to the **bastion**, so a user asking for
the target's loopback would silently reach a different and probably more
privileged service.

This stack runs a decoy on the bastion's own `127.0.0.1:5432` to prove you
never reach it. If the rejected design were in place, the query above would
have returned `IF-YOU-SEE-THIS-THE-BASTION-DIALLED-IT`.

### The allowlist is what keeps the exception deliberate

**The bytes are carried without inspection, and this is an explicit
exception.** Guardrails and masking act on statements and terminal output; an
arbitrary TCP stream has neither. Only metadata is recorded — key id, target,
destination, open and close times, byte counts, and every refusal with its
reason.

So a target admitting `local_forward` **must** declare `forwards_allowed`.
Admitting the capability without the list fails config validation rather than
loading as an allow-everything tunnel:

A local forward opens no channel until something connects to the local port,
so the refusal arrives on first use rather than at setup:

```bash
# client
ssh -f -N -L 16379:127.0.0.1:6379 host-b.prod
nc -w 2 127.0.0.1 16379 </dev/null     # closes immediately: refused
```
```bash
# host
docker compose logs bastion | grep "forward refused"
```
```
ssh forward refused  target=host-b.prod destination=127.0.0.1:6379
  reason="127.0.0.1:6379 is not a destination this host may reach"
```

Exfiltration through an *allowed* destination is visible as volume and timing,
never as content — which is the argument for keeping those lists narrow.

**It does not reopen SFTP.** File transfer has a framed wire format and a
vocabulary this project has defined, so relaying it opaquely would discard
inspection that is achievable and specified. A forwarded TCP stream has no
protocol known in advance.

**One thing this does not do:** the certificate's own grant does not narrow
it. `permit-port-forwarding` is already required to use the bastion as a jump,
so every user holding a usable certificate has it. Anyone who can reach a
target gets that target's whole allowlist.

## 7. What is refused, and why

| Capability | On a terminated target |
|---|---|
| `exec` | full — guardrails, analyzer, output masking, the command audited |
| `env` | full — guardrails on the name, audit |
| `shell`, `pty` | admitted, output masked, recorded as events; **no guardrails, no content trail** |
| `sftp` | **not delivered.** Naming it on a target fails at load |
| `local_forward` | **delivered**, carried uninspected, bounded by `forwards_allowed` |
| `remote_forward` | **not delivered.** Naming it on a target fails at load |
| `agent_forward`, `x11` | not delivered |

```bash
# client
sftp host-b.prod
```
```
hoop: file transfer is not delivered over a terminated session; against a
remote sftp-server it is an opaque subsystem stream, and path gating and
download masking would need it decoded both ways
```

**Refused at load rather than relayed opaquely.** On an end-hop the sidecar
*is* the file-transfer server and sees decoded operations. Relaying it
un-decoded would leave file transfer the one un-inspected path on a listener
whose purpose is inspection.

`ssh -R` stays refused for a different reason: it exposes your machine to the
target rather than the reverse, and needs a listener on the target plus
reverse channel plumbing nothing here has.

## 8. The host that is not inspected

`blind-01` is reachable — `destinations_allowed` covers it — and named nowhere
in `relay.targets`. Same bastion, same client, same certificate:

```bash
# client
ssh blind-01 'cat secrets.env'
```
```
DB_PASSWORD=hunter2
OWNER=alice@example.com
```

The blocked path is readable. The address is **not masked**. Compare with
part 2, where the same command on `host-a.prod` was refused.

```bash
# host
docker compose exec bastion grep blind-01 /tmp/audit.jsonl
```
```
"activity":"forward_open","destination":"blind-01:22","dialing":"172.31.78.24:22"
"activity":"forward_close","bytes_in":"4573","bytes_out":"4249",...
```

A destination and a byte count. No statements, no session, nothing to mask —
the bastion relayed bytes it could not read, and `blind-01` verified the
client's **own** certificate at the far end. That is exactly ADR-0015's
behaviour, and it is still available; what this record changes is that it is
now **a visible choice rather than the only one**.

This is also the honest summary of the trade. The inspected hosts cost you a
bastion that decrypts them. The uninspected one costs you nothing and tells
you nothing.

## 9. The trail has two hops

```bash
# host
docker compose exec bastion grep '"hop":"2"' /tmp/audit.jsonl | head -1
```
```json
{"activity":"connection_open","hop":"2","target":"host-b.prod",
 "key_id":"alice@example.com","login":"devuser","serial":"1", ...}
```

Both hops present the same certificate, so without `hop` and `target` the two
open events would be identical and an auditor could not tell the jump from the
session that reached a host.

The events a terminated session produces:

| Event | What it records |
|---|---|
| `connection_open` / `connection_close` | once per hop; hop 2 carries `hop` and `target` |
| `relay_open` / `relay_close` | a forward that was terminated, and which target |
| `forward_open` / `forward_close` | a forward that was carried, with byte counts |
| `session_close` | duration, byte counts, exit status, terminal geometry |
| `host_key_first_contact` | `accept_new` learned a key; host and fingerprint |
| `host_key_unverified` | `off` connected without checking |
| `upstream_credential_fallback` | an enrolled-subject lookup missed and a shared key was used |
| `upstream_credential_refused` | no credential resolved; the session was refused |
| `capability_refused` | something the target does not admit was asked for |

## 10. Configuration that fails at load

Every path is read and checked before anything binds. Try breaking
[`bastion/config.yaml`](bastion/config.yaml) and running `./run.sh validate`:

| What you write | What you get |
|---|---|
| `private_key` pointing at a `.pub` file | *this is a PUBLIC key; the private half is what authenticates* |
| a key readable by group or other | *ssh(1) refuses a key with these permissions and so does this* |
| `agent_identity` **and** `private_key` | *two credential sources, not a preference and a fallback* |
| `local_forward` with no `forwards_allowed` | *the allowlist is the only control that bounds them* |
| `forwards_allowed` with no `local_forward` | *the list bounds nothing* |
| `capabilities_allowed: [sftp]` on a target | *an opaque subsystem stream ... decoded both ways* |
| a capability the listener does not admit | *a target NARROWS the listener's list and cannot widen it* |
| `targets: {}` | *a listener you believe is inspecting and is not* |
| `host_key_check: yes` | *write strict, accept_new or off* |
| `accept_new` on an unwritable path | *has to be REPLACEABLE and not merely writable* |
| a typo anywhere in the block | the key, named |

**The whole `relay` block is listener topology**, so changing it is a restart
under ADR-0014's rule-only hot-reload boundary — a credential, a target's
ceiling or a host-key setting all move the baseline.

`identities` is the exception, and knowingly so: its keys are read per
session, so a file drop grants access the moment it lands and a deletion
revokes it just as fast. That is the convenience the per-session read buys,
and the cost is that **write access to that directory is equivalent to
granting SSH to every target the overlay reaches.** Mount it read-only from
wherever your enrolment actually lives.

## What this costs

**Easier.** A host that cannot take a sidecar can be inspected, and the
install cost of adoption drops to zero per target. One ingress address holds
the policy for a fleet. A blocked command never reaches the target.

**Harder, and this is the trade.** The bastion holds the plaintext of every
session for every host behind it, and a credential that reaches most of them.
This **inverts** ADR-0015's security story for the listeners that use it
rather than extending it, and makes that host the highest-value target in the
deployment. Two key exchanges per connection at one process, and masking
latency now includes a network round trip.

**The provisioning bill is real.** One identity key per subject on every host
they may reach, removed when they leave: for N people and M hosts, a table
maintained by hand, with no tooling here and no way to detect drift. That is
the bill an SSH CA exists to remove, which is why `identities` is a bridge and
`agent_identity` is the better answer wherever a client has an agent.

Every key installed this way should also carry `from="<bastion addresses>"` in
the target's `authorized_keys` — the control that turns a leaked key from
fleet access *from anywhere* into access that also requires standing on the
bastion. hoop cannot enforce it; it lives on the target and drifts like
deployment instructions. This stack does not set it, so that the
`authorized_keys` files stay readable as the one line they are.

## Known gaps

- **`agent_identity` is for people, not automation.** No agent, no session, by
  decision. A certificate carrying `source-address` is checked against the
  **bastion's** address, so certificates pinned to user networks stop working
  through a bastion at all; and an agent that goes away mid-session fails the
  next dial with no way to re-authenticate.
- **No per-user or per-group forwarding grants.** Anyone who can reach a
  target gets that target's whole `forwards_allowed`.
- **No host CA.** `HostKeyAlias` is the v1 answer and it collapses per-target
  host identity into one key.
- **No published image.** See [Before you start](#before-you-start).

## Files

| Path | What it is |
|---|---|
| [`bastion/config.yaml`](bastion/config.yaml) | the listener, and the `relay` block that is the whole mode switch |
| [`bastion/Dockerfile`](bastion/Dockerfile) | the sidecar, built from local source |
| [`targets/Dockerfile`](targets/Dockerfile) | a stock sshd, and the accounts it has |
| [`targets/sshd_config.keys`](targets/sshd_config.keys) | a host authenticated by `authorized_keys` |
| [`targets/sshd_config.ca`](targets/sshd_config.ca) | a host that trusts the user CA and holds no per-user state |
| [`client/ssh_config`](client/ssh_config) | the entire client cost: two stanzas |
| [`docker-compose.yml`](docker-compose.yml) | six hosts, and why each one differs |
| [`run.sh`](run.sh) | key material, build, start, `-validate` |
| [`demo.sh`](demo.sh) | every claim above, as an assertion |

Related: [`../ssh-stack/`](../ssh-stack/) is ADR-0015's three topologies,
where the endpoint is on the target and the bastion is deliberately blind.
