package dcm

import (
	"context"
	"database/sql"
	"fmt"
	"hash/crc32"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

type EngineType string

const (
	postgresEngineType   = "postgres"
	defaultRenewDuration = time.Hour * 12
)

type UserCredentials struct {
	Username string
	Password string
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
	t := crc32.MakeTable(crc32.IEEE)
	p.Name = fmt.Sprintf("_hoop_session_user_%08x", crc32.Checksum([]byte(p.Name), t))
	return p, p.validate()
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
func NewCredentials(dcmData map[string]any, dbUserPassword string) (*UserCredentials, error) {
	pol, err := parseConfig(dcmData)
	if err != nil {
		return nil, err
	}
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
	createRoleStmt := fmt.Sprintf(`CREATE ROLE "%v" WITH LOGIN ENCRYPTED PASSWORD '%v' VALID UNTIL '%v'`,
		pol.dbUserName,
		dbUserPassword,
		validUntil.Format(time.RFC3339),
	)
	// ttl, _ := time.ParseDuration(fmt.Sprintf("%v", dcmData["ttl"]))
	// ttl := time.Hour * 1
	_, err = db.ExecContext(ctx, createRoleStmt)
	switch v := err.(type) {
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

	return &UserCredentials{
		Username: pol.dbUserName,
		Password: dbUserPassword,
		RevokeAt: validUntil,
		TTL:      pol.RenewDuration,
	}, tx.Commit()
}

func NewRandomPassword() string { return uuid.NewString() }
