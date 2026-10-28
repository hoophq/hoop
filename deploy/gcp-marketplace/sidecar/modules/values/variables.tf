variable "config_source" {
  description = "Where the sidecar config comes from: simple, document, configmap or control_plane."
  type        = string
  default     = "simple"
  validation {
    condition     = contains(["simple", "document", "configmap", "control_plane"], var.config_source)
    error_message = "config_source must be simple, document, configmap or control_plane."
  }
}

variable "protocol" {
  description = "simple: protocol of the one listener."
  type        = string
  default     = "postgres"
  validation {
    condition     = contains(["postgres", "mysql", "mssql", "mongodb", "oracle", "clickhouse", "http"], var.protocol)
    error_message = "protocol must be postgres, mysql, mssql, mongodb, oracle, clickhouse or http. Other protocols need config_source = document."
  }
}

variable "upstream" {
  description = "simple: the resource the sidecar fronts, as host:port."
  type        = string
  default     = ""
}

variable "service_port" {
  description = "simple: port clients connect to on the lane Service. 0 uses the protocol's usual port."
  type        = number
  default     = 0
}

variable "guardrail_preset" {
  description = "simple: none, or read-only, one rule that denies the write operations. read-only is for postgres only."
  type        = string
  default     = "none"
  validation {
    condition     = contains(["none", "read-only"], var.guardrail_preset)
    error_message = "guardrail_preset must be none or read-only. Another rule needs config_source = document."
  }
}

variable "config_document" {
  description = "document: the whole sidecar config, YAML or JSON."
  type        = string
  default     = ""
}

variable "existing_config_map" {
  description = "configmap: name of a ConfigMap in the namespace whose key config.yaml is the config."
  type        = string
  default     = ""
}

variable "control_plane_url" {
  description = "control_plane: URL of the hoop control plane."
  type        = string
  default     = ""
}

variable "lane_ports" {
  description = "configmap and control_plane: ports the lane Service publishes. Each must be a listener port in the config."
  type        = list(number)
  default     = []
}

variable "service_type" {
  description = "Type of the lane Service: ClusterIP or InternalLoadBalancer."
  type        = string
  default     = "ClusterIP"
  validation {
    condition     = contains(["ClusterIP", "InternalLoadBalancer"], var.service_type)
    error_message = "service_type must be ClusterIP or InternalLoadBalancer."
  }
}

variable "image_repository" {
  description = "Sidecar image repository, registry included."
  type        = string
}

variable "image_tag" {
  description = "Sidecar image tag."
  type        = string
}

variable "pod_labels" {
  description = "Extra labels on the sidecar pod."
  type        = map(string)
  default     = {}
}

variable "cpu" {
  description = "CPU request and limit of the sidecar container."
  type        = string
  default     = "500m"
}

variable "replicas" {
  description = "Number of sidecar pods. Each is an independent proxy."
  type        = number
  default     = 1
  validation {
    condition     = var.replicas >= 1
    error_message = "replicas must be 1 or more."
  }
}

variable "memory" {
  description = "Memory request and limit of the sidecar container."
  type        = string
  default     = "512Mi"
}
