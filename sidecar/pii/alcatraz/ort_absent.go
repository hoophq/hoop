//go:build !(cgo && (ORT || ALL))

package alcatraz

import "github.com/hoophq/alcatraz/ner"

// ORTCompiled is documented in ort_compiled.go.
const ORTCompiled = false

const defaultNERBackend = ner.BackendGo
