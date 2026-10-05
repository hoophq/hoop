package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/analytics"
)

// fakeJWT builds an unsigned JWT with the given claims. The sidecar never
// verifies one, so a signature would only be noise here.
func fakeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + enc(payload) + ".sig"
}

// k8sJWT is a projected service account token for one workspace.
func k8sJWT(t *testing.T, sub string, exp time.Time) string {
	return fakeJWT(t, map[string]any{
		"iss": "https://container.googleapis.com/v1/projects/p/locations/eu/clusters/c",
		"sub": sub, "aud": []string{"https://cp.example.com"}, "exp": exp.Unix(),
	})
}

// credCall is which credential headers one request carried.
type credCall struct {
	identity string
	token    string
}

// identityPlane answers every request with status and body and records the
// credential headers, so a test asserts what the plane saw.
func identityPlane(t *testing.T, status int, body string) (*httptest.Server, func() []credCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []credCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, credCall{
			identity: r.Header.Get(SidecarIdentityHeader),
			token:    r.Header.Get(sidecarTokenHeader),
		})
		mu.Unlock()
		if status == http.StatusOK {
			w.Header().Set(LicenseManagedHeader, "true")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []credCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]credCall(nil), calls...)
	}
}

// clearCredentialEnv unsets every credential source, so a developer shell
// holding one cannot decide a test.
func clearCredentialEnv(t *testing.T) {
	t.Helper()
	t.Setenv(SidecarTokenEnv, "")
	t.Setenv(SidecarIdentityTokenFileEnv, "")
	t.Setenv(SidecarIdentityGCPEnv, "")
	t.Setenv(SidecarIdentityAudienceEnv, "")
	t.Setenv(gcpMetadataHostEnv, "")
}

func writeTokenFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The kubelet rotates a projected token in place. A process that read it
// once would authenticate for an hour and then fail every heartbeat, so the
// second handshake must carry what the file holds then.
func TestATokenFileIsReReadOnEveryHandshake(t *testing.T) {
	clearCredentialEnv(t)
	srv, calls := identityPlane(t, http.StatusOK, planeConfig)
	path := filepath.Join(t.TempDir(), "token")
	first := k8sJWT(t, "system:serviceaccount:ws-1:hoop-sidecar", time.Now().Add(time.Hour))
	writeTokenFile(t, path, first)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarIdentityTokenFileEnv, path)

	cfg, _, err := SetupWith("", nil, nil)
	if err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	second := k8sJWT(t, "system:serviceaccount:ws-1:hoop-sidecar", time.Now().Add(2*time.Hour))
	// Trailing whitespace is what an editor or `echo` leaves behind.
	writeTokenFile(t, path, second+"\n")
	if _, err := fetchControlPlaneConfig(cfg.cp.url, cfg.cp.cred, handshakeRequest{Version: Version}); err != nil {
		t.Fatalf("second handshake: %v", err)
	}

	got := calls()
	if len(got) != 2 {
		t.Fatalf("handshakes = %d, want 2", len(got))
	}
	for i, want := range []string{first, second} {
		if got[i].identity != want {
			t.Errorf("handshake %d presented identity %q, want %q", i, got[i].identity, want)
		}
		if got[i].token != "" {
			t.Errorf("handshake %d also presented a token %q", i, got[i].token)
		}
	}
}

func TestAnUnusableTokenFileStopsStartup(t *testing.T) {
	for _, tc := range []struct {
		name, content, want string
	}{
		{"empty", " \n", "empty"},
		{"oversized", strings.Repeat("a", maxIdentityToken+1), "more than"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearCredentialEnv(t)
			srv, calls := identityPlane(t, http.StatusOK, planeConfig)
			path := filepath.Join(t.TempDir(), "token")
			writeTokenFile(t, path, tc.content)
			t.Setenv(ControlPlaneURLEnv, srv.URL)
			t.Setenv(SidecarIdentityTokenFileEnv, path)

			_, _, err := SetupWith("", nil, nil)
			if err == nil {
				t.Fatal("an unusable token file was accepted")
			}
			if !strings.Contains(err.Error(), SidecarIdentityTokenFileEnv) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the error does not name the source and the fault %q: %v", tc.want, err)
			}
			if n := len(calls()); n != 0 {
				t.Errorf("the plane was contacted %d time(s)", n)
			}
		})
	}
}

// Precedence between two credentials would decide which sidecar row this
// process becomes, so two set is an error naming both.
func TestTwoCredentialSourcesStopStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	for _, tc := range []struct {
		name string
		flag string
		env  map[string]string
		want []string
	}{
		{"token env and file", "", map[string]string{SidecarTokenEnv: "hsc_x", SidecarIdentityTokenFileEnv: path},
			[]string{SidecarTokenEnv, SidecarIdentityTokenFileEnv}},
		{"token flag and gcp", "hsc_x", map[string]string{SidecarIdentityGCPEnv: "true"},
			[]string{"token flag", SidecarIdentityGCPEnv}},
		{"file and gcp", "", map[string]string{SidecarIdentityTokenFileEnv: path, SidecarIdentityGCPEnv: "1"},
			[]string{SidecarIdentityTokenFileEnv, SidecarIdentityGCPEnv}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearCredentialEnv(t)
			srv, calls := identityPlane(t, http.StatusOK, planeConfig)
			t.Setenv(ControlPlaneURLEnv, srv.URL)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			_, _, err := SetupWith("", nil, nil, WithControlPlaneToken(tc.flag))
			if err == nil {
				t.Fatal("two credential sources were accepted")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the error does not name %q: %v", want, err)
				}
			}
			if n := len(calls()); n != 0 {
				t.Errorf("the plane was contacted %d time(s)", n)
			}
		})
	}
}

// HOOP_SIDECAR_IDENTITY_GCP=false is no source at all, so a token beside
// it is the one credential.
func TestAFalseGCPSwitchIsNotASource(t *testing.T) {
	clearCredentialEnv(t)
	srv, calls := identityPlane(t, http.StatusOK, planeConfig)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarTokenEnv, "hsc_env")
	t.Setenv(SidecarIdentityGCPEnv, "false")

	if _, _, err := SetupWith("", nil, nil); err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if got := calls(); len(got) != 1 || got[0].token != "hsc_env" || got[0].identity != "" {
		t.Errorf("calls = %+v, want one token handshake", got)
	}
}

func TestAnIdentitySourceWithoutAControlPlaneStopsStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	writeTokenFile(t, path, k8sJWT(t, "s", time.Now().Add(time.Hour)))
	for _, tc := range []struct{ env, value string }{
		{SidecarIdentityTokenFileEnv, path},
		{SidecarIdentityGCPEnv, "true"},
	} {
		t.Run(tc.env, func(t *testing.T) {
			clearCredentialEnv(t)
			t.Setenv(ControlPlaneURLEnv, "")
			t.Setenv(tc.env, tc.value)

			_, _, err := SetupWith(writeConfig(t, minimalConfig+`}`), nil, nil)
			if err == nil {
				t.Fatal("an orphan credential was accepted")
			}
			if !strings.Contains(err.Error(), tc.env) {
				t.Errorf("the error does not name the source: %v", err)
			}
		})
	}
}

func TestAControlPlaneWithoutACredentialNamesEverySource(t *testing.T) {
	clearCredentialEnv(t)
	srv, _ := identityPlane(t, http.StatusOK, planeConfig)
	t.Setenv(ControlPlaneURLEnv, srv.URL)

	_, _, err := SetupWith("", nil, nil)
	if err == nil {
		t.Fatal("a control plane with no credential was accepted")
	}
	for _, want := range []string{SidecarTokenEnv, SidecarIdentityTokenFileEnv, SidecarIdentityGCPEnv} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %q: %v", want, err)
		}
	}
}

func TestAGarbageGCPSwitchStopsStartup(t *testing.T) {
	clearCredentialEnv(t)
	srv, calls := identityPlane(t, http.StatusOK, planeConfig)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarIdentityGCPEnv, "yes please")

	_, _, err := SetupWith("", nil, nil)
	if err == nil {
		t.Fatal("a garbage boolean was accepted")
	}
	if !strings.Contains(err.Error(), SidecarIdentityGCPEnv) || !strings.Contains(err.Error(), "yes please") {
		t.Errorf("the error does not name the source and the value: %v", err)
	}
	if n := len(calls()); n != 0 {
		t.Errorf("the plane was contacted %d time(s)", n)
	}
}

// metadataCall is one request the fake metadata server saw.
type metadataCall struct {
	flavor, audience, format, path string
}

// metadataServer is a fake GCE metadata server minting Google ID tokens that
// expire after ttl. It is reached through GCE_METADATA_HOST, the variable
// the Google client libraries honor.
func metadataServer(t *testing.T, ttl time.Duration) func() []metadataCall {
	t.Helper()
	var mu sync.Mutex
	var calls []metadataCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, metadataCall{
			flavor:   r.Header.Get("Metadata-Flavor"),
			audience: r.URL.Query().Get("audience"),
			format:   r.URL.Query().Get("format"),
			path:     r.URL.Path,
		})
		n := len(calls)
		mu.Unlock()
		if r.Header.Get("Metadata-Flavor") != "Google" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, fakeJWT(t, map[string]any{
			"iss": "https://accounts.google.com", "sub": "1234",
			"email": "sidecar@p.iam.gserviceaccount.com", "email_verified": true,
			"aud": r.URL.Query().Get("audience"), "exp": time.Now().Add(ttl).Unix(), "n": n,
		}))
	}))
	t.Cleanup(srv.Close)
	t.Setenv(gcpMetadataHostEnv, srv.Listener.Addr().String())
	return func() []metadataCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]metadataCall(nil), calls...)
	}
}

func TestGCPIdentityAsksTheMetadataServerForThePlaneAudience(t *testing.T) {
	clearCredentialEnv(t)
	meta := metadataServer(t, time.Hour)
	srv, calls := identityPlane(t, http.StatusOK, planeConfig)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarIdentityGCPEnv, "true")

	cfg, _, err := SetupWith("", nil, nil)
	if err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	if _, err := fetchControlPlaneConfig(cfg.cp.url, cfg.cp.cred, handshakeRequest{Version: Version}); err != nil {
		t.Fatalf("second handshake: %v", err)
	}

	m := meta()
	if len(m) != 1 {
		t.Fatalf("metadata calls = %d, want 1: a token an hour from exp is cached", len(m))
	}
	if m[0].flavor != "Google" || m[0].audience != srv.URL || m[0].format != "full" ||
		m[0].path != "/computeMetadata/v1/instance/service-accounts/default/identity" {
		t.Errorf("metadata request = %+v", m[0])
	}
	got := calls()
	if len(got) != 2 || got[0].identity == "" || got[0].identity != got[1].identity || got[0].token != "" {
		t.Errorf("plane calls = %+v, want two identity handshakes with the cached token", got)
	}
}

// A plane shared by several organizations maps an (issuer, audience) pair
// to one organization, so each organization's sidecars name their own
// audience instead of the plane URL.
func TestGCPIdentityAsksForTheConfiguredAudience(t *testing.T) {
	clearCredentialEnv(t)
	meta := metadataServer(t, time.Hour)
	srv, calls := identityPlane(t, http.StatusOK, planeConfig)
	t.Setenv(ControlPlaneURLEnv, srv.URL)
	t.Setenv(SidecarIdentityGCPEnv, "true")
	t.Setenv(SidecarIdentityAudienceEnv, "hoop-org-a")

	if _, _, err := SetupWith("", nil, nil); err != nil {
		t.Fatalf("SetupWith: %v", err)
	}
	m := meta()
	if len(m) != 1 || m[0].audience != "hoop-org-a" {
		t.Errorf("metadata requests = %+v, want one for audience %q", m, "hoop-org-a")
	}
	if got := calls(); len(got) != 1 || got[0].identity == "" {
		t.Errorf("plane calls = %+v, want one identity handshake", got)
	}
}

// The audience does nothing for a token, and a projected token's audience
// is fixed where it is minted, so setting it beside either is an error
// naming it before the plane is contacted.
func TestAnIdentityAudienceWithoutGCPStopsStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	writeTokenFile(t, path, k8sJWT(t, "s", time.Now().Add(time.Hour)))
	for _, tc := range []struct {
		name string
		flag string
		env  map[string]string
		want string
	}{
		{"token flag", "hsc_x", nil, "token flag"},
		{"token env", "", map[string]string{SidecarTokenEnv: "hsc_x"}, SidecarTokenEnv},
		{"token file", "", map[string]string{SidecarIdentityTokenFileEnv: path}, SidecarIdentityTokenFileEnv},
		{"gcp false", "", map[string]string{SidecarTokenEnv: "hsc_x", SidecarIdentityGCPEnv: "false"}, SidecarTokenEnv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearCredentialEnv(t)
			srv, calls := identityPlane(t, http.StatusOK, planeConfig)
			t.Setenv(ControlPlaneURLEnv, srv.URL)
			t.Setenv(SidecarIdentityAudienceEnv, "hoop-org-a")
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			_, _, err := SetupWith("", nil, nil, WithControlPlaneToken(tc.flag))
			if err == nil {
				t.Fatal("an audience beside a non-GCP credential was accepted")
			}
			for _, want := range []string{SidecarIdentityAudienceEnv, tc.want} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the error does not name %q: %v", want, err)
				}
			}
			if n := len(calls()); n != 0 {
				t.Errorf("the plane was contacted %d time(s)", n)
			}
		})
	}
}

// A token within five minutes of exp could expire between this check and
// the plane's verification, so it is replaced rather than presented.
func TestGCPIdentityRefetchesATokenNearItsExpiry(t *testing.T) {
	clearCredentialEnv(t)
	meta := metadataServer(t, 4*time.Minute)
	cred := &gcpCredential{audience: "https://cp.example.com"}

	_, first, err := cred.present(context.Background())
	if err != nil {
		t.Fatalf("present: %v", err)
	}
	_, second, err := cred.present(context.Background())
	if err != nil {
		t.Fatalf("present: %v", err)
	}
	if n := len(meta()); n != 2 {
		t.Errorf("metadata calls = %d, want 2", n)
	}
	if first == second {
		t.Error("the near-expiry token was presented again")
	}
}

// The unexported host var is the default when GCE_METADATA_HOST is unset.
func TestGCPIdentityDefaultsToTheMetadataHost(t *testing.T) {
	clearCredentialEnv(t)
	meta := metadataServer(t, time.Hour)
	host := os.Getenv(gcpMetadataHostEnv)
	t.Setenv(gcpMetadataHostEnv, "")
	saved := gcpMetadataHost
	gcpMetadataHost = host
	t.Cleanup(func() { gcpMetadataHost = saved })

	if _, _, err := (&gcpCredential{audience: "a"}).present(context.Background()); err != nil {
		t.Fatalf("present: %v", err)
	}
	if n := len(meta()); n != 1 {
		t.Errorf("metadata calls = %d, want 1", n)
	}
}

// A rejected identity names the subject that has to match. The plane's own
// reason is the message when it gives one, so "deleted" never points the
// operator at the allowlist; a vague 401, which is also what a plane
// predating identities answers, gets the allowlist hint.
func TestARejectedIdentityNamesTheSubjectAndThePlanesReason(t *testing.T) {
	cases := []struct {
		name, body string
		want, not  []string
	}{
		{
			name: "specific reason",
			body: `{"message":"this sidecar was deleted in the control plane"}`,
			want: []string{"this sidecar was deleted in the control plane"},
			not:  []string{"allowlist", "without service account support"},
		},
		{
			name: "vague answer from an older plane",
			body: `{"message":"access denied"}`,
			want: []string{"access denied", "allowlist", "without service account support"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearCredentialEnv(t)
			srv, _ := identityPlane(t, http.StatusUnauthorized, tc.body)
			path := filepath.Join(t.TempDir(), "token")
			writeTokenFile(t, path, k8sJWT(t, "system:serviceaccount:ws-9:hoop-sidecar", time.Now().Add(time.Hour)))
			t.Setenv(ControlPlaneURLEnv, srv.URL)
			t.Setenv(SidecarIdentityTokenFileEnv, path)

			_, _, err := SetupWith("", nil, nil)
			if err == nil {
				t.Fatal("a rejected identity started")
			}
			for _, want := range append([]string{"service account identity", "system:serviceaccount:ws-9:hoop-sidecar"}, tc.want...) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the error lacks %q: %v", want, err)
				}
			}
			for _, not := range tc.not {
				if strings.Contains(err.Error(), not) {
					t.Errorf("the error carries %q: %v", not, err)
				}
			}
		})
	}
}

func TestReviewRequestsPresentTheIdentity(t *testing.T) {
	clearCredentialEnv(t)
	srv, calls := identityPlane(t, http.StatusCreated, `{"forward":false,"review":{"id":"9f97","status":"PENDING"}}`)
	path := filepath.Join(t.TempDir(), "token")
	jwt := k8sJWT(t, "system:serviceaccount:ws-1:hoop-sidecar", time.Now().Add(time.Hour))
	writeTokenFile(t, path, jwt)
	cp := &controlPlane{url: srv.URL, cred: tokenFileCredential{path: path}}

	if _, err := cp.reviewer("payments", "approvers").File(context.Background(), "DELETE FROM t"); err != nil {
		t.Fatalf("File: %v", err)
	}
	if got := calls(); len(got) != 1 || got[0].identity != jwt || got[0].token != "" {
		t.Errorf("review calls = %+v, want one carrying only the identity", got)
	}
}

func TestARejectedIdentityOnAReviewNamesTheSubject(t *testing.T) {
	clearCredentialEnv(t)
	srv, _ := identityPlane(t, http.StatusUnauthorized, `{"message":"subject not allowed"}`)
	path := filepath.Join(t.TempDir(), "token")
	writeTokenFile(t, path, k8sJWT(t, "system:serviceaccount:ws-2:hoop-sidecar", time.Now().Add(time.Hour)))
	cp := &controlPlane{url: srv.URL, cred: tokenFileCredential{path: path}}

	_, err := cp.reviewer("payments", "approvers").File(context.Background(), "DELETE FROM t")
	if err == nil {
		t.Fatal("a rejected review was accepted")
	}
	for _, want := range []string{"system:serviceaccount:ws-2:hoop-sidecar", "subject not allowed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error lacks %q: %v", want, err)
		}
	}
}

// Segment bills per distinct id. An identity token rotates hourly, so the id
// follows the plane, issuer and subject, never the token bytes.
func TestAnIdentitySidecarIDSurvivesTokenRotation(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv(analytics.IDEnvVar, "")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	path := filepath.Join(t.TempDir(), "token")
	idFor := func(jwt string) string {
		writeTokenFile(t, path, jwt)
		cfg := &Config{cp: &controlPlane{url: "https://cp.example.com", cred: tokenFileCredential{path: path}}}
		tel := newTelemetry(cfg, log)
		defer tel.close()
		return tel.client.ID()
	}
	first := k8sJWT(t, "system:serviceaccount:ws-1:hoop-sidecar", time.Now().Add(time.Hour))
	rotated := k8sJWT(t, "system:serviceaccount:ws-1:hoop-sidecar", time.Now().Add(2*time.Hour))
	other := k8sJWT(t, "system:serviceaccount:ws-2:hoop-sidecar", time.Now().Add(time.Hour))

	id := idFor(first)
	if got := idFor(rotated); got != id {
		t.Errorf("a rotated token minted a new id: %q != %q", got, id)
	}
	if id == analytics.IDFromToken(first) {
		t.Error("the id is the hash of the rotating token")
	}
	if idFor(other) == id {
		t.Error("two subjects share one id")
	}
}
