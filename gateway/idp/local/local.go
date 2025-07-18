package localprovider

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
	idptypes "github.com/hoophq/hoop/gateway/idp/types"
	"github.com/hoophq/hoop/gateway/models"
)

var ErrNotImplemented = fmt.Errorf("local: user info endpoint not implemented for local provider")

type provider struct {
	tokenSigningKey  ed25519.PrivateKey
	serverAuthConfig *models.ServerAuthConfig
}

func New(serverAuthConfig *models.ServerAuthConfig) (*provider, error) {
	var sharedSigningKey string
	if serverAuthConfig != nil && serverAuthConfig.SharedSigningKey != nil {
		sharedSigningKey = *serverAuthConfig.SharedSigningKey
	}

	if sharedSigningKey != "" {
		tokenSigningKey, err := keys.Base64DecodeEd25519PrivateKey(sharedSigningKey)
		if err != nil {
			return nil, fmt.Errorf("failed to decode shared signing key: %v", err)
		}
		return &provider{
			tokenSigningKey:  tokenSigningKey,
			serverAuthConfig: serverAuthConfig,
		}, nil
	}

	_, tokenSigningKey, err := keys.GenerateEd25519KeyPair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate ed25519 key pair: %v", err)
	}
	log.Infof("saving shared signing key")
	err = models.CreateServerSharedSigningKey(base64.StdEncoding.EncodeToString(tokenSigningKey))
	if err != nil {
		return nil, fmt.Errorf("failed to create server shared signing key: %v", err)
	}
	return &provider{
		tokenSigningKey:  tokenSigningKey,
		serverAuthConfig: serverAuthConfig,
	}, nil
}

func (p *provider) ServerConfig() (config idptypes.ServerConfig) {
	appConfig := appconfig.Get()
	config = idptypes.ServerConfig{
		OrgID:      "",
		ApiKey:     appConfig.ApiKey(),
		GrpcURL:    appConfig.GrpcURL(),
		AuthMethod: idptypes.ProviderTypeLocal,
	}

	if p.serverAuthConfig != nil {
		config.OrgID = p.serverAuthConfig.OrgID
		if p.serverAuthConfig.ApiKey != nil {
			config.ApiKey = *p.serverAuthConfig.ApiKey
		}
		if p.serverAuthConfig.GrpcServerURL != nil {
			config.GrpcURL = *p.serverAuthConfig.GrpcServerURL
		}
	}
	return
}

func (p *provider) HasServerConfigChanged(newConfig *models.ServerAuthConfig) bool {
	return hasServerConfigChanged(p.serverAuthConfig, newConfig)
}

func hasServerConfigChanged(old, new *models.ServerAuthConfig) bool {
	var newc models.ServerAuthConfig
	if new != nil {
		newc = *new
	}

	newConfigStr := fmt.Sprintf("authmethod=%v,apikey=%v,grpcurl=%v,shared-signing-key=%v",
		toStr(newc.AuthMethod), toStr(newc.ApiKey), toStr(newc.GrpcServerURL), toStr(newc.SharedSigningKey))

	var oldc models.ServerAuthConfig
	if old != nil {
		oldc = *old
	}

	oldConfigStr := fmt.Sprintf("authmethod=%v,apikey=%v,grpcurl=%v,shared-signing-key=%v",
		toStr(oldc.AuthMethod), toStr(oldc.ApiKey), toStr(oldc.GrpcServerURL), toStr(oldc.SharedSigningKey))
	return newConfigStr != oldConfigStr
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

func toStr(s *string) string { return ptr.ToString(s) }
