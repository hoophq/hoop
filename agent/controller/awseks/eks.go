package awseks

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

const (
	clusterIDHeader    = "x-k8s-aws-id"
	tokenPrefix        = "k8s-aws-v1."
	tokenExpirySeconds = 60 // Token presign expiry (EKS accepts up to 15 min)

	// emptyPayloadHash is the SHA-256 hash of an empty string.
	// AWS SigV4 requires the request body to be hashed as part of the signature.
	// Since GetCallerIdentity is a GET request with no body, we use this
	// precomputed constant: sha256("") = e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
	emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

func CreateEKSToken(clusterName, region, roleArn, roleSessionName string) (string, error) {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRetryMaxAttempts(1),
		config.WithRegion(region),
	)
	if err != nil {
		return "", fmt.Errorf("unable to load SDK config: %w", err)
	}
	// If roleArn is provided, assume that role first
	if roleArn != "" {
		stsClient := sts.NewFromConfig(cfg)
		creds := stscreds.NewAssumeRoleProvider(stsClient, roleArn, func(o *stscreds.AssumeRoleOptions) {
			if roleSessionName != "" {
				o.RoleSessionName = roleSessionName // e.g., "hoopdev.eks.groups:developers"
			}
		})
		cfg.Credentials = aws.NewCredentialsCache(creds)
	}

	return generatePresignedURL(ctx, cfg, clusterName)
}

func generatePresignedURL(ctx context.Context, cfg aws.Config, clusterName string) (string, error) {
	// Determine STS endpoint (regional)
	// IMPORTANT: Must include trailing slash before query params
	stsEndpoint := fmt.Sprintf("https://sts.%s.amazonaws.com/", cfg.Region)

	// Build the request URL with GetCallerIdentity action
	reqURL, err := url.Parse(stsEndpoint)
	if err != nil {
		return "", fmt.Errorf("failed to parse STS endpoint: %w", err)
	}

	// Add query parameters for GetCallerIdentity
	query := reqURL.Query()
	query.Set("Action", "GetCallerIdentity")
	query.Set("Version", "2011-06-15")
	query.Set("X-Amz-Expires", fmt.Sprintf("%d", tokenExpirySeconds))
	reqURL.RawQuery = query.Encode()

	// Create the HTTP request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return "", fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set the cluster ID header - THIS IS CRITICAL
	// This header must be signed as part of the request
	req.Header.Set(clusterIDHeader, clusterName)
	req.Header.Set("Host", reqURL.Host)

	// Get credentials from the config
	creds, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve AWS credentials: %w", err)
	}

	// Create a V4 signer
	signer := v4.NewSigner()

	// Presign the request - this creates a URL with signature in query params
	signedURL, signedHeaders, err := signer.PresignHTTP(
		ctx,
		creds,
		req,
		emptyPayloadHash,
		"sts",
		cfg.Region,
		time.Now().UTC(),
	)
	if err != nil {
		return "", fmt.Errorf("failed to presign request: %w", err)
	}

	// Verify that x-k8s-aws-id is in the signed headers
	_ = signedHeaders // Contains the headers that were signed

	// Encode the presigned URL as an EKS token
	// The token format is: k8s-aws-v1.<base64url-encoded-presigned-url>
	token := tokenPrefix + base64.RawURLEncoding.EncodeToString([]byte(signedURL))
	return token, nil
}
