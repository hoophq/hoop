---
name: gcp-marketplace-publish
description: Publish a hoop release of the sidecar to the GCP Marketplace Terraform Kubernetes app listing (RD-413) - copy the image, push the chart, upload the Terraform module, test it on GKE, then guide the Producer Portal steps. Use when asked to update, rebuild or publish the GCP Marketplace sidecar listing, or after a Marketplace CVE notice for it.
---

# Publish the sidecar to GCP Marketplace

On demand, not per release: after a Marketplace CVE notice, or when a release
is worth showing. The listing is a showcase; keep it on a recent release.

Everything runs through `scripts/gcp-marketplace/sidecar.sh` (make targets
`gcp-marketplace-sidecar-{setup,bundle,publish,test}`). Read its header for
every setting. Do not reproduce its steps by hand.

Nothing here makes anything public. Publishing to customers is a Producer
Portal action that only the operator takes.

## 0. Inputs

Ask the operator for anything missing; never guess these:

| Setting | Where it comes from |
|---|---|
| `VERSION` | The hoop release, e.g. `1.237.0`. Must exist as `hoophq/hoopsidecar:<VERSION>-distroless` on Docker Hub |
| `SERVICE_NAME` | Producer Portal → Overview → the sidecar product → Service name |
| `CONSUMPTION_LABEL` | Producer Portal → Overview → Consumption tracking label (`isol_plb32_...`) |
| `GCP_PROJECT` | Default `hoopdev-public`, the partner project |

Tools: `gcloud` (logged in, with Artifact Registry, Storage and GKE rights on
the project), `crane`, `go`, `helm`, `kubectl`, `zip`, and `terraform` or `tofu`.
On Nix: `nix-shell -p google-cloud-sdk crane kubernetes-helm kubectl zip opentofu go`.

Check the release exists, and which Go built it, before anything else:

```bash
mkdir -p /tmp/hoop-img
crane export --platform linux/amd64 docker.io/hoophq/hoopsidecar:$VERSION-distroless - | tar -xf - -C /tmp/hoop-img app/hoop
go version /tmp/hoop-img/app/hoop    # e.g. go1.26.8
```

Marketplace scans the image and rejects a release with any known
vulnerability, including Go standard library CVEs that cannot affect Linux.
If https://go.dev/doc/devel/release lists a newer patch release of that Go
with security fixes, stop: the repo's toolchain (`go.work`, `go.mod`) must be
raised and a new hoop release made first. Publishing anyway only fails the
scan two hours later.

## 1. Setup (first time per project only)

```bash
make gcp-marketplace-sidecar-setup
```

Creates a private Artifact Registry repository and a private, versioned
bucket. Safe to run again.

## 2. Publish

```bash
make gcp-marketplace-sidecar-publish VERSION=$VERSION SERVICE_NAME=... CONSUMPTION_LABEL=...
```

Pushes the amd64 distroless image with the Marketplace annotation, tagged
`<VERSION>` and `MAJOR.MINOR`; the chart as `hoopsidecar-chart:MAJOR.MINOR`;
and the module zip. It prints the values Producer Portal needs. Keep them.

The same `MAJOR.MINOR` again overwrites that release. That is how a CVE
rebuild of a patch release is published.

## 3. Test before Producer Portal

```bash
make gcp-marketplace-sidecar-test VERSION=$VERSION CLUSTER_NAME=... CLUSTER_LOCATION=...
```

Installs what step 2 pushed, through the same Terraform module Marketplace
runs, on an existing GKE cluster, with an upstream that does not exist. The
pod must go ready anyway (`/healthz` does not need the upstream). Then it
removes the install. `CREATE_CLUSTER=1` makes an Autopilot cluster instead;
that alone takes about 10 minutes. `KEEP=1` leaves the install for a manual
look.

Run `test` with a command timeout of at least 30 minutes. An interrupted run
leaves cloud resources behind and its state in
`dist/gcp-marketplace-test-<VERSION>/`; the next `test` refuses to start until
that state is destroyed. A cluster still being created when the run stopped is
not in the state: wait for it to be RUNNING, `terraform import` it as
`google_container_cluster.sidecar[0]`, then `apply` and `destroy` there.

The cluster's nodes must be able to pull from the private repository: a
cluster in the partner project can.

Do not continue to step 4 on a failed test. Report the error.

## 4. Producer Portal (the operator does this)

Tell the operator, with the values from step 2:

1. Producer Portal → the sidecar product → **Deployment configuration**.
2. **Specify Helm chart**: the chart URL.
3. **Image release** → **Specify releases** → new release: display tag
   `MAJOR.MINOR`, a title, a one-line description, and the module's `gs://`
   path. Make it the default release, and delete releases whose image failed
   a scan: validation checks every listed release.
4. **Save and validate**. Marketplace runs `terraform plan` with
   `marketplace_test.tfvars` and scans the images; it takes up to two hours.
   After uploading a zip again under the same path, select the module again
   in the release: Producer Portal can keep validating the older copy.
5. Submit for review only when the operator wants it public.

## Report

The release, the image digest (`crane digest <image>:<VERSION>`), the chart
tag, the module path, and the test result. Nothing else.
