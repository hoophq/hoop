package dbprovisioner

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"text/template"
	"time"

	"github.com/hoophq/hoop/common/log"
	pbsystem "github.com/hoophq/hoop/common/proto/system"
	_ "github.com/lib/pq"
)

func postgresRoleStatement(user, password, privileges string) (string, error) {
	res := &bytes.Buffer{}
	t := template.Must(template.New("").Parse(`
DO $$
  DECLARE
    role_count int;
    db_schema_name text;
BEGIN
  -- create role or alter the password
  SELECT COUNT(*) INTO role_count FROM pg_roles WHERE rolname = '{{ .user }}';
  IF role_count > 0 THEN
    ALTER ROLE "{{ .user }}" WITH LOGIN ENCRYPTED PASSWORD '{{ .password }}';
  ELSE
    CREATE ROLE "{{ .user }}" WITH LOGIN ENCRYPTED PASSWORD '{{ .password }}' NOINHERIT NOCREATEDB NOCREATEROLE NOSUPERUSER;
  END IF;

  -- grant the privileges to the new or existing role for all schemas
  FOR db_schema_name IN
    SELECT schema_name
    FROM information_schema.schemata
    WHERE schema_name NOT IN ('information_schema', 'pg_catalog', 'pg_toast')
  LOOP
    EXECUTE 'GRANT USAGE ON SCHEMA ' || db_schema_name || ' TO "{{ .user }}"';
    EXECUTE 'GRANT {{ .privileges }} ON ALL TABLES IN SCHEMA ' || db_schema_name || ' TO "{{ .user }}"';
  END LOOP;
END$$;
	`))
	err := t.Execute(res, map[string]string{
		"user":       user,
		"password":   password,
		"privileges": privileges,
	})
	if err != nil {
		return "", fmt.Errorf("failed generating the role query statement: %v", err)
	}
	return res.String(), nil
}

var postgresPrivileges = map[roleNameType]string{
	readOnlyRoleName:  "SELECT",
	readWriteRoleName: "SELECT, INSERT, UPDATE, DELETE",
	adminRoleName:     "SELECT, INSERT, UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER",
}

func provisionPostgresRoles(r pbsystem.DBProvisionerRequest) *pbsystem.DBProvisionerResponse {
	db, err := sql.Open("postgres", fmt.Sprintf("postgres://%s:%s@%s/postgres?connect_timeout=5",
		r.MasterUsername, r.MasterPassword, r.Address()))
	if err != nil {
		return pbsystem.NewError(r.SID, "failed to create database connection: %s", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), connectionTimeoutDuration)
	defer cancel()

	// Ping actually tests the connection
	err = db.PingContext(ctx)
	if err != nil {
		return pbsystem.NewError(r.SID, "failed to connect to engine %v, host=%v, user=%v, reason=%v",
			r.DatabaseType, r.DatabaseHostname, r.MasterUsername, err)
	}
	rows, err := db.QueryContext(
		ctx,
		`SELECT datname as dbname FROM pg_database WHERE datname NOT IN ('template0', 'template1', 'rdsadmin')`)

	if err != nil {
		return pbsystem.NewError(r.SID, "failed listing databases: %v", err)
	}
	var dbNames []string
	for rows.Next() {
		var dbName string
		if err := rows.Scan(&dbName); err != nil {
			return pbsystem.NewError(r.SID, "failed reading column name: %v", err)
		}
		dbNames = append(dbNames, dbName)
	}

	if len(dbNames) == 0 {
		return pbsystem.NewError(r.SID, "cannot find any databases to provision roles")
	}

	log.With("sid", r.SID, "engine", r.DatabaseType).Infof("starting provisioning roles for the following databases: %v", dbNames)
	res := pbsystem.NewDbProvisionerResponse(r.SID, "", "")
	for _, roleName := range roleNames {
		result := provisionPostgresRole(r, dbNames, roleName)
		res.Result = append(res.Result, result)
	}

	return res
}

func provisionPostgresRole(r pbsystem.DBProvisionerRequest, dbNames []string, roleName roleNameType) *pbsystem.Result {
	userRole := fmt.Sprintf("%s_%s", rolePrefixName, roleName)
	randomPasswd, err := generateRandomPassword()
	if err != nil {
		return pbsystem.NewResultError("failed generating password for user role %v: %v", userRole, err)
	}

	for _, dbName := range dbNames {
		res := func() *pbsystem.Result {
			db, err := sql.Open("postgres", fmt.Sprintf("postgres://%s:%s@%s/%s?connect_timeout=5",
				r.MasterUsername, r.MasterPassword, r.Address(), dbName))
			if err != nil {
				return pbsystem.NewResultError("failed to create database connection: %v", err)
			}
			defer db.Close()

			ctx, cancel := context.WithTimeout(context.Background(), connectionTimeoutDuration)
			defer cancel()

			statement, err := postgresRoleStatement(userRole, randomPasswd, postgresPrivileges[roleName])
			if err != nil {
				return pbsystem.NewResultError("failed generating SQL statement for user role %v: %v", userRole, err)
			}
			if _, err := db.ExecContext(ctx, statement); err != nil {
				return pbsystem.NewResultError(err.Error())
			}
			return nil
		}()
		if res != nil {
			return res
		}
	}

	return &pbsystem.Result{
		RoleSuffixName: string(roleName),
		Status:         pbsystem.StatusCompletedType,
		Message:        "",
		CompletedAt:    time.Now().UTC(),
		Credentials: &pbsystem.DBCredentials{
			SecretsManagerProvider: pbsystem.SecretsManagerProviderDatabase,
			SecretID:               "",
			SecretKeys:             []string{},
			Host:                   r.DatabaseHostname,
			Port:                   r.Port(),
			User:                   userRole,
			Password:               randomPasswd,
			DefaultDatabase:        "postgres",
			Options:                map[string]string{},
		},
	}
}
