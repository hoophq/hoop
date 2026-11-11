output "namespace" {
  description = "Namespace of the sidecar."
  value       = helm_release.sidecar.namespace
}

output "release_name" {
  description = "Helm release of the sidecar."
  value       = helm_release.sidecar.name
}

output "lane_service" {
  description = "Service clients connect to. Empty when no lane port is published."
  value       = length(module.values.lane_ports) == 0 ? "" : "${local.fullname}-lanes"
}

output "lane_ports" {
  description = "Ports of the lane Service."
  value       = module.values.lane_ports
}

output "admin_service" {
  description = "Service of the admin server on 19000: /healthz, /stats, /config."
  value       = local.fullname
}
