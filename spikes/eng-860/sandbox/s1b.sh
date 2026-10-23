# ENG-860 S1b: restricted, allowed_domains = the lane, the probe host,
# postman-echo.com and the public Postgres host. Same vault as S1a.
L={{LANE}} P={{PROBE}}
step() { printf '\n== %s\n' "$*"; }
PG="host=hh-pgsql-public.ebi.ac.uk port=5432 user=reader password=NWDMCE5xdipIjRrp dbname=pfmegrnargs connect_timeout=10"

step "c3 the same headers toward another allowed host (the probe, not the lane)"
curl -sS --max-time 20 "https://$P/echo" -H "X-Hoop-Token: $HOOP_CALLER_TOKEN" \
  -H "Authorization: Bearer $CUSTOMER_API_TOKEN" -H "X-OpenAI-Session-Id: $SESSION_ID"

step "c4 TLS toward a public host outside Cloudflare"
python3 /workspace/probe.py tls postman-echo.com
curl -sS -o /dev/null -w "postman-echo http=%{http_code}\n" --max-time 15 https://postman-echo.com/get
python3 /workspace/probe.py tls "$P"

step "c2 raw TCP to a public Postgres in allowed_domains"
python3 /workspace/probe.py tcp hh-pgsql-public.ebi.ac.uk 5432
command -v psql >/dev/null && psql "$PG" -Atc 'select 1' 2>&1

step "c1 a host outside allowed_domains"
curl -sS -o /dev/null -w "example.com http=%{http_code}\n" --max-time 15 https://example.com/
step "c1 the lane"
curl -sS -o /dev/null -w "lane http=%{http_code}\n" --max-time 20 "https://$L/users/1" \
  -H "X-Hoop-Token: $HOOP_CALLER_TOKEN" -H "X-OpenAI-Session-Id: $SESSION_ID"
step "end"
