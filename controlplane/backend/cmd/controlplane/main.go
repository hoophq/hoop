// Command controlplane is the control plane server for hoop sidecars.
//
// Sidecars poll it for config and report status; it never dials customer
// infrastructure or terminates database traffic, so sidecars keep enforcing
// their last accepted config while it is down.
//
// Usage:
//
//	controlplane serve              start the API server (default)
//	controlplane migrate up         apply pending migrations
//	controlplane migrate down [n]   roll back n migrations, default 1
//	controlplane migrate version    print the applied schema version
//
// Requires POSTGRES_DB_URI; everything else defaults. See internal/config.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/hoophq/hoop/controlplane/backend/internal/api"
	"github.com/hoophq/hoop/controlplane/backend/internal/api/adminauth"
	"github.com/hoophq/hoop/controlplane/backend/internal/api/desiredstate"
	"github.com/hoophq/hoop/controlplane/backend/internal/api/inventory"
	"github.com/hoophq/hoop/controlplane/backend/internal/api/sidecarauth"
	"github.com/hoophq/hoop/controlplane/backend/internal/config"
	"github.com/hoophq/hoop/controlplane/backend/internal/database"
	"github.com/hoophq/hoop/controlplane/backend/internal/logging"
	"github.com/hoophq/hoop/controlplane/backend/internal/migrations"
)

// version is injected at build time via -ldflags "-X main.version=$(VERSION)".
var version = "devel"

func main() {
	// Built before config loads so config errors have somewhere to go;
	// reads LOG_LEVEL/LOG_FORMAT directly for the same reason.
	logger := logging.FromEnv(os.Stderr)
	// Dependencies using slog.Default land in the same stream and format.
	slog.SetDefault(logger)

	if err := run(logger, os.Args[1:]); err != nil {
		logger.Error("control plane exited with an error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger, args []string) error {
	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	switch cmd {
	case "serve":
		return serve(logger, cfg)
	case "migrate":
		return migrateCmd(logger, cfg, args)
	case "-h", "--help", "help":
		fmt.Fprint(os.Stderr, usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q\n%s", cmd, usage)
	}
}

const usage = `usage: controlplane <command>

  serve              start the API server (default)
  migrate up         apply pending migrations
  migrate down [n]   roll back n migrations, default 1
  migrate version    print the applied schema version
`

// migrateCmd lets a deploy pipeline run schema changes as a separate step;
// CONTROLPLANE_AUTO_MIGRATE=false disables the boot-time run once it does.
func migrateCmd(logger *slog.Logger, cfg config.Config, args []string) error {
	runner := migrations.NewRunner(logger, cfg.PostgresURI, cfg.MigrationPathFiles)

	sub := ""
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "up":
		return runner.Up()
	case "down":
		steps := 1
		if len(args) > 0 {
			n, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("migrate down takes a number of steps, got %q", args[0])
			}
			steps = n
		}
		return runner.Down(steps)
	case "version":
		applied, dirty, err := runner.Version()
		if err != nil {
			return err
		}
		latest, err := runner.Latest()
		if err != nil {
			return err
		}
		fmt.Printf("applied=%d latest=%d dirty=%t\n", applied, latest, dirty)
		return nil
	default:
		return fmt.Errorf("unknown migrate subcommand %q (want up, down or version)", sub)
	}
}

// serve returns on every failure instead of calling os.Exit, so deferred
// cleanup runs. Order is migrate, connect, listen: migrating before opening
// the pool keeps schema changes and first queries from interleaving.
func serve(logger *slog.Logger, cfg config.Config) error {
	logger.Info("configuration loaded",
		"version", version,
		"deployment", cfg.Deployment,
		"listen_addr", cfg.ListenAddr,
		"auto_migrate", cfg.AutoMigrate,
	)

	// gin.SetMode is process-global; set it here, not in Engine, so tests
	// that build an engine do not change the mode under the suite.
	if cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	runner := migrations.NewRunner(logger, cfg.PostgresURI, cfg.MigrationPathFiles)
	if cfg.AutoMigrate {
		if err := runner.Up(); err != nil {
			return err
		}
	}
	// Verified even when this process did not migrate: serving against an
	// older schema fails one endpoint at a time at request time.
	if err := runner.Verify(); err != nil {
		return err
	}

	db, err := database.Open(cfg.PostgresURI, cfg.MaxOpenConns)
	if err != nil {
		return err
	}
	defer func() {
		if err := database.Close(db); err != nil {
			logger.Warn("failed closing database pool", "error", err)
		}
	}()
	logger.Info("database pool opened", "schema", database.Schema)

	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// A second signal must kill the process outright. NotifyContext's
	// goroutine exits after the first delivery and swallows later signals;
	// calling stop() restores the default disposition so Ctrl-C twice works.
	go func() {
		<-signalCtx.Done()
		stop()
	}()

	server, err := api.New(deps(cfg, db, logger))
	if err != nil {
		return err
	}
	return server.Run(signalCtx)
}

// deps builds the object graph. Constructor injection, assembled only here:
// main alone knows every concrete type, keeping the packages below unaware
// of each other. No DI framework — a reflective container turns a missing
// dependency from a build error into a first-request panic; api.New
// validates and names what is missing.
func deps(cfg config.Config, db *gorm.DB, logger *slog.Logger) api.Deps {
	admin := adminauth.New()
	sidecars := sidecarauth.New()

	return api.Deps{
		Config:  cfg,
		Logger:  logger,
		Version: version,

		Readiness: database.NewPinger(db),

		RequireAdmin:     admin.RequireAdmin,
		RequireSidecar:   sidecars.RequireSidecar,
		RequireBootstrap: sidecars.RequireBootstrap,

		AdminAuth:    admin,
		DesiredState: desiredstate.New(),
		Inventory:    inventory.New(),
		SidecarAuth:  sidecars,
	}
}
