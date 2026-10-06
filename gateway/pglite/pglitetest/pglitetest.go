// Package pglitetest boots embedded databases with every migration applied,
// for tests.
//
// A fresh pglite.Start plus all migrations costs ~11s; booting a copy of an
// already-migrated data dir costs ~2s. StartMigrated migrates one template
// per test binary and gives each caller its own copy.
package pglitetest

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/pglite"
)

var (
	mainCalled bool

	templateOnce sync.Once
	templateDir  string
	templateErr  error
)

// Main runs the tests, then removes the template. Every package that calls
// StartMigrated must run it from TestMain:
//
//	func TestMain(m *testing.M) { os.Exit(pglitetest.Main(m)) }
func Main(m *testing.M) int {
	mainCalled = true
	code := m.Run()
	if templateDir != "" {
		_ = os.RemoveAll(templateDir)
	}
	return code
}

// StartMigrated boots an embedded database on the test's own copy of the
// migrated template and closes it when t ends.
func StartMigrated(t testing.TB) *pglite.Instance {
	t.Helper()
	// Without Main the ~110 MB template outlives the test binary.
	if !mainCalled {
		t.Fatal("pglitetest: call pglitetest.Main from TestMain")
	}
	templateOnce.Do(func() { templateDir, templateErr = buildTemplate() })
	if templateErr != nil {
		t.Fatalf("pglitetest: build migrated template: %v", templateErr)
	}

	dir := t.TempDir()
	if err := copyTree(templateDir, dir); err != nil {
		t.Fatalf("pglitetest: copy template: %v", err)
	}
	ctx := context.Background()
	inst, err := pglite.Start(ctx, dir)
	if err != nil {
		t.Fatalf("pglitetest: start embedded database: %v", err)
	}
	t.Cleanup(func() {
		if err := inst.Close(ctx); err != nil {
			t.Errorf("pglitetest: close embedded database: %v", err)
		}
	})
	return inst
}

func buildTemplate() (string, error) {
	dir, err := os.MkdirTemp("", "pglite-template-")
	if err != nil {
		return "", err
	}
	ctx := context.Background()
	inst, err := pglite.Start(ctx, dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	if err := modelsbootstrap.MigrateDB(inst.MigrateDSN(), ""); err != nil {
		_ = inst.Close(ctx)
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("migrations failed: %w", err)
	}
	// Close checkpoints, so a copy resumes without replaying much WAL.
	if err := inst.Close(ctx); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// copyTree copies src into dst with the same modes. The data dir holds only
// directories and regular files; anything else is an error, not a skip.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case d.Type().IsRegular():
			return copyFile(path, target, info.Mode().Perm())
		default:
			return fmt.Errorf("unsupported file type %v at %s", d.Type(), path)
		}
	})
}

func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
