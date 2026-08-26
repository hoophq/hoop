// A YAML front end for the hoop-inspect config.
//
// A nested module for the same reason as store/sqlite and pii/alcatraz: the
// root ships zero dependencies, and a syntax preference must not change that
// for callers who did not ask for it. Depending on this module is opting in.
module github.com/hoophq/hoop/hoopinspect/config/yaml

go 1.26.5

toolchain go1.26.5

require (
	github.com/hoophq/hoop/hoopinspect v0.0.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/kr/pretty v0.3.1 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
)

replace github.com/hoophq/hoop/hoopinspect => ../..

