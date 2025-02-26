package awsintegration

import (
	"context"
	"fmt"
	"libhoop/log"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/gin-gonic/gin"
)

func GetCallerIdentity(c *gin.Context) {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	stsClient := sts.NewFromConfig(cfg)
	// Get caller identity
	identity, err := stsClient.GetCallerIdentity(context.TODO(), &sts.GetCallerIdentityInput{})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"account":  identity.Account,
		"arn":      identity.Arn,
		"metadata": identity.ResultMetadata,
		"user_id":  identity.UserId,
	})
}

func VerifyPermissions(c *gin.Context) {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	iamClient := iam.NewFromConfig(cfg)

	stsClient := sts.NewFromConfig(cfg)
	// Get caller identity
	identity, err := stsClient.GetCallerIdentity(context.TODO(), &sts.GetCallerIdentityInput{})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	resp, err := iamClient.SimulatePrincipalPolicy(context.Background(), &iam.SimulatePrincipalPolicyInput{
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
			"action_name":               r.EvalActionName,
			"decision":                  r.EvalDecision,
			"decision_details":          r.EvalDecisionDetails,
			"resource_name":             r.EvalResourceName,
			"matched_statements":        statements,
			"missing_context_values":    r.MissingContextValues,
			"resource_specific_results": r.ResourceSpecificResults,
		})
		// fmt.Printf("Action: %s, Decision: %s\n", *result.EvalActionName, result.EvalDecision)
	}
	c.JSON(http.StatusOK, map[string]any{
		"status":             status,
		"evaluation_details": evaluation,
	})

}

func ListRDSInstances(c *gin.Context) {
	// Load AWS config for management account
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	stsClient := sts.NewFromConfig(cfg)
	// Get caller identity
	identity, err := stsClient.GetCallerIdentity(context.TODO(), &sts.GetCallerIdentityInput{})
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
					"name":               inst.DBName,
					"arn":                inst.DBInstanceArn,
					"endpoint":           inst.Endpoint,
					"engine":             inst.Engine,
					"master_user":        inst.MasterUsername,
					"master_user_secret": inst.MasterUserSecret,
					"iam_auth_enabled":   inst.IAMDatabaseAuthenticationEnabled,
					"status":             inst.DBInstanceStatus,
				})
			}

			// instances = append(instances, []map[string]any{
			// 	""
			// })
			// accounts = append(accounts, map[string]any{
			// 	"arn":            acct.Arn,
			// 	"email":          acct.Email,
			// 	"id":             acct.Id,
			// 	"joined_methods": acct.JoinedMethod.Values(),
			// 	"name":           acct.Name,
			// 	"status":         acct.Status,
			// })

			// log.Infof("ACCOUNT: %#v", acct)
			// accounts = append(accounts, AWSAccount{
			// 	ID:   *acct.Id,
			// 	Name: *acct.Name,
			// })
		}
	}

	c.JSON(http.StatusOK, instances)
}

func listRDSInstances(ctx context.Context, cfg aws.Config, accountID string, isAccountOwner bool) ([]types.DBInstance, error) {
	roleArn := fmt.Sprintf("arn:aws:iam::%s:role/OrganizationAccountAccessRole", accountID)

	// Create RDS client
	rdsClient := rds.NewFromConfig(cfg)
	log.Infof("is account owner = %v", isAccountOwner)
	if !isAccountOwner {
		// Assume Role in target account
		stsClient := sts.NewFromConfig(cfg)
		creds := stscreds.NewAssumeRoleProvider(stsClient, roleArn)

		// Create AWS Config with assumed role credentials
		accountCfg, err := config.LoadDefaultConfig(ctx,
			config.WithCredentialsProvider(creds),
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

func changeRDSMasterPassword(cfg aws.Config, dbInstanceIdentifier, newPassword string) error {
	ctx := context.TODO()
	// Create an RDS client
	rdsClient := rds.NewFromConfig(cfg)

	// Modify RDS instance password
	_, err := rdsClient.ModifyDBInstance(ctx, &rds.ModifyDBInstanceInput{
		DBInstanceIdentifier: aws.String(dbInstanceIdentifier),
		MasterUserPassword:   aws.String(newPassword),
		ApplyImmediately:     aws.Bool(true), // Apply change immediately
	})
	if err != nil {
		return fmt.Errorf("failed to modify RDS instance: %v", err)
	}
	return nil
}
