package spiffe

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/session"
	codectypes "github.com/hoophq/libhoop/v2/codec/types"
)

const testTrustDomain = "example.org"

func newVerifier(t *testing.T, f *fakeAPI, cfg daemon.IdentityConfig) daemon.IdentitySource {
	t.Helper()
	t.Setenv(socketEnv, f.addr)
	cfg.Type = IdentityType
	v, err := buildVerifier(cfg, daemon.IdentityDeps{})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestVerifyAcceptsJWTSVID(t *testing.T) {
	f := startFake(t)
	ca := newTestCA(t, testTrustDomain)
	v := newVerifier(t, f, daemon.IdentityConfig{})

	id, err := v.Verify(context.Background(), "ignored", ca.jwtSVID(t, "/ns/payments/sa/api", DefaultAudience), daemon.PeerInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if id.Subject != "spiffe://example.org/ns/payments/sa/api" {
		t.Fatalf("subject = %q", id.Subject)
	}
	if id.Attributes[AttrTrustDomain] != "example.org" || id.Attributes[AttrPath] != "/ns/payments/sa/api" {
		t.Fatalf("attributes = %v", id.Attributes)
	}
}

func TestVerifyHonoursConfiguredAudience(t *testing.T) {
	f := startFake(t)
	ca := newTestCA(t, testTrustDomain)
	v := newVerifier(t, f, daemon.IdentityConfig{Audience: "pg-prod"})

	if _, err := v.Verify(context.Background(), "", ca.jwtSVID(t, "/sa/x", "pg-prod"), daemon.PeerInfo{}); err != nil {
		t.Fatal(err)
	}
	_, err := v.Verify(context.Background(), "", ca.jwtSVID(t, "/sa/x", DefaultAudience), daemon.PeerInfo{})
	assertRefused(t, err, "jwt-svid")
}

func TestVerifyRefusesOtherTrustDomain(t *testing.T) {
	f := startFake(t)
	other := newTestCA(t, "other.org")
	v := newVerifier(t, f, daemon.IdentityConfig{TrustDomain: testTrustDomain})

	_, err := v.Verify(context.Background(), "", other.jwtSVID(t, "/sa/x", DefaultAudience), daemon.PeerInfo{})
	assertRefused(t, err, "outside trust domain example.org")
}

func TestVerifyRefusesOutsidePrefix(t *testing.T) {
	f := startFake(t)
	ca := newTestCA(t, testTrustDomain)
	v := newVerifier(t, f, daemon.IdentityConfig{Prefix: "/ns/payments"})

	for _, path := range []string{"/ns/payments/sa/api", "/ns/payments"} {
		if _, err := v.Verify(context.Background(), "", ca.jwtSVID(t, path, DefaultAudience), daemon.PeerInfo{}); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	for _, path := range []string{"/ns/billing/sa/api", "/ns/payments-sandbox/sa"} {
		_, err := v.Verify(context.Background(), "", ca.jwtSVID(t, path, DefaultAudience), daemon.PeerInfo{})
		assertRefused(t, err, "is not under /ns/payments")
	}
}

func TestVerifyRefusesGarbageAndEmptySecret(t *testing.T) {
	f := startFake(t)
	v := newVerifier(t, f, daemon.IdentityConfig{})

	_, err := v.Verify(context.Background(), "", "", daemon.PeerInfo{})
	assertRefused(t, err, "no jwt-svid")
	_, err = v.Verify(context.Background(), "", "not-a-jwt", daemon.PeerInfo{})
	assertRefused(t, err, "jwt-svid")
}

func TestVerifyReportsUnreachableAgentAsError(t *testing.T) {
	t.Setenv(socketEnv, "tcp://127.0.0.1:1")
	v, err := buildVerifier(daemon.IdentityConfig{Type: IdentityType}, daemon.IdentityDeps{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	_, err = v.Verify(ctx, "", "x.y.z", daemon.PeerInfo{})
	if err == nil || errors.Is(err, codectypes.ErrLoginRefused) {
		t.Fatalf("want a transport error, not a refusal: %v", err)
	}
}

func TestBuildVerifierRejectsBadConfig(t *testing.T) {
	for name, cfg := range map[string]daemon.IdentityConfig{
		"trust domain": {TrustDomain: "spiffe://has scheme"},
		"prefix":       {Prefix: "no-leading-slash"},
	} {
		if _, err := buildVerifier(cfg, daemon.IdentityDeps{}); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestCredentialPicksSVIDByID(t *testing.T) {
	ca := newTestCA(t, testTrustDomain)
	first := ca.x509SVID(t, "/sa/default")
	second := ca.x509SVID(t, "/sa/pg-writer")
	f := startFake(t, first, second)
	src, err := buildSource(&daemon.CredentialSourcesConfig{SPIFFE: &daemon.SPIFFESourceConfig{Socket: f.addr}})
	if err != nil {
		t.Fatal(err)
	}

	cred, err := src.Credential(context.Background(), daemon.CredentialEntry{Name: "writer", SPIFFEID: second.id.String(), User: "app_writer"}, session.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if cred.User != "app_writer" || cred.Password != "" || cred.GSS != nil || cred.Release != nil {
		t.Fatalf("credential = %+v", cred)
	}
	if cred.Cert == nil || cred.Cert.Leaf == nil || len(cred.Cert.Leaf.URIs) != 1 || cred.Cert.Leaf.URIs[0].String() != second.id.String() {
		t.Fatalf("cert does not carry %s", second.id)
	}
	if cred.Cert.PrivateKey == nil || len(cred.Cert.Certificate) != 1 {
		t.Fatalf("cert = %+v", cred.Cert)
	}

	_, err = src.Credential(context.Background(), daemon.CredentialEntry{Name: "ghost", SPIFFEID: "spiffe://example.org/sa/missing"}, session.Identity{})
	if err == nil || !strings.Contains(err.Error(), "no x509-svid for spiffe://example.org/sa/missing") {
		t.Fatalf("err = %v", err)
	}
}

func TestCredentialRequiresValidSPIFFEID(t *testing.T) {
	src, err := buildSource(&daemon.CredentialSourcesConfig{SPIFFE: &daemon.SPIFFESourceConfig{Socket: "tcp://127.0.0.1:1"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "not a spiffe id"} {
		if _, err := src.Credential(context.Background(), daemon.CredentialEntry{Name: "e", SPIFFEID: id}, session.Identity{}); err == nil {
			t.Errorf("%q: want error", id)
		}
	}
}

func TestBuildSourceRequiresBlock(t *testing.T) {
	if _, err := buildSource(nil); err == nil {
		t.Fatal("nil config: want error")
	}
	if _, err := buildSource(&daemon.CredentialSourcesConfig{}); err == nil {
		t.Fatal("no spiffe block: want error")
	}
}

func TestSocketAddr(t *testing.T) {
	t.Setenv(socketEnv, "")
	if got := socketAddr(""); got != DefaultSocket {
		t.Fatalf("default = %q", got)
	}
	t.Setenv(socketEnv, "unix:///tmp/agent.sock")
	if got := socketAddr(""); got != "unix:///tmp/agent.sock" {
		t.Fatalf("env = %q", got)
	}
	if got := socketAddr("tcp://127.0.0.1:9"); got != "tcp://127.0.0.1:9" {
		t.Fatalf("configured = %q", got)
	}
}

func assertRefused(t *testing.T, err error, want string) {
	t.Helper()
	if !errors.Is(err, codectypes.ErrLoginRefused) {
		t.Fatalf("want ErrLoginRefused, got %v", err)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("want %q in %q", want, err)
	}
}
