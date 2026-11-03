// twins runs one twin or the router, or mints the test PKI:
//
//	twins pki    -dir /pki -leaf oidc=oidc-twin,localhost,127.0.0.1
//	twins oidc   -issuer https://oidc-twin -browser-url http://127.0.0.1:18081 -cert /pki/oidc.crt -key /pki/oidc.key
//	twins router -route oidc-twin=oidc-twin:443
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/hoophq/hoop/twins/kit"
	"github.com/hoophq/hoop/twins/oidc"
	"github.com/hoophq/hoop/twins/router"
)

type multi []string

func (m *multi) String() string     { return strings.Join(*m, " ") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: twins pki|oidc|router [flags]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	tlsAddr := fs.String("tls", ":443", "TLS listener, the surface hoop calls")
	adminAddr := fs.String("admin", ":8080", "plain HTTP listener for /_twin/*")
	cert := fs.String("cert", "", "certificate file")
	key := fs.String("key", "", "key file")
	var err error
	switch os.Args[1] {
	case "pki":
		dir := fs.String("dir", "/pki", "output directory")
		var leaves multi
		fs.Var(&leaves, "leaf", "name=host1,host2 (repeatable)")
		fs.Parse(os.Args[2:])
		m := map[string][]string{}
		for _, l := range leaves {
			name, hosts, ok := strings.Cut(l, "=")
			if !ok || name == "" || hosts == "" {
				fatal(fmt.Errorf("leaf %q is not name=host1,host2", l))
			}
			m[name] = strings.Split(hosts, ",")
		}
		err = kit.WritePKI(*dir, m)
	case "oidc":
		issuer := fs.String("issuer", "https://oidc-twin", "issuer URL, as hoop is configured")
		browserURL := fs.String("browser-url", "", "where a browser reaches this twin; discovery advertises authorize there (default: the issuer)")
		fs.Parse(os.Args[2:])
		var t *oidc.Twin
		if t, err = oidc.New(*issuer, *browserURL); err == nil {
			err = kit.Serve(t, *tlsAddr, *adminAddr, *cert, *key)
		}
	case "router":
		var routes multi
		fs.Var(&routes, "route", "host=target (repeatable)")
		listen := fs.String("listen", ":3128", "proxy and admin listener")
		fs.Parse(os.Args[2:])
		var r *router.Router
		if r, err = router.New(routes); err == nil {
			err = r.Serve(*listen)
		}
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	slog.Error("twins stopped", "cmd", os.Args[1], "err", err)
	os.Exit(1)
}
