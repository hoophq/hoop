#!/usr/bin/env bash
#
# Proves Vertex works before any hoop code is involved.
#
# This issues the same rawPredict request analyzer/vertex builds: same URL
# shape, same anthropic_version in the body, same OAuth bearer. If it fails
# here, the relay will fail identically, and the problem is IAM, model
# enablement or a region name rather than anything in sidecar.
#
# Usage:
#   HOOP_PROJECT=my-project ./00-preflight.sh
#
# Costs one Vertex call.

set -uo pipefail
cd "$(dirname "$0")"
source ./lib.sh

h "Preflight: can this machine reach Claude on Vertex?"

# ------------------------------------------------------------------- tools
for bin in gcloud curl python3; do
    command -v "$bin" >/dev/null || die "$bin is not installed"
done
ok "gcloud, curl and python3 are present"

[[ -n "${HOOP_PROJECT:-}" ]] || die "set HOOP_PROJECT to your GCP project id"
note "project ${HOOP_PROJECT}   region ${HOOP_REGION}   model ${HOOP_MODEL}"

# ------------------------------------------------------------- credentials
# Two modes, matching the relay: a service-account key file, or Application
# Default Credentials. ADC is preferred, because under Workload Identity there
# is then no credential on disk at all.
if [[ -n "${HOOP_CREDENTIALS_FILE:-}" ]]; then
    [[ -f "$HOOP_CREDENTIALS_FILE" ]] || die "HOOP_CREDENTIALS_FILE $HOOP_CREDENTIALS_FILE does not exist"

    # The relay refuses a key any local account can read, the way ssh refuses a
    # private key. Catch it here rather than at startup.
    mode=$(stat -f '%Lp' "$HOOP_CREDENTIALS_FILE" 2>/dev/null || stat -c '%a' "$HOOP_CREDENTIALS_FILE")
    if [[ "${mode: -2}" != "00" ]]; then
        die "credential file is mode $mode; the relay refuses anything readable by group or other.
       chmod 600 $HOOP_CREDENTIALS_FILE"
    fi
    ok "credential file $HOOP_CREDENTIALS_FILE is mode $mode"

    token=$(GOOGLE_APPLICATION_CREDENTIALS="$HOOP_CREDENTIALS_FILE" \
        gcloud auth application-default print-access-token 2>/dev/null) \
        || die "could not mint a token from $HOOP_CREDENTIALS_FILE"
else
    # Application Default Credentials, which is what the relay resolves
    # through google.FindDefaultCredentials when credentials_file is unset.
    #
    # NOT `gcloud auth print-access-token`: that returns the gcloud USER
    # login, a different credential the relay never reads. A machine can have
    # working ADC and no user login, and testing the wrong one reports a
    # failure the relay would not hit.
    token=$(gcloud auth application-default print-access-token 2>/dev/null) || die \
"no Application Default Credentials. Run:
       gcloud auth application-default login
       gcloud auth application-default set-quota-project $HOOP_PROJECT

     Or point at a service-account key instead:
       export HOOP_CREDENTIALS_FILE=/path/to/key.json"
fi
ok "minted a GCP access token from ADC"

# ----------------------------------------------------------------- the API
# "global" uses the unprefixed host. Getting this wrong yields a DNS failure
# rather than an API error, which reads like a network problem and sends you
# to the wrong place.
host="${HOOP_REGION}-aiplatform.googleapis.com"
[[ "$HOOP_REGION" == "global" ]] && host="aiplatform.googleapis.com"
url="https://${host}/v1/projects/${HOOP_PROJECT}/locations/${HOOP_REGION}/publishers/anthropic/models/${HOOP_MODEL}:rawPredict"
note "POST ${url}"

body=$(cat <<'JSON'
{"anthropic_version":"vertex-2023-10-16","max_tokens":64,
 "messages":[{"role":"user","content":"Reply with the single word: ok"}]}
JSON
)

resp=$(curl -sS -w '\n%{http_code}' -X POST "$url" \
    -H "Authorization: Bearer ${token}" \
    -H "Content-Type: application/json" \
    -d "$body" 2>&1)
code=$(tail -1 <<<"$resp")
payload=$(sed '$d' <<<"$resp")

case "$code" in
    200) ok "Vertex answered 200" ;;
    401) die "401 unauthorized. The token is stale:
       gcloud auth application-default login" ;;
    403) die "403 permission denied. The caller lacks roles/aiplatform.user:
       gcloud projects add-iam-policy-binding $HOOP_PROJECT \\
         --member=\"user:\$(gcloud config get-value account)\" \\
         --role=\"roles/aiplatform.user\"" ;;
    404) die "404 on the model path. Either $HOOP_MODEL is not enabled in Model
       Garden for this project, or it is not served from $HOOP_REGION.
       Enable it at:
       https://console.cloud.google.com/vertex-ai/publishers/anthropic/model-garden
       Or try HOOP_REGION=global.

$(head -c 400 <<<"$payload")" ;;
    *)   die "HTTP $code

$(head -c 400 <<<"$payload")" ;;
esac

reply=$(python3 -c '
import json,sys
d = json.load(sys.stdin)
print("".join(b.get("text","") for b in d.get("content", [])).strip() or "(no text)")
' <<<"$payload" 2>/dev/null) || reply="(unparseable)"
ok "model replied: ${reply}"

# --------------------------------------------------------------- tool call
# The analyzer never reads prose: the risk level IS which tool the model
# called. If tool calling does not work, every classification fails, so it is
# worth proving separately from a plain completion.
h "Tool calling, which is how the analyzer reads a verdict"

tools=$(cat <<'JSON'
{"anthropic_version":"vertex-2023-10-16","max_tokens":256,
 "messages":[{"role":"user","content":"DELETE FROM customers"}],
 "tool_choice":{"type":"any"},
 "tools":[{"name":"report_high_risk","description":"Report high risk.",
   "input_schema":{"type":"object",
     "properties":{"title":{"type":"string"},"explanation":{"type":"string"}},
     "required":["title","explanation"]}}]}
JSON
)

tresp=$(curl -sS -X POST "$url" \
    -H "Authorization: Bearer ${token}" \
    -H "Content-Type: application/json" -d "$tools")

called=$(python3 -c '
import json,sys
d = json.load(sys.stdin)
for b in d.get("content", []):
    if b.get("type") == "tool_use":
        print(b.get("name","")); break
' <<<"$tresp" 2>/dev/null)

[[ "$called" == "report_high_risk" ]] \
    || die "the model did not call the tool. Analyzer verdicts will always fail.

$(head -c 400 <<<"$tresp")"
ok "model called report_high_risk"

h "Preflight passed"
note "Vertex is reachable, the model is enabled and tool calling works."
note "Next: ./01-validate.sh"
