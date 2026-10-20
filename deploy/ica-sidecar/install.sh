#!/usr/bin/env bash
# Put a `protocol: ica` lane on a Linux host without Docker: install
# hoop-inspect with the libhoop-ica engine and what the engine reads at run
# time, at the paths it reads by default, and run it under systemd.
#
# This script ships inside hoop-ica-assets_<version>_Linux_<arch>.tar.gz on
# releases.hoop.dev, and it lays out what hoop's `Dockerfile.sidecar --target
# ica` lays out, so a host and the -ica image run the same binary against the
# same assets from the same places:
#
#   /usr/local/bin/hoop-inspect                       cgo, -tags ORT
#   /opt/libhoop-ica/releases/<version>/bin/          hoop-tesseract-worker
#   /opt/libhoop-ica/releases/<version>/models/       the NER model
#   /opt/libhoop-ica/current -> releases/<version>    what the engine reads
#   /opt/alcatraz/ort/releases/<ort>/lib/             libonnxruntime.so
#   /usr/share/tesseract-ocr/5/tessdata               from the distro package
#
# Usage, as root on Ubuntu 24.04 or Debian 12 (amd64 or arm64), from the
# extracted tarball:
#
#   ./install.sh [options]
#
#   --config FILE       install FILE as /etc/hoop-inspect/config.yaml; without
#                       it an existing config stays and a new host gets a
#                       single-mode template to edit
#   --model-dir DIR     copy the NER model from DIR instead of downloading it
#                       (air-gapped hosts). DIR is a KnightsAnalytics_distilbert-NER
#                       directory: config.json, model.onnx, tokenizer.json, ...
#   --model-origin URL  download the model from URL instead of
#                       https://releases.hoop.dev/alcatraz/current
#   --cidr              CIDR (transparent) mode: grant the service CAP_NET_ADMIN.
#                       The TPROXY rule, policy route and rp_filter stay yours;
#                       see libhoop-ica's docs/transparent-linux.md
#   --start             validate the config, start the service and wait for
#                       /readyz (the model load takes minutes)
#
# The worker is dynamically linked against the distro's libtesseract 5, so the
# host must be a distribution of that generation; the script installs the
# package. The model is ~250MB and the same for every architecture; a release
# already on the host lends its copy, so an upgrade does not download it again.
#
# Rerunning upgrades hoop-inspect, the worker and ONNX Runtime, and switches
# `current`. The model, the config and /etc/hoop-inspect/env stay unless
# --config replaces the config.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)

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

ADMIN_URL=${ADMIN_URL:-http://127.0.0.1:19000}
RELEASES=/opt/libhoop-ica/releases
ORT_DIR=/opt/alcatraz/ort/releases/${ORT_VERSION}
rel=$RELEASES/$VERSION
model=$rel/models/KnightsAnalytics_distilbert-NER

step "packages: tesseract 5 and English tessdata"
export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y --no-install-recommends ca-certificates curl tesseract-ocr tesseract-ocr-eng libtesseract5

step "onnx runtime ${ORT_VERSION} into ${ORT_DIR}"
rm -rf "$ORT_DIR" && mkdir -p "$ORT_DIR/lib"
cp -a "$here"/ort/lib/libonnxruntime* "$ORT_DIR/lib/"
install -m 0644 "$here/ort/LICENSE" "$here/ort/ThirdPartyNotices.txt" "$ORT_DIR/"
[[ -e $ORT_DIR/lib/libonnxruntime.so ]] || die "the bundle's ort/lib has no libonnxruntime.so"

step "worker and licences from this tarball into ${rel}"
mkdir -p "$rel/bin" "$rel/models"
install -m 0755 "$here/bin/hoop-tesseract-worker" "$rel/bin/"
install -m 0644 "$here/bin/hoop-tesseract-worker.txt" "$rel/bin/"
install -m 0644 "$here/LICENSE" "$here/NOTICE" "$here/release.txt" "$rel/"
if ldd "$rel/bin/hoop-tesseract-worker" | grep -q 'not found'; then
  ldd "$rel/bin/hoop-tesseract-worker" | grep 'not found' >&2
  die "the worker links libraries this host does not have"
fi

if [[ -f $model/config.json ]]; then
  step "NER model: already at ${model}"
elif [[ -n $MODEL_DIR ]]; then
  step "NER model: copying from ${MODEL_DIR}"
  cp -a "$MODEL_DIR" "$model"
else
  # Reuse a model an earlier release already downloaded before fetching 250MB.
  previous=$(find "$RELEASES" -mindepth 3 -maxdepth 3 -path '*/models/KnightsAnalytics_distilbert-NER' ! -path "$model" 2>/dev/null | head -1)
  if [[ -n $previous && -f $previous/config.json ]]; then
    step "NER model: copying from ${previous}"
    cp -a "$previous" "$model"
  else
    step "NER model: downloading from ${MODEL_ORIGIN}"
    # The same loop as the Dockerfile's models stage: the manifest lists
    # every file with its digest, and a mismatch fails the install.
    tmp=$(mktemp -d "$rel/models/.download.XXXXXX")
    (
      cd "$tmp"
      curl -fsSL "${MODEL_ORIGIN}/checksums.txt" -o checksums.txt
      while read -r _ path; do
        [[ -n $path ]] || continue
        curl -fsSL "${MODEL_ORIGIN}/${path}" --create-dirs -o "$path"
      done <checksums.txt
      sha256sum -c --quiet checksums.txt
    ) || { rm -rf "$tmp"; die "model download failed"; }
    [[ -d $tmp/KnightsAnalytics_distilbert-NER ]] || { rm -rf "$tmp"; die "${MODEL_ORIGIN} does not publish KnightsAnalytics_distilbert-NER"; }
    mv "$tmp/KnightsAnalytics_distilbert-NER" "$model"
    rm -rf "$tmp"
  fi
fi
chmod -R a+rX "$rel" "$ORT_DIR"

step "verifying the assets the engine reads by default"
tessdata=/usr/share/tesseract-ocr/5/tessdata
[[ -x $rel/bin/hoop-tesseract-worker ]] || die "worker is not executable"
command -v tesseract >/dev/null || die "tesseract CLI is missing"
[[ -f $tessdata/eng.traineddata ]] || die "English tessdata is missing under $tessdata"
for f in config.json model.onnx tokenizer.json; do
  [[ -f $model/$f ]] || die "NER model is missing $f"
done
ln -sfn "releases/${VERSION}" /opt/libhoop-ica/current
echo "    /opt/libhoop-ica/current -> releases/${VERSION}"

step "hoop-inspect $("$here/hoop-inspect" -version 2>/dev/null || echo "${VERSION}")"
if ldd "$here/hoop-inspect" | grep -q 'not found'; then
  ldd "$here/hoop-inspect" | grep 'not found' >&2
  die "hoop-inspect links libraries this host does not have"
fi
install -m 0755 "$here/hoop-inspect" /usr/local/bin/hoop-inspect
# A -validate run resolves the lane against the engine's defaults without
# binding a socket or loading the model: a cheap check that the binary links
# the engine and sees the paths above. JSON, because the format follows the
# extension and a temporary file has none.
probe=$(mktemp)
trap 'rm -f "$probe"' EXIT
echo '{"listeners":[{"name":"probe","protocol":"ica","listen":"127.0.0.1:2598","upstream":"127.0.0.1:2599","ica":{"tenant_id":"probe"}}]}' >"$probe"
if ! out=$(/usr/local/bin/hoop-inspect -validate -config "$probe" 2>&1); then
  echo "$out" >&2
  die "hoop-inspect cannot serve an ica lane"
fi

step "user, config and service"
id hoop >/dev/null 2>&1 || useradd --system --user-group --shell /usr/sbin/nologin hoop
install -d -o root -g hoop -m 0750 /etc/hoop-inspect
if [[ -n $CONFIG ]]; then
  install -o root -g hoop -m 0640 "$CONFIG" /etc/hoop-inspect/config.yaml
elif [[ ! -f /etc/hoop-inspect/config.yaml ]]; then
  cat >/etc/hoop-inspect/config.yaml <<'EOF'
# Single mode: one lane serves one VDA. For CIDR mode, replace `upstream` with
#   ica:
#     tenant_id: citrix-production
#     transparent:
#       source_cidrs: [10.10.1.10/32]    # Citrix Gateway SNIP
#       upstream_cidrs: [10.77.0.0/16]   # VDA network
#       upstream_ports: [2598]           # CGP
# and rerun install.sh with --cidr.
log_level: info
admin:
  listen: 0.0.0.0:19000          # /healthz, /readyz, /stats, /api/sessions
audit:
  file: '-'                      # one JSON row per line, into the journal
ica: {}                          # production profile and default asset paths
listeners:
  - name: vdi
    protocol: ica
    listen: 0.0.0.0:2598
    upstream: 10.77.0.30:2598      # EDIT: the VDA this lane serves
    ica:
      tenant_id: finance           # EDIT: the principal on every audit row
EOF
  chown root:hoop /etc/hoop-inspect/config.yaml
  chmod 0640 /etc/hoop-inspect/config.yaml
  echo "    wrote a single-mode template to /etc/hoop-inspect/config.yaml; edit upstream and tenant_id"
fi
if [[ ! -f /etc/hoop-inspect/env ]]; then
  touch /etc/hoop-inspect/env
  chown root:hoop /etc/hoop-inspect/env
  chmod 0640 /etc/hoop-inspect/env
fi

capability='#AmbientCapabilities=CAP_NET_ADMIN
#CapabilityBoundingSet=CAP_NET_ADMIN'
if [[ $CIDR -eq 1 ]]; then
  capability='AmbientCapabilities=CAP_NET_ADMIN
CapabilityBoundingSet=CAP_NET_ADMIN'
fi
cat >/etc/systemd/system/hoop-inspect.service <<EOF
[Unit]
Description=hoop sidecar (hoop-inspect) with the Citrix ICA engine
Wants=network-online.target
After=network-online.target

[Service]
User=hoop
Group=hoop
# HOOP_LICENSE and the control plane variables, if you use them.
EnvironmentFile=-/etc/hoop-inspect/env
ExecStart=/usr/local/bin/hoop-inspect -config /etc/hoop-inspect/config.yaml
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
  cat <<EOF

Installed. Next:
  runuser -u hoop -- hoop-inspect -validate -config /etc/hoop-inspect/config.yaml
  systemctl enable --now hoop-inspect && curl -s ${ADMIN_URL}/readyz
EOF
  exit 0
fi

step "validating /etc/hoop-inspect/config.yaml"
runuser -u hoop -- /usr/local/bin/hoop-inspect -validate -config /etc/hoop-inspect/config.yaml
step "starting hoop-inspect"
systemctl enable hoop-inspect
systemctl restart hoop-inspect
step "waiting for ${ADMIN_URL}/readyz (the NER model and OCR workers load first)"
for _ in $(seq 180); do
  if curl -sf "${ADMIN_URL}/readyz" >/dev/null; then
    echo "    ready"
    exit 0
  fi
  systemctl is-active --quiet hoop-inspect || { journalctl -u hoop-inspect -n 30 --no-pager >&2; die "hoop-inspect exited"; }
  sleep 5
done
journalctl -u hoop-inspect -n 30 --no-pager >&2
die "not ready after 15 minutes: $(curl -s "${ADMIN_URL}/readyz")"
