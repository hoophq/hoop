# ENG-880: what the microVM is. Sent as one InvokeAgentRuntimeCommand.
echo "== id"; id; uname -srm; head -1 /etc/os-release
echo "== tools"; for t in curl psql python3 dig getent nc jq; do printf '%s=%s ' "$t" "$(command -v $t || echo -)"; done; echo
echo "== env names"; env | cut -d= -f1 | sort | tr '\n' ' '; echo
echo "== env AWS_* and *SESSION* (values cut to 12)"; env | grep -E '^AWS_|SESSION|AGENTCORE|BEDROCK' | sed -E 's/^([^=]+)=(.{0,12}).*/\1=\2.../'
echo "== resolv.conf"; cat /etc/resolv.conf
echo "== routes"; (ip route 2>/dev/null || cat /proc/net/route)
echo "== addrs"; (ip -4 addr 2>/dev/null || hostname -I)
echo "== pid1"; tr '\0' ' ' < /proc/1/cmdline; echo
echo "== session id in files"; grep -rls --exclude-dir={proc,sys,dev} "${1:-eng880-}" /etc /run /tmp /var/run /opt /root 2>/dev/null | head
echo "== mmds"; for u in http://169.254.169.254/latest/meta-data/ http://169.254.170.2/ ; do printf '%s -> ' "$u"; curl -sS -m 3 -o /dev/null -w '%{http_code}\n' "$u" 2>&1 | tail -1; done
echo "== mmds v2 token"; curl -sS -m 3 -X PUT -H 'X-metadata-token-ttl-seconds: 60' -o /dev/null -w '%{http_code}\n' http://169.254.169.254/latest/api/token 2>&1 | tail -1
