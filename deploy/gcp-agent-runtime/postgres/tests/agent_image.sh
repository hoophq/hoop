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

# The database addresses: every listener's upstream (host:port, or
# [ipv6]:port) in sidecar.yaml, and its host when the host holds a dot or a
# colon (an FQDN or an IP). A one-label host such as "postgres" is also an
# ordinary word, so only its host:port counts.
# Block, flow and JSON forms: `upstream: x`, `{..., upstream: x}`, `"upstream": "x"`.
read -r -d '' extract <<'EOF' || true
import re, sys
text = open(sys.argv[1]).read()
for m in re.finditer(r"""["']?\bupstream["']?\s*:\s*["']?([^\s"',}#]+)""", text):
    print(m.group(1))
EOF
upstreams=$(python3 -I -c "$extract" "$cfg" | sort -u)
[[ -n $upstreams ]] || { echo "FAIL: no upstream in $cfg" >&2; exit 1; }
# An upstream this test cannot split into host and port would weaken the
# search below without a sign, so it stops here instead.
addr='^([^][:space:]:]+|\[[0-9A-Fa-f:.]+\]):[0-9]+$'
for u in $upstreams; do
  [[ $u =~ $addr ]] \
    || { echo "FAIL: cannot read upstream '$u' in $cfg as host:port or [ipv6]:port" >&2; exit 1; }
done
hosts=$(echo "$upstreams" | sed 's/:[0-9]*$//' | tr -d '[]' | grep -E '[.:]' | sort -u || true)

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
# 2. No database address, in the image config or in any file of the image.
for a in $upstreams $hosts; do
  if grep -qF "$a" "$work/config.json"; then
    echo "FAIL: the image config holds the database address $a"
    fail=1
  fi
done
# shellcheck disable=SC2086 # one argument per address
python3 -I - "$work/fs.tar" $upstreams $hosts <<'EOF' || fail=1
import sys, tarfile
path, needles = sys.argv[1], [n.encode() for n in sys.argv[2:]]
bad = 0
with tarfile.open(path) as tar:
    for m in tar:
        if not m.isfile():
            continue
        data = tar.extractfile(m).read()
        for n in needles:
            if n in data:
                print(f"FAIL: /{m.name} holds the database address {n.decode()}")
                bad = 1
sys.exit(bad)
EOF
# 3. The sidecar's address comes at deploy time, never from the image.
if grep -q '"APPDB_HOST=' "$work/config.json"; then
  echo "FAIL: the image sets APPDB_HOST"
  fail=1
fi
# 4. The image loads the agent with `uv run`, as its CMD does, with no network
# and an empty uv cache: a cold start on Agent Runtime with PSC-I has no route
# to pypi.org. There, uv built the project again at every start; touching
# pyproject.toml makes uv do the same here. The agent's name must be the one
# agents-cli recorded: a project created without --root-agent-name
# support_agent records another one.
want=$(sed -n 's/^[[:space:]]*root_agent_name:[[:space:]]*"\{0,1\}\([^"]*\)"\{0,1\}[[:space:]]*$/\1/p' "$dir/agents-cli-manifest.yaml")
if name=$(docker run --rm --network none -e UV_CACHE_DIR=/tmp/uv-cold "$tag" sh -c \
  'touch pyproject.toml && exec uv run python -c "from app.agent import root_agent; print(root_agent.name)"' \
  2>"$work/run.err"); then
  if [[ $name != "$want" ]]; then
    echo "FAIL: the image's root agent is '$name'; agents-cli-manifest.yaml records '$want'"
    fail=1
  fi
else
  echo "FAIL: the image cannot load the agent offline:"
  tail -5 "$work/run.err"
  fail=1
fi

if [[ $fail == 0 ]]; then
  echo "PASS: the image holds no sidecar and no database address ($(echo $upstreams)), and loads $name"
fi
exit $fail
