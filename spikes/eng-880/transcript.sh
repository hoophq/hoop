#!/usr/bin/env bash
# ENG-880: renders a decoded InvokeHarness stream (ac.sh harness) as text:
# each shell call, its output, then the answer. Usage: ./transcript.sh < out/<file>.jsonl
jq -rs '
  def flush: if .tool != "" then "$ " + (.tool | fromjson? // {} | .command // .) + "\n" else "" end;
  reduce .[] as $e ({out: "", tool: "", text: ""};
    if $e.event == "contentBlockStart" and $e.start.toolUse then .tool = ""
    elif $e.event == "contentBlockDelta" and $e.delta.toolUse then .tool += $e.delta.toolUse.input
    elif $e.event == "contentBlockStop" and .tool != "" then .out += flush | .tool = ""
    elif $e.event == "contentBlockDelta" and $e.delta.toolResult then
      .out += ([$e.delta.toolResult[].text] | join("") | fromjson? // {stdout: .}
               | (.stdout // "") + (if (.stderr // "") != "" then "[stderr] " + (.stderr | .[0:300]) + "\n" else "" end)
               + "[exit \(.exit_code // "?")]\n")
    elif $e.event == "contentBlockDelta" and $e.delta.text then .text += $e.delta.text
    elif $e.event == "messageStop" and .text != "" then .out += "\n" + .text + "\n\n" | .text = ""
    elif $e.event == "runtimeClientError" then .out += "[runtimeClientError] " + $e.message + "\n"
    else . end) | .out'
