package rdp

import (
	"regexp"
	"testing"
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
