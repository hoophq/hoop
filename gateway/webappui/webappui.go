// Package webappui serves the gateway web UI. The UI assets come from one
// of three sources, resolved in order:
//
//  1. STATIC_UI_PATH — an explicitly configured directory on disk
//  2. the default directory (/app/ui/public) when it exists — container
//     images and the dev workflow place the webapp build there
//  3. the build embedded into the binary (gateway/webappui/staticui,
//     populated by `make embed-webapp` at release build time) — this is
//     what makes the standalone binary fully self-contained
//
// Two files reference the gateway URL and are shipped with hardcoded
// placeholders: index.html and js/app.js. They are transformed in memory at
// startup and served from memory — the assets on disk (or in the binary)
// are never mutated. This replaces the old gateway/webappjs behavior of
// rewriting the files in place (which broke on read-only filesystems and
// required .origin backup copies).
package webappui

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/version"
	"github.com/hoophq/hoop/gateway/appconfig"
)

const (
	// defaultStaticUiPath is where container images and the dev workflow
	// place the webapp build.
	defaultStaticUiPath = "/app/ui/public"

	// Placeholders baked into the webapp build, replaced at startup.
	hardcodedWebappApiURL       = "http://localhost:8009"
	hardcodedWebappAssetsURL    = "http://localhost:8280"
	hardcodedWebappAppJsVersion = "?version=unknown"
	hardcodedWebappBaseRoute    = "/hardcoded-runtime-prefix"

	indexFileName = "index.html"
	appJsFileName = "js/app.js"
)

// UI serves the resolved web UI: static assets through FileSystem (which
// excludes the transformed files) and the in-memory transformed entrypoints
// through ServeIndex/ServeAppJs.
type UI struct {
	// Source describes where the assets come from, for logging.
	Source string

	fileSystem *staticFS
	index      []byte
	appJs      []byte
}

// Resolve locates the UI assets and prepares the in-memory transforms.
// It returns nil (without error) when no UI source is available — the
// gateway then runs API-only.
func Resolve() (*UI, error) {
	uiFS, source := resolveSource()
	if uiFS == nil {
		return nil, nil
	}
	ui, err := load(uiFS, source)
	if err != nil {
		return nil, fmt.Errorf("failed loading web UI from %s: %w", source, err)
	}
	return ui, nil
}

func resolveSource() (fs.FS, string) {
	if explicitPath := appconfig.Get().WebappStaticUiPath(); explicitPath != "" {
		return os.DirFS(explicitPath), fmt.Sprintf("STATIC_UI_PATH (%s)", explicitPath)
	}
	if _, err := os.Stat(defaultStaticUiPath + "/" + indexFileName); err == nil {
		return os.DirFS(defaultStaticUiPath), defaultStaticUiPath
	}
	if embedded := embeddedFS(); embedded != nil {
		return embedded, "embedded"
	}
	return nil, ""
}

func load(uiFS fs.FS, source string) (*UI, error) {
	apiURL := appconfig.Get().ApiURL() + appconfig.Get().ApiURLPath()
	baseRoutePrefix := appconfig.Get().ApiURLPath()
	if baseRoutePrefix == "/" {
		baseRoutePrefix = ""
	}

	indexBytes, err := fs.ReadFile(uiFS, indexFileName)
	if err != nil {
		return nil, fmt.Errorf("failed reading %s: %w", indexFileName, err)
	}
	ui := &UI{
		Source: source,
		index:  transformIndex(indexBytes, apiURL),
	}

	// js/app.js may be absent (e.g. a pure React build); it is transformed
	// only when present.
	transformed := []string{indexFileName}
	if appJsBytes, err := fs.ReadFile(uiFS, appJsFileName); err == nil {
		ui.appJs = transformAppJs(appJsBytes, apiURL, baseRoutePrefix)
		transformed = append(transformed, appJsFileName)
	}

	ui.fileSystem = &staticFS{httpFS: http.FS(uiFS), excluded: transformed}
	return ui, nil
}

// transformIndex points the asset URLs at the gateway and stamps the
// running version on the bundle reference (cache busting).
func transformIndex(raw []byte, apiURL string) []byte {
	raw = bytes.ReplaceAll(raw, []byte(hardcodedWebappAssetsURL), []byte(apiURL))
	appVersion := fmt.Sprintf("?version=%v", version.Get().Version)
	return bytes.ReplaceAll(raw, []byte(hardcodedWebappAppJsVersion), []byte(appVersion))
}

// transformAppJs points the API/asset URLs at the gateway and injects the
// base route prefix.
func transformAppJs(raw []byte, apiURL, baseRoutePrefix string) []byte {
	raw = bytes.ReplaceAll(raw, []byte(hardcodedWebappApiURL), []byte(apiURL))
	raw = bytes.ReplaceAll(raw, []byte(hardcodedWebappAssetsURL), []byte(apiURL))
	return bytes.ReplaceAll(raw, []byte(hardcodedWebappBaseRoute), []byte(baseRoutePrefix))
}

// FileSystem implements gin-contrib/static ServeFileSystem for the static
// assets, excluding the in-memory transformed files.
func (u *UI) FileSystem() *staticFS { return u.fileSystem }

// HasAppJs reports whether the build ships a js/app.js entrypoint.
func (u *UI) HasAppJs() bool { return u.appJs != nil }

// WriteIndex writes the transformed index.html to w.
func (u *UI) WriteIndex(w http.ResponseWriter, statusCode int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(statusCode)
	_, _ = w.Write(u.index)
}

// WriteAppJs writes the transformed js/app.js to w.
func (u *UI) WriteAppJs(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(u.appJs)
}

// staticFS adapts an fs.FS to gin-contrib/static's ServeFileSystem,
// reporting the transformed files as absent so their routes take over.
type staticFS struct {
	httpFS   http.FileSystem
	excluded []string
}

func (f *staticFS) Open(name string) (http.File, error) { return f.httpFS.Open(name) }

func (f *staticFS) Exists(prefix string, filepath string) bool {
	name := strings.TrimPrefix(filepath, prefix)
	name = strings.TrimPrefix(name, "/")
	if name == "" {
		return false
	}
	for _, excluded := range f.excluded {
		if name == excluded {
			return false
		}
	}
	file, err := f.httpFS.Open(name)
	if err != nil {
		return false
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || stat.IsDir() {
		return false
	}
	return true
}

// LogSource logs where the web UI is served from, or a warning when the
// gateway runs API-only.
func LogSource(ui *UI) {
	if ui == nil {
		log.Warn("no web UI assets found (no STATIC_UI_PATH, default path or embedded build), running API-only")
		return
	}
	log.Infof("serving web UI from %v", ui.Source)
}
