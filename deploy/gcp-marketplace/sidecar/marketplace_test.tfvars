# Marketplace verification passes this file as --var-file. It must not set
# project_id, helm_chart_*, or a variable declared in schema.yaml.
#
# The upstream does not exist. The pod still goes ready: /healthz answers
# without it, and a lane dials its upstream only when a client connects.
create_cluster   = true
cluster_name     = "hoop-sidecar-test"
cluster_location = "us-central1"
upstream         = "db.invalid:5432"
