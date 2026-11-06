# A sidecar that runs its own file has nobody to approve a login code, so every
# mode but control_plane sets HOOP_SIDECAR_FREE_STANDALONE (ENG-906). Run with
#   terraform -chdir=deploy/gcp-marketplace/sidecar/modules/values init -backend=false
#   terraform -chdir=deploy/gcp-marketplace/sidecar/modules/values test

variables {
  upstream         = "appdb.default.svc.cluster.local:5432"
  image_repository = "docker.io/hoophq/hoopsidecar"
  image_tag        = "1.0.0-distroless"
}

run "simple_runs_free_standalone" {
  variables {
    config_source = "simple"
  }
  assert {
    condition     = anytrue([for d in output.values_yaml : try(yamldecode(d).extraSecret.HOOP_SIDECAR_FREE_STANDALONE == "true", false)])
    error_message = "simple mode does not set HOOP_SIDECAR_FREE_STANDALONE"
  }
}

run "document_runs_free_standalone" {
  variables {
    config_source   = "document"
    config_document = "{\"admin\": {\"listen\": \"0.0.0.0:19000\"}, \"listeners\": [{\"name\": \"pg\", \"protocol\": \"postgres\", \"listen\": \"0.0.0.0:15432\", \"upstream\": \"appdb:5432\"}]}"
  }
  assert {
    condition     = anytrue([for d in output.values_yaml : try(yamldecode(d).extraSecret.HOOP_SIDECAR_FREE_STANDALONE == "true", false)])
    error_message = "document mode does not set HOOP_SIDECAR_FREE_STANDALONE"
  }
}

run "configmap_runs_free_standalone" {
  variables {
    config_source       = "configmap"
    existing_config_map = "sidecar-config"
  }
  assert {
    condition     = anytrue([for d in output.values_yaml : try(yamldecode(d).extraSecret.HOOP_SIDECAR_FREE_STANDALONE == "true", false)])
    error_message = "configmap mode does not set HOOP_SIDECAR_FREE_STANDALONE"
  }
}

run "control_plane_keeps_its_credential" {
  variables {
    config_source     = "control_plane"
    control_plane_url = "https://cp.example.com"
  }
  assert {
    condition     = !anytrue([for d in output.values_yaml : can(yamldecode(d).extraSecret)])
    error_message = "control_plane mode sets an extraSecret; it runs with a credential"
  }
}
