package directorysync

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	cip "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	ciptypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
)

// cognitoProvider reads the groups of a Cognito user pool. A group is named
// by its group name, which is what Cognito puts in the cognito:groups claim.
type cognitoProvider struct {
	client     *cip.Client
	userPoolID string
}

func newCognitoProvider(ctx context.Context, s CognitoSettings, optFns ...func(*cip.Options)) (*cognitoProvider, error) {
	loadOpts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(s.Region)}
	if s.AccessKeyID != "" {
		loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(s.AccessKeyID, s.SecretAccessKey, "")))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed loading aws credentials: %w", err)
	}
	return &cognitoProvider{client: cip.NewFromConfig(cfg, optFns...), userPoolID: s.UserPoolID}, nil
}

func (p *cognitoProvider) ListGroups(ctx context.Context) ([]Group, error) {
	var out []Group
	pages := cip.NewListGroupsPaginator(p.client, &cip.ListGroupsInput{UserPoolId: aws.String(p.userPoolID)})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, g := range page.Groups {
			name := aws.ToString(g.GroupName)
			out = append(out, Group{ID: name, Name: name})
		}
	}
	return out, nil
}

// ListMembers returns the users of the group. A disabled user is inactive.
func (p *cognitoProvider) ListMembers(ctx context.Context, groupName string) ([]User, error) {
	var out []User
	pages := cip.NewListUsersInGroupPaginator(p.client, &cip.ListUsersInGroupInput{
		UserPoolId: aws.String(p.userPoolID),
		GroupName:  aws.String(groupName),
	})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, u := range page.Users {
			out = append(out, cognitoUser(u))
		}
	}
	return out, nil
}

func cognitoUser(u ciptypes.UserType) User {
	attrs := map[string]string{}
	for _, a := range u.Attributes {
		attrs[aws.ToString(a.Name)] = aws.ToString(a.Value)
	}
	id := attrs["sub"]
	if id == "" {
		id = aws.ToString(u.Username)
	}
	name := attrs["name"]
	if name == "" {
		name = attrs["email"]
	}
	return User{ExternalID: id, Email: attrs["email"], Name: name, Active: u.Enabled}
}
