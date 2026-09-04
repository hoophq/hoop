// The hoop-inspect binary.
//
// A nested module for the same reason as store/sqlite, pii/alcatraz and
// config/yaml: the root carries libhoop and nothing else,
// and must keep it that way. This
// module is where the optional plugins get linked together, so it is the one
// place that carries their dependencies. Nothing in the root module imports
// it, so `go build ./...` at the root still resolves nothing.
module github.com/hoophq/hoop/sidecar/cmd

go 1.26.5

require (
	github.com/hoophq/hoop/sidecar v0.0.0
	github.com/hoophq/hoop/sidecar/config/yaml v0.0.0
	github.com/hoophq/hoop/sidecar/pii/alcatraz v0.0.0
)

require (
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	github.com/hoophq/libhoop v0.0.0-20260904162416-990b2b7228d4 // indirect
	github.com/kr/text v0.2.0 // indirect
	go.mongodb.org/mongo-driver v1.17.9 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

require (
	github.com/hoophq/alcatraz v0.19.0 // indirect
	github.com/hoophq/hoop/sidecar/analyzer/vertex v0.0.0
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/hoophq/hoop/sidecar => ..

replace github.com/hoophq/hoop/sidecar/config/yaml => ../config/yaml

replace github.com/hoophq/hoop/sidecar/pii/alcatraz => ../pii/alcatraz

replace github.com/hoophq/hoop/sidecar/analyzer/vertex => ../analyzer/vertex
