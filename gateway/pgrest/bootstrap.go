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
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
	"github.com/runopsio/hoop/common/log"
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

	// migration
	m, err := migrate.New("file:///app/migrations", PgConnectionURI())
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

	if err := provisionPgRoles(roleName); err != nil {
		return fmt.Errorf("failed provisioning roles: %v", err)
	}

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
		fmt.Sprintf("PGRST_DB_URI=%s", PgConnectionURI()),
		fmt.Sprintf("PGRST_JWT_SECRET=%s", string(jwtSecretKey)),
	}

	startProcessFn := func(i int) {
		log.Infof("starting postgrest process, attempt=%v ...", i)
		cmd := exec.Command(postgrestBinFile)
		cmd.Env = envs
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
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

func PgConnectionURI() string {
	pgDbURI := os.Getenv("POSTGRES_DB_URI")
	if pgDbURI != "" {
		return pgDbURI
	}
	log.Warnf("using legacy postgres connection uri configuration, sslmode is disabled. Please use POSTGRES_DB_URI instead")
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		os.Getenv("PG_USER"),
		os.Getenv("PG_PASSWORD"),
		os.Getenv("PG_HOST"),
		os.Getenv("PG_PORT"),
		os.Getenv("PG_DB"),
	)
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
	return pgrestUrl, []byte(base64.RawURLEncoding.EncodeToString(secretRandomBytes)), nil
}

var grantStatements = []string{
	`CREATE ROLE %s LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOSUPERUSER`,
	`COMMENT ON ROLE %s IS 'Used to authenticate requests in postgrest'`,
	`GRANT usage ON SCHEMA public TO %s`,
	`GRANT usage ON SCHEMA private TO %s`,

	`GRANT SELECT, INSERT ON public.orgs TO %s`,
	`GRANT INSERT, SELECT, UPDATE on public.login TO %s`,
	`GRANT SELECT, INSERT, UPDATE ON public.users TO %s`,
	`GRANT SELECT, INSERT, UPDATE, DELETE ON public.user_groups TO %s`,
	`GRANT SELECT, INSERT, UPDATE ON public.serviceaccounts TO %s`,
	`GRANT SELECT, INSERT, UPDATE, DELETE ON public.connections TO %s`,
	`GRANT SELECT, INSERT, UPDATE, DELETE ON public.env_vars TO %s`,
	`GRANT SELECT, INSERT, UPDATE, DELETE ON public.agents TO %s`,
	`GRANT SELECT, INSERT, UPDATE, DELETE ON public.plugin_connections TO %s`,
	`GRANT SELECT, INSERT, UPDATE ON public.plugins TO %s`,
	`GRANT SELECT, INSERT, UPDATE ON public.sessions TO %s`,
	`GRANT SELECT, INSERT, UPDATE ON public.blobs TO %s`,
	`GRANT SELECT, INSERT, UPDATE ON public.reviews TO %s`,
	`GRANT SELECT, INSERT, UPDATE ON public.review_groups TO %s`,
	`GRANT SELECT, INSERT, UPDATE, DELETE ON public.proxymanager_state TO %s`,
	`GRANT SELECT, INSERT, UPDATE, DELETE ON public.clientkeys TO %s`,
}

var dropStatements = []string{
	`REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM %s`,
	`REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public FROM %s`,
	`REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public FROM %s`,
	`REVOKE USAGE ON SCHEMA private FROM %s`,
	`REVOKE USAGE ON SCHEMA public FROM %s`,
	`DROP ROLE IF EXISTS %s`,
}

func provisionPgRoles(roleName string) error {
	log.Infof("provisioning default role %s", roleName)
	db, err := sql.Open("postgres", PgConnectionURI())
	if err != nil {
		return fmt.Errorf("failed opening connection with postgres, err=%v", err)
	}
	ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*10)
	defer cancelFn()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed opening transaction, err=%v", err)
	}
	// validate if the role exists before dropping it
	var res any
	err = tx.QueryRow(`SELECT 1 FROM pg_roles WHERE rolname = $1`, roleName).Scan(&res)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("failed querying pg_roles table, errType=%T, err=%v", err, err)
	}
	// drop all privileges of the role and then the role itself
	if fmt.Sprintf("%v", res) == "1" {
		for _, stmt := range dropStatements {
			if _, err := tx.Exec(fmt.Sprintf(stmt, roleName)); err != nil {
				return fmt.Errorf("fail to drop current role %v, err=%v", roleName, err)
			}
		}
	}
	for _, stmt := range grantStatements {
		if _, err := tx.Exec(fmt.Sprintf(stmt, roleName)); err != nil {
			return fmt.Errorf("failed executing statement %q, err=%v", stmt, err)
		}
	}
	// allow the main role to impersonate the apiuser role
	impersonateGrantStmt := fmt.Sprintf(`GRANT %s TO %s`, roleName, os.Getenv("PG_USER"))
	if _, err := tx.Exec(impersonateGrantStmt); err != nil {
		return fmt.Errorf("failed granting impersonate grant, err=%v", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed committing transaction, err=%v", err)
	}
	return nil
}
