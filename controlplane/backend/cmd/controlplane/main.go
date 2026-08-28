// Command controlplane runs the hoop control plane API.
//
// Scaffold. It serves a health check and nothing else. What the control plane
// does, and how, is still under discussion: see controlplane/backend/CLAUDE.md.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/hoophq/hoop/controlplane/backend/internal/api"
	"github.com/hoophq/hoop/controlplane/backend/internal/config"
	"github.com/hoophq/hoop/controlplane/backend/internal/logging"
)

// version is injected at build time with -ldflags.
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "controlplane: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// The logger is built before config is loaded, or the errors config
	// loading itself produces have nowhere to go.
	logger := logging.FromEnv(os.Stderr)

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// SIGTERM is what a container runtime sends first. SIGINT is Ctrl-C.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return api.New(cfg, logger, version).Run(ctx)
}
