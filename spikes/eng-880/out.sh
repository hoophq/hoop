#!/usr/bin/env bash
# ENG-880: prints a command stream (ac.sh cmd) as text: stdout, stderr, exit.
# Usage: ./out.sh < out/<file>.jsonl
jq -rj 'if .event == "chunk" then
          (.contentDelta.stdout // ""), (.contentDelta.stderr // ""),
          (if .contentStop then "\n[exit \(.contentStop.exitCode) \(.contentStop.status)]\n" else "" end)
        elif .event == "http" then "[http \(.status) \(.seconds)s]\n"
        else "[\(.event)] \(del(.event) | tostring)\n" end'
