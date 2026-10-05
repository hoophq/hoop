# Must not break

The daily parity suite (`make test-parity`, workflow `parity-daily.yml`, ENG-529)
checks each row below against the real `hoop` binary over HTTP.

- `must`: passes today; a failure fails the run.
- `pending ENG-xxx`: the rollout has not delivered it yet. It runs every day and
  shows as pending in the report. When it passes, the report says
  `pass-pending`: drop the marker in the check.
- `—`: does not apply to that run.

Runs:

- **gateway-flag-off**: today's gateway, `beta.sidecar_listeners` off, with a real agent.
- **control-plane**: today's `hoop start control-plane`.
- **gateway-flag-on**: the gateway with `beta.sidecar_listeners` on, with a real agent.
- **migration-rehearsal**: the control plane booted on a synthetic control-plane
  database seeded at schema v127, before the sidecar-in-gateway migrations.

Every run holds an Enterprise license signed by a key the harness generates; the
binary under test is built to trust that key (`buildHoop` in `parity_test.go`).
Slack is a fake Slack server reached through `SLACK_API_URL`.

Each ID here must match one check in this package, and each check must be
listed here: `TestTheListMatchesTheChecks` enforces it.

## Platform

| ID | What must not break | gateway-flag-off | control-plane | gateway-flag-on | migration-rehearsal |
|---|---|---|---|---|---|
| PLT-01 | serverinfo reports the app mode, the flag state and a valid Enterprise license | must | must | must | — |
| PLT-02 | local login issues a token for the registered admin | must | must | must | — |
| PLT-03 | an unauthenticated call to a protected route is refused | must | must | must | — |

## Gateway: agent traffic, connections, reviews, rules

| ID | What must not break | gateway-flag-off | control-plane | gateway-flag-on | migration-rehearsal |
|---|---|---|---|---|---|
| GW-01 | an ad-hoc exec on an agent connection returns its output and records a finished session | must | — | must | — |
| GW-02 | connection CRUD on an agent connection: create, read, replace, conflict, validation, delete | must | — | must | — |
| GW-03 | connection list keeps ordinary connections apart from sidecar mirrors, which exist only with the flag on | must | — | must | — |
| GW-04 | a connection with reviewers holds an exec until approved, then runs it once | must | — | must | — |
| GW-05 | a rejected review leaves its exec unexecutable and cannot be approved afterwards | must | — | must | — |
| GW-06 | a command access request rule holds an agent exec until approved, then runs it | must | — | must | — |
| GW-07 | access request rule CRUD with the gateway's validation: targets, reviewers, one rule per connection and type | must | — | must | — |
| GW-08 | guardrail rule CRUD, with no sidecar fields on an ordinary rule | must | — | must | — |
| GW-09 | a guardrail bound to an agent connection blocks a matching exec and records the violation | must | — | must | — |
| GW-10 | a data masking rule is refused with 422 when no DLP provider is configured, and nothing is stored | must | — | must | — |
| GW-11 | a guardrail carrying a sidecar_spec is stored and read back on the gateway | — | — | pending ENG-524 | — |

## Sidecar setup, configuration and import

| ID | What must not break | gateway-flag-off | control-plane | gateway-flag-on | migration-rehearsal |
|---|---|---|---|---|---|
| SC-01 | an admin creates a sidecar, gets its token once, and lists and reads it by name and id | must | must | must | — |
| SC-02 | the handshake serves the configuration with its revision, the license-managed header and the license, and records the check-in | must | must | must | — |
| SC-03 | a sidecar with no listeners assigned gets 412 on the handshake and is not recorded as seen | must | must | must | — |
| SC-04 | GET /sidecars/configuration serves the handshake's document and records nothing | must | must | must | — |
| SC-05 | PUT and PATCH of a sidecar configuration change the served revision; PATCH merges into the stored document | must | must | must | — |
| SC-06 | PATCH load_from_disk hands the document to the sidecar's file and back | must | must | must | — |
| SC-07 | a sidecar imports its config file once; the plane adopts it, splits the embedded rules into rule items and serves them back | must | must | must | — |
| SC-08 | every sidecar route refuses a missing or unknown token with 401 | must | must | must | — |
| SC-09 | deleting a sidecar revokes its token at once | must | must | must | — |
| SC-10 | with experimental.sidecar_session_events on, the handshake offers session events and POST /sidecars/events records a session idempotently | must | must | must | — |
| SC-11 | with beta.sidecar_listeners on, each listener is a <sidecar>-<listener> connection with every access mode disabled, owned by the sidecar | — | — | must | — |
| SC-12 | with beta.sidecar_listeners off, sidecar writes and imports create no connection | must | must | — | — |
| SC-13 | a sidecar's mirror connections read online once it checks in | — | — | pending ENG-530 | — |

## License

| ID | What must not break | gateway-flag-off | control-plane | gateway-flag-on | migration-rehearsal |
|---|---|---|---|---|---|
| LIC-01 | sidecar admin and daemon routes answer 403 while the organization holds no Enterprise license, and 2xx again once it does | must | must | must | — |
| LIC-02 | the served sidecar configuration carries the organization's license, and a per-sidecar license is refused | must | must | must | — |

## Sidecar reviews: hold, approve, retry

| ID | What must not break | gateway-flag-off | control-plane | gateway-flag-on | migration-rehearsal |
|---|---|---|---|---|---|
| RV-01 | the approval rule a listener holds statements under is served to the sidecar on handshake | — | must | pending ENG-526 | — |
| RV-02 | a held statement files a PENDING review that names the sidecar, the listener and the rule's policy | — | must | pending ENG-526 | — |
| RV-03 | resending a held statement answers the review already filed for it; another statement files its own | — | must | pending ENG-526 | — |
| RV-04 | an approved review releases its statement exactly once on resend | — | must | pending ENG-526 | — |
| RV-05 | a waiting sidecar claims its review by id: pending waits, approved releases once, reads never consume | — | must | pending ENG-526 | — |
| RV-06 | a rejected review never releases its statement and keeps the reviewer's reason | — | must | pending ENG-526 | — |
| RV-07 | revoking an approval before the sidecar claims it withholds the statement; a resend files a new review | — | must | pending ENG-526 | — |
| RV-08 | a sidecar lists its reviews newest first, filtered by status and bounded by limit | — | must | pending ENG-526 | — |
| RV-09 | a sidecar token reaches only the reviews that sidecar filed and the rules its own listeners name | — | must | pending ENG-526 | — |
| RV-10 | a review request the served config does not authorize, or that is malformed, files nothing | — | must | pending ENG-526 | — |
| RV-11 | sidecar review routes refuse a caller without a valid sidecar token | must | must | must | — |
| RV-12 | a review settles by the policy of its rule: all groups, a minimum, or a force approval | — | must | pending ENG-526 | — |

## Sidecar rules: guardrails, masking, analyzer

| ID | What must not break | gateway-flag-off | control-plane | gateway-flag-on | migration-rehearsal |
|---|---|---|---|---|---|
| RUL-01 | a guardrail bound to one listener is served on that listener only | — | must | pending ENG-525 | — |
| RUL-02 | a data masking rule with a sidecar spec saves without a DLP provider and is served on its listener | — | must | pending ENG-525 | — |
| RUL-03 | an analyzer rule bound to a listener serves its trigger, verdict actions and prompt there | — | must | pending ENG-525 | — |
| RUL-04 | an analyzer rule that holds statements creates its approval rule, and reviews follow its binding | — | must | pending ENG-525 | — |
| RUL-05 | unbinding a rule with sidecar_targets [] removes it from the served config and moves the revision | — | must | pending ENG-525 | — |
| RUL-06 | an edit that omits sidecar_spec and sidecar_targets keeps the rule bound and served | — | must | pending ENG-525 | — |
| RUL-07 | a binding the sidecar could not run is refused at save and stores nothing | — | must | pending ENG-525 | — |

## Slack approval

| ID | What must not break | gateway-flag-off | control-plane | gateway-flag-on | migration-rehearsal |
|---|---|---|---|---|---|
| SL-01 | a sidecar review is posted to its listener's Slack channel, Approve in Slack approves it and the sidecar retry forwards | — | must | pending ENG-526 | — |
| SL-02 | Reject in Slack with a reason rejects the sidecar review; the waiting sidecar's claim does not forward | — | must | pending ENG-526 | — |
| SL-03 | a sidecar review of a listener with no Slack channel is posted to the org's Slack channel | — | must | pending ENG-526 | — |
| SL-04 | a Slack user whose email matches no hoop user cannot approve; the sidecar review stays pending | — | must | pending ENG-526 | — |
| SL-05 | a Slack guest or a user of another workspace cannot approve with the admin's email; the admin still can | — | must | pending ENG-526 | — |
| SL-06 | a reviewed exec through the agent is posted to Slack and Approve by the linked Slack user approves it | must | — | must | — |
| SL-07 | a Slack user linked to no hoop user cannot approve a reviewed exec; the review stays pending | must | — | must | — |
| SL-08 | Reject in Slack with a reason rejects a reviewed exec and tells its owner in Slack | must | — | must | — |

## Migration rehearsal on a synthetic control-plane database

| ID | What must not break | gateway-flag-off | control-plane | gateway-flag-on | migration-rehearsal |
|---|---|---|---|---|---|
| MIG-01 | a control-plane database at v127 boots, migrated to the newest schema and not dirty | — | — | — | must |
| MIG-02 | every seeded listener has exactly one sidecar-managed mirror; a taken or invalid name gets the fallback name | — | — | — | must |
| MIG-03 | rules bound before the migration are still served, in position order, in each sidecar's handshake | — | — | — | must |
| MIG-04 | seeded sidecar admin data reads back unchanged: sidecars, bound rules, slack channels, approval rule | — | — | — | must |
| MIG-07 | sidecar reviews filed before the migration still read back to their sidecar, status and rule intact | — | — | — | must |
| MIG-06 | rules bound to listeners are bound through the mirror connections, in the same position | — | — | — | pending ENG-525 |
| MIG-05 | rolling the copy back to v127 removes the mirrors, keeps every seeded binding, and rolls forward again | — | — | — | must |
