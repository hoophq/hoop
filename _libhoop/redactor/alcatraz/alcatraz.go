// Package redactoralcatraz is the open-source stub for the enterprise
// alcatraz redactor client. The real implementation (the in-process alcatraz
// pattern engine and its NER seam) lives in the private libhoop module; this
// stub carries only the registration surface the agent calls at startup so
// the OSS build compiles.
//
// There is no redactor behind it: the OSS mirror of libhoop/redactor exposes
// data types only, so an OSS build never constructs an alcatraz client and
// never consults a backend registered here.
package redactoralcatraz

// NlpBackend is the seam through which a statistical NER backend plugs into
// the alcatraz client. The enterprise build defines it in terms of the
// github.com/hoophq/alcatraz analyzer interfaces; naming those here would
// make the OSS stub depend on the engine it exists to leave out, so it stays
// an unconstrained placeholder — nothing in this build produces or consumes
// a value of it.
type NlpBackend any

// SetNlpProvider does nothing in this build. It exists so the agent's startup
// wiring compiles: with no alcatraz client to serve sessions, there is
// nothing a provider could be registered into.
func SetNlpProvider(provider func() (NlpBackend, error)) {}
