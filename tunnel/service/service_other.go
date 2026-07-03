//go:build !linux && !darwin && !windows

package service

// Catch-all for the BSDs, illumos, and anything else Go can cross-compile
// to. We don't currently ship binaries for those targets, but we keep
// the build green so a contributor poking at `tunnel/...` from a
// FreeBSD workstation does not face an `undefined: newPlatformManager`
// error from the package they have not touched.

func newPlatformManager() Manager { return &stubManager{platform: "unsupported"} }
