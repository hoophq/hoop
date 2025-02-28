package awsintegration

import (
	"context"
	"crypto/rand"
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
	"github.com/hoophq/hoop/gateway/models"
	transportsys "github.com/hoophq/hoop/gateway/transport/sys"
)

type provisioner struct {
	result    chan *pbsys.DBProvisionerResponse
	cancelFn  context.CancelFunc
	ctx       context.Context
	dbArn     string
	orgID     string
	agentID   string
	rdsClient *rds.Client
}

type provisionerResult struct {
	err  error
	resp *pbsys.DBProvisionerResponse
}

func NewRDSProvisioner(dbArn, orgID, agentID string, rdsClient *rds.Client) *provisioner {
	// TODO: add context cancel with cause
	ctx, cancelFn := context.WithCancel(context.Background())
	return &provisioner{
		rdsClient: rdsClient,
		dbArn:     dbArn,
		orgID:     orgID,
		agentID:   agentID,
		result:    make(chan *pbsys.DBProvisionerResponse),
		ctx:       ctx,
		cancelFn:  cancelFn,
	}
}

func (p *provisioner) getDBIdentifier() string {
	parts := strings.Split(p.dbArn, ":")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func (p *provisioner) Run(jobID string) <-chan *pbsys.DBProvisionerResponse {
	// change RDS
	go func() {
		// change RDS user / password
		// in case of error return and close it

		// validate the update_at and expire it
		dbEnvID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("%s:%s", p.orgID, p.dbArn))).String()
		env, err := models.GetEnvVarByID(p.orgID, dbEnvID)
		if err != nil && err != models.ErrNotFound {
			p.result <- pbsys.NewError(jobID, "failed obtaining master user password: %v", err)
			close(p.result)
			return
		}
		if err == models.ErrNotFound {
			log.Infof("master user password not found, modifying the instance %v", p.dbArn)
			randomPasswd, err := generateRandomPassword()
			if err != nil {
				p.result <- pbsys.NewError(jobID, "failed generating master user password: %v", err)
				close(p.result)
				return
			}
			// TODO: add context cancel here
			_, err = p.rdsClient.ModifyDBInstance(context.Background(), &rds.ModifyDBInstanceInput{
				DBInstanceIdentifier: aws.String(p.getDBIdentifier()),
				MasterUserPassword:   aws.String(randomPasswd),
				ApplyImmediately:     aws.Bool(true), // Apply change immediately
			})
			if err != nil {
				p.result <- pbsys.NewError(jobID, err.Error())
				close(p.result)
				return
			}

			backoff := time.Second * 10
			for attempt := 1; ; attempt++ {
				time.Sleep(backoff)

				select {
				case <-p.ctx.Done():
					p.result <- pbsys.NewError(jobID, "%v", p.ctx.Err())
					close(p.result)
					return
				default:
				}

				// TODO: add context cancel here
				db, err := getDbInstance(p.rdsClient, p.dbArn)
				if err != nil {
					p.result <- pbsys.NewError(jobID, "%v", err)
					close(p.result)
					return
				}

				if ptr.ToString(db.DBInstanceStatus) == "available" {
					env = &models.EnvVar{
						OrgID:     p.orgID,
						ID:        dbEnvID,
						UpdatedAt: time.Now().UTC(),
					}
					env.SetEnv("DATABASE_TYPE", ptr.ToString(db.Engine))
					env.SetEnv("HOSTNAME", ptr.ToString(db.Endpoint.Address))
					env.SetEnv("MASTER_USERNAME", ptr.ToString(db.MasterUsername))
					env.SetEnv("MASTER_PASSWORD", randomPasswd)
					if err := models.UpsertEnvVar(env); err != nil {
						p.result <- pbsys.NewError(jobID, "%v", err)
						close(p.result)
						return
					}
					log.Infof("database %v is available, ready to provision roles", p.dbArn)
					break
				}

				if attempt >= 30 {
					p.result <- pbsys.NewError(jobID, "max attempt reached (%v) on changing master user", attempt)
					close(p.result)
					return
				}

				log.Infof("database %v is not available (%v), backoff (%v), attempt (%v)",
					p.dbArn, ptr.ToString(db.DBInstanceStatus), backoff.String(), attempt)
			}
		}

		resp := transportsys.RunDBProvisioner(p.agentID, &pbsys.DBProvisionerRequest{
			OrgID:          env.OrgID,
			SID:            jobID,
			EndpointAddr:   env.GetEnv("HOSTNAME"),
			MasterUsername: env.GetEnv("MASTER_USERNAME"),
			MasterPassword: env.GetEnv("MASTER_PASSWORD"),
			DatabaseType:   env.GetEnv("DATABASE_TYPE"),
		})

		updateErr := models.UpdateDBRoleJobSpec(p.orgID, jobID, "COMPLETED", resp.ErrorMessage)
		if updateErr != nil {
			log.Warnf("unable to update job %v: %v", jobID, updateErr)
		}

		p.result <- resp
		close(p.result)
		// 1. send event to provision users via agent
	}()
	return p.result
}

func (p *provisioner) Cancel() { p.cancelFn() }

func generateRandomPassword() (string, error) {
	// Character set for passwords (lowercase, uppercase, numbers, special chars)
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789$%*_"
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
