# The chart probes its own health port, so a config document may leave admin
# off. When it sets admin, the port must be 19000: the admin Service targets it.

variables {
  upstream         = "appdb.default.svc.cluster.local:5432"
  image_repository = "docker.io/hoophq/hoopsidecar"
  image_tag        = "1.0.0-distroless"
  config_source    = "document"
}

run "document_without_admin_is_accepted" {
  variables {
    config_document = "{\"listeners\": [{\"name\": \"pg\", \"protocol\": \"postgres\", \"listen\": \"0.0.0.0:15432\", \"upstream\": \"appdb:5432\"}]}"
  }
  assert {
    condition     = alltrue([for d in output.values_yaml : try(yamldecode(d).config.admin, null) == null])
    error_message = "a config document without admin rendered values that turn admin on"
  }
  assert {
    condition     = anytrue([for d in output.values_yaml : try(yamldecode(d).service.enabled == false, false)])
    error_message = "admin off left the admin Service on, targeting a port nothing binds"
  }
}

run "document_with_admin_on_another_port_is_refused" {
  command = plan
  variables {
    config_document = "{\"admin\": {\"listen\": \"0.0.0.0:9901\"}, \"listeners\": [{\"name\": \"pg\", \"protocol\": \"postgres\", \"listen\": \"0.0.0.0:15432\", \"upstream\": \"appdb:5432\"}]}"
  }
  expect_failures = [output.values_yaml]
}

run "document_with_a_scalar_admin_is_refused" {
  command = plan
  variables {
    config_document = "{\"admin\": \"0.0.0.0:19000\", \"listeners\": [{\"name\": \"pg\", \"protocol\": \"postgres\", \"listen\": \"0.0.0.0:15432\", \"upstream\": \"appdb:5432\"}]}"
  }
  expect_failures = [output.values_yaml]
}

run "document_with_admin_on_19000_is_accepted" {
  variables {
    config_document = "{\"admin\": {\"listen\": \"0.0.0.0:19000\"}, \"listeners\": [{\"name\": \"pg\", \"protocol\": \"postgres\", \"listen\": \"0.0.0.0:15432\", \"upstream\": \"appdb:5432\"}]}"
  }
  assert {
    condition     = anytrue([for d in output.values_yaml : try(yamldecode(d).config.admin.listen == "0.0.0.0:19000", false)])
    error_message = "admin on 19000 did not reach the chart values"
  }
  assert {
    condition     = alltrue([for d in output.values_yaml : try(yamldecode(d).service.enabled, true) != false])
    error_message = "admin on 19000 turned the admin Service off"
  }
}
