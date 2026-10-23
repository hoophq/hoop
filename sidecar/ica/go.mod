// The ICA engine (libhoop-ica) is a nested module for the same reason as
// mcp and analyzer/vertex: the root module carries libhoop and nothing else,
// and the engine is a dependency. A binary gets an ica lane by importing
// this module; one that does not refuses the lane at startup.
module github.com/hoophq/hoop/sidecar/ica

go 1.26.10

require (
	github.com/hoophq/hoop/sidecar v0.0.0
	github.com/hoophq/libhoop-ica v0.5.0
)

require (
	github.com/ClickHouse/ch-go v0.71.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/creack/pty v1.1.24 // indirect
	github.com/daulet/tokenizers v1.27.0 // indirect
	github.com/go-errors/errors v1.5.1 // indirect
	github.com/go-faster/city v1.0.1 // indirect
	github.com/go-faster/errors v0.7.1 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/gofrs/flock v0.13.0 // indirect
	github.com/gomlx/compute v0.1.2 // indirect
	github.com/gomlx/exceptions v0.0.3 // indirect
	github.com/gomlx/go-huggingface v0.4.1 // indirect
	github.com/gomlx/go-xla v0.4.1 // indirect
	github.com/gomlx/gomlx v0.28.2 // indirect
	github.com/gomlx/onnx-gomlx v0.5.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hoophq/alcatraz v0.23.0 // indirect
	github.com/hoophq/alcatraz/ner v0.23.0 // indirect
	github.com/hoophq/libhoop v0.0.0-20261022191641-55dc7bef0843 // indirect
	github.com/klauspost/compress v1.18.5 // indirect
	github.com/knights-analytics/hugot v0.7.7 // indirect
	github.com/knights-analytics/ortgenai v0.3.2 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.30 // indirect
	github.com/pion/dtls/v3 v3.1.4 // indirect
	github.com/pion/logging v0.2.4 // indirect
	github.com/pion/transport/v4 v4.0.1 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pkg/sftp v1.13.11 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/viant/afs v1.30.0 // indirect
	github.com/yalue/onnxruntime_go v1.32.0 // indirect
	go.mongodb.org/mongo-driver v1.17.9 // indirect
	go.opentelemetry.io/otel v1.44.0 // indirect
	go.opentelemetry.io/otel/trace v1.44.0 // indirect
	golang.org/x/crypto v0.56.0 // indirect
	golang.org/x/exp v0.0.0-20260727155853-b88d891fe743 // indirect
	golang.org/x/image v0.44.0 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/term v0.45.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260706201446-f0a921348800 // indirect
	google.golang.org/grpc v1.84.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	k8s.io/klog/v2 v2.140.0 // indirect
)

replace github.com/hoophq/hoop/sidecar => ..
