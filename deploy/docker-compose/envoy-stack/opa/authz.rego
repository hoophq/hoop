# Tier 1: the fat gate.
#
# This is the layer InfoSec already owns. It answers one question, "may this
# user reach this service at all?", and nothing else.
#
# It does NOT inspect the action, on purpose. A DELETE and a SELECT look
# identical here. That is tier 2, and it lives in hoop-inspect, because
# Envoy cannot parse pgwire and OPA never sees the statement. Even on the HTTP
# lane, where OPA does see method and path, it decides BEFORE the upstream is
# called and so never sees a response.
#
# The second half of this file is tier 2's decision, served to hoop-inspect
# over the Data API rather than to Envoy over ext_authz. Same OPA, same
# review, two callers.
#
# Identity source: X-Hoop-User. In the real deployment this is the verified
# JWT subject that Envoy's jwt_authn filter drops into dynamic metadata
# (input.attributes.metadataContext.filterMetadata). A header keeps the stack
# honest about the shape without dragging an IdP into docker-compose.

package envoy.authz

import rego.v1

default allow := false

http := input.attributes.request.http

user := u if {
	u := http.headers["x-hoop-user"]
	u != ""
}

# Service catalogue. In production this is data pushed by the policy bundle,
# not a literal. Users absent from the map are denied: there is no default
# grant, so a typo fails closed rather than falling back to a shared service
# account (the exact Teleport behaviour Matt objects to).
grants := {
	"alice": ["httpbin", "ledger"],
	"bob": [],
}

# One listener, one service. The base stack has a single ext_authz caller
# (:8443 -> httpbin); the grpc/ overlay adds :8444 -> ledger, and keying on
# the destination port keeps this file the one policy both stacks load.
# A port neither rule names yields an undefined service, and the grant
# check fails closed on it.
listener_port := input.attributes.destination.address.socketAddress.portValue

service := "ledger" if listener_port == 8444

service := "httpbin" if listener_port != 8444

allow if {
	some svc in grants[user]
	svc == service
}

# --------------------------------------------------------------- credential
# Nothing to inject. The upstream here is hoop-inspect, which authenticates
# nobody: Envoy already did that, and the sidecar's contract is that it
# sits behind something owning identity. Correlation is the one thing worth
# passing on, so an audit row can be joined to an Envoy access log line.
headers["x-hoop-correlation-id"] := http.headers["x-request-id"] if {
	allow
	http.headers["x-request-id"]
}

# ------------------------------------------------------------------- denials
status_code := 200 if {
	allow
} else := 401 if {
	not user
} else := 403

body := "missing X-Hoop-User header" if status_code == 401

body := sprintf("user %q is not granted access to %q by the fat gate (tier 1)", [user, service]) if {
	status_code == 403
	user
}

body := "unknown user" if {
	status_code == 403
	not user
}

# Envoy's ext_authz output contract.
# https://www.openpolicyagent.org/docs/envoy/primer
result["allowed"] := allow

result["headers"] := headers

result["http_status"] := status_code

result["body"] := body

# ================================================================== tier 2
# The same OPA, a different caller.
#
# Everything above answers Envoy over ext_authz gRPC. Everything below answers
# hoop-inspect over the Data API at /v1/data/envoy/authz/inspect, which is
# where the sidecar's policy.opa.url points once the marked block in
# sidecar/config.yaml is uncommented. One package because compose loads
# one policy file, and a second rule name is cheaper than a second mount.
#
# `input` here is a parsed statement (protocol, operation, tables, statement
# text) rather than an Envoy CheckRequest, and the answer is {"allow": true}
# or {"denied": true, "message": ...}. Envoy cannot produce that input; it has
# no pgwire parser.
#
# Nothing queries these rules until that config block is uncommented, so they
# cost the default stack nothing. ./run.sh needs no model credential.

# The tables worth spending a model call on. One exists in this stack's
# schema; add names here rather than widening the rules below.
inspect_sensitive := {"customers"}

inspect_touches_sensitive if input.tables[_] in inspect_sensitive

# A single-call lane sends no phase at all. Reading an absent phase as
# "decide" keeps these rules answering if someone uncomments the opa: block
# without the gate and without anything that defers; otherwise `inspect` is
# undefined on that lane and fail_open: false denies every statement.
inspect_phase := object.get(input, "phase", "decide")

# ------------------------------------------------------------- gate phase
# Runs BEFORE the producers and answers one question per source: is this
# statement worth running that producer on? `request` overrides the source's
# own configuration (the ai rule's `trigger` here), so the cost control
# sits next to the policy that spends the money instead of in a YAML trigger
# the Rego author never sees.
#
# An undefined gate decision allows and requests nothing, even under
# fail_open: false. A gate is an optimization over a policy someone already
# wrote, so turning it on must not block every statement until a second rule
# exists.
inspect := {"allow": true, "request": {"ai_analysis": true}} if {
	inspect_phase == "gate"
	inspect_touches_sensitive
}

inspect := {"allow": true, "request": {"ai_analysis": false}} if {
	inspect_phase == "gate"
	not inspect_touches_sensitive
}

# ----------------------------------------------------------- decide phase
# Runs AFTER the producers, carrying what each of them established in
# input.findings, keyed by source. This is the determination that would
# otherwise be `high: block` in hoop-inspect's YAML; `high: defer` on the ai
# rule and `action: defer` on the pii rule hand it here instead.
#
# One else-chain rather than three independent `inspect` definitions: a
# statement can trip the ai finding and the pii finding at once, and two
# complete rules producing different objects is an eval error rather than a
# precedence order.
inspect := inspect_decide if inspect_phase == "decide"

# The ai_analysis producer. UNDEFINED on a lane carrying no ai_analysis rule,
# which is what separates "no analyzer here" from "the analyzer ran and could
# not answer": a configured analyzer reports a finding even when it skips.
inspect_ai := input.findings.ai_analysis

# status is ok, cached, skipped, unavailable or error, and `reason` narrows
# unavailable to budget_exhausted or refused. values.risk_level is present
# ONLY for ok and cached, so a classification that never happened cannot read
# as low. Model prose never arrives: OPA's decision log copies everything sent
# to it.
inspect_ai_answered if inspect_ai.status in {"ok", "cached"}

inspect_ai_level := object.get(inspect_ai, ["values", "risk_level"], "")

# The pii producer, published by the commented `sensitive-columns` rule in
# sidecar/config.yaml. Its values.entities is the one thing in
# input.findings that Rego could not have read off input itself: the text is
# in input.statement, but running a detector over it is not something Rego
# does. The matched VALUES never travel, only the entity classes.
inspect_pii_entities := object.get(input, ["findings", "pii", "values", "entities"], [])

# BR_CPF is absent on purpose: no-cpf-in-query denies it locally, for free,
# before OPA is ever called.
inspect_pii_protected := {"US_SSN", "CREDIT_CARD", "IBAN_CODE"}

inspect_pii_hits := {e | some e in inspect_pii_entities; e in inspect_pii_protected}

inspect_decide := {"denied": true, "rule": "ai-unavailable", "message": msg} if {
	# Fail closed on protected data nobody classified. A level-only
	# contract cannot express this case, which is why status exists.
	# Naming inspect_ai.status is also the presence check: with no ai rule
	# on the lane this branch is undefined and the chain falls through,
	# rather than denying everything because nothing classified.
	inspect_touches_sensitive
	not inspect_ai_answered
	msg := sprintf("risk analysis is %v and this statement touches protected data", [inspect_ai.status])
} else := {"denied": true, "rule": "ai-high-risk", "message": msg} if {
	# No break-glass exception here. Every psql session in this stack
	# connects as appuser, so there is no actor to write one against.
	inspect_ai_level == "high"
	msg := sprintf("blocked by policy: %v rated this high risk", [inspect_ai.rule])
} else := {"denied": true, "rule": "pii-in-query", "message": msg} if {
	count(inspect_pii_hits) > 0
	msg := sprintf("do not put %v in a query; it lands in the database's own logs", [concat(", ", sort(inspect_pii_hits))])
} else := {"allow": true}
