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

require github.com/hoophq/libhoop v0.0.0-20261005142803-0620ef314f79

require (
	github.com/ClickHouse/ch-go v0.71.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/creack/pty v1.1.24 // indirect
	github.com/go-faster/city v1.0.1 // indirect
	github.com/go-faster/errors v0.7.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/klauspost/compress v1.18.5 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.30 // indirect
	github.com/pkg/sftp v1.13.11 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	go.mongodb.org/mongo-driver v1.17.9 // indirect
	go.opentelemetry.io/otel v1.44.0 // indirect
	go.opentelemetry.io/otel/trace v1.44.0 // indirect
	golang.org/x/crypto v0.56.0 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260706201446-f0a921348800 // indirect
	google.golang.org/grpc v1.84.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)
