#!/usr/bin/env bash
# Fixture tests for ai-disclosure.sh. Each case feeds one tool call to the
# hook and checks it allows (exit 0) or blocks (exit 2) the call.
#
# Usage: .claude/hooks/ai-disclosure_test.sh   (from the repo root)

set -uo pipefail

HOOK=$(cd "$(dirname "$0")" && pwd)/ai-disclosure.sh
export HOOP_AGENT=support HOOP_AGENT_OWNER=Rogerio HOOP_AGENT_SLACK_CHANNEL=C0SUPPORT
fail=0

# expect <want-exit> <name> <json>
expect() {
  local got
  "$HOOK" <<<"$3" >/dev/null 2>&1
  got=$?
  if [[ $got == "$1" ]]; then echo "ok   $2"; else echo "FAIL $2: exit $got, want $1"; fail=1; fi
}

slack() { # $1=channel $2=message
  jq -n --arg c "$1" --arg m "$2" '{tool_name:"mcp__Slack__slack_send_message",tool_input:{channel_id:$c,message:$m}}'
}
comment() { jq -n --arg b "$1" '{tool_name:"mcp__Linear__save_comment",tool_input:{issueId:"ENG-1",body:$b}}'; }
issue() { jq -n --argjson i "$1" '{tool_name:"mcp__Linear__save_issue",tool_input:$i}'; }
call() { jq -n --arg t "$1" '{tool_name:$t,tool_input:{}}'; }

SLACK_OK=$'🤖 *Agente de suporte (IA)* · conta do Rogerio\n*Por quê:* ENG-1, preciso saber X\nSessão: https://claude.ai/code/session_01\n\n@ana X acontece?'
LINEAR_OK=$'🤖 **Support agent (AI)** · posted from Rogerio\'s account\n**Why:** spec for ENG-1\nSession: https://claude.ai/code/session_01\n\n## Goal'

expect 0 "slack: disclosed post in the agent channel" "$(slack C0SUPPORT "$SLACK_OK")"
expect 2 "slack: other channel" "$(slack C0PRODUCT "$SLACK_OK")"
expect 2 "slack: direct message" "$(slack U02NTRQ0K1T "$SLACK_OK")"
expect 2 "slack: no AI marker" "$(slack C0SUPPORT 'X acontece?')"
expect 2 "slack: marker without (IA)" "$(slack C0SUPPORT "${SLACK_OK/(IA)/}")"
expect 2 "slack: no reason line" "$(slack C0SUPPORT "${SLACK_OK/Por quê:/}")"
expect 2 "slack: no session link" "$(slack C0SUPPORT "${SLACK_OK/claude.ai\/code\/session_01/}")"
expect 0 "slack: read a thread" "$(call mcp__Slack__slack_read_thread)"
expect 2 "slack: create a canvas" "$(call mcp__Slack__slack_create_canvas)"
expect 0 "linear: disclosed comment" "$(comment "$LINEAR_OK")"
expect 2 "linear: comment without marker" "$(comment 'Spec here')"
expect 0 "linear: lowercase server name" "$(jq '.tool_name="mcp__linear__save_comment"' <<<"$(comment "$LINEAR_OK")")"
expect 0 "linear: add a label" "$(issue '{"id":"ENG-1","addLabels":["agent:working"]}')"
expect 2 "linear: rewrite the description" "$(issue '{"id":"ENG-1","description":"new"}')"
expect 2 "linear: patch the description" "$(issue '{"id":"ENG-1","patch":[{"op":"append","text":"x"}]}')"
expect 2 "linear: create an issue" "$(issue '{"team":"ENG","addLabels":["x"]}')"
expect 0 "linear: list issues" "$(call mcp__Linear__list_issues)"
expect 2 "linear: delete a comment" "$(call mcp__Linear__delete_comment)"
expect 0 "other tools are not checked" "$(call Bash)"

HOOP_AGENT= expect 0 "interactive session: not checked" "$(slack C0PRODUCT 'hello')"
HOOP_AGENT_SLACK_CHANNEL= expect 2 "channel unset: slack refused" "$(slack C0SUPPORT "$SLACK_OK")"

exit $fail
