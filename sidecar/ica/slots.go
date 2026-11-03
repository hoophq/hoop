package ica

import (
	"errors"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/libhoop-ica/ica"
	"net"
	"net/netip"
	"strconv"
)

func tablePrefixes(values []string) []netip.Prefix {
	var out []netip.Prefix
	for _, v := range values {
		p, e := netip.ParsePrefix(v)
		if e == nil {
			out = append(out, p)
		}
	}
	return out
}
func slotTable(lane daemon.ICALane) (*ica.SlotTable, error) {
	s := lane.Slots
	_, p, e := net.SplitHostPort(lane.Listen)
	n, _ := strconv.Atoi(p)
	if e != nil || n != s.FirstPort {
		return nil, errors.New("slot listen port must equal first_port")
	}
	if s.FirstPort <= 20999 && s.FirstPort+s.Count-1 >= 20000 {
		return nil, errors.New("slot ports overlap API data range")
	}
	front, e := netip.ParsePrefix(s.FrontendCIDR)
	if e != nil {
		return nil, e
	}
	prefixes := tablePrefixes(s.UpstreamCIDRs)
	if len(prefixes) != len(s.UpstreamCIDRs) || s.UpstreamPort < 1 || s.UpstreamPort > 65535 {
		return nil, errors.New("invalid slot policy")
	}
	return ica.NewSlotTable(ica.SlotRule{FirstPort: s.FirstPort, Count: s.Count, Pattern: s.UpstreamPattern, Names: s.Names, UpstreamPort: uint16(s.UpstreamPort), FrontendCIDR: front, UpstreamCIDRs: prefixes}, nil)
}
