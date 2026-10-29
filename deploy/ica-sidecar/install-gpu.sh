#!/usr/bin/env bash
# Native Linux amd64 installer for Dockerfile.sidecar's ica-gpu-assets target.
# GPU driver is host-owned. All inference/runtime userspace assets and the
# NER model come from the verified bundle. No CPU OCR/provider fallback.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
die() { echo "install-gpu: $*" >&2; exit 1; }
CONFIG=
CIDR=0
START=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --config) CONFIG=${2:?--config needs a value}; shift 2 ;;
    --cidr) CIDR=1; shift ;;
    --start) START=1; shift ;;
    -h|--help) echo './install.sh --config FILE [--cidr] [--start]'; exit 0 ;;
    *) die "unknown GPU installer option $1" ;;
  esac
done
[[ $EUID -eq 0 && $(uname -s) == Linux && $(uname -m) == x86_64 ]] || die 'requires root on Linux amd64'
[[ -n $CONFIG && -f $CONFIG ]] || die '--config is required; edit a GPU example first'
if grep -q '# EDIT:' "$CONFIG"; then die 'remove each EDIT marker after configuring its value'; fi
version=$(sed -n 's/^version=//p' "$here/release.txt")
[[ $version =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || die 'invalid release version'
command -v nvidia-smi >/dev/null || die 'install the host NVIDIA driver first'
nvidia-smi >/dev/null || die 'NVIDIA driver is unavailable'
export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y --no-install-recommends ca-certificates curl python3 python3-yaml libstdc++6
python3 "$here/gpu-assets.py" verify-tree "$here/gpu"
for f in config.json model.onnx tokenizer.json; do
  [[ -f $here/models/KnightsAnalytics_distilbert-NER/$f ]] || die "bundle is missing NER model $f"
done
id hoop >/dev/null 2>&1 || useradd --system --user-group --shell /usr/sbin/nologin hoop
# NVIDIA devices commonly use video/render. Do not chmod devices globally.
for group in video render; do
  if getent group "$group" >/dev/null; then usermod -a -G "$group" hoop; fi
done
rel=/opt/libhoop-ica/releases/${version}-gpu
[[ ! -e $rel ]] || die "$rel already exists; use a new release version (existing assets are never overwritten)"
install -d -m 0755 "$rel/bin" "$rel/models"
cp -a "$here/gpu" "$rel/gpu"
cp -a "$here/models/KnightsAnalytics_distilbert-NER" "$rel/models/"
install -m 0755 "$here/hoop-ica" "$rel/bin/hoop-ica"
install -m 0644 "$here/release.txt" "$rel/release.txt"
chmod -R a+rX "$rel"
# Preflight with the service identity before changing current/config/binary.
install -o root -g hoop -m 0640 "$CONFIG" "$rel/config.yaml"
# -validate also checks explicit local paths. On a first install current does
# not exist yet, so validate the exact same profile against staged asset paths.
# Keep the original YAML for the service after the current symlink switches.
python3 - "$rel" "$START" "$CIDR" <<'PYCONFIG'
import json
from pathlib import Path
import sys
import yaml
release = Path(sys.argv[1])
cfg = yaml.safe_load((release / "config.yaml").read_text())
options = cfg.get("ica", {}).get("options", {})
if options.get("ocr_backend") != "turboocr" or options.get("ner_backend") != "ort" or options.get("ner_accelerator") != "cuda":
    raise SystemExit("GPU installer requires turboocr OCR and ort/cuda NER")
admin = str(cfg.get("admin", {}).get("listen", ""))
if sys.argv[2] == "1" and not admin.startswith("127.0.0.1:"):
    raise SystemExit("--start requires admin.listen on 127.0.0.1")
if sys.argv[3] != "1" and any(l.get("ica", {}).get("transparent") is not None for l in cfg.get("listeners", [])):
    raise SystemExit("transparent listeners require --cidr and configured TPROXY routes")
with (release / "admin-listen").open("x") as stream:
    stream.write(admin)
for key in ("ocr_turbo_library", "ocr_turbo_det_model", "ocr_turbo_rec_model", "ocr_turbo_rec_dict", "ocr_turbo_cls_model", "ner_model", "ner_ort_library"):
    value = cfg["ica"].get(key, "")
    prefix = "/opt/libhoop-ica/current/"
    if value.startswith(prefix):
        cfg["ica"][key] = str(release / value[len(prefix):])
with (release / "preflight.json").open("x") as stream:
    json.dump(cfg, stream)
PYCONFIG
chown root:hoop "$rel/preflight.json"
chmod 0640 "$rel/preflight.json"
libs="$rel/gpu/lib:$rel/gpu/ort/lib"
as_service() {
  local line key value
  local -a vars=()
  if [[ -f /etc/hoop-inspect/env ]]; then
    while IFS= read -r line || [[ -n $line ]]; do
      [[ $line =~ ^[[:space:]]*([A-Za-z_][A-Za-z0-9_]*)=(.*)$ ]] || continue
      key=${BASH_REMATCH[1]}
      value=${BASH_REMATCH[2]}
      [[ $value == \"*\" ]] && value=${value#\"} && value=${value%\"}
      vars+=("$key=$value")
    done </etc/hoop-inspect/env
  fi
  runuser -u hoop -- env "${vars[@]}" LD_LIBRARY_PATH="$libs" HOOP_ICA_GPU_ASSETS="$rel/gpu" "$@"
}
for library in "$rel"/gpu/lib/*.so* "$rel"/gpu/ort/lib/*.so* "$rel/bin/hoop-ica" "$rel/gpu/bin/hoop-gpuframe-smoke"; do
  deps=$(env LD_LIBRARY_PATH="$libs" ldd "$library" 2>&1) || die "cannot inspect native dependencies in $library"
  if grep -q 'not found' <<<"$deps"; then die "unresolved native dependencies in $library"; fi
done
as_service "$rel/gpu/bin/hoop-gpuframe-smoke" \
  --det "$rel/gpu/engines/det.engine" --rec "$rel/gpu/engines/rec.engine" \
  --dict "$rel/gpu/engines/dict.txt" --image "$rel/gpu/smoke/fixture.ppm" \
  --expect "$(cat "$rel/gpu/smoke/expected.txt")"
out=$(as_service "$rel/bin/hoop-ica" -validate -config "$rel/preflight.json" 2>&1)
echo "$out"
grep -q 'onnx runtime compiled in: true' <<<"$out" || die 'binary was built without ORT'
grep -q 'ocr turboocr' <<<"$out" || die '--config must select ocr_backend: turboocr'
grep -q 'ner accelerator cuda' <<<"$out" || die '--config must select ner_accelerator: cuda'
install -d -o root -g hoop -m 0750 /etc/hoop-inspect
install -d -o hoop -g hoop -m 0750 /var/log/hoop
# Keep configuration and secrets separate. No text inspector is enabled.
if [[ ! -f /etc/hoop-inspect/env ]]; then
  install -o root -g hoop -m 0640 /dev/null /etc/hoop-inspect/env
fi
if [[ -e /opt/libhoop-ica/current && ! -L /opt/libhoop-ica/current ]]; then
  die '/opt/libhoop-ica/current must be a symlink, not a directory'
fi
ln -sfn "releases/${version}-gpu" /opt/libhoop-ica/current
# Unlink before installing to allow replacement of a running ELF binary.
install -m 0755 "$rel/bin/hoop-ica" /usr/local/bin/hoop-ica
install -o root -g hoop -m 0640 "$rel/config.yaml" /etc/hoop-inspect/config.yaml
capability=
if [[ $CIDR -eq 1 ]]; then capability='AmbientCapabilities=CAP_NET_ADMIN
CapabilityBoundingSet=CAP_NET_ADMIN'; fi
cat >/etc/systemd/system/hoop-ica.service <<EOF
[Unit]
Description=hoop ICA GPU sidecar
Wants=network-online.target
After=network-online.target
[Service]
User=hoop
Group=hoop
Environment=LD_LIBRARY_PATH=/opt/libhoop-ica/current/gpu/lib:/opt/libhoop-ica/current/gpu/ort/lib
EnvironmentFile=-/etc/hoop-inspect/env
ExecStart=/usr/local/bin/hoop-ica -config /etc/hoop-inspect/config.yaml
Restart=on-failure
TimeoutStopSec=45
NoNewPrivileges=true
${capability}
[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
if [[ $START -eq 0 ]]; then
  echo 'Installed. Start with: systemctl enable hoop-ica && systemctl restart hoop-ica'
  exit 0
fi
# /readyz includes native OCR and CUDA NER initialization. Provider failure
# remains an error; validation above deliberately loads no inference models.
admin=$(cat "$rel/admin-listen")
systemctl enable hoop-ica
systemctl restart hoop-ica
for _ in $(seq 180); do
  if curl -fsS "http://${admin}/readyz" >/dev/null; then echo 'GPU sidecar ready'; exit 0; fi
  systemctl is-active --quiet hoop-ica || die 'service exited; inspect journalctl -u hoop-ica'
  sleep 5
done
die 'not ready after 15 minutes; inspect journalctl -u hoop-ica (no CPU fallback)'
