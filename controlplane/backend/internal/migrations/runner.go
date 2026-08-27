package migrations

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	// Registers the "postgres" database driver with golang-migrate.
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source"
	// Registers the "file" source driver, used only by the
	// CONTROLPLANE_MIGRATION_PATH_FILES escape hatch.
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/hoophq/hoop/controlplane/backend/internal/database"
)

// Table keeps golang-migrate's bookkeeping out of the default
// public.schema_migrations.
//
// This is the collision that costs an afternoon. The gateway migrates the
// same database with the same tool and the same default table name. Sharing
// it means whichever product migrates second reads the other's version
// number, concludes it is ahead, and applies nothing. Neither errors.
const Table = "controlplane_schema_migrations"

// ErrBehind means the database schema is older than the binary expects.
var ErrBehind = errors.New("database schema is behind the binary")

// Runner applies and inspects the schema.
//
// It opens its own connection, which golang-migrate closes when the operation
// finishes. Migrations take locks, and holding them on a connection out of
// the application pool means a slow migration starves the pool it is about to
// hand over.
type Runner struct {
	postgresURI string
	pathFiles   string
	logger      *slog.Logger
}

// NewRunner returns a Runner. pathFiles overrides the embedded migrations
// with a directory on disk; empty means embedded.
func NewRunner(logger *slog.Logger, postgresURI, pathFiles string) *Runner {
	return &Runner{postgresURI: postgresURI, pathFiles: pathFiles, logger: logger}
}

// Up applies every pending migration.
func (r *Runner) Up() error {
	m, closeFn, err := r.open()
	if err != nil {
		return err
	}
	defer closeFn()

	from, dirty, err := version(m)
	if err != nil {
		return err
	}
	if dirty {
		// Refuse rather than retry. A dirty version means a migration died
		// part way, so the schema matches neither the old version nor the new
		// one and only a human knows which half ran.
		return fmt.Errorf("database is dirty at version %d, requires manual intervention", from)
	}

	switch err := m.Up(); {
	case err == nil:
		to, _, _ := version(m)
		r.logger.Info("applied database migrations", "from", from, "to", to)
	case errors.Is(err, migrate.ErrNoChange):
		r.logger.Debug("no pending database migrations", "version", from)
	default:
		return fmt.Errorf("failed running db migration, reason=%v", err)
	}
	return nil
}

// Down rolls back steps migrations. steps must be positive: `down` with no
// bound is how a production schema gets dropped by a typo, so there is no way
// to ask for all of them through this API.
func (r *Runner) Down(steps int) error {
	if steps <= 0 {
		return fmt.Errorf("steps must be positive, got %d", steps)
	}
	m, closeFn, err := r.open()
	if err != nil {
		return err
	}
	defer closeFn()

	from, dirty, err := version(m)
	if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("database is dirty at version %d, requires manual intervention", from)
	}

	switch err := m.Steps(-steps); {
	case err == nil, errors.Is(err, migrate.ErrNoChange):
		to, _, _ := version(m)
		r.logger.Info("rolled back database migrations", "from", from, "to", to)
	default:
		return fmt.Errorf("failed rolling back db migration, reason=%v", err)
	}
	return nil
}

// Version reports the applied version and whether it is dirty. A database
// with no migrations applied reports zero.
func (r *Runner) Version() (applied uint, dirty bool, err error) {
	m, closeFn, err := r.open()
	if err != nil {
		return 0, false, err
	}
	defer closeFn()
	return version(m)
}

// Latest reports the highest migration the source offers.
func (r *Runner) Latest() (uint, error) {
	src, err := r.source()
	if err != nil {
		return 0, err
	}
	defer func() { _ = src.Close() }()
	return lastVersion(src)
}

// Verify fails when the database is behind the binary.
//
// It is what `serve` calls when CONTROLPLANE_AUTO_MIGRATE is off. Starting
// against an older schema means every query that touches a new column fails
// at request time, one endpoint at a time, which reads as a bug in whichever
// feature was unlucky enough to be called first.
//
// A database ahead of the binary is allowed and logged. That is the normal
// state during a rolling deploy where the migration ran first.
func (r *Runner) Verify() error {
	applied, dirty, err := r.Version()
	if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("database is dirty at version %d, requires manual intervention", applied)
	}
	latest, err := r.Latest()
	if err != nil {
		return err
	}
	switch {
	case applied < latest:
		return fmt.Errorf("%w: applied=%d expected=%d, run `controlplane migrate up`", ErrBehind, applied, latest)
	case applied > latest:
		r.logger.Warn("database schema is ahead of this binary", "applied", applied, "expected", latest)
	}
	return nil
}

// open builds a Migrate and the function that releases it. golang-migrate
// returns two errors from Close because the source and the database close
// independently.
func (r *Runner) open() (*migrate.Migrate, func(), error) {
	src, err := r.source()
	if err != nil {
		return nil, nil, err
	}
	databaseURL, err := withMigrationsTable(r.postgresURI)
	if err != nil {
		_ = src.Close()
		return nil, nil, err
	}
	m, err := migrate.NewWithSourceInstance("source", src, databaseURL)
	if err != nil {
		_ = src.Close()
		return nil, nil, fmt.Errorf("failed initializing migration, reason=%v", err)
	}
	return m, func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			r.logger.Warn("failed closing migration source", "error", srcErr)
		}
		if dbErr != nil {
			r.logger.Warn("failed closing migration database handle", "error", dbErr)
		}
	}, nil
}

func (r *Runner) source() (source.Driver, error) {
	if r.pathFiles == "" {
		src, err := iofs.New(FS, ".")
		if err != nil {
			return nil, fmt.Errorf("failed reading embedded migrations, reason=%v", err)
		}
		return src, nil
	}

	absPath, err := filepath.Abs(r.pathFiles)
	if err != nil {
		return nil, fmt.Errorf("failed resolving migration path, reason=%v", err)
	}
	// Built as a URL rather than concatenated. A path containing a space or a
	// Windows drive letter produces an unparseable source URL when glued onto
	// "file://" by hand.
	sourceURL := (&url.URL{Scheme: "file", Path: absPath}).String()
	r.logger.Warn("loading migrations from disk instead of the embedded copy", "path", absPath)
	src, err := source.Open(sourceURL)
	if err != nil {
		return nil, fmt.Errorf("failed reading migrations from path, reason=%v", err)
	}
	return src, nil
}

// lastVersion walks the source to its highest version. golang-migrate exposes
// no "give me the newest" call, only a linked list, so this walks it. Reading
// the number off the filenames instead would work for the embedded copy and
// break for the on-disk one.
func lastVersion(src source.Driver) (uint, error) {
	v, err := src.First()
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed reading first migration, reason=%v", err)
	}
	for {
		next, err := src.Next(v)
		if err != nil {
			// Next returns os.ErrNotExist at the end of the list. Any other
			// error is a broken source and must not read as "we are current".
			if errors.Is(err, os.ErrNotExist) {
				return v, nil
			}
			return 0, fmt.Errorf("failed walking migrations, reason=%v", err)
		}
		v = next
	}
}

func version(m *migrate.Migrate) (uint, bool, error) {
	v, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("failed reading migration version, reason=%v", err)
	}
	return v, dirty, nil
}

// withMigrationsTable adds x-migrations-table to the connection URI unless the
// operator already set it.
func withMigrationsTable(postgresURI string) (string, error) {
	// database.ParseURI rather than url.Parse: it rejects a scheme
	// golang-migrate would refuse with a message that does not say so, and it
	// keeps the password out of the parse error.
	u, err := database.ParseURI(postgresURI)
	if err != nil {
		return "", err
	}
	q := u.Query()
	if q.Get("x-migrations-table") == "" {
		q.Set("x-migrations-table", Table)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}
