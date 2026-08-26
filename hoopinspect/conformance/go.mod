// conformance is a NESTED module on purpose.
//
// The root github.com/hoophq/hoop/hoopinspect module has zero dependencies and
// must keep them: it is meant to be vendored without a supply-chain review
// and compiled to wasip1. The tests here drive the real protocol codecs,
// which ship in github.com/hoophq/libhoop — a private module. A test-only
// import still lands in go.mod, so holding these files in the root would
// make the root unbuildable for anyone without access to libhoop.
//
// What lives here is the suite that needs real wire bytes: Postgres
// simple-query frames, HTTP requests, MSSQL TDS packets. Tests that only
// need the exported types stayed in the root and run without credentials.
module github.com/hoophq/hoop/hoopinspect/conformance

go 1.26.5

toolchain go1.26.5

require (
	github.com/hoophq/hoop/hoopinspect v0.0.0
	github.com/hoophq/libhoop v1.100.0
)

replace github.com/hoophq/hoop/hoopinspect => ..
