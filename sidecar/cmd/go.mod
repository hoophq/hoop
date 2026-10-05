// The hoop-inspect binary.
//
// A nested module for the same reason as store/sqlite, pii/alcatraz and
// config/yaml: the root carries libhoop and nothing else,
// and must keep it that way. This
// module is where the optional plugins get linked together, so it is the one
// place that carries their dependencies. Nothing in the root module imports
// it, so `go build ./...` at the root still resolves nothing.
module github.com/hoophq/hoop/sidecar/cmd

go 1.26.8

require (
	github.com/hoophq/hoop/sidecar v0.0.0
	github.com/hoophq/hoop/sidecar/config/yaml v0.0.0
	github.com/hoophq/hoop/sidecar/pii/alcatraz v0.0.0
)

require (
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	github.com/ClickHouse/ch-go v0.71.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/creack/pty v1.1.24 // indirect
	github.com/go-faster/city v1.0.1 // indirect
	github.com/go-faster/errors v0.7.1 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hoophq/libhoop v0.0.0-20261005142803-0620ef314f79 // indirect
	github.com/klauspost/compress v1.18.5 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/modelcontextprotocol/go-sdk v1.7.0 // indirect
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

require (
	github.com/hoophq/alcatraz v0.19.0 // indirect
	github.com/hoophq/hoop/sidecar/analyzer/vertex v0.0.0
	github.com/hoophq/hoop/sidecar/descriptors/gcs v0.0.0
	github.com/hoophq/hoop/sidecar/mcp v0.0.0
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/hoophq/hoop/sidecar => ..

replace github.com/hoophq/hoop/sidecar/config/yaml => ../config/yaml

replace github.com/hoophq/hoop/sidecar/pii/alcatraz => ../pii/alcatraz

replace github.com/hoophq/hoop/sidecar/analyzer/vertex => ../analyzer/vertex

replace github.com/hoophq/hoop/sidecar/descriptors/gcs => ../descriptors/gcs

replace github.com/hoophq/hoop/sidecar/mcp => ../mcp
