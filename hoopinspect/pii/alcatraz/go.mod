module github.com/hoophq/hoop/hoopinspect/pii/alcatraz

go 1.26.5

require (
	github.com/hoophq/alcatraz v0.16.0
	github.com/hoophq/hoop/hoopinspect v0.0.0
)

replace github.com/hoophq/hoop/hoopinspect => ../..

replace github.com/hoophq/hoop/hoopinspect/config/yaml => ../../config/yaml
