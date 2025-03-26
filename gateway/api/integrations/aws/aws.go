package awsintegration

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go/ptr"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	pgagents "github.com/hoophq/hoop/gateway/pgrest/agents"
	"github.com/hoophq/hoop/gateway/storagev2"
)

const staticCrossAccountRoleArn = "arn:aws:iam::%s:role/HoopOrganizationAccountAccessRole"

// IAMUpdateAccessKey
//
//	@Summary		Update IAM Access Key
//	@Description	Update IAM Access Key or set a region when using IAM instance role
//	@Tags			AWS
//	@Accept			json
//	@Produce		json
//	@Param			request	body	openapi.IAMAccessKeyRequest	true	"The request body resource"
//	@Success		204
//	@Failure		400,422	{object}	openapi.HTTPError
//	@Router			/integrations/aws/iam/accesskeys [put]
func IAMUpdateAccessKey(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.IAMAccessKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	env := &models.EnvVar{OrgID: ctx.OrgID, ID: ctx.OrgID}
	if req.AccessKeyID != "" {
		if req.SecretAccessKey == "" {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "the attribute 'secret_access_key' is required when 'access_key_id' is set"})
			return
		}
		env.SetEnv("INTEGRATION_AWS_ACCESS_KEY_ID", req.AccessKeyID)
		env.SetEnv("INTEGRATION_AWS_SECRET_ACCESS_KEY", req.SecretAccessKey)
		env.SetEnv("INTEGRATION_AWS_SESSION_TOKEN", req.SessionToken)
	}
	env.SetEnv("INTEGRATION_AWS_REGION", req.Region)
	if err := models.UpsertEnvVar(env); err != nil {
		log.Errorf("failed updating iam access key, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

// IAMDeleteAccessKey
//
//	@Summary		Delete IAM Access Key
//	@Description	Remove IAM Access Key from storage
//	@Tags			AWS
//	@Produce		json
//	@Success		204
//	@Failure		400	{object}	openapi.HTTPError
//	@Router			/integrations/aws/iam/accesskeys [delete]
func IAMDeleteAccessKey(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	env := &models.EnvVar{OrgID: ctx.OrgID, ID: ctx.OrgID}
	if err := models.UpsertEnvVar(env); err != nil {
		log.Errorf("failed clearing iam access key, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	c.JSON(http.StatusNoContent, nil)
}

// IAMGetUserInfo
//
//	@Summary		Get Caller Identity
//	@Description	It obtain the aws identity of the instance role or credentials
//	@Tags			AWS
//	@Produce		json
//	@Success		200	{object}	openapi.IAMUserInfo
//	@Failure		400	{object}	openapi.HTTPError
//	@Router			/integrations/aws/iam/userinfo [get]
func IAMGetUserInfo(c *gin.Context) {
	cfg, i, err := loadAWSConfig(storagev2.ParseContext(c).OrgID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, openapi.IAMUserInfo{
		AccountID: ptr.ToString(i.Account),
		ARN:       ptr.ToString(i.Arn),
		UserID:    ptr.ToString(i.UserId),
		Region:    cfg.Region,
	})
}

// IAMVerifyPermissions
//
//	@Summary		Verify IAM permissions
//	@Description	Verify if the IAM permissions are configured properly
//	@Tags			AWS
//	@Produce		json
//	@Success		200	{object}	openapi.IAMVerifyPermission
//	@Failure		400	{object}	openapi.HTTPError
//	@Router			/integrations/aws/iam/verify [post]
func IAMVerifyPermissions(c *gin.Context) {
	cfg, identity, err := loadAWSConfig(storagev2.ParseContext(c).OrgID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	iamClient := iam.NewFromConfig(cfg)
	resp, err := iamClient.SimulatePrincipalPolicy(context.Background(), &iam.SimulatePrincipalPolicyInput{
		PolicySourceArn: identity.Arn,
		ActionNames: []string{
			"organizations:ListAccounts",
			"rds:ModifyDBInstance",
			"rds:ModifyDBCluster", // aurora
			"rds:DescribeDBInstances",
			"ec2:DescribeSecurityGroups",
			"ec2:AuthorizeSecurityGroupIngress",
			"ec2:CreateSecurityGroup",
			"ec2:CreateTags",
			"sts:AssumeRole",
		},
		ResourceArns: []string{"*"},
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	status := "allowed"
	evaluation := []openapi.IAMEvaluationDetail{}
	for _, r := range resp.EvaluationResults {
		if r.EvalDecision != "allowed" {
			status = "denied"
		}
		statements := []openapi.IAMEvaluationDetailStatement{}
		for _, st := range r.MatchedStatements {
			statements = append(statements, openapi.IAMEvaluationDetailStatement{
				SourcePolicyID:   ptr.ToString(st.SourcePolicyId),
				SourcePolicyType: string(st.SourcePolicyType),
			})
		}

		evaluation = append(evaluation, openapi.IAMEvaluationDetail{
			ActionName:        ptr.ToString(r.EvalActionName),
			Decision:          r.EvalDecision,
			ResourceName:      ptr.ToString(r.EvalResourceName),
			MatchedStatements: statements,
		})
	}

	c.JSON(http.StatusOK, openapi.IAMVerifyPermission{
		Status: status,
		Identity: openapi.IAMUserInfo{
			AccountID: ptr.ToString(identity.Account),
			ARN:       ptr.ToString(identity.Arn),
			UserID:    ptr.ToString(identity.UserId),
			Region:    cfg.Region,
		},
		EvaluationDetails: evaluation,
	})
}

// ListAWSAccounts
//
//	@Summary		List AWS Accounts
//	@Description	It list all AWS accounts associated with the access key credentials
//	@Tags			AWS
//	@Produce		json
//	@Success		200	{object}	openapi.ListAWSAccounts
//	@Failure		400	{object}	openapi.HTTPError
//	@Router			/integrations/aws/organizations [get]
func ListOrganizations(c *gin.Context) {
	cfg, _, err := loadAWSConfig(storagev2.ParseContext(c).OrgID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	orgClient := organizations.NewFromConfig(cfg)
	var accounts []openapi.AWSAccount
	paginator := organizations.NewListAccountsPaginator(orgClient, &organizations.ListAccountsInput{})

	for paginator.HasMorePages() {
		ctx := context.Background()
		page, err := paginator.NextPage(ctx)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Errorf("failed to get AWS accounts: %v", err).Error()})
			return
		}

		for _, acct := range page.Accounts {
			accounts = append(accounts, openapi.AWSAccount{
				AccountID:     ptr.ToString(acct.Id),
				Name:          ptr.ToString(acct.Name),
				Status:        acct.Status,
				JoinedMethods: acct.JoinedMethod,
				Email:         ptr.ToString(acct.Email),
			})
		}
	}
	c.JSON(http.StatusOK, openapi.ListAWSAccounts{Items: accounts})
}

// DescribeDBInstances
//
//	@Summary		List Database Instances
//	@Description	It list RDS Database Instances
//	@Tags			AWS
//	@Produce		json
//	@Param			request	body		openapi.ListAWSDBInstancesRequest	true	"The request body resource"
//	@Success		200		{object}	openapi.ListAWSDBInstances
//	@Failure		400		{object}	openapi.HTTPError
//	@Router			/integrations/aws/rds/describe-db-instances [post]
func DescribeRDSDBInstances(c *gin.Context) {
	// Load AWS config for management account
	// 1. filter instances based on selected accounts
	cfg, identity, err := loadAWSConfig(storagev2.ParseContext(c).OrgID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	orgClient := organizations.NewFromConfig(cfg)
	var instances []openapi.AWSDBInstance
	paginator := organizations.NewListAccountsPaginator(orgClient, &organizations.ListAccountsInput{})

	ctx := context.Background()
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Errorf("failed to get AWS accounts: %v", err).Error()})
			return
		}

		for _, acct := range page.Accounts {
			isAccountOwner := ptr.ToString(acct.Id) == ptr.ToString(identity.Account)
			items, err := listRDSInstances(ctx, cfg, ptr.ToString(acct.Id), isAccountOwner)
			if err != nil {
				log.Warnf("failed listing rds instances, is-account-owner=%v, region=%v, reason=%v", isAccountOwner, cfg.Region, err)
				c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
				return
			}

			for _, inst := range items {
				instances = append(instances, openapi.AWSDBInstance{
					AccountID:        ptr.ToString(acct.Id),
					Name:             ptr.ToString(inst.DBInstanceIdentifier),
					AvailabilityZone: ptr.ToString(inst.AvailabilityZone),
					VpcID:            ptr.ToString(inst.DBSubnetGroup.VpcId),
					ARN:              ptr.ToString(inst.DBInstanceArn),
					Engine:           ptr.ToString(inst.Engine),
					Status:           ptr.ToString(inst.DBInstanceStatus),
				})
			}
		}
	}

	c.JSON(http.StatusOK, openapi.ListAWSDBInstances{Items: instances})
}

// CreateDBRoleJob
//
//	@Summary		Create Database Role Job
//	@Description	It creates a job that performs the provisioning of default database roles
//	@Tags			AWS
//	@Produce		json
//	@Param			request	body		openapi.CreateDBRoleJob	true	"The request body resource"
//	@Success		202		{object}	openapi.CreateDBRoleJobResponse
//	@Failure		400,500	{object}	openapi.HTTPError
//	@Router			/dbroles/jobs [post]
func CreateDBRoleJob(c *gin.Context) {
	usrctx := storagev2.ParseContext(c)
	var req openapi.CreateDBRoleJob
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if req.AWS == nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "missing request attribute 'aws'"})
		return
	}
	dbArn := req.AWS.InstanceArn
	agent, err := pgagents.New().FetchOneByNameOrID(usrctx, req.AgentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": "unable to validate agent, reason=" + err.Error()})
		return
	}
	if agent == nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "agent does not exists"})
		return
	}

	resourceAWSAccountID := parseDatabaseArnAccountID(dbArn)
	if resourceAWSAccountID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Errorf("unable to parse database arn %q", dbArn)})
		return
	}

	ctx := context.Background()
	cfg, identity, err := loadAWSConfig(usrctx.OrgID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	dbAWSAccountID := parseDatabaseArnAccountID(dbArn)
	if dbAWSAccountID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Errorf("unable to parse database arn %q", dbArn)})
		return
	}

	isAccountOwner := dbAWSAccountID == ptr.ToString(identity.Account)
	if !isAccountOwner {
		newConfig, err := assumeRole(ctx, cfg, dbAWSAccountID)
		if err != nil {
			log.Errorf("failed assuming role, reason=%v", err)
			c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
			return
		}
		cfg = *newConfig
	}

	sid := uuid.NewString()
	rdsClient, ec2Client := rds.NewFromConfig(cfg), ec2.NewFromConfig(cfg)
	log.With("sid", sid).Infof("obtained client configuration with success, account-owner=%v, region=%v", isAccountOwner, cfg.Region)
	if err := NewRDSProvisioner(usrctx.OrgID, identity, req, rdsClient, ec2Client).Run(sid); err != nil {
		log.With("sid", sid).Error(err)
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, openapi.CreateDBRoleJobResponse{JobID: sid})
}

// GetDBRoleJobByID
//
//	@Summary		Get DB Role Job
//	@Description	Get DB Role job by id
//	@Tags			AWS
//	@Accept			json
//	@Produce		json
//	@Param			id			path		string	true	"The unique identifier of the resource"
//	@Success		200			{object}	openapi.DBRoleJob
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/dbroles/jobs/{id} [get]
func GetDBRoleJobByID(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	dbRole, err := models.GetDBRoleJobByID(ctx.OrgID, c.Param("id"))
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "job not found"})
	case nil:
		c.JSON(http.StatusOK, toDBRoleOpenAPI(dbRole))
	default:
		log.Errorf("failed getting db role job by id, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
	}
}

// ListDBRolesJob
//
//	@Summary		List DB Role Jobs
//	@Description	List all db role jobs
//	@Tags			AWS
//	@Produce		json
//	@Success		200	{object}	openapi.DBRoleJobList
//	@Failure		400	{object}	openapi.HTTPError
//	@Router			/dbroles/jobs [get]
func ListDBRoleJobs(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	dbRoleItems, err := models.ListDBRoleJobs(ctx.OrgID)
	if err != nil {
		log.Errorf("failed listing db role jobs, err=%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}
	var obj openapi.DBRoleJobList
	for _, item := range dbRoleItems {
		obj.Items = append(obj.Items, *toDBRoleOpenAPI(item))
	}
	c.JSON(http.StatusOK, obj)
}

func listRDSInstances(ctx context.Context, cfg aws.Config, accountID string, isAccountOwner bool) ([]types.DBInstance, error) {
	rdsClient, _, err := loadRDSClientForAccount(ctx, cfg, accountID, isAccountOwner)
	if err != nil {
		return nil, err
	}

	// Fetch RDS instances
	var instances []types.DBInstance
	paginator := rds.NewDescribeDBInstancesPaginator(rdsClient, &rds.DescribeDBInstancesInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get RDS instances for account %s: %v", accountID, err)
		}
		instances = append(instances, page.DBInstances...)
	}

	return instances, nil
}

func assumeRole(ctx context.Context, cfg aws.Config, awsAccountID string) (*aws.Config, error) {
	roleArn := fmt.Sprintf(staticCrossAccountRoleArn, awsAccountID)

	// Assume Role in target account
	stsClient := sts.NewFromConfig(cfg)
	creds := stscreds.NewAssumeRoleProvider(stsClient, roleArn)

	// Create AWS Config with assumed role credentials
	accountCfg, err := config.LoadDefaultConfig(ctx,
		config.WithCredentialsProvider(creds),
		config.WithRegion(cfg.Region),
	)
	return &accountCfg, err
}

func loadRDSClientForAccount(ctx context.Context, cfg aws.Config, accountID string, isAccountOwner bool) (rdsClient *rds.Client, assumed bool, err error) {
	// Create RDS client
	rdsClient = rds.NewFromConfig(cfg)
	if !isAccountOwner {
		accountCfg, err := assumeRole(ctx, cfg, accountID)
		if err != nil {
			return nil, false, fmt.Errorf("failed assuming role (rds) for account %s: %v", accountID, err)
		}
		rdsClient = rds.NewFromConfig(*accountCfg)
		assumed = true
	}
	return
}

func loadAWSConfig(orgID string) (cfg aws.Config, identity *sts.GetCallerIdentityOutput, err error) {
	env, err := models.GetEnvVarByID(orgID, orgID)
	if err != nil && err != models.ErrNotFound {
		return cfg, nil, err
	}
	awsRegion, hasAccessKey := "", false
	if env != nil {
		hasAccessKey, awsRegion = env.HasKey("INTEGRATION_AWS_ACCESS_KEY_ID"), env.GetEnv("INTEGRATION_AWS_REGION")
	}

	if awsRegion == "" {
		return cfg, nil, fmt.Errorf("missing AWS Region configuration")
	}

	if hasAccessKey {
		log.Debugf("using aws static credentials with region=%v", env.GetEnv("INTEGRATION_AWS_REGION"))
		staticCfg := aws.NewConfig()
		staticCfg.Credentials = credentials.NewStaticCredentialsProvider(
			env.GetEnv("INTEGRATION_AWS_ACCESS_KEY_ID"),
			env.GetEnv("INTEGRATION_AWS_SECRET_ACCESS_KEY"),
			env.GetEnv("INTEGRATION_AWS_SESSION_TOKEN"))
		staticCfg.Region = awsRegion
		cfg = staticCfg.Copy()
	}

	ctx := context.Background()
	if !hasAccessKey {
		if !appconfig.Get().IntegrationAWSInstanceRoleAllow() {
			return cfg, nil, fmt.Errorf("unable to find valid AWS credentials(instance role is turned off)")
		}

		cfg, err = config.LoadDefaultConfig(ctx, config.WithRetryMaxAttempts(1), config.WithRegion(awsRegion))
		if err != nil {
			return
		}
		cfg = cfg.Copy()
	}

	stsClient := sts.NewFromConfig(cfg)
	identity, err = stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	return
}

func parseDatabaseArnAccountID(dbArn string) string {
	// arn:aws:rds:us-west-2:<account-id>:db:<db-identifier>
	parts := strings.Split(dbArn, ":")
	if len(parts) != 7 {
		return ""
	}
	return parts[4]
}

func toDBRoleOpenAPI(o *models.DBRole) *openapi.DBRoleJob {
	var spec openapi.AWSDBRoleJobSpec
	if o.Spec != nil {
		var dbTags []openapi.DBTag
		for _, tag := range o.Spec.Tags {
			for key, val := range tag {
				dbTags = append(dbTags, openapi.DBTag{Key: key, Value: fmt.Sprintf("%v", val)})
				break // it should contain only one record
			}
		}
		spec = openapi.AWSDBRoleJobSpec{
			AccountArn: o.Spec.AccountArn,
			DBArn:      o.Spec.DBArn,
			DBName:     o.Spec.DBName,
			DBEngine:   o.Spec.DBEngine,
			DBTags:     dbTags,
		}
	}
	var status *openapi.DBRoleJobStatus
	if o.Status != nil {
		var result []openapi.DBRoleJobStatusResult
		for _, r := range o.Status.Result {
			result = append(result, openapi.DBRoleJobStatusResult{
				UserRole: r.UserRole,
				Status:   r.Status,
				Message:  r.Message,
				CredentialsInfo: openapi.DBRoleJobStatusResultCredentialsInfo{
					SecretsManagerProvider: openapi.SecretsManagerProviderType(r.CredentialsInfo.SecretsManagerProvider),
					SecretID:               r.CredentialsInfo.SecretID,
					SecretKeys:             r.CredentialsInfo.SecretKeys,
				},
				CompletedAt: r.CompletedAt,
			})
		}
		status = &openapi.DBRoleJobStatus{
			Phase:   o.Status.Phase,
			Message: o.Status.Message,
			Result:  result,
		}
	}

	return &openapi.DBRoleJob{
		OrgID:       o.OrgID,
		ID:          o.ID,
		Status:      status,
		CreatedAt:   o.CreatedAt,
		CompletedAt: o.CompletedAt,
		Spec:        spec,
	}
}
