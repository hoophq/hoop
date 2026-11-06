#!/usr/bin/env bash
# Put a `protocol: ica` lane on a Linux host without Docker: install
# hoop-ica, the sidecar with the libhoop-ica engine and ONNX Runtime, and
# what the engine reads at run time, at the paths it reads by default, and
# run it under systemd.
#
# This script ships inside hoop-ica-assets_<version>_Linux_<arch>.tar.gz on
# releases.hoop.dev, and it lays out what hoop's `Dockerfile.sidecar --target
# ica` lays out, so a host and the -ica image run the same binary against the
# same assets from the same places:
#
#   /usr/local/bin/hoop-ica                           the ica engine build (hoop-inspect, cgo -tags ORT)
#   /opt/libhoop-ica/releases/<version>/bin/          hoop-tesseract-worker
#   /opt/libhoop-ica/releases/<version>/models/       the NER model
#   /opt/libhoop-ica/current -> releases/<version>    what the engine reads
#   /opt/alcatraz/ort/releases/<ort>/lib/             libonnxruntime.so
#   /usr/share/tesseract-ocr/5/tessdata               from the distro package
#
# The service runs hoop-ica. The Linux hoop CLI of the same release links
# the engine as well, so `hoop start sidecar --config` on the same file
# serves the lane in-process once these assets are installed; this unit is
# the supported way to run it, and works with a CLI that predates the
# engine.
#
# Usage, as root on Ubuntu 24.04 or Debian 12 (amd64 or arm64), from the
# extracted tarball:
#
#   ./install.sh [options]
#
#   --config FILE       install FILE as /etc/hoop-inspect/config.yaml once it
#                       validates; without it an existing config stays and a
#                       new host gets examples/config.single.yaml to edit
#                       (examples/config.cidr.yaml is the CIDR-mode one,
#                       examples/config.gateway-direct.yaml the single-mode
#                       lane a Citrix Gateway dials directly)
#   --model-dir DIR     copy the NER model from DIR instead of downloading it
#                       (air-gapped hosts). DIR is a KnightsAnalytics_distilbert-NER
#                       directory: config.json, model.onnx, tokenizer.json, ...
#   --model-origin URL  download the model from URL instead of
#                       https://releases.hoop.dev/alcatraz/current
#   --cidr              CIDR (transparent) mode: grant the service CAP_NET_ADMIN.
#                       The TPROXY rule, policy route and rp_filter stay yours;
#                       see libhoop-ica's docs/transparent-linux.md
#   --start             restart the service on the installed config and wait
#                       for its admin endpoint's /readyz (the model load takes
#                       minutes). Refused while the config is still the
#                       unedited template
#
# The worker is dynamically linked against the distro's libtesseract 5, so the
# host must be a distribution of that generation; the script installs the
# package. The model is ~250MB and the same for every architecture; a release
# already on the host lends its copy through hard links, so an upgrade neither
# downloads nor duplicates it.
#
# Every check runs before anything live changes: the new release is staged
# under its own directory, the binary is probed from the tarball, and a
# --config candidate is validated as the service would see it, with
# /etc/hoop-inspect/env. Only then do `current`, the binary and the config
# switch, and releases older than the one being replaced are removed. The
# config and /etc/hoop-inspect/env stay unless --config replaces the config.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
# GPU bundles use their own preflight/immutable release switch. The CPU
# install path below is unchanged.
if [[ -f $here/release.txt ]] && grep -qx 'flavour=ica-gpu' "$here/release.txt"; then
  exec bash "$here/install-gpu.sh" "$@"
fi

usage() { sed -n '/^# Usage/,/^# --config replaces/p' "$0" | sed 's/^# \{0,1\}//'; }
die() { echo "install: $*" >&2; exit 1; }
step() { echo "==> $*"; }

CONFIG=
MODEL_DIR=
MODEL_ORIGIN=https://releases.hoop.dev/alcatraz/current
CIDR=0
START=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --config) CONFIG=${2:?--config needs a value}; shift 2 ;;
    --model-dir) MODEL_DIR=${2:?--model-dir needs a value}; shift 2 ;;
    --model-origin) MODEL_ORIGIN=${2:?--model-origin needs a value}; shift 2 ;;
    --cidr) CIDR=1; shift ;;
    --start) START=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; die "unknown option $1" ;;
  esac
done

[[ $EUID -eq 0 ]] || die "run as root: it installs packages, /opt and a systemd unit"
[[ $(uname -s) == Linux ]] || die "the ica lane runs on Linux"
[[ -z $CONFIG || -f $CONFIG ]] || die "--config $CONFIG: no such file"
[[ -z $MODEL_DIR || -f $MODEL_DIR/config.json ]] || die "--model-dir $MODEL_DIR: no config.json, so not a model directory"

# release.txt is written by the stage that packs this tarball: the hoop
# release, the architecture its binaries were built for, and the ONNX Runtime
# version, which names the directory the engine reads the library from.
[[ -f $here/release.txt ]] || die "no release.txt beside this script: run it from the extracted tarball"
VERSION=$(sed -n 's/^version=//p' "$here/release.txt")
ARCH=$(sed -n 's/^arch=//p' "$here/release.txt")
ORT_VERSION=$(sed -n 's/^ort=//p' "$here/release.txt")
[[ $VERSION =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || die "release.txt: version '$VERSION' is not a version"
[[ $ORT_VERSION =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "release.txt: ort '$ORT_VERSION' is not a version"
case "$(uname -m)" in
  x86_64) host_arch=amd64 ;;
  aarch64) host_arch=arm64 ;;
  *) die "no build for $(uname -m)" ;;
esac
[[ $ARCH == "$host_arch" ]] || die "this tarball is for $ARCH and the host is $host_arch"

RELEASES=/opt/libhoop-ica/releases
CURRENT=/opt/libhoop-ica/current
ORT_DIR=/opt/alcatraz/ort/releases/${ORT_VERSION}
CONF_DIR=/etc/hoop-inspect
CONF=$CONF_DIR/config.yaml
ENV_FILE=$CONF_DIR/env
BIN=/usr/local/bin/hoop-ica
UNIT=hoop-ica
MODEL_NAME=KnightsAnalytics_distilbert-NER
rel=$RELEASES/$VERSION
model=$rel/models/$MODEL_NAME
tessdata=/usr/share/tesseract-ocr/5/tessdata

# The release this run replaces, if any: kept as the rollback target, so
# the prune at the end removes only releases older than it.
previous_release=
if [[ -L $CURRENT ]]; then
  previous_release=$(readlink -f "$CURRENT")
  [[ $previous_release == "$rel" ]] && previous_release=
fi

# Run hoop-ica as the service runs it: the hoop user, with the variables
# the unit's EnvironmentFile supplies (HOOP_LICENSE decides the rule limits
# a config may use). The file is read line by line, never sourced: it is
# KEY=VALUE lines with optional double quotes, as systemd reads it, and its
# values are passed through the environment, never printed.
as_service() {
  local line key value
  local -a vars=()
  if [[ -f $ENV_FILE ]]; then
    while IFS= read -r line || [[ -n $line ]]; do
      [[ $line =~ ^[[:space:]]*([A-Za-z_][A-Za-z0-9_]*)=(.*)$ ]] || continue
      key=${BASH_REMATCH[1]}
      value=${BASH_REMATCH[2]}
      [[ $value == \"*\" ]] && value=${value#\"} && value=${value%\"}
      vars+=("$key=$value")
    done <"$ENV_FILE"
  fi
  runuser -u hoop -- env "${vars[@]}" "$@"
}

# The address the installed config's admin endpoint listens on, from the
# normalized document hoop-ica prints, so a config that moves or drops
# the endpoint is read as written, not assumed. Empty when there is none.
admin_listen() {
  "$BIN" -migrate -config "$CONF" 2>/dev/null |
    awk '/^admin:/ {a=1; next} a && /^[^ ]/ {a=0} a && /^ +listen:/ {print $2; exit}' |
    tr -d '"'"'"
}

step "packages: tesseract 5 and English tessdata"
export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y --no-install-recommends ca-certificates curl tesseract-ocr tesseract-ocr-eng libtesseract5
command -v tesseract >/dev/null || die "tesseract CLI is missing"
[[ -f $tessdata/eng.traineddata ]] || die "English tessdata is missing under $tessdata"

step "checking the tarball's binaries against this host"
for bin in "$here/hoop-ica" "$here/bin/hoop-tesseract-worker"; do
  [[ -x $bin ]] || die "$bin is missing or not executable"
  if ldd "$bin" | grep -q 'not found'; then
    ldd "$bin" | grep 'not found' >&2
    die "$(basename "$bin") links libraries this host does not have"
  fi
done
echo "    $("$here/hoop-ica" -version 2>/dev/null || echo "hoop-ica ${VERSION}")"

step "staging release ${VERSION} under ${rel}"
[[ -d $rel ]] && echo "    release directory exists; refreshing it"
mkdir -p "$rel/bin" "$rel/models"
install -m 0755 "$here/bin/hoop-tesseract-worker" "$rel/bin/"
install -m 0644 "$here/bin/hoop-tesseract-worker.txt" "$rel/bin/"
install -m 0644 "$here/LICENSE" "$here/NOTICE" "$here/release.txt" "$rel/"

if [[ -f $model/config.json ]]; then
  step "NER model: already at ${model}"
elif [[ -n $MODEL_DIR ]]; then
  step "NER model: copying from ${MODEL_DIR}"
  cp -a "$MODEL_DIR" "$model"
else
  # A model an earlier release already holds is hard-linked, not copied:
  # the releases share one filesystem, and 250MB per upgrade adds up.
  previous_model=$(find "$RELEASES" -mindepth 3 -maxdepth 3 -path "*/models/$MODEL_NAME" ! -path "$model" 2>/dev/null | head -1)
  if [[ -n $previous_model && -f $previous_model/config.json ]]; then
    step "NER model: linking from ${previous_model}"
    cp -al "$previous_model" "$model"
  else
    step "NER model: downloading from ${MODEL_ORIGIN}"
    # The manifest lists every file with its digest; every path in it must
    # be a plain relative path under the model directory before it is used
    # as an output path, since this runs as root, and the whole download is
    # verified in a staging directory before it moves into place.
    tmp=$(mktemp -d "$rel/models/.download.XXXXXX")
    (
      cd "$tmp"
      curl -fsSL "${MODEL_ORIGIN}/checksums.txt" -o checksums.txt
      while read -r digest path; do
        [[ -n $path ]] || continue
        case "$path" in
          "$MODEL_NAME"/*) ;;
          *) echo "install: manifest path '$path' is outside $MODEL_NAME" >&2; exit 1 ;;
        esac
        case "/$path/" in
          *'/../'*|*'//'*) echo "install: manifest path '$path' is not a plain relative path" >&2; exit 1 ;;
        esac
        [[ $digest =~ ^[0-9a-f]{64}$ ]] || { echo "install: manifest digest for '$path' is not sha256" >&2; exit 1; }
        curl -fsSL "${MODEL_ORIGIN}/${path}" --create-dirs -o "$path"
      done <checksums.txt
      sha256sum -c --quiet checksums.txt
    ) || { rm -rf "$tmp"; die "model download failed"; }
    [[ -f $tmp/$MODEL_NAME/config.json ]] || { rm -rf "$tmp"; die "${MODEL_ORIGIN} does not publish $MODEL_NAME"; }
    mv "$tmp/$MODEL_NAME" "$model"
    rm -rf "$tmp"
  fi
fi
for f in config.json model.onnx tokenizer.json; do
  [[ -f $model/$f ]] || die "NER model is missing $f"
done
chmod -R a+rX "$rel"

step "onnx runtime ${ORT_VERSION} into ${ORT_DIR}"
mkdir -p "$(dirname "$ORT_DIR")"
staged_ort=$(mktemp -d "$(dirname "$ORT_DIR")/.${ORT_VERSION}.XXXXXX")
mkdir -p "$staged_ort/lib"
cp -a "$here"/ort/lib/libonnxruntime* "$staged_ort/lib/"
install -m 0644 "$here/ort/LICENSE" "$here/ort/ThirdPartyNotices.txt" "$staged_ort/"
[[ -e $staged_ort/lib/libonnxruntime.so ]] || { rm -rf "$staged_ort"; die "the bundle's ort/lib has no libonnxruntime.so"; }
chmod -R a+rX "$staged_ort"

step "probing the engine from the tarball"
# A -validate run resolves the lane without binding a socket or loading the
# model, so it is a cheap check that this binary links the engine with ONNX
# Runtime compiled in, before it replaces anything. JSON, because the format
# follows the extension and a temporary file has none.
probe=$(mktemp)
trap 'rm -f "$probe"' EXIT
echo '{"listeners":[{"name":"probe","protocol":"ica","listen":"127.0.0.1:2598","upstream":"127.0.0.1:2599","ica":{"tenant_id":"probe"}}]}' >"$probe"
if ! out=$("$here/hoop-ica" -validate -config "$probe" 2>&1); then
  echo "$out" >&2
  rm -rf "$staged_ort"
  die "hoop-ica cannot serve an ica lane"
fi
grep -q 'onnx runtime compiled in: true' <<<"$out" || { rm -rf "$staged_ort"; die "this hoop-ica was built without ONNX Runtime; the bundle is for the ORT build"; }

step "user and config"
id hoop >/dev/null 2>&1 || useradd --system --user-group --shell /usr/sbin/nologin hoop
install -d -o root -g hoop -m 0750 "$CONF_DIR"
if [[ ! -f $ENV_FILE ]]; then
  touch "$ENV_FILE"
  chown root:hoop "$ENV_FILE"
  chmod 0640 "$ENV_FILE"
fi
# A --config candidate is validated by the binary about to be installed, as
# the service would run it, before it replaces the live file: a rejected
# config never becomes what the next restart loads. Validation reads the
# engine's defaults, so it runs with `current` and the binary still pointing
# at the previous release; the engine loads nothing at validate.
candidate=
if [[ -n $CONFIG ]]; then
  candidate=$(mktemp "$CONF_DIR/.config.XXXXXX.yaml")
  trap 'rm -f "$probe" "$candidate"' EXIT
  cp "$CONFIG" "$candidate"
  chown root:hoop "$candidate"
  chmod 0640 "$candidate"
  step "validating ${CONFIG} as the service would see it"
  as_service "$here/hoop-ica" -validate -config "$candidate" || { rm -rf "$staged_ort"; die "--config $CONFIG did not validate; nothing was changed"; }
fi

step "switching to release ${VERSION}"
rm -rf "$ORT_DIR"
mv "$staged_ort" "$ORT_DIR"
# A running hoop-ica keeps its mapped text; install unlinks the old file
# before writing the new one, so this never hits ETXTBSY.
install -m 0755 "$here/hoop-ica" "$BIN"
ln -sfn "releases/${VERSION}" "$CURRENT"
echo "    ${CURRENT} -> releases/${VERSION}"
if [[ -n $candidate ]]; then
  mv "$candidate" "$CONF"
  candidate=
  echo "    installed ${CONFIG} as ${CONF}"
elif [[ ! -f $CONF ]]; then
  # The single-mode example from the tarball, with its EDIT markers: the
  # --start branch below refuses to run it until they are gone.
  install -o root -g hoop -m 0640 "$here/examples/config.single.yaml" "$CONF"
  echo "    wrote examples/config.single.yaml to ${CONF}; edit upstream and tenant_id (examples/config.cidr.yaml for CIDR mode, examples/config.gateway-direct.yaml for a Gateway that dials this host)"
fi

# Releases older than the one this run replaced go: the previous one stays
# as the rollback target (point `current` back at it and restart).
for old in "$RELEASES"/*/; do
  old=${old%/}
  [[ -d $old ]] || continue
  [[ $old == "$rel" || $old == "$previous_release" ]] && continue
  echo "    removing old release $(basename "$old")"
  rm -rf "$old"
done

step "service"
capability='#AmbientCapabilities=CAP_NET_ADMIN
#CapabilityBoundingSet=CAP_NET_ADMIN'
if [[ $CIDR -eq 1 ]]; then
  capability='AmbientCapabilities=CAP_NET_ADMIN
CapabilityBoundingSet=CAP_NET_ADMIN'
fi
cat >/etc/systemd/system/${UNIT}.service <<EOF
[Unit]
Description=hoop sidecar (hoop-ica) with the Citrix ICA engine
Wants=network-online.target
After=network-online.target

[Service]
User=hoop
Group=hoop
# HOOP_LICENSE and the control plane variables, if you use them.
EnvironmentFile=-${ENV_FILE}
ExecStart=${BIN} -config ${CONF}
Restart=on-failure
# Each ica lane drains its sessions for up to 30s on SIGTERM.
TimeoutStopSec=45
NoNewPrivileges=true
# CIDR mode: IP_TRANSPARENT needs CAP_NET_ADMIN. systemd grants it to the
# non-root process as an ambient capability (--cidr turns it on).
${capability}

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload

if [[ $START -eq 0 ]]; then
  # `restart`, not `enable --now`: a unit that is already running keeps the
  # old binary and assets until it restarts.
  cat <<EOF

Installed. Next:
  runuser -u hoop -- ${BIN} -validate -config ${CONF}
  systemctl enable ${UNIT} && systemctl restart ${UNIT}
EOF
  exit 0
fi

# The template above is a valid config: left unedited and started, it would
# accept any client on every interface and forward it to the placeholder.
if grep -q '# EDIT:' "$CONF"; then
  die "--start: ${CONF} is still the template; edit upstream and tenant_id (or pass --config) first"
fi
step "validating ${CONF} as the service"
as_service "$BIN" -validate -config "$CONF"
admin=$(admin_listen)
[[ -n $admin ]] || die "--start: ${CONF} has no admin.listen, so there is no /readyz to wait on; start it with 'systemctl enable ${UNIT} && systemctl restart ${UNIT}'"
host=${admin%:*}
port=${admin##*:}
case "$host" in ''|0.0.0.0|'[::]'|::) host=127.0.0.1 ;; esac
ready_url="http://${host}:${port}/readyz"

step "starting ${UNIT}"
systemctl enable "$UNIT"
systemctl restart "$UNIT"
step "waiting for ${ready_url} (the NER model and OCR workers load first)"
for _ in $(seq 180); do
  if curl -sf "$ready_url" >/dev/null; then
    echo "    ready"
    exit 0
  fi
  systemctl is-active --quiet "$UNIT" || { journalctl -u "$UNIT" -n 30 --no-pager >&2; die "${UNIT} exited"; }
  sleep 5
done
journalctl -u "$UNIT" -n 30 --no-pager >&2
die "not ready after 15 minutes: $(curl -s "$ready_url")"
