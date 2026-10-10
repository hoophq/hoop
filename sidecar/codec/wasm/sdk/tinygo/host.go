package codec

import (
	"encoding/json"
	"unsafe"
)

// Level is a log level the host understands.
type Level uint32

const (
	LevelDebug Level = 0
	LevelInfo  Level = 1
	LevelWarn  Level = 2
	LevelError Level = 3
)

// MaskFunc hands one result-set cell to the lane's masker and returns the
// value to forward, which may differ in length. column is empty when the
// protocol does not name one.
type MaskFunc func(column string, value []byte) []byte

// AnalyzeSQL classifies SQL text with the host lexer in the manifest's
// sql_dialect. The classifier stays in the relay on purpose: one
// auditable copy serves every plug-in. Copy the result onto a statement
// with Statement.WithAnalysis.
func AnalyzeSQL(sql string) SQLAnalysis {
	var a SQLAnalysis
	raw := take(hostAnalyzeSQL(stringRegion(sql)))
	if err := json.Unmarshal(raw, &a); err != nil {
		// The host wrote it; a payload it cannot encode is a host bug, and
		// a trap is the ABI's way to report one.
		panic("hoop-codec: analyze_sql returned malformed JSON: " + err.Error())
	}
	return a
}

// SplitSQL splits a multi-statement text with the host lexer, which knows
// that a semicolon inside a string literal ends nothing.
func SplitSQL(sql string) []string {
	var parts []string
	raw := take(hostSplitSQL(stringRegion(sql)))
	if err := json.Unmarshal(raw, &parts); err != nil {
		panic("hoop-codec: split_sql returned malformed JSON: " + err.Error())
	}
	return parts
}

// mask reaches the host's mask import, legal only on the stack of rewrite
// or flush; Rewriter.Rewrite receives it as a MaskFunc.
func mask(column string, value []byte) []byte {
	cptr, cn := stringRegion(column)
	vptr, vn := region(value)
	return take(hostMask(cptr, cn, vptr, vn))
}

func stringRegion(s string) (ptr, n uint32) {
	if len(s) == 0 {
		return 0, 0
	}
	return uint32(uintptr(unsafe.Pointer(unsafe.StringData(s)))), uint32(len(s))
}
