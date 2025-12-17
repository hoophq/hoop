package modelsbootstrap

import (
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/models/bootstrap/migrations"
	_ "github.com/lib/pq"
)

func MigrateDB(postgresURI, migrationPathFiles string) error {
	// migration
	sourceURL := "file://" + migrationPathFiles
	m, err := migrate.New(sourceURL, postgresURI)
	if err != nil {
		return fmt.Errorf("failed initializing db migration, err=%v", err)
	}
	ver, dirty, err := m.Version()
	if err != nil && err != migrate.ErrNilVersion {
		return fmt.Errorf("failed obtaining db migration version, err=%v", err)
	}
	if dirty {
		return fmt.Errorf("database is in a dirty state, requires manual intervention to fix it")
	}
	log.Infof("loaded migration version=%v, is-nil-version=%v", ver, err == migrate.ErrNilVersion)
	err = m.Up()
	switch err {
	case migrate.ErrNilVersion, migrate.ErrNoChange, nil:
		log.Infof("processed db migration with success, nochange=%v", err == migrate.ErrNoChange)
	default:
		// It usually happens when there's the last number is above
		// the local migration files. Let it proceed to be able to rollback
		// to previous versions
		if strings.HasPrefix(err.Error(), "no migration found for version") {
			log.Warn(err)
			break
		}
		return fmt.Errorf("failed running db migration, err=%v", err)
	}

	return nil
}

func RunGolangMigrations() error {
	log.Info("running golang migration scripts!")

	// Run RunbooksV2 migration
	err := migrations.RunRunbooksV2()
	if err != nil {
		return fmt.Errorf("failed running RunbooksV2 migration, err=%v", err)
	}

	return nil
}

// AddDefaultRunbooks creates default runbook configurations for all organizations
// that don't have one yet.
func AddDefaultRunbooks() error {
	orgs, err := models.ListAllOrganizations()
	if err != nil {
		log.Infof("failed listing organizations: %v", err)
		// continue even if we fail to list organizations
		return nil
	}

	for _, org := range orgs {
		_, err := models.CreateDefaultRunbookConfiguration(models.DB, org.ID)
		if err != nil {
			log.Infof("failed creating default runbook for org %s: %v", org.ID, err)
		}
	}

	return nil
}
