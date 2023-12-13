package xtdbmigration

import (
	"net"
	"time"

	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/gateway/pgrest"
	"github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/user"
)

var store = storagev2.NewStorage(nil)

type migrationState struct {
	success int
	failed  int
}

// shouldMigrate validates if the postgrest and xtdb are running
// and if the migration should be performed.
func shouldMigrate() (v bool) {
	if store.URL() == nil {
		return
	}
	timeout := time.Second * 3
	conn, err := net.DialTimeout("tcp", store.URL().Host, timeout)
	if err != nil {
		log.Infof("pgrest migration: xtdb is not responding, host=%v, err=%v", store.URL().Host, err)
		return
	}
	_ = conn.Close()
	if err := pgrest.CheckLiveness(); err != nil {
		log.Infof("pgrest migration: postgrest is not responding, err=%v", err)
		return
	}
	return true
}

// RunCore performs the migration of core resources like
// org, users, service accounts, agents, connections and plugins.
func RunCore(xtdbURL, orgName string) {
	store.SetURL(xtdbURL)
	if !shouldMigrate() {
		return
	}
	log.Infof("starting xtdb to postgrest migration (core resources)")
	// it will disable the rollout logic, allowing to obtain data
	// from the xtdb storage, instead of falling back to the postgrest storage.
	pgrest.DisableRollout()
	userStore := user.Storage{Storage: storage.New()}
	userStore.SetURL(xtdbURL)
	org, err := userStore.GetOrgByName(orgName)
	if err != nil || org == nil {
		log.Warnf("pgrest migration: failed fetching default organization, err=%v", err)
		return
	}
	if err := migrateOrganization(org.Id); err != nil {
		log.Warnf("pgrest migration: failed migrating organization, err=%v", err)
		return
	}
	migrateUsers(xtdbURL, org.Id)
	migrateServiceAccounts(xtdbURL, org.Id)
	migrateAgents(xtdbURL, org.Id)
	migrateConnections(xtdbURL, org.Id)
	migratePlugins(xtdbURL, org.Id)
}

// RunReviews performs the migration of reviews from xtdb to postgrest.
func RunReviews(xtdbURL, orgName string) {
	store.SetURL(xtdbURL)
	if !shouldMigrate() {
		return
	}
	log.Infof("starting xtdb to postgrest migration (review resources)")
	userStore := user.Storage{Storage: storage.New()}
	userStore.SetURL(xtdbURL)
	org, err := userStore.GetOrgByName(orgName)
	if err != nil || org == nil {
		log.Warnf("pgrest migration: failed fetching default organization, err=%v", err)
		return
	}
	// it will disable the rollout logic, allowing to obtain data
	// from the xtdb storage, instead of falling back to the postgrest storage.
	pgrest.DisableRollout()
	migrateReviews(xtdbURL, org.Id)
}

// RunSessions performs the migration of sessions from xtdb to postgrest.
func RunSessions(xtdbURL, orgName string, fromDate time.Time) {
	store.SetURL(xtdbURL)
	if !shouldMigrate() {
		return
	}
	log.Infof("starting xtdb to postgrest migration (session resources)")
	userStore := user.Storage{Storage: storage.New()}
	userStore.SetURL(xtdbURL)
	org, err := userStore.GetOrgByName(orgName)
	if err != nil || org == nil {
		log.Warnf("pgrest migration: failed fetching default organization, err=%v", err)
		return
	}
	// it will disable the rollout logic, allowing to obtain data
	// from the xtdb storage, instead of falling back to the postgrest storage.
	pgrest.DisableRollout()
	migrateSessions(xtdbURL, org.Id, fromDate)
}
