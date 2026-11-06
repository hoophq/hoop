# The hoop sidecar for this agent: the GCP Marketplace module, behind an
# internal load balancer the agent reaches over PSC-I.
#
# By default it is the sidecar alone: its rules come from sidecar.yaml, with
# no license and no control plane. The one change that moves it to a hoop
# control plane later is to set control_plane_url and control_plane_token;
# register the sidecar in the plane first, with its listener and rules,
# because this module's control_plane mode carries no local document to seed
# the plane.
module "hoop_sidecar" {
  # A release tag, never a branch. Terraform takes no variable here: keep
  # sidecar_version below on the same release.
  source = "github.com/hoophq/hoop//deploy/gcp-marketplace/sidecar?ref=1.281.0"

  project_id       = var.project_id
  cluster_name     = var.cluster_name
  cluster_location = var.cluster_location

  # The chart from hoop's release bucket, not ghcr.io: for an oci:// repository
  # the module logs in with a Google token, which ghcr.io refuses.
  helm_chart_repo    = ""
  helm_chart_name    = "https://releases.hoop.dev/release/${var.sidecar_version}/hoopsidecar-chart-${var.sidecar_version}.tgz"
  helm_chart_version = var.sidecar_version
  image_repo         = "docker.io/hoophq/hoopsidecar"
  image_tag          = "${var.sidecar_version}-distroless"

  goog_cm_deployment_name = "appdb-sidecar"
  config_source           = local.on_plane ? "control_plane" : "document"
  config_document         = local.on_plane ? "" : file("${path.module}/sidecar.yaml")
  control_plane_url       = var.control_plane_url
  control_plane_token     = var.control_plane_token
  lane_ports              = "15432"
  service_type            = "InternalLoadBalancer"
}

locals {
  on_plane = var.control_plane_url != ""
}

variable "project_id" { type = string }
variable "cluster_name" { type = string }
variable "cluster_location" { type = string }

variable "sidecar_version" {
  description = "hoop release of the sidecar chart and image. Keep it on the module's ref."
  type        = string
  default     = "1.281.0"
}

# Empty: the sidecar alone. Set both to connect it to a control plane.
variable "control_plane_url" {
  type    = string
  default = ""
}
variable "control_plane_token" {
  type      = string
  default   = ""
  sensitive = true
}

output "lane_service" { value = module.hoop_sidecar.lane_service }
