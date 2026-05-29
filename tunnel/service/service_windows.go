//go:build windows

package service

// Windows Service support is tracked as a follow-up to RD-217. The
// future implementation will use golang.org/x/sys/windows/svc/mgr to
// register hsh-tunneld with the Service Control Manager and report
// status via QueryServiceStatusEx.

func newPlatformManager() Manager { return &stubManager{platform: "windows"} }
