// Package upstreamcredtest builds upstream credentials for the tests of the
// packages that send one: codec/http, gate, proxy and daemon.
package upstreamcredtest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hoophq/hoop/sidecar/upstreamcred"
)

// Token is the token Open holds. It is long enough for the placement guard
// to look for, and distinct enough that a search for it finds only a leak.
const Token = "upstream-api-token-5d1e9a7c3b2f4e60"

// File writes content to a new file that only its owner can read, as
// upstreamcred requires, and returns its path.
func File(t testing.TB, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "api-token")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Open returns a credential that sends Token under header and scheme.
func Open(t testing.TB, header, scheme string) *upstreamcred.Credential {
	t.Helper()
	c, err := upstreamcred.Open(upstreamcred.Spec{Header: header, Scheme: scheme, File: File(t, Token+"\n")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
