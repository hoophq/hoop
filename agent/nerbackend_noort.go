//go:build !ORT

package agent

import "github.com/hoophq/alcatraz/ner"

// Builds without the ORT tag (darwin, windows, dev) have only the pure-Go backend.
const (
	defaultNerBackend = ner.BackendGo
	ortCompiledIn     = false
)
