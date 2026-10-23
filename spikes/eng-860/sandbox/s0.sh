# ENG-860 S0: network enabled, no vault. Recon and the c2 "enabled" half.
L={{LANE}}
step() { printf '\n== %s\n' "$*"; }
PG="host=hh-pgsql-public.ebi.ac.uk port=5432 user=reader password=NWDMCE5xdipIjRrp dbname=pfmegrnargs connect_timeout=10"

step "c8 environment"; python3 /workspace/probe.py env
step "tools"; command -v curl psql python3; python3 --version; uname -m

step "c2 raw TCP to a public Postgres, network enabled"
python3 /workspace/probe.py tcp hh-pgsql-public.ebi.ac.uk 5432
command -v psql >/dev/null && psql "$PG" -Atc 'select 1' 2>&1

step "c4 TLS with network enabled"
python3 /workspace/probe.py tls example.com
python3 /workspace/probe.py tls "$L"
curl -sS -v -o /dev/null --max-time 20 https://example.com/ 2>&1 | grep -iE 'issuer|subject:|SSL connection|^< HTTP|CONNECT|proxy'

step "c5 WebSocket and CONNECT, network enabled"
python3 /workspace/probe.py ws ws.postman-echo.com /raw
python3 /workspace/probe.py connect "$L" example.com:443

step "egress IP (the probe logs the source)"
curl -sS --max-time 15 "https://{{PROBE}}/whoami-from-s0" | grep -E '"src_ip"|"user_agent"'
step "end"
