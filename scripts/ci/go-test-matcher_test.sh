#!/usr/bin/env bash
# Fixture tests for go-test-matcher.json, the problem matcher that names each
# failed Go test on its CI job. Each case is one log line, in the -json or the
# -v format, and the annotations the line must make. jq reads the regexps with
# Oniguruma and the runner with .NET; the matcher uses only syntax both share.
#
# Usage: scripts/ci/go-test-matcher_test.sh   (from the repo root)

set -euo pipefail

MATCHER=$(cd "$(dirname "$0")/../.." && pwd)/.github/actions/setup-go-private/go-test-matcher.json

# The message of every annotation the matcher makes for one line.
annotate() {
  jq -r --arg line "$1" '.problemMatcher[].pattern[0] as $p
    | $line | match($p.regexp) | .captures[$p.message - 1].string' "$MATCHER"
}

passed=0 failed=0
# run <name> <line> [<want annotation>...]
run() {
  local name=$1 line=$2 got want
  shift 2
  got=$(annotate "$line")
  want=$(printf '%s\n' "$@")
  if [ "$got" != "$want" ]; then
    echo "FAIL: $name"
    echo "    line: $line"
    echo "    got:  ${got//$'\n'/ | }"
    echo "    want: ${want//$'\n'/ | }"
    failed=$((failed + 1))
  else
    passed=$((passed + 1))
  fi
}

# go test -json, as make test-unit and make test-sidecar print it.
J='{"Time":"2026-10-26T13:12:55.302072875Z","Action":"output","Package":"github.com/hoophq/hoop/sidecar/mcp"'
run "json: failed test" \
  "$J"',"Test":"TestShutdownStopsAWaitInFlight","Output":"--- FAIL: TestShutdownStopsAWaitInFlight (3.03s)\n"}' \
  "--- FAIL: TestShutdownStopsAWaitInFlight (3.03s)"
run "json: failed subtest" \
  "$J"',"Test":"TestRules/deny_drop","Output":"    --- FAIL: TestRules/deny_drop (0.00s)\n"}' \
  "--- FAIL: TestRules/deny_drop (0.00s)"
run "json: failed package" \
  "$J"',"Output":"FAIL\tgithub.com/hoophq/hoop/sidecar/mcp\t5.139s\n"}' \
  "github.com/hoophq/hoop/sidecar/mcp"
run "json: package that does not build" \
  "$J"',"Output":"FAIL\tgithub.com/hoophq/hoop/sidecar/mcp [build failed]\n"}' \
  "github.com/hoophq/hoop/sidecar/mcp"
run "json: timeout" \
  "$J"',"Output":"panic: test timed out after 15m0s\n"}' \
  "panic: test timed out after 15m0s"
run "json: the line that ends a failed package" "$J"',"Output":"FAIL\n"}'
run "json: fail event" \
  '{"Time":"2026-10-26T13:12:55.302079035Z","Action":"fail","Package":"github.com/hoophq/hoop/sidecar/mcp","Test":"TestShutdownStopsAWaitInFlight","Elapsed":3.03}'
run "json: passed test" "$J"',"Test":"TestServe","Output":"--- PASS: TestServe (0.00s)\n"}'
run "json: skipped test" "$J"',"Test":"TestServe","Output":"--- SKIP: TestServe (0.00s)\n"}'
run "json: passed package" "$J"',"Output":"ok  \tgithub.com/hoophq/hoop/sidecar/mcp\t5.139s\n"}'
run "json: a log line that says FAIL" \
  "$J"',"Test":"TestHold","Output":"    hold_test.go:41: FAIL\tclosed as expected\n"}'

# go test -v, as make test-integration and make test-gateway print it.
run "text: failed test" \
  "--- FAIL: TestShutdownStopsAWaitInFlight (3.03s)" \
  "--- FAIL: TestShutdownStopsAWaitInFlight (3.03s)"
run "text: failed subtest" \
  "    --- FAIL: TestRules/deny_drop (0.00s)" \
  "--- FAIL: TestRules/deny_drop (0.00s)"
run "text: failed package" \
  $'FAIL\tgithub.com/hoophq/hoop/gateway/integration\t41.006s' \
  "github.com/hoophq/hoop/gateway/integration"
run "text: package that does not build" \
  $'FAIL\tgithub.com/hoophq/hoop/gateway/api [build failed]' \
  "github.com/hoophq/hoop/gateway/api"
run "text: timeout" \
  "panic: test timed out after 10m0s" \
  "panic: test timed out after 10m0s"
run "text: the line that ends a failed package" "FAIL"
run "text: passed test" "--- PASS: TestServe (0.00s)"
run "text: passed package" $'ok  \tgithub.com/hoophq/hoop/gateway/api\t3.100s'
run "text: a table with a FAIL column" $'ID\tDESCRIPTION\tDISABLED\tVERSION\tFAIL\tPENDING'
run "text: make error" "make: *** [Makefile:138: test-sidecar] Error 1"

echo "$passed passed, $failed failed"
[ "$failed" -eq 0 ]
