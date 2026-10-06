package main

import (
	"github.com/hoophq/hoop/gateway"
	"github.com/hoophq/hoop/gateway/appconfig"
)

func main() {
	gateway.Run(appconfig.AppModeGateway)
}
