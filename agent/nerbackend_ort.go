//go:build ORT

package agent

import "github.com/hoophq/alcatraz/ner"

// The Linux release build (-tags ORT) carries ONNX Runtime support.
const (
	defaultNerBackend = ner.BackendORT
	ortCompiledIn     = true
)
