# Sessions seed

Writes every session kind of the Sessions behavior matrix (project "Sessions in
React") into a dev stack. Both UIs, CLJS and React, then show the same rows.
Two runs write the same rows.

## Run

```sh
make seed-dev-sessions
```

It needs Node 20+, `psql`, and a running gateway with `AUTH_METHOD=local` and
an Enterprise license (`POST /api/sidecars/events` is Enterprise only). No Go,
no agent. A second run deletes the seeded sessions and writes them again.

| Variable | Default | |
|---|---|---|
| `SEED_DB_URI` | `postgres://hoopdevuser:1a2b3c4d@127.0.0.1:5449/hoopdevdb?sslmode=disable` | The gateway's database, as the host reaches it. |
| `API_URL` | `http://127.0.0.1:8009` | The gateway. |
| `SEED_LICENSE_FILE` | none | A license JSON, uploaded before the sidecar events. Use it on a new database: `psql "$OTHER_DB" -Atc "SELECT license_data FROM private.orgs WHERE name = 'default'" > license.json`. |
| `PSQL` | `psql` | The psql command, for example `docker exec -i hoopdevpg psql` (then `SEED_DB_URI` is the address inside the container). |
| `SEED_ORG` | `default` | The organization name. |
| `ADMIN_USERNAME`, `AUDITOR_USERNAME` | `admin`, `auditor` | The gateway's admin and auditor group names. |

What it does:

1. Upserts the users below, logs in as `admin@seed.example.com`, and stops
   when the gateway at `API_URL` does not serve the organization of
   `SEED_DB_URI`.
2. Upserts 67 connections, the sidecar `seed-sidecar`, and writes 283
   sessions with their blobs, reviews, review groups, RDP job and detections,
   and masking metrics, by SQL.
3. Turns `experimental.sidecar_session_events` on, posts the events of two
   sidecar sessions to `POST /api/sidecars/events`, then sets the times the
   gateway stamps with `now()` (`metadata.sidecar.last_event_at`, blob and
   stream chunk `created_at`) to the session's own, and the random id of its
   masking metrics to one derived from the row.

Times are fixed (2026-10-01 to 2026-10-03, UTC). Freeze the browser clock and
time zone when you compare recordings: "x ago" and local times depend on them.

## Users

Password for all: `hoop-seed-1234`.

| Role (matrix legend) | Email | Groups | Sees in `/sessions` |
|---|---|---|---|
| admin | `admin@seed.example.com` | `admin` | 285 |
| auditor | `auditor@seed.example.com` | `auditor` | 285 |
| approver | `approver@seed.example.com` | `seed-reviewers`, `seed-force` | 11, the sessions reviewed by `seed-reviewers` |
| owner | `owner@seed.example.com` | none | 271, its own |
| other | `other@seed.example.com` | none | 0 |

The owner owns every seeded session except the machine ones (user
`machine:seed-ci`) and the sidecar ones. The sidecar sessions have an empty
`user_id`, so only admin and auditor see them.

## Check the rows

```sh
SEED_DB_URI=... node scripts/dev/seed-sessions/seed.mjs dump > a.dump
```

`dump` prints `private.sessions`, `private.blobs` (content as md5),
`private.reviews`, `private.review_groups`, `private.session_metrics` and
`private.session_stream_chunks` (without its sequence id), one row per line,
without `org_id`. Seed two new databases, dump both, and `diff` them: it is empty.

`make test-seed-sessions` checks that the ids match the gateway's, that the SQL
does not read the clock, that the first page holds every kind ENG-900 lists
but the batch, and that this README matches the catalog; CI runs it in the
"Webapp v2 Lint and Build" job. With `SEED_DB_URI` and `API_URL` set, it also
seeds that stack twice and compares the dumps. With `SEED_DB_URI_2` and
`API_URL_2` set to a second new stack, it seeds both and compares them.

## Sessions

The first page of `/sessions` (20 rows) holds the kinds ENG-900
lists, newest first, with `seed-sc-full`. The batch is the one exception: it is
dated a day earlier, and `/sessions/filtered?batch_id=seed-batch-1` shows it.
Ids come from `catalog.mjs`; `seed.test.mjs` checks this table against it.

| Key | Id | What |
|---|---|---|
| `connect-live-pty` | `1b2fe28d-2341-544e-b532-ea826528c63d` | Live: bash pty, connect, open. |
| `exec-bash-ok` | `d0cfec08-80e6-5f39-8b22-4f74503b0b93` | bash exec, exit 0. |
| `exec-bash-exit-1` | `73bb6211-9382-5f16-8fa8-eac27f2f3c4c` | bash exec, exit 1, stderr only. |
| `exec-bash-stderr` | `74de75c2-c838-50e6-a823-e183c370b394` | bash exec, exit 0, stderr and stdout. |
| `exec-pg-table` | `93f39cf0-3c0e-5e96-ba6e-73b74e4b8fbe` | postgres exec, a 3-row table. |
| `exec-pg-error` | `c5bd9cbd-771c-59b4-8a6b-846ee54ecf69` | postgres exec, exit 1, an ERROR. |
| `connect-pty` | `50deba84-f130-53ad-806b-e0727c2edf93` | bash pty, ANSI colors, a "€" split across two events. |
| `connect-ssh` | `cbdf555a-0632-513e-aba6-03153b8320d6` | SSH frames: pty 100x30, resize to 140x40, a port forward on channel 2. |
| `connect-pg-wire` | `f287be91-f2cc-53b0-8ce7-b6a526b4274b` | Native postgres, format `wire-proto`: Query, Parse/Bind/Execute/Sync, Query. |
| `connect-http` | `b2dc0e22-e803-5e36-9bc0-a9cd8a78cd71` | httpproxy, two requests. |
| `review-pending` | `c9840697-50f3-5814-b28e-dd180874db34` | PENDING for `seed-reviewers`; force group `seed-force`. |
| `review-approved` | `e5daa85f-f7e9-5b2f-b5d5-774675192ee9` | APPROVED, session `ready`. |
| `review-rejected` | `db618bcf-e97f-56c8-a8a9-d807c97d4035` | REJECTED with a two-line reason. |
| `review-approved-time-window` | `ad6b9383-463c-5dbd-8e4a-ed4d1893a354` | APPROVED with a window, 02:00-04:00 UTC. |
| `review-executed` | `49a97aa2-713b-5bd4-a273-13a4ed98bc34` | Approved, then executed (EXECUTED, done, output). |
| `exec-output-5mb` | `d9a73305-ee1f-5c7d-9d65-71e46bc03eb3` | Output above 4 MB (`event_size` 6,130,400). |
| `exec-input-5mb` | `40c06713-e0f0-5ba0-a84c-8eb34398b0fc` | Input above 4 MB (`script_size` 5,577,786). |
| `exec-guardrail-block` | `a17ed0e8-3ab0-5c24-a62d-8eac6daa9dcb` | A guardrail block: one input deny-words hit. |
| `workflow-short-3` | `975a272a-75ca-511a-b97b-7b26dd350afc` | Workflow `seed-wf-short`, step 3: still running. |
| `workflow-short-2` | `12ee3415-1233-566c-bc7c-f23fbc4922a1` | Step 2: a machine identity, exit 2. |
| `workflow-short-1` | `ee4ddbf0-4fc8-5dce-a094-41531a1d66e7` | Step 1: exit 0. |
| `connect-rdp` | `1b0e6776-ad17-51ee-b0a4-f5de5af26a8e` | RDP: 41 synthetic 16 bpp bitmaps, 800x480, a finished detections job with 3 entities. |
| `connect-live-pg` | `c662b6df-c342-5738-948f-5dbff1cbbc7e` | Live: native postgres, connect, open. |
| `exec-pg-empty` | `05baffd6-6b34-5813-be8f-53244e7474c6` | Success, empty output. |
| `exec-mssql-table` | `64bfdba2-b33b-59c7-84c1-fd6d106b6543` | mssql exec; the first output line is a context message. |
| `exec-long-lines` | `c7f6bef9-c6d3-5405-8531-ff12e3e909d3` | Line 3 is 12 KB; trailing blank lines. |
| `exec-binary-output` | `ecf4c34c-7cde-532a-8b8b-b87ca2614ecb` | Output that is not UTF-8. |
| `exec-output-3mb` | `5513a43f-85ce-5d0f-8084-4107fa2e54c9` | Output of 2.6 MB: above the 2 MB download limit, below 4 MB. |
| `exec-input-and-output-5mb` | `a38b9e57-ba29-558e-8016-49a57b1e30b3` | Input and output above 4 MB. |
| `exec-guardrail-multi` | `ade8a9f2-0c90-52c2-b8d2-ad29f7aefcc3` | Two guardrail hits: input deny-words, output pattern. |
| `exec-runbook` | `081f6ddc-99b4-5d35-bd44-d3e734b61b5d` | Runbook file and parameters labels. |
| `exec-jira` | `a19ee22e-01f2-5585-8507-8278c3d36d26` | Jira issue `SEED-42`. |
| `exec-ai-blocked` | `9c345701-0612-51a5-9af5-544d501c5734` | AI analysis, high risk, `block_execution`, 5 steps (one error). |
| `exec-ai-allowed` | `a3e9fcab-efdc-5ead-9ed1-c0018d7eff58` | AI analysis, low risk, `allow_execution`. |
| `exec-metadata` | `cff6c9f9-f2a3-5f10-9742-664f4ad18d94` | Metadata with a link and two keys the page hides. |
| `exec-data-masking` | `7b53474c-ee9a-527f-b0d2-54e575eda46c` | Masking metrics: 3 info types, 6 items. |
| `review-pending-time-window` | `339c0bcb-80ba-508f-84ed-3bb96b100f06` | PENDING with a window, 22:00-23:30 UTC. |
| `review-two-groups` | `cd20d0ae-cba7-5520-9e7a-ebd9fc1fe251` | Two groups, `min_approvals` 2: one APPROVED, one PENDING. |
| `connect-ssh-no-pty` | `d050fa85-d942-5844-bce1-31f2d9cc016e` | SSH frames without a pty (git upload-pack). |
| `connect-ssh-legacy-pty` | `e6755faf-4a23-5163-8a77-3cf4f1f64be9` | Format `ssh`, but the bytes are a pty recording. |
| `connect-legacy-k8s` | `1471d68a-a239-5742-8b16-7f97772a123b` | No `recording_format`: custom kubernetes, read as a pty. |
| `connect-rdp-analysis-failed` | `d06107ee-b9de-529f-bb66-372aec09e1a6` | RDP; the detections job failed 3 times. |
| `connect-rdp-no-frames` | `be91efce-0352-5b2b-ad83-2b46a138048d` | RDP, 0 bitmaps. |
| `jit-ready` | `6dbc0d4c-9792-5597-8b74-f383f996e0f8` | JIT approved, session `ready`. |
| `jit-revoked` | `a0301ce4-3688-5d67-a2f1-d213485950ec` | JIT, credentials revoked. |
| `jit-expired` | `40317c04-a0da-5052-857e-ee9c774fa2fd` | JIT, credentials expired. |
| `jit-ended` | `aa6c17c0-3a88-5bc2-bcea-a1f0329843b6` | JIT, session done. |
| `credentials-link` | `9fe937f7-6595-574f-bacd-e2b5d720f3ee` | `metadata.credential_session` points to `jit-ended`. |
| `seed-sc-full` | `e6191503-a3bf-569c-a19c-83ec8f38d912` | Sidecar: start, statement, violation, masked, error, end. |
| `seed-sc-simple` | `b3b74113-4edc-564b-8f47-9fd73c92ed5f` | Sidecar: start, statement, end. |

Also: `batch-01` to `batch-25` (batch `seed-batch-1`, 2026-10-02;
`batch-01` is `aeee3ded-6e3a-5e17-822c-c4295f915740`), and `workflow-001` to
`workflow-210` (workflow `seed-workflow-210-steps`, 2026-10-01; 3 still
running, 13 failed, 11 by a machine).

## Matrix rows marked SEED

As admin unless the row names a user. A key stands for its session's page,
`/sessions/<id>`.

| Row | Shown by |
|---|---|
| L01 | `/sessions` |
| L02 | Any row, for example `exec-bash-ok` |
| L03 | `connect-live-pty`, `connect-live-pg` |
| L04 | `review-approved` (Approved), `review-pending` (Pending), `review-rejected` (Rejected), `review-executed` (no badge) |
| L05 | `workflow-short-1` (`seed-wf-short`), `workflow-001` (`seed-workflow-210-steps`, truncated) |
| L06 | Any row |
| L07 | 285 sessions: "Load more" past 100 |
| L08 | `/sessions?jira_issue_key=NONE-1` |
| L09 | Any row, with a network delay |
| L10 | `/sessions?status=open&verb=connect`: 2 live sessions |
| L11 | ID search with the id of `exec-bash-ok` |
| L12 | L11, then `/sessions/filtered?batch_id=seed-batch-1`, then back to `/sessions` |
| L14 | Each user above; counts in the Users table |
| F01 | `/sessions` |
| F02 | ID search: the ids of `exec-bash-ok` and `exec-pg-table` |
| F03 | ID search: the ids of `exec-bash-ok`, `exec-pg-table` and `00000000-0000-0000-0000-000000000000` |
| F04 | Query text `customers` (inputs of `seed-pg` sessions) |
| F07 | Query text `missing_table` (`exec-pg-error`); `missing%table` |
| F08 | Live toggle: `connect-live-pty`, `connect-live-pg` |
| F09 | Users popover: the 5 seed users; `owner@seed.example.com` |
| F10 | Resource Role popover: 67 connections, 2 pages of 50 |
| F11 | Type: Custom (`seed-bash`, `seed-rdp`, `seed-k8s`), Database (`seed-pg`, `seed-mssql`, sidecar), Application (`seed-ssh`) |
| F12 | `review.status` PENDING, APPROVED, REJECTED |
| F13 | 2026-10-01 (workflow), 2026-10-02 (batch), 2026-10-03 (kinds) |
| F14 | Jira Ticket ID `SEED-42` (`exec-jira`) |
| B01 | `/sessions/filtered?batch_id=seed-batch-1` |
| B02 | Same: 25 rows, pages of 20 |
| B03 | `/sessions/filtered?id=<exec-bash-ok>,<exec-pg-table>` |
| B06 | B01, search `mssql` |
| D01 | `exec-bash-ok` |
| D02 | `exec-bash-ok` (base64), `connect-live-pty` (raw, open), `connect-pg-wire` (raw-queries), `connect-http` (raw) |
| D03 | `exec-output-5mb` |
| D04 | `exec-input-5mb` |
| D05 | `exec-input-and-output-5mb` |
| D08 | `exec-binary-output`. The webapp asks `event_stream=base64`, which encodes; the error text shows only with `event_stream=utf8`. |
| D09 | approver on `review-executed`: script, no `event_stream` |
| H02 | `workflow-short-1` |
| H04 | `exec-bash-ok` (Re-run); `exec-jira` (no Re-run) |
| FD01 | `exec-pg-table` (resource `seed-postgres`, role `seed-pg`) |
| FD03 | `review-approved` |
| FD04 | `review-two-groups`, `review-approved` |
| FD05 | `exec-bash-ok` (Success), `exec-guardrail-block` (Blocked), `review-approved` (Ready), `review-pending` (`open`). The status enum has no `error`. |
| FD06 | `exec-bash-ok`; `review-pending` (no Finished at) |
| FD07 | `exec-runbook` |
| FD08 | `exec-jira` |
| FD09 | `batch-01` |
| FD10 | `exec-ai-blocked` (ACTION BLOCKED), `exec-ai-allowed` (ACTION ALLOWED) |
| FD11 | `credentials-link` |
| FD12 | `exec-runbook` |
| FD13 | `exec-metadata` |
| FD15 | `exec-bash-ok` |
| FD16 | `exec-input-5mb` |
| FD17 | `review-rejected` |
| I02 | `exec-ai-blocked` (HIGH RISK), `exec-ai-allowed` (LOW RISK) |
| I03 | `exec-ai-blocked` |
| I04 | `exec-guardrail-block` |
| I05 | `exec-guardrail-multi` |
| I06 | `exec-guardrail-multi` (DENY WORD, PATTERN MATCH), `seed-sc-full` (SIDECAR) |
| I07 | `exec-guardrail-block` (input, deny words), `exec-guardrail-multi` (output, pattern) |
| I11 | `exec-data-masking` (report items), `seed-sc-full` (`metrics.data_masking`) |
| I13 | `connect-rdp-analysis-failed` (gave up after 3 attempts, `last_error`) |
| R01 | approver on `review-pending` |
| R04 | approver on `review-pending` |
| R05 | approver on `review-pending` (`seed-force` is a force group of `seed-pg` and of the review) |
| R07 | approver on `review-pending-time-window` |
| R08 | `review-pending` (pending), `review-approved` (ready), `jit-expired` (`credentials_expire_at`) |
| R09 | `review-two-groups` |
| A12 | owner on `jit-revoked` |
| A13 | owner on `jit-expired` |
| A15 | owner on `jit-ended` |
| O03 | `exec-pg-table`, Table tab |
| O04 | `exec-bash-ok` |
| O05 | `exec-mssql-table` |
| O06 | `exec-long-lines` |
| O07 | `exec-output-5mb` |
| O08 | `exec-pg-empty` |
| O09 | `connect-legacy-k8s` (pty from the type), `connect-rdp` (rdp), `connect-ssh` (ssh), `connect-http` (raw) |
| O10 | `connect-http` |
| O12 | `credentials-link` (no stream) |
| O13 | `connect-pty` |
| O14 | `connect-ssh-legacy-pty` |
| P01 | `connect-rdp` |
| DL01 | `exec-bash-ok` (menu), `exec-pg-empty` (no menu) |
| DL03 | `exec-pg-table` (TXT, CSV, JSON), `exec-bash-ok` (TXT) |
| DL04 | `exec-pg-table` |
| DL05 | `exec-output-3mb`, `exec-output-5mb` |
| W01 | `/workflows/seed-workflow-210-steps`, `/workflows/seed-wf-short` |
| W07 | `/workflows/seed-wf-short`: 1 error, 1 in flight |
| W08 | `/workflows/seed-wf-short`: Success, Failed, Running |
| W10 | `/workflows/seed-workflow-210-steps` |
| W13 | `/workflows/seed-wf-short` (step 2 has the machine badge) |

The seed also shows these SC rows: FD02 and FD14 (`seed-sc-full`: listener
`appdb`, sidecar `seed-sidecar`), O11 (`seed-sc-full`: the denied statement at
2 s).
