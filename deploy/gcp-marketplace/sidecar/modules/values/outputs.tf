output "values_yaml" {
  description = "Helm values documents for the sidecar chart, in order."
  value       = compact([local.base_values, local.config_values, local.standalone_values, local.lane_values])

  precondition {
    condition     = var.config_source != "simple" || can(regex("^(\\[[0-9A-Fa-f:.]+\\]|[^\\s:\\[\\]]+):[0-9]+$", var.upstream))
    error_message = "upstream must be host:port, for example appdb.default.svc.cluster.local:5432."
  }
  precondition {
    condition     = var.config_source != "document" || length(try(local.document.listeners, [])) > 0
    error_message = "config_document is empty or names no listeners: the sidecar does not start without one."
  }
  precondition {
    condition     = var.config_source != "document" || try(endswith(local.document.admin.listen, ":19000"), false)
    error_message = "config_document must set admin.listen to '0.0.0.0:19000': the pod is probed on /healthz there."
  }
  precondition {
    condition     = alltrue([for p in local.document_port : p != null])
    error_message = "every tcp listener in config_document needs a port in listen, for example 0.0.0.0:15432."
  }
  precondition {
    condition     = alltrue([for p in local.lane_ports : try(p.port >= 1 && p.port <= 65535 && p.targetPort >= 1 && p.targetPort <= 65535, false)])
    error_message = "every port (service_port, lane_ports, listener ports in config_document) must be between 1 and 65535."
  }
  precondition {
    condition     = var.config_source != "configmap" || var.existing_config_map != ""
    error_message = "config_source = configmap needs existing_config_map."
  }
  precondition {
    condition     = var.config_source != "control_plane" || var.control_plane_url != ""
    error_message = "config_source = control_plane needs control_plane_url."
  }
}

output "lane_ports" {
  description = "Ports the lane Service publishes."
  value       = [for p in local.lane_ports : p.port]
}
