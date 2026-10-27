// Package login registers the postgres, mssql, mongodb and oracle login
// drivers with proxy.RegisterLogin from init. Linking it (the sidecar/auth
// module does) is what lets a lane with identity or credentials terminate
// those logins on the relay.
//
// mysql stays in the relay (sidecar/proxy): its driver is welded to the
// upstream TLS bridge the relay runs without any login termination.
package login
