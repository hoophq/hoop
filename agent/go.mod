module github.com/runopsio/hoop/agent

go 1.19

replace github.com/runopsio/hoop/common => ../common

require (
	github.com/runopsio/hoop/common v0.0.0-00010101000000-000000000000
	google.golang.org/grpc v1.49.0
)

require (
	github.com/creack/pty v1.1.18 // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/google/uuid v1.3.0 // indirect
	github.com/lib/pq v1.10.7 // indirect
	golang.org/x/net v0.0.0-20201021035429-f5854403a974 // indirect
	golang.org/x/sys v0.0.0-20210615035016-665e8c7367d1 // indirect
	golang.org/x/text v0.3.3 // indirect
	google.golang.org/genproto v0.0.0-20200526211855-cb27e3aa2013 // indirect
	google.golang.org/protobuf v1.28.1 // indirect
)
