# CLAUDE.md: sidecarauth

How a sidecar proves who it is, and how it knows the control plane is genuine.

**This is a discovery task, not a build task.** The output of the first pass is
a written recommendation, not an implementation. Read this whole file before
proposing anything, because the question as it is usually asked has no answer.

Read `../CLAUDE.md` and `../transport/CLAUDE.md` first.

## The question is two questions

It is usually posed as: *how does the sidecar find the control plane without a
pre-agreed key?* That is two problems wearing one coat, and they have very
different difficulty.

**Discovery: what address do I dial?** An environment variable or a line in
`bootstrap.yaml`. This is not hard and it is not the blocker.

**Trust: how does each side know the other is genuine?** This is the hard one,
it runs in both directions, and it **cannot be bootstrapped from nothing.**
There has to be an anchor. SPIRE ships an `insecure_bootstrap` mode for
testing only, and documents plainly that without an anchor a
machine-in-the-middle controls the whole infrastructure. Any design that
claims to need no pre-shared anything is trusting the network.

So the real question is: **which anchor, and who issues it.**

## Two anchors worth evaluating

**Bootstrap token, one-time-use.** The control plane issues a token, it lands
on the sidecar as an environment variable, and the sidecar exchanges it at
first connect for a short-lived credential it then rotates. The token expires
the moment it is used, so a leaked token that was already redeemed is worthless.

Works in any environment. Known weakness, and it is the reason SPIRE
recommends against it at scale: one token per sidecar, no selectors, and
somebody has to track which token went where. Painful at a thousand per-user
pods, fine at fifty.

**Platform attestation.** The platform vouches for the workload, so there is no
per-sidecar secret at all. Kubernetes projected service account tokens verified
through the TokenReview API, AWS instance identity documents, GCE identity
tokens. This is the literal answer to "without a pre-agreed key", and it is
what SPIRE recommends as the default.

The trade: the anchor becomes the platform. `k8s_psat` trusts the Kubernetes
API server to identify nodes correctly, AWS attestation trusts AWS. Pick the
one whose root of trust the customer actually controls. Also note the control
plane needs to reach the platform's verification API, which is a deployment
constraint, not just a code one.

## Things already decided or known

- **JWT with rotation is the right shape for the credential after bootstrap.**
  Short TTL, rotate at around 80 percent of lifetime. It does **not** solve
  bootstrap; do not let the two get conflated in discussion.
- **`hello` is where this hooks into the wire.** The credential is presented at
  connect, and a failed check produces `hello.reject` with a reason. No config
  flows before `hello.ok`. See `../transport/CLAUDE.md`.
- **Mutual, not one-way.** The sidecar authenticating to us is half of it. A
  sidecar that will accept policy from any server that answers on that address
  is a sidecar an attacker can disarm.

## Prior art in this repo

SPIFFE machinery already exists here and should be read before designing
anything new:

- `gateway/cmd/spiffe-mint/`
- `scripts/local-spiffe-agent.sh`
- `scripts/local-spiffe-kubernetes.sh`
- `common/dsnkeys/` for how agent credentials are currently minted and parsed

Matheus has this flow in their head already and is the first conversation, not
the last.

## What unblocks the MVP right now

The other four components need a sidecar to connect so they can be built and
demoed. They do not need the final answer.

Ship a **named development credential**: a single static shared token, behind
an interface, so the real mechanism swaps in without touching callers.

It must be explicit about what it is. A flag or config key that says
`dev_token`, a startup log line at warn level, and a refusal to run when the
deployment is marked production. Not a silent default that quietly becomes the
shipping behaviour, which is how a placeholder turns into a CVE.

## Deliverable of the discovery pass

A short written recommendation covering: which anchor, who issues it, the
rotation story, what happens when a credential expires while a sidecar is
connected, and how a sidecar is revoked. Revocation is the one usually
forgotten, and it is the first thing a security review asks about.
