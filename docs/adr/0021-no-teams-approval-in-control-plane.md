# ADR-0021: No Microsoft Teams approval in the control plane

- **Status:** Proposed
- **Date:** 2026-09-25
- **Author:** Rogerio Moura
- **Deciders:** Rogerio Moura, Mat
- **Related:** PR #1855 (Slack approval by email), ADR-0013 (control plane mode), ADR-0020 (control plane reviewers come from the identity provider)
- **Supersedes / Superseded by:** —

## Context

PR #1855 lets a reviewer approve a held sidecar statement from Slack. We
asked if Teams can do the same: Approve and Reject on the card, the
rejection reason on the card, and the card updated when the review settles.

What Teams has in hoop today:

- A one-way card, sent through Svix by the gateway webhooks plugin
  (`gateway/transport/plugins/webhooks/webhooks.go`). It has no buttons.
- It does not reach control plane reviews. A sidecar files its review over
  HTTP (`gateway/api/sidecar/reviews.go`), and the control plane runs only
  the Slack plugin (`controlPlanePlugins` in `gateway/main.go`).

What Teams requires for buttons with a known clicker:

- **A bot with a public HTTPS endpoint.** Teams calls the bot. Nothing like
  Slack Socket Mode exists, and dev tunnels are for development only. The
  control plane must be reachable from the internet.
- **Registration and install per customer.** Someone registers the bot,
  a Teams admin uploads the app to the org catalog, and the app is
  installed in each team.
- **Identity by Entra object ID, not email.** Microsoft says not to use
  `email` or `upn` for authorization: they are not unique and tenant
  admins can change them ([claims validation](https://learn.microsoft.com/entra/identity-platform/claims-validation),
  [ID token claims](https://learn.microsoft.com/entra/identity-platform/id-token-claims-reference)).
  The members API may also stop returning email and UPN. So hoop must link
  each Teams user to a hoop user by `oid` and tenant.
- **No Go SDK.** The Bot Framework SDK support ended on 2025-12-31. The
  Microsoft 365 Agents SDK and the Teams SDK ship for C#, JavaScript and
  Python only. hoop would write the inbound JWT checks and the Connector
  REST calls by hand, and the JWT check is security-critical.

## Options considered

1. **Native Teams bot with Adaptive Card actions.** Gives Slack parity.
   Loses: all four costs above, for each customer and for hoop.
2. **Workflows webhook per listener, with a link to the Reviews page.**
   Simple, and needs no public endpoint. Loses: no approval inside Teams,
   and the card never updates. That is the need we had.
3. **Customer-built flow with "Post adaptive card and wait for a
   response".** Buttons with no bot. Loses: the flow must call a public
   hoop endpoint, hoop must trust the flow's claim of who clicked, and the
   HTTP action likely needs a premium license.
4. **Microsoft Graph Approvals API.** Loses: beta only, and an app cannot
   create an approval (delegated permission only).

## Decision

We do not build Teams approval for the control plane now. Slack stays the
only chat tool that approves control plane reviews. The gateway Teams card
stays as it is.

## Consequences

- No public endpoint, bot credential or Entra link enters the control
  plane.
- A Teams-only customer approves on the Reviews page. The group check is
  the same as a Slack click (`doIndividualReview` in
  `gateway/api/review/review.go`).
- Revisit when a customer requires approval inside Teams and can expose
  the control plane, or when Microsoft ships a Go SDK or an outbound-only
  connection. Start from option 1, with the `oid` link from the start.
