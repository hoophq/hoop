package apikey

import (
	"errors"
	"os"
)

var ErrAPIKeyNotConfigured = errors.New("API key authentication is not configured. Please check the documentation to learn how to enable it.")

func ValidateOrgApiKey(orgID string, apiKey string) (bool, error) {
	// Load the API key from an environment variable
	envApiKey := os.Getenv("ORG_API_KEY")

	// Check if the API key is configured
	if envApiKey == "" {
		return false, ErrAPIKeyNotConfigured
	}

	// Compare the provided API key with the one from the environment variable
	return apiKey == envApiKey, nil
}
