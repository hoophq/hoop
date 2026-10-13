//go:build tinygo

package codec

import "unsafe"

// The imports of module `hoop`. A module imports only what it calls: a
// plug-in that never touches AnalyzeSQL has no analyze_sql import, and the
// host does not ask it for a sql_dialect.

//go:wasmimport hoop analyze_sql
func hostAnalyzeSQL(ptr, n uint32) uint64

//go:wasmimport hoop split_sql
func hostSplitSQL(ptr, n uint32) uint64

//go:wasmimport hoop mask
func hostMask(cptr, cn, vptr, vn uint32) uint64

//go:wasmimport hoop log
func hostLog(level, ptr, n uint32)

// Log writes one line to the relay log, tagged with the lane and
// connection.
func Log(level Level, message string) {
	hostLog(uint32(level), uint32(uintptr(unsafe.Pointer(unsafe.StringData(message)))), uint32(len(message)))
}
