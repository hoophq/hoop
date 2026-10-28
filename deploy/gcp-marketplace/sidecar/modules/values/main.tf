# Turns the deploy form into values for deploy/helm-chart/chart/sidecar.
#
# It has no providers and no resources, so it runs anywhere: the publish
# script applies it on a laptop and renders the chart with its output, without
# a cluster or a GCP credential. Secrets (license, control plane token) are
# not handled here; the root module passes them with set_sensitive.
#
# Each piece is a separate YAML document rather than one object. The pieces
# have different shapes per config_source, and Terraform cannot unify object
# types of different shapes in one conditional.

locals {
  # listen: the port the relay binds in the pod, the convention every config
  # under deploy/docker-compose/*/sidecar/ uses. service: the port a client of
  # that protocol expects, so the lane Service looks like the real resource.
  protocol_ports = {
    postgres   = { listen = 15432, service = 5432 }
    mysql      = { listen = 13306, service = 3306 }
    mssql      = { listen = 11433, service = 1433 }
    mongodb    = { listen = 17017, service = 27017 }
    oracle     = { listen = 11521, service = 1521 }
    clickhouse = { listen = 19006, service = 9000 }
    http       = { listen = 18080, service = 80 }
  }
  simple_ports = local.protocol_ports[var.protocol]

  # The admin port is fixed: the chart probes /healthz on 19000 and refuses a
  # config that puts admin anywhere else.
  admin_listen = "0.0.0.0:19000"

  simple_config = {
    admin = { listen = local.admin_listen }
    audit = { file = "-" }
    listeners = [{
      name     = var.protocol
      protocol = var.protocol
      listen   = "0.0.0.0:${local.simple_ports.listen}"
      upstream = var.upstream
    }]
  }

  # yamldecode reads JSON as well: JSON is YAML.
  document = var.config_source == "document" && trimspace(var.config_document) != "" ? yamldecode(var.config_document) : null
  # The sidecar reads an absent, null or empty network as tcp.
  document_tcp  = [for l in try(local.document.listeners, []) : l if try(coalesce(l.network, "tcp"), "tcp") == "tcp"]
  document_port = [for l in local.document_tcp : try(tonumber(regex(":([0-9]+)$", l.listen)[0]), null)]

  # One Service port per TCP listener, named by position: a port name is at
  # most 15 characters and a listener name is not.
  lane_ports = {
    simple = [{
      name       = "lane-0"
      port       = var.service_port > 0 ? var.service_port : local.simple_ports.service
      targetPort = local.simple_ports.listen
    }]
    document      = [for i, p in local.document_port : { name = "lane-${i}", port = p, targetPort = p }]
    configmap     = [for i, p in var.lane_ports : { name = "lane-${i}", port = p, targetPort = p }]
    control_plane = [for i, p in var.lane_ports : { name = "lane-${i}", port = p, targetPort = p }]
  }[var.config_source]

  base_values = yamlencode({
    # No pullPolicy: the chart's default is Always, and Marketplace deploys
    # the MAJOR.MINOR tag, which a CVE rebuild moves to a new image.
    image = {
      repository = var.image_repository
      tag        = var.image_tag
    }
    podLabels = var.pod_labels
    replicas  = var.replicas
    resources = {
      requests = { cpu = var.cpu, memory = var.memory }
      limits   = { cpu = var.cpu, memory = var.memory }
    }
  })

  config_values = {
    simple        = yamlencode({ config = local.simple_config })
    document      = yamlencode({ config = local.document })
    configmap     = yamlencode({ existingConfigMap = var.existing_config_map })
    control_plane = yamlencode({ controlPlane = { url = var.control_plane_url } })
  }[var.config_source]

  # A sidecar that runs its own file has nobody to approve a login code, so
  # the free tier runs with no login (ENG-906). control_plane has a credential.
  standalone_values = var.config_source == "control_plane" ? "" : yamlencode({
    extraSecret = { HOOP_SIDECAR_FREE_STANDALONE = "true" }
  })

  lane_values = length(local.lane_ports) == 0 ? "" : yamlencode({
    laneServices = {
      lanes = {
        enabled = true
        type    = var.service_type == "InternalLoadBalancer" ? "LoadBalancer" : "ClusterIP"
        # The lanes of most protocols are plaintext, so the only load balancer
        # offered is an internal one: reachable inside the VPC, never public.
        annotations = var.service_type == "InternalLoadBalancer" ? { "networking.gke.io/load-balancer-type" = "Internal" } : {}
        ports       = local.lane_ports
      }
    }
  })
}
