package controllersys

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"text/template"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/hoophq/hoop/common/log"
	pbsys "github.com/hoophq/hoop/common/proto/sys"
)

func mysqlRoleStatement(user, password, privileges string) (string, error) {
	res := &bytes.Buffer{}
	t := template.Must(template.New("").Parse(`
START TRANSACTION;
DROP USER IF EXISTS '{{ .user }}';
CREATE USER '{{ .user }}'@'%' IDENTIFIED BY '{{ .password }}';
GRANT {{ .privileges }} ON *.* TO '{{ .user }}'@'%';
FLUSH PRIVILEGES;
COMMIT;
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

var mysqlPrivileges = map[roleNameType]string{
	readOnlyRoleName:  "SELECT",
	readWriteRoleName: "SELECT, INSERT, UPDATE, DELETE",
	adminRoleName:     "SELECT, INSERT, UPDATE, DELETE, ALTER, CREATE, DROP",
}

func provisionMySQLRoles(r pbsys.DBProvisionerRequest) *pbsys.DBProvisionerResponse {
	db, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s)/mysql?multiStatements=true",
		r.MasterUsername, r.MasterPassword, r.Address()))
	if err != nil {
		return pbsys.NewError(r.SID, "failed to create database connection: %s", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Ping actually tests the connection
	err = db.PingContext(ctx)
	if err != nil {
		return pbsys.NewError(r.SID, "failed to connect to engine %v: %v", r.DatabaseType, err)
	}

	log.With("sid", r.SID, "engine", r.DatabaseType).Infof("starting provisioning roles")
	res := pbsys.NewDbProvisionerResponse(r.SID, "", "")
	for _, roleName := range roleNames {
		result := provisionMySQLRole(db, r, roleName)
		res.Result = append(res.Result, result)
	}
	return res
}

func provisionMySQLRole(db *sql.DB, r pbsys.DBProvisionerRequest, roleName roleNameType) *pbsys.Result {
	userRole := fmt.Sprintf("%s_%s", rolePrefixName, roleName)
	randomPasswd, err := generateRandomPassword()
	if err != nil {
		return pbsys.NewResultError("failed generating password for user role %v: %v", userRole, err)
	}

	statement, err := mysqlRoleStatement(userRole, randomPasswd, mysqlPrivileges[roleName])
	if err != nil {
		return pbsys.NewResultError("failed generating SQL statement for user role %v: %v", userRole, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, statement); err != nil {
		return pbsys.NewResultError(err.Error())
	}
	return &pbsys.Result{
		RoleSuffixName: string(roleName),
		Status:         pbsys.StatusCompletedType,
		Message:        "",
		CompletedAt:    time.Now().UTC(),
		Credentials: &pbsys.DBCredentials{
			SecretsManagerProvider: pbsys.SecretsManagerProviderDatabase,
			Host:                   r.DatabaseHostname,
			Port:                   r.Port(),
			User:                   userRole,
			Password:               randomPasswd,
			DefaultDatabase:        "mysql",
			Options:                map[string]string{},
		},
	}
}
