# The cluster the sidecar goes to: an existing one, or a new Autopilot
# cluster when create_cluster is true. Marketplace verification uses the
# second path (marketplace_test.tfvars).

resource "google_project_service" "container" {
  count              = var.create_cluster ? 1 : 0
  service            = "container.googleapis.com"
  disable_on_destroy = false
}

resource "google_container_cluster" "sidecar" {
  count               = var.create_cluster ? 1 : 0
  name                = var.cluster_name
  location            = var.cluster_location
  enable_autopilot    = true
  deletion_protection = false

  ip_allocation_policy {}

  depends_on = [google_project_service.container]
}

data "google_container_cluster" "existing" {
  count    = var.create_cluster ? 0 : 1
  name     = var.cluster_name
  location = var.cluster_location
}

# Two locals rather than one object: a resource and a data source have
# different schemas, so a conditional between the two whole objects fails.
locals {
  cluster_endpoint = var.create_cluster ? google_container_cluster.sidecar[0].endpoint : data.google_container_cluster.existing[0].endpoint
  cluster_ca       = var.create_cluster ? google_container_cluster.sidecar[0].master_auth[0].cluster_ca_certificate : data.google_container_cluster.existing[0].master_auth[0].cluster_ca_certificate
}
