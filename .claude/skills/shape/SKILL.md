---
name: shape
description: Turn a dictated idea or a Linear project into ready-to-run tickets - investigate the code, cut vertically, size by risk, write them into Linear. Use before /build-ticket, when the work is not a ticket yet.
---

# Shape work into tickets

Input: a dictated idea, a rough note, or a Linear project ID. Output: tickets in
Linear that an agent can run without asking anything.

Write every Linear field in English, whatever the conversation language.

## 1. Understand

Restate the goal in two lines and say who it is for. If a Linear project was
given, read it and its existing issues first, so you extend rather than
duplicate.

## 2. Investigate the code

Do this before cutting anything. Use subagents in parallel for breadth. Answer:

- Which modules: `gateway/`, `agent/`, `client/`, `common/`, `tunnel/`,
  `sidecar/`, `agentrs/`, `webapp_v2/`. The legacy `webapp/` is frozen.
- Does it touch the gateway↔agent contract (`common/proto/transport.proto`,
  packet types, spec keys, payload formats)? That decides `major`.
- Does it touch the sidecar config the control plane serves?
- Migration needed? Read `gateway/migrations/` for the next free number.
- New env var? The helm chart and `.env.sample` change in the same PR.
- New or superseded analytics event?
- Does it need an ADR in `hoophq/adr`? Only for a structural change that spans
  modules, is expensive to reverse, changes the wire contract, or picks between
  real alternatives.

## 3. Cut vertically

Each ticket must be one complete flow, thin, testable end to end on its own.

```
GOOD  PR1: the whole flow for one connection type, behind a flag
      PR2: widen it to the rest
BAD   PR1: migration   PR2: model   PR3: API   PR4: UI
```

A horizontal cut leaves nothing testable until the last ticket. If a slice
cannot stand alone, put it behind a feature flag rather than splitting it by
layer.

## 4. Size by risk, not by lines

Risk = how much existing behaviour the change touches, times how hard it is to
reverse.

- **Its own small ticket**: auth, `transport.proto` and packet types, migrations,
  middleware order, anything a released agent reads.
- **May be large**: new isolated code behind a flag, or a mechanical refactor
  with tests. 3000 lines of new package is safer than 50 lines in
  `AuthMiddleware`.

## 5. When the direction is unclear, write a `spike` first

A spike builds the whole thing on a throwaway branch to learn, and delivers
evidence, not a PR. Once the human approves the direction, shape the prototype
into vertical tickets, using it as the specification.

Code is cheap now. Build big to learn, merge small to ship.

## 6. Write the tickets

One ticket per slice, in this template, with `blocked by` between them:

```markdown
## Type
bug | feature | spike

## Goal
Why, and for whom. 1-3 lines.

## Definition of done
- [ ] Verifiable item (QA checks each one)

## Scope / Out of scope

## Context
Files, past PRs, ADRs, customer threads.

## Constraints
- Feature flag: yes/no, default true/false
- Old agents must keep working: yes/no
- Migration / API change / analytics event: yes/no

## Evidence expected
Screenshot of X, VHS of CLI flow Y.

## Open questions
Empty, or the agent investigates first.

## Checkpoint
none | approve design before code
```

Rules for filling it:

- **Definition of done** is the contract QA checks. Every item must be
  verifiable by someone who did not write the code. No "works well".
- **Feature flag**: answer it here so the agent never asks. A feature that only
  adds a new code path is born `Default: true`; one that changes existing
  behaviour is born `false`.
- **Checkpoint**: `none` for a bug. `approve design before code` for anything
  that picks between real alternatives.
- Reference a ticket by ID, never by URL.
- Fewest possible words. Simplified Technical English.

## 7. Hand back

Report, in the chat: the ticket IDs with one line each, the order and
dependencies, and anything you could not decide. Then say the command to run
them, one worktree per ticket:

```
claude --worktree <ticket-id>    # then /build-ticket <TICKET-ID>
```
