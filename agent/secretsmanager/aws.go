package secretsmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/smithy-go/logging"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
)

type awsProvider struct {
	client *secretsmanager.Client
	cache  memory.Store
}

func newAwsProvider() (*awsProvider, error) {
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		return nil, err
	}
	svc := secretsmanager.NewFromConfig(cfg, func(o *secretsmanager.Options) {
		if log.IsDebugLevel {
			o.ClientLogMode = aws.LogResponse
		}
		// TODO: add zap as logger
		o.Logger = logging.NewStandardLogger(os.Stdout)
	})
	return &awsProvider{svc, memory.New()}, nil
}

func (p *awsProvider) GetKey(secretID, secretKey string) (string, error) {
	if obj := p.cache.Get(secretID); obj != nil {
		if keyVal, ok := obj.(map[string]any); ok {
			if v, ok := keyVal[secretKey]; ok {
				return fmt.Sprintf("%v", v), nil
			}
			return "", fmt.Errorf("secret key not found. secret=%v, key=%v", secretID, secretKey)
		}
	}

	input := &secretsmanager.GetSecretValueInput{
		SecretId: &secretID,
	}
	result, err := p.client.GetSecretValue(context.Background(), input)
	if err != nil {
		return "", fmt.Errorf("(%v) %v", secretID, err)
	}
	var keyValSecret map[string]any
	if err := json.Unmarshal([]byte(*result.SecretString), &keyValSecret); err != nil {
		return "", fmt.Errorf("failed deserializing secret key/val, err=%v", err)
	}
	if v, ok := keyValSecret[secretKey]; ok {
		p.cache.Set(secretID, keyValSecret)
		return fmt.Sprintf("%v", v), nil
	}
	return "", fmt.Errorf("secret id %s found, but key %s was not", secretID, secretKey)
}
