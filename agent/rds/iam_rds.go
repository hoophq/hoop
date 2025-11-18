package rds

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	rdsutils "github.com/aws/aws-sdk-go-v2/feature/rds/auth"
)

func BuildRdsEnvAuth(env map[string]any) (map[string]any, error) {
	userEnv, _ := env["envvar:USER"].(string)
	if userEnv == "" {
		return nil, fmt.Errorf("_aws_iam_rds: not found in envvar")
	}

	decodedEnv, err := base64.StdEncoding.DecodeString(fmt.Sprintf("%v", userEnv))
	if err != nil {
		return nil, fmt.Errorf("failed to decode rds iam user env: %v", err)
	}

	values := strings.Split(string(decodedEnv), ":")
	if len(values) != 2 {
		return nil, fmt.Errorf("invalid rds iam user format env format")
	}
	user := values[1]

	encodedHost, _ := env["envvar:HOST"].(string)
	if encodedHost == "" {
		return nil, fmt.Errorf("rds_iam_auth: missing HOST value in env")
	}

	host, err := base64.StdEncoding.DecodeString(fmt.Sprintf("%v", encodedHost))
	if err != nil {
		return nil, fmt.Errorf("failed to decode rds iam host env: %v", err)
	}

	region, err := regionFromHost(string(host))
	if err != nil {
		return nil, fmt.Errorf("aws region not found in the host: %v", err)
	}

	encodedPort, _ := env["envvar:PORT"].(string)
	if encodedPort == "" {
		return nil, fmt.Errorf("rds_iam_auth: missing PORT value in env")
	}
	port, err := base64.StdEncoding.DecodeString(fmt.Sprintf("%v", encodedPort))
	if err != nil {
		return nil, fmt.Errorf("failed to decode rds iam port env: %v", err)
	}

	token, err := generateToken(string(host), region, string(port), user)
	if err != nil {
		return nil, err
	}

	env["envvar:PASS"] = base64.StdEncoding.EncodeToString([]byte(token))
	env["envvar:USER"] = base64.StdEncoding.EncodeToString([]byte(user))

	return env, nil

}

func regionFromHost(host string) (string, error) {
	parts := strings.Split(host, ".")
	// example: ["pgtestiam", "cqvjxizuxvwe", "us-west-2", "rds", "amazonaws", "com"]
	if len(parts) < 3 {
		return "", fmt.Errorf("unable to parse region from host: %s", host)
	}
	return parts[2], nil
}

func generateToken(host, region, port, user string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cfg, err := config.LoadDefaultConfig(ctx)

	if err != nil {
		return "", err
	}

	token, err := rdsutils.BuildAuthToken(
		context.Background(),
		fmt.Sprintf("%s:%s", host, port),
		region,
		user,
		cfg.Credentials,
	)

	return token, err
}
