package loginflow

import "net"

// defaultListen is a thin var indirection over net.Listen so tests can
// stub it. Production code passes the real network/address through; the
// callback server always uses "tcp" because the OAuth redirect comes
// from a real browser regardless of platform.
var defaultListen = func(network, addr string) (net.Listener, error) {
	return net.Listen(network, addr)
}
