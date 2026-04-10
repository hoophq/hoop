# Feature: Streaming API endpoint for sessions

## Problem Statement

Currently, hoop is very focused on human usage. We're adding Non-Human Identities (NHI) support to hoop. It means that other machines will use hoop to interact with the resources (a.k.a. connections).
With that we need long-lived sessions instead of short-lived ones. The current session model is not suitable for this use case because when a command is run, the session goes to a closed state.

What is the problem with long-lived sessions? We need to find a way to stream the input/output of the commands to the user. 
Currently, the session only has one input and one output. We need to change it so a session can have multiple inputs and outputs. Also, the session should be able to stay open even after the command is finished.

## Proposed Solution

An endpoint that will return the list of the session's input and output.

## Current DB Schema

All tables live in the `private` schema.

### `private.sessions`

Each row represents one command execution. A session references two blobs: one for input and one for the output stream.

| Column                                 | Type                   | Notes                                                                                                 |
|----------------------------------------|------------------------|-------------------------------------------------------------------------------------------------------|
| `id`                                   | `uuid` (PK)            | Auto-generated (`uuid_generate_v4()`)                                                                 |
| `org_id`                               | `uuid` (FK → `orgs`)   | NOT NULL                                                                                              |
| `connection`                           | `varchar(128)`         | Connection name (e.g. `postgres-demo`)                                                                |
| `connection_type`                      | `enum_connection_type` | `command-line`, `postgres`, `mysql`, `mssql`, `tcp`, `application`, `custom`, `database`, `httpproxy` |
| `verb`                                 | `enum_session_verb`    | `connect` or `exec`                                                                                   |
| `status`                               | `enum_session_status`  | `open` → `ready` → `done`                                                                             |
| `blob_input_id`                        | `uuid`                 | FK to `blobs` — the command input                                                                     |
| `blob_stream_id`                       | `uuid`                 | FK to `blobs` — the execution output stream                                                           |
| `user_id` / `user_name` / `user_email` | `varchar`              | Who ran the session                                                                                   |
| `labels`                               | `jsonb`                | Arbitrary key-value labels                                                                            |
| `metadata`                             | `jsonb`                | Session metadata                                                                                      |
| `metrics`                              | `jsonb`                | Performance metrics                                                                                   |
| `created_at`                           | `timestamp`            | Session start                                                                                         |
| `ended_at`                             | `timestamp`            | Session end                                                                                           |
| `exit_code`                            | `smallint`             | Process exit code                                                                                     |
| `session_batch_id`                     | `varchar(255)`         | Groups related sessions                                                                               |
| `connection_subtype`                   | `varchar(64)`          |                                                                                                       |
| `connection_tags`                      | `jsonb`                |                                                                                                       |
| `jira_issue`                           | `text`                 |                                                                                                       |
| `integrations_metadata`                | `jsonb`                |                                                                                                       |
| `ai_analysis`                          | `jsonb`                |                                                                                                       |
| `guardrails_info`                      | `jsonb`                |                                                                                                       |

Key indexes: PK on `id`, unique on `(org_id, id)`, index on `(org_id, session_batch_id)`.

### `private.blobs`

Stores the actual input and output data for sessions. Each session produces exactly two blobs.

| Column        | Type                 | Notes                                                    |
|---------------|----------------------|----------------------------------------------------------|
| `id`          | `uuid`               | Auto-generated (`uuid_generate_v4()`)                    |
| `org_id`      | `uuid` (FK → `orgs`) | NOT NULL                                                 |
| `type`        | `enum_blob_type`     | `session-input`, `session-stream`, or `review-input`     |
| `blob_stream` | `jsonb`              | NOT NULL — the actual payload (see below)                |
| `format`      | `varchar(40)`        | e.g. `wire-proto` for stream blobs, NULL for input blobs |
| `created_at`  | `timestamp`          |                                                          |

Unique index on `(org_id, id)`.

### Blob payload structure

**`session-input`** (`type = 'session-input'`, `format = NULL`):

A JSON array with a single string element — the raw command text.

```json
["SELECT * FROM customers"]
```

**`session-stream`** (`type = 'session-stream'`, `format = 'wire-proto'`):

A JSON array of tuples: `[elapsed_seconds, stream_type, base64_content]`.

- `stream_type` values: `"i"` = input echo, `"o"` = output, `"e"` = error.
- `elapsed_seconds` is a float relative to session start.
- `base64_content` is the raw bytes base64-encoded.

```json
[
  [0.175, "i", "U0VMRUNUICogRlJPTSBjdXN0b21lcnM="],
  [1.392, "o", "Y3VzdG9tZXJpZAlmaXJzdG5hbWUJ..."],
  [1.449, "e", "ZmFpbGVkIGV4ZWN1dGluZyBjb21tYW5k..."]
]
```

### Current limitations

- A session has exactly **one input** (`blob_input_id`) and **one output stream** (`blob_stream_id`).
- Once the command finishes, the session transitions to `done` — there is no mechanism to keep it open for further commands.
- The `blob_stream` JSONB is written as a single atomic value when the session closes; there is no way to read partial results while the session is still running.
- There is no distinction between human and machine sessions — all sessions are implicitly human today.

## Scope

### In Scope

- New `session_interactions` table to store multiple input/output pairs per session.
- New `type` column on `sessions` to distinguish `human` vs `machine` sessions.
- New API endpoint to stream (list) session events for a given session.
- DB schema migration.

### Out of Scope

- Frontend changes.
- NHI authentication and identity management (assumed to exist already when this feature is used).
- Changes to the existing short-lived (human) session flow.

## Technical Design

### Affected Modules

- **Database**: new migration for `session_interactions` table and `type` column on `sessions`.
- **Gateway API**: new endpoint for listing/streaming session events.
- **Gateway transport layer**: write events to `session_interactions` instead of (or in addition to) the single blob pair for machine sessions.

### Data Model Changes

#### 1. New column on `private.sessions`

| Column | Type                | Notes                                                                     |
|--------|---------------------|---------------------------------------------------------------------------|
| `type` | `enum_session_type` | `human` (default) or `machine`. Set at creation based on caller identity. |

All existing sessions are `human`. The default ensures backward compatibility — no backfill needed.

#### 2. New table: `private.session_interactions`

Stores individual command executions within a long-lived session. Each event has its own input/output blob pair, just like a short-lived session does today at the session level.

| Column           | Type                     | Notes                                            |
|------------------|--------------------------|--------------------------------------------------|
| `id`             | `uuid` (PK)              | Auto-generated                                   |
| `session_id`     | `uuid` (FK → `sessions`) | NOT NULL                                         |
| `org_id`         | `uuid` (FK → `orgs`)     | NOT NULL                                         |
| `sequence`       | `integer`                | 1-based ordering within the session              |
| `blob_input_id`  | `uuid`                   | FK to `blobs` — the command input for this event |
| `blob_stream_id` | `uuid`                   | FK to `blobs` — the output stream for this event |
| `exit_code`      | `smallint`               | Process exit code for this event                 |
| `created_at`     | `timestamp`              | When this event started                          |
| `ended_at`       | `timestamp`              | When this event finished                         |

Indexes: PK on `id`, unique on `(session_id, sequence)`, index on `(session_id, created_at)`.

The `sequence` column enables efficient streaming: clients request events with `sequence > N` to get only new results.

### API Changes

#### `GET /api/sessions/:id/events`

Returns the list of events for a session, with support for pagination/streaming via the `sequence` parameter.

**Query parameters:**

| Param            | Type      | Description                                                    |
|------------------|-----------|----------------------------------------------------------------|
| `after_sequence` | `integer` | Only return events with `sequence > N` (for polling/streaming) |
| `limit`          | `integer` | Max events to return (default: 50)                             |

**Response:**

```json
{
  "events": [
    {
      "id": "uuid",
      "sequence": 1,
      "input": "SELECT * FROM customers",
      "output": [
        [0.175, "i", "...base64..."],
        [1.392, "o", "...base64..."]
      ],
      "exit_code": 0,
      "created_at": "2026-04-10T20:15:34Z",
      "ended_at": "2026-04-10T20:15:35Z"
    }
  ],
  "has_more": false
}
```

**Auth/roles:** Same auth middleware as existing session endpoints. User must have access to the session's connection.

### Protocol Changes

None expected. The gRPC transport layer already handles the packet flow for command execution. The change is in how the gateway persists results — writing to `session_interactions` + blobs instead of updating the session's single blob pair.

### Configuration

No new configuration is needed.

## Behavior

### Happy Path (machine session)

1. A machine client authenticates and creates a session. The gateway sets `type = 'machine'` and `status = 'open'`.
2. The client sends a command over the existing transport.
3. The gateway executes the command, writes the input blob and stream blob to `blobs`, and inserts a row into `session_interactions` with `sequence = 1`.
4. The session remains in `open` status.
5. The client sends another command — a new event is created with `sequence = 2`.
6. Between commands, another client (or the same one) calls `GET /api/sessions/:id/events` to retrieve results.
7. When the machine client is done, it explicitly closes the session → status becomes `done`.

### Happy Path (human session — unchanged)

1. Human creates a session via webapp/CLI. Gateway sets `type = 'human'` (default).
2. Command executes, blobs are written to `blob_input_id` / `blob_stream_id` on the session as today.
3. Session transitions to `done`. No `session_interactions` rows are created.

### Edge Cases & Error Handling

- **Polling with no new events**: `GET /api/sessions/:id/events?after_sequence=5` returns an empty `events` array and `has_more: false`.
- **Session closed while command is running**: The in-flight command completes, its event is persisted, then the session transitions to `done`.
- **Session timeout**: Long-lived sessions that receive no new commands for a configurable period should be automatically closed. (Timeout value TBD.)
- **Querying events on a human session**: Returns an empty list (no events are written for human sessions). This is not an error.

## Security Considerations

- The events endpoint must enforce the same authorization as the parent session — users can only see events for sessions on connections they have access to.
- Blob content may contain sensitive data (query results, credentials in output). The same DLP/redaction rules that apply to session blobs today should apply to event blobs.
- Machine sessions that stay open indefinitely are a resource concern. A server-side TTL or max-events limit should be considered.

## Migration & Rollback

- **Migration**: Add `enum_session_type` enum, add `type` column with default `'human'` (no backfill needed), create `session_interactions` table. This is additive — no existing data is modified.
- **Rollback**: Drop `session_interactions` table, drop `type` column, drop `enum_session_type` enum. No data loss for existing sessions since human sessions don't use the new table.

## Open Questions

- [ ] What should the session timeout be for idle machine sessions?
- [ ] Should there be a max number of events per session?
- [ ] Should the events endpoint support SSE (Server-Sent Events) for real-time streaming, or is polling with `after_sequence` sufficient for v1?
- [ ] Should human sessions also write to `session_interactions` for consistency, or keep the current blob-on-session pattern?