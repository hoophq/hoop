// Command spiffe-mint is a local-dev helper that generates a self-signed
// SPIFFE JWT-SVID plus the matching JWKS trust bundle. It is intended to
// stand in for SPIRE during development of the gateway's SPIFFE support;
// see documentation/setup/deployment/spiffe.mdx for production setup.
//
// The keypair is generated on first run and reused on subsequent runs so
// the bundle (which the gateway caches) does not change across rotations.
// The JWT is always re-minted with a fresh iat/exp.
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func main() {
	outDir := flag.String("out", "./dist/dev/spiffe", "output directory")
	trustDomain := flag.String("trust-domain", "local.test", "SPIFFE trust domain")
	spiffeID := flag.String("spiffe-id", "spiffe://local.test/agent/local-dev", "SPIFFE ID emitted as the sub claim")
	audience := flag.String("audience", "http://127.0.0.1:8009", "audience emitted as the aud claim; must match HOOP_SPIFFE_AUDIENCE")
	ttl := flag.Duration("ttl", 24*time.Hour, "JWT lifetime from now")
	flag.Parse()

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatal(err)
	}

	privPath := filepath.Join(*outDir, "priv.pem")
	bundlePath := filepath.Join(*outDir, "bundle.jwks")
	jwtPath := filepath.Join(*outDir, "agent.jwt")

	priv, reused, err := loadOrCreateKey(privPath)
	if err != nil {
		log.Fatalf("load/create key: %v", err)
	}

	kid := kidFor(&priv.PublicKey)

	// The JWKS is deterministic in the public key, so writing it every
	// invocation is safe (no churn once the key is stable). The dev flow
	// in scripts/dev/spiffe-prep.sh base64-encodes this file into
	// HOOP_SPIFFE_BUNDLE_JWKS in .env; the gateway is also happy to
	// consume the file directly via HOOP_SPIFFE_BUNDLE_FILE if you wire
	// it that way.
	if err := writeJWKS(bundlePath, &priv.PublicKey, kid); err != nil {
		log.Fatalf("write bundle: %v", err)
	}

	signed, exp, err := mintJWT(priv, kid, *spiffeID, *audience, *ttl)
	if err != nil {
		log.Fatalf("mint jwt: %v", err)
	}
	if err := os.WriteFile(jwtPath, []byte(signed), 0o600); err != nil {
		log.Fatalf("write jwt: %v", err)
	}

	src := "generated"
	if reused {
		src = "reused"
	}
	fmt.Printf("key          %s (%s)\n", privPath, src)
	fmt.Printf("trust_domain %s\n", *trustDomain)
	fmt.Printf("spiffe_id    %s\n", *spiffeID)
	fmt.Printf("audience     %s\n", *audience)
	fmt.Printf("bundle       %s\n", bundlePath)
	fmt.Printf("jwt          %s (expires %s)\n", jwtPath, exp.Format(time.RFC3339))
}

func loadOrCreateKey(path string) (*rsa.PrivateKey, bool, error) {
	if b, err := os.ReadFile(path); err == nil {
		blk, _ := pem.Decode(b)
		if blk == nil {
			return nil, false, fmt.Errorf("existing key at %q is not PEM-encoded", path)
		}
		priv, err := x509.ParsePKCS1PrivateKey(blk.Bytes)
		if err != nil {
			return nil, false, fmt.Errorf("parse existing key: %w", err)
		}
		return priv, true, nil
	}
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, false, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv),
	})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		return nil, false, err
	}
	return priv, false, nil
}

// kidFor derives a stable key id from the public key bytes. We use
// SHA-256 of the DER-encoded SPKI truncated to a short base64 suffix;
// keyfunc (used by the gateway) matches kid as an opaque string so any
// stable value works.
func kidFor(pub *rsa.PublicKey) string {
	der, _ := x509.MarshalPKIXPublicKey(pub)
	sum := sha256.Sum256(der)
	return base64.RawURLEncoding.EncodeToString(sum[:8])
}

func writeJWKS(path string, pub *rsa.PublicKey, kid string) error {
	jwks := map[string]any{
		"keys": []map[string]any{{
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"kid": kid,
			"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}},
	}
	b, err := json.MarshalIndent(jwks, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func mintJWT(priv *rsa.PrivateKey, kid, spiffeID, audience string, ttl time.Duration) (string, time.Time, error) {
	now := time.Now()
	exp := now.Add(ttl)
	claims := jwt.MapClaims{
		"sub": spiffeID,
		"aud": []string{audience},
		"iss": "local-dev-issuer",
		"iat": now.Unix(),
		"exp": exp.Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	signed, err := tok.SignedString(priv)
	return signed, exp, err
}
