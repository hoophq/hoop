//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd

package loginflow

import "syscall"

// applyReuseAddr is a no-op on platforms where SO_REUSEADDR either
// has the opposite semantics from what we want (Windows) or where
// the syscall surface differs in ways we don't currently need to
// chase. See listen.go for the full reasoning.
func applyReuseAddr(_ syscall.RawConn) error { return nil }
