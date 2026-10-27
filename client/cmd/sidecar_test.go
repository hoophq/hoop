package cmd

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	clientconfig "github.com/hoophq/hoop/client/config"
)

// The key is printed once; no hint repeats it inside a command.
func TestSidecarCredentialHintOmitsTheKey(t *testing.T) {
	const key = "hsk_secret_key_value"
	for _, protocol := range []string{"postgres", "mysql", "mongodb", "mssql", "clickhouse", "oracle", "http"} {
		hint := sidecarCredentialHint(sidecarCredentialResponse{Protocol: protocol, User: "alice", Key: key})
		if hint == "" {
			t.Errorf("%s: no hint", protocol)
			continue
		}
		if strings.Contains(hint, key) {
			t.Errorf("%s: hint leaks the key: %s", protocol, hint)
		}
		if protocol != "http" && !strings.Contains(hint, "alice") {
			t.Errorf("%s: hint lost the user: %s", protocol, hint)
		}
	}
}

type shortWriter struct{ n int }

func (w shortWriter) Write(p []byte) (int, error) {
	if len(p) > w.n {
		return w.n, nil
	}
	return len(p), nil
}

type failWriter struct{ err error }

func (w failWriter) Write([]byte) (int, error) { return 0, w.err }

func TestWriteSidecarCredentialJSONReportsWriteFailures(t *testing.T) {
	body := []byte(`{"key":"k"}`)
	if err := writeSidecarCredentialJSON(shortWriter{n: 3}, body); !errors.Is(err, io.ErrShortWrite) {
		t.Errorf("short write: got %v, want %v", err, io.ErrShortWrite)
	}
	boom := errors.New("boom")
	if err := writeSidecarCredentialJSON(failWriter{err: boom}, body); !errors.Is(err, boom) {
		t.Errorf("failed write: got %v, want %v", err, boom)
	}
	var out strings.Builder
	if err := writeSidecarCredentialJSON(&out, body); err != nil || out.String() != string(body)+"\n" {
		t.Errorf("full write: got %q, %v", out.String(), err)
	}
}

// A body cut short by the server is an error, not an empty credential.
func TestRequestSidecarCredentialReportsTruncatedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"key":`))
	}))
	defer srv.Close()
	config := &clientconfig.Config{ApiURL: srv.URL, Token: "tok"}
	body, err := requestSidecarCredential(config, "edge", "appdb", 1)
	if err == nil || !strings.Contains(err.Error(), "failed reading response") {
		t.Fatalf("got body %q, err %v; want a read error", body, err)
	}
}
