module github.com/runopsio/hoop/agent

go 1.18

require (
	github.com/runopsio/hoop/proto v0.0.0-00010101000000-000000000000
	google.golang.org/grpc v1.49.0
)

require (
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/lib/pq v1.10.7 // indirect
	golang.org/x/net v0.0.0-20220907135653-1e95f45603a7 // indirect
	golang.org/x/sys v0.0.0-20220908164124-27713097b956 // indirect
	golang.org/x/text v0.3.7 // indirect
	google.golang.org/genproto v0.0.0-20220908141613-51c1cc9bc6d0 // indirect
	google.golang.org/protobuf v1.28.1 // indirect
)

replace github.com/runopsio/hoop/proto => ../proto
