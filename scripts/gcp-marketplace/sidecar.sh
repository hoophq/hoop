#!/usr/bin/env bash
# Packages the hoop sidecar for the GCP Marketplace Terraform Kubernetes app
# listing (RD-413). Run on demand, from the repo root:
#
#   scripts/gcp-marketplace/sidecar.sh setup             once per project
#   scripts/gcp-marketplace/sidecar.sh bundle            the module zip only, no cloud
#   scripts/gcp-marketplace/sidecar.sh publish           image + chart + zip to the partner project
#   scripts/gcp-marketplace/sidecar.sh test              install what publish pushed on GKE, then remove it
#
# Nothing here makes anything public. The Artifact Registry repository and the
# bucket stay private; Marketplace copies from them only after the release is
# submitted in Producer Portal, which stays a manual step.
#
# Settings come from the environment:
#
#   VERSION            hoop release, e.g. 1.237.0 (bundle, publish, test)
#   SERVICE_NAME       Producer Portal service name, e.g. hoop-sidecar.endpoints.hoopdev-public.cloud.goog (publish)
#   CONSUMPTION_LABEL  Producer Portal consumption tracking label, isol_plb32_... (bundle, publish)
#   GCP_PROJECT        partner project              [hoopdev-public]
#   AR_REPO            Artifact Registry repository [hoop-sidecar], always in us-docker.pkg.dev
#   BUCKET             bucket for the module zip    [$GCP_PROJECT-hoop-sidecar-terraform]
#   BUCKET_LOCATION    location of a new bucket     [US]
#   DIST_FOLDER        local output                 [./dist]
#   CLUSTER_NAME, CLUSTER_LOCATION    existing GKE cluster for `test`
#   CREATE_CLUSTER=1   `test` creates an Autopilot cluster instead (slow)
#   KEEP=1             `test` leaves the install running
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
CHART="$ROOT/deploy/helm-chart/chart/sidecar"

GCP_PROJECT=${GCP_PROJECT:-hoopdev-public}
AR_REPO=${AR_REPO:-hoop-sidecar}
BUCKET=${BUCKET:-$GCP_PROJECT-hoop-sidecar-terraform}
BUCKET_LOCATION=${BUCKET_LOCATION:-US}
DIST_FOLDER=${DIST_FOLDER:-$ROOT/dist}

# Fixed: Marketplace accepts a Terraform Kubernetes app's images and chart
# only from us-docker.pkg.dev.
AR_LOCATION=us
REGISTRY="$AR_LOCATION-docker.pkg.dev"
AR_PATH="$REGISTRY/$GCP_PROJECT/$AR_REPO"
# Marketplace requires the primary image and the chart side by side in one
# repository; schema.yaml names the image key "hoopsidecar".
IMAGE="$AR_PATH/hoopsidecar"
CHART_NAME=hoopsidecar-chart
SOURCE_IMAGE=docker.io/hoophq/hoopsidecar

die() { echo "error: $*" >&2; exit 1; }
step() { echo "==> $*"; }

need() {
  local t
  for t in "$@"; do
    command -v "$t" >/dev/null || die "$t is not installed"
  done
}

# Copies the module's files as git tracks them, and nothing else: a local
# terraform.tfvars, a saved plan or a state file must never reach the zip.
stage_module() {
  local files
  files=$(git -C "$ROOT" ls-files -- deploy/gcp-marketplace/sidecar)
  [[ -n $files ]] || die "git tracks no file under deploy/gcp-marketplace/sidecar"
  rm -rf "$1" && mkdir -p "$1"
  (cd "$ROOT" && printf '%s\n' "$files" | tar -cf - -T -) \
    | tar -C "$1" -xf - --strip-components 3
}

# terraform or tofu, whichever is installed. Both run this module.
tf() {
  if command -v terraform >/dev/null; then terraform "$@"
  elif command -v tofu >/dev/null; then tofu "$@"
  else die "terraform or tofu is required"
  fi
}

require_version() {
  [[ ${VERSION:-} =~ ^([0-9]+)\.([0-9]+)\.[0-9]+$ ]] \
    || die "VERSION must be a hoop release like 1.237.0, got '${VERSION:-}'"
  # Marketplace keys a release on MAJOR.MINOR: the chart and every image
  # carry that tag, and Producer Portal's release display tag must match it.
  TRACK="${BASH_REMATCH[1]}.${BASH_REMATCH[2]}"
}

require() {
  local v
  for v in "$@"; do
    [[ -n ${!v:-} ]] || die "$v is not set"
  done
}

gcp_token() { gcloud auth print-access-token; }

cmd_setup() {
  need gcloud
  step "enabling Artifact Registry and Cloud Storage in $GCP_PROJECT"
  gcloud services enable artifactregistry.googleapis.com storage.googleapis.com --project "$GCP_PROJECT"

  if gcloud artifacts repositories describe "$AR_REPO" --location "$AR_LOCATION" --project "$GCP_PROJECT" >/dev/null 2>&1; then
    step "repository $AR_PATH exists"
  else
    step "creating private repository $AR_PATH"
    gcloud artifacts repositories create "$AR_REPO" --repository-format docker \
      --location "$AR_LOCATION" --project "$GCP_PROJECT" \
      --description "hoop sidecar: GCP Marketplace Terraform Kubernetes app"
  fi

  if gcloud storage buckets describe "gs://$BUCKET" --project "$GCP_PROJECT" >/dev/null 2>&1; then
    step "bucket gs://$BUCKET exists"
  else
    step "creating private bucket gs://$BUCKET"
    gcloud storage buckets create "gs://$BUCKET" --project "$GCP_PROJECT" \
      --location "$BUCKET_LOCATION" --uniform-bucket-level-access --public-access-prevention
  fi
  # Marketplace refuses a module bucket without versioning.
  gcloud storage buckets update "gs://$BUCKET" --versioning >/dev/null
  step "setup done"
}

# Stages the module with the listing's consumption label and zips it. The
# zip holds the module files at its root, as Marketplace requires.
cmd_bundle() {
  need zip
  require_version
  require CONSUMPTION_LABEL
  local stage="$DIST_FOLDER/gcp-marketplace-sidecar-$VERSION"
  ZIP="$DIST_FOLDER/hoop-sidecar-terraform-$VERSION.zip"
  rm -f "$ZIP"
  stage_module "$stage"
  # An override file, not a tfvars file: Terraform merges *_override.tf into
  # the variable's declaration, so the label is the default whatever var
  # files the deployer passes.
  cat >"$stage/listing_override.tf" <<EOF
# Written by scripts/gcp-marketplace/sidecar.sh for release $VERSION.
variable "consumption_tracking_label" {
  default = "$CONSUMPTION_LABEL"
}
EOF
  step "validating the staged module"
  tf -chdir="$stage" init -backend=false -input=false >/dev/null
  tf -chdir="$stage" validate >/dev/null
  rm -rf "$stage/.terraform" "$stage/.terraform.lock.hcl"
  (cd "$stage" && zip -qr "$ZIP" .)
  step "module zip: $ZIP"
}

cmd_publish() {
  need gcloud crane helm zip
  require_version
  require SERVICE_NAME CONSUMPTION_LABEL

  step "logging in to $REGISTRY"
  gcp_token | crane auth login "$REGISTRY" -u oauth2accesstoken --password-stdin >/dev/null
  gcp_token | helm registry login "$REGISTRY" -u oauth2accesstoken --password-stdin >/dev/null

  # amd64 only: Marketplace GKE apps run on x86 nodes. The distroless flavour
  # carries no OS packages, so a CVE notice on it is a hoop release, not a
  # rebuild of somebody's base image.
  step "copying $SOURCE_IMAGE:$VERSION-distroless (linux/amd64) to $IMAGE:$VERSION"
  crane copy --platform linux/amd64 "$SOURCE_IMAGE:$VERSION-distroless" "$IMAGE:$VERSION"
  # The annotation changes the manifest, so it gets a new digest; both tags
  # are written after it.
  step "annotating with services/$SERVICE_NAME"
  crane mutate "$IMAGE:$VERSION" \
    --annotation "com.googleapis.cloudmarketplace.product.service.name=services/$SERVICE_NAME" \
    -t "$IMAGE:$VERSION" >/dev/null
  crane tag "$IMAGE:$VERSION" "$TRACK"

  step "pushing $CHART_NAME:$TRACK"
  local chartdir="$DIST_FOLDER/gcp-marketplace-chart-$VERSION"
  rm -rf "$chartdir" && mkdir -p "$chartdir"
  helm package "$CHART" --version "$TRACK" --app-version "$VERSION" --destination "$chartdir" >/dev/null
  helm push "$chartdir/$CHART_NAME-$TRACK.tgz" "oci://$AR_PATH"

  cmd_bundle
  local object="gs://$BUCKET/hoop-sidecar-terraform-$VERSION.zip"
  step "uploading $object"
  gcloud storage cp "$ZIP" "$object" --project "$GCP_PROJECT"

  cat <<EOF

Published release $TRACK ($VERSION). Nothing is public yet.

Producer Portal -> the sidecar product -> Deployment configuration:
  Helm chart:     $AR_PATH/$CHART_NAME
  Release:        display tag $TRACK, module $object
  Then:           Save and validate (Marketplace runs terraform plan with marketplace_test.tfvars)
EOF
}

cmd_test() {
  need gcloud kubectl
  require_version
  local stage="$DIST_FOLDER/gcp-marketplace-test-$VERSION"
  # The state of an earlier test (KEEP=1, or a failed destroy) is the only
  # record of what it created. Never stage over it.
  if [[ -s $stage/terraform.tfstate ]] && grep -q '"resources": \[$' "$stage/terraform.tfstate"; then
    die "$stage holds the state of an earlier test install. Remove it first: terraform -chdir=$stage destroy (same -var flags), then delete the directory"
  fi
  stage_module "$stage"

  local cluster=()
  if [[ ${CREATE_CLUSTER:-} == 1 ]]; then
    cluster=(-var create_cluster=true -var "cluster_name=${CLUSTER_NAME:-hoop-sidecar-test}" -var "cluster_location=${CLUSTER_LOCATION:-us-central1}")
  else
    require CLUSTER_NAME CLUSTER_LOCATION
    cluster=(-var "cluster_name=$CLUSTER_NAME" -var "cluster_location=$CLUSTER_LOCATION")
  fi
  local vars=(
    -var "project_id=$GCP_PROJECT"
    -var "helm_chart_repo=oci://$AR_PATH"
    -var "helm_chart_name=$CHART_NAME"
    -var "helm_chart_version=$TRACK"
    -var "image_repo=$IMAGE"
    -var "image_tag=$TRACK"
    -var "consumption_tracking_label=${CONSUMPTION_LABEL:-}"
    -var "upstream=${UPSTREAM:-db.invalid:5432}"
    -var "goog_cm_deployment_name=hoop-sidecar-test"
    "${cluster[@]}"
  )
  step "installing $CHART_NAME:$TRACK with Terraform (the pod must go ready with no database behind it)"
  tf -chdir="$stage" init -input=false >/dev/null
  if ! tf -chdir="$stage" apply -auto-approve -input=false "${vars[@]}"; then
    step "install failed; removing what was created"
    tf -chdir="$stage" destroy -auto-approve -input=false "${vars[@]}" || true
    die "test install failed"
  fi
  tf -chdir="$stage" output
  if [[ ${KEEP:-} == 1 ]]; then
    step "KEEP=1: left running. Remove it with: terraform -chdir=$stage destroy ${vars[*]}"
    return
  fi
  step "removing the test install"
  tf -chdir="$stage" destroy -auto-approve -input=false "${vars[@]}"
  step "test passed"
}

case ${1:-} in
  setup) cmd_setup ;;
  bundle) cmd_bundle ;;
  publish) cmd_publish ;;
  test) cmd_test ;;
  *) sed -n '2,28p' "$0" | sed 's/^# \{0,1\}//'; exit 2 ;;
esac
