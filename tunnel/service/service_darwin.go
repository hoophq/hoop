//go:build darwin

package service

// macOS LaunchDaemon support is tracked as a follow-up to RD-217. The
// stub here lets the rest of the codebase compile on darwin and gives
// `hsh-tunneld install` an actionable error rather than a stack trace.
//
// The shape of the eventual implementation is sketched in service.go:
// write /Library/LaunchDaemons/dev.hoop.hsh-tunneld.plist, `launchctl
// load -w` it, and use `launchctl print` for status.

func newPlatformManager() Manager { return &stubManager{platform: "launchd"} }
