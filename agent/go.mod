module github.com/runopsio/hoop/agent

go 1.19

replace github.com/runopsio/hoop/common => ../common

require (
	cloud.google.com/go/dlp v1.7.0
	github.com/BurntSushi/toml v1.2.0
	github.com/getsentry/sentry-go v0.18.0
	github.com/hashicorp/go-hclog v0.14.1
	github.com/hashicorp/go-plugin v1.4.8
	github.com/hoophq/pluginhooks v0.0.6
	github.com/pyroscope-io/client v0.7.2
	github.com/runopsio/hoop/common v0.0.0-00010101000000-000000000000
	github.com/sethvargo/go-password v0.2.0
	github.com/stretchr/testify v1.8.0
	github.com/xo/dburl v0.14.2
	go.uber.org/zap v1.24.0
	golang.org/x/oauth2 v0.0.0-20221014153046-6fdb5e3db783
	google.golang.org/api v0.102.0
	olympos.io/encoding/edn v0.0.0-20201019073823-d3554ca0b0a3
)

require (
	cloud.google.com/go/compute v1.12.1 // indirect
	cloud.google.com/go/compute/metadata v0.2.1 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/fatih/color v1.7.0 // indirect
	github.com/golang/groupcache v0.0.0-20200121045136-8c9f03a8e57e // indirect
	github.com/google/go-cmp v0.5.9 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.2.0 // indirect
	github.com/googleapis/gax-go/v2 v2.6.0 // indirect
	github.com/hashicorp/yamux v0.0.0-20180604194846-3520598351bb // indirect
	github.com/inconshreveable/mousetrap v1.0.1 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.16 // indirect
	github.com/mitchellh/go-testing-interface v0.0.0-20171004221916-a61a99592b77 // indirect
	github.com/oklog/run v1.0.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/pyroscope-io/godeltaprof v0.1.2 // indirect
	github.com/spf13/cobra v1.6.1 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	go.opencensus.io v0.23.0 // indirect
	go.uber.org/atomic v1.7.0 // indirect
	go.uber.org/multierr v1.6.0 // indirect
	google.golang.org/appengine v1.6.7 // indirect
	google.golang.org/grpc v1.50.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

require (
	github.com/creack/pty v1.1.18
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/google/uuid v1.3.0
	github.com/lib/pq v1.10.7
	golang.org/x/net v0.2.0 // indirect
	golang.org/x/sys v0.3.0 // indirect
	golang.org/x/text v0.4.0 // indirect
	google.golang.org/genproto v0.0.0-20221027153422-115e99e71e1c // indirect
	google.golang.org/protobuf v1.28.1 // indirect
)
