#!/bin/bash
# usage: record-side.sh <side> <psql-dsn> <vite-url> <token-file> <gateway-db> <cdp-port> <out-dir>
# Holds a real psql on the sidecar listener in <psql-dsn>, then records the
# approver rejecting it. cors_files.py must serve <out-dir>/live on port 18990.
set -euo pipefail
side=$1; dsn=$2; vite=$3; token=$4; gwdb=$5; cdp=$6; dir=$7
here=$(cd "$(dirname "$0")" && pwd); skill=$(cd "$here/../.." && pwd)
mkdir -p "$dir/live"; out=$dir/live/$side.out
echo '$ psql -c "DELETE FROM items WHERE id = 7"' > "$out"
psql "$dsn" -c "DELETE FROM items WHERE id = 7" >> "$out" 2>&1 &
held=$!
sleep 4
# The newest pending sidecar review is the one this psql filed.
sid=$(docker exec hoopdevpg psql -U hoopdevuser -d "$gwdb" -Atc \
  "select session_id from private.reviews where sidecar_id is not null and status = 'PENDING' order by created_at desc limit 1")
REVIEW_SESSION=$sid PSQL_OUT_URL=http://127.0.0.1:18990/$side.out \
  node "$skill/rec.mjs" "$vite" "$token" "$here/reject-live.flow.mjs" "$side" "$dir" "$cdp"
wait $held || true
