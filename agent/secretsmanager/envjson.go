package secretsmanager

import (
	"encoding/json"
	"fmt"
	"os"
)

type envJsonProvider struct{}

func (p *envJsonProvider) GetKey(secretID, secretKey string) (string, error) {
	envJson := os.Getenv(secretID)
	if envJson == "" {
		return "", fmt.Errorf("environment variable %q not found", secretID)
	}
	var envMap map[string]string
	if err := json.Unmarshal([]byte(envJson), &envMap); err != nil {
		return "", fmt.Errorf("failed decoding environment variable %q to json, err=%v", secretID, err)
	}
	val, ok := envMap[secretKey]
	if !ok {
		return "", fmt.Errorf("secret key %q not found", secretKey)
	}
	return val, nil
}
