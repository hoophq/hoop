#!/usr/bin/env bash
#
# Walks the two tiers end to end and prints the evidence for each. Run after
# ./run.sh (and ./creds.sh for the postgres lane).
#
# The point of the script is the contrast:
#   tier 1  Envoy + OPA decide reachability. Fast, coarse, InfoSec owns it.
#   tier 2  hoop decides on statement CONTENT and records who ran what.
#           Envoy structurally cannot do this -- it has no pgwire parser.

set -uo pipefail
cd "$(dirname "$0")"

API=http://localhost:8009/api
hr()  { printf '\033[2m%s\033[0m\n' "----------------------------------------------------------------"; }
h()   { printf '\n\033[1;36m%s\033[0m\n'  "$*"; hr; }
note(){ printf '\033[2m%s\033[0m\n' "$*"; }

h "TIER 1 / allow -- alice is granted httpbin; OPA injects her hoop token"
curl -sk https://localhost:8443/json -H 'X-Citadel-User: alice' \
    -o /tmp/poc-alice.json -w 'HTTP %{http_code}\n'
note "$(head -c 120 /tmp/poc-alice.json 2>/dev/null)..."

h "TIER 1 / deny -- bob has no grant. Never reaches hoop."
curl -sk https://localhost:8443/json -H 'X-Citadel-User: bob' -w '\nHTTP %{http_code}\n'

h "TIER 1 / deny -- no identity at all"
curl -sk https://localhost:8443/json -w '\nHTTP %{http_code}\n'

if [[ ! -f .pgtoken || ! -f .sshtoken ]]; then
    h "TIER 2 -- skipped"
    note "run ./creds.sh first to mint the postgres and ssh credentials"
    exit 0
fi
PGT=$(cat .pgtoken)
SSHT=$(cat .sshtoken)

pg()  { docker compose exec -T client \
          env PGPASSWORD=hoop PGSSLMODE=disable \
          psql -h envoy -p 5432 -U "$PGT" -d appdb "$@" 2>&1; }
# No connection name on the command line: the credential is already bound to
# 'appserver' (sshproxy.go:278 carries it in the SSH permission extensions).
ssh_() { docker compose exec -T client \
          sshpass -p "$SSHT" ssh -o StrictHostKeyChecking=no \
          -o LogLevel=ERROR -p 2222 hoop@envoy "$@" 2>&1; }

h "TIER 2 / postgres SELECT -- traverses Envoy's TCP listener"
pg -c 'SELECT name, email FROM customers;'

h "TIER 2 / postgres DELETE -- same listener, same port, same posture"
pg -c 'DELETE FROM customers WHERE id = 3;'
note "Envoy logged this as N bytes on a TCP stream. It cannot tell the two"
note "statements apart: there is no pgwire parser in the filter chain."

h "TIER 2 / ssh -- Envoy has no SSH filter at all, so it is fully blind"
ssh_ 'whoami; head -1 /etc/os-release; ls /'
note "OPA was never consulted on this session: there is nothing in Envoy that"
note "could parse an SSH channel, let alone the command inside it. hoop"
note "terminated the protocol and recorded both directions"
note "(audit.go:316-319 -- SSHConnectionWrite in and out)."

# Sessions flush their WAL to private.blobs asynchronously on close; give the
# gateway a beat before reading, or the newest statements are not there yet.
sleep 3

h "TIER 2 / evidence -- what hoop recorded"
docker compose exec -T db psql -U postgres -d hoopdb -x -c "
  SELECT user_email, connection, connection_subtype, verb, created_at
    FROM private.sessions
   WHERE connection IN ('appdb', 'appserver')
   ORDER BY created_at DESC LIMIT 3;" 2>&1 | grep -vE '^$'

note ""
note "The SQL statements, recovered from the recorded pgwire stream:"
docker compose exec -T db psql -U postgres -d hoopdb -tAc "
  SELECT blob_stream::text FROM private.blobs
   ORDER BY created_at DESC LIMIT 12;" 2>/dev/null \
  | grep -oE '[A-Za-z0-9+/]{16,}={0,2}' \
  | while read -r b64; do
        txt=$(printf '%s' "$b64" | base64 -d 2>/dev/null | tr -cd '\11\12\15\40-\176')
        case "$txt" in
            *SELECT*|*DELETE*|*INSERT*|*UPDATE*|*DROP*)
                # Drop the leading pgwire frame bytes (a 'Q' tag plus a binary
                # length prefix) so the statement reads as the user typed it.
                printf '%s\n' "$txt" \
                  | sed -E 's/.*(SELECT|DELETE|INSERT|UPDATE|DROP)/  \1/' ;;
        esac
    done | sort -u

note ""
note "The SSH session, from the same audit store:"
SSH_BLOB=$(docker compose exec -T db psql -U postgres -d hoopdb -tAc "
  SELECT coalesce(length(b.blob_stream::text), 0)
    FROM private.sessions s
    LEFT JOIN private.blobs b ON b.id = s.blob_input_id
   WHERE s.connection = 'appserver'
   ORDER BY s.created_at DESC LIMIT 1;" 2>/dev/null | tr -d '[:space:]')

if [[ "${SSH_BLOB:-0}" -gt 8 ]]; then
    docker compose exec -T db psql -U postgres -d hoopdb -tAc "
      SELECT b.blob_stream::text
        FROM private.sessions s
        JOIN private.blobs b ON b.id = s.blob_input_id
       WHERE s.connection = 'appserver'
       ORDER BY s.created_at DESC LIMIT 2;" 2>/dev/null \
      | grep -oE '[A-Za-z0-9+/]{12,}={0,2}' \
      | while read -r b64; do
            txt=$(printf '%s' "$b64" | base64 -d 2>/dev/null | tr -cd '\40-\176')
            case "$txt" in
                *whoami*|*appops*|*PRETTY_NAME*|*os-release*)
                    printf '  %s\n' "$txt" ;;
            esac
        done | sort -u | head -6
else
    note "  (payload blob empty on this image -- see README 'Known gaps'.)"
    note "  The session itself IS recorded and attributed: subtype ssh, user"
    note "  admin@hoop.dev, connection appserver, above. The gateway logged"
    note "  the full channel lifecycle:"
    docker compose logs gateway --since 5m 2>&1 \
      | grep -oE 'msg":"(ssh connection attempt|obtained access by id|received new channel)[^"]*' \
      | sed 's/^msg":"/    /' | tail -3
fi

h "Summary"
cat <<'EOF'
  Envoy saw:  HTTPS -> a request line and headers.
              postgres, ssh -> a byte count on a TCP stream. Nothing else.
  OPA saw:    method, path, headers on the HTTP lane only. It was never
              consulted for postgres or ssh -- there was nothing to consult
              it about.
  hoop saw:   the SQL statement verbatim, the SSH channel lifecycle, and
              which human ran each one.

  Tier 1 is config. Tier 2 is the part that is not config -- and on the SSH
  lane Envoy has no filter at any fidelity, so hoop is the only layer that
  can see anything at all.
EOF
