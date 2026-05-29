//go:build darwin

package service

import (
	"encoding/xml"
	"strings"
	"testing"
)

// TestRenderPlist_ContainsRequiredKeys pins the LaunchDaemon plist
// shape. Like the systemd unit test, a regression here (wrong Label,
// missing KeepAlive, dropped --ipc-socket arg) would otherwise only
// surface during a full install + boot cycle.
func TestRenderPlist_ContainsRequiredKeys(t *testing.T) {
	opts := Options{
		BinaryPath: "/usr/local/bin/hsh-tunneld",
		ConfigPath: "/etc/hsh/config.toml",
		SocketPath: "/var/run/hsh/hsh.sock",
		GroupName:  "hsh",
	}
	got, err := renderPlist(opts)
	if err != nil {
		t.Fatalf("renderPlist: %v", err)
	}
	mustContain := []string{
		"<key>Label</key>",
		"<string>dev.hoop.hsh-tunneld</string>",
		"<key>ProgramArguments</key>",
		"<string>/usr/local/bin/hsh-tunneld</string>",
		"<string>--config-file</string>",
		"<string>/etc/hsh/config.toml</string>",
		"<string>--ipc-socket</string>",
		"<string>/var/run/hsh/hsh.sock</string>",
		"<string>--ipc-group</string>",
		"<string>hsh</string>",
		"<key>RunAtLoad</key>",
		"<key>KeepAlive</key>",
		"<key>SuccessfulExit</key>",
		"<key>ThrottleInterval</key>",
		"<key>UserName</key>",
		"<string>root</string>",
	}
	for _, c := range mustContain {
		if !strings.Contains(got, c) {
			t.Errorf("rendered plist missing %q\n--- plist body ---\n%s", c, got)
		}
	}
}

// TestRenderPlist_IsValidXML guards against a template edit producing a
// malformed plist that launchd would reject at bootstrap time. We parse
// it as generic XML — a structural break (unclosed tag, stray &) fails
// here long before it reaches a real `launchctl bootstrap`.
func TestRenderPlist_IsValidXML(t *testing.T) {
	got, err := renderPlist(Options{
		BinaryPath: "/usr/local/bin/hsh-tunneld",
		ConfigPath: "/etc/hsh/config.toml",
		SocketPath: "/var/run/hsh/hsh.sock",
		GroupName:  "hsh",
	})
	if err != nil {
		t.Fatalf("renderPlist: %v", err)
	}
	var v any
	if err := xml.Unmarshal([]byte(got), &v); err != nil {
		t.Fatalf("rendered plist is not valid XML: %v\n%s", err, got)
	}
}

// TestRenderPlist_EscapesPaths ensures a path containing an XML
// metacharacter is escaped rather than producing a broken plist. macOS
// permits `&` in paths, and an unescaped one would make launchd refuse
// the file.
func TestRenderPlist_EscapesPaths(t *testing.T) {
	got, err := renderPlist(Options{
		BinaryPath: "/opt/a&b/hsh-tunneld",
		ConfigPath: "/etc/hsh/config.toml",
		SocketPath: "/var/run/hsh/hsh.sock",
		GroupName:  "hsh",
	})
	if err != nil {
		t.Fatalf("renderPlist: %v", err)
	}
	if strings.Contains(got, "/opt/a&b/") {
		t.Errorf("ampersand not escaped in plist: %s", got)
	}
	if !strings.Contains(got, "a&amp;b") {
		t.Errorf("expected escaped &amp; in plist: %s", got)
	}
	// Still valid XML after escaping.
	var v any
	if err := xml.Unmarshal([]byte(got), &v); err != nil {
		t.Fatalf("escaped plist is not valid XML: %v", err)
	}
}

// TestRenderPlist_TrailingNewline keeps the plist from drifting between
// reinstalls (stable diffs for operator review + the writeFileIfDifferent
// no-op fast path).
func TestRenderPlist_TrailingNewline(t *testing.T) {
	got, err := renderPlist(Options{
		BinaryPath: "/usr/local/bin/hsh-tunneld",
		ConfigPath: "/etc/hsh/config.toml",
		SocketPath: "/var/run/hsh/hsh.sock",
		GroupName:  "hsh",
	})
	if err != nil {
		t.Fatalf("renderPlist: %v", err)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Error("expected trailing newline")
	}
	if strings.HasSuffix(got, "\n\n\n") {
		t.Error("too many trailing newlines")
	}
}
