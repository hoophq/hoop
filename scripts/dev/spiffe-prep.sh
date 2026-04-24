#!/bin/bash
# spiffe-prep.sh — mint a local-dev SPIFFE bundle/JWT and ensure .env
# points the gateway at them. Safe to run repeatedly; re-running rotates
# the JWT but keeps the signing key (and therefore the bundle) stable.
#
# After running this, `make run-dev` will start the gateway with
# SPIFFE validation enabled (enforce mode).
set -eo pipefail

SPIFFE_DIR="${SPIFFE_DIR:-dist/dev/spiffe}"
TRUST_DOMAIN="${HOOP_SPIFFE_TRUST_DOMAIN:-local.test}"
SPIFFE_ID="${HOOP_SPIFFE_ID:-spiffe://local.test/agent/local-dev}"
AUDIENCE="${HOOP_SPIFFE_AUDIENCE:-http://127.0.0.1:8009}"
TTL="${HOOP_SPIFFE_TTL:-24h}"

if [[ ! -f .env ]]; then
  echo "missing .env file (copy .env.sample first)"
  exit 1
fi

mkdir -p "$SPIFFE_DIR"

echo "==> minting SPIFFE artifacts under $SPIFFE_DIR"
(cd gateway && go run ./cmd/spiffe-mint \
  -out "../$SPIFFE_DIR" \
  -trust-domain "$TRUST_DOMAIN" \
  -spiffe-id "$SPIFFE_ID" \
  -audience "$AUDIENCE" \
  -ttl "$TTL")

# Embed the bundle directly into .env as base64. The gateway accepts
# HOOP_SPIFFE_BUNDLE_JWKS as either raw JSON or base64-encoded JWKS JSON;
# base64 is used here so the env var stays on a single line and survives
# whatever shell/editor the operator is using. spiffe-mint reuses the
# signing key across invocations, so this value is stable across reruns
# (only the JWT rotates), which means no gateway restart is needed to
# pick up fresh JWTs.
BUNDLE_B64=$(base64 < "$SPIFFE_DIR/bundle.jwks" | tr -d '\n')

# Idempotently update .env. We manage a marker block so prep can be
# re-run without duplicating entries; anything the user adds outside
# the markers is preserved.
BEGIN='# <<<HOOP_SPIFFE_DEV>>>'
END='# <<</HOOP_SPIFFE_DEV>>>'

TMP="$(mktemp)"
awk -v b="$BEGIN" -v e="$END" '
  $0==b { skip=1; next }
  $0==e { skip=0; next }
  !skip { print }
' .env > "$TMP"

cat >> "$TMP" <<EOF
$BEGIN
HOOP_SPIFFE_MODE=enforce
HOOP_SPIFFE_TRUST_DOMAIN=$TRUST_DOMAIN
HOOP_SPIFFE_BUNDLE_JWKS=$BUNDLE_B64
HOOP_SPIFFE_AUDIENCE=$AUDIENCE
HOOP_SPIFFE_REFRESH_PERIOD=30s
$END
EOF

mv "$TMP" .env

echo "==> .env updated with HOOP_SPIFFE_* (enforce mode, bundle inline as env)"
echo
echo "next steps:"
echo "  1. make run-dev              # starts gateway with SPIFFE validation"
echo "  2. make run-dev-spiffe-agent # starts host-side agent using the minted JWT"
