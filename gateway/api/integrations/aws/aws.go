package awsintegration

import (
	"context"
	"fmt"
	"libhoop/log"
	"libhoop/memory"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/openapi"
)

// TODO(san): temporary, we should store it in the database
var staticKeyStore = memory.New()

// IAMUpdateAccessKey
//
//	@Summary		Update IAM Access Key
//	@Description	Update IAM Access Key
//	@Tags			AWS
//	@Accept			json
//	@Produce		json
//	@Param			request	body	openapi.IAMAccessKeyRequest	true	"The request body resource"
//	@Success		204
//	@Failure		400	{object}	openapi.HTTPError
//	@Router			/integrations/aws/iam/accesskeys [put]
func IAMUpdateAccessKey(c *gin.Context) {
	var req openapi.IAMAccessKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Errorf("failed parsing request payload, err=%v", err)
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	staticKeyStore.Set("1", &req)
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
	staticKeyStore.Del("1")
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
	cfg, identity, err := loadAWSConfig()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, map[string]any{
		"account_id": identity.Account,
		"arn":        identity.Arn,
		"user_id":    identity.UserId,
		"region":     cfg.Region,
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
	ctx := context.Background()
	cfg, identity, err := loadAWSConfig()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	iamClient := iam.NewFromConfig(cfg)
	resp, err := iamClient.SimulatePrincipalPolicy(ctx, &iam.SimulatePrincipalPolicyInput{
		PolicySourceArn: identity.Arn,
		ActionNames:     []string{"organizations:ListAccounts", "rds:ModifyDBInstance", "rds:DescribeDBInstances", "sts:AssumeRole"},
		ResourceArns:    []string{"*"},
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	status := "allowed"
	var evaluation []map[string]any
	for _, r := range resp.EvaluationResults {
		if r.EvalDecision != "allowed" {
			status = "denied"
		}
		statements := []map[string]any{}
		for _, st := range r.MatchedStatements {
			statements = append(statements, map[string]any{
				"source_policy_id":   st.SourcePolicyId,
				"source_policy_type": st.SourcePolicyType,
			})
		}
		evaluation = append(evaluation, map[string]any{
			"action_name":        r.EvalActionName,
			"decision":           r.EvalDecision,
			"decision_details":   r.EvalDecisionDetails,
			"resource_name":      r.EvalResourceName,
			"matched_statements": statements,
		})
	}
	c.JSON(http.StatusOK, map[string]any{
		"status": status,
		"identity": map[string]any{
			"account_id": identity.Account,
			"arn":        identity.Arn,
			"user_id":    identity.UserId,
			"region":     cfg.Region,
		},
		"evaluation_details": evaluation,
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
	cfg, _, err := loadAWSConfig()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	orgClient := organizations.NewFromConfig(cfg)
	var accounts []map[string]any
	paginator := organizations.NewListAccountsPaginator(orgClient, &organizations.ListAccountsInput{})

	for paginator.HasMorePages() {
		ctx := context.Background()
		page, err := paginator.NextPage(ctx)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Errorf("failed to get AWS accounts: %v", err).Error()})
			return
		}

		for _, acct := range page.Accounts {
			accounts = append(accounts, map[string]any{
				"account_id":    acct.Id,
				"name":          acct.Name,
				"status":        acct.Status,
				"joined_method": acct.JoinedMethod,
				"email":         acct.Email,
			})
		}
	}
	c.JSON(http.StatusOK, map[string]any{
		"items": accounts,
	})
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
func ListRDSInstances(c *gin.Context) {
	// Load AWS config for management account
	// 1. filter instances based on selected accounts
	ctx := context.Background()
	cfg, identity, err := loadAWSConfig()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	orgClient := organizations.NewFromConfig(cfg)
	var instances []map[string]any
	paginator := organizations.NewListAccountsPaginator(orgClient, &organizations.ListAccountsInput{})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Errorf("failed to get AWS accounts: %v", err).Error()})
			return
		}

		for _, acct := range page.Accounts {
			isAccountOwner := *acct.Id == *identity.Account
			items, err := listRDSInstances(ctx, cfg, *acct.Id, isAccountOwner)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
				return
			}

			for _, inst := range items {

				instances = append(instances, map[string]any{
					"account_id": acct.Id,
					"name":       inst.DBName,
					"az":         inst.AvailabilityZone,
					"vpc_id":     inst.DBSubnetGroup.VpcId,
					"arn":        inst.DBInstanceArn,
					"engine":     inst.Engine,
					"status":     inst.DBInstanceStatus,
				})
			}
		}
	}

	c.JSON(http.StatusOK, map[string]any{
		"items": instances,
	})
}

// UpdateDBInstanceRoles
//
//	@Summary		Update Database Instance Roles
//	@Description	It update user roles in the target database
//	@Tags			AWS
//	@Produce		json
//	@Param			request	body		openapi.UpdateDBInstanceRolesRequest	true	"The request body resource"
//	@Success		200		{object}	openapi.UpdateDBInstanceRolesResponse
//	@Failure		400		{object}	openapi.HTTPError
//	@Router			/integrations/aws/rds/dbinstances/:dbArn/roles [post]
func UpdateDBInstanceRoles(c *gin.Context) {
	// 1. change the master role username password
	//   - check if accept rds iam auth
	//   - check if it's managed in the aws secrets manager
	// 2. send a stream with the agent to send the payload to provision the users
	// 3. return with a report of the roles that were provisioned
	// 4. create the connection with the credentials
	c.JSON(http.StatusOK, map[string]any{
		"message": "not implemented yet",
	})
}

func listRDSInstances(ctx context.Context, cfg aws.Config, accountID string, isAccountOwner bool) ([]types.DBInstance, error) {
	roleArn := fmt.Sprintf("arn:aws:iam::%s:role/OrganizationAccountAccessRole", accountID)

	// Create RDS client
	rdsClient := rds.NewFromConfig(cfg)
	log.Infof("is account owner=%v, region=%v", isAccountOwner, cfg.Region)
	if !isAccountOwner {
		// Assume Role in target account
		stsClient := sts.NewFromConfig(cfg)
		creds := stscreds.NewAssumeRoleProvider(stsClient, roleArn)

		// Create AWS Config with assumed role credentials
		accountCfg, err := config.LoadDefaultConfig(ctx,
			config.WithCredentialsProvider(creds),
			config.WithRegion(cfg.Region),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to assume role for account %s: %v", accountID, err)
		}
		rdsClient = rds.NewFromConfig(accountCfg)
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

func loadAWSConfig() (cfg aws.Config, identity *sts.GetCallerIdentityOutput, err error) {
	hasStaticKey := staticKeyStore.Has("1")
	if obj := staticKeyStore.Get("1"); obj != nil {
		staticAccessKey, _ := obj.(*openapi.IAMAccessKeyRequest)
		log.Infof("using aws static credentials with region=%v", staticAccessKey.Region)
		staticCfg := aws.NewConfig()
		staticCfg.Credentials = credentials.NewStaticCredentialsProvider(
			staticAccessKey.AccessKeyID,
			staticAccessKey.SecretAccessKey,
			staticAccessKey.SessionToken)
		staticCfg.Region = staticAccessKey.Region
		cfg = staticCfg.Copy()
	}
	ctx := context.Background()
	if !hasStaticKey {
		cfg, err = config.LoadDefaultConfig(ctx, config.WithRetryMaxAttempts(1))
		if err != nil {
			return
		}
		// the user is obligated to to pass the region manually
		// to avoid provisioning the resources in the wrong region
		cfg.Region = ""
		cfg = cfg.Copy()
	}

	stsClient := sts.NewFromConfig(cfg)
	identity, err = stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	return
}

// func changeRDSMasterPassword(cfg aws.Config, dbInstanceIdentifier, newPassword string) error {
// 	ctx := context.TODO()
// 	// Create an RDS client
// 	rdsClient := rds.NewFromConfig(cfg)

// 	// Modify RDS instance password
// 	_, err := rdsClient.ModifyDBInstance(ctx, &rds.ModifyDBInstanceInput{
// 		DBInstanceIdentifier: aws.String(dbInstanceIdentifier),
// 		MasterUserPassword:   aws.String(newPassword),
// 		ApplyImmediately:     aws.Bool(true), // Apply change immediately
// 	})
// 	if err != nil {
// 		return fmt.Errorf("failed to modify RDS instance: %v", err)
// 	}
// 	return nil
// }
