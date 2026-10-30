//go:build cgo && (ORT || ALL)

package alcatraz

import "github.com/hoophq/alcatraz/ner"

// ORTCompiled reports whether this build links hugot's ONNX Runtime backend.
// The tag expression is hugot's own (hugot_ort.go), so it cannot disagree
// with what ner.New accepts.
const ORTCompiled = true

const defaultNERBackend = ner.BackendORT
