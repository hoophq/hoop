package upgrade

import "testing"

func TestPlatformFor(t *testing.T) {
	cases := []struct {
		goos, goarch string
		want         string
		wantErr      bool
	}{
		{"darwin", "arm64", "Darwin_arm64", false},
		{"darwin", "amd64", "Darwin_x86_64", false},
		{"linux", "arm64", "Linux_arm64", false},
		{"linux", "amd64", "Linux_x86_64", false},
		{"windows", "amd64", "Windows_x86_64", false},
		{"plan9", "amd64", "", true},
		{"darwin", "mips", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.goos+"_"+tc.goarch, func(t *testing.T) {
			got, err := platformFor(tc.goos, tc.goarch)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.String() != tc.want {
				t.Fatalf("want %q, got %q", tc.want, got.String())
			}
		})
	}
}

func TestArtifactURL(t *testing.T) {
	p := Platform{OS: "Darwin", Arch: "arm64"}
	got := ArtifactURL("1.72.0", p)
	want := "https://releases.hoop.dev/release/1.72.0/hoop_1.72.0_Darwin_arm64.tar.gz"
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

func TestChecksumsURL(t *testing.T) {
	got := ChecksumsURL("1.72.0")
	want := "https://releases.hoop.dev/release/1.72.0/checksums.txt"
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

func TestNormalizeVersion(t *testing.T) {
	cases := map[string]string{
		"1.72.0":  "1.72.0",
		"v1.72.0": "1.72.0",
		"  1.0  ": "1.0",
		"":        "",
	}
	for in, want := range cases {
		if got := NormalizeVersion(in); got != want {
			t.Fatalf("NormalizeVersion(%q): want %q, got %q", in, want, got)
		}
	}
}
