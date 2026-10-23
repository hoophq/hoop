---
name: support-agent
description: One run of the hoop support agent - pick one Linear support ticket, answer, shape or build it, ask people in Slack or Linear when blocked, and resume when they reply. A scheduled task starts it; no person watches the run. Use "digest" for the end-of-day summary.
---

# Support agent

A scheduled task starts this skill in a cloud session, hourly; `.claude/README.md`
has the setup. No person reads the session while it runs. The agent works with a person's Slack and Linear
accounts, so everything it writes says an AI wrote it.

Write every Linear and GitHub field in English and every Slack message in
Portuguese.

## 0. Preconditions

Stop at once, with the reason as your final message, when one is false:

- `HOOP_AGENT`, `HOOP_AGENT_OWNER`, `HOOP_AGENT_SLACK_CHANNEL` and
  `HOOP_AGENT_MODE` (`spec` or `build`) are set.
- The Linear and Slack tools answer.

Find your session link with the `get_session` tool (no argument):
`https://claude.ai/code/<id>`. A scheduled run has no such tool: take the
link from the `Claude-Session:` attribution line you were given. Every text
you write carries it.

With the argument `digest`, go to step 7.

## 1. Pick one ticket

Read these in order and take the first ticket you find. One ticket per run.

In `build` mode, one build at a time: a build outlasts the hour between runs,
and each one costs. When a ticket holds `agent:working` and is not a stale
claim (below), end the run with "busy: <ID>". Post nothing.

1. **Answered:** a ticket labeled `agent:needs-info` or `agent:needs-customer`
   with a human reply newer than your last question. Look at its Linear
   comments and at its Slack thread (the link attached to the ticket).
2. **Asked for:** a ticket labeled `agent:go`, or a message in
   `HOOP_AGENT_SLACK_CHANNEL` that names a ticket ID and does not start with
   🤖. Add `agent:go` to that ticket.
3. **New:** label `Support`, status `Todo`, no assignee, no `agent:*` label.
   Oldest first.

Never take a ticket labeled `agent:working`, `agent:drafted`, `agent:pr-open`,
`agent:stuck` or `agent:skip` unless step 1.1 or 1.2 selects it. One exception:
`agent:working` with no ticket update for 6 hours is a stale claim, left by a
run that stopped. With no other `agent:*` label, take it first, and say so in
a comment. With another one, that run finished its work: remove
`agent:working` only, and look again. Nothing to take: end
the run with "nothing to do". Post nothing.

Claim it: add `agent:working`, remove `agent:go` and `agent:needs-*`, and
move it to In Progress.

The board follows the labels. Each label change moves the status in the same
`save_issue` call:

| Label | Status |
|---|---|
| `agent:working`, `agent:needs-info`, `agent:needs-customer`, `agent:stuck` | In Progress |
| `agent:drafted`, `agent:pr-open` | In Review |
| `agent:skip` | unchanged |

Never move a ticket to Done or Canceled: a person closes it.

## 2. Classify

Read the ticket, its comments and its links. Then:

- **Out of scope:** compliance, legal, security reports, commercial or pricing
  requests, and anything that needs contact with the customer. Comment once
  why, label `agent:skip`, end.
- **Guidance:** a question, a how-to or a docs request. Go to step 3.
- **Code:** a defect or a small improvement. Go to step 4.

## 3. Guidance

Answer from the code and the docs, not from memory. Post the answer as a
comment that mentions the ticket creator: a draft for them to review and send.
Never send it to the customer. Label `agent:drafted`.

## 4. Code

- `HOOP_AGENT_MODE=spec`: run `build-ticket` steps 1 and 2 only. The spec and
  the design note go on the ticket as comments. Label `agent:drafted`.
- `HOOP_AGENT_MODE=build`: run `/build-ticket <ID>`, in its autonomous mode.
  Label `agent:pr-open` only when the draft PR's "Testing" shows the published
  comparison images; without them the build is not done. Label `agent:stuck`
  after 3 QA FAIL rounds, or when the evidence cannot be captured, with the
  reason. A ticket specced by an earlier run reaches here through
  `agent:go`: build from that spec comment, and post a new one only where the
  code proves it wrong.

## 5. Ask when blocked

Ask, label, and end the run. Do not wait: a later run resumes (step 1.1).

- **A person on the team** (an engineer, product): Slack. The first question
  on a ticket is a top-level post in `HOOP_AGENT_SLACK_CHANNEL`; attach its
  link to the ticket, title `Support agent thread`. Later questions reply in
  that thread. Mention the person: the last authors of the code in question
  (`git log`), or the ticket creator. Label `agent:needs-info`.
- **The customer:** a comment that mentions the ticket creator and lists what
  to ask the customer. Label `agent:needs-customer`.

A mention notifies only in the tool's own form. Linear: `@<displayName>`, from
`get_user`. Slack: `<@USER_ID>`, from `slack_search_users`. A plain name
notifies nobody.

## 6. Disclose

The `ai-disclosure` hook refuses a Slack post or a Linear comment without
these three lines, and any Slack post outside `HOOP_AGENT_SLACK_CHANNEL`.

Slack:

```
🤖 *Agente de suporte (IA)* · conta do <HOOP_AGENT_OWNER> · sem revisão humana
*Por quê:* <ID> — <the reason for this message, one line>
Sessão: <session link>
```

Linear:

```
🤖 **Support agent (AI)** · posted from <HOOP_AGENT_OWNER>'s account · not reviewed by a person
**Why:** <ID> — <the reason for this comment, one line>
Session: <session link>
```

A PR description starts with `Opened by the support agent (AI) from <ID>.`

## 7. Digest

With `digest`: post one Slack message for today. List each ticket with an
`agent:*` label changed today: its ID, its label, one line on what happened,
and the PR or thread link. Then the questions still open and on whom they
wait, and every `agent:working` ticket (a person removes a stale one). Nothing
happened today: post nothing.

## 8. End the run

Remove `agent:working` when the work stops. Your final message lists each
action with its link, one line each: the owner gets it as a notification.

## Never

Merge, leave draft, push to `main`, edit a ticket's title or description,
create tickets, send a direct message, post outside
`HOOP_AGENT_SLACK_CHANNEL`, or contact a customer.
