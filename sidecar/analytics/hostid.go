package analytics

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Where a sidecar runs decides how much its identities can be trusted, so
// every event says which it is. The values are a fixed vocabulary.
const (
	RuntimeKubernetes = "kubernetes"
	RuntimeDocker     = "docker"
	RuntimeMacOS      = "macos"
	RuntimeWindows    = "windows"
	RuntimeLinux      = "linux" // a VM or bare metal, no container detected
)

// Runtime reports the environment the process runs in. Detection is a few
// file and environment reads, all stdlib, none of them failing loudly: a
// wrong guess costs one property's accuracy, never the relay.
//
// Kubernetes is checked first because a pod is also a container, and the
// pod is the fact a dashboard needs: its hostname is the pod name and its
// machine-id is the container's, so neither identifies a machine there.
func Runtime() string {
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return RuntimeKubernetes
	}
	if runtime.GOOS == "linux" && inContainer() {
		return RuntimeDocker
	}
	switch runtime.GOOS {
	case "darwin":
		return RuntimeMacOS
	case "windows":
		return RuntimeWindows
	}
	return RuntimeLinux
}

// inContainer reports the two marks Docker and containerd leave: the
// /.dockerenv file, and a cgroup path naming the runtime.
func inContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	cg, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	return bytes.Contains(cg, []byte("docker")) || bytes.Contains(cg, []byte("containerd"))
}

// HostIDEnvVar lets an operator name the machine a sidecar runs on, for the
// environments where the process cannot learn it: a Kubernetes pod sees its
// own hostname and its own machine-id, not the node's. The downward API
// supplies it in two lines (fieldRef spec.nodeName). Hashed like every
// other identity source.
const HostIDEnvVar = "HOOP_HOST_ID"

// HostID derives an identifier for the machine, so a dashboard can count
// sidecars per host. Highest precedence first:
//
//   - HostIDEnvVar, the operator's word.
//   - The OS machine id plus the hostname. /etc/machine-id on Linux,
//     IOPlatformUUID on macOS. The hostname is folded in because cloned
//     images that never regenerated their machine-id are common enough to
//     plan for, and two clones rarely share a hostname too.
//   - The hostname alone.
//   - "", when the host reports nothing; the property is then omitted.
//
// Trust it by Runtime: on RuntimeLinux and RuntimeMacOS an equal HostID is
// the same OS install. In a container the machine id and hostname are the
// container's, so without HostIDEnvVar every pod reads as its own host.
func HostID() string {
	if v := strings.TrimSpace(os.Getenv(HostIDEnvVar)); v != "" {
		return hashID("host-env", v)
	}
	host, _ := os.Hostname()
	mid := machineID()
	switch {
	case mid != "" && host != "":
		return hashID("machine", mid, host)
	case mid != "":
		return hashID("machine", mid)
	case host != "":
		return hashID("machine", host)
	}
	return ""
}

// machineID returns the OS's per-install identifier, or "" when the
// platform has none this code knows how to read.
func machineID() string {
	switch runtime.GOOS {
	case "linux":
		for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
			if b, err := os.ReadFile(p); err == nil {
				if id := strings.TrimSpace(string(b)); id != "" {
					return id
				}
			}
		}
	case "darwin":
		return darwinPlatformUUID()
	}
	return ""
}

// darwinPlatformUUID reads IOPlatformUUID from the IORegistry. One exec at
// startup, bounded, and any failure is "no machine id": ioreg is part of
// the base system, so a missing binary means a very unusual host.
func darwinPlatformUUID() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "IOPlatformUUID") {
			continue
		}
		// "IOPlatformUUID" = "XXXXXXXX-XXXX-..."
		if i := strings.LastIndex(line, "= \""); i >= 0 {
			return strings.Trim(line[i+3:], "\" ")
		}
	}
	return ""
}
