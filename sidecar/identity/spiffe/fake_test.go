package spiffe

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/spiffe/go-spiffe/v2/proto/spiffe/workload"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

// fakeAPI is a Workload API over loopback TCP. go-spiffe keeps its own fake
// under internal/, so this one serves the two calls the package makes:
// FetchX509SVID streams one response and holds the stream open, and
// ValidateJWTSVID accepts any well-formed JWT-SVID for the asked audience,
// which is what the real agent does after it checked the signature.
type fakeAPI struct {
	workload.UnimplementedSpiffeWorkloadAPIServer
	addr string
	x509 *workload.X509SVIDResponse
}

func startFake(t *testing.T, svids ...testSVID) *fakeAPI {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeAPI{addr: "tcp://" + lis.Addr().String(), x509: &workload.X509SVIDResponse{}}
	for _, s := range svids {
		f.x509.Svids = append(f.x509.Svids, &workload.X509SVID{
			SpiffeId:    s.id.String(),
			X509Svid:    s.certDER,
			X509SvidKey: s.keyDER,
			Bundle:      s.caDER,
		})
	}
	srv := grpc.NewServer()
	workload.RegisterSpiffeWorkloadAPIServer(srv, f)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return f
}

func (f *fakeAPI) FetchX509SVID(_ *workload.X509SVIDRequest, stream grpc.ServerStreamingServer[workload.X509SVIDResponse]) error {
	if err := checkHeader(stream.Context()); err != nil {
		return err
	}
	if len(f.x509.Svids) == 0 {
		return status.Error(codes.PermissionDenied, "no identity issued")
	}
	if err := stream.Send(f.x509); err != nil {
		return err
	}
	<-stream.Context().Done()
	return stream.Context().Err()
}

func (f *fakeAPI) ValidateJWTSVID(ctx context.Context, req *workload.ValidateJWTSVIDRequest) (*workload.ValidateJWTSVIDResponse, error) {
	if err := checkHeader(ctx); err != nil {
		return nil, err
	}
	if req.Audience == "" || req.Svid == "" {
		return nil, status.Error(codes.InvalidArgument, "audience and svid are required")
	}
	svid, err := jwtsvid.ParseInsecure(req.Svid, []string{req.Audience})
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	claims, err := structpb.NewStruct(svid.Claims)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &workload.ValidateJWTSVIDResponse{SpiffeId: svid.ID.String(), Claims: claims}, nil
}

// checkHeader enforces the security header the specification requires on
// every call, so a client that drops it fails here as it would at an agent.
func checkHeader(ctx context.Context) error {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok || len(md.Get("workload.spiffe.io")) == 0 || md.Get("workload.spiffe.io")[0] != "true" {
		return status.Error(codes.InvalidArgument, "security header missing")
	}
	return nil
}

// testCA signs X509-SVIDs and JWT-SVIDs for one trust domain.
type testCA struct {
	td     spiffeid.TrustDomain
	cert   *x509.Certificate
	key    *ecdsa.PrivateKey
	jwtKey *ecdsa.PrivateKey
}

type testSVID struct {
	id      spiffeid.ID
	certDER []byte
	keyDER  []byte
	caDER   []byte
}

func newTestCA(t *testing.T, trustDomain string) *testCA {
	t.Helper()
	td := spiffeid.RequireTrustDomainFromString(trustDomain)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test ca"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
		URIs:                  []*url.URL{td.ID().URL()},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	jwtKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &testCA{td: td, cert: cert, key: key, jwtKey: jwtKey}
}

func (ca *testCA) x509SVID(t *testing.T, path string) testSVID {
	t.Helper()
	id := spiffeid.RequireFromPath(ca.td, path)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		URIs:                  []*url.URL{id.URL()},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return testSVID{id: id, certDER: der, keyDER: keyDER, caDER: ca.cert.Raw}
}

func (ca *testCA) jwtSVID(t *testing.T, path, audience string) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: ca.jwtKey}, nil)
	if err != nil {
		t.Fatal(err)
	}
	claims := jwt.Claims{
		Subject:  spiffeid.RequireFromPath(ca.td, path).String(),
		Audience: jwt.Audience{audience},
		Expiry:   jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	token, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}
