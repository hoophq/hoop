#!/usr/bin/env bash
#
# Pulls one large ClickHouse HTTP result through the sidecar and proves it
# arrived whole.
#
#   client ──TLS──> envoy:8448 ──OPA──> hoop-inspect :18124 ──http──> clickhouse:8123
#
# The same query runs twice: once straight to clickhouse:8123 (the
# baseline), once through the unmasked lane. The run passes when the
# relayed copy has HTTP 200, curl exit 0, the baseline's byte count and
# sha256, no error row in the audit trail, and the sidecar's memory stays
# under MAX_SIDECAR_MIB the whole time.
#
# Each TSV row is 1024 bytes, so SIZE_GIB GiB is exact. The default, 5 GiB,
# is past 4 GiB: a 32-bit byte counter anywhere on the path would wrap.
#
# Prereqs, from the envoy-stack directory: certs minted by ./run.sh. The
# script brings the ClickHouse overlay up itself and leaves it running.
#
# Env:
#   SIZE_GIB         result size in GiB              default 5
#   FORMAT           TSV | RowBinary                 default TSV
#   COMPRESSION      none | gzip                     default none
#   MAX_SIDECAR_MIB  sidecar memory ceiling          default 512
#   SIDECAR_IMAGE    published image to test, e.g. hoophq/hoopsidecar:1.285.2;
#                    unset runs the stack's local build
#   HOOP_LICENSE_FILE  license for SIDECAR_IMAGE: a published image runs
#                    standalone only with one

set -euo pipefail
cd "$(dirname "$0")/.."

SIZE_GIB=${SIZE_GIB:-5}
FORMAT=${FORMAT:-TSV}
COMPRESSION=${COMPRESSION:-none}
MAX_SIDECAR_MIB=${MAX_SIDECAR_MIB:-512}
LANE=clickhouse-http-stream

h()    { printf '\n\033[1;36m%s\033[0m\n' "$*"; }
note() { printf '  %s\n' "$*"; }
die()  { printf '\033[31mFAIL\033[0m %s\n' "$*" >&2; exit 1; }

[[ "$SIZE_GIB" =~ ^[1-9][0-9]*$ ]] || die "SIZE_GIB must be a positive integer: $SIZE_GIB"
case "$FORMAT" in TSV|RowBinary) ;; *) die "FORMAT must be TSV or RowBinary: $FORMAT" ;; esac
case "$COMPRESSION" in none|gzip) ;; *) die "COMPRESSION must be none or gzip: $COMPRESSION" ;; esac
[[ "$MAX_SIDECAR_MIB" =~ ^[1-9][0-9]*$ ]] || die "MAX_SIDECAR_MIB must be a positive integer: $MAX_SIDECAR_MIB"
[[ -f envoy/certs/server.crt && -f upstream/certs/server.crt ]] || die "no certs: run ./run.sh once"

FILES=(-f docker-compose.yml -f clickhouse/docker-compose.clickhouse.yml)
if [[ -n "${SIDECAR_IMAGE:-}" ]]; then
    [[ -f "${HOOP_LICENSE_FILE:-}" ]] || die "SIDECAR_IMAGE needs HOOP_LICENSE_FILE"
    export HOOP_LICENSE_FILE
    FILES+=(-f clickhouse/docker-compose.release.yml)
fi
COMPOSE=(docker compose --progress quiet "${FILES[@]}")

h "Stack"
"${COMPOSE[@]}" up -d --wait
CID=$("${COMPOSE[@]}" ps -q hoop-inspect)
note "sidecar image: $(docker inspect -f '{{.Config.Image}}' "$CID")"

ROWS=$((SIZE_GIB * 1024 * 1024))
# 19 + tab + 1003 + newline = 1024 bytes per TSV row.
SQL="SELECT leftPad(toString(number), 19, '0') AS n, repeat('x', 1003) AS pad FROM numbers($ROWS) FORMAT $FORMAT"
QS=""
ENC=()
if [[ "$COMPRESSION" == gzip ]]; then
    QS="&enable_http_compression=1"
    ENC=(-H 'Accept-Encoding: gzip' --compressed)
fi
# ClickHouse echoes query_id in X-ClickHouse-Query-Id, which the lane records
# on the response row: that names the audit session of THIS request.
QID="stream-$(date +%s)-$$"

# fetch URL [curl args...]: runs curl in the client container and prints one
# line: status, curl exit, wire bytes, decoded bytes, sha256, seconds and the
# Content-Encoding the server chose.
fetch() {
    local url=$1; shift
    "${COMPOSE[@]}" exec -T client sh -c '
        url=$1; sql=$2; shift 2
        rm -f /tmp/f /tmp/n /tmp/h /tmp/w; mkfifo /tmp/f
        wc -c </tmp/f >/tmp/n &
        # The body goes to stdout, -w to stderr, curl exit after it.
        sum=$( { curl -sk -D /tmp/h -o - \
                    -w "%{stderr}%{http_code} %{size_download} %{time_total}" \
                    "$@" --data-binary "$sql" "$url" 2>/tmp/w
                 echo " $?" >>/tmp/w; } | tee /tmp/f | sha256sum | cut -d" " -f1 )
        wait
        set -- $(cat /tmp/w)
        enc=$(grep -i "^content-encoding:" /tmp/h | tr -d "\r" | cut -d" " -f2)
        echo "status=$1 exit=$4 wire=$2 bytes=$(tr -d " " </tmp/n) sha256=$sum secs=$3 encoding=${enc:-identity}"
    ' sh "$url" "$SQL" "$@" -H X-ClickHouse-User:appuser -H X-ClickHouse-Key:apppass ${ENC[@]+"${ENC[@]}"}
}

field() { [[ " $2 " =~ \ $1=([^ ]*) ]] && printf '%s' "${BASH_REMATCH[1]}"; }

h "Baseline: $SIZE_GIB GiB $FORMAT ($COMPRESSION) straight from clickhouse:8123"
BASE=$(fetch "http://clickhouse:8123/?query_id=$QID-direct$QS")
note "$BASE"
[[ "$(field status "$BASE")" == 200 && "$(field exit "$BASE")" == 0 ]] || die "baseline query failed"
if [[ "$FORMAT" == TSV ]]; then
    [[ "$(field bytes "$BASE")" == $((SIZE_GIB << 30)) ]] || die "baseline is not $SIZE_GIB GiB"
fi

h "Relayed: the same query through envoy:8448 and lane $LANE"
MEMLOG=$(mktemp)
STOP="$MEMLOG.stop"
SAMPLER=""
# Stop the sampler on every exit path: it checks STOP between samples, so the
# wait also lets its last docker stats finish before MEMLOG is read or removed.
stop_sampler() {
    [[ -n "$SAMPLER" ]] || return 0
    touch "$STOP"; wait "$SAMPLER" 2>/dev/null || true; SAMPLER=""
}
trap 'stop_sampler; rm -f "$MEMLOG" "$STOP"' EXIT
( while [[ ! -e "$STOP" ]]; do
      docker stats --no-stream --format '{{.MemUsage}}' "$CID" 2>/dev/null \
          | awk '{ v=$1; u=v; gsub(/[0-9.]/, "", u); sub(/[A-Za-z]+$/, "", v);
                   f = u=="GiB" ? 1024 : u=="KiB" ? 1/1024 : u=="B" ? 1/1048576 : 1;
                   printf "%.1f\n", v*f }' >>"$MEMLOG"
  done ) &
SAMPLER=$!
START=$(date -u +%Y-%m-%dT%H:%M:%SZ)
RELAY=$(fetch "https://envoy:8448/?query_id=$QID$QS" -H 'X-Hoop-User: alice')
stop_sampler
note "$RELAY"
PEAK=$(sort -n "$MEMLOG" | tail -1)
note "sidecar memory: peak ${PEAK:-?} MiB over $(wc -l <"$MEMLOG" | tr -d ' ') samples"

h "Audit: lane $LANE, query_id $QID"
# A heredoc on python3 would take its stdin from the logs pipe, so the
# program goes in with -c. The session is the one whose request carried
# QID in its query string; only that session's rows count.
AUDIT_PY=$(cat <<'PY'
import json, sys
lane, qid = sys.argv[1], sys.argv[2]
rows = []
for line in sys.stdin:
    i = line.find('{"kind"')
    if i < 0:
        continue
    try:
        e = json.loads(line[i:])
    except json.JSONDecodeError:
        continue
    if e.get("connection") == lane:
        rows.append(e)
sessions = {e.get("session_id") for e in rows
            if e.get("kind") == "statement" and qid in json.dumps(e.get("http") or {})}
rows = [e for e in rows if e.get("session_id") in sessions]
kinds = lambda k: [e for e in rows if e.get("kind") == k]
errors = [e.get("error", "") for e in kinds("error")]
statuses = [str((e.get("http") or {}).get("status_code", "")) for e in kinds("statement")]
denied = sum(1 for e in kinds("statement") if not e.get("allowed"))
print(f"sessions={len(sessions)} rows={len(rows)} ended={len(kinds('session_end'))} errors={len(errors)} denied={denied} statuses={','.join(s for s in statuses if s) or '-'}")
for err in errors:
    print(f"error: {err}")
PY
)
# An error row lands when the session closes, and Envoy closes its pooled
# upstream connection some seconds after the response, so wait for that
# session's own session_end.
for _ in $(seq 1 45); do
    AUDIT=$("${COMPOSE[@]}" logs --no-log-prefix hoop-inspect --since "$START" 2>/dev/null \
            | python3 -c "$AUDIT_PY" "$LANE" "$QID")
    [[ "$AUDIT" == *" ended=0 "* ]] || break
    sleep 2
done
note "$AUDIT"

h "Verdict"
fail=0
check() { if eval "$2"; then note "ok    $1"; else note "FAIL  $1"; fail=1; fi; }
check "HTTP 200"                     '[[ "$(field status "$RELAY")" == 200 ]]'
check "curl exit 0 (no cut stream)"  '[[ "$(field exit "$RELAY")" == 0 ]]'
check "same byte count as baseline"  '[[ "$(field bytes "$RELAY")" == "$(field bytes "$BASE")" ]]'
check "same sha256 as baseline"      '[[ "$(field sha256 "$RELAY")" == "$(field sha256 "$BASE")" ]]'
check "audit names the request"      '[[ "$AUDIT" == sessions=1\ * ]]'
check "session closed in the audit"  '[[ "$AUDIT" != *" ended=0 "* ]]'
check "no error row in the audit"    '[[ "$AUDIT" == *" errors=0 "* && "$AUDIT" == *" denied=0 "* ]]'
check "sidecar peak memory < ${MAX_SIDECAR_MIB} MiB" '[[ -n "$PEAK" ]] && awk -v p="$PEAK" -v m="$MAX_SIDECAR_MIB" "BEGIN{exit !(p+0 < m+0)}"'
[[ $fail == 0 ]] || die "$SIZE_GIB GiB $FORMAT ($COMPRESSION) did not stream whole"
printf '\033[32mPASS\033[0m %s GiB %s (%s) streamed whole through %s\n' "$SIZE_GIB" "$FORMAT" "$COMPRESSION" "$LANE"
