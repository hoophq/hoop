// The sidecar's MCP server (ADR-0021) is a nested module for the same reason
// as analyzer/vertex and descriptors/gcs: the root module carries libhoop and
// nothing else, and the MCP SDK is a dependency. A binary gets the server by
// importing this module; one that does not refuses an "mcp" block.
module github.com/hoophq/hoop/sidecar/mcp

go 1.26.8

require (
	github.com/hoophq/hoop/sidecar v0.0.0
	github.com/modelcontextprotocol/go-sdk v1.7.0
)

require (
	github.com/ClickHouse/ch-go v0.71.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/creack/pty v1.1.24 // indirect
	github.com/go-faster/city v1.0.1 // indirect
	github.com/go-faster/errors v0.7.1 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hoophq/libhoop v0.0.0-20260925162911-6724f4eda8b2 // indirect
	github.com/klauspost/compress v1.18.5 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.30 // indirect
	github.com/pkg/sftp v1.13.11 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.mongodb.org/mongo-driver v1.17.9 // indirect
	go.opentelemetry.io/otel v1.44.0 // indirect
	go.opentelemetry.io/otel/trace v1.44.0 // indirect
	golang.org/x/crypto v0.56.0 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260706201446-f0a921348800 // indirect
	google.golang.org/grpc v1.84.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/hoophq/hoop/sidecar => ..
