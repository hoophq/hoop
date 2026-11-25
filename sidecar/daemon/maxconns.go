package daemon

import "fmt"

// DefaultMaxConns is the connection limit of a listener that leaves
// max_conns unset. Unlimited let an overload grow memory with the open
// sessions until the OOM kill, and the restart reconnected every client at
// once (ENG-1231). 200 per listener fits the smallest row of the sizing
// table: three listeners in 128Mi / 256Mi.
const DefaultMaxConns = 200

// MaxConnsUnlimited is the max_conns value that removes the limit.
const MaxConnsUnlimited = -1

// connLimit is the concurrent connection limit a lane enforces, in the
// convention of proxy.Config.MaxConns and libhoop's max_conns option, where
// 0 is no limit. Unset gives DefaultMaxConns and a positive value is kept.
// Any negative value is no limit: every release before the default read it
// so, and a stored document may hold one below -1 (see MaxConnsProblems).
func (lc ListenerConfig) connLimit() int {
	switch {
	case lc.MaxConns > 0:
		return lc.MaxConns
	case lc.MaxConns < 0:
		return 0
	default:
		return DefaultMaxConns
	}
}

// MaxConnsProblems names each listener whose max_conns is below -1. The
// control plane refuses such a document on save. A sidecar does not: it
// reads the value as -1, as every release before it did, and says so in
// the lane's notes (maxConnsNote). Refusing it there would stop a sidecar
// whose stored document booted on the release before. An mcp lane is left
// to its own check, which refuses only a positive value.
func MaxConnsProblems(listeners []ListenerConfig) []string {
	var out []string
	for i, lc := range listeners {
		if lc.MaxConns < MaxConnsUnlimited && !isMCPGateway(lc) {
			out = append(out, fmt.Sprintf("%s: max_conns %d is below -1; -1 is unlimited, and unset uses %d",
				lc.displayName(i), lc.MaxConns, DefaultMaxConns))
		}
	}
	return out
}

// maxConnsNote is the lane note for a max_conns below -1, or "".
func maxConnsNote(lc ListenerConfig) string {
	if lc.MaxConns >= MaxConnsUnlimited || isMCPGateway(lc) {
		return ""
	}
	return fmt.Sprintf("max_conns %d is below -1 and reads as -1, unlimited; write -1", lc.MaxConns)
}
