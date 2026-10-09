package sidecartui

import (
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"syscall"

	configyaml "github.com/hoophq/hoop/sidecar/config/yaml"
	"github.com/hoophq/hoop/sidecar/daemon"
)

// Validation reads a config; it binds nothing. A config can therefore be
// valid and still fail the moment it boots, because another program holds
// a port it listens on. The first-run screen checks the ports before it
// boots, says which one is taken, and offers a free one, so that failure
// is a question on the screen and not an error after it closes.

// portUse is one address the booted sidecar will bind.
type portUse struct {
	what string // "listener postgres", "admin", "MCP server", "demo API"
	addr string
	// demo marks the demo API's address, which the file names under the
	// demo key and as the demo listener's upstream.
	demo bool
}

// portConflict is a portUse another program holds, and the address
// offered instead ("" when no free port was found nearby).
type portConflict struct {
	portUse
	inUse bool // false: the bind failed for another reason (permission)
	free  string
}

// bindError is why addr cannot be bound now, nil when it can.
func bindError(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	_ = ln.Close()
	return nil
}

// portUses lists the TCP addresses cfg binds: every tcp listener, the
// admin and MCP servers, and the demo API when the file names one.
func portUses(cfg *daemon.Config, demoAPI string) []portUse {
	var out []portUse
	for i, l := range cfg.Listeners {
		if l.Network == "unix" || l.Listen == "" {
			continue
		}
		name := l.Name
		if name == "" {
			name = fmt.Sprintf("listener[%d]", i)
		}
		out = append(out, portUse{what: "listener " + name, addr: l.Listen})
	}
	if cfg.Admin.Listen != "" {
		out = append(out, portUse{what: "admin server", addr: cfg.Admin.Listen})
	}
	if cfg.MCP != nil && cfg.MCP.Listen != "" {
		out = append(out, portUse{what: "MCP server", addr: cfg.MCP.Listen})
	}
	if demoAPI != "" {
		out = append(out, portUse{what: "demo API", addr: demoAPI, demo: true})
	}
	return out
}

// portConflicts loads the config at path and reports each address it would
// bind that cannot be bound now, with a free one offered for each.
func portConflicts(path string) ([]portConflict, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg, err := configyaml.Load(path)
	if err != nil {
		return nil, err
	}
	demoAPI, _, _ := configyaml.ExtensionValue(data, configyaml.DemoAPIKey)
	uses := portUses(cfg, demoAPI)
	taken := map[string]bool{}
	for _, u := range uses {
		taken[u.addr] = true
	}
	var out []portConflict
	for _, u := range uses {
		err := bindError(u.addr)
		if err == nil {
			continue
		}
		c := portConflict{portUse: u, inUse: errors.Is(err, syscall.EADDRINUSE)}
		c.free = freeNear(u.addr, taken)
		if c.free != "" {
			taken[c.free] = true
		}
		out = append(out, c)
	}
	return out, nil
}

// freeNear is the first address after addr, on the same host, that binds
// now and that nothing else in the config uses; "" when none of the next
// 200 ports does.
func freeNear(addr string, taken map[string]bool) string {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return ""
	}
	for p := port + 1; p <= min(port+200, 65535); p++ {
		cand := net.JoinHostPort(host, strconv.Itoa(p))
		if !taken[cand] && bindError(cand) == nil {
			return cand
		}
	}
	return ""
}

// freeFrom is addr when it binds now, else the next free one after it, so
// a config the setup screens write starts on ports nothing holds.
func freeFrom(addr string, taken map[string]bool) string {
	if !taken[addr] && bindError(addr) == nil {
		return addr
	}
	if f := freeNear(addr, taken); f != "" {
		return f
	}
	return addr
}

// usePorts rewrites the file at path to bind each conflict's free address.
// Only the lines that name the address under a key that binds it change
// (listen, and for the demo API its key and the upstream that points at
// it), so the person's comments and layout survive.
func usePorts(path string, conflicts []portConflict) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file; change its ports there", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, c := range conflicts {
		if c.free == "" {
			return fmt.Errorf("no free port was found near %s", c.addr)
		}
		keys := "listen"
		if c.demo {
			keys = "upstream|" + regexp.QuoteMeta(configyaml.DemoAPIKey)
		}
		re := regexp.MustCompile(`(?m)^(\s*(?:-\s+)?(?:` + keys + `):\s*["']?)` + regexp.QuoteMeta(c.addr) + `(["']?\s*(?:#.*)?)$`)
		next := re.ReplaceAll(data, []byte("${1}"+c.free+"${2}"))
		if string(next) == string(data) {
			return fmt.Errorf("%s does not name %s on a line it can change; edit it there", path, c.addr)
		}
		data = next
	}
	return replaceFile(path, data, fi.Mode().Perm())
}
