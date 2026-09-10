package daemon

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	codecgrpc "github.com/hoophq/libhoop/v2/codec/grpc"
)

// grpcDiscoverTimeout bounds one -grpc-discover run. Reflection is a few
// small messages; a fetch that needs longer than this is a wrong address,
// not a big schema.
const grpcDiscoverTimeout = 30 * time.Second

// DiscoverGRPC implements -grpc-discover: dial the named grpc lane's
// upstream with the lane's own upstream_tls facts, fetch its descriptor set
// over gRPC server reflection, and report every method with its maskable
// field paths. With out set, the serialized set lands there for the
// operator to review and pin as listeners[].grpc.descriptors.
//
// It reads the lane's config rather than taking an address flag so
// discovery dials exactly what the lane will dial — same upstream, same
// TLS material — and a config mistake surfaces here instead of at the
// first proxied RPC. Runtime lanes never touch reflection; ADR-0013 keeps
// them on pinned files.
func DiscoverGRPC(ctx context.Context, cfg *Config, name, out string, w io.Writer) error {
	var lc *ListenerConfig
	var grpcNames []string
	for i := range cfg.Listeners {
		l := &cfg.Listeners[i]
		if !isGRPC(*l) {
			continue
		}
		n := l.displayName(i)
		grpcNames = append(grpcNames, n)
		if n == name {
			lc = l
		}
	}
	if lc == nil {
		if len(grpcNames) == 0 {
			return fmt.Errorf("-grpc-discover needs a grpc listener in the config; " +
				"it dials the lane's upstream with the lane's upstream_tls")
		}
		return fmt.Errorf("no grpc listener named %q; the config defines: %s",
			name, strings.Join(grpcNames, ", "))
	}

	tlsConf, err := lc.UpstreamTLS.BuildTLS()
	if err != nil {
		return fmt.Errorf("%s: upstream_tls: %w", name, err)
	}

	ctx, cancel := context.WithTimeout(ctx, grpcDiscoverTimeout)
	defer cancel()
	schema, raw, err := codecgrpc.Discover(ctx, lc.Upstream, tlsConf)
	if err != nil {
		return err
	}

	if out != "" {
		if err := os.WriteFile(out, raw, 0o600); err != nil {
			return fmt.Errorf("writing the descriptor set: %w", err)
		}
	}

	// Same single-write discipline as PrintLanes: this report is what the
	// operator reads to author mask rules, and half of it must not pass
	// for all of it.
	var b strings.Builder
	methods := schema.MethodPaths()
	fmt.Fprintf(&b, "%s: %s serves %d method(s)\n", name, lc.Upstream, len(methods))
	for _, m := range methods {
		fmt.Fprintf(&b, "  %s\n", m)
	}
	if out != "" {
		fmt.Fprintf(&b, "wrote %s (%d bytes); review it, then set listeners[].grpc.descriptors\n",
			out, len(raw))
	} else {
		fmt.Fprintln(&b, "dry run: re-run with -grpc-discover-out FILE to write the descriptor set")
	}
	body := b.String()
	n, err := io.WriteString(w, body)
	if err == nil && n != len(body) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return fmt.Errorf("writing the discovery report: %w", err)
	}
	return nil
}
