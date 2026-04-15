package services

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

var validMachineIdentityConnectionTypes = []string{"postgres", "ssh", "rdp", "aws-ssm", "httpproxy", "kubernetes", "claude-code"}

type MachineIdentityCreateResult struct {
	Identity    *models.MachineIdentity
	Credentials []*CredentialInfo
	Attributes  []string
}

type MachineIdentityUpdateResult struct {
	Identity       *models.MachineIdentity
	NewCredentials []*CredentialInfo
	Attributes     []string
}

func CreateMachineIdentity(ctx context.Context, mi *models.MachineIdentity, attributes []string) (*MachineIdentityCreateResult, error) {
	if mi.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	existing, err := models.GetMachineIdentityByName(mi.OrgID, mi.Name)
	if err != nil && err != models.ErrNotFound {
		return nil, fmt.Errorf("failed checking existing identity: %w", err)
	}
	if existing != nil {
		return nil, models.ErrAlreadyExists
	}

	if mi.ID == "" {
		mi.ID = models.GenerateMachineIdentityID(mi.OrgID, mi.Name)
	}
	mi.CreatedAt = time.Now().UTC()
	mi.UpdatedAt = time.Now().UTC()

	uc := models.NewAdminContext(mi.OrgID)
	serverConf, err := models.GetServerMiscConfig()
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("failed retrieving server config: %w", err)
	}

	// Validate all connections before persisting anything
	type validatedConn struct {
		name string
		conn *models.Connection
	}
	var validated []validatedConn
	for _, connName := range mi.ConnectionNames {
		conn, err := models.GetConnectionByNameOrID(uc, connName)
		if err != nil {
			return nil, fmt.Errorf("connection %s not found: %w", connName, err)
		}
		if conn == nil {
			return nil, fmt.Errorf("connection %s not found", connName)
		}

		subtype := MapValidSubtypeToHttpProxy(conn)
		if !slices.Contains(validMachineIdentityConnectionTypes, subtype.String()) {
			return nil, fmt.Errorf("connection %s has unsupported type %s for machine identities", connName, subtype.String())
		}

		if conn.AccessModeConnect != "enabled" {
			return nil, fmt.Errorf("connection %s does not have access mode connect enabled", connName)
		}
		validated = append(validated, validatedConn{name: connName, conn: conn})
	}

	// Create the machine identity row first so FK references from
	// machine_identity_credentials are satisfied.
	if err := models.CreateMachineIdentity(mi); err != nil {
		return nil, fmt.Errorf("failed creating machine identity: %w", err)
	}

	var credentials []*CredentialInfo
	for _, vc := range validated {
		_, _, credInfo, err := ProvisionCredentialForConnection(mi, vc.name, vc.conn, serverConf)
		if err != nil {
			// Clean up the MI row if credential provisioning fails partway.
			if delErr := models.DeleteMachineIdentity(mi.OrgID, mi.ID); delErr != nil {
				log.Warnf("failed cleaning up machine identity %s after credential error: %v", mi.ID, delErr)
			}
			return nil, fmt.Errorf("failed provisioning credential for connection %s: %w", vc.name, err)
		}
		credentials = append(credentials, credInfo)
	}

	orgUUID, _ := uuid.Parse(mi.OrgID)
	if err := models.UpsertMachineIdentityAttributes(models.DB, orgUUID, mi.Name, attributes); err != nil {
		return nil, fmt.Errorf("failed upserting machine identity attributes: %w", err)
	}

	// Reconcile ABAC-driven credentials based on attribute overlap with connections
	if err := ReconcileMachineIdentityCredentials(ctx, mi.OrgID, mi.Name); err != nil {
		log.Warnf("failed reconciling credentials after creating MI %s: %v", mi.Name, err)
	}

	// Re-fetch credentials after reconciliation to include any ABAC-provisioned ones
	allCreds, err := models.ListMachineIdentityCredentials(mi.OrgID, mi.ID)
	if err != nil {
		log.Warnf("failed listing credentials after reconciliation for MI %s: %v", mi.Name, err)
	}

	// Merge: keep the directly-provisioned credentials (which have secret keys in memory)
	// and add any new ABAC-provisioned ones from the DB
	directSet := make(map[string]bool, len(credentials))
	for _, c := range credentials {
		directSet[c.ConnectionName] = true
	}
	for _, mic := range allCreds {
		if directSet[mic.ConnectionName] {
			continue
		}
		// This is an ABAC-provisioned credential — build info from stored data
		credInfo, err := GetMachineIdentityCredentialInfo(ctx, mi.OrgID, mi.Name, mic.ConnectionName)
		if err != nil {
			log.Warnf("failed building credential info for ABAC connection %s on MI %s: %v", mic.ConnectionName, mi.Name, err)
			continue
		}
		credentials = append(credentials, credInfo)
	}

	return &MachineIdentityCreateResult{
		Identity:    mi,
		Credentials: credentials,
		Attributes:  attributes,
	}, nil
}

func UpdateMachineIdentity(ctx context.Context, orgID, currentName string, newName, description string, connectionNames []string, attributes []string) (*MachineIdentityUpdateResult, error) {
	existing, err := models.GetMachineIdentityByName(orgID, currentName)
	if err != nil {
		return nil, fmt.Errorf("failed fetching machine identity: %w", err)
	}

	if newName != existing.Name {
		dup, err := models.GetMachineIdentityByName(orgID, newName)
		if err != nil && err != models.ErrNotFound {
			return nil, fmt.Errorf("failed checking duplicate name: %w", err)
		}
		if dup != nil && dup.ID != existing.ID {
			return nil, models.ErrAlreadyExists
		}
	}

	added, removed := diffConnectionNames(existing.ConnectionNames, connectionNames)

	// Compute ABAC desired set from the incoming attributes to avoid
	// revoking credentials that are still desired by attribute overlap.
	orgUUID, _ := uuid.Parse(orgID)
	attrMatchedConns, _ := models.GetConnectionNamesMatchingAttributes(models.DB, orgUUID, attributes)
	abacDesired := make(map[string]bool, len(attrMatchedConns))
	for _, n := range attrMatchedConns {
		abacDesired[n] = true
	}

	uc := models.NewAdminContext(orgID)
	serverConf, err := models.GetServerMiscConfig()
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("failed retrieving server config: %w", err)
	}

	var newCredentials []*CredentialInfo
	for _, connName := range added {
		conn, err := models.GetConnectionByNameOrID(uc, connName)
		if err != nil {
			return nil, fmt.Errorf("connection %s not found: %w", connName, err)
		}
		if conn == nil {
			return nil, fmt.Errorf("connection %s not found", connName)
		}

		subtype := MapValidSubtypeToHttpProxy(conn)
		if !slices.Contains(validMachineIdentityConnectionTypes, subtype.String()) {
			return nil, fmt.Errorf("connection %s has unsupported type %s for machine identities", connName, subtype.String())
		}

		if conn.AccessModeConnect != "enabled" {
			return nil, fmt.Errorf("connection %s does not have access mode connect enabled", connName)
		}

		_, _, credInfo, err := ProvisionCredentialForConnection(existing, connName, conn, serverConf)
		if err != nil {
			return nil, fmt.Errorf("failed provisioning credential for connection %s: %w", connName, err)
		}
		newCredentials = append(newCredentials, credInfo)
	}

	for _, connName := range removed {
		if abacDesired[connName] {
			continue // attribute overlap keeps this credential alive
		}
		info, err := models.DeleteMachineIdentityCredential(orgID, existing.ID, connName)
		if err != nil && err != models.ErrNotFound {
			log.Warnf("failed revoking credential for connection %s on machine identity %s: %v", connName, currentName, err)
			continue
		}
		revokeActiveProxySessions(info)
	}

	updated := &models.MachineIdentity{
		ID:              existing.ID,
		OrgID:           orgID,
		Name:            newName,
		Description:     description,
		ConnectionNames: connectionNames,
	}
	if err := models.UpdateMachineIdentity(updated); err != nil {
		return nil, fmt.Errorf("failed updating machine identity: %w", err)
	}

	if err := models.UpsertMachineIdentityAttributes(models.DB, orgUUID, newName, attributes); err != nil {
		return nil, fmt.Errorf("failed upserting machine identity attributes: %w", err)
	}

	// Inline ABAC provisioning: after attributes are upserted, provision credentials
	// for any connections that now match via attribute overlap but don't yet have a credential.
	// This replaces the full ReconcileMachineIdentityCredentials call which could
	// re-provision credentials that were just explicitly removed via connection_names.
	abacConns, _ := models.GetConnectionNamesMatchingAttributes(models.DB, orgUUID, attributes)

	// Build set of connections that already have credentials
	existingMICreds, err := models.ListMachineIdentityCredentials(orgID, existing.ID)
	if err != nil {
		log.Warnf("failed listing existing credentials for MI %s: %v", newName, err)
	}
	hasCredential := make(map[string]bool, len(existingMICreds))
	for _, mic := range existingMICreds {
		hasCredential[mic.ConnectionName] = true
	}
	// Also count the ones we just provisioned in this update
	for _, c := range newCredentials {
		hasCredential[c.ConnectionName] = true
	}

	for _, connName := range abacConns {
		if hasCredential[connName] {
			continue // already has a credential
		}
		conn, err := models.GetConnectionByNameOrID(uc, connName)
		if err != nil || conn == nil {
			continue
		}
		subtype := MapValidSubtypeToHttpProxy(conn)
		if !slices.Contains(validMachineIdentityConnectionTypes, subtype.String()) {
			continue
		}
		if conn.AccessModeConnect != "enabled" {
			continue
		}
		_, _, credInfo, err := ProvisionCredentialForConnection(existing, connName, conn, serverConf)
		if err != nil {
			log.Warnf("failed provisioning ABAC credential for connection %s on MI %s: %v", connName, newName, err)
			continue
		}
		newCredentials = append(newCredentials, credInfo)
	}

	return &MachineIdentityUpdateResult{
		Identity:       updated,
		NewCredentials: newCredentials,
		Attributes:     attributes,
	}, nil
}

func DeleteMachineIdentity(ctx context.Context, orgID, name string) error {
	mi, err := models.GetMachineIdentityByName(orgID, name)
	if err != nil {
		return err
	}

	// Revoke all credentials with full proxy cancellation BEFORE deleting the MI
	revokedInfos, err := models.DeleteAllMachineIdentityCredentials(orgID, mi.ID)
	if err != nil {
		log.Warnf("failed revoking credentials for machine identity %s: %v", name, err)
	}
	for _, info := range revokedInfos {
		revokeActiveProxySessions(info)
	}

	return models.DeleteMachineIdentity(orgID, mi.ID)
}

func RotateMachineIdentityCredential(ctx context.Context, orgID, identityName, connName string) (*CredentialInfo, error) {
	mi, err := models.GetMachineIdentityByName(orgID, identityName)
	if err != nil {
		return nil, fmt.Errorf("failed fetching machine identity: %w", err)
	}

	revokedInfo, err := models.DeleteMachineIdentityCredential(orgID, mi.ID, connName)
	if err != nil {
		return nil, fmt.Errorf("failed revoking old credential for connection %s: %w", connName, err)
	}
	revokeActiveProxySessions(revokedInfo)

	uc := models.NewAdminContext(orgID)
	conn, err := models.GetConnectionByNameOrID(uc, connName)
	if err != nil {
		return nil, fmt.Errorf("connection %s not found: %w", connName, err)
	}
	if conn == nil {
		return nil, fmt.Errorf("connection %s not found", connName)
	}

	serverConf, err := models.GetServerMiscConfig()
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("failed retrieving server config: %w", err)
	}

	_, _, credInfo, err := ProvisionCredentialForConnection(mi, connName, conn, serverConf)
	if err != nil {
		return nil, fmt.Errorf("failed provisioning new credential for connection %s: %w", connName, err)
	}

	return credInfo, nil
}

func GetMachineIdentityCredentialInfo(ctx context.Context, orgID, identityName, connName string) (*CredentialInfo, error) {
	mi, err := models.GetMachineIdentityByName(orgID, identityName)
	if err != nil {
		return nil, fmt.Errorf("machine identity %s not found: %w", identityName, err)
	}
	mic, err := models.GetMachineIdentityCredentialByConnName(orgID, mi.ID, connName)
	if err != nil {
		return nil, fmt.Errorf("credential not found for connection %s: %w", connName, err)
	}

	cred, err := models.GetConnectionCredentialsByID(orgID, mic.ConnectionCredentialID)
	if err != nil {
		return nil, fmt.Errorf("failed fetching connection credential: %w", err)
	}

	uc := models.NewAdminContext(orgID)
	conn, err := models.GetConnectionByNameOrID(uc, connName)
	if err != nil {
		return nil, fmt.Errorf("connection %s not found: %w", connName, err)
	}
	if conn == nil {
		return nil, fmt.Errorf("connection %s not found", connName)
	}

	serverConf, err := models.GetServerMiscConfig()
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("failed retrieving server config: %w", err)
	}

	subtype := MapValidSubtypeToHttpProxy(conn)
	connCopy := *conn
	connCopy.SubType = sql.NullString{String: subtype.String(), Valid: true}

	info := BuildCredentialInfo(cred, &connCopy, serverConf, mic.SecretKey)
	return info, nil
}

// ReconcileMachineIdentityCredentials computes the desired set of connections for a machine
// identity (union of explicit connection_names and attribute-matched connections), diffs against
// existing credentials, and provisions new / revokes stale credentials.
//
// A credential should exist if the connection is in the MI's connection_names OR the MI and
// connection share at least one attribute. A credential is revoked only when neither condition
// holds.
func ReconcileMachineIdentityCredentials(ctx context.Context, orgID, identityName string) error {
	mi, err := models.GetMachineIdentityByName(orgID, identityName)
	if err != nil {
		return fmt.Errorf("failed fetching machine identity: %w", err)
	}

	orgUUID, _ := uuid.Parse(orgID)
	miAttrs, err := models.GetMachineIdentityAttributes(models.DB, orgUUID, mi.Name)
	if err != nil {
		return fmt.Errorf("failed fetching MI attributes: %w", err)
	}

	// Connections matched by attribute overlap
	attrMatchedConns, err := models.GetConnectionNamesMatchingAttributes(models.DB, orgUUID, miAttrs)
	if err != nil {
		return fmt.Errorf("failed fetching attribute-matched connections: %w", err)
	}

	// Desired set = union(connection_names, attribute-matched connections)
	desiredSet := make(map[string]bool, len(mi.ConnectionNames)+len(attrMatchedConns))
	for _, n := range mi.ConnectionNames {
		desiredSet[n] = true
	}
	for _, n := range attrMatchedConns {
		desiredSet[n] = true
	}

	// Existing credentials
	existingCreds, err := models.ListMachineIdentityCredentials(orgID, mi.ID)
	if err != nil {
		return fmt.Errorf("failed listing existing credentials: %w", err)
	}
	existingSet := make(map[string]bool, len(existingCreds))
	for _, c := range existingCreds {
		existingSet[c.ConnectionName] = true
	}

	// Provision credentials for connections in desired but not existing
	uc := models.NewAdminContext(orgID)
	serverConf, err := models.GetServerMiscConfig()
	if err != nil && err != gorm.ErrRecordNotFound {
		return fmt.Errorf("failed retrieving server config: %w", err)
	}

	for connName := range desiredSet {
		if existingSet[connName] {
			continue
		}
		conn, err := models.GetConnectionByNameOrID(uc, connName)
		if err != nil || conn == nil {
			log.Warnf("reconcile: skipping connection %s for MI %s: not found", connName, mi.Name)
			continue
		}
		subtype := MapValidSubtypeToHttpProxy(conn)
		if !slices.Contains(validMachineIdentityConnectionTypes, subtype.String()) {
			log.Warnf("reconcile: skipping connection %s for MI %s: unsupported type %s", connName, mi.Name, subtype.String())
			continue
		}
		if conn.AccessModeConnect != "enabled" {
			log.Warnf("reconcile: skipping connection %s for MI %s: access mode connect not enabled", connName, mi.Name)
			continue
		}
		if _, _, _, provErr := ProvisionCredentialForConnection(mi, connName, conn, serverConf); provErr != nil {
			log.Warnf("reconcile: failed provisioning credential for connection %s on MI %s: %v", connName, mi.Name, provErr)
		}
	}

	// Revoke credentials for connections in existing but not desired
	for _, c := range existingCreds {
		if desiredSet[c.ConnectionName] {
			continue
		}
		info, err := models.DeleteMachineIdentityCredential(orgID, mi.ID, c.ConnectionName)
		if err != nil && err != models.ErrNotFound {
			log.Warnf("reconcile: failed revoking credential for connection %s on MI %s: %v", c.ConnectionName, mi.Name, err)
			continue
		}
		revokeActiveProxySessions(info)
	}

	return nil
}

// ReconcileAllMachineIdentitiesForConnection reconciles credentials for all machine identities
// that are affected by a change to the given connection's attributes. This is called when a
// connection's attributes are updated.
func ReconcileAllMachineIdentitiesForConnection(ctx context.Context, orgID, connectionName string) error {
	orgUUID, _ := uuid.Parse(orgID)

	// Get the connection's current attributes
	connAttrs, err := models.GetConnectionAttributes(models.DB, orgUUID, connectionName)
	if err != nil {
		return fmt.Errorf("failed fetching connection attributes: %w", err)
	}

	// Find all MIs that share at least one attribute with this connection
	affectedMINames, err := models.GetMachineIdentityNamesMatchingAttributes(models.DB, orgUUID, connAttrs)
	if err != nil {
		return fmt.Errorf("failed fetching affected machine identities: %w", err)
	}

	// Also find all MIs that currently have a credential for this connection
	// (they might need revocation if they no longer match)
	allMIs, err := models.ListMachineIdentities(orgID)
	if err != nil {
		return fmt.Errorf("failed listing machine identities: %w", err)
	}

	affectedSet := make(map[string]bool, len(affectedMINames))
	for _, name := range affectedMINames {
		affectedSet[name] = true
	}

	// Include MIs that have an existing credential for this connection
	for _, mi := range allMIs {
		if affectedSet[mi.Name] {
			continue
		}
		creds, err := models.ListMachineIdentityCredentials(orgID, mi.ID)
		if err != nil {
			continue
		}
		for _, c := range creds {
			if c.ConnectionName == connectionName {
				affectedSet[mi.Name] = true
				break
			}
		}
	}

	// Reconcile each affected MI
	for _, mi := range allMIs {
		if !affectedSet[mi.Name] {
			continue
		}
		if err := ReconcileMachineIdentityCredentials(ctx, orgID, mi.Name); err != nil {
			log.Warnf("reconcile: failed reconciling MI %s after connection %s attribute change: %v", mi.Name, connectionName, err)
		}
	}
	return nil
}

func diffConnectionNames(old, new []string) (added, removed []string) {
	oldSet := make(map[string]bool, len(old))
	for _, n := range old {
		oldSet[n] = true
	}
	newSet := make(map[string]bool, len(new))
	for _, n := range new {
		newSet[n] = true
	}
	for _, n := range new {
		if !oldSet[n] {
			added = append(added, n)
		}
	}
	for _, n := range old {
		if !newSet[n] {
			removed = append(removed, n)
		}
	}
	return
}
