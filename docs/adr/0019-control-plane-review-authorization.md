# ADR-0019: The control plane names reviewers without a login

- **Status:** Proposed
- **Date:** 2026-09-24
- **Author:** Rogerio Moura
- **Code:** [`gateway/transport/plugins/slack/events_controlplane.go`](../../gateway/transport/plugins/slack/events_controlplane.go), [`gateway/services/provisioning.go`](../../gateway/services/provisioning.go), [`gateway/directorysync/`](../../gateway/directorysync/), [`gateway/api/scim/`](../../gateway/api/scim/), [`gateway/api/user/import.go`](../../gateway/api/user/import.go), [`gateway/services/analyzerapproval.go`](../../gateway/services/analyzerapproval.go), [`gateway/api/user/user.go`](../../gateway/api/user/user.go), [`gateway/api/sidecar/slackchannels.go`](../../gateway/api/sidecar/slackchannels.go)
- **Related:** ADR-0013 (control plane mode), #1834 (Slack user groups), EVL-242 (approver role)
- **Supersedes / Superseded by:** —

## Context

A sidecar holds a statement and files a review with the control plane. The
people who release it are named by the approval rule's `reviewers_groups`.
Before this change the control plane got those people wrong in three ways:

- Reviewers were a hoop-only `approver` group (EVL-242), and the analyzer's
  hold switch created a rule fixed to `[approver, admin]`.
- A Slack click was authorized by Slack user groups whose name matched a
  review group (#1834), and still required a hoop user linked by `slack_id`.
  That link is made on a ClojureScript page the control plane never loads.
- Every sidecar review went to the org's default Slack channel.

Reviewers of a sidecar usually never open the control plane. They approve in
Slack. Whatever names them must work without a login.

The requester is unknown: a sidecar reports a statement, not a person. Nothing
here can recover requester groups, self-approval checks, or a DM to the
requester.

## Options considered

1. **Match Slack user groups on each click** (#1834). Loses: the group is read
   at click time, so hoop keeps no record of who could approve, and a user
   group edit takes effect with no audit trail in hoop.
2. **Ask the identity provider's directory API on each click.** Loses: every
   vendor has its own API and credentials, OIDC and SAML standardize login
   only, and "Other" has nothing to call.
3. **Require one SSO login per reviewer.** Generic, but the reviewer who only
   ever clicks in Slack must first open an app they never use.
4. **An admin creates or imports users with their groups.** Always works, with
   no setup and no vendor. Loses: the list goes stale the day someone leaves.
5. **Slack as the directory.** Import the members of chosen Slack user groups
   on an interval. Chosen as the default, with SCIM and a file import beside
   it.
6. **Pull from each identity provider's directory** (Google Workspace, Auth0,
   Cognito). Loses: one adapter and one stored credential per vendor, for
   customers who mostly already manage Slack from that same identity provider.

## Decision

**Users and groups reach `users` and `user_groups` from four sources**, all
through `gateway/services/provisioning.go`, in this order of preference:

- **Slack import** (default). The control plane reads `users.list` and
  `usergroups.list` through the org's Slack app and imports the members of the
  user groups an admin picks. The group handle is the hoop group name:
  `@dba-leads` is the group `dba-leads`. It writes `users.slack_id`.
- **SCIM** (`/api/scim/v2`), for an identity provider that pushes: Okta, Entra
  ID, OneLogin, JumpCloud, Ping. The identity provider is the authority.
- **File import**: a CSV of `email,name,groups`.
- **Manual**: the Users page edits groups for every auth method.

Slack is the default because the click and the directory are the same
identity: the person who clicks Approve is the Slack user the import read.
Deactivation flows from the identity provider through Slack, which most
workspaces provision from it. The trade-off: hoop cannot enforce who edits a
user group, and the Slack API has no flag saying the identity provider manages
one. So a run refuses a user group whose last editor is not a workspace admin
or owner, unless the admin allows member-managed groups; the Provisioning page
says to restrict user group editing to admins in Slack.

The Slack import and SCIM manage the groups: while either has written a user
or a group, SSO login stops rewriting groups and the Users page changes only
the admin group. An explicit "stop managing groups" hands them back. A file
import is an admin's own edit and does not manage them. A user who leaves every
synced group keeps the account and loses those groups; only the source
deactivates a user (Slack `deleted`, SCIM `active=false`), and a sync never
deactivates an administrator. No source may write a group named `admin`,
`auditor` or `approver`. No vendor secret is stored; the SCIM token is kept as
a hash.

**A Slack click names its approver by Slack ID, then email.** The control
plane looks up the hoop user linked to the clicking Slack user; with none, it
reads the user with `users.info` and requires exactly one hoop user with that
email (case-insensitive). Either way the user must be active or invited, and
their groups must contain the clicked group. It refuses a deleted user, a bot,
a guest, a user from another organization, and an unconfirmed email.

**Reviewer groups are chosen on the rule.** The analyzer's hold switch takes
`reviewers_groups`; with none it names the admin group. A user is reported with
the `approver` role when their groups meet any sidecar rule's
`reviewers_groups`, which is what opens the Reviews page to them.

**Slack channels are set per sidecar or per listener.** A listener's channels
replace its sidecar's; with neither, the org default applies. The default
channel keeps receiving every review, as on the gateway.

**The gateway does not change.** Every branch sits behind `IsControlPlane()`,
or in a `ControlPlane*` file of the web app. ADR-0013 keeps one route tree for
both modes, so the new routes exist on the gateway too and answer 412 there,
as `/sidecars/reviews` already does. That is a handler hiding a feature by
mode, which ADR-0013 said none would; it is accepted for routes whose data
only a control plane has.

## Consequences

- A reviewer never logs in to the control plane. An admin picks a Slack user
  group, pushes SCIM, or uploads a file, and the next Slack click works.
- A group renamed at the source is renamed in `user_groups`, in the access
  request rules that name it (`reviewers_groups`, `force_approval_groups`,
  `approval_required_groups`, `skip_review_groups`) and in the groups of
  pending reviews. Settled reviews keep the name they were decided under.
- Every Slack import run writes one audit entry with what changed; SCIM writes
  are audited with the admin who generated the token as the actor.
- The Slack app needs `users:read`, `users:read.email` and `usergroups:read`.
  Without the first two, only users with a Slack ID link can approve.
- Directory sync from an identity provider comes back when a named customer
  on Google Workspace does not manage Slack from it: one provider at a time,
  Google first, by OAuth consent rather than a stored service account key.
