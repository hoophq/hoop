package dcm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/crc32"
	"math/rand"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/runopsio/hoop/common/log"
	"github.com/sethvargo/go-password/password"
	"github.com/xo/dburl"
	"go.uber.org/zap"
)

var (
	errUnknownEngine          = errors.New("unknown policy database engine")
	errMissingPolicyName      = errors.New("missing policy name")
	errMissingInstancesConfig = errors.New("missing policy instances configuration")
	errMissingGrantPrivileges = errors.New("missing policy grant privileges")
	errMissingChecksum        = errors.New("missing policy checksum")
	errMissingExpiration      = errors.New("missing policy expiration")
)

type EngineType string

const (
	postgresEngineType    = "postgres"
	defaultRenewDuration  = time.Hour * 12
	prefixSessionUserName = "_hoop_session_user_"
)

type Credentials struct {
	sessionID string

	policyName            string
	policyEngine          EngineType
	policyExpiration      time.Duration
	policyInstances       []string
	policyGrantPrivileges []string
	policyMainDatasource  string
	policyChecksum        string

	Username string
	Password string
	Host     string
	Port     string
	RevokeAt time.Time

	dbSuperUser     string
	dbSuperUserPwd  string
	dbDriverOptions string
}

func (c *Credentials) IsExpired() bool { return c.RevokeAt.Before(time.Now().UTC()) }
func (c *Credentials) Expiration() string {
	return fmt.Sprintf("%vm", c.RevokeAt.Sub(time.Now().UTC()).Minutes())
}
func (c *Credentials) Engine() string      { return string(c.policyEngine) }
func (c *Credentials) Checksum() string    { return string(c.policyChecksum) }
func (c *Credentials) redactSuperUserPwd() { c.dbSuperUserPwd = "******" }

func (c *Credentials) logContext() *zap.SugaredLogger {
	return log.With("sid", c.sessionID, "name", c.policyName, "engine", c.policyEngine,
		"instances", c.policyInstances, "privileges", c.policyGrantPrivileges,
		"checksum", c.policyChecksum, "expiration", c.policyExpiration.String())
}

func (c *Credentials) parseDatasource() error {
	dbURL, err := dburl.Parse(c.policyMainDatasource)
	if err != nil {
		return fmt.Errorf("failed parsing datasource: %v", err)
	}

	qs := dbURL.Query()
	qs.Set("connect_timeout", "5")

	dbPort := dbURL.Port()
	if c.policyEngine == "postgres" && dbPort == "" {
		dbPort = "5432"
	}
	c.dbSuperUser = dbURL.User.Username()
	superUserPwd, isset := dbURL.User.Password()
	if !isset {
		return fmt.Errorf("missing user password from datasource")
	}
	c.dbSuperUserPwd = superUserPwd
	c.dbDriverOptions = qs.Encode()
	c.Host = dbURL.Hostname()
	c.Port = dbPort

	datasourceFn := func(pwd string) string {
		return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?%s",
			c.dbSuperUser, pwd,
			c.Host, c.Port,
			"postgres", c.dbDriverOptions)
	}
	c.policyMainDatasource = datasourceFn(c.dbSuperUserPwd)
	c.logContext().Infof("main datasource %v", datasourceFn("******"))
	return nil
}

func (c *Credentials) validate() error {
	switch {
	case c.policyName == "<nil>" || c.policyName == "":
		return errMissingPolicyName
	case c.policyEngine != postgresEngineType:
		return errUnknownEngine
	case len(c.policyInstances) == 0:
		return errMissingInstancesConfig
	case len(c.policyGrantPrivileges) == 0:
		return errMissingGrantPrivileges
	case c.policyChecksum == "":
		return errMissingChecksum
	}
	return nil
}

// ProvisionSessionUser manages database credentials and privileges for postgres instances.
//
// dcmData is a map with the following structure
//
//	map[string]any{
//		"name":             string,
//		"engine":           string,
//		"datasource":       string,
//		"instances":        []string,
//		"expiration":   string/duration,
//		"grant-privileges": []string,
//		"checksum":         string,
//	}
func ProvisionSessionUser(sessionID string, dcmData map[string]any, dbUserPassword string) (*Credentials, error) {
	cred, err := parseConfig(dcmData, dbUserPassword)
	if err != nil {
		return nil, err
	}
	cred.RevokeAt = time.Now().UTC().Add(cred.policyExpiration)
	// redact the manager password
	// from memory for security sake
	defer cred.redactSuperUserPwd()
	return cred, provisionPostgres(cred, dbUserPassword)
}

func provisionPostgres(cred *Credentials, passwd string) error {
	validUntil := time.Now().UTC().Add(cred.policyExpiration)
	if err := provisionPgUser(cred, passwd, validUntil); err != nil {
		return err
	}
	cred.logContext().Infof("provisioning users")
	for _, dbinstance := range cred.policyInstances {
		dbname, schema := parseDbSchema(dbinstance)
		err := func() error {
			dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?%s",
				cred.dbSuperUser, cred.dbSuperUserPwd,
				cred.Host, cred.Port,
				dbname, cred.dbDriverOptions)
			db, err := sql.Open("postgres", dsn)
			if err != nil {
				return fmt.Errorf("failed connecting to postgres host/db %s/%s, err=%v",
					cred.Host, dbinstance, err)
			}
			ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*5)
			defer cancelFn()
			defer db.Close()

			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return fmt.Errorf("failed beginning transaction, err=%v", err)
			}
			for _, stmt := range grantPrivilegesStmt(cred.Username, schema, cred.policyGrantPrivileges) {
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return fmt.Errorf("failed executing revoke statement, err=%v", err)
				}
			}
			return tx.Commit()
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

func provisionPgUser(cred *Credentials, passwd string, validUntil time.Time) error {
	db, err := sql.Open("postgres", cred.policyMainDatasource)
	if err != nil {
		return fmt.Errorf("failed connecting to postgres instance, err=%v", err)
	}
	ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*5)
	defer cancelFn()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer db.Close()
	createRoleStmt := createRoleStmt(cred.Username, passwd, validUntil.Format(time.RFC3339))
	cred.logContext().Infof("creating database role %s", cred.Username)
	_, err = db.ExecContext(ctx, createRoleStmt)
	switch v := err.(type) {
	case nil: // noop
	case *pq.Error:
		if v.Code.Name() == "duplicate_object" {
			cred.logContext().Infof("role already exists, updating with new expiration")
			stmt := alterRoleStmt(cred.Username, passwd, validUntil.Format(time.RFC3339))
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("failed updating expiration %v", err)
			}
		}
	default:
		return err
	}
	return tx.Commit()
}

func parseConfig(dcmData map[string]any, dbUserPassword string) (*Credentials, error) {
	engineType := EngineType(fmt.Sprintf("%v", dcmData["engine"]))
	instances, _ := dcmData["instances"].([]string)
	grantPrivileges, _ := dcmData["grant-privileges"].([]string)
	policyExpiration, err := time.ParseDuration(fmt.Sprintf("%v", dcmData["expiration"]))
	if err != nil {
		return nil, fmt.Errorf("failed parsing expiration: %v", err)
	}
	if policyExpiration == 0 {
		return nil, errMissingExpiration
	}
	cred := &Credentials{
		policyName:            fmt.Sprintf("%v", dcmData["name"]),
		policyEngine:          engineType,
		policyMainDatasource:  fmt.Sprintf("%v", dcmData["datasource"]),
		policyInstances:       instances,
		policyExpiration:      policyExpiration,
		policyGrantPrivileges: grantPrivileges,
		policyChecksum:        fmt.Sprintf("%v", dcmData["checksum"]),

		// RevokeAt: time.Now().UTC().Add(policyExpiration),
		Password: dbUserPassword,
	}
	if err := cred.parseDatasource(); err != nil {
		return nil, err
	}
	sort.Strings(cred.policyGrantPrivileges)
	sort.Strings(cred.policyInstances)

	// this allows creating a unique user for distinct policies
	// without the need revoking privileges or removing users when a policy changes.
	//
	// This could be insecure if an engine doesn't enforce any sort of expirating
	// the creation of users.
	crc32Suffix := fmt.Sprintf(
		"%s:%s",
		strings.Join(cred.policyInstances, ","),
		strings.Join(cred.policyGrantPrivileges, ","),
	)

	cred.Username = newSessionUserName(crc32Suffix)
	return cred, cred.validate()
}

func parseDbSchema(dbinstance string) (dbname string, schema string) {
	parts := strings.Split(dbinstance, ".")
	if len(parts) == 1 {
		return dbinstance, "public"
	}
	schema = parts[len(parts)-1]
	if schema == "" {
		schema = "public"
	}
	return strings.Join(parts[:len(parts)-1], "."), schema
}

func newSessionUserName(suffixData string) string {
	t := crc32.MakeTable(crc32.IEEE)
	return fmt.Sprintf("%s%08x", prefixSessionUserName, crc32.Checksum([]byte(suffixData), t))
}

func NewRandomPassword() (string, error) {
	length := rand.Intn(35-25) + 25
	numDigits := rand.Intn(15-10) + 10
	return password.Generate(length, numDigits, 0, false, true)
}
