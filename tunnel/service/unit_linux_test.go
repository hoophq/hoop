//go:build linux

package service

import (
	"strings"
	"testing"
)

// TestRenderUnit_ContainsRequiredClauses pins the systemd unit shape.
// The unit is a critical operational artifact — a regression here
// (missing RuntimeDirectory, wrong ExecStartPre, etc.) would only be
// caught by a full install + run cycle, which is slow and requires
// root. Doing the check as a string-content assertion is crude but
// catches the common breakages.
func TestRenderUnit_ContainsRequiredClauses(t *testing.T) {
	opts := Options{
		BinaryPath: "/usr/local/bin/hsh-tunneld",
		ConfigPath: "/etc/hsh/config.toml",
		SocketPath: "/var/run/hsh/hsh.sock",
		GroupName:  "hsh",
	}
	got, err := renderUnit(opts)
	if err != nil {
		t.Fatalf("renderUnit: %v", err)
	}
	mustContain := []string{
		"[Unit]",
		"[Service]",
		"[Install]",
		"Type=simple",
		"ExecStartPre=/usr/local/bin/hsh-tunneld validate-config --config-file /etc/hsh/config.toml",
		"--ipc-socket /var/run/hsh/hsh.sock",
		"--ipc-group hsh",
		"RuntimeDirectory=hsh",
		"ConfigurationDirectory=hsh",
		"SupplementaryGroups=hsh",
		"Restart=on-failure",
		"NoNewPrivileges=yes",
		"ProtectSystem=strict",
		"CapabilityBoundingSet=CAP_NET_ADMIN",
		"DeviceAllow=/dev/net/tun rwm",
		"WantedBy=multi-user.target",
	}
	for _, c := range mustContain {
		if !strings.Contains(got, c) {
			t.Errorf("rendered unit missing %q\n--- unit body ---\n%s", c, got)
		}
	}
}

// TestRenderUnit_NoSystemCallFilter pins the deliberate omission of
// the SystemCallFilter directive (see unit_linux.go for rationale). A
// SystemCallFilter set that fits today's Go runtime will SIGSYS the
// daemon on the next Go upgrade, so we leave it off. Regressions
// re-enabling it would be invisible in the wild until they break in
// prod — easier to catch with this assertion.
func TestRenderUnit_NoSystemCallFilter(t *testing.T) {
	got, err := renderUnit(Options{
		BinaryPath: "/usr/local/bin/hsh-tunneld",
		ConfigPath: "/etc/hsh/config.toml",
		SocketPath: "/var/run/hsh/hsh.sock",
		GroupName:  "hsh",
	})
	if err != nil {
		t.Fatalf("renderUnit: %v", err)
	}
	if strings.Contains(got, "SystemCallFilter=") {
		t.Errorf("unit includes SystemCallFilter; if re-introducing, derive the set from a real strace run, not from coarse filter groups")
	}
}

// TestRenderUnit_EmptyGroupOmitsSupplementary asserts that the
// "{{- if .GroupName }}" branch drops the SupplementaryGroups line when
// the operator opts out of the group entirely (an unusual but
// supported configuration for very small installs where one user owns
// the whole machine).
func TestRenderUnit_EmptyGroupOmitsSupplementary(t *testing.T) {
	opts := Options{
		BinaryPath: "/usr/local/bin/hsh-tunneld",
		ConfigPath: "/etc/hsh/config.toml",
		SocketPath: "/var/run/hsh/hsh.sock",
		GroupName:  "",
	}
	got, err := renderUnit(opts)
	if err != nil {
		t.Fatalf("renderUnit: %v", err)
	}
	if strings.Contains(got, "SupplementaryGroups=") {
		t.Errorf("unit includes SupplementaryGroups despite empty GroupName:\n%s", got)
	}
}

// TestRenderUnit_TrailingNewline keeps the unit file from drifting
// between re-installs (systemd accepts trailing whitespace either way,
// but stable diffs help operator review).
func TestRenderUnit_TrailingNewline(t *testing.T) {
	got, err := renderUnit(Options{
		BinaryPath: "/usr/local/bin/hsh-tunneld",
		ConfigPath: "/etc/hsh/config.toml",
		SocketPath: "/var/run/hsh/hsh.sock",
		GroupName:  "hsh",
	})
	if err != nil {
		t.Fatalf("renderUnit: %v", err)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("expected trailing newline")
	}
	if strings.HasSuffix(got, "\n\n\n") {
		t.Errorf("too many trailing newlines")
	}
}
