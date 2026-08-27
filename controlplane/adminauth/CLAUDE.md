# CLAUDE.md: adminauth

Signup and signin for the humans who administer the control plane.

Read `../CLAUDE.md` first.

## Administration only, never end users

In hoop 2.0 the end user does not authenticate with us at all. They connect to
their database the way they already do and the sidecar is transparent on the
path. Auth exists so an admin can enter a protected area and invite other
admins.

If a requirement starts with "when the end user logs in", it is in the wrong
product. Push back rather than building it.

## Two populations, never one mechanism

| | `adminauth/` | `sidecarauth/` |
|---|---|---|
| Who | people | workloads |
| Credential | session, from a password or an IdP | short-lived token from an anchor |
| Lifetime | hours, tied to a human being present | minutes, rotated automatically |
| Revocation | the person leaves the company | the sidecar is decommissioned |

Never share a token type across the two. A leaked admin session becoming a
credential that can impersonate a sidecar is the failure this table exists to
prevent.

## MVP scope

Done means: on a first run with an empty database, the operator creates the
first admin; that admin can sign in and out; and no unauthenticated request
reaches `desiredstate/` or `inventory/`.

SSO and OIDC come later and stay administration-only when they land. Inviting
other admins is next after that, not part of the first cut.

## Reuse ideas, not code

`gateway/idp/` is months of working authentication and it is worth reading:
provider resolution, cached verifiers, the local and OIDC and SAML split.

It is also wired to the gateway's Postgres models, its request context, and
its role middleware, none of which exist here. Copying it drags all three
across. Read it, understand what each piece solves, then write the version
this product needs.

## Gotchas

- **First-run admin creation is the classic hole.** An endpoint that creates
  an admin when the user table is empty must stop working the instant it is
  not empty, and must not be reachable over the network before the operator
  has been given the chance to use it. Get this wrong and the first person to
  find the deployment owns it.

- **Do not build roles yet.** One admin role for the MVP. Auditor and
  read-only were a gateway concept and they should be re-derived from what 2.0
  actually needs, not inherited because they existed.

- **Session storage.** `inventory/` is in memory on purpose; admin sessions
  probably should not be, or every control plane restart signs everybody out
  mid-task. Small decision, worth making on purpose.
