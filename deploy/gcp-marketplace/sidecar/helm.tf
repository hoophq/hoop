provider "helm" {
  kubernetes {
    host                   = "https://${local.cluster_endpoint}"
    token                  = data.google_client_config.default.access_token
    cluster_ca_certificate = base64decode(local.cluster_ca)
  }
  # The chart is an OCI artifact in Artifact Registry. The deploying identity's
  # own token reads it, so a private repository works for a test install. A
  # chart from a local checkout (helm_chart_repo empty, helm_chart_name a
  # path) needs no login.
  dynamic "registry" {
    for_each = startswith(var.helm_chart_repo, "oci://") ? [var.helm_chart_repo] : []
    content {
      url      = "oci://${split("/", trimprefix(registry.value, "oci://"))[0]}"
      username = "oauth2accesstoken"
      password = data.google_client_config.default.access_token
    }
  }
}

module "values" {
  source = "./modules/values"

  config_source       = var.config_source
  protocol            = var.protocol
  upstream            = var.upstream
  service_port        = var.service_port
  guardrail_preset    = var.guardrail_preset
  config_document     = var.config_document
  existing_config_map = var.existing_config_map
  control_plane_url   = var.control_plane_url
  lane_ports          = [for p in split(",", var.lane_ports) : tonumber(trimspace(p)) if trimspace(p) != ""]
  service_type        = var.service_type
  image_repository    = var.image_repo
  image_tag           = var.image_tag
  cpu                 = var.cpu
  memory              = var.memory
  replicas            = var.replicas
  pod_labels = var.consumption_tracking_label == "" ? {} : {
    goog-partner-solution = var.consumption_tracking_label
  }
}

# set_sensitive, not a values document: helm provider 2.x masks only
# set_sensitive paths in the release metadata and its logs. Its value goes
# through Helm's strvals parser, where a comma ends the value and a leading
# { starts a list, and a license is JSON. Escaped, it arrives byte for byte.
locals {
  sensitive_sets = {
    license              = var.license
    "controlPlane.token" = var.control_plane_token
  }
}

resource "helm_release" "sidecar" {
  name             = local.release_name
  namespace        = var.namespace
  create_namespace = true

  repository = var.helm_chart_repo
  chart      = var.helm_chart_name
  version    = var.helm_chart_version

  values = module.values.values_yaml

  dynamic "set_sensitive" {
    for_each = { for k, v in local.sensitive_sets : k => v if nonsensitive(v != "") }
    content {
      name  = set_sensitive.key
      type  = "string"
      value = replace(replace(replace(set_sensitive.value, "\\", "\\\\"), ",", "\\,"), "{", "\\{")
    }
  }

  wait    = true
  timeout = 600

  lifecycle {
    precondition {
      condition     = var.config_source != "control_plane" || var.control_plane_token != ""
      error_message = "config_source = control_plane needs control_plane_token."
    }
  }
}
