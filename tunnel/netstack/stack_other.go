//go:build !linux && !darwin

package netstack

import "errors"

func openTUN(requestedName string, mtu uint32) (tunDevice, error) {
	return nil, errors.New("netstack: TUN device support is implemented on Linux and macOS only")
}

// ConfigureRoutes is a non-Linux/darwin stub.
func ConfigureRoutes(cfg RouteConfig) error {
	return errors.New("netstack: ConfigureRoutes not implemented on this platform")
}

// UnconfigureRoutes is a non-Linux/darwin stub.
func UnconfigureRoutes(cfg RouteConfig) {}
