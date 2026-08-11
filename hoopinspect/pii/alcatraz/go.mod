module github.com/hoophq/hoopinspect/pii/alcatraz

go 1.26.5

require (
	github.com/hoophq/alcatraz v0.16.0
	github.com/hoophq/hoopinspect v0.0.0
)

replace github.com/hoophq/hoopinspect => ../..

replace github.com/hoophq/hoopinspect/config/yaml => ../../config/yaml
