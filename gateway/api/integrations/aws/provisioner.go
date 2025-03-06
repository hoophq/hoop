package awsintegration

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/aws/smithy-go/ptr"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	pbsys "github.com/hoophq/hoop/common/proto/sys"
	apiconnections "github.com/hoophq/hoop/gateway/api/connections"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	transportsys "github.com/hoophq/hoop/gateway/transport/sys"
)

type provisioner struct {
	cancelFn   context.CancelFunc
	ctx        context.Context
	orgID      string
	apiRequest openapi.CreateDBRoleJob
	rdsClient  *rds.Client
}

func NewRDSProvisioner(orgID string, apiRequest openapi.CreateDBRoleJob, rdsClient *rds.Client) *provisioner {
	ctx, cancelFn := context.WithCancel(context.Background())
	return &provisioner{
		rdsClient:  rdsClient,
		orgID:      orgID,
		apiRequest: apiRequest,
		ctx:        ctx,
		cancelFn:   cancelFn,
	}
}

func (p *provisioner) getDBIdentifier() string {
	parts := strings.Split(p.apiRequest.AWS.InstanceArn, ":")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func (p *provisioner) RunOnBackground(jobID string) {
	dbArn := p.apiRequest.AWS.InstanceArn
	go func() {
		dbEnvID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("%s:%s", p.orgID, dbArn))).String()
		env, err := models.GetEnvVarByID(p.orgID, dbEnvID)
		if err != nil && err != models.ErrNotFound {
			p.updateJob(pbsys.NewError(jobID, "failed obtaining master user password: %v", err))
			return
		}
		if err == models.ErrNotFound {
			log.Infof("master user password not found, modifying the instance %v", dbArn)
			randomPasswd, err := generateRandomPassword()
			if err != nil {
				p.updateJob(pbsys.NewError(jobID, "failed generating master user password: %v", err))
				return
			}
			// TODO: add context cancel here
			_, err = p.rdsClient.ModifyDBInstance(context.Background(), &rds.ModifyDBInstanceInput{
				DBInstanceIdentifier: aws.String(p.getDBIdentifier()),
				MasterUserPassword:   aws.String(randomPasswd),
				ApplyImmediately:     aws.Bool(true),
			})
			if err != nil {
				p.updateJob(pbsys.NewError(jobID, "failed modifying db instance: %v", err))
				return
			}

			backoff := time.Second * 10
			for attempt := 1; ; attempt++ {
				time.Sleep(backoff)

				select {
				case <-p.ctx.Done():
					p.updateJob(pbsys.NewError(jobID, "context done: %v", p.ctx.Err()))
					return
				default:
				}

				// TODO: add context cancel here
				db, err := getDbInstance(p.rdsClient, dbArn)
				if err != nil {
					p.updateJob(pbsys.NewError(jobID, "failed obtaining db instance: %v", err))
					return
				}

				if ptr.ToString(db.DBInstanceStatus) == "available" {
					env = &models.EnvVar{
						OrgID:     p.orgID,
						ID:        dbEnvID,
						UpdatedAt: time.Now().UTC(),
					}
					env.SetEnv("DATABASE_TYPE", ptr.ToString(db.Engine))
					env.SetEnv("DATABASE_HOSTNAME", ptr.ToString(db.Endpoint.Address))
					env.SetEnv("DATABASE_PORT", ptr.ToInt32(db.Endpoint.Port))
					env.SetEnv("MASTER_USERNAME", ptr.ToString(db.MasterUsername))
					env.SetEnv("MASTER_PASSWORD", randomPasswd)
					if err := models.UpsertEnvVar(env); err != nil {
						p.updateJob(pbsys.NewError(jobID, "failed updating master credentials: %v", err))
						return
					}
					log.Infof("database %v is available, ready to provision roles", dbArn)
					break
				}

				if attempt >= 30 {
					p.updateJob(pbsys.NewError(jobID, "max attempt reached (%v) on changing master user", attempt))
					return
				}

				log.Infof("database %v is not available (%v), backoff (%v), attempt (%v)",
					dbArn, ptr.ToString(db.DBInstanceStatus), backoff.String(), attempt)
			}
		}

		request := pbsys.DBProvisionerRequest{
			OrgID:            env.OrgID,
			ResourceID:       dbArn,
			SID:              jobID,
			DatabaseHostname: env.GetEnv("DATABASE_HOSTNAME"),
			DatabasePort:     env.GetEnv("DATABASE_PORT"),
			MasterUsername:   env.GetEnv("MASTER_USERNAME"),
			MasterPassword:   env.GetEnv("MASTER_PASSWORD"),
			DatabaseType:     env.GetEnv("DATABASE_TYPE"),
		}

		resp := transportsys.RunDBProvisioner(p.apiRequest.AgentID, &request)
		if resp.Status == pbsys.StatusCompletedType {
			if err := p.handleConnectionProvision(request.DatabaseType, resp); err != nil {
				log.With("sid", jobID).Errorf("failed provisioning connections: %v", err)
				resp.Status = pbsys.StatusFailedType
				resp.Message = fmt.Sprintf("Failed provisioning connections: %v", err)
			}
		}

		p.updateJob(resp)
	}()
}

func (p *provisioner) updateJob(resp *pbsys.DBProvisionerResponse) {
	if resp.Status == pbsys.StatusFailedType {
		log.With("sid", resp.SID).Warnf(resp.String())
	}
	completedAt := time.Now().UTC()
	if err := models.UpdateDBRoleJob(p.orgID, &completedAt, resp); err != nil {
		log.With("sid", resp.SID).Warnf("unable to update job: %v", err)
	}
}

func (p *provisioner) handleConnectionProvision(databaseType string, resp *pbsys.DBProvisionerResponse) error {
	var connections []*models.Connection
	for _, result := range resp.Result {
		connSubtype := coerceToSubtype(databaseType)
		defaultCmd, _ := apiconnections.GetConnectionDefaults("database", connSubtype, true)
		connections = append(connections, &models.Connection{
			OrgID:              p.orgID,
			Name:               p.apiRequest.ConnectionPrefixName + result.RoleSuffixName,
			AgentID:            sql.NullString{String: p.apiRequest.AgentID, Valid: true},
			Type:               "database",
			SubType:            sql.NullString{String: connSubtype, Valid: true},
			Command:            defaultCmd,
			Status:             models.ConnectionStatusOnline,
			AccessModeRunbooks: "enabled",
			AccessModeExec:     "enabled",
			AccessModeConnect:  "enabled",
			AccessSchema:       "enabled",
			Envs:               parseEnvVars(databaseType, result.Credentials),
		})
	}
	return models.UpsertBatchConnections(connections)
}

func (p *provisioner) Cancel() { p.cancelFn() }

func coerceToSubtype(databaseType string) string {
	switch databaseType {
	case "postgres", "mysql":
		return databaseType
	case "sqlserver-ee", "sqlserver-se", "sqlserver-ex", "sqlserver-web":
		return "mssql"
	}
	return databaseType
}

func parseEnvVars(databaseType string, cred *pbsys.DBCredentials) map[string]string {
	var dbName string
	switch databaseType {
	case "postgres", "mysql":
		dbName = databaseType
	case "sqlserver-ee", "sqlserver-se", "sqlserver-ex", "sqlserver-web":
		dbName = "master"
	}
	return map[string]string{
		"envvar:HOST": base64.StdEncoding.EncodeToString([]byte(cred.Host)),
		"envvar:PORT": base64.StdEncoding.EncodeToString([]byte(cred.Port)),
		"envvar:USER": base64.StdEncoding.EncodeToString([]byte(cred.User)),
		"envvar:PASS": base64.StdEncoding.EncodeToString([]byte(cred.Password)),
		"envvar:DB":   base64.StdEncoding.EncodeToString([]byte(dbName)),
	}
}

func generateRandomPassword() (string, error) {
	// Character set for passwords (lowercase, uppercase, numbers, special chars)
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789$*_"
	passwordLength := 25

	// Create a byte slice to store the password
	password := make([]byte, passwordLength)

	// Generate random bytes
	_, err := rand.Read(password)
	if err != nil {
		return "", err
	}

	// Map random bytes to characters in the charset
	for i := 0; i < passwordLength; i++ {
		// Use modulo to map the random byte to an index in the charset
		// This ensures the mapping is within the charset boundaries
		password[i] = charset[int(password[i])%len(charset)]
	}

	return string(password), nil
}

func getDbInstance(rdsClient *rds.Client, dbArn string) (*rdstypes.DBInstance, error) {
	ctx := context.TODO()
	input := &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String(dbArn),
	}
	result, err := rdsClient.DescribeDBInstances(ctx, input)
	if err != nil {
		return nil, err
	}
	if len(result.DBInstances) == 0 {
		return nil, fmt.Errorf("db instance not found")
	}
	return &result.DBInstances[0], nil
}
