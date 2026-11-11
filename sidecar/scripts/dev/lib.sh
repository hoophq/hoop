#!/usr/bin/env bash
#
# Shared settings and output helpers. Sourced by every script here.

# Colours match deploy/docker-compose/envoy-stack/demo.sh, so the two read the
# same way when run back to back.
hr()   { printf '\033[2m%s\033[0m\n' "----------------------------------------------------------------"; }
h()    { printf '\n\033[1;36m%s\033[0m\n' "$*"; hr; }
note() { printf '\033[2m%s\033[0m\n' "$*"; }
ok()   { printf '  \033[32mok\033[0m    %s\n' "$*"; }
bad()  { printf '  \033[31mFAIL\033[0m  %s\n' "$*"; }
die()  { printf '\n\033[31m%s\033[0m\n' "$*" >&2; exit 1; }

# One provider config for every script, so a change is made once.
export HOOP_PROJECT="${HOOP_PROJECT:-}"
export HOOP_REGION="${HOOP_REGION:-global}"
export HOOP_MODEL="${HOOP_MODEL:-claude-sonnet-4-5@20250929}"

# Ports. Deliberately not 5432/8080: a laptop usually has something there, and
# a test that silently talks to your real database is worse than one that
# fails to start.
export PG_UPSTREAM_PORT="${PG_UPSTREAM_PORT:-55432}"
export HTTP_UPSTREAM_PORT="${HTTP_UPSTREAM_PORT:-58080}"
export PG_RELAY_PORT="${PG_RELAY_PORT:-15432}"
export HTTP_RELAY_PORT="${HTTP_RELAY_PORT:-18080}"
export ADMIN_PORT="${ADMIN_PORT:-19000}"

# Everything this suite writes lands here, including the relay log and the
# audit stream, so a failed run leaves evidence to read.
export WORKDIR="${WORKDIR:-/tmp/hoop-inspect-vertex}"
mkdir -p "$WORKDIR"

export CONFIG="$WORKDIR/config.yaml"
export RELAY_LOG="$WORKDIR/relay.log"
export RELAY_PID="$WORKDIR/relay.pid"
export BIN="$WORKDIR/hoop-inspect"

export PGC="env PGPASSWORD=testpass PGSSLMODE=disable psql -h 127.0.0.1 -p ${PG_RELAY_PORT} -U testuser -d appdb"

# admin GETs a path off the relay's admin listener.
admin() { curl -sf "http://127.0.0.1:${ADMIN_PORT}$1"; }

# jq is optional; python3 is already required for the JSON the relay emits.
pyq() { python3 -c "$1"; }

# seed_postgres <container> — create and populate the test table.
#
# Retries until a verification query succeeds. The official postgres image
# starts a temporary server for its init scripts and then restarts, so
# pg_isready can report ready against a server that is about to go away. A
# seed that lands in that window disappears, and the symptom is a later query
# failing with "relation does not exist" long after startup looked clean.
seed_postgres() {
    local c="$1" i
    for i in $(seq 1 30); do
        docker exec -i "$c" psql -U testuser -d appdb -v ON_ERROR_STOP=1 >/dev/null 2>&1 <<'SQL'
CREATE TABLE IF NOT EXISTS customers(
    id serial primary key, name text, email text, ssn text);
TRUNCATE customers;
INSERT INTO customers(name, email, ssn) VALUES
    ('Ada Lovelace', 'ada@example.com',   '123-45-6789'),
    ('Grace Hopper', 'grace@example.com', '987-65-4321');
SQL
        if [[ "$(docker exec "$c" psql -U testuser -d appdb -tAc \
                 'SELECT count(*) FROM customers;' 2>/dev/null | tr -d '[:space:]')" == "2" ]]; then
            return 0
        fi
        sleep 1
    done
    return 1
}
