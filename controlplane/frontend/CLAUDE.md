# CLAUDE.md: controlplane frontend

The admin interface for the control plane. Read `../CLAUDE.md` first: the
non-negotiables and the decided/open tables there bind this too.

**Nothing here is built yet, and the stack is not chosen.** This file records
what is already settled and what still has to be decided, so the first person
in does not have to guess which is which.

One file on purpose. Split it once there is enough code to split.

## Settled

- **Admins only, never end users.** In hoop 2.0 the end user does not
  authenticate with us at all: they connect to their database the way they
  already do and the sidecar is transparent on the path. There is no end-user
  surface to build. A requirement starting with "when the end user logs in" is
  in the wrong product.
- **Show desired and actual as two different things.** The config an admin
  wrote and the config a sidecar is running disagree routinely, and that is
  the normal condition rather than an error. A UI that renders the intended
  state as though it were the live state hides exactly the problem the
  operator opened it to find. Applied generation and NACK reasons belong on
  screen, not only in a log.
- **The empty fleet after a restart is not an outage.** The backend inventory
  is in memory, so the list is empty until sidecars reconnect, roughly one
  backoff window. Say that on screen. An empty table with no explanation reads
  as "everything is down".
- **The config document is `hoopinspect/sidecar.Config`.** It already exists
  and already validates exhaustively. An editor that builds its own idea of
  the schema will accept configs the sidecar then refuses.

## Open

- **The stack.** `webapp_v2/` is React 19, Vite, Mantine v8, Zustand and React
  Router v7, and it is where the 1.0 frontend is heading; `webapp/` is the
  legacy ClojureScript app being retired. Whether this reuses that stack,
  shares components with it, or starts clean is undecided. Decide it before
  writing components, not after.
- **Where it is served from.** Bundled into the control plane binary, as the
  gateway does today, or a separate origin. This drives the auth and CORS
  story, so it is not only a packaging question.
- **Whether there is a terminal UI as well as a web one.** Raised in the
  architecture session, never decided.

## Reuse ideas, not code

Same rule as the backend. `webapp_v2/` solves real problems worth reading:
its store and service split, its Mantine conventions, its shell. It is also
built around the 1.0 gateway's API shape and its session model, neither of
which exists here. Read it, then write what this product needs.

If the stack decision does land on reusing `webapp_v2/`, read
`webapp_v2/CLAUDE.md` and `webapp_v2/COMPONENTS.md` before building anything,
and treat their rules as binding here too.
