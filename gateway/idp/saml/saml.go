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

	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	"github.com/hoophq/hoop/gateway/appconfig"
	localprovider "github.com/hoophq/hoop/gateway/idp/local"
	idptypes "github.com/hoophq/hoop/gateway/idp/types"
	saml2 "github.com/russellhaering/gosaml2"
	"github.com/russellhaering/gosaml2/types"
	dsig "github.com/russellhaering/goxmldsig"
)

var ErrNotImplemented = fmt.Errorf("saml: user info endpoint not implemented for SAML2 provider")

var (
	metadataStore = memory.New()
	metadataKey   = "metadata"
)

type provider struct {
	idpSSOUrl             string
	serviceProviderIssuer string
	acsURL                string
	audienceURI           string
	tokenSigningKey       ed25519.PrivateKey
	samlServiceProvider   *saml2.SAMLServiceProvider
	options               Options
}

type Options struct {
	GroupsName string
}

type ServiceProvider struct {
	*saml2.SAMLServiceProvider
	Options Options
}

func GetInstance() (*provider, error) {
	if obj := metadataStore.Get(metadataKey); obj != nil {
		data, ok := obj.(*provider)
		if !ok {
			return nil, fmt.Errorf("internal error, failed to cast metadata to provider, got=%T", obj)
		}
		return data, nil
	}
	apiURL := appconfig.Get().ApiURL()

	serviceProviderIssuer := fmt.Sprintf("%s/saml/acs", apiURL)
	audienceURI := fmt.Sprintf("%s/saml/acs", apiURL)
	serviceProviderAcsURL := fmt.Sprintf("%s/api/saml/callback", apiURL)

	// TODO: load these attributes from database on upcoming releases
	idpMetadataUrl := "<idp-metadata-url>"
	idpGroupsName := "<idp-groups-name>"

	res, err := http.Get(idpMetadataUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch SAML metadata: %v", err)
	}

	rawMetadata, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read SAML metadata response: %v", err)
	}

	md := &types.EntityDescriptor{}
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
	log.Infof("fetched SAML metadata, idp-entity-id=%v, idp-sso-url=%v, valid-until=%v, key-descriptions=%d",
		md.EntityID, idpSsoURL, md.ValidUntil.Format(time.RFC3339), keyDescriptorsLength)

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

	tokenSigningKey, err := localprovider.GetOrCreateSigningKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate ed25519 signing key: %v", err)
	}
	p := &provider{
		idpSSOUrl:             md.IDPSSODescriptor.SingleSignOnServices[0].Location,
		serviceProviderIssuer: serviceProviderIssuer,
		acsURL:                serviceProviderAcsURL,
		audienceURI:           audienceURI,
		tokenSigningKey:       tokenSigningKey,
		options:               Options{GroupsName: idpGroupsName},
	}
	p.samlServiceProvider = p.newServerProvider(md.EntityID, &certStore)
	log.Infof("loaded SAML 2 provider configuration, sp-issuer=%v, sp-audience=%v, groupsclaim=%v, idp-metadata-url=%v",
		p.serviceProviderIssuer, p.audienceURI, idpGroupsName, idpMetadataUrl)
	metadataStore.Set(metadataKey, p)
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
