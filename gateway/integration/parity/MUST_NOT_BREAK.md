# Must not break

The daily parity suite (`make test-parity`, workflow `parity-daily.yml`, ENG-529)
checks each row below against the real `hoop` binary over HTTP. The Friday gate
is green when every `must` passes and no row is pending.

- `must`: a failure fails the run.
- `pending ENG-xxx`: not delivered yet; reported, does not fail the run. When it
  passes, the report says `pass-pending`: drop the marker in the check.
- `—`: does not apply to that run.

Runs:

- **gateway**: `hoop start gateway`, with a real agent.
- **control-plane-alias**: `hoop start control-plane`, the command existing
  control-plane installs still run. It boots the same gateway, with no agent.

Every run holds an Enterprise license signed by a key the harness generates; the
binary under test is built to trust that key (`buildHoop` in `parity_test.go`).
Slack is a fake Slack server reached through `SLACK_API_URL`.

Each ID here must match one check in this package, with the same title and run
cells: `TestTheListMatchesTheChecks` enforces it.

## Platform

| ID | What must not break | gateway | control-plane-alias |
|---|---|---|---|
| PLT-01 | serverinfo reports the gateway mode and a valid Enterprise license | must | must |
| PLT-02 | local login issues a token for the registered admin | must | must |
| PLT-03 | an unauthenticated call to a protected route is refused | must | must |

## Gateway: agent traffic, connections, reviews, rules

| ID | What must not break | gateway | control-plane-alias |
|---|---|---|---|
| GW-01 | an ad-hoc exec on an agent connection returns its output and records a finished session | must | — |
| GW-02 | connection CRUD on an agent connection: create, read, replace, conflict, validation, delete | must | — |
| GW-03 | connection lists show ordinary connections and leave sidecar mirrors out | must | — |
| GW-04 | a connection with reviewers holds an exec until approved, then runs it once | must | — |
| GW-05 | a rejected review leaves its exec unexecutable and cannot be approved afterwards | must | — |
| GW-06 | a command access request rule holds an agent exec until approved, then runs it | must | — |
| GW-07 | access request rule CRUD with the gateway's validation: targets, reviewers, one rule per connection and type | must | — |
| GW-08 | guardrail rule CRUD, with no sidecar fields on an ordinary rule | must | — |
| GW-09 | a guardrail bound to an agent connection blocks a matching exec and records the violation | must | — |
| GW-10 | a data masking rule is refused with 422 when no DLP provider is configured, and nothing is stored | must | — |
| GW-11 | a guardrail carrying a sidecar_spec is stored and read back on the gateway | must | — |

## Sidecar setup, configuration and import

| ID | What must not break | gateway | control-plane-alias |
|---|---|---|---|
| SC-01 | an admin creates a sidecar, gets its token once, and lists and reads it by name and id | must | must |
| SC-02 | the handshake serves the configuration with its revision, the license-managed header and the license, and records the check-in | must | must |
| SC-03 | a sidecar with no listeners assigned gets 412 on the handshake and is not recorded as seen | must | must |
| SC-04 | GET /sidecars/configuration serves the handshake's document and records nothing | must | must |
| SC-05 | PUT and PATCH of a sidecar configuration change the served revision; PATCH merges into the stored document | must | must |
| SC-06 | PATCH load_from_disk hands the document to the sidecar's file and back | must | must |
| SC-07 | a sidecar imports its config file once; the plane adopts it, splits the embedded rules into rule items and serves them back | must | must |
| SC-08 | every sidecar route refuses a missing or unknown token with 401 | must | must |
| SC-09 | deleting a sidecar revokes its token at once | must | must |
| SC-10 | with experimental.sidecar_session_events on, the handshake offers session events and POST /sidecars/events records a session idempotently | must | must |
| SC-11 | each listener is a <sidecar>-<listener> connection with every access mode disabled, owned by the sidecar and left out of lists | must | must |
| SC-13 | a sidecar's mirror connections read online once it checks in | must | must |

## License

| ID | What must not break | gateway | control-plane-alias |
|---|---|---|---|
| LIC-01 | sidecar admin and daemon routes answer 403 while the organization holds no Enterprise license, and 2xx again once it does | must | must |
| LIC-02 | the served sidecar configuration carries the organization's license, and a per-sidecar license is refused | must | must |

## Sidecar reviews: hold, approve, retry

| ID | What must not break | gateway | control-plane-alias |
|---|---|---|---|
| RV-01 | the approval rule a listener holds statements under is served to the sidecar on handshake | must | must |
| RV-02 | a held statement files a PENDING review that names the sidecar, the listener and the rule's policy | must | must |
| RV-03 | resending a held statement answers the review already filed for it; another statement files its own | must | must |
| RV-04 | an approved review releases its statement exactly once on resend | must | must |
| RV-05 | a waiting sidecar claims its review by id: pending waits, approved releases once, reads never consume | must | must |
| RV-06 | a rejected review never releases its statement and keeps the reviewer's reason | must | must |
| RV-07 | revoking an approval before the sidecar claims it withholds the statement; a resend files a new review | must | must |
| RV-08 | a sidecar lists its reviews newest first, filtered by status and bounded by limit | must | must |
| RV-09 | a sidecar token reaches only the reviews that sidecar filed and the rules its own listeners name | must | must |
| RV-10 | a review request the served config does not authorize, or that is malformed, files nothing | must | must |
| RV-11 | sidecar review routes refuse a caller without a valid sidecar token | must | must |
| RV-12 | a review settles by the policy of its rule: all groups, a minimum, or a force approval | must | must |

## Sidecar rules: guardrails, masking, analyzer

| ID | What must not break | gateway | control-plane-alias |
|---|---|---|---|
| RUL-01 | a guardrail bound to one listener is served on that listener only | must | must |
| RUL-02 | a data masking rule with a sidecar spec saves without a DLP provider and is served on its listener | must | must |
| RUL-03 | an analyzer rule bound to a listener serves its trigger, verdict actions and prompt there | must | must |
| RUL-04 | an analyzer rule that holds statements creates its approval rule, and reviews follow its binding | must | must |
| RUL-05 | unbinding a rule with sidecar_targets [] removes it from the served config and moves the revision | must | must |
| RUL-06 | an edit that omits sidecar_spec and sidecar_targets keeps the rule bound and served | must | must |
| RUL-07 | a binding the sidecar could not run is refused at save and stores nothing | must | must |

## Slack approval

| ID | What must not break | gateway | control-plane-alias |
|---|---|---|---|
| SL-01 | a sidecar review is posted to its listener's Slack channel, Approve in Slack approves it and the sidecar retry forwards | must | must |
| SL-02 | Reject in Slack with a reason rejects the sidecar review; the waiting sidecar's claim does not forward | must | must |
| SL-03 | a sidecar review of a listener with no Slack channel is posted to the org's Slack channel | must | must |
| SL-04 | a Slack user whose email matches no hoop user cannot approve; the sidecar review stays pending | must | must |
| SL-05 | a Slack guest or a user of another workspace cannot approve with the admin's email; the admin still can | must | must |
| SL-06 | a reviewed exec through the agent is posted to Slack and Approve by the linked Slack user approves it | must | — |
| SL-07 | a Slack user linked to no hoop user cannot approve a reviewed exec; the review stays pending | must | — |
| SL-08 | Reject in Slack with a reason rejects a reviewed exec and tells its owner in Slack | must | — |
