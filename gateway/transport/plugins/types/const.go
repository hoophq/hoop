package plugintypes

import (
	"fmt"
	"os"
)

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

// CheckAuditPath creates AuditPath when it is missing and proves that this
// process can write to it. The error names the directory and PLUGIN_AUDIT_PATH.
func CheckAuditPath() error {
	if err := os.MkdirAll(AuditPath, 0o700); err != nil {
		return auditPathError(err)
	}
	f, err := os.CreateTemp(AuditPath, ".write-check-*")
	if err != nil {
		return auditPathError(err)
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
	return nil
}

func auditPathError(err error) error {
	return fmt.Errorf("session storage directory %s is not writable, set PLUGIN_AUDIT_PATH to a writable directory: %w", AuditPath, err)
}
