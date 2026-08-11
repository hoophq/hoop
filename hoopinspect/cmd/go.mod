// The hoop-inspect binary.
//
// A nested module for the same reason as store/sqlite, pii/alcatraz and
// config/yaml: the root ships zero dependencies and must keep doing so. This
// module is where the optional plugins get linked together, so it is the one
// place that carries their dependencies. Nothing in the root module imports
// it, so `go build ./...` at the root still resolves nothing.
module github.com/hoophq/hoopinspect/cmd

go 1.26.5

require (
	github.com/hoophq/hoopinspect v0.0.0
	github.com/hoophq/hoopinspect/config/yaml v0.0.0
	github.com/hoophq/hoopinspect/pii/alcatraz v0.0.0
)

require (
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	golang.org/x/oauth2 v0.32.0 // indirect
	golang.org/x/sys v0.35.0 // indirect
)

require (
	github.com/hoophq/alcatraz v0.16.0 // indirect
	github.com/hoophq/hoopinspect/analyzer/vertex v0.0.0
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/hoophq/hoopinspect => ..

replace github.com/hoophq/hoopinspect/config/yaml => ../config/yaml

replace github.com/hoophq/hoopinspect/pii/alcatraz => ../pii/alcatraz

replace github.com/hoophq/hoopinspect/analyzer/vertex => ../analyzer/vertex
