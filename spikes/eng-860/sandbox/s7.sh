# ENG-860 S7: a session that set only environment_template_id.
L={{LANE}}
step() { printf '\n== %s\n' "$*"; }
step "c7 vault placeholders present"; echo "HOOP_CALLER_TOKEN len=${#HOOP_CALLER_TOKEN} CUSTOMER_API_TOKEN len=${#CUSTOMER_API_TOKEN}"
step "c7 the lane"; curl -sS -o /dev/null -w "lane http=%{http_code}\n" --max-time 20 "https://$L/users/1" \
  -H "X-Hoop-Token: $HOOP_CALLER_TOKEN" -H "X-OpenAI-Session-Id: $SESSION_ID"
step "c7 a host the template does not allow"; curl -sS -o /dev/null -w "example.com http=%{http_code}\n" --max-time 15 https://example.com/
step "end"
