package xtdbmigration

import (
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/pgrest"
	pgserviceaccounts "github.com/runopsio/hoop/gateway/pgrest/serviceaccounts"
	pgusers "github.com/runopsio/hoop/gateway/pgrest/users"
	"github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/storagev2"
	serviceaccountstorage "github.com/runopsio/hoop/gateway/storagev2/serviceaccount"
	"github.com/runopsio/hoop/gateway/user"
)

func migrateOrganization(orgID string) error {
	org, err := pgusers.New().FetchOrgByID(orgID)
	if err != nil {
		return err
	}
	if org != nil {
		log.Infof("pgrest migration: organization already exists, id=%s", orgID)
		return nil
	}
	if err := pgusers.New().CreateOrg(orgID, proto.DefaultOrgName); err != nil {
		return err

	}
	log.Infof("pgrest migration: organization migrated, id=%s", orgID)
	return nil
}

func migrateUsers(xtdbURL, orgID string) {
	log.Infof("pgrest migration: migrating users")
	userStore := user.Storage{Storage: storage.New()}
	userStore.SetURL(xtdbURL)
	userList, err := userStore.FindAll(user.NewContext(orgID, ""))
	if err != nil {
		log.Warnf("pgrest migration: failed listing users, err=%v", err)
		return
	}
	var state migrationState
	for _, u := range userList {
		err := pgusers.New().Upsert(pgrest.User{
			OrgID:   u.Org,
			Subject: u.Id,
			Name:    u.Name,
			Email:   u.Email,
			Status:  string(u.Status),
			SlackID: u.SlackID,
			Groups:  u.Groups,
		})
		if err != nil {
			log.Warnf("pgrest migration: failed migrating user=%v, err=%v", u.Id, err)
			state.failed++
			continue
		}
		state.success++
	}
	log.Infof("pgrest migration: users migrated, total=%v, success=%d, failed=%d", len(userList), state.success, state.failed)
}

func migrateServiceAccounts(xtdbURL, orgID string) {
	log.Infof("pgrest migration: migrating service accounts")
	ctx := storagev2.NewOrganizationContext(orgID, store)
	ctx.SetURL(xtdbURL)
	// TODO: make sure to toggle the rollout env
	saList, err := serviceaccountstorage.List(ctx)
	if err != nil {
		log.Warnf("pgrest migration: fail listing service accounts org default, err=%v", err)
		return
	}
	var state migrationState
	for _, sa := range saList {
		if _, err := pgserviceaccounts.New().Upsert(ctx, &sa); err != nil {
			state.failed++
			log.Warnf("pgrest migration: failed migrating service account=%v, err=%v", sa.ID, err)
			continue
		}
		state.success++
	}
	log.Infof("pgrest migration: service accounts migrated, total=%v, success=%d, failed=%d", len(saList), state.success, state.failed)
}
