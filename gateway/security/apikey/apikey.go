package apikey

import (
	"os"
)

func ValidateOrgApiKey(orgID string, apiKey string) (bool, error) {
	// Load the API key from an environment variable
	envApiKey := os.Getenv("ORG_API_KEY")

	// Compare the provided API key with the one from the environment variable
	return apiKey == envApiKey, nil
}
