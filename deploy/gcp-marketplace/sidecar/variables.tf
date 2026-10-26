# The deploy form is built from metadata.yaml and metadata.display.yaml. After
# adding, removing or renaming a variable here, regenerate metadata.yaml with
#   cft blueprint metadata -p . -q --nested=false
# put back its spec.info.source and spec.requirements blocks (-q drops them),
# add the variable to metadata.display.yaml by hand, and check both with
#   cft blueprint metadata -p . -v

variable "project_id" {
  description = "The project the cluster is in."
  type        = string
}

# --- Set by Marketplace at publish time; scripts/gcp-marketplace/sidecar.sh
# --- writes defaults for a CLI install from our own registry.

variable "helm_chart_repo" {
  description = "OCI repository of the sidecar chart, for example oci://us-docker.pkg.dev/PROJECT/REPO."
  type        = string
}

variable "helm_chart_name" {
  description = "Name of the sidecar chart in helm_chart_repo."
  type        = string
}

variable "helm_chart_version" {
  description = "Version of the sidecar chart."
  type        = string
}

variable "image_repo" {
  description = "Sidecar image repository, registry included. Declared in schema.yaml."
  type        = string
}

variable "image_tag" {
  description = "Sidecar image tag. Declared in schema.yaml."
  type        = string
}

variable "consumption_tracking_label" {
  description = "The product's Producer Portal consumption tracking label. Empty adds no label."
  type        = string
  default     = ""
}

# --- Cluster

variable "create_cluster" {
  description = "Create a new GKE Autopilot cluster instead of using an existing one."
  type        = bool
  default     = false
}

variable "cluster_name" {
  description = "Name of the GKE cluster."
  type        = string
}

variable "cluster_location" {
  description = "Region or zone of the GKE cluster. A new Autopilot cluster needs a region."
  type        = string
}

variable "namespace" {
  description = "Namespace for the sidecar. Created when missing."
  type        = string
  default     = "hoop-sidecar"
}

# Marketplace requires this name and sets it per deployment. At most 45
# characters: the chart names the lane Service <name>-hoopsidecar-lanes, and a
# Service name is at most 63.
variable "goog_cm_deployment_name" {
  description = "Name of the deployment, used as the Helm release name."
  type        = string
  validation {
    condition     = can(regex("^[a-z]([-a-z0-9]{0,43}[a-z0-9])?$", var.goog_cm_deployment_name))
    error_message = "goog_cm_deployment_name must be lowercase letters, digits and hyphens, start with a letter, and be at most 45 characters."
  }
}

# --- Sidecar

variable "config_source" {
  description = "Where the sidecar config comes from: simple (the fields below), document (a whole config), configmap (an existing ConfigMap) or control_plane."
  type        = string
  default     = "simple"
}

variable "protocol" {
  description = "simple: protocol of the resource."
  type        = string
  default     = "postgres"
}

variable "upstream" {
  description = "simple: the resource the sidecar fronts, as host:port."
  type        = string
  default     = ""
}

variable "service_port" {
  description = "simple: port clients connect to. 0 uses the protocol's usual port."
  type        = number
  default     = 0
}

variable "guardrail_preset" {
  description = "simple: none (audit only), or read-only to deny every statement that writes or changes the database. read-only is for postgres only."
  type        = string
  default     = "none"
}

variable "config_document" {
  description = "document: the whole sidecar config as JSON or YAML. admin.listen must be 0.0.0.0:19000."
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

variable "control_plane_token" {
  description = "control_plane: the sidecar token from the control plane."
  type        = string
  default     = ""
  sensitive   = true
}

# A string, not list(number): the Marketplace deploy form refuses list(number).
variable "lane_ports" {
  description = "configmap and control_plane: listener ports the lane Service publishes, comma separated, e.g. 15432,18080."
  type        = string
  default     = ""
  validation {
    condition     = can(regex("^\\s*([0-9]+\\s*(,\\s*[0-9]+\\s*)*)?$", var.lane_ports))
    error_message = "lane_ports must be port numbers separated by commas, e.g. 15432,18080."
  }
}

variable "service_type" {
  description = "Lane Service type: ClusterIP, or InternalLoadBalancer to reach it from the VPC."
  type        = string
  default     = "ClusterIP"
}

variable "license" {
  description = "Hoop license document. Empty runs the free tier: one guardrail rule and one masking rule. Ignored in control_plane mode, where the organization's license applies."
  type        = string
  default     = ""
  sensitive   = true
}

variable "cpu" {
  description = "CPU request and limit of the sidecar."
  type        = string
  default     = "500m"
}

variable "memory" {
  description = "Memory request and limit of the sidecar."
  type        = string
  default     = "512Mi"
}

variable "replicas" {
  description = "Number of sidecar pods. Each is an independent proxy; the lane Service spreads connections."
  type        = number
  default     = 1
}
