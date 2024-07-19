output "default_client_id" {
  description = "The defualt app Oauth2 client id"
  value       = zitadel_application_oidc.default.client_id
  sensitive   = true
}

output "default_client_secret" {
  description = "The default app Oauth2 client secret"
  value       = zitadel_application_oidc.default.client_secret
  sensitive   = true
}