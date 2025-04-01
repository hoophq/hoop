package awsintegration

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go/ptr"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	pbsystem "github.com/hoophq/hoop/common/proto/system"
	apiconnections "github.com/hoophq/hoop/gateway/api/connections"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/transport/plugins/webhooks"
	transportsystem "github.com/hoophq/hoop/gateway/transport/system"
)

const defaultSecurityGroupDescription = "Database ingress rule for connectivity with Hoop Agent"

type provisioner struct {
	cancelFn    context.CancelFunc
	ctx         context.Context
	orgID       string
	apiRequest  openapi.CreateDBRoleJob
	identity    *sts.GetCallerIdentityOutput
	rdsClient   *rds.Client
	ec2Client   *ec2.Client
	environment string
}

type modifyInstanceInput struct {
	instanceIdentifier       string
	instanceCusterIdentifier string
	vpcSecurityGroupIds      []string
	masterUserPassword       *string
	isAurora                 bool
}

func NewRDSProvisioner(orgID string, sts *sts.GetCallerIdentityOutput, apiRequest openapi.CreateDBRoleJob, rdsClient *rds.Client, ec2Client *ec2.Client) *provisioner {
	ctx, cancelFn := context.WithCancel(context.Background())
	return &provisioner{
		rdsClient:   rdsClient,
		ec2Client:   ec2Client,
		identity:    sts,
		orgID:       orgID,
		apiRequest:  apiRequest,
		ctx:         ctx,
		cancelFn:    cancelFn,
		environment: appconfig.Get().ApiHostname(),
	}
}

func (p *provisioner) hasStep(stepType openapi.DBRoleJobStepType) bool {
	return slices.Contains(p.apiRequest.JobSteps, stepType)
}

func (p *provisioner) Run(jobID string) error {
	dbArn := p.apiRequest.AWS.InstanceArn
	db, err := p.getDbInstance(dbArn)
	if err != nil {
		return fmt.Errorf("failed fetching db instance, reason=%v", err)
	}
	err = models.CreateDBRoleJob(&models.DBRole{
		OrgID: p.orgID,
		ID:    jobID,
		Spec: &models.AWSDBRoleSpec{
			AccountArn:    ptr.ToString(p.identity.Arn),
			AccountUserID: ptr.ToString(p.identity.UserId),
			DBArn:         ptr.ToString(db.DBInstanceArn),
			DBName:        ptr.ToString(db.DBName),
			DBEngine:      ptr.ToString(db.Engine),
			Tags:          parseAWSTags(db),
		},
		Status: &models.DBRoleStatus{
			Phase:   pbsystem.StatusRunningType,
			Message: "",
			Result:  nil,
		},
	})
	if err != nil {
		return fmt.Errorf("unable to create db role job, err=%v", err)
	}

	startedAt := time.Now().UTC()
	go func() {
		defaultSg, securityGroupID := p.apiRequest.AWS.DefaultSecurityGroup, ""
		if defaultSg != nil {
			sgName := "hoop-aws-connect-sg-" + ptr.ToString(db.DBInstanceIdentifier)
			log.With("sid", jobID).Infof("synchronizing security group, sgname=%v, vpc_id=%v, ingress_cidr=%v, port=%v",
				sgName, ptr.ToString(db.DBSubnetGroup.VpcId), defaultSg.IngressCIDR, ptr.ToInt32(db.Endpoint.Port))
			securityGroupID, err = p.syncSecurityGroup(
				sgName,
				ptr.ToString(db.DBSubnetGroup.VpcId),
				defaultSg.IngressCIDR,
				ptr.ToInt32(db.Endpoint.Port),
			)
			if err != nil {
				p.updateJob(pbsystem.NewError(jobID, err.Error()))
				return
			}
		}

		dbEnvID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("%s:%s", p.orgID, dbArn))).String()
		env, err := models.GetEnvVarByID(p.orgID, dbEnvID)
		if err != nil && err != models.ErrNotFound {
			p.updateJob(pbsystem.NewError(jobID, "failed obtaining master user password: %v", err))
			return
		}

		instInput := &modifyInstanceInput{
			instanceIdentifier:       ptr.ToString(db.DBInstanceIdentifier),
			instanceCusterIdentifier: ptr.ToString(db.DBClusterIdentifier),
			isAurora:                 strings.HasPrefix(ptr.ToString(db.Engine), "aurora"),
			vpcSecurityGroupIds:      nil,
			masterUserPassword:       nil,
		}

		if securityGroupID != "" {
			var securityGroupIDs []string
			for _, sg := range db.VpcSecurityGroups {
				securityGroupIDs = append(securityGroupIDs, *sg.VpcSecurityGroupId)
			}
			securityGroupIDs = append(securityGroupIDs, securityGroupID)
			instInput.vpcSecurityGroupIds = securityGroupIDs
		}

		switch err {
		case models.ErrNotFound:
			log.With("sid", jobID).Infof("master user password not found, modifying the instance %v", dbArn)
			randomPasswd, err := generateRandomPassword()
			if err != nil {
				p.updateJob(pbsystem.NewError(jobID, "failed generating master user password: %v", err))
				return
			}
			instInput.masterUserPassword = &randomPasswd
			err = p.modifyRDSInstance(jobID, instInput, func() error {
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
					return fmt.Errorf("failed updating master credentials: %v", err)
				}
				return nil
			})
			if err != nil {
				p.updateJob(pbsystem.NewError(jobID, "failed modifying db instance: %v", err))
				return
			}
		case nil:
			if err := p.modifyRDSInstance(jobID, instInput, func() error { return nil }); err != nil {
				p.updateJob(pbsystem.NewError(jobID, "failed modifying db instance: %v", err))
				return
			}
		default:
			p.updateJob(pbsystem.NewError(jobID, "failed obtaining master user password: %v", err))
			return
		}
		log.With("sid", jobID).Infof("database is available, ready to provision roles for %v", dbArn)
		request := pbsystem.DBProvisionerRequest{
			OrgID:            env.OrgID,
			ResourceID:       dbArn,
			SID:              jobID,
			DatabaseHostname: env.GetEnv("DATABASE_HOSTNAME"),
			DatabasePort:     env.GetEnv("DATABASE_PORT"),
			MasterUsername:   env.GetEnv("MASTER_USERNAME"),
			MasterPassword:   env.GetEnv("MASTER_PASSWORD"),
			DatabaseType:     env.GetEnv("DATABASE_TYPE"),
		}

		// set vault provider if it's set
		if p.apiRequest.VaultProvider != nil {
			request.Vault = &pbsystem.VaultProvider{
				SecretID: p.apiRequest.VaultProvider.SecretID,
			}
		}

		resp := transportsystem.RunDBProvisioner(p.apiRequest.AgentID, &request)
		if resp.Status == pbsystem.StatusCompletedType && p.hasStep(openapi.DBRoleJobStepCreateConnections) {
			if err := p.handleConnectionProvision(request.DatabaseType, resp); err != nil {
				log.With("sid", jobID).Errorf("failed provisioning connections: %v", err)
				resp.Status = pbsystem.StatusFailedType
				resp.Message = fmt.Sprintf("Failed provisioning connections: %v", err)
			}
		}

		var webhookSent bool
		if res := p.updateJob(resp); res != nil && p.hasStep(openapi.DBRoleJobStepSendWebhook) {
			err = p.sendWebhook(res)
			if err != nil {
				log.With("sid", jobID).Warnf("failed sending webhook, reason=%v", err)
			}
			webhookSent = err == nil
		}

		log.With("sid", jobID).Infof("database provisioner finished, name=%v, engine=%v, status=%v, with-security-group=%v, webhook-sent=%v, duration=%v, message=%v",
			ptr.ToString(db.DBInstanceIdentifier), ptr.ToString(db.Engine), resp.Status, defaultSg != nil,
			webhookSent, time.Now().UTC().Sub(startedAt).String(), resp.Message)

	}()
	return nil
}

func (p *provisioner) modifyRDSInstance(jobID string, input *modifyInstanceInput, instanceAvailableCallback func() error) error {
	var err error
	if input.isAurora {
		_, err = p.rdsClient.ModifyDBCluster(context.Background(), &rds.ModifyDBClusterInput{
			DBClusterIdentifier: &input.instanceCusterIdentifier,
			ApplyImmediately:    aws.Bool(true),
			VpcSecurityGroupIds: input.vpcSecurityGroupIds,
			MasterUserPassword:  input.masterUserPassword,
		})
	} else {
		_, err = p.rdsClient.ModifyDBInstance(context.Background(), &rds.ModifyDBInstanceInput{
			DBInstanceIdentifier: &input.instanceIdentifier,
			ApplyImmediately:     aws.Bool(true),
			VpcSecurityGroupIds:  input.vpcSecurityGroupIds,
			MasterUserPassword:   input.masterUserPassword,
		})
	}
	if err != nil {
		return fmt.Errorf("failed modifying db instance: %v", err)
	}

	dbArn := p.apiRequest.AWS.InstanceArn
	backoff := time.Second * 10
	for attempt := 1; ; attempt++ {
		time.Sleep(backoff)

		select {
		case <-p.ctx.Done():
			return fmt.Errorf("context done: %v", p.ctx.Err())
		default:
		}

		// TODO: add context cancel here
		db, err := p.getDbInstance(dbArn)
		if err != nil {
			return fmt.Errorf("failed obtaining db instance: %v", err)
		}

		if ptr.ToString(db.DBInstanceStatus) == "available" {
			return instanceAvailableCallback()
		}

		if attempt >= 30 {
			return fmt.Errorf("max attempt reached (%v) on changing master user", attempt)
		}

		log.With("sid", jobID).Infof("database is not available, arn=%v, status=%v, backoff=%v, attempt=%v",
			dbArn, ptr.ToString(db.DBInstanceStatus), backoff.String(), attempt)
	}
}

func (p *provisioner) updateJob(resp *pbsystem.DBProvisionerResponse) *models.DBRole {
	if resp.Status == pbsystem.StatusFailedType {
		log.With("sid", resp.SID).Warnf(resp.String())
	}
	completedAt := time.Now().UTC()
	job, err := models.UpdateDBRoleJob(p.orgID, &completedAt, resp)
	if err != nil {
		log.With("sid", resp.SID).Warnf("unable to update job: %v", err)
		return nil
	}
	return job
}

func (p *provisioner) handleConnectionProvision(databaseType string, resp *pbsystem.DBProvisionerResponse) error {
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
			Envs:               parseEnvVars(result.Credentials),
		})
	}
	return models.UpsertBatchConnections(connections)
}

func (p *provisioner) Cancel() { p.cancelFn() }

func coerceToSubtype(databaseType string) string {
	switch databaseType {
	case "postgres", "mysql":
		return databaseType
	case "aurora-postgresql":
		return "postgres"
	case "aurora-mysql":
		return "mysql"
	case "sqlserver-ee", "sqlserver-se", "sqlserver-ex", "sqlserver-web":
		return "mssql"
	}
	return databaseType
}

func parseEnvVars(cred *pbsystem.DBCredentials) map[string]string {
	switch cred.SecretsManagerProvider {
	case pbsystem.SecretsManagerProviderDatabase:
		return map[string]string{
			"envvar:HOST": b64enc(cred.Host),
			"envvar:PORT": b64enc(cred.Port),
			"envvar:USER": b64enc(cred.User),
			"envvar:PASS": b64enc(cred.Password),
			"envvar:DB":   b64enc(cred.DefaultDatabase),
		}
	case pbsystem.SecretsManagerProviderVault:
		return map[string]string{
			"envvar:HOST": b64enc("_vaultkv2:%s:HOST", cred.SecretID),
			"envvar:PORT": b64enc("_vaultkv2:%s:PORT", cred.SecretID),
			"envvar:USER": b64enc("_vaultkv2:%s:USER", cred.SecretID),
			"envvar:PASS": b64enc("_vaultkv2:%s:PASSWORD", cred.SecretID),
			"envvar:DB":   b64enc("_vaultkv2:%s:DB", cred.SecretID),
		}
	}
	return nil
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

func (p *provisioner) getDbInstance(dbArn string) (*rdstypes.DBInstance, error) {
	ctx := context.TODO()
	input := &rds.DescribeDBInstancesInput{
		DBInstanceIdentifier: aws.String(dbArn),
	}
	result, err := p.rdsClient.DescribeDBInstances(ctx, input)
	if err != nil {
		return nil, err
	}
	if len(result.DBInstances) == 0 {
		return nil, fmt.Errorf("db instance not found")
	}
	return &result.DBInstances[0], nil
}

func (p *provisioner) getSGByName(vpcID, sgName string) (*ec2types.SecurityGroup, error) {
	// Create the describe security groups input
	input := &ec2.DescribeSecurityGroupsInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("group-name"),
				Values: []string{sgName},
			},
			{
				Name:   aws.String("vpc-id"),
				Values: []string{vpcID},
			},
		},
	}

	output, err := p.ec2Client.DescribeSecurityGroups(context.TODO(), input)
	if err != nil {
		return nil, err
	}
	if len(output.SecurityGroups) > 0 {
		return &output.SecurityGroups[0], nil
	}
	return nil, nil
}

func (p *provisioner) syncSecurityGroup(sgName, vpcID, ingressCIDR string, dbPort int32) (groupID string, err error) {
	sg, err := p.getSGByName(vpcID, sgName)
	if err != nil {
		return "", err
	}

	if sg == nil {
		createSgInput := &ec2.CreateSecurityGroupInput{
			GroupName:   aws.String(sgName),
			Description: aws.String(defaultSecurityGroupDescription),
			VpcId:       aws.String(vpcID),
			TagSpecifications: []ec2types.TagSpecification{
				{
					ResourceType: ec2types.ResourceTypeSecurityGroup,
					Tags: []ec2types.Tag{
						{
							Key:   aws.String("Name"),
							Value: aws.String(sgName),
						},
						{
							Key:   aws.String("hoop.dev/gateway"),
							Value: aws.String(p.environment),
						},
					},
				},
			},
		}

		createSgOutput, err := p.ec2Client.CreateSecurityGroup(context.TODO(), createSgInput)
		if err != nil {
			return "", fmt.Errorf("unable to create security group: %v", err)
		}
		sg = &ec2types.SecurityGroup{GroupId: createSgOutput.GroupId}
	}

	// check if the rule being set is already present in the security group
	var isAuthorized bool
	for _, perm := range sg.IpPermissions {
		if ptr.ToInt32(perm.FromPort) == dbPort &&
			ptr.ToInt32(perm.ToPort) == dbPort {
			for _, iprange := range perm.IpRanges {
				if ptr.ToString(iprange.CidrIp) == ingressCIDR {
					isAuthorized = true
					break
				}
			}
		}
		if isAuthorized {
			break
		}
	}

	if !isAuthorized {
		authInput := &ec2.AuthorizeSecurityGroupIngressInput{
			GroupId: sg.GroupId,
			IpPermissions: []ec2types.IpPermission{
				{
					IpProtocol: aws.String("tcp"),
					FromPort:   aws.Int32(dbPort),
					ToPort:     aws.Int32(dbPort),
					IpRanges: []ec2types.IpRange{
						{
							CidrIp:      aws.String(ingressCIDR),
							Description: aws.String(defaultSecurityGroupDescription),
						},
					},
				},
			},
		}

		_, err = p.ec2Client.AuthorizeSecurityGroupIngress(context.TODO(), authInput)
		if err != nil {
			return "", fmt.Errorf("unable to add inbound rules: %v", err)
		}
	}
	return ptr.ToString(sg.GroupId), nil
}

func (p *provisioner) sendWebhook(obj *models.DBRole) error {
	apiObj := toDBRoleOpenAPI(obj)
	jsonData, err := json.Marshal(apiObj)
	if err != nil {
		return fmt.Errorf("failed encoding to json: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(jsonData, &payload); err != nil {
		return fmt.Errorf("failed decoding json to map: %v", err)
	}

	if err := p.sendWebhookCustom(*apiObj); err != nil {
		return err
	}
	return webhooks.SendMessage(p.orgID, webhooks.EventDBRoleJobFinishedType, map[string]any{
		"event_type":    webhooks.EventDBRoleJobFinishedType,
		"event_payload": payload,
	})
}

func (p *provisioner) sendWebhookCustom(job openapi.DBRoleJob) error {
	payload := map[string]any{
		"engine":                job.Spec.DBEngine,
		"tags":                  job.Spec.DBTags,
		"usr_dbre_namespace_ro": map[string]any{},
		"usr_dbre_namespace":    map[string]any{},
	}
	vaultKeys := map[string]any{}
	if job.Status != nil && job.Status.Phase == "completed" {
		for _, res := range job.Status.Result {
			if res.CredentialsInfo.SecretsManagerProvider == openapi.SecretsManagerProviderVault {
				vaultKeys[res.UserRole] = map[string]any{
					"envs":      res.CredentialsInfo.SecretKeys,
					"namespace": res.CredentialsInfo.SecretID,
				}
			}
		}
	}

	payload["vault_keys"] = vaultKeys
	return webhooks.SendMessage(p.orgID, webhooks.EventDBRoleJobCustomFinishedType, map[string]any{
		"event_type":    webhooks.EventDBRoleJobCustomFinishedType,
		"event_payload": payload,
	})

}

func parseAWSTags(obj *rdstypes.DBInstance) []map[string]any {
	v := []map[string]any{}
	for _, t := range obj.TagList {
		v = append(v, map[string]any{ptr.ToString(t.Key): ptr.ToString(t.Value)})
	}
	return v
}

func b64enc(format string, v ...any) string {
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf(format, v...)))
}
