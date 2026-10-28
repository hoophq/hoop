// The hoop-inspect binary.
//
// A nested module for the same reason as store/sqlite, pii/alcatraz and
// config/yaml: the root carries libhoop and nothing else,
// and must keep it that way. This
// module is where the optional plugins get linked together, so it is the one
// place that carries their dependencies. Nothing in the root module imports
// it, so `go build ./...` at the root still resolves nothing.
module github.com/hoophq/hoop/sidecar/cmd

go 1.26.10

require (
	github.com/hoophq/hoop/sidecar v0.0.0
	github.com/hoophq/hoop/sidecar/codec/wasm v0.0.0
	github.com/hoophq/hoop/sidecar/config/yaml v0.0.0
	github.com/hoophq/hoop/sidecar/pii/alcatraz v0.0.0
	github.com/hoophq/hoop/sidecar/telemetry/otlp v0.0.0
)

require (
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	github.com/ClickHouse/ch-go v0.71.0 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/creack/pty v1.1.24 // indirect
	github.com/daulet/tokenizers v1.27.0 // indirect
	github.com/go-errors/errors v1.5.1 // indirect
	github.com/go-faster/city v1.0.1 // indirect
	github.com/go-faster/errors v0.7.1 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/gofrs/flock v0.13.0 // indirect
	github.com/gomlx/compute v0.1.2 // indirect
	github.com/gomlx/exceptions v0.0.3 // indirect
	github.com/gomlx/go-huggingface v0.4.1 // indirect
	github.com/gomlx/go-xla v0.4.1 // indirect
	github.com/gomlx/gomlx v0.28.2 // indirect
	github.com/gomlx/onnx-gomlx v0.5.2 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.29.0 // indirect
	github.com/hoophq/alcatraz/ner v0.23.0 // indirect
	github.com/hoophq/libhoop v0.0.0-20261028122443-e602d2f8d44b // indirect
	github.com/hoophq/libhoop-ica v0.7.0 // indirect
	github.com/klauspost/compress v1.18.5 // indirect
	github.com/knights-analytics/hugot v0.7.7 // indirect
	github.com/knights-analytics/ortgenai v0.3.2 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/modelcontextprotocol/go-sdk v1.7.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.30 // indirect
	github.com/pion/dtls/v3 v3.1.4 // indirect
	github.com/pion/logging v0.2.4 // indirect
	github.com/pion/transport/v4 v4.0.1 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pkg/sftp v1.13.11 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/tetratelabs/wazero v1.12.0 // indirect
	github.com/viant/afs v1.30.0 // indirect
	github.com/yalue/onnxruntime_go v1.32.0 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.mongodb.org/mongo-driver v1.17.9 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc v0.20.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp v0.20.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.44.0 // indirect
	go.opentelemetry.io/otel/log v0.20.0 // indirect
	go.opentelemetry.io/otel/metric v1.44.0 // indirect
	go.opentelemetry.io/otel/sdk v1.44.0 // indirect
	go.opentelemetry.io/otel/sdk/log v0.20.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.44.0 // indirect
	go.opentelemetry.io/otel/trace v1.44.0 // indirect
	go.opentelemetry.io/proto/otlp v1.10.0 // indirect
	golang.org/x/crypto v0.56.0 // indirect
	golang.org/x/exp v0.0.0-20260727155853-b88d891fe743 // indirect
	golang.org/x/image v0.44.0 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/term v0.45.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260706201446-f0a921348800 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260706201446-f0a921348800 // indirect
	google.golang.org/grpc v1.84.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	k8s.io/klog/v2 v2.140.0 // indirect
)

require (
	github.com/hoophq/alcatraz v0.23.0 // indirect
	github.com/hoophq/hoop/sidecar/analyzer/vertex v0.0.0
	github.com/hoophq/hoop/sidecar/descriptors/gcs v0.0.0
	github.com/hoophq/hoop/sidecar/ica v0.0.0
	github.com/hoophq/hoop/sidecar/mcp v0.0.0
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/hoophq/hoop/sidecar => ..

replace github.com/hoophq/hoop/sidecar/config/yaml => ../config/yaml

replace github.com/hoophq/hoop/sidecar/pii/alcatraz => ../pii/alcatraz

replace github.com/hoophq/hoop/sidecar/analyzer/vertex => ../analyzer/vertex

replace github.com/hoophq/hoop/sidecar/descriptors/gcs => ../descriptors/gcs

replace github.com/hoophq/hoop/sidecar/mcp => ../mcp

replace github.com/hoophq/hoop/sidecar/ica => ../ica

replace github.com/hoophq/hoop/sidecar/codec/wasm => ../codec/wasm

replace github.com/hoophq/hoop/sidecar/telemetry/otlp => ../telemetry/otlp
