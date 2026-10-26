# ENG-880 c5: does the resolver answer public names (a channel out)?
for n in example.com api.openai.com "eng880-c5-$RANDOM.example.com" bedrock-runtime.us-east-1.amazonaws.com \
         sidecar.eng880.internal; do
    printf '%-44s ' "$n"; dig +time=3 +tries=1 "$n" | grep -E 'status:|^[a-z].*IN.*A' | head -2 | tr '\n' ' '; echo
done
echo "lane after the firewall:"; curl -sS -m 10 -o /dev/null -w '%{http_code}\n' "https://${SIDECAR_HOST:-sidecar.eng880.internal}:8443/users/1" -H "X-Hoop-Token: $HOOP_CALLER_TOKEN"
