package ipc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rotateOrFatal is shorthand for tests that need a populated store
// without caring about the rotation error.
func rotateOrFatal(t *testing.T, s TokenStore) ControlToken {
	t.Helper()
	tok, err := s.Rotate()
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	return tok
}

func TestControlTokenString_RedactsValue(t *testing.T) {
	tok := ControlToken("supersecret")
	got := tok.String()
	if got != "<redacted>" {
		t.Errorf("String() = %q, want <redacted>", got)
	}
	// Sanity: Raw() must still return the real value.
	if tok.Raw() != "supersecret" {
		t.Errorf("Raw() = %q, want supersecret", tok.Raw())
	}
}

func TestGenerateControlToken_Unique(t *testing.T) {
	a, err := GenerateControlToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateControlToken()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("GenerateControlToken returned the same token twice (%s)", a.Raw())
	}
	if len(a.Raw()) != 64 {
		t.Errorf("token length = %d, want 64 (32 bytes hex)", len(a.Raw()))
	}
}

func TestMemoryTokenStore_RotateAndCurrent(t *testing.T) {
	var s MemoryTokenStore
	if !s.Current().IsZero() {
		t.Errorf("fresh store: Current() = %q, want zero", s.Current().Raw())
	}
	tok, err := s.Rotate()
	if err != nil {
		t.Fatal(err)
	}
	if s.Current() != tok {
		t.Errorf("Current() = %q, want %q", s.Current().Raw(), tok.Raw())
	}
	// Rotate again — Current should follow.
	tok2, err := s.Rotate()
	if err != nil {
		t.Fatal(err)
	}
	if tok2 == tok {
		t.Errorf("Rotate returned the same token twice")
	}
	if s.Current() != tok2 {
		t.Errorf("Current() = %q after second Rotate, want %q", s.Current().Raw(), tok2.Raw())
	}
}

func TestFileTokenStore_RotatePersistsToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "control-token")

	store, err := NewFileTokenStore(path, FileTokenOptions{Mode: 0o600, DirMode: 0o700})
	if err != nil {
		t.Fatalf("NewFileTokenStore: %v", err)
	}
	tok := rotateOrFatal(t, store)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := strings.TrimRight(string(raw), "\n")
	if got != tok.Raw() {
		t.Errorf("file content = %q, want %q", got, tok.Raw())
	}

	// File mode should match.
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Errorf("file mode = %o, want 0600", got)
	}
}

func TestFileTokenStore_RotateAtomicOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "control-token")

	store, err := NewFileTokenStore(path, FileTokenOptions{Mode: 0o600, DirMode: 0o700})
	if err != nil {
		t.Fatalf("NewFileTokenStore: %v", err)
	}
	first := rotateOrFatal(t, store)
	second := rotateOrFatal(t, store)
	if first == second {
		t.Fatal("second rotate returned the same token")
	}

	raw, _ := os.ReadFile(path)
	got := strings.TrimRight(string(raw), "\n")
	if got != second.Raw() {
		t.Errorf("file content = %q after 2 rotates, want %q", got, second.Raw())
	}

	// The temp-rename strategy must not leave .tmp leftovers behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

func TestAuthMiddleware(t *testing.T) {
	var store MemoryTokenStore
	tok := rotateOrFatal(t, &store)

	// Trivial handler the middleware wraps.
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	h := authMiddleware(&store, inner)

	cases := []struct {
		name       string
		header     string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "valid bearer token",
			header:     "Bearer " + tok.Raw(),
			wantStatus: http.StatusOK,
		},
		{
			name:       "no Authorization header",
			header:     "",
			wantStatus: http.StatusUnauthorized,
			wantCode:   "unauthorized",
		},
		{
			name:       "wrong scheme",
			header:     "Basic " + tok.Raw(),
			wantStatus: http.StatusUnauthorized,
			wantCode:   "unauthorized",
		},
		{
			name:       "wrong token",
			header:     "Bearer not-the-token",
			wantStatus: http.StatusUnauthorized,
			wantCode:   "unauthorized",
		},
		{
			name:       "empty token after Bearer",
			header:     "Bearer ",
			wantStatus: http.StatusUnauthorized,
			wantCode:   "unauthorized",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if tc.wantCode != "" {
				var body ErrorResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode error body: %v (raw=%q)", err, rec.Body.String())
				}
				if body.Code != tc.wantCode {
					t.Errorf("error.code = %q, want %q", body.Code, tc.wantCode)
				}
			}
		})
	}
}

func TestAuthMiddleware_ZeroTokenAlwaysRejects(t *testing.T) {
	// A store that has never been rotated should never accept any token,
	// even one that happens to equal the empty string.
	var store MemoryTokenStore
	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("inner handler should not be reached")
	})
	h := authMiddleware(&store, inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
