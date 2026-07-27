package samlprovider

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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

const (
	defaultGroupsName = "groups"

	// maxMetadataResponseSize caps how much of the metadata response is read.
	// Real metadata documents are a few KiB; anything larger indicates a
	// misconfigured URL streaming an arbitrary payload.
	maxMetadataResponseSize = 1 << 20 // 1 MiB
)

// metadataHTTPClient bounds the metadata fetch so an unresponsive metadata URL
// cannot stall configuration updates or login provider initialization.
var metadataHTTPClient = &http.Client{Timeout: 15 * time.Second}

type Provider struct {
	Options

	idpSSOUrl             string
	serviceProviderIssuer string
	acsURL                string
	audienceURI           string
	tokenSigningKey       ed25519.PrivateKey
	samlServiceProvider   *saml2.SAMLServiceProvider
	resolvedMetadata      ResolvedMetadata
}

type Options struct {
	IdpMetadataURL string
	GroupsClaim    string
}

// ResolvedMetadata identifies the identity provider the configured metadata
// URL actually resolves to. Surfacing it lets administrators spot a metadata
// URL that serves another tenant's document before users are redirected there.
type ResolvedMetadata struct {
	EntityID             string
	SsoURL               string
	CertificateExpiresAt time.Time
}

type ServiceProvider struct {
	*saml2.SAMLServiceProvider
	GroupsClaim string
}

// idpMetadata holds the values extracted from a validated IdP metadata document.
type idpMetadata struct {
	entityID   string
	ssoURL     string
	certStore  dsig.MemoryX509CertificateStore
	certExpiry time.Time
}

// New retrieves the singleton instance of the SAML provider.
// It creates a new instance if it does not exist or if the SAML configuration has changed.
func New(opts Options) (*Provider, error) {
	if opts.IdpMetadataURL == "" {
		return nil, fmt.Errorf("idp metadata URL is required")
	}

	apiURL := appconfig.Get().ApiURL()
	serviceProviderIssuer := fmt.Sprintf("%s/saml/acs", apiURL)
	audienceURI := fmt.Sprintf("%s/saml/acs", apiURL)
	serviceProviderAcsURL := fmt.Sprintf("%s/api/saml/callback", apiURL)

	idpGroupsClaim := opts.GroupsClaim
	if idpGroupsClaim == "" {
		idpGroupsClaim = defaultGroupsName
	}

	rawMetadata, err := fetchIdpMetadata(opts.IdpMetadataURL)
	if err != nil {
		return nil, err
	}

	md, err := parseIdpMetadata(rawMetadata)
	if err != nil {
		return nil, err
	}
	log.Infof("fetched SAML metadata, idp-entity-id=%v, idp-sso-url=%v, cert-expires-at=%v",
		md.entityID, md.ssoURL, md.certExpiry.Format(time.RFC3339))

	tokenSigningKey, err := getOrCreateSigningKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate ed25519 signing key: %v", err)
	}
	p := &Provider{
		idpSSOUrl:             md.ssoURL,
		serviceProviderIssuer: serviceProviderIssuer,
		acsURL:                serviceProviderAcsURL,
		audienceURI:           audienceURI,
		tokenSigningKey:       tokenSigningKey,
		resolvedMetadata: ResolvedMetadata{
			EntityID:             md.entityID,
			SsoURL:               md.ssoURL,
			CertificateExpiresAt: md.certExpiry,
		},
		Options: opts,
	}
	p.samlServiceProvider = p.newServerProvider(md.entityID, &md.certStore)
	log.Infof("loaded SAML 2 provider configuration, sp-issuer=%v, sp-audience=%v, groupsclaim=%v, config-groupsclaim=%v, idp-metadata-url=%v",
		p.serviceProviderIssuer, p.audienceURI, idpGroupsClaim, p.GroupsClaim, p.IdpMetadataURL)

	return p, nil
}

// fetchIdpMetadata downloads the IdP metadata document, failing on transport
// errors, non-2xx responses and oversized payloads.
func fetchIdpMetadata(metadataURL string) ([]byte, error) {
	res, err := metadataHTTPClient.Get(metadataURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch SAML metadata: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, fmt.Errorf("SAML metadata URL returned HTTP status %d", res.StatusCode)
	}

	rawMetadata, err := io.ReadAll(io.LimitReader(res.Body, maxMetadataResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read SAML metadata response: %v", err)
	}
	if len(rawMetadata) > maxMetadataResponseSize {
		return nil, fmt.Errorf("SAML metadata response exceeds %d bytes", maxMetadataResponseSize)
	}
	return rawMetadata, nil
}

// parseIdpMetadata validates the metadata document and extracts everything
// needed to assemble the SAML service provider. It rejects documents that can
// never produce a working login — missing SSO endpoint, no signing
// certificates, or certificates that are all outside their validity window
// (assertion signature validation refuses such certificates at callback time).
// Failing here keeps the error at configuration time, where it is actionable.
func parseIdpMetadata(rawMetadata []byte) (*idpMetadata, error) {
	md := &saml2types.EntityDescriptor{}
	if err := xml.Unmarshal(rawMetadata, md); err != nil {
		return nil, fmt.Errorf("failed to unmarshal SAML metadata: %v", err)
	}

	var idpSsoURL string
	if md.IDPSSODescriptor != nil && len(md.IDPSSODescriptor.SingleSignOnServices) > 0 {
		idpSsoURL = md.IDPSSODescriptor.SingleSignOnServices[0].Location
	}
	if idpSsoURL == "" {
		return nil, fmt.Errorf("SAML metadata does not contain IDP SSO URL")
	}

	certStore := dsig.MemoryX509CertificateStore{
		Roots: []*x509.Certificate{},
	}

	now := time.Now().UTC()
	var certExpiry time.Time
	hasUsableCert := false
	for _, kd := range md.IDPSSODescriptor.KeyDescriptors {
		for idx, xcert := range kd.KeyInfo.X509Data.X509Certificates {
			if xcert.Data == "" {
				return nil, fmt.Errorf("metadata certificate(%d) must not be empty", idx)
			}
			// xsd:base64Binary allows arbitrary whitespace inside the value
			certData, err := base64.StdEncoding.DecodeString(stripWhitespace(xcert.Data))
			if err != nil {
				return nil, fmt.Errorf("failed to decode certificate data: %v", err)
			}

			idpCert, err := x509.ParseCertificate(certData)
			if err != nil {
				return nil, fmt.Errorf("failed to parse certificate: %v", err)
			}
			certStore.Roots = append(certStore.Roots, idpCert)
			if idpCert.NotAfter.After(certExpiry) {
				certExpiry = idpCert.NotAfter
			}
			if !now.Before(idpCert.NotBefore) && !now.After(idpCert.NotAfter) {
				hasUsableCert = true
			}
		}
	}

	if len(certStore.Roots) == 0 {
		return nil, fmt.Errorf(
			"SAML IdP metadata does not contain any signing certificates (entity-id=%q, sso-url=%q); "+
				"verify the idp_metadata_url points to your identity provider application's metadata",
			md.EntityID, idpSsoURL)
	}
	if !hasUsableCert {
		return nil, fmt.Errorf(
			"none of the signing certificates in the SAML IdP metadata is currently valid, latest expiry %s (entity-id=%q, sso-url=%q); "+
				"verify the idp_metadata_url points to your identity provider application's metadata",
			certExpiry.Format("2006-01-02"), md.EntityID, idpSsoURL)
	}

	return &idpMetadata{
		entityID:   md.EntityID,
		ssoURL:     idpSsoURL,
		certStore:  certStore,
		certExpiry: certExpiry,
	}, nil
}

func stripWhitespace(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r':
			return -1
		}
		return r
	}, s)
}

func (p *Provider) newServerProvider(idpIssuer string, idpCertStore dsig.X509CertificateStore) *saml2.SAMLServiceProvider {
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

func (p *Provider) ServiceProvider() *ServiceProvider {
	return &ServiceProvider{
		SAMLServiceProvider: p.samlServiceProvider,
		GroupsClaim:         p.GroupsClaim,
	}
}

// ResolvedMetadata returns the identity provider details extracted from the
// metadata document when the provider was initialized.
func (p *Provider) ResolvedMetadata() ResolvedMetadata {
	return p.resolvedMetadata
}

func (p *Provider) NewAccessToken(subject, email string, tokenDuration time.Duration) (string, error) {
	return keys.NewJwtToken(p.tokenSigningKey, subject, email, tokenDuration)
}

func (p *Provider) VerifyAccessToken(accessToken string) (subject string, err error) {
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
func (p *Provider) VerifyAccessTokenWithUserInfo(accessToken string) (*idptypes.ProviderUserInfo, error) {
	return nil, ErrNotImplemented
}

// getOrCreateSigningKey generates a new Ed25519 signing key or retrieves the existing one from the server config.
// It saves the key to the server config if it doesn't exist
func getOrCreateSigningKey() (ed25519.PrivateKey, error) {
	sharedSigningKey, err := models.GetSharedSigningKey()
	if err != nil && err != models.ErrNotFound {
		return nil, fmt.Errorf("failed to obtain server config shared signing key: %v", err)
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
	log.Debugf("saving shared signing key")
	err = models.CreateServerSharedSigningKey(base64.StdEncoding.EncodeToString(privKey))
	if err != nil {
		return nil, fmt.Errorf("failed to create server shared signing key: %v", err)
	}
	return privKey, nil
}
