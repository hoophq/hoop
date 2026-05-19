//go:build !linux

package netstack

import "errors"

func openTUN(requestedName string, mtu uint32) (tunDevice, error) {
	return nil, errors.New("netstack: TUN device support is currently Linux-only (RD-176 spike)")
}

// ConfigureRoutes is a non-Linux stub.
func ConfigureRoutes(deviceName string, prefix string, gateway string) error {
	return errors.New("netstack: ConfigureRoutes not implemented on this platform")
}

// UnconfigureRoutes is a non-Linux stub.
func UnconfigureRoutes(deviceName string, prefix string, gateway string) {}
