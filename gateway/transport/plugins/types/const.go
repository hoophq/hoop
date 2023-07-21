package plugintypes

import "os"

const (
	defaultAuditPath = "/opt/hoop/sessions"
	defaultIndexPath = "/opt/hoop/indexes"

	PluginReviewName                     = "review"
	PluginAuditName                      = "audit"
	PluginEditorName                     = "editor"
	PluginRunbooksName                   = "runbooks"
	PluginSlackName                      = "slack"
	PluginAccessControlName              = "access_control"
	PluginIndexName                      = "indexer"
	PluginDLPName                        = "dlp"
	PluginDatabaseCredentialsManagerName = "database-credentials-manager"
)

var (
	// AuditPath is the filesystem path where wal logs are stored
	AuditPath = os.Getenv("PLUGIN_AUDIT_PATH")
	// IndexPath is the filesytem path where index wal logs are stored
	IndexPath = os.Getenv("PLUGIN_INDEX_PATH")
	// registered at gateway/main.go
	RegisteredPlugins []Plugin
)

func init() {
	if AuditPath == "" {
		AuditPath = defaultAuditPath
	}
	if IndexPath == "" {
		IndexPath = defaultIndexPath
	}
	_ = os.MkdirAll(AuditPath, 0755)
	_ = os.MkdirAll(IndexPath, 0755)
}
