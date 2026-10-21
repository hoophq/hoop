#!/usr/bin/env bash
# Reads the diff of a PR and reports what weakens its tests and what makes it
# high risk. It reads the diff only: it never runs code from the PR.
#
#   scripts/ci/verification-guard.sh <base> <head> <out-dir>
#
# <base>...<head> is the PR (merge-base semantics). Writes <out-dir>/report.md
# and <out-dir>/labels (one label per line). Exit 0 unless the script fails.
set -euo pipefail

base=$1 head=$2 out=$3
range="$base...$head"
mkdir -p "$out"

# A change here needs a careful human review (CLAUDE.md).
risk_paths=(
	common/proto/                            # gateway <-> agent wire contract
	gateway/migrations/
	gateway/api/apiroutes/                   # roles and auth middleware
	gateway/idp/
	gateway/api/review/
	gateway/transport/plugins/review/
	gateway/transport/plugins/accesscontrol/
	gateway/analytics/events.go
	deploy/helm-chart/
)
# These steer or check the agents. A change here changes the guardrails.
harness_paths=(
	.github/ .claude/ .mcp.json Makefile scripts/ci/
	twins/ gateway/integration/parity/
)
# Above this many changed lines, a careful review is not a quick look.
max_lines=1500

git_diff() { git diff --no-color --no-ext-diff "$@"; }
# grep that does not fail the script on no match.
match() { grep -E "$@" || true; }
# One diff line, short enough for a comment.
clip() { cut -c1-160 | sed 's/```/` ` `/g'; }

risk=() harness=() weak=()

while IFS= read -r f; do
	for p in "${risk_paths[@]}"; do
		if [[ $f == "$p"* ]]; then risk+=("$f"); fi
	done
	for p in "${harness_paths[@]}"; do
		if [[ $f == "$p"* ]]; then harness+=("$f"); fi
	done
	if [[ $f == CLAUDE.md || $f == */CLAUDE.md ]]; then harness+=("$f"); fi
done < <(git_diff --name-only "$range")

lines=$(git_diff --numstat "$range" -- . \
	':(exclude)gateway/api/openapi/autogen/**' ':(exclude)**/*.pb.go' \
	':(exclude)**/package-lock.json' ':(exclude)**/go.sum' ':(exclude)**/*.wasm' |
	awk '$1 != "-" { n += $1 + $2 } END { print n + 0 }')

tests_diff=$(git_diff -U0 "$range" -- '*_test.go')

deleted_files=$(git_diff --name-only --diff-filter=D "$range" -- \
	'*_test.go' 'twins/scenario/**' 'gateway/integration/**' 'agent/integration/**')
if [[ -n $deleted_files ]]; then weak+=("deleted test files"); fi

# A function removed and not added back under the same name.
removed_funcs=$(comm -23 \
	<(match -o '^-func (Test|Fuzz)[A-Za-z0-9_]*' <<<"$tests_diff" | sed 's/^-func //' | sort -u) \
	<(match -o '^\+func (Test|Fuzz)[A-Za-z0-9_]*' <<<"$tests_diff" | sed 's/^+func //' | sort -u))
if [[ -n $removed_funcs ]]; then weak+=("deleted test functions"); fi

skips=$(
	match '^\+.*\b[tbf]\.Skip(f|Now)?\(|^\+.*testing\.Short\(\)' <<<"$tests_diff"
	git_diff -U0 "$range" -- '*.rs' | match '^\+[[:space:]]*#\[ignore'
)
if [[ -n $skips ]]; then weak+=("new skips"); fi

assert_re='t\.(Fatal|Fatalf|Error|Errorf|Fail|FailNow)\(|\b(require|assert)\.[A-Z]|\.Expect\(c,'
asserts_removed=$(match "^-.*($assert_re)" <<<"$tests_diff" | wc -l)
asserts_added=$(match "^\+.*($assert_re)" <<<"$tests_diff" | wc -l)
asserts_net=$((asserts_added - asserts_removed))
if ((asserts_net < 0)); then weak+=("fewer assertions"); fi

# A `must` row of the parity contract that the PR removes or downgrades.
musts=$(git_diff -U0 "$range" -- gateway/integration/parity/MUST_NOT_BREAK.md |
	match '^-\|.*\| must')
if [[ -n $musts ]]; then weak+=("parity must rows changed"); fi

labels=()
if ((${#risk[@]} + ${#harness[@]} + ${#weak[@]} > 0)) || ((lines > max_lines)); then
	labels+=(risk:high)
else
	labels+=(risk:low)
fi
if ((${#weak[@]} > 0)); then labels+=(tests-weakened); fi
printf '%s\n' "${labels[@]}" >"$out/labels"

{
	echo "### Verification guard"
	echo
	echo "Labels: \`${labels[*]}\`. Changed lines (without generated files): $lines."
	echo
	if ((${#weak[@]} > 0)); then
		echo "#### Tests weakened: ${weak[*]}"
		echo
		echo "Read these before you approve. Each one needs a reason in the PR."
		echo
		echo '```'
		if [[ -n $deleted_files ]]; then sed 's/^/deleted file: /' <<<"$deleted_files"; fi
		if [[ -n $removed_funcs ]]; then sed 's/^/deleted func: /' <<<"$removed_funcs"; fi
		if [[ -n $skips ]]; then clip <<<"$skips"; fi
		if ((asserts_net < 0)); then echo "assertions: -$asserts_removed +$asserts_added in *_test.go"; fi
		if [[ -n $musts ]]; then clip <<<"$musts"; fi
		echo '```'
		echo
	fi
	if ((${#harness[@]} > 0)); then
		echo "#### Guardrails changed"
		echo
		printf -- '- `%s`\n' "${harness[@]}" | sort -u
		echo
	fi
	if ((${#risk[@]} > 0)); then
		echo "#### High-risk paths"
		echo
		printf -- '- `%s`\n' "${risk[@]}" | sort -u
		echo
	fi
	if ((lines > max_lines)); then printf '#### Size: %s lines, above %s\n\n' "$lines" "$max_lines"; fi
} >"$out/report.md"
