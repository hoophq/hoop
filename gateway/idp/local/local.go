package localprovider

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	idptypes "github.com/hoophq/hoop/gateway/idp/types"
	"github.com/hoophq/hoop/gateway/models"
)

var ErrNotImplemented = fmt.Errorf("saml: user info endpoint not implemented for SAML2 provider")

var (
	keyStore    = memory.New()
	keyStoreKey = "localprovider"
)

type provider struct {
	tokenSigningKey ed25519.PrivateKey
}

// GetInstance retrieves the singleton instance of the local provider.
func GetInstance() (*provider, error) {
	if obj := keyStore.Get(keyStoreKey); obj != nil {
		data, ok := obj.(*provider)
		if !ok {
			return nil, fmt.Errorf("internal error, failed to cast key store to provider, got=%T", obj)
		}
		return data, nil
	}

	serverConfig, err := models.GetServerConfig()
	if err != nil && err != models.ErrNotFound {
		return nil, fmt.Errorf("failed to obtain server config shared signing key: %v", err)
	}
	var tokenSigningKey ed25519.PrivateKey
	if serverConfig == nil || serverConfig.SharedSigningKey == "" {
		_, tokenSigningKey, err = keys.GenerateEd25519KeyPair()
		if err != nil {
			return nil, fmt.Errorf("failed to generate ed25519 key pair: %v", err)
		}
		log.Infof("saving shared signing key")
		err = models.CreateServerSharedSigningKey(base64.StdEncoding.EncodeToString(tokenSigningKey))
		if err != nil {
			return nil, fmt.Errorf("failed to create server shared signing key: %v", err)
		}
	} else {
		tokenSigningKey, err = keys.Base64DecodeEd25519PrivateKey(serverConfig.SharedSigningKey)
		if err != nil {
			return nil, fmt.Errorf("failed to decode shared signing key: %v", err)
		}
	}
	p := &provider{tokenSigningKey: tokenSigningKey}
	keyStore.Set(keyStoreKey, p)
	return p, nil
}

func (p *provider) VerifyAccessTokenWithUserInfo(accessToken string) (*idptypes.ProviderUserInfo, error) {
	return nil, ErrNotImplemented
}

func (p *provider) NewAccessToken(subject, email string, tokenDuration time.Duration) (string, error) {
	return keys.NewJwtToken(p.tokenSigningKey, subject, email, tokenDuration)
}

func (p *provider) VerifyAccessToken(accessToken string) (string, error) {
	if len(p.tokenSigningKey) == 0 {
		return "", fmt.Errorf("signing key is not set")
	}

	pubKey, ok := p.tokenSigningKey.Public().(ed25519.PublicKey)
	if !ok {
		return "", fmt.Errorf("internal error, failed to cast private key to ed25519.PublicKey")
	}
	return keys.VerifyAccessToken(accessToken, pubKey)
}

// GetOrCreateSigningKey generates a new Ed25519 signing key or retrieves the existing one from the server config.
// It saves the key to the server config if it does not already exist.
func GetOrCreateSigningKey() (ed25519.PrivateKey, error) {
	serverConfig, err := models.GetServerConfig()
	if err != nil && err != models.ErrNotFound {
		return nil, fmt.Errorf("failed to obtain server config shared signing key: %v", err)
	}
	var privKey ed25519.PrivateKey
	if serverConfig == nil || serverConfig.SharedSigningKey == "" {
		_, privKey, err = keys.GenerateEd25519KeyPair()
		if err != nil {
			return nil, fmt.Errorf("failed to generate ed25519 key pair: %v", err)
		}
		log.Infof("saving shared signing key")
		err = models.CreateServerSharedSigningKey(base64.StdEncoding.EncodeToString(privKey))
		if err != nil {
			return nil, fmt.Errorf("failed to create server shared signing key: %v", err)
		}
	} else {
		privKey, err = keys.Base64DecodeEd25519PrivateKey(serverConfig.SharedSigningKey)
		if err != nil {
			return nil, fmt.Errorf("failed to decode shared signing key: %v", err)
		}
	}
	return privKey, nil
}
