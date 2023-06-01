
policy "pg-local-readonly" {
    engine               = "postgres"
	plugin_config_entry  = "pg-local"
	instances            = ["testdb", "hoopdev", "dellstore"]
	grant_privileges     = ["SELECT", "UPDATE"]
}
