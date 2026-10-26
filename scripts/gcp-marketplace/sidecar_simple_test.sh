#!/usr/bin/env bash
# Tests the simple config of the GCP Marketplace sidecar module
# (deploy/gcp-marketplace/sidecar/modules/values): Terraform renders it with no
# guardrail preset and with read-only, the documents are pinned, and the
# sidecar validates both with no license, as a free install runs them.
#
# Usage: scripts/gcp-marketplace/sidecar_simple_test.sh [image]
#
#   image  the sidecar that validates the documents
#          [hoophq/hoopsidecar:1.271.0-distroless, the flavour Marketplace ships]
#
# Needs terraform (or tofu), jq and docker. Exits 1 after all checks when one
# fails.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
IMAGE=${1:-hoophq/hoopsidecar:1.271.0-distroless}
TEMPLATE_CONFIG=$ROOT/deploy/gcp-agent-runtime/postgres/deploy/sidecar/sidecar.yaml

tf() {
  if command -v terraform >/dev/null; then terraform "$@"
  elif command -v tofu >/dev/null; then tofu "$@"
  else echo "terraform or tofu is required" >&2; exit 1
  fi
}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
# A copy, so no .terraform directory lands in the module the zip is made from.
cp -R "$ROOT/deploy/gcp-marketplace/sidecar/modules/values" "$work/values"

fail=0
bad() { echo "FAIL: $*"; fail=1; }

# apply <case> <module arguments>: renders the module and writes the sidecar
# config it gives the chart to $work/<case>.json. The output of a refused
# apply is in $work/<case>/apply.log.
apply() {
  mkdir -p "$work/$1"
  cat >"$work/$1/main.tf" <<EOF
module "values" {
  source           = "../values"
  image_repository = "hoophq/hoopsidecar"
  image_tag        = "test"
  upstream         = "appdb.default.svc.cluster.local:5432"
  $2
}
output "config" {
  value = jsonencode([for d in module.values.values_yaml : yamldecode(d).config if can(yamldecode(d).config)][0])
}
EOF
  tf -chdir="$work/$1" init -input=false -no-color >/dev/null
  tf -chdir="$work/$1" apply -auto-approve -input=false -no-color >"$work/$1/apply.log" 2>&1 || return 1
  tf -chdir="$work/$1" output -raw config >"$work/$1.json"
}

# same <case> <want>: the rendered config is the JSON <want>, keys in any order.
same() {
  if [[ "$(jq -S . "$work/$1.json")" != "$(jq -S . <<<"$2")" ]]; then
    bad "$1 renders another config:"
    diff <(jq -S . <<<"$2") <(jq -S . "$work/$1.json") || true
  fi
}

# refused <case> <module arguments> <message>: the module refuses the
# arguments with the message.
refused() {
  if apply "$1" "$2"; then
    bad "$1: the module accepted $2"
  elif ! tr -s ' \n' ' ' <"$work/$1/apply.log" | grep -qF -- "$3"; then
    bad "$1: the module refused $2, but not with '$3':"
    cat "$work/$1/apply.log"
  fi
}

listener='{"listen": "0.0.0.0:15432", "name": "postgres", "protocol": "postgres", "upstream": "appdb.default.svc.cluster.local:5432"}'
none='{"admin": {"listen": "0.0.0.0:19000"}, "audit": {"file": "-"}, "listeners": ['"$listener"']}'
operations='["insert", "update", "delete", "create", "drop", "alter", "truncate", "grant", "revoke", "call", "other"]'
read_only='{"admin": {"listen": "0.0.0.0:19000"}, "audit": {"file": "-"}, "listeners": ['"$listener"'],
  "guardrails": {"rules": [{"name": "read-only", "type": "operation", "operations": '"$operations"',
  "message": "hoop: this connection may read the database, not change it"}]}}'

# 1. No preset: the config a simple install has always had.
apply none '' || { cat "$work/none/apply.log"; exit 1; }
same none "$none"
apply none-explicit 'guardrail_preset = "none"' || { cat "$work/none-explicit/apply.log"; exit 1; }
same none-explicit "$none"

# 2. read-only: the same config plus one rule.
apply read-only 'guardrail_preset = "read-only"' || { cat "$work/read-only/apply.log"; exit 1; }
same read-only "$read_only"
# On a wire lane, a rule naming unknown refuses every result set (ENG-545).
if jq -e '.guardrails.rules[].operations | index("unknown")' "$work/read-only.json" >/dev/null; then
  bad "read-only names unknown"
fi
# The preset is the agents-read-only rule of the agents-cli template. A change
# to one of them must reach the other.
template_ops=$(sed -n '/name: agents-read-only/,/message:/s/^ *operations: \[\(.*\)\]$/\1/p' "$TEMPLATE_CONFIG")
[[ $template_ops == "$(jq -r 'join(", ")' <<<"$operations")" ]] \
  || bad "the read-only operations differ from agents-read-only in $TEMPLATE_CONFIG: [$template_ops]"

# 3. What the module refuses.
refused read-only-mysql 'protocol = "mysql"
  guardrail_preset = "read-only"' "guardrail_preset = read-only is for postgres only"
refused unknown-preset 'guardrail_preset = "unknown"' "guardrail_preset must be none or read-only"

# 4. The sidecar accepts both documents with no license, and enforces the rule.
for c in none read-only; do
  if ! out=$(docker run --rm -v "$work/$c.json:/etc/hoop-inspect/config.yaml:ro" "$IMAGE" \
      hoop start sidecar --validate --strict 2>&1); then
    bad "$IMAGE refused the $c config:"
    echo "$out"
    continue
  fi
  want="no rules to enforce"
  [[ $c == read-only ]] && want="enforcing 1 rule(s)"
  for w in "license: missing, running the free tier" "$want"; do
    grep -qF -- "$w" <<<"$out" || bad "$IMAGE validated the $c config without '$w': $out"
  done
done

if [[ $fail == 0 ]]; then
  echo "PASS: the simple config renders with and without read-only, and $IMAGE accepts both with no license"
fi
exit $fail
