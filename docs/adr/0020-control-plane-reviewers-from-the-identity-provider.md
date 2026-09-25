# ADR-0020: Control plane reviewers come from the identity provider

- **Status:** Proposed
- **Date:** 2026-09-25
- **Author:** Rogerio Moura
- **Code:** [`gateway/transport/plugins/slack/events_controlplane.go`](../../gateway/transport/plugins/slack/events_controlplane.go), [`gateway/services/analyzerapproval.go`](../../gateway/services/analyzerapproval.go), [`gateway/api/sidecar/slackchannels.go`](../../gateway/api/sidecar/slackchannels.go), [`gateway/api/login/oidc/login.go`](../../gateway/api/login/oidc/login.go)
- **Related:** PR #1855 (implementation), PR #1850 (closed, the Slack import design), PR #1834 (Slack user group match), ADR-0013 (control plane mode), ADR-0017 (control plane composes sidecar rules), hoophq/documentation#195
- **Supersedes / Superseded by:** —

## Context

A sidecar holds a statement and files a review with the control plane. The
approval rule's `reviewers_groups` names who may release it. Before this
change the control plane named those people in three ways that did not fit:

- The analyzer's hold switch created a rule fixed to `[approver, admin]`.
  Nobody could name another group.
- A Slack click required a hoop user linked by `slack_id`. The control plane
  has no page that makes the link, so an unlinked reviewer got "You are not
  registered" and a link to a page that does not exist. On top of that, #1834
  matched Slack user groups by name on each click.
- Every sidecar review went to the org's one default Slack channel.

Two facts shaped the options:

- Each login already syncs groups: OIDC reads the groups claim
  (`IDP_GROUPS_CLAIM`) and SAML the groups attribute, and both replace the
  user's groups. Only the user who logs in is synced.
- A sidecar reports a statement, not a person. The requester is unknown, so
  requester groups, a self-approval check and a DM to the requester do not
  apply.

## Options considered

1. **Match Slack user groups on each click** (#1834). Loses: a second group
   authority beside the identity provider. hoop keeps no record of who could
   approve. It needs `usergroups:read` and a paid Slack plan.
2. **Import Slack user groups as the directory** (PR #1850). Loses: the same
   second authority, and whoever edits a Slack user group names reviewers.
   Slack does not say whether an identity provider manages a group.
3. **SCIM from the identity provider.** The standard push, with deactivation
   at the source. Loses for now: a public endpoint, a credential at rest, and
   more than this change needs.
4. **Ask each identity provider's directory API.** Loses: one adapter and one
   credential per vendor. OIDC and SAML standardize login only.
5. **Require a `slack_id` link for every reviewer.** Loses: manual setup for
   every person, and the control plane has no page that makes the link.
6. **Use the groups the identity provider sends at login** (chosen). The sync
   exists already, and the identity provider stays the one authority. Costs:
   a reviewer logs in once, and a group change reaches hoop at the next login.

## Decision

**A reviewer is a hoop user, with the groups of their last login.** With an
identity provider, the first login creates the user as active, with the groups
of the token. OIDC keeps the admin group; SAML replaces every group. With local
auth, or a login that sends no groups, an admin sets the groups on the Users
page. An admin can add a user before the first login; that user is `invited`
and may approve with the groups the admin set.

**A Slack click names the approver by Slack ID, then email.** The control plane
looks for the hoop user with the clicking Slack ID. With none, it reads the user
with `users.info` and requires exactly one active or invited hoop user with that
email, case-insensitive. Either way the user must be active or invited, and in
the group of the clicked button. It refuses a deleted user, a bot, a guest, a
user from outside the workspace or its Enterprise Grid, and, on the email path,
an unconfirmed email. Only the clicker sees a refusal, and the refusals that a
login fixes carry the control plane login URL.

**The analyzer rule names its reviewer groups.** `reviewers_groups` takes any
groups of the org; with none, the admin group reviews. An edit that does not
send the field keeps the stored groups. A gateway refuses `sidecar_spec`, so it
never creates this rule.

**Every signed-in user reaches the Reviews page.** An admin lands on Sidecars
and every other user on Reviews. The approve buttons show for the review's
groups, the same check the API makes. Reviewer groups are not roles.

**Slack channels are set per listener**, as guardrails, data masking and the
analyzer bind to a listener. They are stored in `sidecar_slack_channels`, keyed
by org, sidecar and listener name. A configuration write that drops a listener
drops its channels. A listener with none posts to the default channel, which
becomes the fallback. A `PUT` sets only the listeners in its body.

**The gateway keeps its behavior.** Its Slack click still requires the
`slack_id` link, and its Reviews page keeps the approver gate. The new routes
exist on the gateway too, per ADR-0013's one route tree, and answer 412 there.
Two changes reach both products, because they live in the shared Slack service.
Each review message is tracked as soon as it posts, and a settled review gets
no new buttons. A rejection shows its reason on the message.

## Consequences

- Reviewer groups have one authority, the identity provider. The Slack app
  needs `users:read` and `users:read.email`, and no longer `usergroups:read`.
- Each reviewer must log in once, or an admin must add them. A group change in
  the identity provider reaches hoop at the reviewer's next login, and hoop
  cannot tell how old the groups are: there is no last-login column.
- The identity provider must send groups. Entra ID sends group IDs by default,
  and Auth0 sends roles only through an Action that sets the claim on the ID
  token.
- An inactive user matched by email gets "No Hoop user has the email", because
  the lookup skips inactive users.
- The sidecar still denies a rejected statement without the reason: the review
  API it polls does not return `rejection_reason`.
- The `approver` role no longer gates anything a control plane reviewer needs.
  Whether it stays, and in which product, is a separate decision.
- The hoophq/hoop-skills route tables must list the two `slack-channels`
  routes.

## Revisit when

- A customer needs deactivation at the source, or reviewers who never log in:
  add SCIM (option 3).
- Stale groups cause a wrong approval: record the login time and refuse groups
  older than a limit.
