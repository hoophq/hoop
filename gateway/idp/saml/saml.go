package samlprovider

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
	idptypes "github.com/hoophq/hoop/gateway/idp/types"
	"github.com/hoophq/hoop/gateway/models"
	saml2 "github.com/russellhaering/gosaml2"
	saml2types "github.com/russellhaering/gosaml2/types"
	dsig "github.com/russellhaering/goxmldsig"
)

var ErrNotImplemented = fmt.Errorf("saml: user info endpoint not implemented for SAML2 provider")

const defaultGroupsName = "groups"

type provider struct {
	idpSSOUrl             string
	serviceProviderIssuer string
	acsURL                string
	audienceURI           string
	tokenSigningKey       ed25519.PrivateKey
	samlServiceProvider   *saml2.SAMLServiceProvider
	options               Options
	serverAuthConfig      *models.ServerAuthConfig
}

type Options struct {
	GroupsName string
}

type ServiceProvider struct {
	*saml2.SAMLServiceProvider
	Options Options
}

// New retrieves the singleton instance of the SAML provider.
// It creates a new instance if it does not exist or if the SAML configuration has changed.
func New(serverAuthConfig *models.ServerAuthConfig) (*provider, error) {
	if serverAuthConfig == nil || serverAuthConfig.SamlConfig == nil {
		return nil, fmt.Errorf("SAML configuration is not set")
	}

	apiURL := appconfig.Get().ApiURL()
	serviceProviderIssuer := fmt.Sprintf("%s/saml/acs", apiURL)
	audienceURI := fmt.Sprintf("%s/saml/acs", apiURL)
	serviceProviderAcsURL := fmt.Sprintf("%s/api/saml/callback", apiURL)

	idpMetadataUrl := serverAuthConfig.SamlConfig.IdpMetadataURL
	idpGroupsClaim := serverAuthConfig.SamlConfig.GroupsClaim
	if idpGroupsClaim == "" {
		idpGroupsClaim = defaultGroupsName
	}

	res, err := http.Get(idpMetadataUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch SAML metadata: %v", err)
	}

	rawMetadata, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read SAML metadata response: %v", err)
	}

	md := &saml2types.EntityDescriptor{}
	err = xml.Unmarshal(rawMetadata, md)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal SAML metadata: %v", err)
	}

	var idpSsoURL string
	if md.IDPSSODescriptor != nil && len(md.IDPSSODescriptor.SingleSignOnServices) > 0 {
		idpSsoURL = md.IDPSSODescriptor.SingleSignOnServices[0].Location
	}

	keyDescriptorsLength := 0
	if md.IDPSSODescriptor != nil {
		keyDescriptorsLength = len(md.IDPSSODescriptor.KeyDescriptors)
	}
	if idpSsoURL == "" {
		return nil, fmt.Errorf("SAML metadata does not contain IDP SSO URL")
	}
	log.Infof("fetched SAML metadata, idp-entity-id=%v, idp-sso-url=%v, key-descriptions=%d",
		md.EntityID, idpSsoURL, keyDescriptorsLength)

	certStore := dsig.MemoryX509CertificateStore{
		Roots: []*x509.Certificate{},
	}

	for _, kd := range md.IDPSSODescriptor.KeyDescriptors {
		for idx, xcert := range kd.KeyInfo.X509Data.X509Certificates {
			if xcert.Data == "" {
				return nil, fmt.Errorf("metadata certificate(%d) must not be empty", idx)
			}
			certData, err := base64.StdEncoding.DecodeString(xcert.Data)
			if err != nil {
				return nil, fmt.Errorf("failed to decode certificate data: %v", err)
			}

			idpCert, err := x509.ParseCertificate(certData)
			if err != nil {
				return nil, fmt.Errorf("failed to parse certificate: %v", err)
			}
			certStore.Roots = append(certStore.Roots, idpCert)
		}
	}

	tokenSigningKey, err := getOrCreateSigningKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate ed25519 signing key: %v", err)
	}
	p := &provider{
		idpSSOUrl:             md.IDPSSODescriptor.SingleSignOnServices[0].Location,
		serviceProviderIssuer: serviceProviderIssuer,
		acsURL:                serviceProviderAcsURL,
		audienceURI:           audienceURI,
		tokenSigningKey:       tokenSigningKey,
		options:               Options{GroupsName: idpGroupsClaim},
		serverAuthConfig:      serverAuthConfig,
	}
	p.samlServiceProvider = p.newServerProvider(md.EntityID, &certStore)
	log.Infof("loaded SAML 2 provider configuration, sp-issuer=%v, sp-audience=%v, groupsclaim=%v, config-groupsclaim=%v, idp-metadata-url=%v",
		p.serviceProviderIssuer, p.audienceURI, idpGroupsClaim, serverAuthConfig.SamlConfig.GroupsClaim, idpMetadataUrl)

	return p, nil
}

func (p *provider) newServerProvider(idpIssuer string, idpCertStore dsig.X509CertificateStore) *saml2.SAMLServiceProvider {
	return &saml2.SAMLServiceProvider{
		IdentityProviderSSOURL:      p.idpSSOUrl,
		IdentityProviderIssuer:      idpIssuer,
		ServiceProviderIssuer:       p.serviceProviderIssuer,
		AssertionConsumerServiceURL: p.acsURL,
		SignAuthnRequests:           true,
		AudienceURI:                 p.audienceURI,
		IDPCertificateStore:         idpCertStore,
		SPKeyStore:                  dsig.RandomKeyStoreForTest(),
		AllowMissingAttributes:      true,
	}
}

func (p *provider) ServiceProvider() *ServiceProvider {
	return &ServiceProvider{
		SAMLServiceProvider: p.samlServiceProvider,
		Options:             p.options,
	}
}

func (p *provider) ServerConfig() (config idptypes.ServerConfig) {
	appConfig := appconfig.Get()
	config = idptypes.ServerConfig{
		ApiKey:     appConfig.ApiKey(),
		GrpcURL:    appConfig.GrpcURL(),
		AuthMethod: idptypes.ProviderTypeSAML,
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
	var saml models.ServerAuthSamlConfig
	if newc.SamlConfig != nil {
		saml = *newc.SamlConfig
	}

	newConfigStr := fmt.Sprintf("authmethod=%v,apikey=%v,grpcurl=%v,idp-metadata-url=%v,groupsclaim=%v,shared-signing-key=%v",
		toStr(newc.AuthMethod), toStr(newc.ApiKey), toStr(newc.GrpcServerURL), saml.IdpMetadataURL, saml.GroupsClaim, toStr(newc.SharedSigningKey))

	var oldc models.ServerAuthConfig
	if old != nil {
		oldc = *old
	}

	var oldSaml models.ServerAuthSamlConfig
	if oldc.SamlConfig != nil {
		oldSaml = *oldc.SamlConfig
	}

	oldConfigStr := fmt.Sprintf("authmethod=%v,apikey=%v,grpcurl=%v,idp-metadata-url=%v,groupsclaim=%v,shared-signing-key=%v",
		toStr(oldc.AuthMethod), toStr(oldc.ApiKey), toStr(oldc.GrpcServerURL), oldSaml.IdpMetadataURL, oldSaml.GroupsClaim, toStr(oldc.SharedSigningKey))

	return newConfigStr != oldConfigStr
}

func (p *provider) NewAccessToken(subject, email string, tokenDuration time.Duration) (string, error) {
	return keys.NewJwtToken(p.tokenSigningKey, subject, email, tokenDuration)
}

func (p *provider) VerifyAccessToken(accessToken string) (subject string, err error) {
	if len(p.tokenSigningKey) == 0 {
		return "", fmt.Errorf("signing key is not set")
	}

	pubKey, ok := p.tokenSigningKey.Public().(ed25519.PublicKey)
	if !ok {
		return "", fmt.Errorf("internal error, failed to cast private key to ed25519.PublicKey")
	}
	return keys.VerifyAccessToken(accessToken, pubKey)
}

// VerifyAccessTokenWithUserInfo verify the access token by querying the OIDC user info endpoint
func (p *provider) VerifyAccessTokenWithUserInfo(accessToken string) (*idptypes.ProviderUserInfo, error) {
	return nil, ErrNotImplemented
}

// getOrCreateSigningKey generates a new Ed25519 signing key or retrieves the existing one from the server config.
// It saves the key to the server config if it doesn't exist
func getOrCreateSigningKey() (ed25519.PrivateKey, error) {
	serverConfig, err := models.GetServerAuthConfig()
	if err != nil && err != models.ErrNotFound {
		return nil, fmt.Errorf("failed to obtain server config shared signing key: %v", err)
	}
	var sharedSigningKey string
	if serverConfig != nil && serverConfig.SharedSigningKey != nil {
		sharedSigningKey = *serverConfig.SharedSigningKey
	}
	if sharedSigningKey != "" {
		privKey, err := keys.Base64DecodeEd25519PrivateKey(sharedSigningKey)
		if err != nil {
			return nil, fmt.Errorf("failed to decode shared signing key: %v", err)
		}
		return privKey, nil
	}
	_, privKey, err := keys.GenerateEd25519KeyPair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate ed25519 key pair: %v", err)
	}
	log.Infof("saving shared signing key")
	err = models.CreateServerSharedSigningKey(base64.StdEncoding.EncodeToString(privKey))
	if err != nil {
		return nil, fmt.Errorf("failed to create server shared signing key: %v", err)
	}
	return privKey, nil
}

func toStr(s *string) string { return ptr.ToString(s) }
