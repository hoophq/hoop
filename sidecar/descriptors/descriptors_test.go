package descriptors

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

// A descriptors entry is a file unless it is unmistakably a URL: a Windows
// drive letter or a colon inside a relative path must never be handed to a
// fetcher, and a scheme must never be read as a file that does not exist.
func TestSchemeSeparatesURLsFromPaths(t *testing.T) {
	for entry, want := range map[string]string{
		"gs://schemas/billing.pb":              "gs",
		"GS://schemas/billing.pb":              "gs",
		"s3+https://schemas/billing.pb":        "s3+https",
		"/etc/hoop/billing.pb":                 "",
		"billing.pb":                           "",
		`C:\schemas\billing.pb`:                "",
		"dir:with:colons/billing.pb":           "",
		"://schemas/billing.pb":                "",
		"1gs://schemas/billing.pb":             "",
		"gs:/schemas/billing.pb":               "",
		"with space://schemas/billing.pb":      "",
		"gs://schemas/billing.pb?generation=7": "gs",
	} {
		if got := Scheme(entry); got != want {
			t.Errorf("Scheme(%q) = %q, want %q", entry, got, want)
		}
	}
}

func TestFetchRefusesAnUnlinkedSchemeAndNamesWhatIsLinked(t *testing.T) {
	Register("testlinked", func(context.Context, *url.URL) ([]byte, error) {
		return []byte("set"), nil
	})

	_, err := Fetch(context.Background(), "nolink://bucket/api.pb")
	if err == nil {
		t.Fatal("an unlinked scheme fetched")
	}
	for _, want := range []string{"nolink://bucket/api.pb", `scheme "nolink"`, "linked: testlinked"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}

	blob, err := Fetch(context.Background(), "TESTLINKED://bucket/api.pb")
	if err != nil || string(blob) != "set" {
		t.Fatalf("linked fetch = %q, %v", blob, err)
	}

	if _, err := Fetch(context.Background(), "/etc/hoop/api.pb"); err == nil {
		t.Fatal("a local path fetched")
	}
}
