package rds

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	rdsutils "github.com/aws/aws-sdk-go-v2/feature/rds/auth"
)

func BuildRdsEnvAuth(env map[string]any) (map[string]any, error) {
	userEnv, ok := env["envvar:USER"].(string)
	if !ok {
		return nil, fmt.Errorf("aws rds-user not found in env")
	}

	decodedEnv, err := base64.StdEncoding.DecodeString(fmt.Sprintf("%v", userEnv))
	if err != nil {
		return nil, fmt.Errorf("failed to decode rds-user env: %v", err)
	}

	values := strings.Split(string(decodedEnv), ":")
	if len(values) != 2 {
		return nil, fmt.Errorf("invalid rds-user env format")
	}
	user := values[1]

	region, ok := "us-west-2", true
	if !ok {
		return nil, fmt.Errorf("aws region not found in env")
	}

	encodedHost, ok := env["envvar:HOST"].(string)
	host, err := base64.StdEncoding.DecodeString(fmt.Sprintf("%v", encodedHost))

	if !ok {
		return nil, fmt.Errorf("aws rds host not found in env")
	}

	encodedPort, ok := env["envvar:PORT"].(string)
	port, err := base64.StdEncoding.DecodeString(fmt.Sprintf("%v", encodedPort))
	if !ok {
		return nil, fmt.Errorf("aws rds port not found in env")
	}

	token, err := generateToken(string(host), region, string(port), user)
	if err != nil {
		return nil, err
	}

	env["envvar:PASS"] = base64.StdEncoding.EncodeToString([]byte(token))
	env["envvar:USER"] = base64.StdEncoding.EncodeToString([]byte(user))

	return env, nil

}

func generateToken(host, region, port, user string) (string, error) {

	cfg, err := config.LoadDefaultConfig(context.Background())

	if err != nil {
		fmt.Printf("unable to load SDK config, %v", err)
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
