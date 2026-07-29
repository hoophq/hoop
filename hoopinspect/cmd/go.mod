// The hoop-inspect binary.
//
// A nested module for the same reason as store/sqlite, pii/alcatraz and
// config/yaml: the root ships zero dependencies and must keep doing so. This
// module is where the optional plugins get linked together, so it is the one
// place that carries their dependencies. Nothing in the root module imports
// it, so `go build ./...` at the root still resolves nothing.
module github.com/hoophq/hoopinspect/cmd

go 1.24

require (
	github.com/hoophq/hoopinspect v0.0.0
	github.com/hoophq/hoopinspect/config/yaml v0.0.0
	github.com/hoophq/hoopinspect/pii/alcatraz v0.0.0
)

require (
	github.com/hoophq/alcatraz v0.7.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/hoophq/hoopinspect => ..

replace github.com/hoophq/hoopinspect/config/yaml => ../config/yaml

replace github.com/hoophq/hoopinspect/pii/alcatraz => ../pii/alcatraz
