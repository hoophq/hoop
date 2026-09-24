# ADR-0019: The control plane takes reviewers from the identity provider

- **Status:** Accepted
- **Date:** 2026-09-24
- **Author:** Rogerio Moura
- **Code:** [`gateway/transport/plugins/slack/events.go`](../../gateway/transport/plugins/slack/events.go), [`gateway/services/provisioning.go`](../../gateway/services/provisioning.go), [`gateway/api/scim/`](../../gateway/api/scim/), [`gateway/directorysync/`](../../gateway/directorysync/), [`gateway/services/analyzerapproval.go`](../../gateway/services/analyzerapproval.go), [`gateway/api/user/user.go`](../../gateway/api/user/user.go), [`gateway/api/sidecar/slackchannels.go`](../../gateway/api/sidecar/slackchannels.go)
- **Related:** ADR-0013 (control plane mode), #1834 (Slack user groups), EVL-242 (approver role)
- **Supersedes / Superseded by:** —

## Context

A sidecar holds a statement and files a review with the control plane. The
people who release it are named by the approval rule's `reviewers_groups`.
Before this change the control plane got those people wrong in three ways:

- Reviewers were a hoop-only `approver` group (EVL-242), and the analyzer's
  hold switch created a rule fixed to `[approver, admin]`. The customer's
  identity provider already knows who the DBAs are; hoop asked again.
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

1. **Slack user groups as the authority** (#1834). Loses: a second source of
   truth next to the identity provider, and any workspace member may edit a
   user group unless the workspace forbids it. Hoop cannot enforce that.
2. **Ask the identity provider's directory API on each click.** Loses: every
   vendor has its own API and credentials, OIDC and SAML standardize login
   only, and "Other" has nothing to call.
3. **Require one SSO login per reviewer.** Generic, but the reviewer who only
   ever clicks in Slack must first open an app they never use.
4. **Provision users and groups into hoop, and match the Slack click by
   email.** Chosen. SCIM is the standard way an identity provider pushes
   users and groups; for the three common providers that cannot push
   (Google Workspace, Auth0, Cognito) the control plane pulls.

## Decision

**The identity provider is the only source of groups in the control plane.**
Users and groups reach `users` and `user_groups` in one of two ways, never both
for one org:

- **SCIM** (`/api/scim/v2`), for any provider that pushes: Okta, Entra ID,
  OneLogin, JumpCloud, Ping.
- **Directory sync**, which pulls the members of the groups an admin picks from
  Google Workspace (Admin SDK), Auth0 (Management API, roles as groups) or
  Cognito (user pool groups), on an interval and on demand.

While either is active, SSO login stops rewriting a user's groups, so the two
never overwrite each other. The web app stops offering group edits; the API
still accepts them.

**A Slack click names its approver by email.** The control plane reads the
clicking user with `users.info`, refuses a deactivated user, a bot, a guest or
an unconfirmed email, and requires exactly one active hoop user with that email
(case-insensitive). That user's groups must contain the clicked group. When
Slack gives no email (the app lacks `users:read.email`) or the call fails, the
control plane falls back to the `slack_id` link, so an org that has not updated
its Slack app keeps its approvals.

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

- A reviewer never logs in to the control plane. An admin assigns the hoop app
  to a group in the identity provider, and the next Slack click works.
- Adding a provider that cannot push SCIM means a new directory sync adapter.
  The SCIM endpoint also serves as an import API for anything else.
- Directory sync secrets live in the database, as the OIDC client secret
  already does.
- A group renamed in the identity provider is renamed in the approval rules
  that name it, so a rename does not strand a review.
- The Slack app needs `users:read` and `users:read.email`. Until an org adds
  them, the `slack_id` link keeps working.
- The unused directory group listing from #1834 (`gateway/idp/oidc/directory.go`)
  stays for a follow-up.
