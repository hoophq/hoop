package daemon

import (
	"fmt"
	"net"
	"strings"
)

// HealthListenEnv is the address of the health listener: GET /healthz and
// GET /readyz, and nothing else. Unset, no health listener binds.
//
// It exists so a deployment can probe a port other than admin.listen. Like
// admin.listen, it serves no audit data or rules: those are on
// admin.api_listen. It answers whether the process is up and, from /readyz,
// the process status as JSON (or the ICA inspection attestation).
//
// The run path binds it at process start (ListenHealth), so it answers while
// the boot waits for its control plane.
//
// An environment variable and never a config key: the probe belongs to the
// deployment that declares it, not to a document a control plane serves.
const HealthListenEnv = "HOOP_SIDECAR_HEALTH_LISTEN"

// checkHealthListen refuses a health address that is not host:port, or whose
// port another listener of cfg also binds: a lane, a lane's ICA slot range,
// the admin server or the MCP server. Run calls it before anything binds, so
// the operator reads the conflict instead of a bind error from whichever
// listener lost the race.
func checkHealthListen(cfg *Config, addr string) error {
	if addr == "" {
		return nil
	}
	host, p, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%s %q: want host:port: %w", HealthListenEnv, addr, err)
	}
	port, err := net.LookupPort("tcp", p)
	if err != nil {
		return fmt.Errorf("%s %q: %q is not a port number", HealthListenEnv, addr, p)
	}
	if port == 0 {
		return nil
	}
	clash := func(what string) error {
		return fmt.Errorf("%s %q: port %d is also %s; pick another port", HealthListenEnv, addr, port, what)
	}
	mcpListen := ""
	if cfg.MCP != nil {
		mcpListen = cfg.MCP.Listen
	}
	for what, other := range map[string]string{
		"the admin listener (admin.listen)":         cfg.Admin.Listen,
		"the admin API listener (admin.api_listen)": cfg.Admin.APIListen,
		"the MCP server (mcp.listen)":               mcpListen,
	} {
		if oh, n, ok := listenHostPort(other); ok && n == port && sameBindHost(host, oh) {
			return clash(what)
		}
	}
	for i, l := range cfg.Listeners {
		if l.Network == "unix" {
			continue
		}
		lh, first, ok := listenHostPort(l.listenAddress())
		if !ok || !sameBindHost(host, lh) {
			continue
		}
		last := first
		// Slot mode binds first_port through first_port+count-1 on the
		// lane's host; Validate makes listen name first_port.
		if l.ICA != nil && l.ICA.Slots != nil {
			first, last = l.ICA.Slots.FirstPort, l.ICA.Slots.FirstPort+l.ICA.Slots.Count-1
		}
		if port >= first && port <= last {
			return clash("bound by " + l.displayName(i))
		}
	}
	return nil
}

// listenHostPort splits a host:port and resolves the port the way net.Listen
// does: a service name or a zero-padded number names the same port as its
// decimal.
func listenHostPort(addr string) (string, int, bool) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, false
	}
	n, err := net.LookupPort("tcp", p)
	return h, n, err == nil && n != 0
}

// sameBindHost reports whether two listeners on one port would collide. An
// empty host or "::" binds every address of both families; "0.0.0.0" binds
// every IPv4 address and leaves IPv6 free. Otherwise two literals collide when
// equal, and a name collides with the same name. Names are not resolved: an
// alias the check misses still fails at bind, loudly, as before.
func sameBindHost(a, b string) bool {
	ia, ib := net.ParseIP(a), net.ParseIP(b)
	allFamilies := func(h string, ip net.IP) bool {
		return h == "" || (ip != nil && ip.IsUnspecified() && ip.To4() == nil)
	}
	if allFamilies(a, ia) || allFamilies(b, ib) {
		return true
	}
	anyV4 := func(ip net.IP) bool { return ip != nil && ip.IsUnspecified() }
	// A name may resolve to IPv4, so it counts as one against 0.0.0.0.
	v4OrName := func(ip net.IP) bool { return ip == nil || ip.To4() != nil }
	if (anyV4(ia) && v4OrName(ib)) || (anyV4(ib) && v4OrName(ia)) {
		return true
	}
	if ia != nil && ib != nil {
		return ia.Equal(ib)
	}
	return strings.EqualFold(a, b)
}
