package plugin

import "os"

const (
	defaultAuditPath = "/opt/hoop/sessions"
	defaultIndexPath = "/opt/hoop/indexes"
)

var (
	// AuditPath is the filesystem path where wal logs are stored
	AuditPath = os.Getenv("PLUGIN_AUDIT_PATH")
	// IndexPath is the filesytem path where index wal logs are stored
	IndexPath = os.Getenv("PLUGIN_INDEX_PATH")
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
