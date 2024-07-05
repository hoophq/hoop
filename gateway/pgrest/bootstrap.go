package pgrest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/version"
	"github.com/hoophq/hoop/gateway/appconfig"
	_ "github.com/lib/pq"
)

// CheckLiveness validates if the postgrest process is running
// by checking if the port is open and responding.
func CheckLiveness() error {
	ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*3)
	defer cancelFn()
	// ready checks the state of the database connection and the schema cache
	// https://postgrest.org/en/v12/references/admin.html#health-check
	req, err := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:8007/ready", nil)
	if err != nil {
		return fmt.Errorf("failed creating liveness request %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed performing request %v", err)
	}
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	return fmt.Errorf("postgrest is not ready, status=%v", resp.StatusCode)
}

// Run performs the initalization and necessary migrations to
// start the postgrest process. This function will take care of
// managing the process lifecycle of postgrest.
func Run() (err error) {
	baseURL, jwtSecretKey, err = loadConfig()
	if err != nil {
		return err
	}
	roleName = appconfig.Get().PostgRESTRole()
	postgrestBinFile, err := exec.LookPath("postgrest")
	if err != nil {
		return fmt.Errorf("unable to find postgrest binary in PATH, err=%v", err)
	}

	// migration
	sourceURL := "file://" + appconfig.Get().MigrationPathFiles()
	m, err := migrate.New(sourceURL, appconfig.Get().PgURI())
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

	appState, err := bootstrapState(roleName, appconfig.Get().PgUsername(), appconfig.Get().PgURI())
	if err != nil {
		return err
	}
	// change role name
	roleName = appState.RoleName
	schemaName = appState.Schema
	log.Infof("bootstrap with success to schema=%v, role=%v, checksum=%v", appState.Schema, appState.RoleName, appState.Checksum)
	// if err := provisionPgRoles(roleName, pgUser, pgConnectionURI); err != nil {
	// 	return fmt.Errorf("failed provisioning roles: %v", err)
	// }

	// https://postgrest.org/en/stable/references/configuration.html#env-variables-config
	envs := []string{
		"PGTZ=UTC", // generate timestamps in UTC
		"PGRST_DB_ANON_ROLE=web_anon",
		"PGRST_DB_CHANNEL_ENABLED=False",
		"PGRST_DB_CONFIG=False",
		"PGRST_DB_PLAN_ENABLED=True",
		"PGRST_LOG_LEVEL=error",
		"PGRST_SERVER_HOST=!4",
		"PGRST_SERVER_PORT=8008",
		"PGRST_ADMIN_SERVER_PORT=8007", // health check port
		fmt.Sprintf("PGRST_DB_SCHEMAS=%s,%s", publicSchemeA, publicSchemeB),
		fmt.Sprintf("PGRST_DB_URI=%s", appconfig.Get().PgURI()),
		fmt.Sprintf("PGRST_JWT_SECRET=%s", string(jwtSecretKey)),
	}

	startProcessFn := func(i int) {
		log.Infof("starting postgrest process, attempt=%v ...", i)
		cmd := exec.Command(postgrestBinFile)
		cmd.Env = envs
		cmd.Stdout = &logInfoWriter{}
		cmd.Stderr = &logInfoWriter{}
		if err := cmd.Run(); err != nil {
			log.Errorf("failed running postgrest process, err=%v", err)
			return
		}
		pid := -1
		if cmd.Process != nil {
			pid = cmd.Process.Pid
		}
		log.Infof("postgrest process (pid:%v) finished", pid)
	}

	go func() {
		for i := 1; ; i++ {
			startProcessFn(i)
			// give some time to retry
			time.Sleep(time.Second * 5)
		}
	}()

	for i := 1; ; i++ {
		if i > 15 {
			return fmt.Errorf("max attempts (15) reached. failed to validate postgrest liveness at %v", baseURL.Host)
		}
		if err := CheckLiveness(); err != nil {
			time.Sleep(time.Second * 1)
			continue
		}
		log.Infof("postgrest is ready at %v", baseURL.Host)
		break
	}
	return nil
}

type logInfoWriter struct{}

func (w *logInfoWriter) Write(p []byte) (n int, err error) {
	v := strings.TrimSuffix(string(p), "\n")
	v = strings.TrimPrefix(v, "\n")
	if v == "" {
		return len(p), nil
	}
	log.With("app", "postgrest").Info(v)
	return len(p), nil
}

func loadConfig() (u *url.URL, jwtSecret []byte, err error) {
	secretRandomBytes := make([]byte, 32)
	if _, err := rand.Read(secretRandomBytes); err != nil {
		return nil, nil, fmt.Errorf("failed generating entropy, err=%v", err)
	}

	pgrestUrlStr := os.Getenv("PGREST_URL")
	if pgrestUrlStr == "" {
		pgrestUrlStr = "http://127.0.0.1:8008"
	}
	pgrestUrl, err := url.Parse(pgrestUrlStr)
	if err != nil {
		return nil, nil, fmt.Errorf("PGREST_URL in wrong format, err=%v", err)
	}
	encSecretKey := []byte(base64.RawURLEncoding.EncodeToString(secretRandomBytes))
	// write the secret key to a file
	// so it can be used by any internal ad-hoc process
	_ = os.Remove("/app/pgrest-secret-key")
	_ = os.WriteFile("/app/pgrest-secret-key", encSecretKey, 0400)
	return pgrestUrl, encSecretKey, nil
}

func getLastAppState(tx *sql.Tx) (*AppStateRollout, error) {
	var stateItems []AppState
	rows, err := tx.Query(`
SELECT id, state_rollback, checksum, schema, role_name, version, commit, pgversion, created_at
FROM private.appstate ORDER BY created_at DESC LIMIT 2`)
	if err != nil {
		return nil, fmt.Errorf("failed query appstate: %v", err)
	}
	for rows.Next() {
		var s AppState
		err := rows.Scan(
			&s.ID, &s.StateRollback, &s.Checksum, &s.Schema, &s.RoleName,
			&s.Version, &s.GitCommit, &s.PgVersion, &s.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed decoding appstate: %v", err)
		}
		stateItems = append(stateItems, s)
	}
	switch {
	case len(stateItems) == 0:
		return &AppStateRollout{}, nil
	case len(stateItems) == 1:
		return &AppStateRollout{First: &stateItems[0]}, nil
	default:
		// TODO: Current, Canary
		return &AppStateRollout{&stateItems[0], &stateItems[1]}, nil
	}
}

func insertAppState(tx *sql.Tx, s AppState) error {
	_, err := tx.Exec(`
INSERT INTO private.appstate(state_rollback, checksum, schema, role_name, version, pgversion, commit)
VALUES ($1, $2, $3, $4, $5, (SELECT VERSION()), $6)`,
		s.StateRollback, s.Checksum, s.Schema, s.RoleName, s.Version, s.GitCommit)
	return err
}

func bootstrapState(roleName, pgUser, connectionString string) (*AppState, error) {
	if err := validateAppState(); err != nil {
		return nil, err
	}
	db, err := sql.Open("postgres", connectionString)
	if err != nil {
		return nil, fmt.Errorf("failed opening connection with postgres, err=%v", err)
	}
	ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*10)
	defer cancelFn()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed opening transaction, reason=%v", err)
	}
	if _, err := tx.Exec("LOCK TABLE private.appstate IN ACCESS EXCLUSIVE MODE"); err != nil {
		return nil, fmt.Errorf("failed locking appstate table, reason=%v", err)
	}
	log := log.With("role", roleName, "pguser", pgUser, "version", version.Get().Version)
	log.Infof("start bootstrap current state")

	ls, err := getLastAppState(tx)
	if err != nil {
		return nil, err
	}
	currentStateChecksum := getCurrentAppStateChecksum()
	if checksumMatches, state := ls.GetAppState(currentStateChecksum); checksumMatches {
		return state, nil
	}
	newState := AppState{
		StateRollback: currentStateRollback,
		Checksum:      currentStateChecksum,
		Schema:        "",
		RoleName:      "",
		Version:       version.Get().Version,
		GitCommit:     version.Get().GitCommit,
	}
	var dropStatements []string
	switch {
	case ls.First == nil && ls.Second == nil:
		// 1. if none of the state exists, rollout to public a schema (don't drop anything)
		newState.Schema = publicSchemeA
		newState.RoleName = fmt.Sprintf("%s_a", roleName)
	case ls.First != nil && ls.Second == nil:
		// 2. if first state exists and second doesn't, rollout to public b schema (don't drop anything)
		newState.Schema = publicSchemeB
		newState.RoleName = fmt.Sprintf("%s_b", roleName)
	default:
		// these records must always be distinct between each other
		if ls.First.Schema == ls.Second.Schema {
			return nil, fmt.Errorf("app state is inconsistent")
		}
		// 3. if both state exists, rollout to inverse of first schema (drop based on rollback statement)
		dropStatements = parseRollbackStatements(ls.Second.StateRollback, ls.Second.Schema)

		// match always the inverse of the current app state
		newState.Schema = publicSchemeA
		newState.RoleName = fmt.Sprintf("%s_a", roleName)
		if ls.First.Schema == publicSchemeA {
			newState.Schema = publicSchemeB
			newState.RoleName = fmt.Sprintf("%s_b", roleName)
		}
	}

	appStateSQL, err := parseAppStateSQL(newState.RoleName, pgUser, newState.Schema)
	if err != nil {
		return nil, err
	}

	// rollback existent content if it's applicable
	for _, dropStmt := range dropStatements {
		if _, err := tx.Exec(dropStmt); err != nil {
			return nil, fmt.Errorf("failed cleanup last app state, statement=%v, reason=%v", dropStmt, err)
		}
	}

	log.Infof("rolling out app state to schema %v with role %v, content-length=%v",
		newState.Schema, newState.RoleName, len(appStateSQL))
	if _, err := tx.Exec(appStateSQL); err != nil {
		return nil, fmt.Errorf("failed applying current app state, reason=%v", err)
	}
	if err := insertAppState(tx, newState); err != nil {
		return nil, fmt.Errorf("failed updating app state, reason=%v", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed commiting current app state changes, reason=%v", err)
	}
	return &newState, nil
}
