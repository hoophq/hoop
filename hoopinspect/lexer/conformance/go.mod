// lexer/conformance is a NESTED module holding a test-only oracle.
//
// The root github.com/hoophq/hoop/hoopinspect module carries libhoop alone and
// must keep them, test dependencies included. But a hand-written SQL scanner
// that nobody checks against a real grammar is a pile of assertions about
// SQL, and SQL does not care what we assert. So PostgreSQL's own parser lives
// here, behind its own go.mod, and checks the scanner on every run of this
// package. Nothing the root ships imports any of it.
//
// The parser is wasilibs/go-pgquery, which is the same libpg_query compiled
// to WebAssembly and run under wazero, rather than pganalyze/pg_query_go's
// cgo build. A conformance suite that only runs where a C toolchain is
// configured is a conformance suite that stops running, and this has to run
// under CGO_ENABLED=0 in the same CI job as the wasip1 build.
//
// pganalyze is still a direct require: it owns the generated protobuf node
// types the walk switches on, and those are pure Go. Its cgo entry points
// sit behind `//go:build cgo` and are never reached from here.
module github.com/hoophq/hoop/hoopinspect/lexer/conformance

go 1.26.5

require (
	github.com/hoophq/hoop/hoopinspect v0.0.0
	github.com/pganalyze/pg_query_go/v6 v6.2.2
	github.com/wasilibs/go-pgquery v0.0.0-20260728010200-155ebad2880e
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/tetratelabs/wazero v1.12.0 // indirect
	github.com/wasilibs/wazero-helpers v0.0.0-20250123031827-cd30c44769bb // indirect
	golang.org/x/sys v0.44.0 // indirect
)

// The parent is developed in-tree and not yet tagged.
replace github.com/hoophq/hoop/hoopinspect => ../..
