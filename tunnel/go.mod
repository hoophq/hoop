module github.com/hoophq/hoop/tunnel

go 1.26.2

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/hoophq/hoop/common v0.0.0-00010101000000-000000000000
	github.com/miekg/dns v1.1.72
	golang.org/x/sys v0.44.0
	gvisor.dev/gvisor v0.0.0-20260518063529-209a0ce076a1
)

require (
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	github.com/google/btree v1.1.2 // indirect
	github.com/mattn/go-isatty v0.0.21 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.1 // indirect
	golang.org/x/exp v0.0.0-20250711185948-6ae5c78190dc // indirect
	golang.org/x/mod v0.35.0 // indirect
	golang.org/x/net v0.54.0 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/tools v0.44.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260414002931-afd174a4e478 // indirect
	google.golang.org/grpc v1.80.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/hoophq/hoop/common => ../common
