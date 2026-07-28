# Tier 1: the fat gate.
#
# This is the layer InfoSec already owns. It answers one question -- "may this
# user reach this service at all?" -- and, on allow, hands Envoy the hoop proxy
# token for that user so the request arrives at hoop already carrying a
# per-user identity.
#
# What it deliberately does NOT do: inspect the action. A DELETE and a SELECT
# look identical here. That is tier 2, and it lives in hoop, because Envoy
# cannot parse pgwire and OPA never sees the statement.
#
# Identity source: X-Citadel-User. In the real deployment this is the verified
# JWT subject that Envoy's jwt_authn filter drops into dynamic metadata
# (input.attributes.metadataContext.filterMetadata). A header keeps the POC
# honest about the shape without dragging an IdP into docker-compose.

package envoy.authz

import rego.v1

default allow := false

http := input.attributes.request.http

user := u if {
	u := http.headers["x-citadel-user"]
	u != ""
}

# Per-user hoop proxy tokens. run.sh mints these against the hoop API and
# rewrites this block. Users absent from the map are denied -- there is no
# default token, so a typo fails closed rather than falling back to a shared
# service account (the exact Teleport behaviour Matt objects to).
tokens := {
	# BEGIN-TOKENS
	"alice": "httpproxy-uc8Laax0AdLTD-dPdwd_e0v4bvux2NZR8_eksBFbhFI",
	"bob": "",
	# END-TOKENS
}

# Service catalogue. In production this is data pushed by the policy bundle,
# not a literal.
grants := {
	"alice": ["httpbin"],
	"bob": [],
}

service := "httpbin"

allow if {
	user
	tokens[user] != ""
	some svc in grants[user]
	svc == service
}

# --------------------------------------------------------------- credential
# On allow, inject the caller's hoop proxy token. hoop reads this at
# gateway/proxyproto/httpproxy/httpproxy.go:245 (Authorization header),
# resolves it to a ConnectionCredentials row, and impersonates that user for
# the whole session. Per-user identity survives the hop.
headers["authorization"] := tokens[user] if allow

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

body := "missing X-Citadel-User header" if status_code == 401

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
