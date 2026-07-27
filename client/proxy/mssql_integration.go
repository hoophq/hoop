//go:build integration

package proxy

import "errors"

// MSSQLServerListenerAddrForIntegration returns the address assigned by the
// kernel after Serve binds the MSSQL listener. Production callers use Host;
// integration tests use this helper when requesting an ephemeral port.
func MSSQLServerListenerAddrForIntegration(server *MSSQLServer) (string, error) {
	if server == nil || server.listener == nil {
		return "", errors.New("mssql listener is not initialized")
	}
	return server.listener.Addr().String(), nil
}
