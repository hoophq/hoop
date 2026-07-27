// Package pglite runs an embedded PostgreSQL (PGlite, PostgreSQL 17.x
// compiled to WASI preview 1) inside the gateway process using the wazero
// runtime: pure Go, no CGO, no child process, no extracted executables.
//
// It is enabled by setting POSTGRES_DB_URI to a pglite:// URI pointing at a
// data directory (e.g. pglite:///var/lib/hoop/pgdata). The gateway then
// boots the embedded database and connects to it over a loopback TCP
// listener speaking the regular Postgres wire protocol, so the rest of the
// data layer (golang-migrate, GORM/pgx) is unchanged.
//
// Constraints inherited from PGlite's single-user architecture:
//   - one wire-protocol session at a time: the bridge serializes client
//     connections, and the gateway must cap its pool at one connection
//     (clients must connect with sslmode=disable; TLS and query
//     cancellation requests are not supported by the bridge)
//   - a single database (template1) — hoop's schema lives there
//   - the cluster runs with fsync=on: committed transactions reach the
//     WAL on the host filesystem. Close() performs a clean shutdown
//     (checkpoint); after a hard kill the next boot runs WAL crash
//     recovery, like a regular PostgreSQL
//
// The embedding contract (lifecycle exports, env vars and the socket-file
// wire transport) follows the upstream reference hosts:
// https://github.com/electric-sql/pglite-bindings (pglite-wasi-gateway.py)
// https://github.com/kshcherban/pglite-rust-bindings
//
// NOTE: requires a wazero version that allows fd_renumber onto stdio file
// descriptors. Until https://github.com/wazero/wazero/pull/2507 ships in a
// release, gateway/go.mod and client/go.mod pin a fork carrying the fix.
package pglite

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	_ "embed"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

//go:embed runtime/pglite-runtime.tar.gz
var runtimeArchive []byte

const (
	// guestPrefix is the runtime tree location from the guest's point of
	// view. The host runtime root is mounted as guest "/", so host
	// <root>/tmp/pglite == guest /tmp/pglite.
	guestPrefix = "tmp/pglite"

	// defaultPassword matches the "password" file shipped in the runtime
	// archive, used when that file is absent or empty.
	defaultPassword = "password"

	// database is the only database PGlite serves.
	database = "template1"

	// user is the bootstrap superuser baked into the runtime image.
	user = "postgres"
)

// Instance is a running embedded PGlite database.
type Instance struct {
	dsn      string
	root     string // host directory mounted as guest "/"
	ioBase   string // host path base for .s.PGSQL.5432{.in,.out,.lock.*}
	runtime  wazero.Runtime
	compiled wazero.CompiledModule
	listener net.Listener

	// mu guards the wasm module and all export calls: wazero's
	// api.Function is not safe for concurrent use, and the bridge
	// goroutine races Close().
	mu             sync.Mutex
	mod            api.Module
	interactiveOne api.Function
	clearError     api.Function // optional, may be nil
	pglClosed      api.Function // optional, may be nil
	closed         bool
}

// DSN returns a libpq-style connection URI for the embedded database.
//
// Note for ad-hoc consumers: hoop's data layer always schema-qualifies its
// objects (the migrations pin `SET search_path TO private`), because the
// single-user wasm backend ignores startup parameters and database-level
// settings, and its ambient search_path is a system schema that differs
// between first boot and resume. Unqualified DDL through this DSN would
// land in surprising schemas.
func (i *Instance) DSN() string { return i.dsn }

// MigrateDSN returns the connection URI golang-migrate must use. The
// migrations table is pinned to "public"."schema_migrations": the migrate
// postgres driver otherwise derives its schema from CURRENT_SCHEMA() at
// connect time, which on this backend is information_schema on first boot
// and pg_catalog on resumed boots — upgrade migrations would look for the
// version table in the wrong schema (or fail creating it in pg_catalog).
func (i *Instance) MigrateDSN() string {
	return i.dsn + "&x-migrations-table=%22public%22.%22schema_migrations%22&x-migrations-table-quoted=true"
}

// Start extracts the embedded runtime into dataDir (idempotent), boots the
// PGlite wasm module and starts the loopback wire-protocol bridge. The
// returned instance lives for the duration of the process; call Close for
// a clean database shutdown.
//
// Layout under dataDir:
//
//	runtime/tmp/pglite/...  runtime tree; PGDATA at runtime/tmp/pglite/base
//	cache/                  wazero compilation cache
func Start(ctx context.Context, dataDir string) (*Instance, error) {
	root := filepath.Join(dataDir, "runtime")
	if err := extractRuntime(root); err != nil {
		return nil, err
	}

	cacheDir := filepath.Join(dataDir, "cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, fmt.Errorf("failed creating wazero cache dir: %w", err)
	}
	cache, err := wazero.NewCompilationCacheWithDir(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("failed creating wazero compilation cache: %w", err)
	}

	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCompilationCache(cache))
	ok := false
	defer func() {
		if !ok {
			_ = rt.Close(ctx)
		}
	}()

	wasi_snapshot_preview1.MustInstantiate(ctx, rt)

	wasmBytes, err := os.ReadFile(filepath.Join(root, guestPrefix, "bin", "pglite.wasi"))
	if err != nil {
		return nil, fmt.Errorf("failed reading pglite wasm module: %w", err)
	}
	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		return nil, fmt.Errorf("failed compiling pglite wasm module: %w", err)
	}

	inst := &Instance{
		root:     root,
		ioBase:   filepath.Join(root, guestPrefix, "base", ".s.PGSQL.5432"),
		runtime:  rt,
		compiled: compiled,
	}
	if err := inst.bootModuleLocked(ctx); err != nil {
		return nil, err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed binding pglite bridge listener: %w", err)
	}
	inst.listener = listener
	inst.dsn = fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		user, readPassword(root), listener.Addr().String(), database)

	go inst.serveLoop()

	log.Infof("embedded pglite database started, data-dir=%v, addr=%v", dataDir, listener.Addr())
	ok = true
	return inst, nil
}

// Close performs a clean shutdown of the embedded database (shutdown
// checkpoint via pgl_shutdown) and releases the wasm runtime. The data
// directory is left intact for the next boot.
func (i *Instance) Close(ctx context.Context) error {
	_ = i.listener.Close()

	i.mu.Lock()
	defer i.mu.Unlock()
	i.closed = true
	if i.mod != nil && !i.mod.IsClosed() {
		// Flush dirty buffers first: this build's shutdown path cannot
		// complete its own checkpoint (it traps emitting the shutdown log
		// through the wire transport), so the next boot replays WAL crash
		// recovery — an explicit CHECKPOINT keeps that replay minimal.
		_ = i.sendFrameLocked(ctx, simpleQueryFrame("CHECKPOINT"))
		if shutdown := i.mod.ExportedFunction("pgl_shutdown"); shutdown != nil {
			if _, err := shutdown.Call(ctx); err != nil && !isCleanExit(err) {
				log.Debugf("pglite shutdown finished with guest error (expected on this build), err=%v", err)
			}
		}
		if !i.mod.IsClosed() {
			_ = i.mod.Close(ctx)
		}
	}
	i.mod = nil
	return i.runtime.Close(ctx)
}

// bootModuleLocked (re-)instantiates the wasm module and drives the PGlite
// lifecycle exports. The first boot initializes the cluster; subsequent
// boots resume it. Callers must hold i.mu (or be the only reference).
func (i *Instance) bootModuleLocked(ctx context.Context) error {
	modCfg := wazero.NewModuleConfig().
		WithFSConfig(wazero.NewFSConfig().
			WithDirMount(i.root, "/").
			// The guest reads entropy from /dev/urandom as a regular file
			// (e.g. pg_strong_random for gen_random_uuid). A static seed
			// file would replay the same bytes on every open, producing
			// duplicate UUIDs; this virtual device serves crypto/rand.
			WithFSMount(devFS{}, "/dev")).
		WithStdout(guestLogWriter("stdout")).
		WithStderr(guestLogWriter("stderr")).
		WithSysWalltime().
		WithSysNanotime().
		WithRandSource(rand.Reader).
		// argv mirrors `/tmp/pglite/bin/postgres --single postgres`.
		WithArgs("/tmp/pglite/bin/postgres", "--single", "postgres").
		WithEnv("ENVIRONMENT", "wasm32_wasi_preview1").
		WithEnv("PREFIX", "/tmp/pglite").
		WithEnv("PGDATA", "/tmp/pglite/base").
		WithEnv("PGSYSCONFDIR", "/tmp/pglite").
		WithEnv("PGUSER", user).
		WithEnv("PGDATABASE", database).
		WithEnv("MODE", "REACT").
		WithEnv("REPL", "N").
		WithEnv("TZ", "UTC").
		WithEnv("PGTZ", "UTC").
		WithEnv("PATH", "/tmp/pglite/bin").
		// The lifecycle exports are driven manually below.
		WithStartFunctions()

	mod, err := i.runtime.InstantiateModule(ctx, i.compiled, modCfg)
	if err != nil {
		return fmt.Errorf("failed instantiating pglite wasm module: %w", err)
	}

	// Lifecycle contract from the upstream reference hosts: _start performs
	// the embedded-environment setup (a clean proc_exit(0) is tolerated),
	// pgl_initdb creates or resumes the cluster, pgl_backend starts the
	// single-user backend, use_wire(1) switches to wire-protocol framing.
	if start := mod.ExportedFunction("_start"); start != nil {
		if _, err := start.Call(ctx); err != nil && !isCleanExit(err) {
			return fmt.Errorf("pglite _start failed: %w", err)
		}
	}
	if initdb := mod.ExportedFunction("pgl_initdb"); initdb != nil {
		if _, err := initdb.Call(ctx); err != nil {
			return fmt.Errorf("pglite initdb failed: %w", err)
		}
	}
	if backend := mod.ExportedFunction("pgl_backend"); backend != nil {
		if _, err := backend.Call(ctx); err != nil {
			return fmt.Errorf("pglite backend start failed: %w", err)
		}
	}
	if useSocketfile := mod.ExportedFunction("use_socketfile"); useSocketfile != nil {
		if _, err := useSocketfile.Call(ctx); err != nil {
			return fmt.Errorf("pglite use_socketfile failed: %w", err)
		}
	}
	interactiveOne := mod.ExportedFunction("interactive_one")
	if interactiveOne == nil {
		return fmt.Errorf("pglite wasm module is missing the interactive_one export")
	}
	useWire := mod.ExportedFunction("use_wire")
	if useWire == nil {
		return fmt.Errorf("pglite wasm module is missing the use_wire export")
	}
	if _, err := useWire.Call(ctx, 1); err != nil {
		return fmt.Errorf("pglite use_wire(1) failed: %w", err)
	}

	i.mod = mod
	i.interactiveOne = interactiveOne
	i.clearError = mod.ExportedFunction("clear_error")
	i.pglClosed = mod.ExportedFunction("pgl_closed")
	return nil
}

// isCleanExit reports whether err is a wasm proc_exit with code zero.
func isCleanExit(err error) bool {
	exitErr, ok := err.(*sys.ExitError)
	return ok && exitErr.ExitCode() == 0
}

// extractRuntime unpacks the embedded runtime archive into root and installs
// the uuid-ossp compatibility extension. It is idempotent: when the wasm
// module is already present the extraction is skipped.
func extractRuntime(root string) error {
	moduleHostPath := filepath.Join(root, guestPrefix, "bin", "pglite.wasi")
	if _, err := os.Stat(moduleHostPath); err == nil {
		return nil
	}

	gzr, err := gzip.NewReader(bytes.NewReader(runtimeArchive))
	if err != nil {
		return fmt.Errorf("failed reading embedded pglite runtime archive: %w", err)
	}
	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed reading pglite runtime archive entry: %w", err)
		}
		target := filepath.Join(root, filepath.Clean(hdr.Name))
		if rel, err := filepath.Rel(root, target); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("pglite runtime archive entry escapes extraction root: %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o700)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return fmt.Errorf("failed extracting %s: %w", hdr.Name, err)
			}
			if err := out.Close(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported pglite runtime archive entry type %v for %q", hdr.Typeflag, hdr.Name)
		}
	}

	if _, err := os.Stat(moduleHostPath); err != nil {
		return fmt.Errorf("embedded pglite runtime archive is missing %s", filepath.Join(guestPrefix, "bin", "pglite.wasi"))
	}
	return installUUIDOSSPShim(root)
}

// installUUIDOSSPShim writes a SQL-only uuid-ossp extension into the runtime
// share directory. The runtime archive ships only plpgsql, but hoop's first
// migration runs `CREATE EXTENSION IF NOT EXISTS "uuid-ossp"` and later
// migrations call uuid_generate_v4(). The shim provides uuid_generate_v4()
// on top of the core gen_random_uuid() (identical semantics: random v4
// UUID). No other uuid-ossp function is used by hoop migrations or code.
func installUUIDOSSPShim(root string) error {
	extDir := filepath.Join(root, guestPrefix, "share", "postgresql", "extension")
	control := `# uuid-ossp compatibility shim shipped by hoop (see gateway/pglite)
comment = 'compatibility shim: uuid_generate_v4() backed by core gen_random_uuid()'
default_version = '1.1'
relocatable = true
`
	script := `\echo Use "CREATE EXTENSION \"uuid-ossp\"" to load this file. \quit
CREATE FUNCTION uuid_generate_v4() RETURNS uuid
LANGUAGE sql VOLATILE PARALLEL SAFE
AS 'SELECT gen_random_uuid()';
`
	if err := os.WriteFile(filepath.Join(extDir, "uuid-ossp.control"), []byte(control), 0o600); err != nil {
		return fmt.Errorf("failed installing uuid-ossp shim control file: %w", err)
	}
	if err := os.WriteFile(filepath.Join(extDir, "uuid-ossp--1.1.sql"), []byte(script), 0o600); err != nil {
		return fmt.Errorf("failed installing uuid-ossp shim script: %w", err)
	}
	return nil
}

// devFS is a minimal virtual /dev for the guest: urandom (and random)
// backed by crypto/rand. Mounted read-only.
type devFS struct{}

func (devFS) Open(name string) (fs.File, error) {
	switch name {
	case ".":
		return devDir{}, nil
	case "urandom", "random":
		return &devRandomFile{name: name}, nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

type devDir struct{}

func (devDir) Stat() (fs.FileInfo, error)   { return devFileInfo{name: ".", dir: true}, nil }
func (devDir) Read([]byte) (int, error)     { return 0, fs.ErrInvalid }
func (devDir) Close() error                 { return nil }
func (devDir) ReadDir(int) ([]fs.DirEntry, error) {
	return []fs.DirEntry{
		fs.FileInfoToDirEntry(devFileInfo{name: "urandom"}),
		fs.FileInfoToDirEntry(devFileInfo{name: "random"}),
	}, nil
}

type devRandomFile struct{ name string }

func (f *devRandomFile) Stat() (fs.FileInfo, error) { return devFileInfo{name: f.name}, nil }
func (f *devRandomFile) Read(p []byte) (int, error) { return rand.Read(p) }
func (f *devRandomFile) Close() error               { return nil }

type devFileInfo struct {
	name string
	dir  bool
}

func (i devFileInfo) Name() string { return i.name }
func (i devFileInfo) Size() int64  { return 0 }
func (i devFileInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}
func (i devFileInfo) ModTime() time.Time { return time.Time{} }
func (i devFileInfo) IsDir() bool        { return i.dir }
func (i devFileInfo) Sys() any           { return nil }

// readPassword returns the superuser password provisioned in the runtime
// image. The cluster is initialized from the bundled "password" file.
func readPassword(root string) string {
	b, err := os.ReadFile(filepath.Join(root, guestPrefix, "password"))
	if err != nil {
		return defaultPassword
	}
	if pw := strings.TrimSpace(string(b)); pw != "" {
		return pw
	}
	return defaultPassword
}

// guestLogWriter forwards the guest's stdio output to the structured logger
// at debug level, line-buffered.
func guestLogWriter(stream string) io.Writer {
	return &lineLogger{prefix: "pglite " + stream}
}

type lineLogger struct {
	prefix string
	buf    []byte
}

func (l *lineLogger) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		idx := bytes.IndexByte(l.buf, '\n')
		if idx < 0 {
			break
		}
		if line := strings.TrimRight(string(l.buf[:idx]), "\r"); line != "" {
			log.Debugf("%s: %s", l.prefix, line)
		}
		l.buf = l.buf[idx+1:]
	}
	return len(p), nil
}
