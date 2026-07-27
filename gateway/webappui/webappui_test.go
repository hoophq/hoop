package webappui

import (
	"fmt"

	"github.com/hoophq/hoop/common/version"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

func TestTransformIndex(t *testing.T) {
	raw := []byte(`<script src="http://localhost:8280/js/app.js?version=unknown"></script>`)
	out := string(transformIndex(raw, "https://hoop.corp/prefix"))
	if strings.Contains(out, "localhost:8280") {
		t.Fatalf("assets URL not replaced: %s", out)
	}
	// The version placeholder is stamped with the running version (which is
	// literally "unknown" on dev builds without ldflags, making the
	// replacement a no-op by value).
	expected := fmt.Sprintf(`https://hoop.corp/prefix/js/app.js?version=%v`, version.Get().Version)
	if !strings.Contains(out, expected) {
		t.Fatalf("expected %q in transform output: %s", expected, out)
	}
}

func TestTransformAppJs(t *testing.T) {
	raw := []byte(`api="http://localhost:8009";assets="http://localhost:8280";route="/hardcoded-runtime-prefix/login"`)
	out := string(transformAppJs(raw, "https://hoop.corp/prefix", "/prefix"))
	for _, leftover := range []string{"localhost:8009", "localhost:8280", "hardcoded-runtime-prefix"} {
		if strings.Contains(out, leftover) {
			t.Fatalf("placeholder %q not replaced: %s", leftover, out)
		}
	}
	if !strings.Contains(out, `route="/prefix/login"`) {
		t.Fatalf("base route prefix not applied: %s", out)
	}
}

func TestLoadAndStaticFS(t *testing.T) {
	uiFS := fstest.MapFS{
		"index.html":      {Data: []byte(`<script src="http://localhost:8280/js/app.js?version=unknown"></script>`)},
		"js/app.js":       {Data: []byte(`fetch("http://localhost:8009/api")`)},
		"css/site.css":    {Data: []byte(`body{}`)},
		"images/logo.svg": {Data: []byte(`<svg/>`)},
	}
	ui, err := load(uiFS, "test")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !ui.HasAppJs() {
		t.Fatal("expected app.js to be detected")
	}

	fs := ui.FileSystem()
	// Transformed files must be reported absent so their routes serve them.
	for _, excluded := range []string{"/index.html", "/js/app.js"} {
		if fs.Exists("/", excluded) {
			t.Fatalf("%s must be excluded from static serving", excluded)
		}
	}
	// Regular assets must be served; directories must not.
	if !fs.Exists("/", "/css/site.css") || !fs.Exists("/", "/images/logo.svg") {
		t.Fatal("expected static assets to exist")
	}
	if fs.Exists("/", "/css") {
		t.Fatal("directories must not be served")
	}
	if fs.Exists("/", "/missing.txt") {
		t.Fatal("missing file reported as existing")
	}
}

func TestLoadWithoutAppJs(t *testing.T) {
	uiFS := fstest.MapFS{
		"index.html": {Data: []byte(`<html></html>`)},
	}
	ui, err := load(uiFS, "test")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if ui.HasAppJs() {
		t.Fatal("app.js must be optional")
	}
}

func TestEmbeddedFSEmptyPlaceholder(t *testing.T) {
	// The repository ships only the .gitkeep placeholder: the embedded
	// source must report itself unavailable, not serve an empty UI.
	if embeddedFS() != nil {
		t.Fatal("embeddedFS must be nil when no webapp build is staged")
	}
}

// statusRecorder is a minimal http.ResponseWriter for WriteIndex/WriteAppJs.
type statusRecorder struct {
	header http.Header
	status int
	body   []byte
}

func (r *statusRecorder) Header() http.Header { return r.header }
func (r *statusRecorder) WriteHeader(s int)   { r.status = s }
func (r *statusRecorder) Write(p []byte) (int, error) {
	r.body = append(r.body, p...)
	return len(p), nil
}

func TestWriteIndexAndAppJs(t *testing.T) {
	uiFS := fstest.MapFS{
		"index.html": {Data: []byte(`<html>x</html>`)},
		"js/app.js":  {Data: []byte(`js`)},
	}
	ui, err := load(uiFS, "test")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	rec := &statusRecorder{header: http.Header{}}
	ui.WriteIndex(rec, http.StatusOK)
	if rec.status != http.StatusOK || !strings.Contains(rec.header.Get("Content-Type"), "text/html") {
		t.Fatalf("unexpected index response: status=%d headers=%v", rec.status, rec.header)
	}
	rec = &statusRecorder{header: http.Header{}}
	ui.WriteAppJs(rec)
	if rec.status != http.StatusOK || !strings.Contains(rec.header.Get("Content-Type"), "javascript") {
		t.Fatalf("unexpected app.js response: status=%d headers=%v", rec.status, rec.header)
	}
}
