# ENG-880 c3, c4, c5: what the microVM reaches. One InvokeAgentRuntimeCommand.
H=${SIDECAR_HOST:-sidecar.eng880.internal}
try() { # try <label> <cmd...>: one line, exit code and the last line of output
    local o; o=$("${@:2}" 2>&1); printf '%-34s exit=%-3s %s\n' "$1" "$?" "$(printf '%s' "$o" | tail -1 | cut -c1-150)"
}
http() { curl -sS -m 8 -o /dev/null -w '%{http_code} %{remote_ip} %{time_total}s' "$@"; }

echo "== otel and runtime endpoints"
env | grep -E '^(AGENTCORE_RUNTIME_URL|OTEL_EXPORTER_OTLP_(LOGS|TRACES)_ENDPOINT|OTEL_RESOURCE_ATTRIBUTES)=' | cut -c1-200

echo "== dns"
for n in "$H" db.eng880.internal probe.eng880.internal example.com api.openai.com \
         bedrock-runtime.us-east-1.amazonaws.com bedrock-agentcore.us-east-1.amazonaws.com sts.amazonaws.com; do
    try "getent $n" getent ahosts "$n"
done
try "dig example.com @127.0.0.2" dig +short +time=3 +tries=1 example.com
try "dig TXT eng880.test (NX)" dig +time=3 +tries=1 TXT eng880-c5-$RANDOM.example.com

echo "== c3 lane (demo API)"
curl -sS -m 10 -i "https://$H:8443/users/1" -H "X-Hoop-Token: $HOOP_CALLER_TOKEN" -H "X-Hoop-Session-Id: ${1:-none}" 2>&1 | grep -E '^HTTP|email|phone|card' | head -4
curl -sS -m 10 -i -X DELETE "https://$H:8443/users/1" -H "X-Hoop-Token: $HOOP_CALLER_TOKEN" -H "X-Hoop-Session-Id: ${1:-none}" 2>&1 | grep -iE '^HTTP|x-hoop-denied|only read' | head -3
curl -sS -m 10 -i "https://$H:8443/users/1" 2>&1 | grep -E '^HTTP|refused' | head -2

echo "== c3 outside the lane"
try "https example.com" http https://example.com
try "https 1.1.1.1 (IP)" http https://1.1.1.1
try "https api.openai.com" http https://api.openai.com/v1/models
try "probe direct :8088" http http://probe.eng880.internal:8088/users
try "IPv6 [2606:4700:4700::1111]" http -6 https://[2606:4700:4700::1111]
try "IPv6 ipv6.google.com" http -6 https://ipv6.google.com
try "bedrock-agentcore (no endpoint)" http https://bedrock-agentcore.us-east-1.amazonaws.com/ping
try "sts global" http https://sts.amazonaws.com

echo "== c4 postgres"
try "psql lane select" psql "host=$H port=15432 dbname=appdb user=agent password=agent-demo-password connect_timeout=8" -Atc 'select id, email from customers order by id limit 2'
try "psql lane delete" psql "host=$H port=15432 dbname=appdb user=agent password=agent-demo-password connect_timeout=8" -Atc 'delete from customers'
try "psql direct db:5432" psql "host=db.eng880.internal port=5432 dbname=appdb user=agent password=agent-demo-password connect_timeout=8" -Atc 'select 1'
