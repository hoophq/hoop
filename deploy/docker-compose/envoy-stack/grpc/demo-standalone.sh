#!/usr/bin/env bash
#
# Walks the standalone lane: hoop-inspect terminating TLS itself, no Envoy.
#
#   grpcurl ──TLS──> hoop-inspect:18443 ──h2c──> ledger:9000
#
# Prereqs, from this directory, with the envoy stack down:
#   docker compose -f docker-compose.standalone.yml up -d --wait --build
#
# The MASKED beat is an assertion, not a printout: the script exits
# non-zero if the email comes back in the clear.

set -uo pipefail
cd "$(dirname "$0")"

hr()   { printf '\033[2m%s\033[0m\n' "----------------------------------------------------------------"; }
h()    { printf '\n\033[1;36m%s\033[0m\n' "$*"; hr; }
note() { printf '\033[2m%s\033[0m\n' "$*"; }

COMPOSE="docker compose --progress quiet -f docker-compose.standalone.yml"
GRPCURL="$COMPOSE run --rm -T grpcurl -protoset /descriptors/ledger.pb"

if ! curl -sf http://localhost:19000/healthz >/dev/null; then
    echo "hoop-inspect is not healthy. Bring the standalone stack up first:" >&2
    echo "  $COMPOSE up -d --wait --build" >&2
    exit 1
fi

# ------------------------------------------------------------------- masked
h "MASKED / TLS to the lane, email redacted on the way back"
note "The lane owns the client's TLS (downstream_tls) and speaks h2c to"
note "ledger behind it. ledger answered customer_email: ada@example.com;"
note "what crosses the wire back is the re-encoded frame below. -insecure"
note "because the demo cert is self-signed; a real pair makes it plain TLS."
note ""
OUT=$($GRPCURL -insecure -d '{"id":"INV-1001"}' \
    hoop-inspect:18443 demo.v1.Ledger/GetInvoice 2>&1)
printf '%s\n' "$OUT" | sed 's/^/  /'
note ""
if printf '%s' "$OUT" | grep -q '"customerEmail": "\[REDACTED:EMAIL_ADDRESS\]"'; then
    printf '  \033[32mok\033[0m  customer_email masked\n'
else
    printf '  \033[1;31mFAIL: email returned in the clear\033[0m\n'
    exit 1
fi
if printf '%s' "$OUT" | grep -q 'ada@example.com'; then
    printf '  \033[1;31mFAIL: the clear value leaked to the client\033[0m\n'
    exit 1
fi

# ---------------------------------------------------------------- plaintext
h "PLAINTEXT / the dial dies before gRPC exists"
note "The h2c opt-in from the overlay does not exist here: the listener"
note "expects a TLS handshake and never answers a cleartext HTTP/2"
note "preface, so the client times out dialing. Compare config-grpc.yaml,"
note "where the same -plaintext dial is the without-Envoy path."
note ""
$GRPCURL -plaintext -max-time 5 -d '{"id":"INV-1001"}' \
    hoop-inspect:18443 demo.v1.Ledger/GetInvoice 2>&1 | sed 's/^/  /' | head -3

# ---------------------------------------------------------------- guardrail
h "DENIED / the guardrail is the same one, TLS or not"
note "no-cpf-in-query scans the decoded request message and answers"
note "PermissionDenied in the trailers; ledger never sees the frame."
note ""
$GRPCURL -insecure \
    -d '{"id":"INV-1001","note":"customer cpf 111.444.777-35"}' \
    hoop-inspect:18443 demo.v1.Ledger/GetInvoice 2>&1 | sed 's/^/  /' | head -6

# ------------------------------------------------------------------- audit
h "AUDIT / what the lane recorded"
sleep 1
$COMPOSE logs hoop-inspect --since 60s 2>/dev/null | ../sidecar/read-audit.py
note ""
note "principal is anonymous on every session: nothing authenticated the"
note "caller and the lane honors no identity header without a proxy in"
note "front. Client certificates are the standalone way to fill it."

h "Summary"
cat <<'EOF'
  One process owns the whole story here: TLS termination, decoding,
  policy, masking, audit. The email was redacted by re-encoding the
  protobuf frame; the taxpayer id was refused inside a message field;
  the plaintext dial never reached either code path.

  Front it with Envoy when something must own identity and routing --
  that is the grpc/ overlay. The lane's config barely changes: drop
  downstream_tls, add identity_header, and it is the same inspection.
EOF
