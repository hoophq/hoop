package secretsmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hoophq/hoop/common/envloader"
	"github.com/hoophq/hoop/common/httpclient"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
)

const defaultKV2Path string = "secret/data/"

type vaultProvider struct {
	config     *vaultConfig
	cache      memory.Store
	kvType     secretProviderType
	httpClient httpclient.HttpClient
}

type vaultConfig struct {
	serverAddr      string
	tlsCA           string
	vaultToken      string
	appRoleID       string
	appRoleSecretID string
}

// https://developer.hashicorp.com/vault/api-docs/auth/approle#create-update-approle
type AppRoleLoginResponse struct {
	RequestID string         `json:"request_id"`
	Auth      map[string]any `json:"auth"`
}

type KeyValData struct {
	Data     map[string]string `json:"data"`
	Metadata map[string]any    `json:"metadata"`
}

type KeyValMeta struct {
	RequestID     string `json:"request_id"`
	LeaseID       string `json:"lease_id"`
	LeaseDuration int64  `json:"lease_duration"`
	Renewable     bool   `json:"renewable"`
	MountType     string `json:"mount_type"`
}

type KeyValV1 struct {
	KeyValMeta `json:",inline"`
	Data       map[string]string `json:"data"`
}

type KeyValV2 struct {
	KeyValMeta `json:",inline"`
	Data       KeyValData `json:"data"`
}

type KVGetter interface {
	GetData() map[string]string
}

func NewVaultProvider() (*vaultProvider, error) {
	return newVaultKeyValProvider(secretProviderVaultKv2Type, nil)
}

func newVaultKeyValProvider(kvType secretProviderType, httpClient httpclient.HttpClient) (*vaultProvider, error) {
	config, err := loadDefaultVaultConfig()
	if err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = httpclient.NewHttpClient(config.tlsCA)
	}
	return &vaultProvider{config: config, cache: memory.New(), kvType: kvType, httpClient: httpClient}, nil
}

func loadAppRoleCredentials() (string, string, error) {
	appRoleID := os.Getenv("VAULT_APP_ROLE_ID")
	appRoleSecretID := os.Getenv("VAULT_APP_ROLE_SECRET_ID")
	if appRoleID != "" && appRoleSecretID == "" {
		return "", "", fmt.Errorf("VAULT_APP_ROLE_ID env is set but VAULT_APP_ROLE_SECRET_ID env is empty")
	}
	if appRoleSecretID != "" && appRoleID == "" {
		return "", "", fmt.Errorf("VAULT_APP_ROLE_SECRET_ID env is set but VAULT_APP_ROLE_ID env is empty")
	}
	if appRoleID != "" && appRoleSecretID != "" {
		return appRoleID, appRoleSecretID, nil
	}
	return "", "", nil
}

func loadDefaultVaultConfig() (*vaultConfig, error) {
	tlsCA, err := envloader.GetEnv("VAULT_CACERT")
	if err != nil {
		return nil, fmt.Errorf("unable to load VAULT_CACERT env, reason=%v", err)
	}
	srvAddr := os.Getenv("VAULT_ADDR")
	token, err := envloader.GetEnv("VAULT_TOKEN")
	if err != nil {
		return nil, fmt.Errorf("unable to load VAULT_TOKEN env, reason=%v", err)
	}
	appRoleID, appRoleSecretID, err := loadAppRoleCredentials()
	if err != nil {
		return nil, err
	}
	config := &vaultConfig{serverAddr: srvAddr, tlsCA: tlsCA}
	if appRoleID != "" {
		config.appRoleID = appRoleID
		config.appRoleSecretID = appRoleSecretID
		return config, nil
	}

	if token == "" || srvAddr == "" {
		return nil, fmt.Errorf("VAULT_TOKEN and/or VAULT_ADDR env not set")
	}
	config.vaultToken = token
	return config, nil
}

func (p *vaultProvider) SetValue(secretID string, secretsPayload map[string]string) error {
	apiURL := urlPathForProvider("", p.config.serverAddr, secretID)
	log.With("secretid", secretID).Debugf("creating or updating secret at %v", apiURL)
	ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*5)
	defer cancelFn()

	secretsJsonData, err := json.Marshal(map[string]any{"data": secretsPayload})
	if err != nil {
		return fmt.Errorf("failed encoding secrets payload, reason=%v", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(secretsJsonData))
	if err != nil {
		return fmt.Errorf("failed creating http request, err=%v", err)
	}
	vaultToken, err := p.GetVaultToken()
	if err != nil {
		return err
	}
	req.Header.Set("X-Vault-Token", vaultToken)
	req.Header.Set("X-Vault-Request", "true")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := decodeVaultHttpErrorResponseBody(resp); err != nil {
		return err
	}
	log.With("secretid", secretID).Debugf("secret saved with success, status=%v", resp.StatusCode)
	return nil
}

func (p *vaultProvider) GetKey(secretID, secretKey string) (string, error) {
	if obj := p.cache.Get(secretID); obj != nil {
		if keyVal, ok := obj.(map[string]string); ok {
			if v, ok := keyVal[secretKey]; ok {
				return fmt.Sprintf("%v", v), nil
			}
			return "", fmt.Errorf("secret key not found. secret=%v, key=%v", secretID, secretKey)
		}
	}
	kv, err := p.keyValGetRequest(secretID)
	if err != nil {
		return "", fmt.Errorf("(%v) %v", secretID, err)
	}
	log.Infof("vault decoded response: %s", kv)
	if data := kv.GetData(); data != nil {
		if v, ok := data[secretKey]; ok {
			p.cache.Set(secretID, data)
			return v, nil
		}
	}
	return "", fmt.Errorf("secret id %s found, but key %s was not", secretID, secretKey)
}

func (p *vaultProvider) GetVaultToken() (string, error) {
	if p.config.appRoleID != "" {
		ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*5)
		defer cancelFn()

		appRoleLoginURI := strings.TrimSuffix(p.config.serverAddr, "/") + "/v1/auth/approle/login"
		payload, err := json.Marshal(map[string]string{
			"role_id":   p.config.appRoleID,
			"secret_id": p.config.appRoleSecretID,
		})
		if err != nil {
			return "", fmt.Errorf("unable to encode /auth/approle/login payload, reason=%v", err)
		}
		req, err := http.NewRequestWithContext(ctx, "POST", appRoleLoginURI, bytes.NewBuffer(payload))
		if err != nil {
			return "", fmt.Errorf("failed creating http request to obtain vault token, err=%v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Vault-Request", "true")
		resp, err := p.httpClient.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if err := decodeVaultHttpErrorResponseBody(resp); err != nil {
			return "", err
		}
		var login AppRoleLoginResponse
		if err := json.NewDecoder(resp.Body).Decode(&login); err != nil {
			return "", fmt.Errorf("failed decoding app role login response, status=%v, length=%v, reason=%v",
				resp.StatusCode, resp.ContentLength, err)
		}
		log.Infof("app role login decoded with success: %s", login.String())
		clientToken := login.getClientToken()
		return clientToken, nil
	}
	return p.config.vaultToken, nil
}

// keyValGetRequest performs a get request to Vault Key Value store.
// It decodes the response based on which type of key value provider is used (v1 or v2)
// This function is analog to the cli request below
//
// vault kv get -mount=secret -output-curl-string <key> | sh | jq .
func (p *vaultProvider) keyValGetRequest(secretID string) (KVGetter, error) {
	apiURL := urlPathForProvider(p.kvType, p.config.serverAddr, secretID)
	log.Infof("fetching key value secret at %v", apiURL)
	ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*5)
	defer cancelFn()

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed creating http request, err=%v", err)
	}
	vaultToken, err := p.GetVaultToken()
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Vault-Token", vaultToken)
	req.Header.Set("X-Vault-Request", "true")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := decodeVaultHttpErrorResponseBody(resp); err != nil {
		return nil, err
	}
	switch p.kvType {
	case secretProviderVaultKv1Type:
		obj := KeyValV1{Data: map[string]string{}}
		if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
			return nil, fmt.Errorf("failed decoding response, status=%v, length=%v, reason=%v",
				resp.StatusCode, resp.ContentLength, err)
		}
		return &obj, nil
	case secretProviderVaultKv2Type:
		obj := KeyValV2{Data: KeyValData{Data: map[string]string{}}}
		if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
			return nil, fmt.Errorf("failed decoding response, status=%v, length=%v, reason=%v",
				resp.StatusCode, resp.ContentLength, err)
		}
		return &obj, nil
	}
	return nil, fmt.Errorf("unknown secret provider %v", p.kvType)
}

func (k *KeyValV1) GetData() map[string]string { return k.Data }
func (k *KeyValV1) String() string {
	return fmt.Sprintf("request_id=%v, lease_id=%v, lease_duration=%v, mount_type=%v, keys=%v",
		k.RequestID, k.LeaseID, k.LeaseDuration, k.MountType, getDataKeys(k.Data))
}

func (k *KeyValV2) GetData() map[string]string { return k.Data.Data }
func (k *KeyValV2) String() string {
	m := k.Data.Metadata
	return fmt.Sprintf("request_id=%v, lease_id=%v, lease_duration=%v, mount_type=%v, created_at=%v, destroyed=%v, version=%v, keys=%v",
		k.RequestID, k.LeaseID, k.LeaseDuration, k.MountType,
		m["created_at"], m["destroyed"], m["version"], getDataKeys(k.Data.Data))
}

func (r *AppRoleLoginResponse) String() string {
	auth := map[string]any{}
	if len(r.Auth) > 0 {
		auth = r.Auth
	}
	clientToken := fmt.Sprintf("%v", auth["client_token"])
	return fmt.Sprintf("requestid=%v, token_length=%v, token_policies=%v, orphan=%v, lease_duration=%v, renewable=%v",
		r.RequestID, len(clientToken), auth["token_policies"], auth["orphan"],
		auth["lease_duration"], auth["renewable"])
}

func (r *AppRoleLoginResponse) getClientToken() string {
	if len(r.Auth) > 0 {
		return fmt.Sprintf("%v", r.Auth["client_token"])
	}
	return ""
}

// return the status code and the decoded error in case of a bad status http code
// https://developer.hashicorp.com/vault/api-docs#error-response
func decodeVaultHttpErrorResponseBody(resp *http.Response) error {
	if resp.StatusCode >= 400 && resp.StatusCode <= 499 {
		var obj struct {
			Errs []string `json:"errors"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
			return fmt.Errorf("failed decoding error response, status=%v, length=%v, reason=%v",
				resp.StatusCode, resp.ContentLength, err)
		}
		return fmt.Errorf("vault error response, status=%v, errs=%v",
			resp.StatusCode, strings.Join(obj.Errs, "; "))
	}
	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed performing request, status=%v, body=%v",
			resp.StatusCode, string(respBody))
	}
	return nil
}

func urlPathForProvider(kvProvider secretProviderType, serverAddr, secretID string) (apiURL string) {
	apiURL = strings.TrimSuffix(serverAddr, "/") + "/v1/"
	if kvProvider == secretProviderVaultKv1Type {
		return apiURL + strings.TrimPrefix(secretID, "/")
	}

	// it's a path based, add to the suffix of the url
	if strings.Contains(secretID, "/") {
		return apiURL + strings.TrimPrefix(secretID, "/")
	}
	return apiURL + defaultKV2Path + secretID
}

func getDataKeys(m map[string]string) (keys []string) {
	for key := range m {
		keys = append(keys, key)
	}
	return
}
