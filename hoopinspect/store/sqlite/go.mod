// store/sqlite is a NESTED module on purpose.
//
// The root github.com/hoophq/hoopinspect module has zero dependencies and
// must keep them: it is meant to be vendored without a supply-chain review
// and compiled to wasip1. A SQLite backend needs a driver, so the driver
// lives here, behind its own go.mod. A deployment that only wants JSONL
// never resolves it.
//
// modernc.org/sqlite rather than mattn/go-sqlite3 because it is pure Go: the
// sidecar ships as a static binary and a cgo dependency breaks that build.
module github.com/hoophq/hoopinspect/store/sqlite

go 1.25.0

require (
	github.com/hoophq/hoopinspect v0.0.0
	modernc.org/sqlite v1.49.1
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.21 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/tools v0.48.0 // indirect
	modernc.org/libc v1.72.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

// The parent is developed in-tree and not yet tagged.
replace github.com/hoophq/hoopinspect => ../..
