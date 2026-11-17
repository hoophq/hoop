package daemon

import (
	"net"
	"net/http"
	"net/http/pprof"
	"runtime"
	"time"
)

func loopbackListen(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	return err == nil && loopbackHost(host)
}

// registerPprof serves the standard Go profiles under /debug/pprof/. Block
// and mutex sampling are off by default, so their profiles stay empty
// unless they start here. cmdline is not served: arguments can hold paths.
func registerPprof(mux *http.ServeMux) {
	runtime.SetBlockProfileRate(int(time.Millisecond))
	runtime.SetMutexProfileFraction(5)

	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
}
