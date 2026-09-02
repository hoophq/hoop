package types

import "os"

type (
	ClientStatusType string
	UserStatusType   string
)

// GroupAdmin is the name of the admin user, defaults to "admin"
// if the env ADMIN_USERNAME is not set
var (
	GroupAdmin = func() string {
		username := os.Getenv("ADMIN_USERNAME")
		if username == "" {
			username = "admin"
		}
		return username
	}()

	GroupAuditor = func() string {
		auditor := os.Getenv("AUDITOR_USERNAME")
		if auditor == "" {
			return "auditor"
		}
		return auditor
	}()

	// GroupApprover is the control plane's second role: it reviews and
	// administers nothing.
	GroupApprover = func() string {
		approver := os.Getenv("APPROVER_USERNAME")
		if approver == "" {
			return "approver"
		}
		return approver
	}()
)

const (
	UserStatusActive    UserStatusType = "active"
	UserStatusReviewing UserStatusType = "reviewing"
	UserStatusInactive  UserStatusType = "inactive"
)
