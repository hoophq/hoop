// Command stub-gateway is a single-process local server that satisfies the
// RD-176 spike's "minimal gateway-side changes" requirement. It accepts
// tunnel WebSocket sessions and forwards each opened stream to a target
// listed in --targets.
//
// Example:
//
//	stub-gateway -listen :7575 -targets pg-prod=127.0.0.1:5432,redis=127.0.0.1:6379
//
// Targets can also be loaded from a CSV file via -targets-file.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/hoophq/hoop/tunnel/stub"
)

func main() {
	listen := flag.String("listen", ":7575", "address to listen on")
	targetsFlag := flag.String("targets", "", "comma-separated name=host:port mappings")
	targetsFile := flag.String("targets-file", "", "optional file with one name=host:port per line")
	flag.Parse()

	targets, err := loadTargets(*targetsFlag, *targetsFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "stub-gateway:", err)
		os.Exit(2)
	}
	if len(targets) == 0 {
		fmt.Fprintln(os.Stderr, "stub-gateway: no targets configured (-targets / -targets-file)")
		os.Exit(2)
	}

	logger := log.New(os.Stderr, "stub-gateway ", log.LstdFlags|log.Lmicroseconds)
	srv := stub.NewServer(targets, logger)

	logger.Printf("listening on %s with %d target(s):", *listen, len(targets))
	for name, t := range targets {
		logger.Printf("  %s -> %s://%s", name, t.Network, t.Address)
	}
	if err := http.ListenAndServe(*listen, srv); err != nil {
		logger.Fatalf("listen: %v", err)
	}
}

func loadTargets(flagValue, filePath string) (map[string]stub.Target, error) {
	out := make(map[string]stub.Target)
	if flagValue != "" {
		for _, kv := range strings.Split(flagValue, ",") {
			name, addr, ok := strings.Cut(strings.TrimSpace(kv), "=")
			if !ok || name == "" || addr == "" {
				return nil, fmt.Errorf("invalid target %q (expected name=host:port)", kv)
			}
			out[name] = stub.Target{Address: addr, Network: "tcp"}
		}
	}
	if filePath == "" {
		return out, nil
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, addr, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: invalid line %q", filePath, i+1, line)
		}
		out[strings.TrimSpace(name)] = stub.Target{Address: strings.TrimSpace(addr), Network: "tcp"}
	}
	return out, nil
}
