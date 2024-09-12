package secretsmanager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/hoophq/hoop/common/log"
	"github.com/stretchr/testify/assert"
)

type clientFunc func(req *http.Request) (*http.Response, error)

func (f clientFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func createTestServer(keyVal any, err error) clientFunc {
	return clientFunc(func(req *http.Request) (*http.Response, error) {
		if err != nil {
			content := fmt.Sprintf(`{"errors": [%q]}`, err.Error())
			return &http.Response{
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(bytes.NewBufferString(content)),
			}, nil
		}
		data, err := json.Marshal(keyVal)
		if err != nil {
			errMsg := fmt.Sprintf("unable to encode keyVal data, reason=%v", err)
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(bytes.NewBufferString(errMsg)),
			}, nil
		}

		return &http.Response{
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer(data)),
		}, nil
	})
}

func TestVaultProviderGetKey(t *testing.T) {
	os.Setenv("VAULT_TOKEN", "noop")
	os.Setenv("VAULT_ADDR", "noop")
	log.SetDefaultLoggerLevel(log.LevelWarn) // disabled info logging
	for _, tt := range []struct {
		msg            string
		kvType         secretProviderType
		err            error
		fakeHttpClient clientFunc
		secretID       string
		expectedData   map[string]string
	}{
		{
			msg:            "kv1: it should return data with a single secret from server",
			kvType:         secretProviderVaultKv1Type,
			secretID:       "kv/mysecret",
			expectedData:   map[string]string{"HOST": "127.0.0.1"},
			fakeHttpClient: createTestServer(KeyValV1{Data: map[string]string{"HOST": "127.0.0.1"}}, nil),
		},
		{
			msg:          "kv1: it should return data with multiple secrets from server",
			kvType:       secretProviderVaultKv1Type,
			secretID:     "kv/mysecret",
			expectedData: map[string]string{"HOST": "127.0.0.1", "USER": "dbuser", "PASS": "dbsecret"},
			fakeHttpClient: createTestServer(
				KeyValV1{Data: map[string]string{"HOST": "127.0.0.1", "USER": "dbuser", "PASS": "dbsecret"}}, nil),
		},
		{
			msg:          "kv2: it should return data with multiple secrets from server",
			kvType:       secretProviderVaultKv2Type,
			secretID:     "dbsecret",
			expectedData: map[string]string{"HOST": "127.0.0.1", "USER": "dbuser", "PASS": "dbsecret"},
			fakeHttpClient: createTestServer(
				KeyValV2{Data: KeyValData{Data: map[string]string{"HOST": "127.0.0.1", "USER": "dbuser", "PASS": "dbsecret"}}}, nil),
		},
		{
			msg:            "kv2: it should return error from the server",
			kvType:         secretProviderVaultKv2Type,
			secretID:       "dbsecret",
			expectedData:   map[string]string{"HOST": "127.0.0.1"},
			fakeHttpClient: createTestServer(nil, fmt.Errorf("permission denied")),
			err:            fmt.Errorf("(dbsecret) vault error response, status=400, errs=permission denied"),
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			prov, err := newVaultKeyValProvider(tt.kvType, tt.fakeHttpClient)
			if prov == nil {
				t.Fatalf("did not expected to obtain error obtaining vault provider, reason=%v", err)
			}
			for secretKey, expectedVal := range tt.expectedData {
				gotVal, err := prov.GetKey(tt.secretID, secretKey)
				if tt.err != nil {
					assert.EqualError(t, err, tt.err.Error())
					return
				}
				assert.Nil(t, err)
				assert.Equal(t, expectedVal, gotVal, "must match with keys from server")
			}
			if !prov.cache.Has(tt.secretID) && tt.err == nil {
				t.Errorf("secret key %q not found. Expect to cache secret id from server in memory", tt.secretID)
			}
		})
	}
}

func TestUrlPathForProvider(t *testing.T) {
	for _, tt := range []struct {
		msg      string
		provider secretProviderType
		wantURL  string
		secretID string
	}{
		{
			msg:      "it should return v1 path based url",
			provider: secretProviderVaultKv1Type,
			wantURL:  "http://127.0.0.1:8200/v1/kv/mysecret",
			secretID: "kv/mysecret",
		},
		{
			msg:      "it should return v2 path based url by default",
			provider: "",
			wantURL:  "http://127.0.0.1:8200/v1/secret/data/mydbsecret",
			secretID: "mydbsecret",
		},
		{
			msg:      "it should return v2 path based url",
			provider: secretProviderVaultKv2Type,
			wantURL:  "http://127.0.0.1:8200/v1/secret/data/mydbsecret",
			secretID: "mydbsecret",
		},
		{
			msg:      "it should return v2 path based url using a custom mount point",
			provider: secretProviderVaultKv2Type,
			wantURL:  "http://127.0.0.1:8200/v1/custom-mount-point/mydbsecret",
			secretID: "custom-mount-point/mydbsecret",
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			gotURL := urlPathForProvider(tt.provider, "http://127.0.0.1:8200", tt.secretID)
			assert.Equal(t, tt.wantURL, gotURL)
		})
	}
}
