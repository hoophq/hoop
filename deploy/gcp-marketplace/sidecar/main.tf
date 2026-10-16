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

locals {
  # Marketplace gives every deployment its own name, so two deployments in one
  # namespace never share a release. A release name that contains
  # "hoopsidecar" is used as is by the chart; any other gets the suffix.
  release_name = var.goog_cm_deployment_name
  fullname     = length(regexall("hoopsidecar", local.release_name)) > 0 ? local.release_name : "${local.release_name}-hoopsidecar"
}
