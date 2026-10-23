# ENG-856 · Postgres guardrail blocks are saved to the session

PR #2043 · tested on `bcd18a4` against `origin/main` `8bf2b9c` · 2026-10-23

**Result: a guardrail block on a native Postgres connection now shows in the session record, once per block. On main the record stays empty.**

## What changes, and why it is risky
The gateway now saves the guardrails info of a Postgres packet that holds an ErrorResponse. It runs on the agent receive loop, so a bug would cost every Postgres result: it decodes only packets that carry guardrails info.

## 1. Simple and extended protocol: one entry per block
![1-block-saved](1-block-saved.gif)

What to look at:
- Top of both panels: the client gets the same block error on both sides.
- Bottom: `violations` is 0 on main and 2 on the branch, one per blocked DROP.

## 2. A pipelined batch: SELECT, then a blocked DROP, one Sync
![2-pipelined-batch](2-pipelined-batch.gif)

What to look at:
- Bottom: `violations` is 0 on main and 1 on the branch. libhoop sends that block in one packet with the SELECT rows.
- The `can't scan` errors from the client are the same on both sides. They are a separate libhoop defect (see Open items). The table is not dropped on either side.

## What QA caught on the way
- Round 1: a block in one packet after other rows was not saved. Fixed; `TestHasPGErrorResponse` and the combined-packet case in `TestListenAgentMessagesPersistsPGGuardRailsInfo` guard it.
- Round 2: the check decoded every Postgres result (~30 µs per 200-row packet). Fixed: 10 ns, 0 allocs when the packet has no guardrails info.

## What it solves
Admins and auditors see Postgres guardrail blocks in the session's guardrails info, as they do for MSSQL.

## Open items
- Pre-existing, same on main: in a pgx pipelined batch the client gets `can't scan into dest[0]` and not the guardrail error. The libhoop redactor seems to corrupt the held-back DataRow. Needs its own ticket.
- Pre-existing: saved entries have an empty `rule_name`; libhoop does not set it.
- `TestMySQL_ParallelSessions` failed once on `d20ea3f` (same tree as `5105391`, where it passed). It is green on `bcd18a4`.
