# GCP Marketplace Terraform Kubernetes app for the hoop sidecar.
#
# Marketplace runs this module through Infrastructure Manager. It installs
# deploy/helm-chart/chart/sidecar on a GKE cluster, new or existing. At publish
# time Marketplace replaces helm_chart_* and the variables named in
# schema.yaml with its own copies of the chart and the image.

provider "google" {
  project = var.project_id
  # Consumption tracking on what this module creates itself (the cluster
  # when create_cluster is true). The pod carries the same label below.
  default_labels = var.consumption_tracking_label == "" ? {} : {
    goog-partner-solution = var.consumption_tracking_label
  }
}

data "google_client_config" "default" {}

resource "random_string" "suffix" {
  length  = 4
  special = false
  upper   = false
}

locals {
  # A release name that contains "hoopsidecar" is used as is by the chart,
  # so every resource is named after the release and nothing else.
  release_name = var.helm_release_name != "" ? var.helm_release_name : "hoopsidecar-${random_string.suffix.result}"
  fullname     = length(regexall("hoopsidecar", local.release_name)) > 0 ? local.release_name : "${local.release_name}-hoopsidecar"
}
