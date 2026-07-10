package samlprovider

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func generateCertBase64(t *testing.T, notBefore, notAfter time.Time) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "Test IdP"},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(der)
}

type metadataSpec struct {
	entityID string
	ssoURL   string
	certs    []string
}

func buildMetadata(spec metadataSpec) []byte {
	var keyDescriptors strings.Builder
	for _, cert := range spec.certs {
		fmt.Fprintf(&keyDescriptors, `
    <KeyDescriptor use="signing">
      <ds:KeyInfo xmlns:ds="http://www.w3.org/2000/09/xmldsig#">
        <ds:X509Data>
          <ds:X509Certificate>%s</ds:X509Certificate>
        </ds:X509Data>
      </ds:KeyInfo>
    </KeyDescriptor>`, cert)
	}
	var ssoService string
	if spec.ssoURL != "" {
		ssoService = fmt.Sprintf(`
    <SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect" Location="%s"/>`, spec.ssoURL)
	}
	return fmt.Appendf(nil, `<?xml version="1.0"?>
<EntityDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata" entityID="%s">
  <IDPSSODescriptor xmlns:ds="http://www.w3.org/2000/09/xmldsig#" protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">%s%s
  </IDPSSODescriptor>
</EntityDescriptor>`, spec.entityID, keyDescriptors.String(), ssoService)
}

func TestParseIdpMetadata(t *testing.T) {
	now := time.Now().UTC()
	validCert := generateCertBase64(t, now.Add(-time.Hour), now.Add(24*time.Hour))
	expiredCert := generateCertBase64(t, now.Add(-48*time.Hour), now.Add(-24*time.Hour))
	notYetValidCert := generateCertBase64(t, now.Add(24*time.Hour), now.Add(48*time.Hour))

	entityID := "https://idp.example.com/saml/metadata/1234"
	ssoURL := "https://idp.example.com/trust/saml2/http-redirect/sso/1234"

	t.Run("valid metadata", func(t *testing.T) {
		md, err := parseIdpMetadata(buildMetadata(metadataSpec{entityID: entityID, ssoURL: ssoURL, certs: []string{validCert}}))
		require.NoError(t, err)
		require.Equal(t, entityID, md.entityID)
		require.Equal(t, ssoURL, md.ssoURL)
		require.Len(t, md.certStore.Roots, 1)
		require.True(t, md.certExpiry.After(now))
	})

	t.Run("certificate data with embedded whitespace", func(t *testing.T) {
		var wrapped strings.Builder
		for i, r := range validCert {
			if i > 0 && i%64 == 0 {
				wrapped.WriteString("\n          ")
			}
			wrapped.WriteRune(r)
		}
		md, err := parseIdpMetadata(buildMetadata(metadataSpec{entityID: entityID, ssoURL: ssoURL, certs: []string{wrapped.String()}}))
		require.NoError(t, err)
		require.Len(t, md.certStore.Roots, 1)
	})

	t.Run("one expired and one valid certificate", func(t *testing.T) {
		md, err := parseIdpMetadata(buildMetadata(metadataSpec{entityID: entityID, ssoURL: ssoURL, certs: []string{expiredCert, validCert}}))
		require.NoError(t, err)
		require.Len(t, md.certStore.Roots, 2)
	})

	t.Run("all certificates expired", func(t *testing.T) {
		_, err := parseIdpMetadata(buildMetadata(metadataSpec{entityID: entityID, ssoURL: ssoURL, certs: []string{expiredCert}}))
		require.ErrorContains(t, err, "is currently valid")
		require.ErrorContains(t, err, entityID)
		require.ErrorContains(t, err, ssoURL)
	})

	t.Run("certificate not yet valid", func(t *testing.T) {
		_, err := parseIdpMetadata(buildMetadata(metadataSpec{entityID: entityID, ssoURL: ssoURL, certs: []string{notYetValidCert}}))
		require.ErrorContains(t, err, "is currently valid")
	})

	t.Run("no signing certificates", func(t *testing.T) {
		_, err := parseIdpMetadata(buildMetadata(metadataSpec{entityID: entityID, ssoURL: ssoURL}))
		require.ErrorContains(t, err, "does not contain any signing certificates")
		require.ErrorContains(t, err, entityID)
	})

	t.Run("no SSO endpoint", func(t *testing.T) {
		_, err := parseIdpMetadata(buildMetadata(metadataSpec{entityID: entityID, certs: []string{validCert}}))
		require.ErrorContains(t, err, "does not contain IDP SSO URL")
	})

	t.Run("empty certificate element", func(t *testing.T) {
		_, err := parseIdpMetadata(buildMetadata(metadataSpec{entityID: entityID, ssoURL: ssoURL, certs: []string{""}}))
		require.ErrorContains(t, err, "must not be empty")
	})

	t.Run("not a metadata document", func(t *testing.T) {
		_, err := parseIdpMetadata([]byte("<html><body>Sign in</body></html>"))
		require.ErrorContains(t, err, "failed to unmarshal SAML metadata")
	})
}

func TestFetchIdpMetadata(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("metadata-body"))
		}))
		defer srv.Close()

		raw, err := fetchIdpMetadata(srv.URL)
		require.NoError(t, err)
		require.Equal(t, "metadata-body", string(raw))
	})

	t.Run("non-2xx status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "not found", http.StatusNotFound)
		}))
		defer srv.Close()

		_, err := fetchIdpMetadata(srv.URL)
		require.ErrorContains(t, err, "returned HTTP status 404")
	})

	t.Run("oversized response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(make([]byte, maxMetadataResponseSize+1))
		}))
		defer srv.Close()

		_, err := fetchIdpMetadata(srv.URL)
		require.ErrorContains(t, err, "exceeds")
	})

	t.Run("unreachable server", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		srv.Close()

		_, err := fetchIdpMetadata(srv.URL)
		require.ErrorContains(t, err, "failed to fetch SAML metadata")
	})
}
