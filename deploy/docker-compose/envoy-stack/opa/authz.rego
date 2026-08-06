# Tier 1: the fat gate.
#
# This is the layer InfoSec already owns. It answers one question -- "may this
# user reach this service at all?" -- and nothing else.
#
# What it deliberately does NOT do: inspect the action. A DELETE and a SELECT
# look identical here. That is tier 2, and it lives in hoop-inspect, because
# Envoy cannot parse pgwire and OPA never sees the statement. Even on the HTTP
# lane, where OPA does see method and path, it decides BEFORE the upstream is
# called and so never sees a response.
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
# not a literal. Users absent from the map are denied -- there is no default
# grant, so a typo fails closed rather than falling back to a shared service
# account (the exact Teleport behaviour Matt objects to).
grants := {
	"alice": ["httpbin"],
	"bob": [],
}

service := "httpbin"

allow if {
	some svc in grants[user]
	svc == service
}

# --------------------------------------------------------------- credential
# Nothing to inject. The upstream here is hoop-inspect, which authenticates
# nobody: Envoy already did that, and the sidecar's whole contract is that it
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
