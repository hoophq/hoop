package plugintypes

import "os"

const (
	defaultAuditPath = "/opt/hoop/sessions"

	PluginReviewName                     = "review"
	PluginAuditName                      = "audit"
	PluginEditorName                     = "editor"
	PluginRunbooksName                   = "runbooks"
	PluginSlackName                      = "slack"
	PluginAccessControlName              = "access_control"
	PluginIndexName                      = "indexer"
	PluginDLPName                        = "dlp"
	PluginDatabaseCredentialsManagerName = "database-credentials-manager"
	PluginWebhookName                    = "webhooks"
)

var (
	// AuditPath is the filesystem path where wal logs are stored.
	// The env PLUGIN_AUDIT_PATH should be used to set a new path
	AuditPath = os.Getenv("PLUGIN_AUDIT_PATH")
	// registered at gateway/main.go
	RegisteredPlugins []Plugin
)

func init() {
	if AuditPath == "" {
		AuditPath = defaultAuditPath
	}
	_ = os.MkdirAll(AuditPath, 0755)
}
