#!/usr/bin/env bash
#
# ENG-880 c1, c2: for each endpoint named, turn it off, start a fresh session
# on each agent, turn it back on. Runs on the operator's machine.
#
#   cells/ablate.sh baseline            no stack change
#   cells/ablate.sh EcrDkr EcrApi ...   the stack's Ep<name> parameters
#
# One line per session in out/c1-ablation.txt; raw streams in out/c1-*.

set -uo pipefail
cd "$(dirname "$0")/.."
log() { printf '%s %s\n' "$(date -u +%H:%M:%S)" "$*" | tee -a out/c1-ablation.txt; }

session() { # session <label> <agent>
    local sid f=out/c1-$1-$2 ok err
    sid=$(./ac.sh session)
    if [[ $2 == *_rt* ]]; then
        ./ac.sh runtime "$(cat .work/$2.arn)" "$sid" 'Run the shell command: echo shell-ok' > "$f.json" 2> "$f.http"
        ok=$(jq -r '[.steps[]?.stdout] | join("") | test("shell-ok")' "$f.json" 2>/dev/null || echo false)
        err=$(jq -r '.error // .message // empty' "$f.json" 2>/dev/null | cut -c1-160)
    else
        ./ac.sh harness "$(cat .work/$2.arn)" "$sid" 'Run the shell command: echo shell-ok' > "$f.jsonl" 2> "$f.http"
        ok=$(jq -r 'select(.event == "contentBlockDelta") | .delta.toolResult[0].text? // empty' "$f.jsonl" 2>/dev/null |
            grep -q '"stdout": "shell-ok' && echo true || echo false)
        err=$(jq -r 'select(.event != "contentBlockDelta" and .event != "contentBlockStart" and .event != "contentBlockStop"
              and .event != "messageStart" and .event != "messageStop" and .event != "metadata") | tostring' "$f.jsonl" 2>/dev/null | head -2 | cut -c1-200 | tr '\n' ' ')
    fi
    log "$1 $2 shell=$ok $(tail -1 "$f.http" | jq -rc '"http=\(.status) \(.seconds|floor)s"' 2>/dev/null) $err"
}

round() { for a in eng880_h1 eng880_h2 eng880_rt1; do session "$1" "$a"; done; }

for ep in "$@"; do
    if [[ $ep == baseline ]]; then round baseline; continue; fi
    log "== Ep$ep=false"
    ./deploy.sh params "Ep$ep=false" > /dev/null || { log "stack update failed for $ep"; exit 1; }
    round "no-$ep"
    ./deploy.sh params "Ep$ep=true" > /dev/null || { log "stack restore failed for $ep"; exit 1; }
done
