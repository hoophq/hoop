package dcm

import (
	"context"
	"database/sql"
	"fmt"
	"hash/crc32"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/xo/dburl"
)

type EngineType string

const (
	postgresEngineType   = "postgres"
	defaultRenewDuration = time.Hour * 12
)

type UserCredentials struct {
	Host     string
	Port     string
	Username string
	Password string
	Engine   string
	RevokeAt time.Time
	TTL      time.Duration
}

func (u *UserCredentials) IsExpired() bool { return u.RevokeAt.Before(time.Now().UTC()) }
func (u *UserCredentials) Expiration() string {
	return fmt.Sprintf("%vm", u.RevokeAt.Sub(time.Now().UTC()).Minutes())
}

type Policy struct {
	Name            string
	Engine          EngineType
	Datasource      string
	RenewDuration   time.Duration
	Instances       []string
	GrantPrivileges []string

	dbUserName string

	dbSuperUser    string
	dbSuperUserPwd string
	dbHost         string
	dbPort         string
	// sslmode=disable&timeout=5s
	dbRawOptions string
}

func parseConfig(dcmData map[string]any) (*Policy, error) {
	engineType := EngineType(fmt.Sprintf("%v", dcmData["engine"]))
	instances := dcmData["instances"].([]string)
	renew, err := time.ParseDuration(fmt.Sprintf("%v", dcmData["renew-duration"]))
	if err != nil {
		return nil, fmt.Errorf("failed parsing renew-duration: %v", err)
	}
	if renew == 0 {
		renew = defaultRenewDuration
	}
	privileges := dcmData["grant-privileges"].([]string)
	p := &Policy{
		Name:            fmt.Sprintf("%v", dcmData["name"]),
		Engine:          engineType,
		Datasource:      fmt.Sprintf("%v", dcmData["datasource"]),
		Instances:       instances,
		RenewDuration:   renew,
		GrantPrivileges: privileges,
	}
	if err := p.setDatabaseAddress(); err != nil {
		return nil, err
	}
	t := crc32.MakeTable(crc32.IEEE)
	p.dbUserName = fmt.Sprintf("_hoop_session_user_%08x", crc32.Checksum([]byte(p.Name), t))
	return p, p.validate()
}

func (p *Policy) setDatabaseAddress() error {
	dburl, err := dburl.Parse(p.Datasource)
	if err != nil {
		return err
	}
	dbPort := dburl.Port()
	switch {
	case p.Engine == "postgres" && dbPort == "":
		dbPort = "5432"
	case p.Engine == "mysql" && dbPort == "":
		dbPort = "3306"
	}
	if dbPort == "" {
		return fmt.Errorf("empty port for %q datasource", p.Engine)
	}
	p.dbSuperUser = dburl.User.Username()
	superUserPwd, isset := dburl.User.Password()
	if !isset {
		return fmt.Errorf("super user password is not set")
	}
	p.dbSuperUserPwd = superUserPwd
	p.dbRawOptions = dburl.RawQuery
	p.dbHost = dburl.Hostname()
	p.dbPort = dbPort
	return nil
}

func (p *Policy) validate() error {
	switch {
	case p.Name == "nil" || p.Name == "":
		return fmt.Errorf("missing policy name")
	case p.Engine != postgresEngineType:
		return fmt.Errorf("engine %q not supported", p.Engine)
	case p.Datasource == "nil" || p.Datasource == "":
		return fmt.Errorf("missing datasource configuration")
	case len(p.Instances) == 0:
		return fmt.Errorf("missing instances entries, must be at least one")
	case len(p.GrantPrivileges) == 0:
		return fmt.Errorf("missing privileges entries, must be at least one")

	}
	return nil
}

// NewCredentials manages database credentials and privileges for postgres instances.
//
// dcmData is a map with the following structure
//
//	map[string]any{
//		"name":             string,
//		"engine":           string,
//		"datasource":       string,
//		"instances":        []string,
//		"renew-duration":   string/duration,
//		"grant-privileges": []string,
//	}
//
// Rename to ProvisionSessionUser
func NewCredentials(dcmData map[string]any, dbUserPassword string) (*UserCredentials, error) {
	pol, err := parseConfig(dcmData)
	if err != nil {
		return nil, err
	}
	return provisionPostgres(pol, dbUserPassword)
	db, err := sql.Open("postgres", pol.Datasource)
	if err != nil {
		return nil, fmt.Errorf("failed connecting to postgres instance, err=%v", err)
	}
	// wipe the master credentials from memory for security sake
	pol.Datasource = ""
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*15)
	defer cancelFn()

	validUntil := time.Now().UTC().Add(pol.RenewDuration)
	createRoleStmt, err := parseCreateRoleStatementTmpl(pol.dbUserName, dbUserPassword, validUntil.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	createGrantsStmt, err := parseGrantPrivilegesStatementTmpl(pol.dbUserName, "dellstore", pol.GrantPrivileges)
	if err != nil {
		return nil, err
	}
	fmt.Println("GRANTS STMT->>", createGrantsStmt)
	_, err = db.ExecContext(ctx, createRoleStmt)
	switch v := err.(type) {
	case nil: // noop
	case *pq.Error:
		if v.Code.Name() == "duplicate_object" {
			updateStmt := fmt.Sprintf(`ALTER ROLE "%v" WITH LOGIN ENCRYPTED PASSWORD '%v' VALID UNTIL '%s'`,
				pol.dbUserName,
				dbUserPassword,
				validUntil.Format(time.RFC3339),
			)
			_, err := db.ExecContext(ctx, updateStmt)
			if err != nil {
				return nil, fmt.Errorf("failed updating expiration %v", err)
			}
		}
	default:
		return nil, err
	}

	if _, err := db.ExecContext(ctx, createGrantsStmt); err != nil {
		return nil, fmt.Errorf("failed creating grants, err=%v", err)
	}

	return &UserCredentials{
		Host:     pol.dbHost,
		Port:     pol.dbPort,
		Engine:   string(pol.Engine),
		Username: pol.dbUserName,
		Password: dbUserPassword,
		RevokeAt: validUntil,
		TTL:      pol.RenewDuration,
	}, tx.Commit()
}

func provisionPostgres(p *Policy, passwd string) (*UserCredentials, error) {
	validUntil := time.Now().UTC().Add(p.RenewDuration)
	if err := provisionPgUser(p, passwd, validUntil); err != nil {
		return nil, err
	}
	// provision role
	// var txmap map[string]
	for _, dbinstance := range p.Instances {
		dbname, schema, found := strings.Cut(dbinstance, ".")
		if !found || schema == "" {
			schema = "public"
		}
		err := func() error {
			dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?%s",
				p.dbSuperUser, p.dbSuperUserPwd,
				p.dbHost, p.dbPort,
				dbname, p.dbRawOptions)
			db, err := sql.Open("postgres", dsn)
			if err != nil {
				return fmt.Errorf("failed connecting to postgres host/db %s/%s, err=%v",
					p.dbHost, dbinstance, err)
			}
			ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*5)
			defer cancelFn()
			defer db.Close()
			grantPrivilegesStmt, _ := parseGrantPrivilegesStatementTmpl(p.dbUserName, schema, p.GrantPrivileges)
			fmt.Println("STAMENT->>", grantPrivilegesStmt)
			fmt.Println("DSN->>", dsn)
			_, err = db.ExecContext(ctx, grantPrivilegesStmt)
			if err != nil {
				return fmt.Errorf("failed granting privileges to %s/%s, err=%v", p.dbHost, dbinstance, err)
			}
			return nil
		}()
		if err != nil {
			return nil, err
		}
	}
	return &UserCredentials{
		Host:     p.dbHost,
		Port:     p.dbPort,
		Engine:   string(p.Engine),
		Username: p.dbUserName,
		Password: passwd,
		RevokeAt: validUntil,
		TTL:      p.RenewDuration,
	}, nil
}

func provisionPgUser(p *Policy, passwd string, validUntil time.Time) error {
	db, err := sql.Open("postgres", p.Datasource)
	if err != nil {
		return fmt.Errorf("failed connecting to postgres instance, err=%v", err)
	}
	// wipe the master credentials from memory for security sake
	// p.Datasource = ""
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*15)
	defer cancelFn()

	// validUntil := time.Now().UTC().Add(p.RenewDuration)
	createRoleStmt, err := parseCreateRoleStatementTmpl(p.dbUserName, passwd, validUntil.Format(time.RFC3339))
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, createRoleStmt)
	switch v := err.(type) {
	case nil: // noop
	case *pq.Error:
		if v.Code.Name() == "duplicate_object" {
			updateStmt := fmt.Sprintf(`ALTER ROLE "%v" WITH LOGIN ENCRYPTED PASSWORD '%v' VALID UNTIL '%s'`,
				p.dbUserName,
				passwd,
				validUntil.Format(time.RFC3339),
			)
			_, err := db.ExecContext(ctx, updateStmt)
			if err != nil {
				return fmt.Errorf("failed updating expiration %v", err)
			}
		}
	default:
		return err
	}
	return tx.Commit()
}

// TODO: add db options from root datasource
func datasourceWithDBname(user, pwd, host, dbname, port string) string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s", user, pwd, host, dbname, port)
}
func NewRandomPassword() string { return uuid.NewString() }
