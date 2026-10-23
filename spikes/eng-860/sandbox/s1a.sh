# ENG-860 S1a: restricted, allowed_domains = the lane host only. Vault:
# HOOP_CALLER_TOKEN and CUSTOMER_API_TOKEN, both bound to the lane host.
L={{LANE}} P={{PROBE}}
step() { printf '\n== %s\n' "$*"; }
PG="host=hh-pgsql-public.ebi.ac.uk port=5432 user=reader password=NWDMCE5xdipIjRrp dbname=pfmegrnargs connect_timeout=10"

step "c8 environment"; python3 /workspace/probe.py env
step "tools"; command -v curl psql python3; python3 --version

step "c1 GET /users/1 through the lane"
curl -sS -i --max-time 20 "https://$L/users/1" -H "X-Hoop-Token: $HOOP_CALLER_TOKEN" \
  -H "Authorization: Bearer $CUSTOMER_API_TOKEN" -H "X-OpenAI-Session-Id: $SESSION_ID"
step "c1 DELETE /users/1 through the lane"
curl -sS -i --max-time 20 -X DELETE "https://$L/users/1" -H "X-Hoop-Token: $HOOP_CALLER_TOKEN" \
  -H "X-OpenAI-Session-Id: $SESSION_ID"
step "c1 GET without X-Hoop-Token"
curl -sS -i --max-time 20 "https://$L/users/1" -H "X-OpenAI-Session-Id: $SESSION_ID"

step "c3 placeholders in headers, URL and body (the probe shows a filled secret as <REAL:name>)"
curl -g -sS --max-time 20 "https://$L/echo?t=$CUSTOMER_API_TOKEN" -H "X-Hoop-Token: $HOOP_CALLER_TOKEN" \
  -H "Authorization: Bearer $CUSTOMER_API_TOKEN" -H "X-Probe-Raw: $CUSTOMER_API_TOKEN" \
  -H "X-Probe-Mid: a-${CUSTOMER_API_TOKEN}-b" -H "X-OpenAI-Session-Id: $SESSION_ID" \
  --data-binary "token=$CUSTOMER_API_TOKEN"

step "c1 hosts outside allowed_domains"
for h in "$P" example.com postman-echo.com; do
  curl -sS -o /dev/null -w "$h http=%{http_code}\n" --max-time 15 "https://$h/"
  python3 /workspace/probe.py tcp "$h" 443
done
step "c1 and c3: the lane host on port 80, with the caller token (the vault fills 443 and 8443 only)"
curl -sS -i --max-time 15 "http://$L/users/1" -H "X-Hoop-Token: $HOOP_CALLER_TOKEN" \
  -H "X-OpenAI-Session-Id: $SESSION_ID" | grep -iE '^HTTP|^server|^x-hoop|^request|^[{]|email'
step "c3 curl without -g rejects a placeholder in a URL"
curl -sS -o /dev/null --max-time 10 "https://$L/echo?t=$CUSTOMER_API_TOKEN"

step "c4 TLS seen by the sandbox"
python3 /workspace/probe.py tls "$L"
curl -sS -v -o /dev/null --max-time 20 "https://$L/users/1" 2>&1 | grep -iE 'issuer|subject:|SSL connection|ALPN|^< HTTP|proxy'

step "c5 WebSocket upgrade through the lane"
python3 /workspace/probe.py ws "$L" /ws "X-Hoop-Token: $HOOP_CALLER_TOKEN" "X-OpenAI-Session-Id: $SESSION_ID"
step "c5 HTTP CONNECT inside TLS to the lane host"
python3 /workspace/probe.py connect "$L" example.com:443

step "c2 raw TCP to a public Postgres (not in allowed_domains)"
python3 /workspace/probe.py tcp hh-pgsql-public.ebi.ac.uk 5432
command -v psql >/dev/null && psql "$PG" -Atc 'select 1' 2>&1
step "end"
