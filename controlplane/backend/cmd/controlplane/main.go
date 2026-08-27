// Command controlplane is the control plane server for hoopinspect sidecars.
//
// It stores what should be running on each hoopinspect sidecar, tracks what
// actually is, and pushes the difference over a WebSocket that the sidecar
// dials out to. It terminates no database traffic itself: when this process
// is down, sidecars keep enforcing the last config they accepted.
//
// Usage:
//
//	controlplane serve              start the API server (default)
//	controlplane migrate up         apply pending migrations
//	controlplane migrate down [n]   roll back n migrations, default 1
//	controlplane migrate version    print the applied schema version
//
// Run it with POSTGRES_DB_URI set. Everything else has a default. See
// internal/config for the full list.
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

	"github.com/hoophq/hoop/controlplane/backend/internal/config"
	"github.com/hoophq/hoop/controlplane/backend/internal/database"
	"github.com/hoophq/hoop/controlplane/backend/internal/httpapi"
	"github.com/hoophq/hoop/controlplane/backend/internal/logging"
	"github.com/hoophq/hoop/controlplane/backend/internal/migrations"
)

// version is injected at build time with
// -ldflags "-X main.version=$(VERSION)". The literal below is what a plain
// `go build` produces, and it says so rather than pretending to be a release.
var version = "devel"

func main() {
	// The logger is built before config is loaded, because config loading is
	// the first thing that can fail and its error has to go somewhere. It
	// reads LOG_LEVEL and LOG_FORMAT directly for the same reason.
	logger := logging.FromEnv(os.Stderr)
	// Anything in a dependency that reaches for slog.Default lands in the
	// same stream, in the same format, instead of stderr text.
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

// migrateCmd exists so a deploy pipeline can run the schema change as its own
// step, separate from starting the process.
//
// The gateway migrates only as a boot side effect, which means a rolling
// deploy of a schema change has every replica racing to apply it. An advisory
// lock keeps that from corrupting anything and does not make it a good
// deployment shape. Here it is a command, and CONTROLPLANE_AUTO_MIGRATE=false
// turns the boot-time run off once a pipeline owns it.
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

// serve holds the boot sequence so every failure returns instead of calling
// os.Exit from deep in the stack, which would skip deferred cleanup.
//
// The order is migrate, connect, listen. Migrating before opening the
// application's own pool means a schema change and the first query cannot
// interleave, and migrations run on a connection golang-migrate closes when
// it is done rather than one held out of the pool it is about to hand over.
func serve(logger *slog.Logger, cfg config.Config) error {
	logger.Info("configuration loaded",
		"version", version,
		"deployment", cfg.Deployment,
		"listen_addr", cfg.ListenAddr,
		"auto_migrate", cfg.AutoMigrate,
	)

	// Gin defaults to debug mode, which prints the whole route table and a
	// warning on every start. Set here rather than in Engine because
	// gin.SetMode is process-global and a test that builds an engine must not
	// change the mode out from under the rest of the suite.
	if cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	runner := migrations.NewRunner(logger, cfg.PostgresURI, cfg.MigrationPathFiles)
	if cfg.AutoMigrate {
		if err := runner.Up(); err != nil {
			return err
		}
	}
	// Checked whether or not this process applied them. Serving against an
	// older schema means every query touching a new column fails at request
	// time, one endpoint at a time, which reads as a bug in whichever feature
	// was unlucky enough to be called first.
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

	// Fail at boot if the database is unreachable, rather than at the first
	// request. A process that reports healthy and then 500s every call is
	// worse than one that refuses to start.
	pingCtx, cancelPing := context.WithTimeout(context.Background(), database.PingTimeout)
	err = database.Ping(pingCtx, db)
	cancelPing()
	if err != nil {
		return err
	}
	logger.Info("database connection established", "schema", database.Schema)

	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// The second signal has to kill the process outright, because an operator
	// pressing Ctrl-C twice means now. signal.NotifyContext alone does not do
	// that: its goroutine exits after the first delivery, the registration
	// stays in place, and every later signal is swallowed. Restoring the
	// default disposition once the first one arrives is what makes the second
	// one work.
	go func() {
		<-signalCtx.Done()
		stop()
	}()

	return httpapi.New(cfg, db, logger, version).Run(signalCtx)
}
