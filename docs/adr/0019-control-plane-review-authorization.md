# ADR-0019: The control plane names reviewers without a login

- **Status:** Proposed
- **Date:** 2026-09-24
- **Author:** Rogerio Moura
- **Code:** [`gateway/transport/plugins/slack/events_controlplane.go`](../../gateway/transport/plugins/slack/events_controlplane.go), [`gateway/directorysync/`](../../gateway/directorysync/), [`gateway/services/analyzerapproval.go`](../../gateway/services/analyzerapproval.go), [`gateway/api/sidecar/slackchannels.go`](../../gateway/api/sidecar/slackchannels.go)
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
4. **An admin adds users with their groups.** Always works, with no setup and
   no vendor. Loses: the list goes stale the day someone leaves. Kept as the
   fallback, on the Users page. A CSV import of the same data was written and
   removed: no customer needs it yet.
5. **Slack as the directory.** Import the members of chosen Slack user groups
   on an interval. Chosen for the first version, with the Users page beside
   it.
6. **Pull from each identity provider's directory** (Google Workspace, Auth0,
   Cognito). Loses: one adapter and one stored credential per vendor, for
   customers who mostly already manage Slack from that same identity provider.
7. **SCIM from the identity provider.** The standard push, and the right
   authority when the identity provider does not manage Slack. Deferred to the
   next step: it adds a public endpoint, a credential at rest and two
   dependencies, and the first version does not need it (see Next steps).

## Decision

**Users and groups reach `users` and `user_groups` from two sources:**

- **Slack import** (default, `gateway/directorysync/`). The control plane
  reads `users.list` and `usergroups.list` through the org's Slack app and
  imports the members of the user groups an admin picks. The group handle at
  the first import is the hoop group name: `@dba-leads` is the group
  `dba-leads`. It writes `users.slack_id`, which is the only link between a
  hoop user and Slack.
- **Manual**: the Users page adds users and edits groups for every auth
  method.

Slack is the default because the click and the directory are the same
identity: the person who clicks Approve is the Slack user the import read.
Deactivation flows from the identity provider through Slack, which most
workspaces provision from it. The trade-off: hoop cannot enforce who edits a
user group, and the Slack API has no flag saying the identity provider manages
one. So a run refuses a user group whose last editor is not a workspace admin
or owner, unless the admin allows member-managed groups; the Provisioning page
says to restrict user group editing to admins in Slack.

While the Slack import owns a group, SSO login stops rewriting groups.
Removing the import hands them back; users and their groups stay. The Users
page always edits groups, and a run resets only the groups it imports. A user
who leaves every imported group keeps the account and loses those groups;
only Slack deactivates a user (`deleted`), and an import never deactivates an
administrator or reactivates a user an admin deactivated. The import refuses
a group named `admin`, `auditor` or `approver`. No secret is stored.

**A Slack click names its approver by Slack ID, then email.** The control
plane looks up the hoop user linked to the clicking Slack user; with none, it
reads the user with `users.info` and requires exactly one hoop user with that
email (case-insensitive). Either way the user must be active or invited, and
their groups must contain the clicked group. It refuses a deleted user, a bot,
a guest, a user from another organization, and an unconfirmed email.

**Reviewer groups are chosen on the rule.** The analyzer's hold switch takes
`reviewers_groups`; with none it names the admin group. These groups are not
roles: their members approve in Slack. The `approver` role keeps its meaning,
the reserved group, which opens the Reviews page.

**Slack channels are set per listener**, as guardrails, data masking and the
analyzer bind to a listener. The default channel keeps receiving every review,
as on the gateway.

**The gateway does not change.** Every branch sits behind `IsControlPlane()`,
or in a `ControlPlane*` file of the web app. ADR-0013 keeps one route tree for
both modes, so the new routes exist on the gateway too and answer 412 there,
as `/sidecars/reviews` already does. That is a handler hiding a feature by
mode, which ADR-0013 said none would; it is accepted for routes whose data
only a control plane has.

## Consequences

- A reviewer never logs in to the control plane. An admin picks a Slack user
  group or adds the user, and the next Slack click works.
- A user group renamed in Slack keeps its hoop name, so the rules that name it
  and pending reviews keep working. The import writes no table the gateway
  reads for rules or reviews.
- Every Slack import run writes one audit entry with what changed.
- The Slack app needs `users:read`, `users:read.email` and `usergroups:read`.
  Without the first two, only users with a Slack ID link can approve.
- Directory sync from an identity provider comes back when a named customer
  on Google Workspace does not manage Slack from it: one provider at a time,
  Google first, by OAuth consent rather than a stored service account key.

## Next steps

**SCIM** (`/api/scim/v2`), for an identity provider that pushes users and
groups: Okta, Entra ID, OneLogin, JumpCloud, Ping. It is the right authority
when the identity provider does not manage Slack, and it adds deactivation at
the source (`active=false`). A first implementation was written and removed
from this change to keep it small; it is in the history of PR #1850 (commits
`a0af561` and `f5e28f7`). What it needs:

- A bearer token per org, stored as a hash, rotated with one `PUT`, and
  audited with the admin who generated it as the actor.
- The Slack import's write path (`gateway/directorysync/write.go`) moved to a
  shared place, a table linking hoop users to SCIM ids, and only one of SCIM
  and the Slack import active per org.
- A unique index on `lower(user_name)` per source (RFC 7643), a unique
  violation answered as 409, and reserved group names answered as 400
  `invalidValue`.
- Tests against the Okta and Entra ID request shapes: PATCH with no path,
  `"True"`/`"False"` strings, `members[value eq "…"]` removal, and a group
  rename.
