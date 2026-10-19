#!/usr/bin/env bash
#
# Walks grpc.discover: lanes that read their schema from the upstream over
# gRPC server reflection, with no descriptor set mounted into the sidecar.
#
# Prereqs, from this directory, with the envoy and standalone stacks down:
#   docker compose -f docker-compose.discover.yml up -d --wait --build
#
# The DISCOVERED, REFUSED and PINNED beats are assertions: the script exits
# non-zero when a lane does not answer the way its config says.

set -uo pipefail
cd "$(dirname "$0")"

hr()   { printf '\033[2m%s\033[0m\n' "----------------------------------------------------------------"; }
h()    { printf '\n\033[1;36m%s\033[0m\n' "$*"; hr; }
note() { printf '\033[2m%s\033[0m\n' "$*"; }
ok()   { printf '  \033[32mok\033[0m  %s\n' "$*"; }
fail() { printf '  \033[1;31mFAIL: %s\033[0m\n' "$*"; exit 1; }

COMPOSE="docker compose --progress quiet -f docker-compose.discover.yml"
GRPCURL="$COMPOSE run --rm -T grpcurl -plaintext -protoset /descriptors/ledger.pb -protoset /descriptors/reports.pb"
INSPECT="$COMPOSE exec -T hoop-inspect hoop-inspect -config /etc/hoop-inspect/config.yaml"

if ! curl -sf http://localhost:19000/healthz >/dev/null; then
    echo "hoop-inspect is not healthy. Bring the discover stack up first:" >&2
    echo "  $COMPOSE up -d --wait --build" >&2
    exit 1
fi

# Every lane makes its first attempt at startup; /readyz answers once each
# attempt ended, whatever it found.
for _ in $(seq 1 30); do
    [ "$(curl -s http://localhost:19000/readyz)" = "ready" ] && break
    sleep 1
done

# --------------------------------------------------------------- discovered
h "DISCOVERED / billing: no descriptor set, the email is still masked"
note "hoop-inspect mounts no .pb file. The billing lane fetched demo.v1.Ledger"
note "and reports.v1.Reports from ledger-reflect at startup, so it decodes the"
note "response and the inherited emails rule rewrites customer_email."
note ""
OUT=$($GRPCURL -d '{"id":"INV-1001"}' hoop-inspect:18443 demo.v1.Ledger/GetInvoice 2>&1)
printf '%s\n' "$OUT" | sed 's/^/  /'
note ""
printf '%s' "$OUT" | grep -q '"customerEmail": "\[REDACTED:EMAIL_ADDRESS\]"' || fail "customer_email not masked"
printf '%s' "$OUT" | grep -q 'ada@example.com' && fail "the clear value leaked to the client"
ok "customer_email masked with a discovered schema"
note ""
note "The guardrail reads decoded request fields too:"
$GRPCURL -d '{"id":"INV-1001","note":"customer cpf 111.444.777-35"}' \
    hoop-inspect:18443 demo.v1.Ledger/GetInvoice 2>&1 | sed 's/^/  /' | head -4

# ------------------------------------------------------------------ refused
h "REFUSED / legacy: reflection off, fail_open: false (the default)"
note "ledger serves no reflection. The lane still listens, refuses every RPC"
note "with UNAVAILABLE and the reason, and retries discovery up to every 30 s."
note "ledger never sees the call."
note ""
OUT=$($GRPCURL -d '{"id":"INV-1001"}' hoop-inspect:18444 demo.v1.Ledger/GetInvoice 2>&1)
printf '%s\n' "$OUT" | sed 's/^/  /'
printf '%s' "$OUT" | grep -q 'Unavailable' || fail "legacy did not refuse with UNAVAILABLE"
printf '%s' "$OUT" | grep -q 'exposes no gRPC server reflection' || fail "the refusal does not name the cause"
ok "refused, naming the missing reflection"

# ---------------------------------------------------------------- fail open
h "FAIL OPEN / legacy-open: reflection off, fail_open: true"
note "The call reaches ledger, so customer_email comes back in the clear: no"
note "schema, no masking. That is why startup refuses fail_open next to mask"
note "rules, and why this lane sets mask: {rules: []}. The audit records"
note "carry grpc.schema: unavailable."
note ""
$GRPCURL -d '{"id":"INV-1001"}' hoop-inspect:18445 demo.v1.Ledger/GetInvoice 2>&1 | sed 's/^/  /'

# ------------------------------------------------------------------- pinned
h "PINNED / pinned: the schema's digest is not the one in the config"
note "The lane refuses a schema it was not told to expect, so the service"
note "cannot rename a field out from under a mask rule."
note ""
OUT=$($GRPCURL -d '{"id":"INV-1001"}' hoop-inspect:18446 demo.v1.Ledger/GetInvoice 2>&1)
printf '%s\n' "$OUT" | sed 's/^/  /'
printf '%s' "$OUT" | grep -q 'not the pinned' || fail "pinned did not refuse the schema"
ok "refused, naming both digests"
note ""
note "-grpc-discover prints the digest to pin, from inside the container:"
$INSPECT -grpc-discover billing 2>&1 | grep -E 'serves|sha256' | sed 's/^/  /'
note "Paste it into config-discover.yaml (pinned.grpc.discover.sha256), then"
note "  $COMPOSE restart hoop-inspect"

# -------------------------------------------------------------------- audit
h "AUDIT / what the lanes recorded"
sleep 1
LOGS=$($COMPOSE logs hoop-inspect --since 120s 2>/dev/null)
printf '%s\n' "$LOGS" | ../sidecar/read-audit.py
note ""
note "rpc_refused records (legacy and pinned):"
printf '%s\n' "$LOGS" | grep -o '"connection":"[a-z-]*".*"activity":"rpc_refused","method":"[^"]*"' \
    | sed -E 's/"connection":"([a-z-]*)".*"method":"([^"]*)"/  \1  \2/'
note "audit records marked grpc.schema: unavailable (legacy-open):"
printf '  %s\n' "$(printf '%s\n' "$LOGS" | grep -c '"grpc.schema":"unavailable"')"

h "Summary"
cat <<'EOF'
  grpc.discover replaces grpc.descriptors when the upstream serves
  reflection: point the lane at the service and it decodes, masks and
  audits. Without reflection it refuses (or, with fail_open, forwards
  with method-level audit only); use descriptors for those services.
  Pin the digest in production.
EOF
