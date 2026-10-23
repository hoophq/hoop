#!/usr/bin/env bash
# PreToolUse hook: the support agent posts with a person's Slack and Linear
# account, so every text it writes must say an AI wrote it, why, and link the
# session that wrote it. Slack posts go to one channel only. Unknown Slack and
# Linear writes are refused. Active only where HOOP_AGENT is set (the agent's
# own cloud environment): interactive sessions are not affected.
[[ -z "$HOOP_AGENT" ]] && exit 0

input=$(cat)
tool=$(jq -r '.tool_name' <<<"$input" | tr '[:upper:]' '[:lower:]')

deny() { echo "BLOCKED by ai-disclosure: $1" >&2; exit 2; }

# Line 1 starts with the robot and names the author as AI. A "Why:" or
# "Por quê:" line gives the reason, not empty. A full URL names the session.
check_text() {
  local text="$1" first
  first=$(head -n1 <<<"$text")
  [[ $first == "🤖"* && ( $first == *"(IA)"* || $first == *"(AI)"* ) ]] ||
    deny "line 1 must start with '🤖' and name the author as AI, e.g. '🤖 *Agente de suporte (IA)* · conta do ${HOOP_AGENT_OWNER:-<owner>}'"
  grep -qE '^[*_]*(Why|Por quê):[*_]*[[:space:]]*[^[:space:]*_]' <<<"$text" ||
    deny "add a 'Por quê:' (Slack) or 'Why:' (Linear) line with the ticket and the reason"
  grep -qE 'https://claude\.ai/code/session_[A-Za-z0-9]{16,}' <<<"$text" ||
    deny "link the session that wrote this text (https://claude.ai/code/session_<id>)"
}

case "$tool" in
  mcp__*slack*__*)
    case "$tool" in
      *read* | *search*) ;;
      *send_message* | *schedule_message* | *post_message*)
        channel=$(jq -r '.tool_input.channel_id // .tool_input.channel // empty' <<<"$input")
        [[ -n "$HOOP_AGENT_SLACK_CHANNEL" && "$channel" == "$HOOP_AGENT_SLACK_CHANNEL" ]] ||
          deny "post only in channel ${HOOP_AGENT_SLACK_CHANNEL:-<HOOP_AGENT_SLACK_CHANNEL unset>}; mention people there"
        check_text "$(jq -r '.tool_input.message // .tool_input.text // empty' <<<"$input")"
        ;;
      *) deny "the agent only reads Slack and posts messages ($tool)" ;;
    esac
    ;;
  mcp__*linear*__*)
    case "$tool" in
      *__get_* | *__list_* | *__search_* | *__extract_*) ;;
      *__save_comment)
        check_text "$(jq -r '.tool_input.body // empty' <<<"$input")"
        ;;
      *__save_issue)
        # The description is the support team's text: the agent comments
        # instead of rewriting it. It changes only labels, state and links.
        jq -e '.tool_input | has("description") or has("patch") or has("title")' <<<"$input" >/dev/null &&
          deny "do not edit the issue text; post a comment instead"
        jq -e '.tool_input | has("id")' <<<"$input" >/dev/null ||
          deny "the agent does not create issues"
        other=$(jq -r '.tool_input | keys - ["id", "addLabels", "removeLabels", "state", "links"] | join(", ")' <<<"$input")
        [[ -z $other ]] ||
          deny "the agent changes only addLabels, removeLabels, state and links of an issue (got: $other)"
        ;;
      *__prepare_attachment_upload | *__create_attachment*) ;;
      *) deny "the agent only reads Linear, comments, changes labels and state, and attaches files ($tool)" ;;
    esac
    ;;
esac
exit 0
