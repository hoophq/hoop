package pgrest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/version"
)

// CheckLiveness validates if the postgrest process is running
// by checking if the port is open and responding.
func CheckLiveness() error {
	timeout := time.Second * 3
	conn, err := net.DialTimeout("tcp", baseURL.Host, timeout)
	if err != nil {
		return fmt.Errorf("not responding, err=%v", err)
	}
	_ = conn.Close()
	return nil
}

// Run performs the initalization and necessary migrations to
// start the postgrest process. This function will take care of
// managing the process lifecycle of postgrest.
func Run() (err error) {
	baseURL, jwtSecretKey, err = loadConfig()
	if err != nil {
		return err
	}
	roleName = os.Getenv("PGREST_ROLE")
	if roleName == "" {
		roleName = "hoop_apiuser"
	}
	postgrestBinFile := "/usr/local/bin/postgrest"
	if _, err := os.Stat(postgrestBinFile); err != nil && os.IsNotExist(err) {
		return nil
	}
	// validate if the migration files are present
	if _, err := os.Stat("/app/migrations/000001_init.up.sql"); err != nil {
		return fmt.Errorf("failed validating migration files, err=%v", err)
	}

	pgConnectionURI := os.Getenv("POSTGRES_DB_URI")
	pgURL, err := url.Parse(pgConnectionURI)
	if err != nil {
		return fmt.Errorf("failed parsing POSTGRES_DB_URI, err=%v", err)
	}
	var pgUser string
	if pgURL.User != nil {
		pgUser = pgURL.User.Username()
	}
	if pgUser == "" {
		return fmt.Errorf("invalid format for POSTGRES_DB_URI, missing user")
	}
	// migration
	m, err := migrate.New("file:///app/migrations", pgConnectionURI)
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
	if err != nil && err != migrate.ErrNilVersion && err != migrate.ErrNoChange {
		return fmt.Errorf("failed running db migration, err=%v", err)
	}
	log.Infof("processed db migration with success, nochange=%v", err == migrate.ErrNoChange)

	appState, err := BootstrapState(roleName, pgUser, pgConnectionURI)
	if err != nil {
		return err
	}
	// change role name
	roleName = appState.RoleName
	schemaName = appState.Schema
	log.Infof("bootstrap with success to schema=%v", appState.Schema)
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
		fmt.Sprintf("PGRST_DB_SCHEMAS=%s,%s", publicSchemeA, publicSchemeB),
		fmt.Sprintf("PGRST_DB_URI=%s", pgConnectionURI),
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
		return &AppStateRollout{&stateItems[0], &stateItems[1]}, nil
	}
	// if s.Checksum == "" || s.StateRollback == "" || s.CreatedAt.IsZero() {
	// 	return nil, fmt.Errorf("unable to scan properly, found empty data, object=%#v", s)
	// }
	// return &s, nil
}

func insertAppState(tx *sql.Tx, s AppState) error {
	_, err := tx.Exec(`
INSERT INTO private.appstate(state_rollback, checksum, schema, role_name, version, pgversion, commit)
VALUES ($1, $2, $3, $4, $5, (SELECT VERSION()), $6)`,
		s.StateRollback, s.Checksum, s.Schema, s.RoleName, s.Version, s.GitCommit)
	return err
}

func BootstrapState(roleName, pgUser, connectionString string) (*AppState, error) {
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

		// log.With("dropstatements", len(currentState.rollbackStmt), "checksum", currentState.checksum).
		// Infof("initializing bootstrap process, oldversion=%v,oldchecksum=%v,oldrole=%v,oldpgver=%v,created_at=%v",
		// 	ls.Version, ls.Checksum, ls.RoleName, ls.PgVersion, ls.CreatedAt.Format(time.RFC3339))
		// 3. if both state exists, rollout to inverse of first schema (drop based on rollback statement)
		dropStatements = parseRollbackStatements(ls.Second.StateRollback, ls.Second.Schema)

		// match always the inverse of the current app state
		newState.Schema = publicSchemeA
		newState.RoleName = fmt.Sprintf("%s_a", roleName)
		if ls.First.Schema == publicSchemeA {
			newState.Schema = publicSchemeB
			newState.RoleName = fmt.Sprintf("%s_b", roleName)
		}

		// log.With("dropstatements", len(currentState.rollbackStmt), "checksum", currentState.checksum).
		// Infof("initializing bootstrap process, oldversion=%v,oldchecksum=%v,oldrole=%v,oldpgver=%v,created_at=%v",
		// 	ls.Version, ls.Checksum, ls.RoleName, ls.PgVersion, ls.CreatedAt.Format(time.RFC3339))

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

	log.Infof("rollout app state to schema %v with role %v, content-length=%v",
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

// var grantStatements = []string{
// 	`CREATE ROLE %s LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOSUPERUSER`,
// 	`COMMENT ON ROLE %s IS 'Used to authenticate requests in postgrest'`,
// 	`GRANT usage ON SCHEMA public TO %s`,
// 	`GRANT usage ON SCHEMA private TO %s`,

// 	`GRANT SELECT, INSERT ON public.orgs TO %s`,
// 	`GRANT INSERT, SELECT, UPDATE on public.login TO %s`,
// 	`GRANT SELECT, INSERT, UPDATE, DELETE ON public.users TO %s`,
// 	`GRANT SELECT, INSERT, UPDATE, DELETE ON public.user_groups TO %s`,
// 	`GRANT SELECT, INSERT, UPDATE ON public.serviceaccounts TO %s`,
// 	`GRANT SELECT, INSERT, UPDATE, DELETE ON public.connections TO %s`,
// 	`GRANT SELECT, INSERT, UPDATE, DELETE ON public.env_vars TO %s`,
// 	`GRANT SELECT, INSERT, UPDATE, DELETE ON public.agents TO %s`,
// 	`GRANT SELECT, INSERT, UPDATE, DELETE ON public.plugin_connections TO %s`,
// 	`GRANT SELECT, INSERT, UPDATE ON public.plugins TO %s`,
// 	`GRANT SELECT, INSERT, UPDATE ON public.sessions TO %s`,
// 	`GRANT SELECT, INSERT, UPDATE ON public.blobs TO %s`,
// 	`GRANT SELECT, INSERT, UPDATE ON public.reviews TO %s`,
// 	`GRANT SELECT, INSERT, UPDATE ON public.review_groups TO %s`,
// 	`GRANT SELECT, INSERT, UPDATE, DELETE ON public.proxymanager_state TO %s`,
// 	`GRANT SELECT, INSERT, UPDATE, DELETE ON public.clientkeys TO %s`,
// 	`GRANT SELECT, INSERT, UPDATE ON public.audit TO %s`,
// }

// var dropStatements = []string{
// 	`REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM %s`,
// 	`REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM %s`,
// 	`REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public FROM %s`,
// 	`REVOKE USAGE ON SCHEMA private FROM %s`,
// 	`REVOKE USAGE ON SCHEMA public FROM %s`,
// 	`DROP ROLE IF EXISTS %s`,
// }

// func provisionPgRoles(roleName, pgUser, connectionString string) error {
// 	log.Infof("provisioning default role %s", roleName)
// 	db, err := sql.Open("postgres", connectionString)
// 	if err != nil {
// 		return fmt.Errorf("failed opening connection with postgres, err=%v", err)
// 	}
// 	ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*10)
// 	defer cancelFn()
// 	tx, err := db.BeginTx(ctx, nil)
// 	if err != nil {
// 		return fmt.Errorf("failed opening transaction, err=%v", err)
// 	}
// 	// validate if the role exists before dropping it
// 	var res any
// 	err = tx.QueryRow(`SELECT 1 FROM pg_roles WHERE rolname = $1`, roleName).Scan(&res)
// 	if err != nil && err != sql.ErrNoRows {
// 		return fmt.Errorf("failed querying pg_roles table, errType=%T, err=%v", err, err)
// 	}
// 	// drop all privileges of the role and then the role itself
// 	if fmt.Sprintf("%v", res) == "1" {
// 		for _, stmt := range dropStatements {
// 			if _, err := tx.Exec(fmt.Sprintf(stmt, roleName)); err != nil {
// 				return fmt.Errorf("fail to drop current role %v, err=%v", roleName, err)
// 			}
// 		}
// 	}
// 	for _, stmt := range grantStatements {
// 		if _, err := tx.Exec(fmt.Sprintf(stmt, roleName)); err != nil {
// 			return fmt.Errorf("failed executing statement %q, err=%v", stmt, err)
// 		}
// 	}
// 	// allow the main role to impersonate the apiuser role
// 	impersonateGrantStmt := fmt.Sprintf(`GRANT %s TO %s`, roleName, pgUser)
// 	if _, err := tx.Exec(impersonateGrantStmt); err != nil {
// 		return fmt.Errorf("failed granting impersonate grant, err=%v", err)
// 	}
// 	if err := tx.Commit(); err != nil {
// 		return fmt.Errorf("failed committing transaction, err=%v", err)
// 	}
// 	return nil
// }
