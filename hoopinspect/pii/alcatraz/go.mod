module github.com/hoophq/hoopinspect/pii/alcatraz

go 1.24

require (
	github.com/hoophq/alcatraz v0.7.0
	github.com/hoophq/hoopinspect v0.0.0
)

replace github.com/hoophq/hoopinspect => ../..

require github.com/hoophq/hoopinspect/config/yaml v0.0.0

require gopkg.in/yaml.v3 v3.0.1 // indirect

replace github.com/hoophq/hoopinspect/config/yaml => ../../config/yaml
