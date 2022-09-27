module github.com/runopsio/hoop/client

go 1.19

require (
	github.com/briandowns/spinner v1.19.0
	github.com/google/uuid v1.3.0
	github.com/runopsio/hoop/proto v0.0.0-00010101000000-000000000000
	github.com/spf13/cobra v1.5.0
	google.golang.org/grpc v1.49.0
)

require (
	github.com/fatih/color v1.7.0 // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/inconshreveable/mousetrap v1.0.0 // indirect
	github.com/mattn/go-colorable v0.1.2 // indirect
	github.com/mattn/go-isatty v0.0.8 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	golang.org/x/net v0.0.0-20201021035429-f5854403a974 // indirect
	golang.org/x/sys v0.0.0-20210119212857-b64e53b001e4 // indirect
	golang.org/x/text v0.3.3 // indirect
	google.golang.org/genproto v0.0.0-20200526211855-cb27e3aa2013 // indirect
	google.golang.org/protobuf v1.28.1 // indirect
)

replace github.com/runopsio/hoop/proto => ../proto
