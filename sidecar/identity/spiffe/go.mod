// The SPIFFE identity type and credential source are a nested module for
// the same reason as ica and mcp: the root module carries libhoop and
// nothing else, and go-spiffe brings grpc-go. A binary gets `identity:
// {type: spiffe}` and the `spiffe` pool source by importing this module;
// one that does not refuses both at Validate.
module github.com/hoophq/hoop/sidecar/identity/spiffe

go 1.26.10

require (
	github.com/go-jose/go-jose/v4 v4.1.4
	github.com/hoophq/hoop/sidecar v0.0.0
	github.com/hoophq/libhoop v0.0.0-20261024144850-a59ec27aeeda
	github.com/spiffe/go-spiffe/v2 v2.8.1
	google.golang.org/grpc v1.84.0
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/ClickHouse/ch-go v0.71.0 // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
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
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260706201446-f0a921348800 // indirect
)

replace github.com/hoophq/hoop/sidecar => ../..
