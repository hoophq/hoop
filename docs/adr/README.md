# Architecture Decision Records

An ADR captures a structural decision at the point it was made: the
forces at play, the options considered, and why one was picked. It exists
so the next engineer doesn't have to reconstruct the reasoning from a PR
thread or a Slack DM.

Not every change is structural. Most aren't. Use the tests below before
writing one.

## Write an ADR when...

- **Deployment topology:** single region vs. multi, container vs. serverless,
  one deployable vs. several
- **Any dependency that would take weeks to remove** — ORM, framework,
  a vendor whose SDK reaches into your domain code
- **Data model choices that constrain future migrations:** soft deletes,
  event sourcing, denormalization you can't easily reverse, choice of primary key type
- **The decision spans multiple modules and is expensive to reverse.**
  ADR-0004 (protocol-aware MCP gateway) touches `common/proto/`,
  credential issuance, agent dispatch, and gateway routing at once —
  unwinding it later means touching all four again.
- **You chose between real alternatives and the reasoning would otherwise
  be lost.** ADR-0002 (RDP OCR cache) exists because the choice followed
  a profiling exercise across several placements; without the ADR, the
  next person re-runs the same investigation from scratch.
- **The change affects the gateway↔agent wire contract**, per
  "Gateway ↔ Agent Compatibility" in the root `CLAUDE.md` — a new packet
  type, a new `supports_*` capability gate, or anything that changes what
  an old agent must tolerate.
- **The change reorders or extends the transport plugin chain**
  (`review` → `audit` → `dlp` → `accesscontrol` → `webhooks` → `slack`) or
  the gRPC interceptor chain — order there is load-bearing, and a change
  needs a recorded reason.
- **It establishes a pattern others will be told to follow.** ADR-0001
  (tunnel virtual addressing) fixed the TLD, IP family, and allocation
  scheme that every later tunnel feature builds on.
- **Deviating from an already-accepted ADR.**
  That's a new ADR superseding the old one, not a quiet exception.

And if you're genuinely unsure, write it — a 20-minute record that turns out unnecessary
costs almost nothing, while a missing one costs a rediscovery six months out.

## Skip the ADR when...

- **You're adding one more instance of an existing pattern.** A new
  protocol handler under `agent/controller/` (following the existing
  `oracle.go`/`mongodb.go` packet-dispatch shape) or a new domain package
  under `gateway/api/` (following existing Gin route + middleware
  conventions) uses a decision that's already made. The pattern itself
  earned its ADR once, if any; each new file doesn't re-earn one.
- **It's a bug fix or a validation rule getting stricter**, e.g. widening
  a token audience check. That's tightening existing behavior, not
  choosing an architecture.
- **It's a routine schema change** — add a column, add an index — that
  doesn't change the storage approach.
- **The right label is `skip-release`.** Docs, CI config, renames, and
  internal refactors that don't change externally-visible structure are
  almost never ADR material either.

**Judgment call:** a change that looks like "just an implementation
detail" sometimes changes how the *next* person is expected to solve the
same class of problem — that's the signal to write the ADR, even if the
diff itself is small. Conversely, a large diff that mechanically applies
an already-decided pattern doesn't need one just because of its size.

## Worked examples from this repo

| Change | ADR? | Why |
|---|---|---|
| New first-class `mcp` connection type with its own credential and audit path | Yes — [ADR-0004](0004-mcpproxy-connection-type.md) | Replaces an existing byte-level relay with protocol-aware handling; cross-cutting and hard to reverse. |
| Virtual IP/DNS addressing scheme for `hsh tunnel` | Yes — [ADR-0001](0001-tunnel-addressing.md) | Fixes a convention (`.hoop` TLD, IP family, allocation) every later tunnel feature depends on. Later amended in place rather than superseded — see the amendment note at the top of that file. |
| Where the RDP PII guard's OCR cache lives and at what granularity | Yes — [ADR-0002](0002-rdp-pii-guard-line-ocr-cache.md) | Followed a profiling exercise across multiple placements; the measured numbers and rejected options are the reusable part. |
| Adding `agent/controller/oracle.go` alongside the existing `postgres.go`, `mysql.go`, `mssql.go` handlers | No | Same packet-dispatch pattern, new instance. The "one file per protocol, dispatched by packet type" decision doesn't need re-litigating per protocol. |
| Extending token validation to check `client_id` in the audience claim | No | Tightens an existing rule inside the current auth model; no new option was chosen between. |
| A living document like `docs/adr/hoopinspect-flow.md` or `webapp_v2/CONTEXT_MIGRATION.md` | No, and it's not an ADR to begin with | These describe *current-state* architecture or migration progress and get edited as things change. An ADR is a record of *one point-in-time decision* — it doesn't get rewritten when reality moves on; it gets superseded or amended. Don't conflate the two doc types. |

## Process

- **File:** `docs/adr/NNNN-short-slug.md`. Copy `0000-adr-template.md`.
- **Numbering:** use the next free number, read from the directory
  listing — same rule as SQL migrations in the root `CLAUDE.md`. The
  sequence can have gaps (there's no `0003` yet); don't renumber existing
  files to close one.
- **Status lifecycle:** `Proposed` while under discussion → `Accepted`
  once decided → `Superseded by ADR-NNNN` if a later ADR reverses it.
  For a scoped revision that doesn't invalidate the original reasoning,
  amend in place instead: keep the file, add an `> **Amendment
  (date):**` callout at the top, and note it in the `Status` line — see
  ADR-0001 for the pattern.
- **Link it:** reference the ADR number in the PR description that
  implements it, and fill in `Linear`/`Related` in the frontmatter so the
  ticket and the decision stay connected.

## Review workflow

Open the ADR as its own PR, `Status: Proposed`, separate from the
implementation PR — commit message prefix `docs(adr):` (see
`1161c976`, ADR-0002's history). Don't fold the decision into the same
PR that codes it; the ADR needs to be approvable (or objectable-to) on
its own.

1. **Broadcast it.** Post the PR link in the engineering Slack channel
   and @-mention `@engineering-team`. Every ADR, no exceptions for ones
   that feel small — the Slack post is the real review surface, not
   GitHub notifications.
2. **Keep it open 1–3 days**, counted from the Slack post. Pick the
   length by blast radius: 1 day for a single-subsystem call, the full 3
   for anything touching the gateway↔agent wire contract or a
   cross-cutting convention. If you need speed, make sure to say it out loud.
3. **Silence past the minimum is approval.** No blocking comment by the
   deadline → the author merges and flips `Status` to `Accepted`.
4. **A blocking comment pauses the clock**, regardless of how many days
   have already elapsed. Resolve it, then the window (or what's left of
   it) resumes.
