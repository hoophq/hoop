package telemetry

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fatalChildEnv = "HOOP_TELEMETRY_FATAL_CHILD"

// TestFatalExitChild is the gateway the test below starts: export on, one
// API request recorded, then a fatal startup error. It does nothing in a
// normal run.
func TestFatalExitChild(t *testing.T) {
	host := os.Getenv(fatalChildEnv)
	if host == "" {
		t.Skip("run as a child of TestAFatalExitFlushesTheExport only")
	}
	// An hour: only the flush on exit can send the request below.
	cfg := &appconfig.OTLPExport{Protocol: appconfig.OTLPProtocolHTTP, Host: host, Insecure: true,
		Timeout: time.Second, Interval: time.Hour}
	require.NoError(t, Start(cfg, "test", nil))
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(HTTPServer())
	r.GET("/api/x", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/x", nil))
	log.Fatal("failed to start http server")
	t.Fatal("Fatal returned")
}

// A fatal startup error after Start exits through log.Fatal, not a signal.
// The metrics recorded until then still reach the collector.
func TestAFatalExitFlushesTheExport(t *testing.T) {
	got := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- r.URL.Path + " " + string(body)
	}))
	t.Cleanup(srv.Close)

	// A watchdog, so a hang fails here instead of at the package timeout.
	const watchdog = 30 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), watchdog)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFatalExitChild$")
	cmd.Env = append(os.Environ(), fatalChildEnv+"="+strings.TrimPrefix(srv.URL, "http://"), "LOG_ENCODING=json")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	require.NoError(t, ctx.Err(), "the watchdog stopped the child after %s; output:\n%s", watchdog, out.String())
	var exitErr *exec.ExitError
	require.True(t, errors.As(err, &exitErr), "want an exit error, got %v; output:\n%s", err, out.String())
	assert.Equal(t, 1, exitErr.ExitCode(), out.String())

	select {
	case req := <-got:
		assert.True(t, strings.HasPrefix(req, "/v1/metrics "), "got a request to %q", req)
		assert.Contains(t, req, "http.server.request.duration")
	default:
		t.Fatalf("the collector got nothing before the exit; output:\n%s", out.String())
	}
}
