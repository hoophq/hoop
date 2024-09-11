package secretsmanager

import (
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

const defaultKV2Path string = "/secret/data/"

type vaultProvider struct {
	config *vaultConfig
	cache  memory.Store
	kvType secretProviderType
}

type vaultConfig struct {
	serverAddr string
	tlsCA      string
	token      string
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

func newVaultKeyValProvider(kvType secretProviderType) (*vaultProvider, error) {
	config, err := loadDefaultVaultConfig()
	if err != nil {
		return nil, err
	}
	return &vaultProvider{config: config, cache: memory.New(), kvType: kvType}, nil
}

func loadDefaultVaultConfig() (*vaultConfig, error) {
	tlsCA, err := envloader.GetEnv("VAULT_CACERT")
	if err != nil {
		return nil, fmt.Errorf("unable to load VAULT_CACERT env, reason=%v", err)
	}
	srvAddr := os.Getenv("VAULT_ADDR")
	if srvAddr == "" {
		srvAddr = "http://127.0.0.1:8200/"
	}
	token, err := envloader.GetEnv("VAULT_TOKEN")
	if err != nil {
		return nil, fmt.Errorf("unable to load VAULT_TOKEN env, reason=%v", err)
	}
	return &vaultConfig{serverAddr: srvAddr, tlsCA: tlsCA, token: token}, nil
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
	kv, err := keyValGetRequest(p.config, p.kvType, secretID)
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

// keyValGetRequest performs a get request to Vault Key Value store.
// It decodes the response based on which type of key value provider is used (v1 or v2)
// This function is analog to the cli request below
//
// vault kv get -mount=secret -output-curl-string <key> | sh | jq .
func keyValGetRequest(conf *vaultConfig, kvProvider secretProviderType, secretID string) (KVGetter, error) {
	apiURL := urlPathForProvider(kvProvider, conf.serverAddr, secretID)
	log.Infof("fetching key value secret at %v", apiURL)
	ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*5)
	defer cancelFn()
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed creating http request, err=%v", err)
	}
	req.Header.Set("X-Vault-Token", conf.token)
	req.Header.Set("X-Vault-Request", "true")
	resp, err := httpclient.NewHttpClient(conf.tlsCA).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// https://developer.hashicorp.com/vault/api-docs#error-response
	if resp.StatusCode >= 400 && resp.StatusCode <= 499 {
		var obj struct {
			Errs []string `json:"errors"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
			return nil, fmt.Errorf("failed decoding error response, status=%v, length=%v, reason=%v",
				resp.StatusCode, resp.ContentLength, err)
		}
		return nil, fmt.Errorf("vault error response, status=%v, errs=%v",
			resp.StatusCode, strings.Join(obj.Errs, "; "))
	}
	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed performing request, status=%v, body=%v",
			resp.StatusCode, string(respBody))
	}
	switch kvProvider {
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
	return nil, fmt.Errorf("unknown secret provider %v", kvProvider)
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
