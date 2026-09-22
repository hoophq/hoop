// The sidecar module has exactly ONE dependency: github.com/hoophq/libhoop.
//
// It used to have none, and that was a product requirement rather than an
// accident — a dependency-free stdlib module can be read end to end in an
// afternoon, vendored without a supply-chain review, and compiled to
// `GOOS=wasip1 GOARCH=wasm` for an Envoy network filter.
//
// That ended when the protocol codecs moved to libhoop. The codecs are the
// thing that turns bytes into the Statement a policy evaluates, so this
// module cannot describe its own inputs without naming libhoop's types. They
// are aliased in wiretypes.go rather than copied: one definition of the
// policy document, no conversion on the hot path, no drift.
//
// libhoop is PRIVATE. Building or testing this module therefore needs
// GOPRIVATE=github.com/hoophq/libhoop and credentials for that repository.
// Anyone without them cannot build this module at all — that is the cost of
// the codecs being private, and it is deliberate.
//
// The dependency runs one way. libhoop imports nothing from this repository.
module github.com/hoophq/hoop/sidecar

go 1.26.8

require github.com/hoophq/libhoop v0.0.0-20260916183132-1443e5d2e2d2

require (
	github.com/creack/pty v1.1.24 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/pkg/sftp v1.13.11 // indirect
	go.mongodb.org/mongo-driver v1.17.9 // indirect
	golang.org/x/crypto v0.56.0 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260706201446-f0a921348800 // indirect
	google.golang.org/grpc v1.84.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)
