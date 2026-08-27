module github.com/hoophq/hoop/sidecar/pii/alcatraz

go 1.26.5

require (
	github.com/hoophq/alcatraz v0.16.0
	github.com/hoophq/hoop/sidecar v0.0.0
)

replace github.com/hoophq/hoop/sidecar => ../..

replace github.com/hoophq/hoop/sidecar/config/yaml => ../../config/yaml
