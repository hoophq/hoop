package rdp

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRDPDesktopSizeFromPreset(t *testing.T) {
	tests := []struct {
		name   string
		preset string
		want   rdpDesktopSize
		valid  bool
	}{
		{name: "omitted uses browser window", want: rdpDesktopSize{}, valid: true},
		{name: "fit uses browser window", preset: "fit", want: rdpDesktopSize{}, valid: true},
		{name: "HD", preset: "1280x720", want: rdpDesktopSize{width: 1280, height: 720}, valid: true},
		{name: "laptop", preset: "1366x768", want: rdpDesktopSize{width: 1366, height: 768}, valid: true},
		{name: "HD plus", preset: "1600x900", want: rdpDesktopSize{width: 1600, height: 900}, valid: true},
		{name: "full HD", preset: "1920x1080", want: rdpDesktopSize{width: 1920, height: 1080}, valid: true},
		{name: "QHD", preset: "2560x1440", want: rdpDesktopSize{width: 2560, height: 1440}, valid: true},
		{name: "4K", preset: "3840x2160", want: rdpDesktopSize{width: 3840, height: 2160}, valid: true},
		{name: "unknown preset", preset: "4096x4096", valid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, valid := rdpDesktopSizeFromPreset(tt.preset)
			if valid != tt.valid {
				t.Fatalf("valid = %v, want %v", valid, tt.valid)
			}
			if got != tt.want {
				t.Fatalf("desktop size = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestInvalidRDPDesktopSizeDimensions(t *testing.T) {
	tests := []struct {
		name       string
		preset     string
		wantWidth  int
		wantHeight int
		wantOK     bool
	}{
		{name: "unsupported dimensions", preset: "4096x4096", wantWidth: 4096, wantHeight: 4096, wantOK: true},
		{name: "unsupported aspect ratio", preset: "1280x800", wantWidth: 1280, wantHeight: 800, wantOK: true},
		{name: "credential-like value", preset: "xagt-secret", wantOK: false},
		{name: "multiple separators", preset: "1280x720x32", wantOK: false},
		{name: "oversized component", preset: "12345x720", wantOK: false},
		{name: "signed component", preset: "+1x2", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			width, height, ok := invalidRDPDesktopSizeDimensions(tt.preset)
			if ok != tt.wantOK || width != tt.wantWidth || height != tt.wantHeight {
				t.Fatalf(
					"dimensions = (%d, %d, %v), want (%d, %d, %v)",
					width, height, ok, tt.wantWidth, tt.wantHeight, tt.wantOK,
				)
			}
		})
	}
}

func TestHandleClientRejectsInvalidDesktopSize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []string{"4096x4096", "xagt-secret"}

	for _, desktopSize := range tests {
		t.Run(desktopSize, func(t *testing.T) {
			form := url.Values{
				"credential":   {"test-credential"},
				"desktop_size": {desktopSize},
			}
			request := httptest.NewRequest(
				http.MethodPost,
				"/client",
				strings.NewReader(form.Encode()),
			)
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			ctx.Request = request

			(&IronRDPGateway{}).handleClient(ctx)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestRenderWebClientTemplatePassesDesktopSize(t *testing.T) {
	got := renderWebClientTemplate(
		"RDP Connection",
		"credential",
		rdpDesktopSize{width: 1280, height: 720},
	)

	initializeCall := regexp.MustCompile(
		`initializeApp\("credential",\s+1280\s*,\s+720\s*\);`,
	)
	if !initializeCall.MatchString(got) {
		t.Fatalf("rendered client does not initialize the requested desktop size: %s", got)
	}
}
