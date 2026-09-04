#!/usr/bin/env bash
#
# Walks the gRPC lane, through Envoy and without it.
#
#   grpcurl ──TLS──> envoy:8444 ──ext_authz(OPA)──> hoop-inspect ──h2c──> ledger
#   grpcurl ──h2c──────────────────────────────────> hoop-inspect ──h2c──> ledger
#
# Prereqs, from the envoy-stack directory:
#   ./run.sh
#   docker compose -f docker-compose.yml -f grpc/docker-compose.grpc.yml up -d --wait
#
# The client is the grpcurl service in the overlay, run on demand; nothing
# gRPC needs to be installed on the host. The ledger server publishes no
# reflection (it has no gRPC library at all), so every call names the same
# descriptor set the sidecar reads: -protoset /descriptors/ledger.pb.

set -uo pipefail
cd "$(dirname "$0")/.."

hr()   { printf '\033[2m%s\033[0m\n' "----------------------------------------------------------------"; }
h()    { printf '\n\033[1;36m%s\033[0m\n' "$*"; hr; }
note() { printf '\033[2m%s\033[0m\n' "$*"; }

COMPOSE="docker compose --progress quiet -f docker-compose.yml -f grpc/docker-compose.grpc.yml"
GRPCURL="$COMPOSE run --rm -T grpcurl -protoset /descriptors/ledger.pb"

if ! curl -sf http://localhost:19000/healthz >/dev/null; then
    echo "hoop-inspect is not healthy. Bring the overlay up first:" >&2
    echo "  ./run.sh && $COMPOSE up -d --wait" >&2
    exit 1
fi

# ------------------------------------------------------------- tier 1 (OPA)
h "TIER 1 / the fat gate answers reachability, now for RPCs"
note "gRPC is HTTP/2, so unlike the postgres lane Envoy is not blind here:"
note "the same ext_authz filter runs and OPA sees the method identity"
note "(:path /demo.v1.Ledger/GetInvoice), never the message. alice is"
note "granted ledger; bob is not."
note ""
note "  alice:"
$GRPCURL -insecure -H 'x-hoop-user: alice' -d '{"id":"INV-1001"}' \
    envoy:8444 demo.v1.Ledger/GetInvoice 2>&1 | sed 's/^/  /'
note ""
note "  bob (a 403 from OPA, before hoop-inspect or ledger see anything):"
$GRPCURL -insecure -H 'x-hoop-user: bob' -d '{"id":"INV-1001"}' \
    envoy:8444 demo.v1.Ledger/GetInvoice 2>&1 | sed 's/^/  /' | head -4

# ----------------------------------------------------------------- masking
h "MASKED / the response above was rewritten in flight"
note "ledger answered customer_email: ada@example.com; the client read"
note "[REDACTED:EMAIL_ADDRESS]. The emails rule is the process's ONE mask"
note "rule, inherited by this lane like the other two, and gRPC masking is"
note "re-encoding: the sidecar decodes the message against ledger.pb,"
note "rewrites the field, and rebuilds the length-prefixed frame. The ssn"
note "comes back in the clear because the rule that would cover it is over"
note "the one-rule limit (see ../sidecar/config.yaml)."

# --------------------------------------------------------------- guardrail
h "DENIED / a taxpayer id inside a request message"
note "no-cpf-in-query is the process's ONE guardrail rule, a top-level"
note "default, so the rule that refuses the pgwire DELETE and the HTTP query"
note "string refuses a protobuf field too. capture_payload renders the"
note "decoded message for the pii scan; the sidecar withholds the denied"
note "frame from ledger and answers PermissionDenied in the trailers."
note ""
$GRPCURL -insecure -H 'x-hoop-user: alice' \
    -d '{"id":"INV-1001","note":"customer cpf 111.444.777-35"}' \
    envoy:8444 demo.v1.Ledger/GetInvoice 2>&1 | sed 's/^/  /' | head -6

# ------------------------------------------------------------ without envoy
h "WITHOUT ENVOY / the same lane, direct, h2c"
note "The overlay publishes the lane on host port 18443, so this call skips"
note "TLS and the fat gate entirely: -plaintext, straight to hoop-inspect."
note "Policy, masking and the audit trail are identical -- the lane does not"
note "know whether Envoy exists. What is lost is exactly Envoy's half:"
note "nothing authenticated this caller, so the x-hoop-user header is a"
note "claim anyone on the network can make. From the host:"
note "  grpcurl -plaintext -protoset app/../ledger.pb localhost:18443 ..."
note ""
$GRPCURL -plaintext -H 'x-hoop-user: mallory' -d '{"id":"INV-2002"}' \
    hoop-inspect:18443 demo.v1.Ledger/GetInvoice 2>&1 | sed 's/^/  /'

# ------------------------------------------------- the rules not afforded
h "ALLOWED / the rules this build cannot afford"
note "ExportAll answers as shipped. no-bulk-export, the http_resource rule"
note "that would refuse it by method identity, sits commented out in"
note "grpc/config-grpc.yaml beside a grpc_status example -- over the"
note "one-guardrail limit no-cpf-in-query already spends."
note ""
$GRPCURL -insecure -H 'x-hoop-user: alice' -d '{}' \
    envoy:8444 demo.v1.Ledger/ExportAll 2>&1 | sed 's/^/  /'

# ------------------------------------------------------------------- audit
h "AUDIT / what hoop-inspect recorded"
sleep 1
$COMPOSE logs hoop-inspect --since 60s 2>/dev/null | ./sidecar/read-audit.py

h "Summary"
cat <<'EOF'
  Envoy saw:        TLS, a user header, ":path /demo.v1.Ledger/GetInvoice".
  OPA saw:          the same method identity -- reachability, tier 1.
  hoop-inspect saw: the decoded request and response messages, the taxpayer
                    id inside a protobuf field, and the email it redacted.
  ledger saw:       only what policy let through, already inspected.

  The direct :18443 call proves the lane stands alone: same verdicts, same
  masking, no Envoy. What the direct path gives up is identity -- the
  header is unverified -- which is why the base stack keeps data ports off
  the host and the lane's config marks identity_header as proxy-trust.
EOF
