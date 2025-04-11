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
	_ "github.com/microsoft/go-mssqldb"
)

func mssqlRoleStatement(user, password, privStatement string) (string, error) {
	res := &bytes.Buffer{}
	err := template.Must(template.New("").Parse(privStatement)).
		Execute(res, map[string]string{"user": user})
	if err != nil {
		return "", fmt.Errorf("failed generating role SQL statement: %v", err)
	}
	t := template.Must(template.New("").Parse(`
BEGIN TRANSACTION;

-- Create or alter LOGIN with password
IF NOT EXISTS (SELECT * FROM sys.server_principals WHERE name = '{{ .user }}')
BEGIN
	CREATE LOGIN {{ .user }} WITH PASSWORD = '{{ .password }}';
END
ELSE
	ALTER LOGIN {{ .user }} WITH PASSWORD = '{{ .password }}';

-- Obtain existent databases in the instace
DECLARE @DBName NVARCHAR(100)
DECLARE db_cursor CURSOR FOR
SELECT name FROM sys.databases WHERE name NOT IN ('master', 'model', 'msdb', 'tempdb', 'rdsadmin')

OPEN db_cursor
FETCH NEXT FROM db_cursor INTO @DBName

-- Iterate over all databases creating the user and associating to roles
WHILE @@FETCH_STATUS = 0
BEGIN
	DECLARE @SQL NVARCHAR(MAX)
  SET @SQL = N'
  USE ' + QUOTENAME(@DBName) + '
  IF NOT EXISTS (SELECT * FROM sys.database_principals WHERE name = ''{{ .user }}'')
  BEGIN
    CREATE USER {{ .user }} FOR LOGIN {{ .user }};
  END
  -- role statements
  {{ .statement }}';
  EXEC sp_executesql @SQL;
  FETCH NEXT FROM db_cursor INTO @DBName
END

CLOSE db_cursor
DEALLOCATE db_cursor
COMMIT;
	`))

	roleStatement := res.String()
	res = &bytes.Buffer{}
	err = t.Execute(res, map[string]string{
		"user":      user,
		"password":  password,
		"statement": roleStatement,
	})
	if err != nil {
		return "", fmt.Errorf("failed generating the role query statement: %v", err)
	}
	return res.String(), nil
}

// https://learn.microsoft.com/en-us/sql/relational-databases/security/authentication-access/database-level-roles?view=sql-server-ver16#fixed-database-roles
var sqlServerPrivileges = map[roleNameType]string{
	readOnlyRoleName: "ALTER ROLE db_datareader ADD MEMBER {{ .user }};",
	readWriteRoleName: `ALTER ROLE db_datareader ADD MEMBER {{ .user }};
ALTER ROLE db_datawriter ADD MEMBER {{ .user }}`,
	adminRoleName: `ALTER ROLE db_datareader ADD MEMBER {{ .user }};
ALTER ROLE db_datawriter ADD MEMBER {{ .user }}
ALTER ROLE db_ddladmin ADD MEMBER {{ .user }}`,
}

func provisionMSSQLRoles(r pbsystem.DBProvisionerRequest) *pbsystem.DBProvisionerResponse {
	db, err := sql.Open("sqlserver", fmt.Sprintf("sqlserver://%s:%s@%s?database=master",
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

	log.With("sid", r.SID, "engine", r.DatabaseType).Infof("starting provisioning roles")
	res := pbsystem.NewDbProvisionerResponse(r.SID, "", "")
	for _, roleName := range roleNames {
		result := provisionMSSQLRole(db, r, roleName)

		res.Result = append(res.Result, result)
	}
	return res
}

func provisionMSSQLRole(db *sql.DB, r pbsystem.DBProvisionerRequest, roleName roleNameType) *pbsystem.Result {
	userRole := fmt.Sprintf("%s_%s", rolePrefixName, roleName)
	randomPasswd, err := generateRandomPassword()
	if err != nil {
		return pbsystem.NewResultError("failed generating password for user role %v: %v", userRole, err)
	}

	statement, err := mssqlRoleStatement(userRole, randomPasswd, sqlServerPrivileges[roleName])
	if err != nil {
		return pbsystem.NewResultError("failed generating SQL statement for user role %v: %v", userRole, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), connectionTimeoutDuration)
	defer cancel()
	if _, err := db.ExecContext(ctx, statement); err != nil {
		return pbsystem.NewResultError(err.Error())
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
			DefaultDatabase:        "master",
			Options:                map[string]string{},
		},
	}
}
