package daemon

import (
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

var icaSlotPattern = regexp.MustCompile(`%d|%03d`)
var icaSlotLabel = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

func (s *ICASlotsConfig) validate(lc ListenerConfig, sources []string, name string) []string {
	bad := func(why string) []string { return []string{name + ": ica.slots: " + why} }
	if s.FirstPort < 1024 || s.FirstPort > 65535 || s.Count < 1 || s.Count > 250 || s.FirstPort+s.Count-1 > 65535 || s.UpstreamPort < 1 || s.UpstreamPort > 65535 {
		return bad("invalid port range, count or upstream port")
	}
	if s.FirstPort <= 20999 && s.FirstPort+s.Count-1 >= 20000 {
		return bad("ports overlap engine API data range 20000..20999")
	}
	host, port, e := net.SplitHostPort(lc.Listen)
	n, _ := strconv.Atoi(port)
	if e != nil || n != s.FirstPort {
		return bad("listen must name first_port")
	}
	if host != "" {
		if _, e := netip.ParseAddr(host); e != nil {
			return bad("listen host must be a literal IP")
		}
	}
	if len(sources) == 0 {
		return bad("source_cidrs is required")
	}
	front, e := netip.ParsePrefix(s.FrontendCIDR)
	if e != nil || front.Addr().Is4In6() {
		return bad("invalid frontend_cidr")
	}
	prefixes, e := parsePrefixes(s.UpstreamCIDRs)
	if e != nil || len(prefixes) == 0 {
		return bad("upstream_cidrs is required and must be valid")
	}
	for _, p := range prefixes {
		if p.Addr().Is4In6() || p.Overlaps(front) {
			return bad("frontend and upstream networks must be disjoint")
		}
	}
	if (s.UpstreamPattern == "") == (len(s.Names) == 0) {
		return bad("set exactly one upstream_pattern or names list")
	}
	if len(s.Names) > 0 && len(s.Names) != s.Count {
		return bad("names count differs from count")
	}
	if s.UpstreamPattern != "" && (strings.Count(s.UpstreamPattern, "%") != 1 || len(icaSlotPattern.FindAllStringIndex(s.UpstreamPattern, -1)) != 1) {
		return bad("pattern needs one %d or %03d")
	}
	seen := map[string]bool{}
	for i := 1; i <= s.Count; i++ {
		var h string
		if s.UpstreamPattern != "" {
			h = fmt.Sprintf(s.UpstreamPattern, i)
		} else {
			h = s.Names[i-1]
		}
		h = strings.ToLower(strings.TrimSuffix(h, "."))
		if len(h) > 253 || !strings.Contains(h, ".") || net.ParseIP(h) != nil {
			return bad("VDA names must be fully qualified hostnames")
		}
		for _, l := range strings.Split(h, ".") {
			if !icaSlotLabel.MatchString(l) {
				return bad("invalid hostname")
			}
		}
		if seen[h] {
			return bad("duplicate hostname")
		}
		seen[h] = true
	}
	return nil
}

// Extra slot ports do not appear as separate listeners in the document.
// Refuse overlapping ranges before any runtime or sockets start.
func (c *Config) validateICASlotBinds() []string {
	var out []string
	for i, l := range c.Listeners {
		if l.ICA == nil || l.ICA.Slots == nil {
			continue
		}
		s := l.ICA.Slots
		for j, other := range c.Listeners {
			if i == j || other.Network == "unix" {
				continue
			}
			_, p, e := net.SplitHostPort(other.listenAddress())
			n, _ := strconv.Atoi(p)
			if e != nil {
				continue
			}
			end := n
			if other.ICA != nil && other.ICA.Slots != nil {
				end = n + other.ICA.Slots.Count - 1
			}
			if n <= s.FirstPort+s.Count-1 && end >= s.FirstPort {
				out = append(out, l.displayName(i)+": slot ports overlap another listener")
			}
		}
		// Admin's production default; an explicit admin address is checked too.
		admin := c.Admin.Listen
		if admin == "" {
			admin = "127.0.0.1:19000"
		}
		_, p, _ := net.SplitHostPort(admin)
		n, _ := strconv.Atoi(p)
		if n >= s.FirstPort && n < s.FirstPort+s.Count {
			out = append(out, l.displayName(i)+": slot ports overlap admin listener")
		}
	}
	return out
}
