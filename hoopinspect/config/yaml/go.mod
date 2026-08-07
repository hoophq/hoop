// A YAML front end for the hoop-inspect config.
//
// A nested module for the same reason as store/sqlite and pii/alcatraz: the
// root ships zero dependencies, and a syntax preference must not change that
// for callers who did not ask for it. Depending on this module is opting in.
module github.com/hoophq/hoopinspect/config/yaml

go 1.26.5

toolchain go1.26.5

require (
	github.com/hoophq/hoopinspect v0.0.0
	gopkg.in/yaml.v3 v3.0.1
)

replace github.com/hoophq/hoopinspect => ../..
