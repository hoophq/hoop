# PRODUCT.md: the joins the docs cannot make

`https://hoop.dev/docs` describes the product. This file does **not** repeat it — a copy
of a live source rots, and a rotted source of truth is worse than none. It holds the two
things that exist nowhere else, because both describe *our* side of the join:

1. **[Vocabulary](#vocabulary)** — what the product calls a thing against what the code calls it.
2. **[Disagreements](#where-the-docs-and-the-repository-disagree)** — where a doc page and the repository say different things, and which one wins.

For anything else, read the source. It is one command:

```bash
curl -sS -L "https://hoop.dev/docs/<path>.md"
```

## The source of truth is five sections, not the whole site

| Section | Pages |
|---|---|
| Getting Started | `introduction/getting-started`, `introduction/quickstart` |
| Core Concepts | `core-concepts/sidecar`, `core-concepts/control-plane` |
| Features | `features/direct-access`, `features/agentic-access`, `features/data-masking`, `features/guardrails` |
| Control Plane Setup | `control-plane/install`, `control-plane/deployment/{docker-compose,kubernetes,aws}`, `control-plane/connect-sidecar` |
| Sidecar Reference | `setup/configuration/hoop-inspect/{get-started,config-file,policy-rules,risk-analysis,components,kerberos}` |

Everything else on that site — Hoop Gateway, API Reference, Blog, Community — describes a
**different product**. `features/guardrails` says so itself: *"Looking for guardrails on
the Hoop Gateway instead? That is a different implementation."* Never source a Control
Plane fact from `/docs/learn/`, `/docs/clients/` or `/docs/quickstart/`.

**The site has no 404.** An unknown path silently serves the docs home page, whose
subtitle is `Runtime control for agents`, so a link check on the HTTP status passes on a
dead link. Read the body:

```bash
curl -sS -L "https://hoop.dev/docs/<path>.md" | head -8 \
  | grep -q "Runtime control for agents" && echo DEAD
```

`introduction/getting-started` **is** the home page, so it always reports DEAD. That one
is fine.

## Vocabulary

The left column is the product's word. The middle is what the code says today. A mismatch
is a recorded fact, not a rename to do in passing — see the naming rule in
`frontend/CLAUDE.md`.

| Docs term | The code says | Where |
|---|---|---|
| Sidecar | `sidecar/`; the binary and `HOOP_INSPECT_CONFIG` keep the `hoop-inspect` spelling | `sidecar/CLAUDE.md` |
| listener | `lane`, in the resolved config and the policy engine | `GET /config` → `.lanes[]` |
| resource | the upstream a listener points at | listener field `upstream` |
| connection | the operator-facing name a listener records in audit | listener field `connection` |
| — | **Resource Role**, the frontend's primary user-facing noun. Gateway vocabulary for a gateway `connection`; the docs never use it here | `frontend/src/components/ConnectionsMultiSelect` and 10 more |
| AI Analyzer, classifying a **statement** | **AI Session Analyzer**, classifying a **session** | `gateway/api/ai/`, `/ai/session-analyzer/*` |
| Agentic Access — the request *path* through the analyzer | `agentic`, a boolean field on a gateway analyzer rule. Unrelated | migration `000112_ai_session_analyzer_agentic` |
| Data Masking | **Live Data Masking** for the gateway's engine | `/datamasking-rules` |
| rule set | `rulepack`, behind `experimental.rulepacks` | `gateway/api/rulepacks/` |
| review | *Coming soon.* `require_review` is refused at startup | `features/agentic-access` |

## Cardinality, and the key a resource list aggregates on

Two facts that decide the shape of any fleet or resource surface.

**The axis that grows is sidecars, not listeners.** The docs: *"Most deployments run a
single listener. Add a second when the same workload reaches more than one protected
resource."* The scale the design anticipates is the other one — `controlplane/CLAUDE.md`
reasons about *"a thousand per-user pods, fine at fifty."* So the shape is many sidecars
× a handful of listeners each, never one sidecar × a thousand listeners.

**`connection` is the fleet-wide resource identity, and it is many-to-one with
sidecars.** `upstream` is the physical `host:port` and may change; `connection` is the
operator-facing name, and it is what audit and policy key on. Five hundred per-user pods
fronting the same database all carry `connection: appdb`.

Both together give the rule: a **fleet list keyed on the sidecar** answers *"is everything
running what I sent?"*, and it is the surface the docs name — *"which Sidecars are
running, what each one resolved, and when it last checked in"*. It cannot answer *"which
sidecars serve `appdb`?"*, because that question is keyed on `connection`. A resource
index answering it is a **second, additive page**, aggregated by `connection` — never a
flat join, which would render five hundred rows all reading `appdb`.

Consequence for the UI: do not create a resource detail route keyed on
`(sidecar, listener)`. It contradicts the `connection`-keyed identity and one of the two
would have to be withdrawn later.

## Where the docs and the repository disagree

Checked against the tree. **The recorded decision wins over the docs page** — for the
handshake, the docs page says so itself, carrying its own *Draft* warning over
`control_plane.url`, `control_plane.token_file`, `HOOP_CONTROL_PLANE_URL` and
`HOOP_SIDECAR_TOKEN_FILE`.

| Topic | hoop.dev/docs | The repository |
|---|---|---|
| CP ↔ Sidecar transport | "Ordinary HTTP … nothing bidirectional stays open"; "picked up on the ping, not pushed" | One WebSocket per sidecar, opened by the sidecar, `config.apply` pushed down, `status` heartbeats up. gRPC considered and rejected — `controlplane/CLAUDE.md`, "Decided" |
| Sidecar credential | a token file path | a `sidecarauth.Anchor` whose `Verify` returns the sidecar **name**, not a boolean. The anchor itself is open (EVL-234) |
| Control Plane deployment | three pages of its own | those pages deploy the **gateway**: image `hoophq/hoop`, chart `hoop-chart`, service `hoopgateway`, port `8009`, `API_URL`, `POSTGRES_DB_URI`. The Control Plane backend binds `0.0.0.0:8020`, deliberately not 8009 |
| Code paths | `hoopinspect/proxy/proxy.go` | renamed to `sidecar/`; `hoop-inspect` survives as the binary name |

Verified on `main`:

- `grep -rniE "control[_-]?plane" --include="*.go" .` finds only the unrelated IPC in
  `tunnel/`. No control-plane client in `sidecar/`, no sidecar endpoint in `gateway/`, no
  `controlplane` entry in `go.work`. (The `-E` matters — without it `?` is a literal and
  the command matches nothing at all.)
- The sidecar's only HTTP surface is inbound (`serveAdmin`). Its only outbound HTTP is the
  three analyzer providers and the OPA client.
- The Control Plane backend lives on the unmerged branch
  `origin/perotto/evl-230-bootstrap-new-controlplane-package-in-the-hoop-repo`, every
  handler answering `501` and naming its ticket.

## Two rule vocabularies

`docs/adr/0009-guardrails-and-masking-architecture.md` records this and already flags it
as a known cost. It is here because a Control Plane that ships rules to Sidecars has to
reconcile the two, and **nothing in the repository does today**.

| | control-plane path | standalone relay |
|---|---|---|
| rules stored in | Postgres: `guardrail_rules`, `datamasking_rules` | one YAML file |
| shipped by | the gateway, per session | read from disk at startup |
| evaluated in | the **agent**, `libhoop/agent/<proto>/` | the Sidecar process |
| vocabulary | 2 types: `deny_words_list`, `pattern_match` | 8 types, incl. `operation`, `table`, `pii`, `ai_analysis` |
| detection | MS Presidio, alcatraz or GCP DLP | alcatraz, in-process |

`controlplane/frontend` edits the **left** column.

## Related ADRs

| ADR | Settles |
|---|---|
| `0005-sidecar-flow.md` | How one request flows through the relay. Living current-state doc, not a decision. |
| `0006-sidecar-config-defaults-and-overrides.md` | Why `mask` replaces on inherit while `policy.rules` concatenate. |
| `0008-analyzer-enforces-without-opa.md` | The analyzer decides for itself; OPA is the opt-in second decider. |
| `0009-guardrails-and-masking-architecture.md` | The two engines, and the deny/mask split. |
| `0010-local-sql-rule-set.md` | One ordered rule list, first denial wins. |
