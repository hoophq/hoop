locals {
  provider_insecure = var.tls_mode == "enabled" ? "false" : "true"
  provider_port     = var.tls_mode == "enabled" ? "443" : "80"
  public_url        = var.tls_mode == "enabled" ? "https://${var.public_hostname}" : "http://${var.public_hostname}"
}

terraform {
  backend "local" {
    path = "/hoopdata/terraform.tfstate"
  }
  required_providers {
    zitadel = {
      source  = "zitadel/zitadel"
      version = "1.2.0"
    }
  }
}

provider "zitadel" {
  domain           = var.public_hostname
  insecure         = local.provider_insecure
  port             = local.provider_port
  jwt_profile_file = "/hoopdata/zitadel-admin-sa.json"
}

resource "zitadel_org" "default" {
  name       = "hoophq"
  is_default = true
}

resource "zitadel_privacy_policy" "default" {
  org_id        = zitadel_org.default.id
  tos_link      = "https://hoop.dev/docs/legal/tos"
  privacy_link  = "https://hoop.dev/docs/legal/privacy"
  help_link     = "https://help.hoop.dev"
  support_email = "help@hoop.dev"
}

resource "zitadel_login_policy" "default" {
  org_id                        = zitadel_org.default.id
  user_login                    = true
  allow_register                = false
  allow_external_idp            = false
  force_mfa                     = false
  force_mfa_local_only          = false
  passwordless_type             = "PASSWORDLESS_TYPE_ALLOWED" # TODO: don't know the other values for this attribute
  hide_password_reset           = true
  password_check_lifetime       = "240h0m0s"
  external_login_check_lifetime = "240h0m0s"
  multi_factor_check_lifetime   = "24h0m0s"
  mfa_init_skip_lifetime        = "720h0m0s"
  second_factor_check_lifetime  = "24h0m0s"
  ignore_unknown_usernames      = false
  default_redirect_uri          = local.public_url
  second_factors                = ["SECOND_FACTOR_TYPE_OTP", "SECOND_FACTOR_TYPE_U2F"]
  multi_factors                 = ["MULTI_FACTOR_TYPE_U2F_WITH_VERIFICATION"]
  idps                          = []
  allow_domain_discovery        = true
  disable_login_with_email      = false
  disable_login_with_phone      = true
}

resource "zitadel_default_label_policy" "default" {
  primary_color          = "#5469d4"
  warn_color             = "#cd3d56"
  background_color       = "#fafafa"
  font_color             = "#000000"
  primary_color_dark     = "#a5b4fc"
  background_color_dark  = "#111827"
  warn_color_dark        = "#ff3b5b"
  font_color_dark        = "#ffffff"
  hide_login_name_suffix = true
  disable_watermark      = true
  set_active             = true
  logo_hash              = filemd5("./imgs/hoop_symbol_text_black_vertical_4x.png")
  logo_path              = "./imgs/hoop_symbol_text_black_vertical_4x.png"
  icon_hash              = filemd5("./imgs/icon.png")
  icon_path              = "./imgs/icon.png"
  theme_mode             = "THEME_MODE_LIGHT"
}

resource "zitadel_login_texts" "default" {
  org_id   = zitadel_org.default.id
  language = "en"

  login_text = {
    description                 = " "
    description_linking_process = ""
    external_user_description   = ""
    login_name_label            = "Email"
    login_name_placeholder      = "user@domain.tld"
    next_button_text            = "Next"
    register_button_text        = "Register"
    title                       = "Login with your User or Email"
    title_linking_process       = ""
    user_must_be_member_of_org  = "The user must be member of the {{.OrgName}} organization."
    user_name_placeholder       = "username"
  }
}

resource "zitadel_password_complexity_policy" "default" {
  org_id        = zitadel_org.default.id
  min_length    = "8"
  has_uppercase = true
  has_lowercase = true
  has_number    = true
  has_symbol    = false
}

resource "zitadel_project" "default" {
  name                     = "hoopdev"
  org_id                   = zitadel_org.default.id
  project_role_assertion   = true
  project_role_check       = true
  has_project_check        = true
  private_labeling_setting = "PRIVATE_LABELING_SETTING_ENFORCE_PROJECT_RESOURCE_OWNER_POLICY"
}

resource "zitadel_application_oidc" "default" {
  project_id = zitadel_project.default.id
  org_id     = zitadel_org.default.id

  name = "hoopdev-default-app"
  redirect_uris = [
    "http://127.0.0.1/api/callback",
    "https://127.0.0.1/api/callback",
    "${local.public_url}/api/callback",
  ]
  response_types = ["OIDC_RESPONSE_TYPE_CODE"]
  grant_types    = ["OIDC_GRANT_TYPE_AUTHORIZATION_CODE"]

  app_type                    = "OIDC_APP_TYPE_WEB"
  auth_method_type            = "OIDC_AUTH_METHOD_TYPE_BASIC"
  version                     = "OIDC_VERSION_1_0"
  clock_skew                  = "0s"
  dev_mode                    = false
  access_token_type           = "OIDC_TOKEN_TYPE_BEARER"
  access_token_role_assertion = false
  id_token_role_assertion     = true
  id_token_userinfo_assertion = true
  additional_origins          = []
}

resource "zitadel_domain" "default" {
  org_id     = zitadel_org.default.id
  name       = "hoop.local"
  is_primary = false
}

resource "zitadel_human_user" "default" {
  org_id             = zitadel_org.default.id
  user_name          = "admin"
  first_name         = "Admin"
  last_name          = "Hoop"
  nick_name          = "admin"
  display_name       = "Hoop Admin"
  preferred_language = "en"
  gender             = "GENDER_MALE"
  is_phone_verified  = false
  email              = "admin@hoop.local"
  is_email_verified  = true
  initial_password   = "Password1"

  depends_on = [zitadel_password_complexity_policy.default]
}

resource "zitadel_project_role" "default" {
  org_id       = zitadel_org.default.id
  project_id   = zitadel_project.default.id
  role_key     = "super-user"
  display_name = "Default Project Role"
  group        = "hoopadmin"
}

resource "zitadel_user_grant" "default" {
  project_id = zitadel_project.default.id
  org_id     = zitadel_org.default.id
  role_keys  = ["super-user"]
  user_id    = zitadel_human_user.default.id
}

resource "zitadel_org_member" "default" {
  org_id  = zitadel_org.default.id
  user_id = zitadel_human_user.default.id
  roles   = ["ORG_OWNER"]
}

# https://zitadel.com/docs/guides/manage/console/managers#roles
resource "zitadel_instance_member" "default" {
  user_id = zitadel_human_user.default.id
  roles   = ["IAM_OWNER"]
}
