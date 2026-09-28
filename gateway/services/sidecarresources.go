package services

import (
	"errors"
	"fmt"

	"github.com/hoophq/hoop/common/featureflag"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

// SidecarResourcesFlag gates the ADR-0022 prototype: a sidecar stored as a
// resource and each listener as a role.
const SidecarResourcesFlag = "experimental.sidecar_resources"

// resourceNameMaxLen is private.resources.name (000047).
const resourceNameMaxLen = 128

// ErrSidecarProjectionInvalid is a configuration the prototype cannot store as
// resources and roles. It is the admin's to fix.
type ErrSidecarProjectionInvalid struct{ Err error }

func (e ErrSidecarProjectionInvalid) Error() string {
	return "cannot store the sidecar as a resource: " + e.Err.Error()
}

func (e ErrSidecarProjectionInvalid) Unwrap() error { return e.Err }

// SidecarResourcesEnabled reports whether the prototype is on for the org.
func SidecarResourcesEnabled(orgID string) bool {
	return featureflag.IsEnabled(orgID, SidecarResourcesFlag)
}

// SidecarRoleName is the connection name a listener's role is stored under.
// Connections are unique per org, listeners only per sidecar.
func SidecarRoleName(sidecarName, listenerName string) string {
	return sidecarName + "." + listenerName
}

// sidecarRoleType maps a listener protocol onto the connection type and
// subtype of its role. The types are for storage and forms only.
func sidecarRoleType(protocol string) (string, string, error) {
	switch protocol {
	case "postgres", "mysql", "mssql", "mongodb":
		return "database", protocol, nil
	case "http", "grpc", "spanner", "ssh", "clickhouse":
		return "custom", protocol, nil
	}
	return "", "", fmt.Errorf("listener protocol %q has no role type", protocol)
}

// ProjectSidecarTx stores sc as a resource and each of its listeners as a
// role, inside the transaction that writes the configuration. It is
// idempotent. Roles of listeners the configuration dropped are removed.
func ProjectSidecarTx(tx *gorm.DB, sc *models.Sidecar) error {
	if len(sc.Name) > resourceNameMaxLen {
		return ErrSidecarProjectionInvalid{Err: fmt.Errorf("sidecar name longer than %d characters", resourceNameMaxLen)}
	}
	if err := models.UpsertSidecarResource(tx, sc.OrgID, sc.ID, sc.Name); err != nil {
		return wrapTaken(err)
	}
	keep := make([]string, 0, len(sc.Configuration.Listeners))
	for _, l := range sc.Configuration.Listeners {
		if l.Name == "" {
			continue
		}
		roleName := SidecarRoleName(sc.Name, l.Name)
		if err := apivalidation.ValidateResourceName(roleName); err != nil {
			return ErrSidecarProjectionInvalid{Err: fmt.Errorf("role %q: %w", roleName, err)}
		}
		connType, subtype, err := sidecarRoleType(l.Protocol)
		if err != nil {
			return ErrSidecarProjectionInvalid{Err: err}
		}
		if _, err := models.UpsertSidecarListenerRole(tx, sc.OrgID, sc.ID, l.Name, sc.Name, roleName, connType, subtype); err != nil {
			return wrapTaken(err)
		}
		keep = append(keep, l.Name)
	}
	return models.DeleteSidecarListenerRolesExcept(tx, sc.OrgID, sc.ID, keep)
}

func wrapTaken(err error) error {
	var taken models.ErrSidecarTargetTaken
	if errors.As(err, &taken) {
		return ErrSidecarProjectionInvalid{Err: taken}
	}
	return err
}
