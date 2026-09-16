# SPEC: SSH lane in the sidecar — ADR-0015 implementation

Source of truth for behaviour: [ADR-0015](../adr/0015-ssh-terminates-at-the-sidecar.md).
This file plans the work; where the two disagree, the ADR wins and this file
is wrong.

## Two branches, two PRs, one deliverable

| # | Repo | Branch | Carries | Spec |
|---|---|---|---|---|
| 1 | `hoophq/libhoop` | `feat/v2-codec-ssh` | the SSH endpoint, the SFTP decoder, the operation vocabulary | `SPEC-0015-ssh-endpoint.md` in that repo |
| 2 | `hoophq/hoop` | `feat/ssh-sidecar-lane` | the lane: config, admission, account resolution, `destinations_allowed`, gate/policy/audit/mask wiring | this file |

There is no third branch and no branch per phase. The phases below are the
order of commits **inside** branch 2; branch 1's phases are in its own spec.
Both branches ship together: nothing in either is useful alone.

**Ordering is forced by the module graph.** `sidecar/` imports libhoop and
libhoop imports nothing back, so branch 1 merges first, and branch 2's last
commit pins the resulting version. Until then, work against the local clone:

```bash
make libhoop-dev     # go work edit -replace github.com/hoophq/libhoop=./libhoop
                     # ./libhoop is a symlink to the sibling clone
```

**Never commit that replace.** `go.work` with a replace directive in it is a
build that ignores the published pin, which is the thing CI tests.

If a Linear ticket exists, use its `branchName` instead of the names above and
start each commit with the ticket ID.

## What this branch may not do

- `sidecar/` keeps **exactly one dependency**, libhoop (`sidecar/CLAUDE.md`).
  The SSH lane adds no third. `golang.org/x/crypto/ssh` is already a libhoop
  dependency and stays on that side of the seam.
- No import from `gateway/`, `agent/`, `client/` or `common/`.
- **No feature flag.** Decided, not merely unavailable: `common/featureflag`
  is unreachable from `sidecar/` anyway, and the config file is a better
  switch than a flag would be — no `ssh` listener means no lane and no linked
  code, and an operator turning the lane off is deleting a listener rather
  than toggling a global.
- No change to `common/proto/transport.proto` or any packet type. The sidecar
  is its own binary; the gateway↔agent contract is untouched, so this is
  `minor`, never `major`.

## Phases

Each phase is a commit that builds and tests green on its own. A phase that
admits a capability whose enforcement is not yet written must refuse it at
load, not admit it (No Hacks, test 3).

### H1 — Vocabulary

`sidecar/inspect/wiretypes.go`

Alias the protocol and the twelve operations libhoop defines: `exec_line`,
`env_set`, and the ten `sftp_*`. Aliases, never copies — one definition of the
document a policy evaluates.

SSH registers **no codec**, for the same reason gRPC does not: there are no
relay bytes to decode from a registry position. Add an `isSSH(ListenerConfig)`
helper next to `isGRPC` and give it the same bypass in
`Config.validate`, so `inspect.New` is never asked for a protocol it cannot
answer for.

**Done when** the constants exist, `go test ./inspect/...` passes, and a
listener with `protocol: ssh` no longer fails with `unsupported protocol`.

### H2 — Config schema and every refusal

`sidecar/daemon/config.go`, new `sidecar/daemon/ssh_config.go`,
`sidecar/config/yaml/yaml.go`

`SSHConfig` on `ListenerConfig` as `SSH *SSHConfig` beside `HTTP` and `GRPC`.
Keys are exactly the ADR's table and no others.

`capabilities_allowed` is **tri-state**, so it cannot be a `[]string`: absent,
empty and populated are three different configs. Use a pointer with a custom
`UnmarshalJSON` (`*Capabilities`) and keep `nil` meaning absent through the
whole resolve path. Resolve to an explicit set exactly once, at load, and hand
libhoop only the resolved set — the tri-state must not exist twice.

`destinations_allowed` parses at load into `netip.Prefix` plus an optional
port, `any` included. A parse failure is a startup refusal; absent and empty
both resolve to the empty set, which denies.

Refusals this phase owns, each with a test that asserts the message:

| Refusal | Why |
|---|---|
| `upstream`, `upstream_tls`, `downstream_tls` on an ssh lane | no fixed upstream, and SSH negotiates its own transport. `Upstream == ""` is currently a global error — exempt ssh |
| `run_as`, or any other unknown key in the `ssh` block | the account is the login name and no key names it; a config still carrying the key must fail rather than have it ignored |
| a capability name the design does not define | default-deny means the surface is closed, so an unknown name is a typo, not a future |
| `remote_forward`, `agent_forward`, `x11`, `subsystem` | v1 delivers no handler; naming one asks for what cannot run |
| rule types `table`, `http_resource`, `http_status`, `grpc_status` | nothing on an SSH lane for them to read |
| a mask strategy other than `mask` | a variable-length replacement desynchronizes terminal escapes |
| unreadable `host_key` or `trusted_ca` | load the material now, the way `DownstreamTLS.BuildDownstreamTLS` does |

`identity` maps certificate fields to `session.Identity`: `subject`, `email`,
`groups`, `attributes`, with `key_id` the default subject.

**Done when** `hoop-inspect -validate` prints each refusal above for a config
that earns it, and `daemon/config_test.go` covers every row.

### H3 — The lane

new `sidecar/daemon/ssh.go`, new `sidecar/daemon/ssh_statement.go`,
`sidecar/daemon/daemon.go`

`buildSSHServer` mirrors `buildGRPCServer`: translate the lane config into
libhoop's `map[string]string` options, and pass an `OpenFunc` that owns
everything policy-shaped. Register it in `daemon.go` at both call sites — the
`-validate` pass and the run pass — beside the `isGRPC` branches.

Per connection, in the `OpenFunc`:

1. Map the verified certificate to `session.Identity` per `identity`.
2. `session.New(inspect.SSH, identity)`, then `gate.NewStatementGate`, the
   same statement-gate shape the gRPC lane uses.
3. Resolve the account: look up the login name libhoop reports — already
   checked against the certificate's principals — and **deny the session**
   when it resolves to no OS user, when its login shell does not exist or is
   not executable, or when this process cannot become it. No default account,
   and no fallback to the sidecar's own user.

Per statement, callbacks enter `gate.EvaluateStatement`, which is where
guardrails, OPA and the analyzer already live. `ssh_statement.go` builds the
statements, one per operation, with the text each operation's rule matches
against (`laneStatements` in `grpc_statement.go` is the model).

A forward gets no statement. The destination check is its own path: libhoop
hands over the requested host, the port and **the address it resolved**, the
lane tests that address against `destinations_allowed`, and libhoop dials the
address that was checked. Checking a name and dialling it again is the window
this shape exists to close.

**Done when** an `exec` is allowed, denied by a guardrail with the rule's
message reaching the client, and recorded; a forward to an allowed
destination connects and one outside `destinations_allowed` is refused.

### H4 — Masking, analyzer, policy

`sidecar/gate/gate.go`, `sidecar/analyzer/content.go`, new `sidecar/policy/ssh.go`

- `MaskSupported` and `substitutionSafe` must answer true for SSH. Both are
  currently keyed to a `Reframer` codec or to HTTP, and SSH has neither: it
  masks a byte stream in place.
- Enforce length preservation where the bytes are rewritten, not only in the
  config check. A masker that returns a different length must fail the stream
  closed — a comment is not enforcement.
- An analyzer content builder for SSH, or `ai_analysis` rules parse and
  classify nothing. `exec_line` is the operation worth sending; a builder that
  renders the others is a bill with no reader.
- `policy/ssh.go` holds the four rule-type refusals as the protocol-aware
  check, next to `policy/http.go` and `policy/grpc.go`.

**Done when** a `pii` rule on `exec_line` fires, a mask rule rewrites shell
output without corrupting a full-screen program, and an `ai_analysis` rule
reaches the model.

### H5 — Audit: events and statements, no content

`sidecar/audit/event.go`, `sidecar/daemon/ssh.go`

**v1 records no session content.** No stream sink, no recorder, no replay —
see ADR-0015 Audit granularity. The existing event and statement kinds carry
everything this phase writes, which is why this phase is small and why H2 has
no "shell is unrecorded" refusal to make.

- `exec`, `env`, `sftp` — statements. `KindStatement` and `KindViolation`
  already carry them, verdict included. An `exec` statement is the command
  line in full; its output is masked in flight and not retained.
- `shell` — events. Open, the geometry `pty` contributed, duration, byte
  counts. No keystrokes and no output.
- `sftp` — operation, path, direction and byte count, unconditionally. The
  file's bytes are never recorded, and **no config key offers them**: a
  setting that records nothing is exactly the silent failure this codebase
  refuses.
- forwards, unrecognized subsystems, refused capabilities — thin metadata: a
  destination or bind address, duration, byte count. Never the relayed bytes.

Masking is independent of all of it: it rewrites bytes in flight, so a masked
shell is masked with no copy of either version kept.

**Done when** a shell session leaves an open/close pair with geometry and byte
counts and no content anywhere in the trail, an `sftp` download records its
path and byte count, and a refused capability leaves exactly one event.

### H6 — Close it out

- `sidecar/cmd`: `-validate` notes for an SSH lane — what it admits, the
  destinations it will carry, the account it will run as.
- `sidecar/README.md`: the `ssh` lane section, and the Protocols table row.
  Every quiet-failure seam in "Adding a protocol is not one package" gets a
  row or an explicit "not applicable".
- Pin the merged libhoop version in `sidecar/go.mod`. The workspace resolves
  one version for every module, so leave `agent/`, `client/` and `gateway/`
  pins alone unless a build needs them.
- `make test-sidecar` green, and `make test-oss` through it.

## Settled

- **No feature flag.** See above.
- **`shell` ships in v1, as events only.** No content trail, so H5 stays
  small and the capability is admitted from H3 onward rather than refused
  until a recorder exists.

## Open — not for this spec to settle

1. **Is an SSH lane licensed?** `sidecar/license` caps rules today. Whether
   the lane itself is a paid capability is a product call, and it changes
   `buildLanes`.
2. **An SSH e2e suite.** `sidecar/e2e` boots a container per protocol and is
   deliberately outside `go.work`. An SSH case needs a client in the image;
   worth it, but not in this deliverable unless asked.

## PR shape

Both PRs are born draft. The hoop PR gets one release label — `minor`: a new
lane is a new code path, no existing behaviour changes, and no wire contract
moves. It must carry a "How to test" section (`/test-plan`) and link ADR-0015.
