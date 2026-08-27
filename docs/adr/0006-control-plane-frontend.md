# ADR-0006: A separate frontend for the control plane

- **Status:** Proposed
- **Date:** 2026-08-27
- **Author:** @rogefm
- **Deciders:** @rogefm
- **Supersedes / Superseded by:** —
- **Linear:** Control Plane → Navigation Routing

## Context

The Control Plane initiative turns hoop into a fleet manager: an admin connects
sidecars, configures features once for all of them, and approves reviews. It is a
different product surface from the gateway, and the Navigation Routing project is its
design entry point — settling the information architecture first is what gives the
other projects somewhere to land instead of each inventing its own surface.

The existing UI is two apps in one. `webapp/` is a ClojureScript SPA; `webapp_v2/` is a
React shell that wraps it, rendering migrated pages itself and handing everything else
to a `/*` catch-all that mounts the CLJS bundle. `webapp_v2/MIGRATION_ROADMAP.md` plans
seven waves before the bundle can be removed.

The control plane does not need most of what is left in that catch-all. Terminal,
runbooks, session browsing, hand-created resources and the resource wizard are out of
scope for a fleet manager. Measuring the dependency closure of the pages it does need
gives 177 of `webapp_v2/src`'s 371 files.

Three constraints shaped the decision:

- `make build-webapp` merges `webapp/resources/` and `webapp_v2/dist/` into one tar with
  a single `index.html`, which `gateway/webappui` serves after substituting placeholders
  in memory. Two SPAs in that pipeline would compete for `/`.
- The control plane is getting its own backend under `controlplane/`, so it does not
  have to share that pipeline.
- `webapp_v2` ships to customers today and must keep working untouched.

## Options considered

1. **Migrate in place, behind a mode flag.** One app, one build, and a boot-time switch
   between the gateway route map and the control plane one. Cheapest to start and it was
   prototyped. It loses because the two products then share a shell, a theme and a
   component library while pulling in opposite directions — every control plane change
   risks the gateway, and the ClojureScript bridge survives in code the control plane
   never runs.

2. **Extract a shared package.** `controlplane/frontend` and `webapp_v2` both consume a
   components-and-theme package. Avoids divergence, but creates an npm workspace and a
   versioning contract between an app in end-of-life and one being born, so the dying
   app gets a vote on the new one's design.

3. **Copy the closure into `controlplane/frontend`, freeze `webapp_v2`.** Duplicates
   ~13k lines that will diverge anyway. Wins because the duplication has an end date:
   `webapp_v2` is deleted when the gateway is retired, and until then neither app can
   break the other.

## Decision

We will build the control plane UI as a separate app under `controlplane/frontend`,
copied from the closure of the pages it keeps, and freeze `webapp_v2`.

The two do not share code. A fix made in one does not reach the other, and that is
accepted for as long as `webapp_v2` is in end-of-life.

The extraction drops what the control plane has no use for rather than carrying it
disabled: the ClojureScript bridge, the Native Connections drawer (nobody reaches a
resource through this app — the sidecar is transparent to the end user), the onboarding
checklist, and every page outside the control plane's scope. Cutting the first two
removes the last references to ClojureScript on their own.

There is no catch-all route. A path the router does not claim renders a 404, and routes
whose backend does not exist yet render a placeholder that names the project that owes
the work.

## Consequences

**Easier.** The control plane's information architecture is defined in one file that
reads as the product, with no gateway routes to explain away. The theme drops the
compatibility layer it carried to match the ClojureScript app's Radix look. New work
lands without a migration wave in front of it.

**Harder.** A bug fixed in a shared component has to be fixed twice while both apps
live. Assets are the sharp edge: `webapp_v2` serves images from the ClojureScript
resources tree through a dev proxy, so anything referenced by `/images` must now be
copied into `controlplane/frontend/public/` — a missing one renders as a broken image
with no error.

**Committed to.** `webapp_v2` is frozen: it takes bug fixes, not features. Its deletion
is now on the critical path for retiring the gateway rather than a cleanup that can be
deferred indefinitely.

**Revisit if** the gateway does not get retired. Two frontends drifting for years is a
worse position than either option we rejected, and the answer then is to promote the
shared package (option 2) rather than let the copies rot.
