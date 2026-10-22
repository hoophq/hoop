#!/usr/bin/env bash
# Builds this project's agent image, the one `agents-cli deploy` builds for
# Agent Runtime, and checks that it holds no sidecar and no database address.
# The agent may know only the sidecar's address, and only from APPDB_HOST at
# deploy time.
#
#   tests/agent_image.sh [project dir]    default: the directory above tests/
#
# Needs docker. Exits 1 on the first build error, or after all checks when one
# fails.
set -euo pipefail

dir=$(cd "${1:-$(dirname "$0")/..}" && pwd)
cfg="$dir/deploy/sidecar/sidecar.yaml"
[[ -f $dir/Dockerfile ]] || { echo "FAIL: no Dockerfile in $dir; run this in a project made by agents-cli create" >&2; exit 1; }
[[ -f $cfg ]] || { echo "FAIL: no $cfg" >&2; exit 1; }

# The database addresses: the host of every listener's upstream in sidecar.yaml.
hosts=$(sed -n 's/^[[:space:]]*upstream:[[:space:]]*\([^[:space:]#]*\).*/\1/p' "$cfg" | sed 's/:[0-9]*$//' | sort -u)
[[ -n $hosts ]] || { echo "FAIL: no upstream in $cfg" >&2; exit 1; }

tag="agent-image-test:$$"
cid=""
work=$(mktemp -d)
cleanup() {
  [[ -n $cid ]] && docker rm -f "$cid" >/dev/null 2>&1
  docker rmi -f "$tag" >/dev/null 2>&1
  rm -rf "$work"
}
trap cleanup EXIT

echo "building $dir"
docker build -q -t "$tag" "$dir" >/dev/null
cid=$(docker create "$tag")
docker export "$cid" >"$work/fs.tar"
tar -tf "$work/fs.tar" >"$work/files"
docker image inspect "$tag" --format '{{json .Config}}' >"$work/config.json"

fail=0
# 1. No sidecar: its binary, its config, or the deploy/sidecar/ tree.
if grep -E '(^|/)(hoopsidecar|hoop|sidecar\.yaml)$|(^|/)deploy/sidecar(/|$)' "$work/files"; then
  echo "FAIL: the image holds the sidecar or its config (paths above)"
  fail=1
fi
# 2. No database address, in the image's config or in any of its files.
for h in $hosts; do
  if grep -qF "$h" "$work/config.json"; then
    echo "FAIL: the image config holds the database address $h"
    fail=1
  fi
  if grep -qaF "$h" "$work/fs.tar"; then
    echo "FAIL: a file in the image holds the database address $h:"
    python3 -I - "$work/fs.tar" "$h" <<'EOF'
import sys, tarfile
path, needle = sys.argv[1], sys.argv[2].encode()
with tarfile.open(path) as tar:
    for m in tar:
        if m.isfile() and needle in tar.extractfile(m).read():
            print("  /" + m.name)
EOF
    fail=1
  fi
done
# 3. The sidecar's address comes at deploy time, never from the image.
if grep -q '"APPDB_HOST=' "$work/config.json"; then
  echo "FAIL: the image sets APPDB_HOST"
  fail=1
fi
# 4. The image loads the agent, under the name agents-cli recorded: a project
# created without --root-agent-name support_agent records another one.
want=$(sed -n 's/^[[:space:]]*root_agent_name:[[:space:]]*"\{0,1\}\([^"]*\)"\{0,1\}[[:space:]]*$/\1/p' "$dir/agents-cli-manifest.yaml")
name=$(docker run --rm "$tag" uv run --no-sync python -c 'from app.agent import root_agent; print(root_agent.name)')
if [[ $name != "$want" ]]; then
  echo "FAIL: the image's root agent is '$name'; agents-cli-manifest.yaml records '$want'"
  fail=1
fi

if [[ $fail == 0 ]]; then
  echo "PASS: the image holds no sidecar and no database address ($(echo $hosts)), and loads $name"
fi
exit $fail
