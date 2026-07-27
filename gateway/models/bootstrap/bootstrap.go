package modelsbootstrap

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/hoophq/hoop/common/log"
	migrationfiles "github.com/hoophq/hoop/gateway/migrations"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/models/bootstrap/migrations"
	_ "github.com/lib/pq"
)

// MigrateDB applies the SQL migrations against postgresURI. By default the
// migrations embedded in the binary (gateway/migrations) are used; when
// migrationPathFiles is non-empty, migration files are loaded from that
// directory instead (MIGRATION_PATH_FILES override, for deployments that
// manage migration files externally).
func MigrateDB(postgresURI, migrationPathFiles string) error {
	m, err := newMigrate(postgresURI, migrationPathFiles)
	if err != nil {
		return err
	}
	// Release the migration connection when done: leaving it open leaks a
	// connection and deadlocks single-session backends (embedded PGlite).
	defer func() {
		if srcErr, dbErr := m.Close(); srcErr != nil || dbErr != nil {
			log.Warnf("failed closing db migration handles, source-err=%v, db-err=%v", srcErr, dbErr)
		}
	}()
	ver, dirty, err := m.Version()
	if err != nil && err != migrate.ErrNilVersion {
		return fmt.Errorf("failed obtaining db migration version, err=%v", err)
	}
	if dirty {
		return fmt.Errorf("database is in a dirty state, requires manual intervention to fix it")
	}
	log.Debugf("loaded migration version=%v, is-nil-version=%v", ver, err == migrate.ErrNilVersion)
	err = m.Up()
	switch err {
	case migrate.ErrNilVersion, migrate.ErrNoChange, nil:
		log.Debugf("processed db migration with success, nochange=%v", err == migrate.ErrNoChange)
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

// newMigrate builds a migrate instance from the embedded migration files,
// or from a disk directory when migrationPathFiles is non-empty.
func newMigrate(postgresURI, migrationPathFiles string) (*migrate.Migrate, error) {
	if migrationPathFiles != "" {
		absPath, err := filepath.Abs(migrationPathFiles)
		if err != nil {
			return nil, fmt.Errorf("failed resolving migration path %v, err=%v", migrationPathFiles, err)
		}
		// Build a proper file URL instead of concatenating strings so
		// paths containing characters that require escaping stay valid.
		sourceURL := &url.URL{Scheme: "file", Path: absPath}
		m, err := migrate.New(sourceURL.String(), postgresURI)
		if err != nil {
			return nil, fmt.Errorf("failed initializing db migration from path %v, err=%v", absPath, err)
		}
		return m, nil
	}
	src, err := iofs.New(migrationfiles.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("failed loading embedded migrations, err=%v", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, postgresURI)
	if err != nil {
		return nil, fmt.Errorf("failed initializing db migration, err=%v", err)
	}
	return m, nil
}

func RunGolangMigrations() error {
	log.Debug("running golang migration scripts!")

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
