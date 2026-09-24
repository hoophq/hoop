package directorysync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hoophq/hoop/gateway/models"
)

// RedactedValue replaces a secret in what the API returns. Sending it back
// unchanged keeps the stored secret, so an admin can edit the other fields
// without pasting the secret again.
const RedactedValue = "********"

// GoogleSettings reads Google Workspace through the Admin SDK Directory API
// with a service account that has domain-wide delegation.
type GoogleSettings struct {
	// ServiceAccountJSON is the service account key file.
	ServiceAccountJSON string `json:"service_account_json"`
	// AdminEmail is the Workspace admin the service account acts as.
	AdminEmail string `json:"admin_email"`
	// Customer is the Workspace customer id; empty means the admin's own.
	Customer string `json:"customer,omitempty"`
}

// Auth0Settings reads Auth0 through the Management API with a machine to
// machine application granted read:users and read:roles. Roles are the
// groups.
type Auth0Settings struct {
	Domain       string `json:"domain"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// CognitoSettings reads a Cognito user pool. Without access keys the control
// plane's own AWS credentials are used (instance role, IRSA, environment).
type CognitoSettings struct {
	Region          string `json:"region"`
	UserPoolID      string `json:"user_pool_id"`
	AccessKeyID     string `json:"access_key_id,omitempty"`
	SecretAccessKey string `json:"secret_access_key,omitempty"`
}

var secretFields = map[string][]string{
	models.ProvisioningSourceGoogle:  {"service_account_json"},
	models.ProvisioningSourceAuth0:   {"client_secret"},
	models.ProvisioningSourceCognito: {"secret_access_key"},
}

// IsProvider reports whether hoop can sync from this provider.
func IsProvider(provider string) bool {
	_, ok := secretFields[provider]
	return ok
}

// ValidateSettings checks that the settings carry what the provider needs.
func ValidateSettings(provider string, raw json.RawMessage) error {
	switch provider {
	case models.ProvisioningSourceGoogle:
		var s GoogleSettings
		if err := decodeSettings(raw, &s); err != nil {
			return err
		}
		if s.ServiceAccountJSON == "" || s.AdminEmail == "" {
			return errors.New("google requires service_account_json and admin_email")
		}
	case models.ProvisioningSourceAuth0:
		var s Auth0Settings
		if err := decodeSettings(raw, &s); err != nil {
			return err
		}
		if s.Domain == "" || s.ClientID == "" || s.ClientSecret == "" {
			return errors.New("auth0 requires domain, client_id and client_secret")
		}
	case models.ProvisioningSourceCognito:
		var s CognitoSettings
		if err := decodeSettings(raw, &s); err != nil {
			return err
		}
		if s.Region == "" || s.UserPoolID == "" {
			return errors.New("cognito requires region and user_pool_id")
		}
		if (s.AccessKeyID == "") != (s.SecretAccessKey == "") {
			return errors.New("cognito requires both access_key_id and secret_access_key, or neither")
		}
	default:
		return fmt.Errorf("unknown provider %q, want google, auth0 or cognito", provider)
	}
	return nil
}

// RedactSettings replaces the provider's secrets for the API response.
func RedactSettings(provider string, raw json.RawMessage) json.RawMessage {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return json.RawMessage(`{}`)
	}
	for _, k := range secretFields[provider] {
		if v, ok := m[k].(string); ok && v != "" {
			m[k] = RedactedValue
		}
	}
	out, _ := json.Marshal(m)
	return out
}

// MergeSettings puts the stored secrets back where the request sent the
// redacted placeholder. A different provider keeps nothing from before.
func MergeSettings(provider string, incoming json.RawMessage, stored *models.DirectorySyncConfig) (json.RawMessage, error) {
	var in map[string]any
	if err := json.Unmarshal(incoming, &in); err != nil {
		return nil, fmt.Errorf("invalid settings: %w", err)
	}
	var old map[string]any
	if stored != nil && stored.Provider == provider {
		_ = json.Unmarshal(stored.Settings, &old)
	}
	for _, k := range secretFields[provider] {
		if v, ok := in[k].(string); ok && v == RedactedValue {
			prev, _ := old[k].(string)
			if prev == "" {
				return nil, fmt.Errorf("%s is required", k)
			}
			in[k] = prev
		}
	}
	return json.Marshal(in)
}

// NewProvider builds the reader for a stored sync.
func NewProvider(ctx context.Context, provider string, raw json.RawMessage) (Provider, error) {
	switch provider {
	case models.ProvisioningSourceGoogle:
		var s GoogleSettings
		if err := decodeSettings(raw, &s); err != nil {
			return nil, err
		}
		return newGoogleProvider(ctx, s)
	case models.ProvisioningSourceAuth0:
		var s Auth0Settings
		if err := decodeSettings(raw, &s); err != nil {
			return nil, err
		}
		return newAuth0Provider(s), nil
	case models.ProvisioningSourceCognito:
		var s CognitoSettings
		if err := decodeSettings(raw, &s); err != nil {
			return nil, err
		}
		return newCognitoProvider(ctx, s)
	}
	return nil, fmt.Errorf("unknown provider %q", provider)
}

